package notify_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
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

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

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
	var sleeps []time.Duration
	srv, client := newTLSTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
		} else {
			w.WriteHeader(http.StatusOK)
		}
	}))

	opts := retryOpts(srv.URL, client)
	opts = notify.WithSleepFunc(opts, func(_ context.Context, d time.Duration) error {
		sleeps = append(sleeps, d)
		return nil
	})

	h := mustNewHandler(t, opts)
	require.NoError(t, h.Handle(context.Background(), warnRecord("test")))
	require.NoError(t, h.Flush(context.Background()))
	assert.Equal(t, int32(2), calls.Load())
	assert.Empty(t, sleeps, "Retry-After: 0 must retry immediately without exponential backoff")
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
	var calls atomic.Int32
	client := &http.Client{
		Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
			calls.Add(1)
			return nil, &net.DNSError{IsTimeout: true, Err: "dial timeout", Name: "hooks.slack.com"}
		}),
	}

	opts := notify.WithNoOpSleep(notify.SlackHandlerOptions{
		WebhookURL:    config.Secret("https://hooks.slack.com/services/retry"),
		AllowedHost:   "hooks.slack.com",
		RunID:         "test",
		LevelMode:     notify.LevelModeWarnAndAbove,
		HTTPClient:    client,
		BackoffConfig: notify.BackoffConfig{Base: 2 * time.Second, RetryCount: 2},
	})
	h := mustNewHandler(t, opts)
	require.NoError(t, h.Handle(context.Background(), warnRecord("test")))
	err := h.Flush(context.Background())
	require.Error(t, err)
	_, ok := errors.AsType[*notify.SlackServerError](err)
	assert.True(t, ok)
	assert.Equal(t, int32(3), calls.Load(), "must attempt initial request + 2 retries")
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
	_, ok := errors.AsType[*notify.SlackClientError](err)
	require.True(t, ok)
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
	_, ok := errors.AsType[*notify.SlackServerError](err)
	assert.True(t, ok)
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
	var sleeps []time.Duration
	srv, client := newTLSTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.Header().Set("Retry-After", "9999")
		w.WriteHeader(http.StatusTooManyRequests)
	}))

	opts := retryOpts(srv.URL, client)
	opts.BackoffConfig.RetryCount = 3
	opts = notify.WithSleepFunc(opts, func(_ context.Context, d time.Duration) error {
		sleeps = append(sleeps, d)
		return nil
	})
	h := mustNewHandler(t, opts)
	require.NoError(t, h.Handle(context.Background(), warnRecord("test")))
	err := h.Flush(context.Background())
	require.Error(t, err)
	// Retry-After=9999 is capped to maxCumulativeWait (14s). One retry is
	// allowed (0+14=14, not > 14), then 14+14=28 > 14 aborts. Exactly 2 requests.
	assert.Equal(t, int32(2), calls.Load())
	assert.Equal(t, []time.Duration{14 * time.Second}, sleeps)
}

func TestHTTPPost_CumulativeWaitBoundary_ContinueAt14StopBeyond14(t *testing.T) {
	var calls atomic.Int32
	srv, client := newTLSTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		switch calls.Add(1) {
		case 1:
			w.Header().Set("Retry-After", "29")
			w.WriteHeader(http.StatusTooManyRequests)
		case 2:
			w.Header().Set("Retry-After", "2")
			w.WriteHeader(http.StatusTooManyRequests)
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))

	var sleeps []time.Duration
	var cumulativeSleep time.Duration

	opts := notify.SlackHandlerOptions{
		WebhookURL:    config.Secret(srv.URL + "/webhook"),
		AllowedHost:   "127.0.0.1",
		RunID:         "test",
		LevelMode:     notify.LevelModeWarnAndAbove,
		HTTPClient:    client,
		BackoffConfig: notify.BackoffConfig{Base: 0, RetryCount: 3},
	}
	opts = notify.WithSleepFunc(opts, func(_ context.Context, d time.Duration) error {
		if d > 0 {
			sleeps = append(sleeps, d)
			cumulativeSleep += d
		}
		return nil
	})

	h := mustNewHandler(t, opts)
	require.NoError(t, h.Handle(context.Background(), warnRecord("test")))
	err := h.Flush(context.Background())
	require.Error(t, err)

	// First retry waits 14 seconds and continues to the second attempt.
	assert.Equal(t, []time.Duration{14 * time.Second}, sleeps)
	assert.Equal(t, 14*time.Second, cumulativeSleep)
	// The second Retry-After would push cumulative wait beyond 14 seconds,
	// so the retry loop stops before sleeping or making another request.
	assert.Equal(t, int32(2), calls.Load())
}

func TestHTTPPost_LastAttemptRetryAfterDoesNotSleep(t *testing.T) {
	var calls atomic.Int32
	var sleeps []time.Duration
	srv, client := newTLSTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.Header().Set("Retry-After", "10")
		w.WriteHeader(http.StatusTooManyRequests)
	}))

	opts := retryOpts(srv.URL, client)
	opts.BackoffConfig.RetryCount = 0
	opts = notify.WithSleepFunc(opts, func(_ context.Context, d time.Duration) error {
		sleeps = append(sleeps, d)
		return nil
	})
	h := mustNewHandler(t, opts)
	require.NoError(t, h.Handle(context.Background(), warnRecord("test")))
	err := h.Flush(context.Background())
	require.Error(t, err)
	assert.Equal(t, int32(1), calls.Load())
	assert.Empty(t, sleeps, "final attempt must not sleep when no retry remains")
}

func TestHTTPPost_ExponentialBackoffBoundary_ContinueAt14StopBeyond14(t *testing.T) {
	var calls atomic.Int32
	srv, client := newTLSTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		// 429 without Retry-After forces exponential backoff.
		w.WriteHeader(http.StatusTooManyRequests)
	}))

	var sleeps []time.Duration
	var cumulativeSleep time.Duration

	opts := notify.SlackHandlerOptions{
		WebhookURL:    config.Secret(srv.URL + "/webhook"),
		AllowedHost:   "127.0.0.1",
		RunID:         "test",
		LevelMode:     notify.LevelModeWarnAndAbove,
		HTTPClient:    client,
		BackoffConfig: notify.BackoffConfig{Base: 2 * time.Second, RetryCount: 5},
	}
	opts = notify.WithSleepFunc(opts, func(_ context.Context, d time.Duration) error {
		if d > 0 {
			sleeps = append(sleeps, d)
			cumulativeSleep += d
		}
		return nil
	})

	h := mustNewHandler(t, opts)
	require.NoError(t, h.Handle(context.Background(), warnRecord("test")))
	err := h.Flush(context.Background())
	require.Error(t, err)

	// maxCumulativeWait = 14s. Exponential waits: 2+4+8 = 14 (fits exactly).
	// Next backoff (16s, capped to 14s) would push cumulative to 28 > 14, so
	// the loop stops before sleeping again.
	assert.Equal(t, []time.Duration{2 * time.Second, 4 * time.Second, 8 * time.Second}, sleeps)
	assert.Equal(t, 14*time.Second, cumulativeSleep)
	// Requests: initial + 3 retries that fit the cap.
	assert.Equal(t, int32(4), calls.Load())
}
