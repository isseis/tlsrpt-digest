package notify

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/isseis/tlsrpt-digest/internal/config"
)

// LevelMode defines which log levels a SlackHandler forwards.
type LevelMode string

const (
	// LevelModeExactInfo forwards only INFO level records (success webhook).
	LevelModeExactInfo LevelMode = "exact_info"
	// LevelModeWarnAndAbove forwards WARN and ERROR level records (error webhook).
	LevelModeWarnAndAbove LevelMode = "warn_and_above"
)

// BackoffConfig defines exponential backoff parameters for HTTP retries.
type BackoffConfig struct {
	Base       time.Duration
	RetryCount int
}

const (
	defaultBackoffBase       = 2 * time.Second
	defaultBackoffRetryCount = 3
)

// DefaultBackoffConfig is the production retry configuration.
var DefaultBackoffConfig = BackoffConfig{
	Base:       defaultBackoffBase,
	RetryCount: defaultBackoffRetryCount,
}

// SlackHandlerOptions holds all configuration for creating a SlackHandler.
type SlackHandlerOptions struct {
	// WebhookURL is the Slack Incoming Webhook URL. Wrapped in Secret to
	// prevent accidental log exposure.
	WebhookURL config.Secret
	// AllowedHost is the permitted hostname (no port) for the webhook URL.
	AllowedHost string
	// RunID is a unique identifier for the current process run, included in
	// every notification message for log correlation.
	RunID string
	// LevelMode controls which log levels this handler forwards.
	LevelMode LevelMode
	// IsDryRun suppresses HTTP POST when true; payloads are written to
	// DebugLogger instead.
	IsDryRun bool
	// BackoffConfig controls retry backoff. Zero value uses DefaultBackoffConfig.
	BackoffConfig BackoffConfig
	// DebugLogger receives dry-run payloads and send-failure details.
	// When nil, such output is silently discarded.
	DebugLogger *slog.Logger
	// HTTPClient is used for Slack API requests. When nil, a default client
	// with a 5-second timeout is used. Injecting a custom client (e.g. with a
	// test TLS server's certificate) does not bypass the per-request context
	// deadline that enforces the 5-second limit.
	HTTPClient *http.Client
}
