//go:build test && slack_notify

package main

import (
	"bytes"
	"context"
	"net/mail"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/stretchr/testify/require"

	"github.com/isseis/tlsrpt-digest/internal/config"
	"github.com/isseis/tlsrpt-digest/internal/mailparse"
	"github.com/isseis/tlsrpt-digest/internal/tlsrpt"
)

// loadSlackNotifyTestEnv skips the test when TLSRPT_SLACK_WEBHOOK_URL_ERROR is
// unset or empty, then returns the webhook URL string.
func loadSlackNotifyTestEnv(t *testing.T) string {
	t.Helper()
	if missing := missingSlackNotifyEnv(nil); len(missing) > 0 {
		t.Skip("Slack notify env not configured: " + strings.Join(missing, ", "))
	}
	return os.Getenv(slackNotifyWebhookEnvKey)
}

func TestSlackNotify_FailureAlert_Integration(t *testing.T) {
	// 60s gives comfortable headroom for the full retry sequence;
	// update this if the notifier's retry parameters change.
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	webhookURL := loadSlackNotifyTestEnv(t)

	// runID is unique per call so repeated runs produce distinguishable Slack messages.
	runID := ulid.Make().String()

	emlPath := filepath.Join("..", "..", "testdata", "tlsrpt_failure.eml")
	raw, err := os.ReadFile(emlPath) //nolint:gosec // G304: path is a hardcoded testdata literal
	require.NoError(t, err, "testdata/tlsrpt_failure.eml must be readable")

	msg, err := mail.ReadMessage(bytes.NewReader(raw))
	require.NoError(t, err)

	atts, err := mailparse.ExtractAttachments(msg, 10<<20)
	require.NoError(t, err)

	var report *tlsrpt.Report
	for _, att := range atts {
		// parseTLSRPTAttachment returns (nil, nil) for non-TLSRPT attachments;
		// a non-nil error here means a real TLSRPT parse failure.
		r, parseErr := parseTLSRPTAttachment(att)
		require.NoError(t, parseErr)
		if r != nil {
			report = r
			break
		}
	}
	require.NotNil(t, report, "no TLS-RPT attachment found in testdata/tlsrpt_failure.eml")
	require.True(t, report.HasFailure(), "testdata/tlsrpt_failure.eml must contain at least one failing policy")

	// Assert expected field values from the known testdata report.
	require.Equal(t, "Google Inc.", report.OrganizationName)
	var failingPolicies []tlsrpt.PolicyRecord
	for _, p := range report.Policies {
		if p.Summary.TotalFailureSessionCount > 0 {
			failingPolicies = append(failingPolicies, p)
		}
	}
	require.Len(t, failingPolicies, 1, "expected exactly 1 failing policy")
	require.Equal(t, "sts", failingPolicies[0].Policy.PolicyType)
	require.Equal(t, int64(2), failingPolicies[0].Summary.TotalFailureSessionCount)
	require.Equal(t, time.Date(2026, 2, 8, 0, 0, 0, 0, time.UTC), report.DateRange.StartDatetime.UTC())
	require.Equal(t, time.Date(2026, 2, 9, 0, 0, 0, 0, time.UTC), report.DateRange.EndDatetime.UTC())

	// Build notification config from the webhook URL host for allowed-host validation.
	u, err := url.Parse(webhookURL)
	require.NoError(t, err)
	cfg := &config.Config{}
	cfg.Notify.Slack.AllowedHost = u.Hostname()

	sink, err := setupNotifyHandlers(config.Secret(""), config.Secret(webhookURL), cfg, runID, false)
	require.NoError(t, err)

	logAlerts(ctx, sink, report, "slack-notify-test")

	require.NoError(t, sink.Flush(ctx))
}
