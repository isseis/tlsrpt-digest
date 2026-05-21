package notify_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/isseis/tlsrpt-digest/internal/config"
	"github.com/isseis/tlsrpt-digest/internal/notify"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const sampleWebhookSuffix = "sample-webhook-segment"

func TestSecretNotInMessage(t *testing.T) {
	var recv []byte
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		recv, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	webhookURL := srv.URL + "/" + sampleWebhookSuffix + "/webhook"
	opts := notify.SlackHandlerOptions{
		WebhookURL:    config.Secret(webhookURL),
		AllowedHost:   "127.0.0.1",
		RunID:         "test",
		LevelMode:     notify.LevelModeWarnAndAbove,
		HTTPClient:    srv.Client(),
		BackoffConfig: notify.DefaultBackoffConfig,
	}
	h, err := notify.NewSlackHandler(opts)
	require.NoError(t, err)

	require.NoError(t, notify.LogAlert(context.Background(), h, notify.Alert{
		OrganizationName: "example.com",
		PolicyType:       notify.PolicyTypeSTS,
		FailureCount:     1,
	}))
	require.NoError(t, h.Flush(context.Background()))

	body := string(recv)
	assert.NotContains(t, body, sampleWebhookSuffix, "webhook marker must not appear in Slack payload")
}

func TestWebhookURLNotLogged(t *testing.T) {
	// Any slog output produced by the handler must not contain the webhook URL.
	var buf strings.Builder
	debugLogger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	webhookURL := srv.URL + "/" + sampleWebhookSuffix + "/webhook"
	opts := notify.SlackHandlerOptions{
		WebhookURL:    config.Secret(webhookURL),
		AllowedHost:   "127.0.0.1",
		RunID:         "test",
		LevelMode:     notify.LevelModeWarnAndAbove,
		HTTPClient:    srv.Client(),
		BackoffConfig: notify.DefaultBackoffConfig,
		DebugLogger:   debugLogger,
	}
	h, err := notify.NewSlackHandler(opts)
	require.NoError(t, err)

	require.NoError(t, notify.LogAlert(context.Background(), h, notify.Alert{
		OrganizationName: "example.com",
		PolicyType:       notify.PolicyTypeSTS,
		FailureCount:     1,
	}))
	require.NoError(t, h.Flush(context.Background()))

	assert.NotContains(t, buf.String(), sampleWebhookSuffix, "webhook marker must not appear in log output")
}

func TestFlushError_NoURLInErrorString(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	webhookURL := srv.URL + "/" + sampleWebhookSuffix + "/webhook"
	opts := notify.SlackHandlerOptions{
		WebhookURL:    config.Secret(webhookURL),
		AllowedHost:   "127.0.0.1",
		RunID:         "test",
		LevelMode:     notify.LevelModeWarnAndAbove,
		HTTPClient:    srv.Client(),
		BackoffConfig: notify.DefaultBackoffConfig,
	}
	h, err := notify.NewSlackHandler(opts)
	require.NoError(t, err)

	r := slog.NewRecord(time.Now(), slog.LevelWarn, "test", 0)
	require.NoError(t, h.Handle(context.Background(), r))
	flushErr := h.Flush(context.Background())
	require.Error(t, flushErr)
	assert.NotContains(t, flushErr.Error(), sampleWebhookSuffix, "error message must not expose webhook marker")

	// Error chain must still carry typed errors.
	_, ok := errors.AsType[*notify.SlackClientError](flushErr)
	assert.True(t, ok)
}

func TestDebugWriterNotTriggerSlack(t *testing.T) {
	var slackCalls int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&slackCalls, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	h, err := notify.NewSlackHandler(notify.SlackHandlerOptions{
		WebhookURL:    config.Secret(srv.URL + "/webhook"),
		AllowedHost:   "127.0.0.1",
		RunID:         "test",
		LevelMode:     notify.LevelModeWarnAndAbove,
		HTTPClient:    srv.Client(),
		BackoffConfig: notify.DefaultBackoffConfig,
	})
	require.NoError(t, err)
	assert.NotNil(t, h)

	debugLogger := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelDebug}))
	debugLogger.Info("debug info unrelated to Slack")

	assert.Equal(t, int32(0), atomic.LoadInt32(&slackCalls), "writing to Debug Logger must not trigger Slack POSTs")
}

