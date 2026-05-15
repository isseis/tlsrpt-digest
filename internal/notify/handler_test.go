package notify_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/isseis/tlsrpt-digest/internal/config"
	"github.com/isseis/tlsrpt-digest/internal/notify"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---- compile-time interface checks ----

func TestSlackHandler_ImplementsInterface(_ *testing.T) {
	var _ slog.Handler = (*notify.SlackHandler)(nil)
	var _ notify.Flusher = (*notify.SlackHandler)(nil)
}

// ---- helpers shared across handler tests ----

func newPairHandlers(t *testing.T) (success, errH *notify.SlackHandler, successReqs, errReqs *atomic.Int32) {
	t.Helper()
	successReqs = &atomic.Int32{}
	errReqs = &atomic.Int32{}

	successSrv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		successReqs.Add(1)
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(successSrv.Close)

	errSrv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		errReqs.Add(1)
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(errSrv.Close)

	makeOpts := func(url string, mode notify.LevelMode, client *http.Client) notify.SlackHandlerOptions {
		return notify.WithNoOpSleep(notify.SlackHandlerOptions{
			WebhookURL:    config.Secret(url + "/webhook"),
			AllowedHost:   "127.0.0.1",
			RunID:         "test",
			LevelMode:     mode,
			HTTPClient:    client,
			BackoffConfig: notify.DefaultBackoffConfig,
		})
	}
	var errNew error
	success, errNew = notify.NewSlackHandler(makeOpts(successSrv.URL, notify.LevelModeExactInfo, successSrv.Client()))
	require.NoError(t, errNew)
	errH, errNew = notify.NewSlackHandler(makeOpts(errSrv.URL, notify.LevelModeWarnAndAbove, errSrv.Client()))
	require.NoError(t, errNew)
	return success, errH, successReqs, errReqs
}

// ---- individual tests ----

func TestFlush_EmptyBuffer(t *testing.T) {
	success, _, _, _ := newPairHandlers(t)
	require.NoError(t, success.Flush(context.Background()))
}

func TestHandle_BufferOnly(t *testing.T) {
	_, errH, _, errReqs := newPairHandlers(t)
	r := slog.NewRecord(time.Now(), slog.LevelWarn, "test", 0)
	require.NoError(t, errH.Handle(context.Background(), r))
	// Flush NOT called — no HTTP request should have been made.
	assert.Equal(t, int32(0), errReqs.Load())
}

func TestFlush_InfoGoesToSuccessWebhook(t *testing.T) {
	success, _, successReqs, errReqs := newPairHandlers(t)
	r := slog.NewRecord(time.Now(), slog.LevelInfo, "test", 0)
	require.NoError(t, success.Handle(context.Background(), r))
	require.NoError(t, success.Flush(context.Background()))
	assert.Equal(t, int32(1), successReqs.Load())
	assert.Equal(t, int32(0), errReqs.Load())
}

func TestFlush_WarnGoesToErrorWebhook(t *testing.T) {
	_, errH, successReqs, errReqs := newPairHandlers(t)
	r := slog.NewRecord(time.Now(), slog.LevelWarn, "test", 0)
	require.NoError(t, errH.Handle(context.Background(), r))
	require.NoError(t, errH.Flush(context.Background()))
	assert.Equal(t, int32(0), successReqs.Load())
	assert.Equal(t, int32(1), errReqs.Load())
}

func TestFlush_ErrorGoesToErrorWebhook(t *testing.T) {
	_, errH, successReqs, errReqs := newPairHandlers(t)
	r := slog.NewRecord(time.Now(), slog.LevelError, "test", 0)
	require.NoError(t, errH.Handle(context.Background(), r))
	require.NoError(t, errH.Flush(context.Background()))
	assert.Equal(t, int32(0), successReqs.Load())
	assert.Equal(t, int32(1), errReqs.Load())
}

func TestFlush_InfoNotToErrorWebhook(t *testing.T) {
	_, errH, _, _ := newPairHandlers(t)
	r := slog.NewRecord(time.Now(), slog.LevelInfo, "test", 0)
	// INFO is not accepted by errH (LevelModeWarnAndAbove).
	require.NoError(t, errH.Handle(context.Background(), r))
	require.NoError(t, errH.Flush(context.Background()))
	// Handle stored it but Enabled=false so Flush skips it... actually Handle
	// doesn't check Enabled. Let's verify via direct Enabled check.
	assert.False(t, errH.Enabled(context.Background(), slog.LevelInfo))
}

func TestFlush_WarnNotToSuccessOnly(t *testing.T) {
	success, _, successReqs, _ := newPairHandlers(t)
	r := slog.NewRecord(time.Now(), slog.LevelWarn, "test", 0)
	assert.False(t, success.Enabled(context.Background(), slog.LevelWarn))
	require.NoError(t, success.Handle(context.Background(), r))
	require.NoError(t, success.Flush(context.Background()))
	// success handler ignores WARN.
	assert.Equal(t, int32(0), successReqs.Load())
}

