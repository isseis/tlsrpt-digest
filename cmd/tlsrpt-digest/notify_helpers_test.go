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

// TestLogAlerts_MapsPublicFailureFields verifies that logAlerts maps ReportID and
// only the public 4 FailureDetail fields; IP and additional-information are excluded.
func TestLogAlerts_MapsPublicFailureFields(t *testing.T) {
	report := &tlsrpt.Report{
		ReportID:         "report-xyz",
		OrganizationName: "example.com",
		DateRange:        tlsrpt.DateRange{},
		Policies: []tlsrpt.PolicyRecord{
			{
				Policy: tlsrpt.Policy{PolicyType: "sts"},
				Summary: tlsrpt.Summary{
					TotalFailureSessionCount: 10,
				},
				FailureDetails: []tlsrpt.FailureDetail{
					{
						ResultType:            "certificate-expired",
						FailedSessionCount:    7,
						ReceivingMXHostname:   "mx.example.com",
						FailureReasonCode:     "X509_V_ERR_CERT_HAS_EXPIRED",
						SendingMTAIP:          "192.0.2.1",     // must NOT be copied
						ReceivingIP:           "198.51.100.1",  // must NOT be copied
						AdditionalInformation: "some raw text", // must NOT be copied
					},
				},
			},
		},
	}

	spy := &SpyNotificationSink{}
	logAlerts(context.Background(), spy, report, "test")

	require.Len(t, spy.Alerts, 1)
	a := spy.Alerts[0]

	assert.Equal(t, "report-xyz", a.ReportID)
	require.Len(t, a.FailureDetails, 1)
	fd := a.FailureDetails[0]
	assert.Equal(t, "certificate-expired", fd.ResultType)
	assert.Equal(t, int64(7), fd.FailedSessionCount)
	assert.Equal(t, "mx.example.com", fd.ReceivingMXHostname)
	assert.Equal(t, "X509_V_ERR_CERT_HAS_EXPIRED", fd.FailureReasonCode)
	// notify.FailureDetail has no IP or AdditionalInformation fields - structural exclusion.
}
