// Package main is the entry point for the tlsrpt-digest binary.
package main

import (
	"flag"
	"log/slog"
	"os"

	"github.com/oklog/ulid/v2"

	"github.com/isseis/tlsrpt-digest/internal/config"
	"github.com/isseis/tlsrpt-digest/internal/notify"
)

func main() {
	dryRun := flag.Bool("dry-run", false, "log notification payloads to stderr without sending HTTP requests")
	flag.Parse()

	setupPhase1Logging()
	slog.Info("tlsrpt-digest starting", "dry_run", *dryRun)

	runID := generateRunID()

	successURL := os.Getenv("TLSRPT_SLACK_WEBHOOK_URL_SUCCESS")
	errorURL := os.Getenv("TLSRPT_SLACK_WEBHOOK_URL_ERROR")

	if err := notify.ValidateEnvCombination(successURL, errorURL); err != nil {
		slog.Error("invalid Slack webhook configuration", "error", err)
		os.Exit(1)
	}

	cfg, err := loadConfig()
	if err != nil {
		slog.Error("failed to load configuration", "error", err)
		os.Exit(1)
	}

	// Build notification handlers (Phase 2: after TOML).
	// SlackHandlers are intentionally NOT wired into slog.Default() — ordinary
	// application log calls must not enter the notification buffer. Callers use
	// the typed helpers LogAlert/LogSystemError/LogSummary and then call
	// Flush() explicitly at the end of each processing run (task 0050).
	_, err = setupNotifyHandlers(successURL, errorURL, cfg, runID, *dryRun)
	if err != nil {
		slog.Error("failed to initialise Slack handlers", "error", err)
		os.Exit(1)
	}

	slog.Info("tlsrpt-digest ready", "run_id", runID)
}

// setupPhase1Logging initialises console-only logging (Phase 1: before TOML).
func setupPhase1Logging() {
	h := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})
	slog.SetDefault(slog.New(h))
}

// setupNotifyHandlers validates URLs and creates SlackHandler instances for
// use by the processing loop. The handlers are separate from slog.Default()
// and must be used via the typed helpers (LogAlert, LogSystemError, LogSummary)
// followed by an explicit Flush() call after each processing run.
// Returns the handlers and any configuration error.
func setupNotifyHandlers(successURL, errorURL string, cfg *config.Config, runID string, dryRun bool) ([]*notify.SlackHandler, error) {
	// DebugLogger uses LevelDebug so that dry-run payloads written with
	// DebugLogger.Debug(...) are not suppressed.
	debugLogger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))

	opts := notify.SlackHandlerOptions{
		RunID:         runID,
		IsDryRun:      dryRun,
		DebugLogger:   debugLogger,
		BackoffConfig: notify.DefaultBackoffConfig,
	}
	return notify.BuildHandlers(successURL, errorURL, cfg.Notify.Slack.AllowedHost, opts)
}

// loadConfig reads the TOML configuration from TLSRPT_CONFIG, or returns an
// empty Config when the variable is unset.
func loadConfig() (*config.Config, error) {
	path := os.Getenv("TLSRPT_CONFIG")
	if path == "" {
		return &config.Config{}, nil
	}
	data, err := os.ReadFile(path) //nolint:gosec // G304: path is an operator-supplied env var
	if err != nil {
		return nil, err
	}
	return config.Load(data)
}

// generateRunID returns a new ULID that is unique across all invocations.
func generateRunID() string {
	return ulid.Make().String()
}
