// Package main is the entry point for the tlsrpt-digest binary.
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"flag"
	"log/slog"
	"os"

	"github.com/isseis/tlsrpt-digest/internal/config"
	"github.com/isseis/tlsrpt-digest/internal/notify"
)

func main() {
	dryRun := flag.Bool("dry-run", false, "log notification payloads to stderr without sending HTTP requests")
	flag.Parse()

	localHandler := setupPhase1Logging()
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

	if err := setupPhase2Slack(localHandler, successURL, errorURL, cfg, runID, *dryRun); err != nil {
		slog.Error("failed to initialise Slack handlers", "error", err)
		os.Exit(1)
	}

	slog.Info("tlsrpt-digest ready", "run_id", runID)
}

// setupPhase1Logging initialises console-only logging (Phase 1: before TOML).
func setupPhase1Logging() slog.Handler {
	h := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})
	slog.SetDefault(slog.New(h))
	return h
}

// setupPhase2Slack validates URLs and rebuilds the default logger with Slack
// handlers added (Phase 2: after TOML). No-op when both URLs are empty and
// dry-run is false.
func setupPhase2Slack(local slog.Handler, successURL, errorURL string, cfg *config.Config, runID string, dryRun bool) error {
	opts := notify.SlackHandlerOptions{
		RunID:         runID,
		IsDryRun:      dryRun,
		DebugLogger:   slog.New(local),
		BackoffConfig: notify.DefaultBackoffConfig,
	}
	handlers, err := notify.BuildHandlers(successURL, errorURL, cfg.Notify.Slack.AllowedHost, opts)
	if err != nil {
		return err
	}
	if len(handlers) > 0 {
		slog.SetDefault(slog.New(newFanOutHandler(local, handlers)))
	}
	return nil
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

// generateRunID returns a cryptographically random 8-byte hex string.
func generateRunID() string {
	const runIDBytes = 8
	b := make([]byte, runIDBytes)
	if _, err := rand.Read(b); err != nil {
		return "unknown"
	}
	return hex.EncodeToString(b)
}

// fanOutHandler distributes records to multiple slog.Handler implementations.
// Each child handler's Enabled is checked before forwarding.
type fanOutHandler struct {
	handlers []slog.Handler
}

func newFanOutHandler(local slog.Handler, slack []*notify.SlackHandler) *fanOutHandler {
	h := &fanOutHandler{handlers: []slog.Handler{local}}
	for _, s := range slack {
		h.handlers = append(h.handlers, s)
	}
	return h
}

func (f *fanOutHandler) Enabled(ctx context.Context, level slog.Level) bool {
	for _, h := range f.handlers {
		if h.Enabled(ctx, level) {
			return true
		}
	}
	return false
}

func (f *fanOutHandler) Handle(ctx context.Context, r slog.Record) error {
	for _, h := range f.handlers {
		if h.Enabled(ctx, r.Level) {
			if err := h.Handle(ctx, r.Clone()); err != nil {
				return err
			}
		}
	}
	return nil
}

func (f *fanOutHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	children := make([]slog.Handler, len(f.handlers))
	for i, h := range f.handlers {
		children[i] = h.WithAttrs(attrs)
	}
	return &fanOutHandler{handlers: children}
}

func (f *fanOutHandler) WithGroup(name string) slog.Handler {
	children := make([]slog.Handler, len(f.handlers))
	for i, h := range f.handlers {
		children[i] = h.WithGroup(name)
	}
	return &fanOutHandler{handlers: children}
}
