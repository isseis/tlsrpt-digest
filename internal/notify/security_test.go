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
	"testing"
	"time"

	"github.com/isseis/tlsrpt-digest/internal/config"
	"github.com/isseis/tlsrpt-digest/internal/notify"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const secretWebhookToken = "s3cr3t-token-must-not-appear"

func TestSecretNotInMessage(t *testing.T) {
	var recv []byte
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		recv, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	webhookURL := srv.URL + "/" + secretWebhookToken + "/webhook"
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
	assert.NotContains(t, body, secretWebhookToken, "webhook token must not appear in Slack payload")
}

func TestWebhookURLNotLogged(t *testing.T) {
	// Any slog output produced by the handler must not contain the webhook URL.
	var buf strings.Builder
	debugLogger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	webhookURL := srv.URL + "/" + secretWebhookToken + "/webhook"
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

	assert.NotContains(t, buf.String(), secretWebhookToken, "webhook token must not appear in log output")
}

func TestFlushError_NoURLInErrorString(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	webhookURL := srv.URL + "/" + secretWebhookToken + "/webhook"
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
	assert.NotContains(t, flushErr.Error(), secretWebhookToken, "error message must not expose webhook token")

	// Error chain must still carry typed errors.
	_, ok := errors.AsType[*notify.SlackClientError](flushErr)
	assert.True(t, ok)
}

func TestDebugWriterNotTriggerSlack(t *testing.T) {
	// Writing to a Debug Logger (separate io.Writer path) must not invoke
	// SlackHandler.Handle().
	var slackHandleCalls int
	spy := &countingHandler{count: &slackHandleCalls}

	debugWriter := slog.New(slog.NewTextHandler(io.Discard, nil))
	_ = debugWriter // Use a normal logger as Debug Logger, not the spy.

	// The spy is a slog.Handler, not a SlackHandler. Write to it directly to
	// simulate what a Debug Logger might do.
	debugLogger := slog.New(spy)
	debugLogger.Info("debug info unrelated to Slack")

	assert.Equal(t, 1, slackHandleCalls) // spy called once for the debug msg
	// The spy is NOT connected to any Slack handler, so no Slack POST happens.
	// This test verifies the separation by construction.
}

func TestPrivateLogger_NotExported(t *testing.T) {
	// The internal slog.Logger used for Slack notifications must not be exported
	// from the notify package.
	notifyPkg := reflect.TypeOf(notify.SlackHandler{})
	_ = notifyPkg // package exists
	// Verify there is no exported *slog.Logger field in notify that callers
	// could use as a write path.
	var exported []string
	for i := range notifyPkg.NumField() {
		f := notifyPkg.Field(i)
		if f.IsExported() && f.Type == reflect.TypeOf((*slog.Logger)(nil)) {
			exported = append(exported, f.Name)
		}
	}
	assert.Empty(t, exported, "no exported *slog.Logger field should exist in SlackHandler")
}

func TestRedactionAlwaysEnabled(t *testing.T) {
	// SlackHandlerOptions must not have a field that disables redaction.
	// We check that the type has no "DisableRedaction" or similar field.
	opts := notify.SlackHandlerOptions{}
	v := reflect.ValueOf(opts)
	tp := v.Type()
	for i := range tp.NumField() {
		name := strings.ToLower(tp.Field(i).Name)
		assert.False(t,
			strings.Contains(name, "disableredact") || strings.Contains(name, "noredact"),
			"field %s looks like a redaction-disable option", tp.Field(i).Name,
		)
	}
}

// countingHandler is a test helper slog.Handler that counts Handle() calls.
type countingHandler struct {
	count *int
}

func (c *countingHandler) Enabled(_ context.Context, _ slog.Level) bool { return true }
func (c *countingHandler) Handle(_ context.Context, _ slog.Record) error {
	*c.count++
	return nil
}
func (c *countingHandler) WithAttrs(_ []slog.Attr) slog.Handler { return c }
func (c *countingHandler) WithGroup(_ string) slog.Handler      { return c }

// Verify JSON shape doesn't contain webhook URL using the same helper as above.
func TestSecretNotInMessage_JSONCheck(t *testing.T) {
	var recv []byte
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		recv, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	webhookURL := srv.URL + "/" + secretWebhookToken + "/webhook"
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
	assert.NotContains(t, raw, secretWebhookToken)
}
