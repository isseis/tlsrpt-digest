package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

const (
	requestTimeout    = 5 * time.Second
	maxCumulativeWait = 14 * time.Second
)

// sleepFunc is the sleep abstraction injected in tests to avoid real waits.
type sleepFunc func(ctx context.Context, d time.Duration) error

// realSleep is the production sleep implementation.
func realSleep(ctx context.Context, d time.Duration) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(d):
		return nil
	}
}

// postConfig holds the parameters for a single webhook POST attempt session.
type postConfig struct {
	client     *http.Client
	backoff    BackoffConfig
	sleep      sleepFunc
	webhookURL string        // raw URL, not logged in errors
	maskedURL  string        // redacted representation for error messages
	reqTimeout time.Duration // per-request deadline override; 0 uses requestTimeout (5s)
}

// postWithRetry sends payload as JSON to webhookURL with retry logic.
// The 5-second per-request timeout is enforced via context regardless of the
// injected client's Timeout setting.
func postWithRetry(ctx context.Context, cfg postConfig, payload slackMessage) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("notify: marshal payload: %w", err)
	}

	backoff := cfg.backoff
	if backoff.Base == 0 && backoff.RetryCount == 0 {
		backoff = DefaultBackoffConfig
	}
	sleep := cfg.sleep
	if sleep == nil {
		sleep = realSleep
	}

	client := cfg.client
	if client == nil {
		client = &http.Client{}
	}

	var lastErr error
	cumulativeWait := time.Duration(0)
	// retryAfterHandled is set when a Retry-After sleep was already performed for
	// this attempt, so the next iteration skips the normal exponential backoff sleep.
	retryAfterHandled := false

loop:
	for attempt := 0; attempt <= backoff.RetryCount; attempt++ {
		if attempt > 0 && !retryAfterHandled {
			wait := backoffDuration(backoff.Base, attempt-1)
			if cumulativeWait+wait > maxCumulativeWait {
				break loop
			}
			if err := sleep(ctx, wait); err != nil {
				return err
			}
			cumulativeWait += wait
		}
		retryAfterHandled = false

		done, retryWait, retryAfterSeen, err := doAttempt(ctx, client, cfg, body)
		if done {
			return err
		}
		if err != nil {
			lastErr = err
		}
		if retryAfterSeen {
			if attempt == backoff.RetryCount {
				break loop
			}
			if retryWait > 0 {
				if cumulativeWait+retryWait > maxCumulativeWait {
					break loop
				}
				if sleepErr := sleep(ctx, retryWait); sleepErr != nil {
					return sleepErr
				}
				cumulativeWait += retryWait
			}
			retryAfterHandled = true
			continue
		}
	}

	if lastErr == nil {
		lastErr = &SlackServerError{}
	}
	return fmt.Errorf("notify: all retries exhausted for %s: %w", cfg.maskedURL, lastErr)
}

// postResult holds the outcome of a single HTTP POST attempt.
type postResult struct {
	statusCode int
	retryAfter string // value of Retry-After header, empty if absent
}

// doAttempt performs one HTTP POST attempt and classifies the outcome.
// Returns: done=true means stop the loop; err carries the result; retryWait>0
// means sleep that long before the next attempt (Retry-After case).

func doAttempt(ctx context.Context, client *http.Client, cfg postConfig, body []byte) (done bool, retryWait time.Duration, retryAfterSeen bool, err error) {
	if err := ctx.Err(); err != nil {
		return true, 0, false, err
	}
	timeout := cfg.reqTimeout
	if timeout == 0 {
		timeout = requestTimeout
	}
	reqCtx, cancel := context.WithTimeout(ctx, timeout)
	result, postErr := doPost(reqCtx, client, cfg.webhookURL, body)
	cancel()

	if postErr != nil {
		// Network-level failure: signal retry with no extra wait.
		return false, 0, false, &SlackServerError{StatusCode: 0, Cause: sanitizeRequestError(postErr)}
	}

	sc := result.statusCode
	if sc >= 200 && sc < 300 {
		return true, 0, false, nil
	}

	switch {
	case sc == http.StatusTooManyRequests || sc >= 500:
		if sc == http.StatusTooManyRequests {
			if d, ok := parseRetryAfter(result.retryAfter); ok {
				// Signal caller to sleep d before next attempt.
				return false, d, true, &SlackServerError{StatusCode: sc}
			}
		}
		return false, 0, false, &SlackServerError{StatusCode: sc}
	default:
		return true, 0, false, &SlackClientError{StatusCode: sc}
	}
}

// sanitizeRequestError hides webhook URLs from request errors while preserving
// the underlying cause for debugging.
func sanitizeRequestError(err error) error {
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return fmt.Errorf("%s [redacted]: %w", urlErr.Op, urlErr.Err)
	}
	return err
}

// doPost sends a single HTTP POST request and returns status code and headers.
// It always closes the response body.
func doPost(ctx context.Context, client *http.Client, webhookURL string, body []byte) (postResult, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, webhookURL, bytes.NewReader(body))
	if err != nil {
		return postResult{}, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req) //nolint:bodyclose // closed below
	if err != nil {
		return postResult{}, err
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	return postResult{
		statusCode: resp.StatusCode,
		retryAfter: resp.Header.Get("Retry-After"),
	}, nil
}

const maxBackoffShift = 30 // cap to prevent int overflow: 1<<30 ~= 1 billion

// backoffDuration computes 2^attempt * base, capped at maxCumulativeWait.
func backoffDuration(base time.Duration, attempt int) time.Duration {
	if attempt > maxBackoffShift {
		attempt = maxBackoffShift
	}
	d := base * (1 << attempt) //nolint:mnd
	if d > maxCumulativeWait {
		return maxCumulativeWait
	}
	return d
}

// maxRetryAfterSeconds caps Retry-After to prevent overflow in time.Duration arithmetic.
const maxRetryAfterSeconds = int(maxCumulativeWait / time.Second)

// parseRetryAfter parses the Retry-After header value (integer seconds only).
// Returns 0 and false when the value is absent or unparseable.
func parseRetryAfter(header string) (time.Duration, bool) {
	if header == "" {
		return 0, false
	}
	secs, err := strconv.Atoi(header)
	if err != nil || secs < 0 {
		return 0, false
	}
	if secs > maxRetryAfterSeconds {
		secs = maxRetryAfterSeconds
	}
	return time.Duration(secs) * time.Second, true
}

// maskedWebhookURL returns a redacted version of the webhook URL for use in
// error messages so that the token is never logged.
func maskedWebhookURL(_ string) string {
	return "[webhook URL redacted]"
}
