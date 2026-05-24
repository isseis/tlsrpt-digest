package notify_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/isseis/tlsrpt-digest/internal/config"
	"github.com/isseis/tlsrpt-digest/internal/notify"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testPeriod is a fixed DateRange used across tests that do not assert on
// period values, keeping tests deterministic.
var testPeriod = notify.DateRange{
	Start: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
	End:   time.Date(2024, 1, 7, 0, 0, 0, 0, time.UTC),
}

func sampleAlert() notify.Alert {
	return notify.Alert{
		OrganizationName: "example.com",
		PolicyType:       notify.PolicyTypeSTS,
		FailureCount:     5,
		DateRange: notify.DateRange{
			Start: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
			End:   time.Date(2024, 1, 7, 0, 0, 0, 0, time.UTC),
		},
	}
}

// buildCaptureHandler creates a SlackHandler that writes the POST body to *recv.
func buildCaptureHandler(t *testing.T, levelMode notify.LevelMode, recv *[]byte) (*notify.SlackHandler, func()) {
	t.Helper()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*recv, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	opts := notify.SlackHandlerOptions{
		WebhookURL:    config.Secret(srv.URL + "/webhook"),
		AllowedHost:   "127.0.0.1",
		RunID:         "run-001",
		LevelMode:     levelMode,
		HTTPClient:    srv.Client(),
		BackoffConfig: notify.DefaultBackoffConfig,
	}
	h, err := notify.NewSlackHandler(opts)
	require.NoError(t, err)
	return h, srv.Close
}

// flushAlert flushes one Alert and returns the raw request body sent to Slack.
func flushAlert(t *testing.T, alert notify.Alert) []byte {
	t.Helper()
	var recv []byte
	h, cleanup := buildCaptureHandler(t, notify.LevelModeWarnAndAbove, &recv)
	defer cleanup()
	require.NoError(t, notify.LogAlert(context.Background(), h, alert))
	require.NoError(t, h.Flush(context.Background()))
	return recv
}

func flushSummary(t *testing.T, summary notify.Summary) []byte {
	t.Helper()
	var recv []byte
	h, cleanup := buildCaptureHandler(t, notify.LevelModeExactInfo, &recv)
	defer cleanup()
	require.NoError(t, notify.LogSummary(context.Background(), h, summary))
	require.NoError(t, h.Flush(context.Background()))
	return recv
}

func TestFormatAlerts_Fields(t *testing.T) {
	body := string(flushAlert(t, sampleAlert()))
	assert.Contains(t, body, "example.com")
	assert.Contains(t, body, "sts")
	assert.Contains(t, body, "5")
	assert.Contains(t, body, "2024-01-01")
	assert.Contains(t, body, "2024-01-07")
}

func TestFormatAlerts_RunID(t *testing.T) {
	assert.Contains(t, string(flushAlert(t, sampleAlert())), "run-001")
}

func TestFormatAlerts_TitleOrgCount(t *testing.T) {
	assert.Contains(t, string(flushAlert(t, sampleAlert())), "1 organizations affected")
}

// TestFormatAlerts_TitleOrgCountDedup verifies that duplicate OrganizationName
// values are counted only once in the title (AC-20e).
func TestFormatAlerts_TitleOrgCountDedup(t *testing.T) {
	var recv []byte
	h, cleanup := buildCaptureHandler(t, notify.LevelModeWarnAndAbove, &recv)
	defer cleanup()

	// Two alerts for the same org with different policies.
	for _, pt := range []notify.PolicyType{notify.PolicyTypeSTS, notify.PolicyTypeTLSA} {
		require.NoError(t, notify.LogAlert(context.Background(), h, notify.Alert{
			OrganizationName: "example.com",
			PolicyType:       pt,
			FailureCount:     1,
		}))
	}
	require.NoError(t, h.Flush(context.Background()))

	// One unique org, so title must say "1 organizations affected".
	assert.Contains(t, string(recv), "1 organizations affected")
}

func TestFormatAlerts_Color(t *testing.T) {
	body := string(flushAlert(t, sampleAlert()))
	assert.Contains(t, body, "warning")
	assert.Contains(t, body, "⚠️")
}

