//go:build test

package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/isseis/tlsrpt-digest/internal/notify"
	"github.com/isseis/tlsrpt-digest/internal/tlsrpt"
)

// TestLogAlerts_WarnOnError verifies that logAlerts emits slog.Warn (not
// slog.Error) when LogAlert returns a non-nil error.
func TestLogAlerts_WarnOnError(t *testing.T) {
	buf := captureSlog(t)

	report := &tlsrpt.Report{
		OrganizationName: "example.com",
		DateRange:        tlsrpt.DateRange{},
		Policies: []tlsrpt.PolicyRecord{
			{
				Policy: tlsrpt.Policy{PolicyType: "sts"},
				Summary: tlsrpt.Summary{
					TotalFailureSessionCount: 1,
				},
			},
		},
	}

	spy := &SpyNotificationSink{LogError: errors.New("slack error")}
	logAlerts(context.Background(), spy, report, "fetch")

	out := buf.String()
	require.NotEmpty(t, out, "expected log output")
	assert.True(t, strings.Contains(out, "level=WARN"), "expected WARN level, got: %s", out)
	assert.True(t, strings.Contains(out, "error="), "expected error field, got: %s", out)
	assert.False(t, strings.Contains(out, "level=ERROR"), "unexpected ERROR level, got: %s", out)
}

// TestLogAlerts_NoLogWhenNoError verifies that logAlerts emits no log output
// when LogAlert succeeds.
func TestLogAlerts_NoLogWhenNoError(t *testing.T) {
	buf := captureSlog(t)

	report := &tlsrpt.Report{
		Policies: []tlsrpt.PolicyRecord{
			{
				Policy:  tlsrpt.Policy{PolicyType: "sts"},
				Summary: tlsrpt.Summary{TotalFailureSessionCount: 1},
			},
		},
	}

	spy := &SpyNotificationSink{} // no error
	logAlerts(context.Background(), spy, report, "fetch")

	assert.Empty(t, buf.String(), "expected no log output on success")
}

// TestLogWarn_WarnOnError verifies that logWarn emits slog.Warn (not
// slog.Error) when LogWarning returns a non-nil error.
func TestLogWarn_WarnOnError(t *testing.T) {
	buf := captureSlog(t)

	spy := &SpyNotificationSink{LogError: errors.New("slack error")}
	logWarn(context.Background(), spy, notify.WarningKindSizeMismatch, 42, 100, "msg-id", "fetch")

	out := buf.String()
	require.NotEmpty(t, out, "expected log output")
	assert.True(t, strings.Contains(out, "level=WARN"), "expected WARN level, got: %s", out)
	assert.True(t, strings.Contains(out, "error="), "expected error field, got: %s", out)
	assert.False(t, strings.Contains(out, "level=ERROR"), "unexpected ERROR level, got: %s", out)
}

// TestLogWarn_NoLogWhenNoError verifies that logWarn emits no log output when
// LogWarning succeeds.
func TestLogWarn_NoLogWhenNoError(t *testing.T) {
	buf := captureSlog(t)

	spy := &SpyNotificationSink{} // no error
	logWarn(context.Background(), spy, notify.WarningKindSizeMismatch, 42, 100, "msg-id", "fetch")

	assert.Empty(t, buf.String(), "expected no log output on success")
}
