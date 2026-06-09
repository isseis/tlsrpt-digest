package notify_test

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/isseis/tlsrpt-digest/internal/config"
	"github.com/isseis/tlsrpt-digest/internal/notify"
	storetestutil "github.com/isseis/tlsrpt-digest/internal/store/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// spyHandler records the slog.Record received by Handle().
type spyHandler struct {
	records []slog.Record
}

func (s *spyHandler) Enabled(_ context.Context, _ slog.Level) bool { return true }
func (s *spyHandler) Handle(_ context.Context, r slog.Record) error {
	s.records = append(s.records, r.Clone())
	return nil
}
func (s *spyHandler) WithAttrs(_ []slog.Attr) slog.Handler { return s }
func (s *spyHandler) WithGroup(_ string) slog.Handler      { return s }

func TestLogAlert_Level(t *testing.T) {
	var spy spyHandler
	require.NoError(t, notify.LogAlert(context.Background(), &spy, notify.Alert{
		OrganizationName: "example.com",
		PolicyType:       notify.PolicyTypeSTS,
		FailureCount:     3,
	}))
	require.Len(t, spy.records, 1)
	assert.Equal(t, slog.LevelWarn, spy.records[0].Level)
}

func TestLogSystemError_Level(t *testing.T) {
	var spy spyHandler
	require.NoError(t, notify.LogSystemError(context.Background(), &spy, notify.SystemError{
		Kind: notify.SystemErrorKindIMAPOperationFailed, Component: "imap",
	}))
	require.Len(t, spy.records, 1)
	assert.Equal(t, slog.LevelError, spy.records[0].Level)
}

func TestLogWarning_Level(t *testing.T) {
	var spy spyHandler
	require.NoError(t, notify.LogWarning(context.Background(), &spy, notify.Warning{
		Kind:        notify.WarningKindSizeMismatch,
		UID:         42,
		UIDValidity: 100,
		MessageID:   "<test@example.com>",
	}))
	require.Len(t, spy.records, 1)
	assert.Equal(t, slog.LevelWarn, spy.records[0].Level)
}

func TestLogWarning_TypedFieldsOnly(t *testing.T) {
	var spy spyHandler
	require.NoError(t, notify.LogWarning(context.Background(), &spy, notify.Warning{
		Kind:        notify.WarningKindParseFailure,
		UID:         7,
		UIDValidity: 99,
		MessageID:   "<msg@example.com>",
	}))
	require.Len(t, spy.records, 1)
	r := spy.records[0]
	assert.Equal(t, "fetch_warning", r.Message)

	var foundKind, foundUID, foundUIDValidity, foundMessageID bool
	r.Attrs(func(attr slog.Attr) bool {
		switch attr.Key {
		case "kind":
			foundKind = true
			assert.Equal(t, "parse_failure", attr.Value.String())
		case "uid":
			foundUID = true
			assert.Equal(t, uint64(7), attr.Value.Uint64())
		case "uidvalidity":
			foundUIDValidity = true
			assert.Equal(t, uint64(99), attr.Value.Uint64())
		case "message_id":
			foundMessageID = true
			assert.Equal(t, "<msg@example.com>", attr.Value.String())
		}
		return true
	})
	assert.True(t, foundKind)
	assert.True(t, foundUID)
	assert.True(t, foundUIDValidity)
	assert.True(t, foundMessageID)
}

func TestLogSummary_Level(t *testing.T) {
	var spy spyHandler
	require.NoError(t, notify.LogSummary(context.Background(), &spy, notify.Summary{
		Period: notify.DateRange{Start: time.Now(), End: time.Now()},
	}))
	require.Len(t, spy.records, 1)
	assert.Equal(t, slog.LevelInfo, spy.records[0].Level)
}

func TestLogSummary_OrganizationStats_Serialized(t *testing.T) {
	var spy spyHandler
	require.NoError(t, notify.LogSummary(context.Background(), &spy, notify.Summary{
		Period: notify.DateRange{Start: time.Now(), End: time.Now()},
		OrganizationStats: map[string]int64{
			"org-b": 20,
			"org-a": 10,
		},
		ReportCount: 2,
	}))
	require.Len(t, spy.records, 1)

	stats := summaryOrganizationStats(t, spy.records[0])
	assert.Equal(t, map[string]int64{"org-a": 10, "org-b": 20}, stats)
}

func TestLogSummary_OrganizationStats_SortedKeys(t *testing.T) {
	var spy spyHandler
	require.NoError(t, notify.LogSummary(context.Background(), &spy, notify.Summary{
		Period: notify.DateRange{Start: time.Now(), End: time.Now()},
		OrganizationStats: map[string]int64{
			"org-c": 30,
			"org-a": 10,
			"org-b": 20,
		},
	}))
	require.Len(t, spy.records, 1)

	group := summaryOrganizationStatsGroup(t, spy.records[0])
	keys := make([]string, 0, len(group))
	for _, attr := range group {
		keys = append(keys, attr.Key)
	}
	assert.Equal(t, []string{"org-a", "org-b", "org-c"}, keys)
}

func TestLogSummary_EmptyOrganizationStats(t *testing.T) {
	var spy spyHandler
	require.NotPanics(t, func() {
		require.NoError(t, notify.LogSummary(context.Background(), &spy, notify.Summary{
			Period:            notify.DateRange{Start: time.Now(), End: time.Now()},
			OrganizationStats: map[string]int64{},
		}))
	})
	require.Len(t, spy.records, 1)
}

