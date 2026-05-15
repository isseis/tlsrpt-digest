package notify_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/isseis/tlsrpt-digest/internal/config"
	"github.com/isseis/tlsrpt-digest/internal/notify"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTLSTestServer creates a TLS test server and returns it with a pre-configured client.
func newTLSTestServer(t *testing.T, h http.Handler) (*httptest.Server, *http.Client) {
	t.Helper()
	srv := httptest.NewTLSServer(h)
	t.Cleanup(srv.Close)
	return srv, srv.Client()
}

// mustNewHandler creates a SlackHandler or fails the test.
func mustNewHandler(t *testing.T, opts notify.SlackHandlerOptions) *notify.SlackHandler {
	t.Helper()
	h, err := notify.NewSlackHandler(opts)
	require.NoError(t, err)
	return h
}

// warnRecord returns a minimal WARN slog.Record with a fixed message.
func warnRecord(_ string) slog.Record {
	return slog.NewRecord(time.Now(), slog.LevelWarn, "test notification", 0)
}

// retryOpts returns a base SlackHandlerOptions for retry tests using a TLS server.
// Backoff uses realistic production values (2s base, 3 retries) combined with
// a no-op sleep function so tests do not actually wait between retries.
func retryOpts(serverURL string, client *http.Client) notify.SlackHandlerOptions {
	base := notify.SlackHandlerOptions{
		WebhookURL:    config.Secret(serverURL + "/webhook"),
		AllowedHost:   "127.0.0.1",
		RunID:         "test",
		LevelMode:     notify.LevelModeWarnAndAbove,
		HTTPClient:    client,
		BackoffConfig: notify.DefaultBackoffConfig, // 2s base, 3 retries
	}
	return notify.WithNoOpSleep(base)
}

func TestHTTPPost_Timeout(t *testing.T) {
	// Server sleeps, which causes the per-request timeout to fire.
	// We close client connections before srv.Close to avoid the cleanup hang.
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Drain the request body so the server can close connections cleanly.
		_, _ = io.Copy(io.Discard, r.Body)
		select {
		case <-r.Context().Done():
		case <-time.After(30 * time.Second):
		}
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	client := srv.Client()
	t.Cleanup(srv.Close)

	base := retryOpts(srv.URL, client)
	base.BackoffConfig.RetryCount = 0
	opts := notify.WithRequestTimeout(base, 50*time.Millisecond)

	h := mustNewHandler(t, opts)
	require.NoError(t, h.Handle(context.Background(), warnRecord("test")))
	require.Error(t, h.Flush(context.Background()))
}

func TestHTTPPost_5xxRetry(t *testing.T) {
	var calls atomic.Int32
	srv, client := newTLSTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) < 3 {
			w.WriteHeader(http.StatusInternalServerError)
		} else {
			w.WriteHeader(http.StatusOK)
		}
	}))

	h := mustNewHandler(t, retryOpts(srv.URL, client))
	require.NoError(t, h.Handle(context.Background(), warnRecord("test")))
	require.NoError(t, h.Flush(context.Background()))
	assert.GreaterOrEqual(t, calls.Load(), int32(3))
}

func TestHTTPPost_429WithRetryAfter(t *testing.T) {
	var calls atomic.Int32
	srv, client := newTLSTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
		} else {
			w.WriteHeader(http.StatusOK)
		}
	}))

	h := mustNewHandler(t, retryOpts(srv.URL, client))
	require.NoError(t, h.Handle(context.Background(), warnRecord("test")))
	require.NoError(t, h.Flush(context.Background()))
	assert.Equal(t, int32(2), calls.Load())
}

func TestHTTPPost_429WithoutRetryAfter(t *testing.T) {
	var calls atomic.Int32
	srv, client := newTLSTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) < 2 {
			w.WriteHeader(http.StatusTooManyRequests)
		} else {
			w.WriteHeader(http.StatusOK)
		}
	}))

	h := mustNewHandler(t, retryOpts(srv.URL, client))
	require.NoError(t, h.Handle(context.Background(), warnRecord("test")))
	require.NoError(t, h.Flush(context.Background()))
}

