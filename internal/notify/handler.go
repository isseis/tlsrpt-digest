package notify

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"

	"github.com/isseis/tlsrpt-digest/internal/config"
)

// SlackHandler implements slog.Handler and Flusher.
// Handle() buffers records; Flush() formats and sends them to Slack.
type SlackHandler struct {
	opts SlackHandlerOptions
	mu   sync.Mutex
	buf  []slog.Record
}

// Compile-time interface checks.
var (
	_ slog.Handler = (*SlackHandler)(nil)
	_ Flusher      = (*SlackHandler)(nil)
)

// NewSlackHandler creates a validated SlackHandler.
// URL validation is skipped when opts.IsDryRun is true and opts.WebhookURL is empty.
func NewSlackHandler(opts SlackHandlerOptions) (*SlackHandler, error) {
	if !opts.IsDryRun || opts.WebhookURL.Value() != "" {
		if err := validateWebhookURL(opts.WebhookURL.Value(), opts.AllowedHost); err != nil {
			return nil, err
		}
	}
	if opts.BackoffConfig.Base == 0 && opts.BackoffConfig.RetryCount == 0 {
		opts.BackoffConfig = DefaultBackoffConfig
	}
	return &SlackHandler{opts: opts}, nil
}

// newSlackHandlerInternal creates a SlackHandler without URL validation.
// Used by BuildHandlers which has already validated URLs.
func newSlackHandlerInternal(opts SlackHandlerOptions) (*SlackHandler, error) {
	if opts.BackoffConfig.Base == 0 && opts.BackoffConfig.RetryCount == 0 {
		opts.BackoffConfig = DefaultBackoffConfig
	}
	return &SlackHandler{opts: opts}, nil
}

// newDryRunHandler creates a dry-run handler that logs payloads to DebugLogger
// without sending HTTP requests. Used by BuildHandlers DryRunNoURL mode.
func newDryRunHandler(opts SlackHandlerOptions) (*SlackHandler, error) {
	opts.IsDryRun = true
	return newSlackHandlerInternal(opts)
}

// Enabled reports whether this handler accepts records at the given level.
// The decision is based solely on LevelMode, independent of any CLI log-level setting.
func (h *SlackHandler) Enabled(_ context.Context, level slog.Level) bool {
	switch h.opts.LevelMode {
	case LevelModeExactInfo:
		return level == slog.LevelInfo
	case LevelModeWarnAndAbove:
		return level >= slog.LevelWarn
	default:
		return level >= slog.LevelInfo
	}
}

// Handle buffers the record for later delivery by Flush().
// It clones the record to avoid shared backing-store issues.
func (h *SlackHandler) Handle(_ context.Context, r slog.Record) error {
	clone := r.Clone()
	h.mu.Lock()
	h.buf = append(h.buf, clone)
	h.mu.Unlock()
	return nil
}

// WithAttrs returns h unchanged. Slack notifications are written only through
// typed helpers and do not use With-based attribute accumulation.
func (h *SlackHandler) WithAttrs(_ []slog.Attr) slog.Handler { return h }

// WithGroup returns h unchanged (same rationale as WithAttrs).
func (h *SlackHandler) WithGroup(_ string) slog.Handler { return h }

// Flush formats and sends all buffered records.
// It uses a snapshot strategy: records buffered during an in-flight Flush are
// preserved for the next call rather than dropped.
// Flush always clears its snapshot regardless of send errors.
func (h *SlackHandler) Flush(ctx context.Context) error {
	// Snapshot and clear under lock so Handle() can continue unblocked.
	h.mu.Lock()
	snapshot := h.buf
	h.buf = nil
	h.mu.Unlock()

	if len(snapshot) == 0 {
		return nil
	}

	if h.opts.IsDryRun {
		h.logDryRun(snapshot)
		return nil
	}

	return h.send(ctx, snapshot)
}

// send formats buffered records and delivers them to the webhook.
// Format → log to DebugLogger (full text) → truncate → POST.
func (h *SlackHandler) send(ctx context.Context, records []slog.Record) error {
	msg := buildMessage(records, h.opts.RunID)

	// Log full (untruncated) payload to DebugLogger before truncation.
	if h.opts.DebugLogger != nil {
		if raw, err := json.Marshal(msg); err == nil {
			h.opts.DebugLogger.Debug("slack notification payload", "payload", string(raw))
		}
	}

	truncateMessage(&msg)

	cfg := postConfig{
		client:     h.opts.HTTPClient,
		backoff:    h.opts.BackoffConfig,
		webhookURL: h.opts.WebhookURL.Value(),
		maskedURL:  maskedWebhookURL(h.opts.WebhookURL.Value()),
		reqTimeout: h.opts.testReqTimeout,
	}
	if err := postWithRetry(ctx, cfg, msg); err != nil {
		if h.opts.DebugLogger != nil {
			h.opts.DebugLogger.Error("slack notification failed", "masked_url", cfg.maskedURL, "error", err)
		}
		return fmt.Errorf("notify: send failed: %w", stripURLFromError(err))
	}
	return nil
}

// logDryRun writes the formatted payload to DebugLogger without sending.
func (h *SlackHandler) logDryRun(records []slog.Record) {
	msg := buildMessage(records, h.opts.RunID)
	if h.opts.DebugLogger != nil {
		if raw, err := json.Marshal(msg); err == nil {
			h.opts.DebugLogger.Debug("[dry-run] slack notification would send", "payload", string(raw))
		}
	}
}

// stripURLFromError wraps err with a message that does not expose the webhook URL.
func stripURLFromError(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("delivery error (webhook URL redacted): %w", err)
}

// buildMessage converts buffered slog.Records to a slackMessage.
// Actual formatting logic is in format.go.
func buildMessage(records []slog.Record, runID string) slackMessage {
	return formatRecords(records, runID)
}

// BuildHandlers validates URLs and returns 0–2 SlackHandler instances.
// DryRunNoURL mode: IsDryRun=true and both URLs empty — skips validation,
// returns a single DebugLogger-only handler.
func BuildHandlers(successURL, errorURL, allowedHost string, opts SlackHandlerOptions) ([]*SlackHandler, error) {
	// DryRunNoURL mode.
	if opts.IsDryRun && successURL == "" && errorURL == "" {
		opts.LevelMode = LevelModeWarnAndAbove
		h, err := newDryRunHandler(opts)
		if err != nil {
			return nil, err
		}
		return []*SlackHandler{h}, nil
	}

	if err := ValidateEnvCombination(successURL, errorURL); err != nil {
		return nil, err
	}
	if successURL == "" && errorURL == "" {
		return nil, nil
	}

	if successURL != "" && errorURL != "" {
		if err := validateBothURLs(successURL, errorURL, allowedHost); err != nil {
			return nil, err
		}
	} else if errorURL != "" {
		if err := validateWebhookURL(errorURL, allowedHost); err != nil {
			return nil, err
		}
	}

	var handlers []*SlackHandler

	if successURL != "" {
		o := opts
		o.WebhookURL = config.Secret(successURL)
		o.LevelMode = LevelModeExactInfo
		h, err := newSlackHandlerInternal(o)
		if err != nil {
			return nil, err
		}
		handlers = append(handlers, h)
	}

	if errorURL != "" {
		o := opts
		o.WebhookURL = config.Secret(errorURL)
		o.LevelMode = LevelModeWarnAndAbove
		h, err := newSlackHandlerInternal(o)
		if err != nil {
			return nil, err
		}
		handlers = append(handlers, h)
	}

	return handlers, nil
}