func TestLogAlert_StructuredPayloadOnly(t *testing.T) {
	var spy spyHandler
	require.NoError(t, notify.LogAlert(context.Background(), &spy, notify.Alert{
		OrganizationName: "example.com",
		PolicyType:       notify.PolicyTypeSTS,
		FailureCount:     2,
		ReportID:         "report-id-1",
		FailureDetails: []notify.FailureDetail{
			{
				ResultType:          "certificate-expired",
				FailedSessionCount:  5,
				ReceivingMXHostname: "mx.example.com",
				FailureReasonCode:   "X509_V_ERR_CERT_HAS_EXPIRED",
			},
		},
	}))
	require.Len(t, spy.records, 1)
	r := spy.records[0]

	// Allowlist: only these top-level keys are permitted (AC-13).
	allowedTopLevel := map[string]bool{
		"organization_name":              true,
		"policy_type":                    true,
		"failure_count":                  true,
		"date_start":                     true,
		"date_end":                       true,
		"report_id":                      true,
		"failure_details":                true,
		"failure_details_total_count":    true,
		"failure_details_total_sessions": true,
	}
	// Allowlist: only these keys are permitted inside each failure_details child group.
	allowedDetailKeys := map[string]bool{
		"result_type":           true,
		"failed_session_count":  true,
		"receiving_mx_hostname": true,
		"failure_reason_code":   true,
	}

	r.Attrs(func(attr slog.Attr) bool {
		assert.True(t, allowedTopLevel[attr.Key],
			"unexpected top-level attr key %q in LogAlert record", attr.Key)
		if attr.Key == "failure_details" && attr.Value.Kind() == slog.KindGroup {
			for _, child := range attr.Value.Group() {
				require.Equal(t, slog.KindGroup, child.Value.Kind(),
					"failure_details child %q must be a group", child.Key)
				for _, field := range child.Value.Group() {
					assert.True(t, allowedDetailKeys[field.Key],
						"unexpected key %q in failure_details[%s]", field.Key, child.Key)
				}
			}
		}
		return true
	})
}

func TestSummaryFlow_Integration(t *testing.T) {
	start := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2024, 1, 7, 0, 0, 0, 0, time.UTC)

	st := fakeStoreWithReports(
		summaryReport("r1", "org-a", start.Add(time.Hour), 100, 0),
		summaryReport("r2", "org-b", start.Add(2*time.Hour), 200, 0),
	)

	summary, err := notify.GenerateSummary(context.Background(), st, start, end, nil)
	require.NoError(t, err)

	var recv []byte
	h, cleanup := buildCaptureHandler(t, notify.LevelModeExactInfo, &recv)
	defer cleanup()

	require.NoError(t, notify.LogSummary(context.Background(), h, summary))
	require.NoError(t, h.Flush(context.Background()))

	msg := decodeSlackMessage(t, recv)
	fields := flattenSlackFields(msg)
	assert.Equal(t, "100 successful sessions", fields["org-a"])
	assert.Equal(t, "200 successful sessions", fields["org-b"])
	assert.Contains(t, msg.Text, "2024-01-01")
	assert.Contains(t, msg.Text, "2024-01-07")
	assert.Equal(t, "run-001", fields["Run ID"])
}

func TestSummaryFlow_Integration_NoReports(t *testing.T) {
	start := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2024, 1, 7, 0, 0, 0, 0, time.UTC)

	st := storetestutil.NewFakeStore()
	summary, err := notify.GenerateSummary(context.Background(), st, start, end, nil)
	require.NoError(t, err)

	var recv []byte
	h, cleanup := buildCaptureHandler(t, notify.LevelModeExactInfo, &recv)
	defer cleanup()

	require.NoError(t, notify.LogSummary(context.Background(), h, summary))
	require.NoError(t, h.Flush(context.Background()))

	msg := decodeSlackMessage(t, recv)
	require.Len(t, msg.Attachments, 1)
	require.Len(t, msg.Attachments[0].Fields, 1)
	assert.Equal(t, "Run ID", msg.Attachments[0].Fields[0].Title)
	assert.Equal(t, "run-001", msg.Attachments[0].Fields[0].Value)
}

func TestSummaryFlow_FlushError(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	h, err := notify.NewSlackHandler(notify.SlackHandlerOptions{
		WebhookURL:    config.Secret(srv.URL + "/webhook"),
		AllowedHost:   "127.0.0.1",
		RunID:         "test",
		LevelMode:     notify.LevelModeExactInfo,
		HTTPClient:    srv.Client(),
		BackoffConfig: notify.DefaultBackoffConfig,
	})
	require.NoError(t, err)

	summary := notify.Summary{
		Period:            notify.DateRange{Start: time.Now(), End: time.Now()},
		OrganizationStats: map[string]int64{"org-a": 10},
		ReportCount:       1,
	}

	require.NoError(t, notify.LogSummary(context.Background(), h, summary))
	flushErr := h.Flush(context.Background())
	require.Error(t, flushErr)
	_, ok := errors.AsType[*notify.SlackClientError](flushErr)
	assert.True(t, ok, "Flush must propagate SlackClientError to the caller")
}

func summaryOrganizationStats(t *testing.T, record slog.Record) map[string]int64 {
	t.Helper()
	stats := make(map[string]int64)
	for _, attr := range summaryOrganizationStatsGroup(t, record) {
		stats[attr.Key] = attr.Value.Int64()
	}
	return stats
}

func summaryOrganizationStatsGroup(t *testing.T, record slog.Record) []slog.Attr {
	t.Helper()
	var group []slog.Attr
	record.Attrs(func(attr slog.Attr) bool {
		if attr.Key == "organization_stats" {
			require.Equal(t, slog.KindGroup, attr.Value.Kind())
			group = attr.Value.Group()
		}
		return true
	})
	require.NotNil(t, group, "organization_stats group not found")
	return group
}