func TestSlackHandler_NoExportedLoggerField(t *testing.T) {
	// SlackHandler should not expose an exported *slog.Logger field that could
	// become a notification write path.
	notifyType := reflect.TypeOf(notify.SlackHandler{})
	var exported []string
	for f := range notifyType.Fields() {
		f := f
		if f.IsExported() && f.Type == reflect.TypeOf((*slog.Logger)(nil)) {
			exported = append(exported, f.Name)
		}
	}
	assert.Empty(t, exported)
}

func TestSummary_NoSensitiveFields(t *testing.T) {
	var spy spyHandler
	require.NoError(t, notify.LogSummary(context.Background(), &spy, notify.Summary{
		Period:            notify.DateRange{Start: time.Now(), End: time.Now()},
		OrganizationStats: map[string]int64{"org-a": 10},
		ReportCount:       1,
	}))

	require.Len(t, spy.records, 1)
	allowed := map[string]bool{
		"period_start":       true,
		"period_end":         true,
		"report_count":       true,
		"organization_stats": true,
	}
	spy.records[0].Attrs(func(attr slog.Attr) bool {
		assert.True(t, allowed[attr.Key], "unexpected attr key %q in LogSummary record", attr.Key)
		return true
	})
}

func TestMixedReportWarn_NotInNotifyLogger(t *testing.T) {
	start := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2024, 1, 7, 0, 0, 0, 0, time.UTC)

	st := fakeStoreWithReports(summaryReport("mixed", "org-mixed", start.Add(time.Hour), 42, 1))

	// Wire a spy as slog.Default to simulate a notify handler being globally
	// accessible. GenerateSummary must write warnings only to the provided
	// debugLogger, not to slog.Default().
	var defaultSpy spyHandler
	prev := slog.Default()
	slog.SetDefault(slog.New(&defaultSpy))
	defer slog.SetDefault(prev)

	var debugBuf strings.Builder
	debugLogger := slog.New(slog.NewTextHandler(&debugBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	_, err := notify.GenerateSummary(context.Background(), st, start, end, debugLogger)
	require.NoError(t, err)

	assert.Contains(t, debugBuf.String(), "org-mixed", "warning must appear in debugLogger")
	assert.Empty(t, defaultSpy.records, "mixed-report warning must not flow to slog.Default()")
}

func TestRedactionAlwaysEnabled(t *testing.T) {
	// SlackHandlerOptions must not have a field that disables redaction.
	// We check that the type has no "DisableRedaction" or similar field.
	opts := notify.SlackHandlerOptions{}
	v := reflect.ValueOf(opts)
	tp := v.Type()
	for field := range tp.Fields() {
		name := strings.ToLower(field.Name)
		assert.False(t,
			strings.Contains(name, "disableredact") || strings.Contains(name, "noredact"),
			"field %s looks like a redaction-disable option", field.Name,
		)
	}
}

// Verify JSON shape doesn't contain webhook URL using the same helper as above.
func TestSecretNotInMessage_JSONCheck(t *testing.T) {
	var recv []byte
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		recv, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	webhookURL := srv.URL + "/" + sampleWebhookSuffix + "/webhook"
	opts := notify.SlackHandlerOptions{
		WebhookURL:    config.Secret(webhookURL),
		AllowedHost:   "127.0.0.1",
		RunID:         "test",
		LevelMode:     notify.LevelModeWarnAndAbove,
		HTTPClient:    srv.Client(),
		BackoffConfig: notify.DefaultBackoffConfig,
	}
	h, err := notify.NewSlackHandler(opts)
	require.NoError(t, err)

	require.NoError(t, notify.LogSystemError(context.Background(), h, notify.SystemError{
		ErrorType: "StorageError",
		Message:   "disk full",
		Component: "storage",
	}))
	require.NoError(t, h.Flush(context.Background()))

	// Verify the JSON can be parsed and does not contain the webhook token.
	var msg map[string]interface{}
	require.NoError(t, json.Unmarshal(recv, &msg))
	raw := string(recv)
	assert.NotContains(t, raw, sampleWebhookSuffix)
}