func TestTruncateText_ExactLimit(t *testing.T) {
	exact := strings.Repeat("a", 4000)
	result := notify.TruncateText(exact, 4000)
	assert.Equal(t, exact, result, "exactly at limit: no truncation")

	over := strings.Repeat("a", 4001)
	truncated := notify.TruncateText(over, 4000)
	assert.True(t, strings.HasSuffix(truncated, "..."))
	assert.LessOrEqual(t, utf8.RuneCountInString(truncated), 4000)
}

func TestTruncateText_MultibyteRune(t *testing.T) {
	s := strings.Repeat("あ", 4001)
	result := notify.TruncateText(s, 4000)
	assert.True(t, strings.HasSuffix(result, "..."))
	assert.LessOrEqual(t, utf8.RuneCountInString(result), 4000)
	assert.True(t, utf8.ValidString(result), "result must be valid UTF-8")
}

func TestTruncateField_ExactLimit(t *testing.T) {
	exact := strings.Repeat("b", 1000)
	assert.Equal(t, exact, notify.TruncateText(exact, 1000))

	over := strings.Repeat("b", 1001)
	truncated := notify.TruncateText(over, 1000)
	assert.True(t, strings.HasSuffix(truncated, "..."))
	assert.LessOrEqual(t, utf8.RuneCountInString(truncated), 1000)
}