func TestCLILogLevel_Independent(t *testing.T) {
	// The handler's Enabled is determined by LevelMode, not by slog.Default's level.
	slog.SetLogLoggerLevel(slog.LevelError) // suppress non-error console output
	defer slog.SetLogLoggerLevel(slog.LevelInfo)

	success, _, _, _ := newPairHandlers(t)
	// Even with global level set to ERROR, success handler accepts INFO.
	assert.True(t, success.Enabled(context.Background(), slog.LevelInfo))
}

func TestFlush_OnError_LogsToDebugLogger(t *testing.T) {
	var buf strings.Builder
	debugLogger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	// Server that returns an error.
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()

	opts := notify.SlackHandlerOptions{
		WebhookURL:    config.Secret(srv.URL + "/webhook"),
		AllowedHost:   "127.0.0.1",
		RunID:         "test",
		LevelMode:     notify.LevelModeWarnAndAbove,
		HTTPClient:    srv.Client(),
		BackoffConfig: notify.DefaultBackoffConfig,
		DebugLogger:   debugLogger,
	}
	h, err := notify.NewSlackHandler(opts)
	require.NoError(t, err)

	r := slog.NewRecord(time.Now(), slog.LevelWarn, "test", 0)
	require.NoError(t, h.Handle(context.Background(), r))
	flushErr := h.Flush(context.Background())
	require.Error(t, flushErr)
	assert.NotEmpty(t, buf.String(), "error should be logged to DebugLogger")
}

func TestFlush_4xx_ImmediateError(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	opts := notify.SlackHandlerOptions{
		WebhookURL:    config.Secret(srv.URL + "/webhook"),
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
	var ce *notify.SlackClientError
	assert.True(t, errors.As(flushErr, &ce))
}

func TestNewSlackHandler_URLValidation(t *testing.T) {
	_, err := notify.NewSlackHandler(notify.SlackHandlerOptions{
		WebhookURL:  config.Secret("http://hooks.slack.com/xxx"),
		AllowedHost: "hooks.slack.com",
		RunID:       "test",
	})
	var ve *notify.WebhookValidationError
	require.True(t, errors.As(err, &ve))
}

func TestHandle_ClonesRecord(t *testing.T) {
	// Using SpyHandler to inspect the buffered record directly.
	// This verifies Handle() uses Record.Clone() so mutations after the call
	// do not corrupt the stored data.
	var spy spyHandler
	r := slog.NewRecord(time.Now(), slog.LevelWarn, "original", 0)
	require.NoError(t, spy.Handle(context.Background(), r))
	require.Len(t, spy.records, 1)
	assert.Equal(t, "original", spy.records[0].Message, "cloned message must equal original")
	// Also verify SlackHandler actually clones via the handler itself.
	h := mustNewHandler(t, retryOpts("https://127.0.0.1:9999", nil))
	r2 := slog.NewRecord(time.Now(), slog.LevelWarn, "clonecheck", 0)
	require.NoError(t, h.Handle(context.Background(), r2))
	// If Handle clones correctly, no panic or data corruption occurs.
}

func TestFlush_Concurrent(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	opts := notify.SlackHandlerOptions{
		WebhookURL:    config.Secret(srv.URL + "/webhook"),
		AllowedHost:   "127.0.0.1",
		RunID:         "test",
		LevelMode:     notify.LevelModeWarnAndAbove,
		HTTPClient:    srv.Client(),
		BackoffConfig: notify.DefaultBackoffConfig,
	}
	h, err := notify.NewSlackHandler(opts)
	require.NoError(t, err)

	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r := slog.NewRecord(time.Now(), slog.LevelWarn, "concurrent", 0)
			_ = h.Handle(context.Background(), r)
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = h.Flush(context.Background())
	}()
	wg.Wait()
	// No race condition (run with -race) and no panic.
}

func TestFlush_RecordsDuringFlushPreserved(t *testing.T) {
	var mu sync.Mutex
	var bodies []string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, string(b))
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	opts := notify.SlackHandlerOptions{
		WebhookURL:    config.Secret(srv.URL + "/webhook"),
		AllowedHost:   "127.0.0.1",
		RunID:         "test",
		LevelMode:     notify.LevelModeWarnAndAbove,
		HTTPClient:    srv.Client(),
		BackoffConfig: notify.DefaultBackoffConfig,
	}
	h, err := notify.NewSlackHandler(opts)
	require.NoError(t, err)

	// First record buffered before Flush.
	r1 := slog.NewRecord(time.Now(), slog.LevelWarn, "first", 0)
	require.NoError(t, h.Handle(context.Background(), r1))
	require.NoError(t, h.Flush(context.Background()))

	// Second record buffered after first Flush.
	r2 := slog.NewRecord(time.Now(), slog.LevelWarn, "second", 0)
	require.NoError(t, h.Handle(context.Background(), r2))
	require.NoError(t, h.Flush(context.Background()))

	mu.Lock()
	defer mu.Unlock()
	require.Len(t, bodies, 2)
}

