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

// TestFormatAlerts_Fields verifies core alert values appear in the attachment fields.
func TestFormatAlerts_Fields(t *testing.T) {
	msg := decodeSlackMessage(t, flushAlert(t, sampleAlert()))
	texts := alertBodyTexts(msg)
	require.NotEmpty(t, texts)
	combined := strings.Join(texts, "\n")
	assert.Contains(t, combined, "example.com")
	assert.Contains(t, combined, "sts")
	assert.Contains(t, combined, "5")
	assert.Contains(t, combined, "2024-01-01")
	assert.Contains(t, combined, "2024-01-07")
}

// TestFormatAlerts_RunID verifies the Run ID appears in the attachment field of the last chunk.
func TestFormatAlerts_RunID(t *testing.T) {
	msg := decodeSlackMessage(t, flushAlert(t, sampleAlert()))
	fields := flattenSlackFields(msg)
	assert.Equal(t, "run-001", fields["Run ID"])
}

func TestFormatAlerts_TitleOrgCount(t *testing.T) {
	assert.Contains(t, string(flushAlert(t, sampleAlert())), "1 organizations affected")
}

// TestFormatAlerts_TitleOrgCountDedup verifies that duplicate OrganizationName
// values are counted only once in the title.
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
	assert.Contains(t, body, "⚠️")
	assert.Contains(t, body, "TLS Failures")
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

// TestFormatAlerts_NoTruncation verifies the debug logger gets the full untruncated
// payload and the Slack payload has the long string truncated.
func TestFormatAlerts_NoTruncation(t *testing.T) {
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

	// Per-field truncation occurs inside formatAlerts (before debug logging), so the
	// debug logger sees the 120-char truncated org name, not the full 5000-char string.
	truncatedOrg := strings.Repeat("x", 117) + "..."
	assert.Contains(t, debugBuf.String(), truncatedOrg, "debug logger must contain the per-field-truncated org name")
	msg := decodeSlackMessage(t, recv)
	for _, att := range msg.Attachments {
		for _, f := range att.Fields {
			assert.LessOrEqual(t, utf8.RuneCountInString(f.Value), 1000, "field value must be within 1000 runes")
		}
	}
	assert.NotContains(t, string(recv), longName, "Slack payload should be truncated")
}