func TestFormatAlerts_NoTruncation(t *testing.T) {
	// DebugLogger must receive the untruncated payload.
	var debugBuf strings.Builder
	debugLogger := slog.New(slog.NewTextHandler(&debugBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	var recv []byte
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		recv, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	longName := strings.Repeat("x", 5000)
	opts := notify.SlackHandlerOptions{
		WebhookURL:    config.Secret(srv.URL + "/webhook"),
		AllowedHost:   "127.0.0.1",
		RunID:         "run-001",
		LevelMode:     notify.LevelModeWarnAndAbove,
		HTTPClient:    srv.Client(),
		BackoffConfig: notify.DefaultBackoffConfig,
		DebugLogger:   debugLogger,
	}
	h, err := notify.NewSlackHandler(opts)
	require.NoError(t, err)

	require.NoError(t, notify.LogAlert(context.Background(), h, notify.Alert{
		OrganizationName: longName,
		PolicyType:       notify.PolicyTypeSTS,
		FailureCount:     1,
	}))
	require.NoError(t, h.Flush(context.Background()))

	// Debug log contains full name; Slack payload does not.
	assert.Contains(t, debugBuf.String(), longName)
	assert.NotContains(t, string(recv), longName, "Slack payload should be truncated")
}

func TestFormatAlerts_AttachmentFields(t *testing.T) {
	body := string(flushAlert(t, sampleAlert()))
	assert.Contains(t, body, `"title"`)
	assert.Contains(t, body, `"value"`)
}

func TestFormatSystemError_Title(t *testing.T) {
	var recv []byte
	h, cleanup := buildCaptureHandler(t, notify.LevelModeWarnAndAbove, &recv)
	defer cleanup()
	require.NoError(t, notify.LogSystemError(context.Background(), h, notify.SystemError{
		Kind: notify.SystemErrorKindIMAPAuthFailed, Component: "imap",
	}))
	require.NoError(t, h.Flush(context.Background()))
	assert.Contains(t, string(recv), "imap_auth_failed")
}

func TestFormatSystemError_Fields(t *testing.T) {
	var recv []byte
	h, cleanup := buildCaptureHandler(t, notify.LevelModeWarnAndAbove, &recv)
	defer cleanup()
	require.NoError(t, notify.LogSystemError(context.Background(), h, notify.SystemError{
		Kind: notify.SystemErrorKindStoreCorruption, Component: "storage",
	}))
	require.NoError(t, h.Flush(context.Background()))
	body := string(recv)
	assert.Contains(t, body, "store_corruption")
	assert.Contains(t, body, "storage")
	assert.Contains(t, body, "run-001")
}

func TestFormatSystemError_Color(t *testing.T) {
	var recv []byte
	h, cleanup := buildCaptureHandler(t, notify.LevelModeWarnAndAbove, &recv)
	defer cleanup()
	require.NoError(t, notify.LogSystemError(context.Background(), h, notify.SystemError{
		Kind: notify.SystemErrorKindLockHeld, Component: "test",
	}))
	require.NoError(t, h.Flush(context.Background()))
	body := string(recv)
	assert.Contains(t, body, "danger")
	assert.Contains(t, body, "🚨")
}

func TestFormatSummary_Color(t *testing.T) {
	var recv []byte
	h, cleanup := buildCaptureHandler(t, notify.LevelModeExactInfo, &recv)
	defer cleanup()
	require.NoError(t, notify.LogSummary(context.Background(), h, notify.Summary{
		Period: testPeriod,
	}))
	require.NoError(t, h.Flush(context.Background()))
	body := string(recv)
	assert.Contains(t, body, "good")
	assert.Contains(t, body, "✅")
}

func TestFormatSummary_Fields(t *testing.T) {
	var recv []byte
	h, cleanup := buildCaptureHandler(t, notify.LevelModeExactInfo, &recv)
	defer cleanup()
	require.NoError(t, notify.LogSummary(context.Background(), h, notify.Summary{
		Period: notify.DateRange{
			Start: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
			End:   time.Date(2024, 1, 7, 0, 0, 0, 0, time.UTC),
		},
		OrganizationStats: map[string]int64{"org-a": 10, "org-b": 20, "org-c": 30, "org-d": 40},
		ReportCount:       7,
	}))
	require.NoError(t, h.Flush(context.Background()))
	body := string(recv)
	assert.Contains(t, body, "4")
	assert.Contains(t, body, "7")
	assert.Contains(t, body, "run-001")
	assert.Contains(t, body, "2024-01-01")
}

func TestFormatSummary_UsesProvidedPeriod(t *testing.T) {
	var recv []byte
	h, cleanup := buildCaptureHandler(t, notify.LevelModeExactInfo, &recv)
	defer cleanup()
	require.NoError(t, notify.LogSummary(context.Background(), h, notify.Summary{
		Period: notify.DateRange{
			Start: time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC),
			End:   time.Date(2025, 6, 14, 0, 0, 0, 0, time.UTC),
		},
	}))
	require.NoError(t, h.Flush(context.Background()))
	body := string(recv)
	assert.Contains(t, body, "2025-06-01")
	assert.Contains(t, body, "2025-06-14")
}

func TestFormatSummary_OrganizationStatsFromLogSummary(t *testing.T) {
	msg := decodeSlackMessage(t, flushSummary(t, notify.Summary{
		Period: notify.DateRange{
			Start: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
			End:   time.Date(2024, 1, 7, 0, 0, 0, 0, time.UTC),
		},
		OrganizationStats: map[string]int64{"org-a": 10, "org-b": 20},
		ReportCount:       2,
	}))

	fields := flattenSlackFields(msg)
	assert.Equal(t, "10 successful sessions", fields["org-a"])
	assert.Equal(t, "20 successful sessions", fields["org-b"])
}

func TestFormatSummary_PeriodInText(t *testing.T) {
	msg := decodeSlackMessage(t, flushSummary(t, notify.Summary{
		Period: notify.DateRange{
			Start: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
			End:   time.Date(2024, 1, 7, 0, 0, 0, 0, time.UTC),
		},
	}))

	assert.Contains(t, msg.Text, "2024-01-01")
	assert.Contains(t, msg.Text, "2024-01-07")
}

func TestFormatSummary_OrgStatsInAttachment(t *testing.T) {
	msg := decodeSlackMessage(t, flushSummary(t, notify.Summary{
		Period:            testPeriod,
		OrganizationStats: map[string]int64{"org-a": 10, "org-b": 20},
	}))

	fields := flattenSlackFields(msg)
	assert.Equal(t, "10 successful sessions", fields["org-a"])
	assert.Equal(t, "20 successful sessions", fields["org-b"])
}

func TestFormatSummary_OrganizationStatsSortedInAttachment(t *testing.T) {
	msg := decodeSlackMessage(t, flushSummary(t, notify.Summary{
		Period: testPeriod,
		OrganizationStats: map[string]int64{
			"org-b": 20,
			"org-a": 10,
		},
	}))

	require.Len(t, msg.Attachments, 1)
	require.GreaterOrEqual(t, len(msg.Attachments[0].Fields), 2)
	assert.Equal(t, "org-a", msg.Attachments[0].Fields[0].Title)
	assert.Equal(t, "10 successful sessions", msg.Attachments[0].Fields[0].Value)
	assert.Equal(t, "org-b", msg.Attachments[0].Fields[1].Title)
	assert.Equal(t, "20 successful sessions", msg.Attachments[0].Fields[1].Value)
}

func TestFormatSummary_ReportCountInText(t *testing.T) {
	msg := decodeSlackMessage(t, flushSummary(t, notify.Summary{
		Period:            testPeriod,
		OrganizationStats: map[string]int64{"org-a": 10},
		ReportCount:       7,
	}))

	assert.Contains(t, msg.Text, "Reports: 7")
	assert.Contains(t, msg.Text, "Organizations: 1")
}

func TestFormatSummary_SingleAttachmentUpTo9Orgs(t *testing.T) {
	msg := decodeSlackMessage(t, flushSummary(t, notify.Summary{
		Period:            testPeriod,
		OrganizationStats: summaryOrgStats(9),
	}))

	require.Len(t, msg.Attachments, 1)
	assert.Len(t, msg.Attachments[0].Fields, 10)
	assert.Equal(t, "run-001", msg.Attachments[0].Fields[9].Value)
}

func TestFormatSummary_ChunkingOver9Orgs(t *testing.T) {
	msg := decodeSlackMessage(t, flushSummary(t, notify.Summary{
		Period:            testPeriod,
		OrganizationStats: summaryOrgStats(10),
	}))

	require.Len(t, msg.Attachments, 2)
	assert.Len(t, msg.Attachments[0].Fields, 9)
	assert.NotContains(t, flattenFields(msg.Attachments[0].Fields), "Run ID")
	assert.Len(t, msg.Attachments[1].Fields, 2)
	assert.Equal(t, "Run ID", msg.Attachments[1].Fields[1].Title)
	assert.Equal(t, "run-001", msg.Attachments[1].Fields[1].Value)
}

func TestFormatSummary_EmptyOrganizationStats(t *testing.T) {
	msg := decodeSlackMessage(t, flushSummary(t, notify.Summary{
		Period:            testPeriod,
		OrganizationStats: map[string]int64{},
	}))

	require.Len(t, msg.Attachments, 1)
	require.Len(t, msg.Attachments[0].Fields, 1)
	assert.Equal(t, "Run ID", msg.Attachments[0].Fields[0].Title)
	assert.Equal(t, "run-001", msg.Attachments[0].Fields[0].Value)
	assert.Contains(t, msg.Text, "Organizations: 0")
}

func TestFormatAlerts_NoPolicyFound(t *testing.T) {
	a := sampleAlert()
	a.PolicyType = notify.PolicyTypeNoPolicyFound
	assert.Contains(t, string(flushAlert(t, a)), "no-policy-found")
}

func TestFormatAlerts_PolicyTypeUnknown(t *testing.T) {
	a := sampleAlert()
	a.PolicyType = notify.PolicyTypeUnknown
	body := string(flushAlert(t, a))
	// The unknown placeholder must appear in the rendered message so operators
	// can spot reports that omit a policy-type value.
	assert.Contains(t, body, "(unknown)")
}

// TestExtract_UnknownAttrKeyLogged verifies that an attr key not recognised by
// the extract functions produces a Warn entry in the DebugLogger.
// This catches helper/format mismatches early (e.g. a key renamed in LogAlert
// but not updated in extractAlert).
func TestExtractSummary_MalformedOrganizationStatsLogged(t *testing.T) {
	var debugBuf strings.Builder
	debugLogger := slog.New(slog.NewTextHandler(&debugBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	var recv []byte
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		recv, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	opts := notify.SlackHandlerOptions{
		WebhookURL:    config.Secret(srv.URL + "/webhook"),
		AllowedHost:   "127.0.0.1",
		RunID:         "run-malformed",
		LevelMode:     notify.LevelModeExactInfo,
		HTTPClient:    srv.Client(),
		BackoffConfig: notify.DefaultBackoffConfig,
		DebugLogger:   debugLogger,
	}
	h, err := notify.NewSlackHandler(opts)
	require.NoError(t, err)

	r := slog.NewRecord(time.Now(), slog.LevelInfo, "periodic_summary", 0)
	r.AddAttrs(
		slog.Any("period_start", time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)),
		slog.Any("period_end", time.Date(2024, 1, 7, 0, 0, 0, 0, time.UTC)),
		slog.Int64("report_count", 1),
		slog.Group("organization_stats",
			slog.String("org-a", "not-an-int"),
			slog.Int64("org-b", 20),
		),
	)
	require.NoError(t, h.Handle(context.Background(), r))
	require.NotPanics(t, func() {
		require.NoError(t, h.Flush(context.Background()))
	})

	msg := decodeSlackMessage(t, recv)
	fields := flattenSlackFields(msg)
	assert.NotContains(t, fields, "org-a")
	assert.Equal(t, "20 successful sessions", fields["org-b"])
	assert.Contains(t, debugBuf.String(), "organization_stats.org-a")
	assert.NotContains(t, debugBuf.String(), "not-an-int")
}

func TestExtract_UnknownAttrKeyLogged(t *testing.T) {
	var debugBuf strings.Builder
	debugLogger := slog.New(slog.NewTextHandler(&debugBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	var recv []byte
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		recv, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	opts := notify.SlackHandlerOptions{
		WebhookURL:    config.Secret(srv.URL + "/webhook"),
		AllowedHost:   "127.0.0.1",
		RunID:         "run-unk",
		LevelMode:     notify.LevelModeWarnAndAbove,
		HTTPClient:    srv.Client(),
		BackoffConfig: notify.DefaultBackoffConfig,
		DebugLogger:   debugLogger,
	}
	h, err := notify.NewSlackHandler(opts)
	require.NoError(t, err)

	// Inject a record with an attr key that extractAlert does not recognise.
	r := slog.NewRecord(time.Now(), slog.LevelWarn, "tls_failure_alert", 0)
	r.AddAttrs(slog.String("unexpected_field", "some_value"))
	require.NoError(t, h.Handle(context.Background(), r))
	require.NoError(t, h.Flush(context.Background()))

	require.NotEmpty(t, recv, "record should still produce a Slack payload")
	assert.Contains(t, debugBuf.String(), "unexpected_field",
		"DebugLogger should warn about the unknown attr key")
	assert.NotContains(t, debugBuf.String(), "some_value",
		"DebugLogger must not log the attr value")
}

type capturedSlackMessage struct {
	Text        string                    `json:"text"`
	Attachments []capturedSlackAttachment `json:"attachments"`
}

type capturedSlackAttachment struct {
	Color  string               `json:"color"`
	Fields []capturedSlackField `json:"fields"`
}

type capturedSlackField struct {
	Title string `json:"title"`
	Value string `json:"value"`
	Short bool   `json:"short"`
}

func decodeSlackMessage(t *testing.T, body []byte) capturedSlackMessage {
	t.Helper()
	var msg capturedSlackMessage
	require.NoError(t, json.Unmarshal(body, &msg))
	return msg
}

func flattenSlackFields(msg capturedSlackMessage) map[string]string {
	fields := make(map[string]string)
	for _, attachment := range msg.Attachments {
		for _, field := range attachment.Fields {
			fields[field.Title] = field.Value
		}
	}
	return fields
}

func flattenFields(fields []capturedSlackField) map[string]string {
	result := make(map[string]string)
	for _, field := range fields {
		result[field.Title] = field.Value
	}
	return result
}

func summaryOrgStats(count int) map[string]int64 {
	stats := make(map[string]int64, count)
	for i := 1; i <= count; i++ {
		stats[fmt.Sprintf("org-%02d", i)] = int64(i)
	}
	return stats
}

func TestFetchWarning_NotAggregatedWithAlerts(t *testing.T) {
	var spy spyHandler

	ctx := context.Background()
	require.NoError(t, notify.LogAlert(ctx, &spy, sampleAlert()))
	require.NoError(t, notify.LogWarning(ctx, &spy, notify.Warning{
		Kind:        notify.WarningKindSizeMismatch,
		UID:         10,
		UIDValidity: 1,
		MessageID:   "<warn@example.com>",
	}))

	require.Len(t, spy.records, 2)
	// Alert and warning must be separate records with different messages.
	messages := make(map[string]bool)
	for _, r := range spy.records {
		messages[r.Message] = true
	}
	assert.True(t, messages["tls_failure_alert"], "expected tls_failure_alert record")
	assert.True(t, messages["fetch_warning"], "expected fetch_warning record")
}

func TestFetchWarning_DistinctSlackMessage(t *testing.T) {
	var spy spyHandler

	ctx := context.Background()
	require.NoError(t, notify.LogWarning(ctx, &spy, notify.Warning{
		Kind:        notify.WarningKindParseFailure,
		UID:         5,
		UIDValidity: 99,
		MessageID:   "<parse@example.com>",
	}))
	require.Len(t, spy.records, 1)
	assert.Equal(t, slog.LevelWarn, spy.records[0].Level)
	assert.Equal(t, "fetch_warning", spy.records[0].Message)
}