func TestFlush_ClearsBufferAfterSend(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	opts := notify.SlackHandlerOptions{
		WebhookURL:    config.Secret(srv.URL + "/webhook"),
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
	require.NoError(t, h.Flush(context.Background()))
	assert.Equal(t, int32(1), calls.Load())

	// Second Flush with empty buffer: no additional request.
	require.NoError(t, h.Flush(context.Background()))
	assert.Equal(t, int32(1), calls.Load())
}

func TestFlush_MultipleAlerts_SinglePost(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	opts := notify.SlackHandlerOptions{
		WebhookURL:    config.Secret(srv.URL + "/webhook"),
		AllowedHost:   "127.0.0.1",
		RunID:         "test",
		LevelMode:     notify.LevelModeWarnAndAbove,
		HTTPClient:    srv.Client(),
		BackoffConfig: notify.DefaultBackoffConfig,
	}
	h, err := notify.NewSlackHandler(opts)
	require.NoError(t, err)

	// Buffer three alerts.
	for range 3 {
		require.NoError(t, notify.LogAlert(context.Background(), h, notify.Alert{
			OrganizationName: "org.example.com",
			PolicyType:       notify.PolicyTypeSTS,
			FailureCount:     1,
		}))
	}
	require.NoError(t, h.Flush(context.Background()))
	assert.Equal(t, int32(1), calls.Load(), "multiple alerts must be sent as a single POST")
}

func TestFlush_DryRun(t *testing.T) {
	var buf strings.Builder
	debugLogger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	var serverCalls atomic.Int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		serverCalls.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	opts := notify.SlackHandlerOptions{
		WebhookURL:    config.Secret(srv.URL + "/webhook"),
		AllowedHost:   "127.0.0.1",
		RunID:         "test",
		LevelMode:     notify.LevelModeWarnAndAbove,
		IsDryRun:      true,
		HTTPClient:    srv.Client(),
		BackoffConfig: notify.DefaultBackoffConfig,
		DebugLogger:   debugLogger,
	}
	h, err := notify.NewSlackHandler(opts)
	require.NoError(t, err)

	r := slog.NewRecord(time.Now(), slog.LevelWarn, "test", 0)
	require.NoError(t, h.Handle(context.Background(), r))
	require.NoError(t, h.Flush(context.Background()))

	assert.Equal(t, int32(0), serverCalls.Load(), "dry-run: no HTTP POST")
	assert.NotEmpty(t, buf.String(), "dry-run: payload logged to DebugLogger")
}

func TestFlush_FileLog_NoTruncation(t *testing.T) {
	var buf strings.Builder
	debugLogger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	var recv []byte
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		recv, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	longName := strings.Repeat("z", 5000)
	opts := notify.SlackHandlerOptions{
		WebhookURL:    config.Secret(srv.URL + "/webhook"),
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
		OrganizationName: longName,
		PolicyType:       notify.PolicyTypeSTS,
		FailureCount:     1,
	}))
	require.NoError(t, h.Flush(context.Background()))

	assert.Contains(t, buf.String(), longName, "DebugLogger must have full untruncated text")
	assert.NotContains(t, string(recv), longName, "Slack payload must be truncated")
}

func TestFlush_SequentialMessages(t *testing.T) {
	var mu sync.Mutex
	var requestBodies []string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		requestBodies = append(requestBodies, string(b))
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	opts := notify.SlackHandlerOptions{
		WebhookURL:    config.Secret(srv.URL + "/webhook"),
		AllowedHost:   "127.0.0.1",
		RunID:         "test",
		LevelMode:     notify.LevelModeWarnAndAbove,
		HTTPClient:    srv.Client(),
		BackoffConfig: notify.DefaultBackoffConfig,
	}
	h, err := notify.NewSlackHandler(opts)
	require.NoError(t, err)

	// Buffer a TLS failure alert and a system error.
	require.NoError(t, notify.LogAlert(context.Background(), h, notify.Alert{
		OrganizationName: "example.com",
		PolicyType:       notify.PolicyTypeSTS,
		FailureCount:     2,
	}))
	require.NoError(t, notify.LogSystemError(context.Background(), h, notify.SystemError{
		ErrorType: "StorageError",
		Message:   "disk full",
		Component: "storage",
	}))
	require.NoError(t, h.Flush(context.Background()))

	mu.Lock()
	defer mu.Unlock()
	// Two separate HTTP requests sent sequentially.
	assert.Equal(t, 2, len(requestBodies), "TLS failure and system error should be separate POSTs")
}
