// Package notify provides Slack notification delivery via Incoming Webhooks.
package notify

import "fmt"

// WebhookValidationError is returned when a webhook URL or environment variable
// combination fails validation during startup.
type WebhookValidationError struct {
	Msg string
}

func (e *WebhookValidationError) Error() string {
	return fmt.Sprintf("webhook validation error: %s", e.Msg)
}

// SlackServerError is returned when the Slack API returns a retryable error
// (HTTP 5xx, 429) or when a request cannot be sent and all retry attempts are
// exhausted.
type SlackServerError struct {
	StatusCode int // 0 when the request itself failed (e.g. timeout)
	Cause      error
}

func (e *SlackServerError) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("slack server error (status %d): %s", e.StatusCode, e.Cause)
	}
	return fmt.Sprintf("slack server error (status %d)", e.StatusCode)
}

func (e *SlackServerError) Unwrap() error { return e.Cause }

// SlackClientError is returned when the Slack API returns a non-retryable 4xx
// response (excluding 429).
type SlackClientError struct {
	StatusCode int
}

func (e *SlackClientError) Error() string {
	return fmt.Sprintf("slack client error (status %d)", e.StatusCode)
}