// TestFormatAlerts_AttachmentFields verifies alerts keep the legacy attachment
// field layout used by Slack's yellow warning block.
func TestFormatAlerts_AttachmentFields(t *testing.T) {
	msg := decodeSlackMessage(t, flushAlert(t, sampleAlert()))
	require.Len(t, msg.Attachments, 1)
	assert.Equal(t, "warning", msg.Attachments[0].Color)
	require.NotEmpty(t, msg.Attachments[0].Fields)
	assert.Equal(t, "Organization / Policy / Failures / Period", msg.Attachments[0].Fields[0].Title)
	assert.Contains(t, msg.Attachments[0].Fields[0].Value, "example.com | sts | 5 | 2024-01-01 – 2024-01-07")
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

func TestFormatSystemError_ActionHint_UIDValidityChanged(t *testing.T) {
	var recv []byte
	h, cleanup := buildCaptureHandler(t, notify.LevelModeWarnAndAbove, &recv)
	defer cleanup()
	require.NoError(t, notify.LogSystemError(context.Background(), h, notify.SystemError{
		Kind: notify.SystemErrorKindUIDValidityChanged, Component: "fetch",
	}))
	require.NoError(t, h.Flush(context.Background()))
	body := string(recv)
	assert.Contains(t, body, "Action Required")
	assert.Contains(t, body, "tlsrpt-digest --config")
	assert.Contains(t, body, "recover --mode discard-old --yes")
	assert.NotContains(t, body, "abort-reset")
}

func TestFormatSystemError_ActionHint_RecoveryRequired(t *testing.T) {
	var recv []byte
	h, cleanup := buildCaptureHandler(t, notify.LevelModeWarnAndAbove, &recv)
	defer cleanup()
	require.NoError(t, notify.LogSystemError(context.Background(), h, notify.SystemError{
		Kind: notify.SystemErrorKindRecoveryRequired, Component: "fetch",
	}))
	require.NoError(t, h.Flush(context.Background()))
	body := string(recv)
	assert.Contains(t, body, "Action Required")
	assert.Contains(t, body, "tlsrpt-digest --config")
	assert.Contains(t, body, "recover --mode discard-old --yes")
	assert.NotContains(t, body, "abort-reset")
}

func TestFormatSystemError_ActionHint_CredentialsMissing(t *testing.T) {
	var recv []byte
	h, cleanup := buildCaptureHandler(t, notify.LevelModeWarnAndAbove, &recv)
	defer cleanup()
	require.NoError(t, notify.LogSystemError(context.Background(), h, notify.SystemError{
		Kind: notify.SystemErrorKindIMAPCredentialsMissing, Component: "fetch",
	}))
	require.NoError(t, h.Flush(context.Background()))
	body := string(recv)
	assert.Contains(t, body, "Action Required")
	assert.Contains(t, body, "TLSRPT_IMAP_USERNAME")
}

func TestFormatSystemError_NoActionHint_StoreCorruption(t *testing.T) {
	var recv []byte
	h, cleanup := buildCaptureHandler(t, notify.LevelModeWarnAndAbove, &recv)
	defer cleanup()
	require.NoError(t, notify.LogSystemError(context.Background(), h, notify.SystemError{
		Kind: notify.SystemErrorKindStoreCorruption, Component: "fetch",
	}))
	require.NoError(t, h.Flush(context.Background()))
	assert.NotContains(t, string(recv), "Action Required")
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

// TestFormatAlerts_NoPolicyFound verifies no-policy-found appears in alert fields.
func TestFormatAlerts_NoPolicyFound(t *testing.T) {
	a := sampleAlert()
	a.PolicyType = notify.PolicyTypeNoPolicyFound
	msg := decodeSlackMessage(t, flushAlert(t, a))
	combined := strings.Join(alertBodyTexts(msg), "\n")
	assert.Contains(t, combined, "no-policy-found")
}

// TestFormatAlerts_PolicyTypeUnknown verifies unknown placeholder appears in alert fields.
func TestFormatAlerts_PolicyTypeUnknown(t *testing.T) {
	a := sampleAlert()
	a.PolicyType = notify.PolicyTypeUnknown
	msg := decodeSlackMessage(t, flushAlert(t, a))
	combined := strings.Join(alertBodyTexts(msg), "\n")
	assert.Contains(t, combined, "(unknown)")
}

// TestExtract_UnknownAttrKeyLogged verifies that an attr key not recognised by
// the extract functions produces a Warn entry in the DebugLogger.
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

// TestExtract_UnknownAttrKeyLogged verifies:
// 1. Known keys (report_id, failure_details) are not warned about.
// 2. Unknown top-level key "unexpected_field" is warned by key name only.
// 3. Unknown key inside a failure_details child group is warned by key name only.
// 4. A failure_details child that is not a group is skipped without panic.
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

	// Record with: known attrs + unknown top-level + unknown child key in failure_details.
	r := slog.NewRecord(time.Now(), slog.LevelWarn, "tls_failure_alert", 0)
	r.AddAttrs(
		slog.String("organization_name", "example.com"),
		slog.String("policy_type", "sts"),
		slog.Int64("failure_count", 1),
		slog.String("report_id", "rpt-unk"),
		slog.Int64("failure_details_total_count", 0),
		slog.Int64("failure_details_total_sessions", 0),
		slog.Group("failure_details",
			slog.Group("0",
				slog.String("result_type", "certificate-expired"),
				slog.Int64("failed_session_count", 3),
				slog.String("unexpected_detail_key", "injected"),
			),
		),
		slog.String("unexpected_field", "some_value"),
	)
	require.NoError(t, h.Handle(context.Background(), r))
	require.NoError(t, h.Flush(context.Background()))

	require.NotEmpty(t, recv, "record should still produce a Slack payload")
	// Known keys must not generate warnings.
	assert.NotContains(t, debugBuf.String(), "report_id",
		"report_id is a known key and must not be warned")
	assert.NotContains(t, debugBuf.String(), "failure_details\"",
		"failure_details is a known key and must not be warned")
	// Unknown top-level key must be warned.
	assert.Contains(t, debugBuf.String(), "unexpected_field",
		"DebugLogger should warn about the unknown top-level attr key")
	assert.NotContains(t, debugBuf.String(), "some_value",
		"DebugLogger must not log the attr value")
	// Unknown detail key must be warned.
	assert.Contains(t, debugBuf.String(), "unexpected_detail_key",
		"DebugLogger should warn about the unknown key in failure_details child group")
	assert.NotContains(t, debugBuf.String(), "injected",
		"DebugLogger must not log the detail attr value")

	// Case: failure_details child that is a non-group value must not panic.
	r2 := slog.NewRecord(time.Now(), slog.LevelWarn, "tls_failure_alert", 0)
	r2.AddAttrs(
		slog.String("organization_name", "example.com"),
		slog.String("policy_type", "sts"),
		slog.Int64("failure_count", 1),
		slog.String("report_id", "rpt-non-group"),
		slog.Int64("failure_details_total_count", 0),
		slog.Int64("failure_details_total_sessions", 0),
		slog.Group("failure_details",
			slog.String("0", "not-a-group"),
		),
	)
	require.NoError(t, h.Handle(context.Background(), r2))
	require.NotPanics(t, func() {
		require.NoError(t, h.Flush(context.Background()))
	})
}

// ---- Decoder and helper types ----

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

// alertBodyTexts returns policy and overflow field text for alert messages.
func alertBodyTexts(msg capturedSlackMessage) []string {
	var texts []string
	for _, att := range msg.Attachments {
		for _, f := range att.Fields {
			if f.Title == "Run ID" {
				continue
			}
			texts = append(texts, f.Title+"\n"+f.Value)
		}
	}
	return texts
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

// TestFormatWarning_MailboxLevel_OmitsPerMessageFields verifies that a mailbox-level
// warning (UID=0, UIDValidity=0, MessageID="") does not render misleading zero fields.
func TestFormatWarning_MailboxLevel_OmitsPerMessageFields(t *testing.T) {
	var recv []byte
	h, cleanup := buildCaptureHandler(t, notify.LevelModeWarnAndAbove, &recv)
	defer cleanup()

	ctx := context.Background()
	require.NoError(t, notify.LogWarning(ctx, h, notify.Warning{
		Kind: notify.WarningKindMailboxReadOnly,
	}))
	require.NoError(t, h.Flush(ctx))

	body := string(recv)
	assert.Contains(t, body, "mailbox_read_only", "kind field should appear in payload")
	assert.NotContains(t, body, `"UID"`, "UID field must be absent for mailbox-level warning")
	assert.NotContains(t, body, `"UIDValidity"`, "UIDValidity field must be absent for mailbox-level warning")
	assert.NotContains(t, body, `"Message-ID"`, "Message-ID field must be absent for mailbox-level warning")
}

// TestFormatWarning_SlackPayloadFields verifies that LogWarning+Flush produces a Slack
// JSON payload containing all expected fields: kind, uid, uidvalidity, message_id, run_id.
func TestFormatWarning_SlackPayloadFields(t *testing.T) {
	var recv []byte
	h, cleanup := buildCaptureHandler(t, notify.LevelModeWarnAndAbove, &recv)
	defer cleanup()

	ctx := context.Background()
	require.NoError(t, notify.LogWarning(ctx, h, notify.Warning{
		Kind:        notify.WarningKindSizeMismatch,
		UID:         123,
		UIDValidity: 456,
		MessageID:   "<abc@example.com>",
	}))
	require.NoError(t, h.Flush(ctx))

	body := string(recv)
	assert.Contains(t, body, "size_mismatch", "kind field should appear in payload")
	assert.Contains(t, body, "123", "uid value should appear in payload")
	assert.Contains(t, body, "456", "uidvalidity value should appear in payload")
	assert.Contains(t, body, "abc@example.com", "message_id should appear in payload")
	assert.Contains(t, body, "run-001", "run_id should appear in payload")
}

// TestFormatAlerts_PolicyField verifies each policy's org name, policy type,
// failure count, and period (UTC) appear in the same field.
func TestFormatAlerts_PolicyField(t *testing.T) {
	alert := notify.Alert{
		OrganizationName: "acme.example",
		PolicyType:       notify.PolicyTypeTLSA,
		FailureCount:     42,
		DateRange: notify.DateRange{
			Start: time.Date(2024, 3, 1, 6, 0, 0, 0, time.UTC),
			End:   time.Date(2024, 3, 7, 18, 0, 0, 0, time.UTC),
		},
		ReportID: "rpt-tlsa-1",
	}
	msg := decodeSlackMessage(t, flushAlert(t, alert))
	texts := alertBodyTexts(msg)
	require.NotEmpty(t, texts)
	sec := strings.Join(texts, "\n")
	assert.Contains(t, sec, "acme.example")
	assert.Contains(t, sec, "tlsa")
	assert.Contains(t, sec, "42")
	assert.Contains(t, sec, "2024-03-01")
	assert.Contains(t, sec, "2024-03-07")
	assert.Contains(t, sec, "rpt-tlsa-1")
}

// TestFormatAlerts_AllPoliciesIncluded verifies all failure policies appear in
// distinct attachment fields.
func TestFormatAlerts_AllPoliciesIncluded(t *testing.T) {
	var recv []byte
	h, cleanup := buildCaptureHandler(t, notify.LevelModeWarnAndAbove, &recv)
	defer cleanup()

	orgs := []string{"org-a.example", "org-b.example", "org-c.example"}
	for _, org := range orgs {
		require.NoError(t, notify.LogAlert(context.Background(), h, notify.Alert{
			OrganizationName: org,
			PolicyType:       notify.PolicyTypeSTS,
			FailureCount:     1,
		}))
	}
	require.NoError(t, h.Flush(context.Background()))

	msg := decodeSlackMessage(t, recv)
	fields := alertBodyTexts(msg)
	require.Len(t, fields, len(orgs), "each policy must have its own attachment field")
	for i, org := range orgs {
		assert.Contains(t, fields[i], org, "field[%d] must contain org %s", i, org)
	}
}

// TestFormatAlerts_FieldHeaders verifies the legacy field header is preserved
// so Slack renders the alert in the familiar warning attachment layout.
func TestFormatAlerts_FieldHeaders(t *testing.T) {
	var recv []byte
	h, cleanup := buildCaptureHandler(t, notify.LevelModeWarnAndAbove, &recv)
	defer cleanup()

	for _, pt := range []notify.PolicyType{notify.PolicyTypeSTS, notify.PolicyTypeTLSA} {
		require.NoError(t, notify.LogAlert(context.Background(), h, notify.Alert{
			OrganizationName: "corp.example",
			PolicyType:       pt,
			FailureCount:     2,
		}))
	}
	require.NoError(t, h.Flush(context.Background()))

	msg := decodeSlackMessage(t, recv)
	require.Len(t, msg.Attachments, 1)
	var headerCount int
	for _, f := range msg.Attachments[0].Fields {
		if f.Title == "Organization / Policy / Failures / Period" {
			headerCount++
		}
	}
	assert.Equal(t, 2, headerCount, "each policy should use the familiar field header")
}

// TestFormatAlerts_FailureDetails_Basic verifies result-type and failed-session-count
// appear in alert fields.
func TestFormatAlerts_FailureDetails_Basic(t *testing.T) {
	alert := notify.Alert{
		OrganizationName:            "basic.example",
		PolicyType:                  notify.PolicyTypeSTS,
		FailureCount:                7,
		FailureDetailsTotalCount:    1,
		FailureDetailsTotalSessions: 7,
		FailureDetails: []notify.FailureDetail{
			{ResultType: "certificate-expired", FailedSessionCount: 7},
		},
	}
	msg := decodeSlackMessage(t, flushAlert(t, alert))
	sec := strings.Join(alertBodyTexts(msg), "\n")
	assert.Contains(t, sec, "certificate-expired")
	assert.Contains(t, sec, "7")
}

// TestFormatAlerts_FailureDetails_MXHostname verifies receiving-mx-hostname is
// shown when present and absent when empty.
func TestFormatAlerts_FailureDetails_MXHostname(t *testing.T) {
	withMX := notify.Alert{
		OrganizationName:            "mx.example",
		PolicyType:                  notify.PolicyTypeSTS,
		FailureCount:                3,
		FailureDetailsTotalCount:    1,
		FailureDetailsTotalSessions: 3,
		FailureDetails: []notify.FailureDetail{
			{ResultType: "validation-failure", FailedSessionCount: 3, ReceivingMXHostname: "mail.mx.example"},
		},
	}
	noMX := notify.Alert{
		OrganizationName:            "nomx.example",
		PolicyType:                  notify.PolicyTypeSTS,
		FailureCount:                2,
		FailureDetailsTotalCount:    1,
		FailureDetailsTotalSessions: 2,
		FailureDetails: []notify.FailureDetail{
			{ResultType: "validation-failure", FailedSessionCount: 2},
		},
	}
	withSec := strings.Join(alertBodyTexts(decodeSlackMessage(t, flushAlert(t, withMX))), "\n")
	noSec := strings.Join(alertBodyTexts(decodeSlackMessage(t, flushAlert(t, noMX))), "\n")

	assert.Contains(t, withSec, "mail.mx.example")
	assert.NotContains(t, noSec, "MX:")
}

// TestFormatAlerts_FailureDetails_ReasonCode verifies failure-reason-code is
// shown when present and absent when empty.
func TestFormatAlerts_FailureDetails_ReasonCode(t *testing.T) {
	withReason := notify.Alert{
		OrganizationName:            "reason.example",
		PolicyType:                  notify.PolicyTypeSTS,
		FailureCount:                4,
		FailureDetailsTotalCount:    1,
		FailureDetailsTotalSessions: 4,
		FailureDetails: []notify.FailureDetail{
			{ResultType: "certificate-expired", FailedSessionCount: 4, FailureReasonCode: "X509_EXPIRED"},
		},
	}
	noReason := notify.Alert{
		OrganizationName:            "noreason.example",
		PolicyType:                  notify.PolicyTypeSTS,
		FailureCount:                1,
		FailureDetailsTotalCount:    1,
		FailureDetailsTotalSessions: 1,
		FailureDetails: []notify.FailureDetail{
			{ResultType: "starttls-not-supported", FailedSessionCount: 1},
		},
	}
	withSec := strings.Join(alertBodyTexts(decodeSlackMessage(t, flushAlert(t, withReason))), "\n")
	noSec := strings.Join(alertBodyTexts(decodeSlackMessage(t, flushAlert(t, noReason))), "\n")

	assert.Contains(t, withSec, "X509_EXPIRED")
	assert.NotContains(t, noSec, "Reason:")
}

// TestFormatAlerts_FailureDetails_AllWhenLE3 verifies that 3 or fewer entries
// are all shown in detail.
func TestFormatAlerts_FailureDetails_AllWhenLE3(t *testing.T) {
	alert := notify.Alert{
		OrganizationName:            "le3.example",
		PolicyType:                  notify.PolicyTypeSTS,
		FailureCount:                6,
		FailureDetailsTotalCount:    3,
		FailureDetailsTotalSessions: 6,
		FailureDetails: []notify.FailureDetail{
			{ResultType: "certificate-expired", FailedSessionCount: 3},
			{ResultType: "validation-failure", FailedSessionCount: 2},
			{ResultType: "starttls-not-supported", FailedSessionCount: 1},
		},
	}
	sec := strings.Join(alertBodyTexts(decodeSlackMessage(t, flushAlert(t, alert))), "\n")
	assert.Contains(t, sec, "certificate-expired")
	assert.Contains(t, sec, "validation-failure")
	assert.Contains(t, sec, "starttls-not-supported")
	assert.NotContains(t, sec, "Other")
}

// TestFormatAlerts_FailureDetails_SummaryWhenGT3 verifies that 4+ entries show
// top 3 in detail and the rest as "Other N entries (M sessions total)".
func TestFormatAlerts_FailureDetails_SummaryWhenGT3(t *testing.T) {
	// 5 entries, sessions: 10,8,6,4,2 = total 30; top3 = 24, other2 = 6 sessions
	alert := notify.Alert{
		OrganizationName:            "gt3.example",
		PolicyType:                  notify.PolicyTypeSTS,
		FailureCount:                30,
		FailureDetailsTotalCount:    5,
		FailureDetailsTotalSessions: 30,
		FailureDetails: []notify.FailureDetail{
			{ResultType: "type-a", FailedSessionCount: 10},
			{ResultType: "type-b", FailedSessionCount: 8},
			{ResultType: "type-c", FailedSessionCount: 6},
			{ResultType: "type-d", FailedSessionCount: 4},
			{ResultType: "type-e", FailedSessionCount: 2},
		},
	}
	sec := strings.Join(alertBodyTexts(decodeSlackMessage(t, flushAlert(t, alert))), "\n")
	assert.Contains(t, sec, "type-a")
	assert.Contains(t, sec, "type-b")
	assert.Contains(t, sec, "type-c")
	assert.NotContains(t, sec, "type-d")
	assert.NotContains(t, sec, "type-e")
	assert.Contains(t, sec, "Other 2 entries")
	assert.Contains(t, sec, "6 sessions total")

	// Verify >10 entries: LogAlert computes totalCount=12 from the full 12-entry
	// slice and caps FailureDetails to 10. buildFailureDetailsText then uses the
	// pre-cap totals for the "Other" line.
	alertBig := notify.Alert{
		OrganizationName: "big.example",
		PolicyType:       notify.PolicyTypeSTS,
		FailureCount:     78, // sum 1..12
		// Pass all 12 entries; LogAlert sorts desc, computes totalCount=12 / totalSessions=78, caps to 10.
		FailureDetails: func() []notify.FailureDetail {
			dets := make([]notify.FailureDetail, 12)
			for i := range dets {
				dets[i] = notify.FailureDetail{
					ResultType:         fmt.Sprintf("type-%02d", i+1),
					FailedSessionCount: int64(i + 1), // 1..12
				}
			}
			return dets
		}(),
	}
	secBig := strings.Join(alertBodyTexts(decodeSlackMessage(t, flushAlert(t, alertBig))), "\n")
	// Other = 12 - 3 = 9 entries; sessions total - top3 sessions = 78 - (12+11+10) = 45
	assert.Contains(t, secBig, "Other 9 entries")
	assert.Contains(t, secBig, "45 sessions total")
}

// TestFormatAlerts_FailureDetails_Empty verifies empty failure-details produces
// a clean alert body with no error or strange output.
func TestFormatAlerts_FailureDetails_Empty(t *testing.T) {
	alert := notify.Alert{
		OrganizationName: "empty-details.example",
		PolicyType:       notify.PolicyTypeSTS,
		FailureCount:     3,
		ReportID:         "rpt-empty",
	}
	sec := strings.Join(alertBodyTexts(decodeSlackMessage(t, flushAlert(t, alert))), "\n")
	assert.Contains(t, sec, "empty-details.example")
	assert.Contains(t, sec, "rpt-empty")
	assert.NotContains(t, sec, "[1]")
	assert.NotContains(t, sec, "Other")
}

// TestFormatAlerts_ReportID verifies the Report ID appears in alert fields.
func TestFormatAlerts_ReportID(t *testing.T) {
	alert := sampleAlert()
	alert.ReportID = "rpt-unique-42"
	sec := strings.Join(alertBodyTexts(decodeSlackMessage(t, flushAlert(t, alert))), "\n")
	assert.Contains(t, sec, "rpt-unique-42")
}

// TestFormatAlerts_NormalizesControlChars verifies control characters in external
// values are replaced with spaces, not rendered as line breaks.
func TestFormatAlerts_NormalizesControlChars(t *testing.T) {
	alert := notify.Alert{
		OrganizationName:            "evil\norg\ttab",
		PolicyType:                  notify.PolicyTypeSTS,
		FailureCount:                1,
		ReportID:                    "rpt\r\ninjected",
		FailureDetailsTotalCount:    1,
		FailureDetailsTotalSessions: 1,
		FailureDetails: []notify.FailureDetail{
			{
				ResultType:          "type\nwith\nnewlines",
				FailedSessionCount:  1,
				ReceivingMXHostname: "mx\thost",
				FailureReasonCode:   "CODE\rX",
			},
		},
	}
	sec := strings.Join(alertBodyTexts(decodeSlackMessage(t, flushAlert(t, alert))), "\n")
	// Control characters must be replaced with space.
	assert.NotContains(t, sec, "evil\norg")
	assert.NotContains(t, sec, "rpt\r\ninjected")
	assert.NotContains(t, sec, "type\nwith")
	// Normalised values should appear (verifying \n, \t, and \r are all spaces).
	assert.Contains(t, sec, "evil org tab")       // \n and \t in org name become spaces
	assert.Contains(t, sec, "type with newlines") // \n in result-type becomes space
	assert.Contains(t, sec, "mx host")            // \t in MX hostname becomes space
	assert.Contains(t, sec, "CODE X")             // \r in reason code becomes space
}

// TestFormatAlerts_ValueTruncation verifies that each per-field limit is enforced
// and required labels remain in the output.
func TestFormatAlerts_ValueTruncation(t *testing.T) {
	longOrg := strings.Repeat("o", 200)
	longReportID := strings.Repeat("r", 200)
	longResultType := strings.Repeat("t", 200)
	longMX := strings.Repeat("m", 200)
	longReason := strings.Repeat("c", 200)

	alert := notify.Alert{
		OrganizationName:            longOrg,
		PolicyType:                  notify.PolicyTypeSTS,
		FailureCount:                1,
		ReportID:                    longReportID,
		FailureDetailsTotalCount:    1,
		FailureDetailsTotalSessions: 1,
		FailureDetails: []notify.FailureDetail{
			{
				ResultType:          longResultType,
				FailedSessionCount:  1,
				ReceivingMXHostname: longMX,
				FailureReasonCode:   longReason,
			},
		},
	}
	msg := decodeSlackMessage(t, flushAlert(t, alert))
	texts := alertBodyTexts(msg)
	require.NotEmpty(t, texts)
	sec := strings.Join(texts, "\n")

	// Full long values must not appear.
	assert.NotContains(t, sec, longOrg)
	assert.NotContains(t, sec, longReportID)
	assert.NotContains(t, sec, longResultType)
	assert.NotContains(t, sec, longMX)
	assert.NotContains(t, sec, longReason)

	// Truncated forms (ending "...") must appear in alert fields, proving fields
	// are truncated rather than silently dropped.
	assert.Contains(t, sec, strings.Repeat("o", 117)+"...", "org name truncated form must appear")
	assert.Contains(t, sec, strings.Repeat("r", 157)+"...", "report-id truncated form must appear")
	assert.Contains(t, sec, strings.Repeat("t", 77)+"...", "result-type truncated form must appear")
	assert.Contains(t, sec, strings.Repeat("m", 117)+"...", "MX hostname truncated form must appear")
	assert.Contains(t, sec, strings.Repeat("c", 77)+"...", "reason code truncated form must appear")

	for _, att := range msg.Attachments {
		for _, f := range att.Fields {
			assert.LessOrEqual(t, utf8.RuneCountInString(f.Value), 1000, "field value must be within 1000 runes")
		}
	}

	// Structural labels must remain.
	assert.Contains(t, sec, "Report ID")
	assert.Contains(t, sec, "Failure Details")
}

// TestFormatAlerts_AllPoliciesSpanMultipleAttachments verifies that a large
// number of failing policies are all shown across multiple attachments — none
// are silently moved to an overflow summary — and that all arrive in a single
// Slack POST.
func TestFormatAlerts_AllPoliciesSpanMultipleAttachments(t *testing.T) {
	var recv []byte
	h, cleanup := buildCaptureHandler(t, notify.LevelModeWarnAndAbove, &recv)
	defer cleanup()

	const total = 60
	for i := range total {
		require.NoError(t, notify.LogAlert(context.Background(), h, notify.Alert{
			OrganizationName: fmt.Sprintf("org-%02d.example", i),
			PolicyType:       notify.PolicyTypeSTS,
			FailureCount:     int64(i + 1),
		}))
	}
	require.NoError(t, h.Flush(context.Background()))

	msg := decodeSlackMessage(t, recv)

	// All 60 policies must appear as "Organization / Policy / Failures / Period"
	// fields, distributed across multiple attachments (3 per attachment).
	var policyFieldCount int
	var hasAdditionalPolicies bool
	for _, att := range msg.Attachments {
		for _, f := range att.Fields {
			switch f.Title {
			case "Organization / Policy / Failures / Period":
				policyFieldCount++
			case "Additional Policies":
				hasAdditionalPolicies = true
			}
		}
	}
	assert.Equal(t, total, policyFieldCount, "all policies must have a visible summary field")
	assert.False(t, hasAdditionalPolicies, "no policies should be hidden in an overflow summary")

	// Verify chunking: each attachment has at most 3 policy summary fields.
	for i, att := range msg.Attachments {
		var count int
		for _, f := range att.Fields {
			if f.Title == "Organization / Policy / Failures / Period" {
				count++
			}
		}
		assert.LessOrEqual(t, count, 3, "attachment[%d] must have at most 3 policy summary fields", i)
	}
}

// TestFormatAlerts_AttachmentOverflow verifies that when the number of policies
// would exceed Slack's 100-attachment limit, an overflow summary is added to the
// last attachment and the total attachment count stays at or below 100.
func TestFormatAlerts_AttachmentOverflow(t *testing.T) {
	var recv []byte
	h, cleanup := buildCaptureHandler(t, notify.LevelModeWarnAndAbove, &recv)
	defer cleanup()

	// 301 policies requires ceil(301/3)=101 attachments without overflow capping.
	const total = 301
	for i := range total {
		require.NoError(t, notify.LogAlert(context.Background(), h, notify.Alert{
			OrganizationName: fmt.Sprintf("org-%03d.example", i),
			PolicyType:       notify.PolicyTypeSTS,
			FailureCount:     int64(i + 1),
		}))
	}
	require.NoError(t, h.Flush(context.Background()))

	msg := decodeSlackMessage(t, recv)
	assert.LessOrEqual(t, len(msg.Attachments), 100, "must not exceed Slack's 100-attachment limit")

	// The last attachment must contain the overflow summary and Run ID.
	lastAtt := msg.Attachments[len(msg.Attachments)-1]
	var hasOverflow, hasRunID bool
	for _, f := range lastAtt.Fields {
		switch f.Title {
		case "Additional Policies":
			hasOverflow = true
			assert.Contains(t, f.Value, "additional policies omitted")
		case "Run ID":
			hasRunID = true
		}
	}
	assert.True(t, hasOverflow, "overflow summary must appear in the last attachment")
	assert.True(t, hasRunID, "Run ID must appear in the last attachment")

	// With maxPoliciesInLastChunkWithOverflow=2, shown = 99×3+2 = 299 policies.
	var policyFieldCount int
	for _, att := range msg.Attachments {
		for _, f := range att.Fields {
			if f.Title == "Organization / Policy / Failures / Period" {
				policyFieldCount++
			}
		}
	}
	assert.Equal(t, 299, policyFieldCount, "299 policies shown (last chunk capped at 2); 2 overflow to summary")

	// Also verify that the last attachment's field count stays within the 10-field budget.
	// Worst case: 2 policies × 3 fields (summary+reportID+details) + overflow + Run ID = 8.
	assert.LessOrEqual(t, len(lastAtt.Fields), 10, "last attachment must not exceed 10 fields")
}