func TestHTTPPost_RequestFailureRetry(t *testing.T) {
	srv, client := newTLSTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	url := srv.URL + "/webhook"
	srv.Close()

	opts := notify.WithNoOpSleep(notify.SlackHandlerOptions{
		WebhookURL:    config.Secret(url),
		AllowedHost:   "127.0.0.1",
		RunID:         "test",
		LevelMode:     notify.LevelModeWarnAndAbove,
		HTTPClient:    client,
		BackoffConfig: notify.BackoffConfig{Base: 2 * time.Second, RetryCount: 2},
	})
	h := mustNewHandler(t, opts)
	require.NoError(t, h.Handle(context.Background(), warnRecord("test")))
	err := h.Flush(context.Background())
	require.Error(t, err)
	var se *notify.SlackServerError
	assert.True(t, errors.As(err, &se))
}

func TestHTTPPost_4xxImmediate(t *testing.T) {
	var calls atomic.Int32
	srv, client := newTLSTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusBadRequest)
	}))

	h := mustNewHandler(t, retryOpts(srv.URL, client))
	require.NoError(t, h.Handle(context.Background(), warnRecord("test")))
	err := h.Flush(context.Background())
	require.Error(t, err)
	var ce *notify.SlackClientError
	require.True(t, errors.As(err, &ce))
	assert.Equal(t, int32(1), calls.Load(), "must not retry on non-retryable 4xx")
}

func TestHTTPPost_AllRetriesExhausted(t *testing.T) {
	srv, client := newTLSTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))

	opts := retryOpts(srv.URL, client)
	opts.BackoffConfig.RetryCount = 2
	h := mustNewHandler(t, opts)
	require.NoError(t, h.Handle(context.Background(), warnRecord("test")))
	err := h.Flush(context.Background())
	require.Error(t, err)
	var se *notify.SlackServerError
	assert.True(t, errors.As(err, &se))
}

func TestHTTPPost_ContextCancel(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		select {
		case <-r.Context().Done():
		case <-time.After(30 * time.Second):
		}
		w.WriteHeader(http.StatusOK)
	}))
	client := srv.Client()
	t.Cleanup(srv.Close)

	h := mustNewHandler(t, retryOpts(srv.URL, client))
	ctx, cancel := context.WithCancel(context.Background())
	require.NoError(t, h.Handle(ctx, warnRecord("test")))
	go func() { time.Sleep(50 * time.Millisecond); cancel() }()
	require.Error(t, h.Flush(ctx))
}

func TestHTTPPost_ResponseBodyClosed(t *testing.T) {
	var calls atomic.Int32
	srv, client := newTLSTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) < 2 {
			w.WriteHeader(http.StatusInternalServerError)
		} else {
			w.WriteHeader(http.StatusOK)
		}
	}))

	h := mustNewHandler(t, retryOpts(srv.URL, client))
	require.NoError(t, h.Handle(context.Background(), warnRecord("test")))
	// Success implies the server wasn't stalled by unclosed bodies.
	require.NoError(t, h.Flush(context.Background()))
}

func TestHTTPPost_RetryAfterCapped(t *testing.T) {
	var calls atomic.Int32
	srv, client := newTLSTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.Header().Set("Retry-After", "9999")
		w.WriteHeader(http.StatusTooManyRequests)
	}))

	opts := retryOpts(srv.URL, client)
	opts.BackoffConfig.RetryCount = 3
	h := mustNewHandler(t, opts)
	require.NoError(t, h.Handle(context.Background(), warnRecord("test")))
	err := h.Flush(context.Background())
	require.Error(t, err)
	// Oversized Retry-After should be capped; only one attempt made.
	assert.Equal(t, int32(1), calls.Load())
}
