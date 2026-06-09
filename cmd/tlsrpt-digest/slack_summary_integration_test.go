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
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/isseis/tlsrpt-digest/internal/config"
	"github.com/isseis/tlsrpt-digest/internal/mailparse"
	"github.com/isseis/tlsrpt-digest/internal/notify"
	"github.com/isseis/tlsrpt-digest/internal/store"
	storetestutil "github.com/isseis/tlsrpt-digest/internal/store/testutil"
	"github.com/isseis/tlsrpt-digest/internal/tlsrpt"
)

// loadSlackSummaryTestEnv skips the test when either Slack webhook URL env var
// is unset or empty, then returns (successURL, errorURL).
func loadSlackSummaryTestEnv(t *testing.T) (successURL, errorURL string) {
	t.Helper()
	if missing := missingSlackSummaryEnv(nil); len(missing) > 0 {
		t.Skip("Slack summary env not configured: " + strings.Join(missing, ", "))
	}
	return os.Getenv(notify.EnvSlackWebhookURLSuccess), os.Getenv(notify.EnvSlackWebhookURLError)
}

func TestSlackSummary_Summary_Integration(t *testing.T) {
	// 60s gives comfortable headroom for the full retry sequence;
	// update this if the notifier's retry parameters change.
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	successURL, errorURL := loadSlackSummaryTestEnv(t)

	// runID is unique per call so repeated runs produce distinguishable Slack messages.
	runID := ulid.Make().String()

	emlPaths := []string{
		filepath.Join("..", "..", "testdata", "tlsrpt_success_google_1.eml"),
		filepath.Join("..", "..", "testdata", "tlsrpt_success_google_2.eml"),
		filepath.Join("..", "..", "testdata", "tlsrpt_success_microsoft.eml"),
	}

	var inputs []store.ReportInput
	for _, emlPath := range emlPaths {
		raw, err := os.ReadFile(emlPath) //nolint:gosec // G304: path is a hardcoded testdata literal
		require.NoError(t, err)

		msg, err := mail.ReadMessage(bytes.NewReader(raw))
		require.NoError(t, err)

		atts, err := mailparse.ExtractAttachments(msg, 10<<20)
		require.NoError(t, err)

		var report *tlsrpt.Report
		for _, att := range atts {
			r, parseErr := parseTLSRPTAttachment(att)
			require.NoError(t, parseErr)
			if r != nil {
				report = r
				inputs = append(inputs, store.ReportInput{Report: *r})
				break
			}
		}
		require.NotNil(t, report, "no TLS-RPT attachment found in %s", emlPath)
		assert.False(t, report.HasFailure(), "expected zero failure sessions in %s", emlPath)
	}

	fakeStore := storetestutil.NewFakeStore()
	require.NoError(t, fakeStore.SaveReports(inputs))
	require.Len(t, fakeStore.Reports, 3)

	start := time.Date(2026, 5, 11, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 5, 14, 0, 0, 0, 0, time.UTC)

	summary, err := notify.GenerateSummary(ctx, fakeStore, start, end, nil)
	require.NoError(t, err)

	require.Equal(t, int64(3), summary.ReportCount)
	assert.Contains(t, summary.OrganizationStats, "Google Inc.")
	assert.Contains(t, summary.OrganizationStats, "Microsoft Corporation")
	assert.Equal(t, int64(5), summary.OrganizationStats["Google Inc."])
	assert.Equal(t, int64(2), summary.OrganizationStats["Microsoft Corporation"])

	cfg := &config.Config{}
	u, err := url.Parse(successURL)
	require.NoError(t, err)
	cfg.Notify.Slack.AllowedHost = u.Hostname()

	notifier, err := setupNotifyHandlers(config.Secret(successURL), config.Secret(errorURL), cfg, runID, false)
	require.NoError(t, err)

	require.NoError(t, notifier.LogSummary(ctx, summary))
	require.NoError(t, notifier.Flush(ctx))
}
