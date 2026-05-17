package notify_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/isseis/tlsrpt-digest/internal/config"
	"github.com/isseis/tlsrpt-digest/internal/notify"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func captureWarnPayload(t *testing.T) []byte {
	t.Helper()

	var recv []byte
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var err error
		recv, err = io.ReadAll(r.Body)
		require.NoError(t, err)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	h, err := notify.NewSlackHandler(notify.SlackHandlerOptions{
		WebhookURL:    config.Secret(srv.URL + "/webhook"),
		AllowedHost:   "127.0.0.1",
		RunID:         "run-msg-test",
		LevelMode:     notify.LevelModeWarnAndAbove,
		HTTPClient:    srv.Client(),
		BackoffConfig: notify.DefaultBackoffConfig,
	})
	require.NoError(t, err)

	require.NoError(t, notify.LogAlert(context.Background(), h, notify.Alert{
		OrganizationName: "example.com",
		PolicyType:       notify.PolicyTypeSTS,
		FailureCount:     1,
		DateRange: notify.DateRange{
			Start: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
			End:   time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC),
		},
	}))
	require.NoError(t, h.Flush(context.Background()))
	require.NotEmpty(t, recv)

	return recv
}

func TestSlackMessage_JSONShape(t *testing.T) {
	raw := captureWarnPayload(t)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(raw, &payload))

	text, ok := payload["text"].(string)
	require.True(t, ok)
	assert.NotEmpty(t, text)

	attachments, ok := payload["attachments"].([]any)
	require.True(t, ok)
	require.NotEmpty(t, attachments)
}

func TestSlackAttachment_FieldsEncoding(t *testing.T) {
	raw := captureWarnPayload(t)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(raw, &payload))

	attachments, ok := payload["attachments"].([]any)
	require.True(t, ok)
	require.NotEmpty(t, attachments)

	attachment, ok := attachments[0].(map[string]any)
	require.True(t, ok)

	fields, ok := attachment["fields"].([]any)
	require.True(t, ok)
	require.NotEmpty(t, fields)

	firstField, ok := fields[0].(map[string]any)
	require.True(t, ok)
	assert.Contains(t, firstField, "title")
	assert.Contains(t, firstField, "value")
	assert.Contains(t, firstField, "short")
}
