// Package main is the entry point for the tlsrpt-digest binary.
package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/isseis/tlsrpt-digest/internal/config"
	"github.com/isseis/tlsrpt-digest/internal/notify"
	"github.com/isseis/tlsrpt-digest/internal/store"
)

func main() {
	dryRun := flag.Bool("dry-run", false, "log notification payloads to stderr without sending HTTP requests")
	configPath := flag.String("config", "", "path to TOML configuration file")
	flag.Parse()

	setupPhase1Logging()
	slog.Info("tlsrpt-digest starting", "dry_run", *dryRun)

	runID := ulid.Make().String()

	successURL := os.Getenv("TLSRPT_SLACK_WEBHOOK_URL_SUCCESS")
	errorURL := os.Getenv("TLSRPT_SLACK_WEBHOOK_URL_ERROR")

	if err := notify.ValidateEnvCombination(successURL, errorURL); err != nil {
		slog.Error("invalid Slack webhook configuration", "error", err)
		os.Exit(1)
	}

	cfg, err := loadConfig(*configPath)
	if err != nil {
		slog.Error("failed to load configuration", "error", err)
		os.Exit(1)
	}

	// Build notification handlers (Phase 2: after TOML).
	// SlackHandlers are intentionally NOT wired into slog.Default() — ordinary
	// application log calls must not enter the notification buffer. Callers use
	// the typed helpers LogAlert/LogSystemError/LogSummary and then call
	// Flush() explicitly at the end of each processing run (task 0050).
	handlers, err := setupNotifyHandlers(successURL, errorURL, cfg, runID, *dryRun)
	if err != nil {
		slog.Error("failed to initialise Slack handlers", "error", err)
		os.Exit(1)
	}

	if err := primeNotifyHandlers(context.Background(), handlers, *dryRun); err != nil {
		slog.Error("failed to prime Slack handlers", "error", err)
		os.Exit(1)
	}

	slog.Info("tlsrpt-digest ready", "run_id", runID)
}

// setupPhase1Logging initialises console-only logging (Phase 1: before TOML).
// It returns the handler so tests can verify Phase 1 contains no Slack handler.
func setupPhase1Logging() slog.Handler {
	h := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})
	slog.SetDefault(slog.New(h))
	return h
}

// setupNotifyHandlers validates URLs and creates SlackHandler instances for
// use by the processing loop. The handlers are separate from slog.Default()
// and must be used via the typed helpers (LogAlert, LogSystemError, LogSummary)
// followed by an explicit Flush() call after each processing run.
// Returns the handlers and any configuration error.
func setupNotifyHandlers(successURL, errorURL string, cfg *config.Config, runID string, dryRun bool) ([]*notify.SlackHandler, error) {
	// In dry-run mode use LevelDebug so payload dumps appear on stderr.
	// In normal mode use LevelWarn so Debug-level payload logs are suppressed
	// (they would otherwise duplicate notification content into service logs)
	// while send-failure errors and unexpected-key warnings remain visible.
	debugLevel := slog.LevelWarn
	if dryRun {
		debugLevel = slog.LevelDebug
	}
	debugLogger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: debugLevel}))

	opts := notify.SlackHandlerOptions{
		AllowedHost:   cfg.Notify.Slack.AllowedHost,
		RunID:         runID,
		IsDryRun:      dryRun,
		DebugLogger:   debugLogger,
		BackoffConfig: notify.DefaultBackoffConfig,
	}
	return notify.BuildHandlers(successURL, errorURL, cfg.Notify.Slack.AllowedHost, opts)
}

// primeNotifyHandlers performs a minimal end-to-end wiring pass for typed helper
// calls and Flush(). This keeps the notification path reachable from main while
// task 0050 integration is in progress.
//
// For normal (non dry-run) execution, this function is intentionally a no-op.
func primeNotifyHandlers(ctx context.Context, handlers []*notify.SlackHandler, dryRun bool) error {
	if !dryRun || len(handlers) == 0 {
		return nil
	}

	now := time.Now().UTC()
	for _, h := range handlers {
		if err := notify.LogSummary(ctx, h, notify.Summary{
			Period:            notify.DateRange{Start: now, End: now},
			OrganizationCount: 0,
			ReportCount:       0,
		}); err != nil {
			return err
		}
		if err := notify.LogAlert(ctx, h, notify.Alert{
			OrganizationName: "bootstrap.example",
			PolicyType:       notify.PolicyTypeUnknown,
			FailureCount:     0,
			DateRange:        notify.DateRange{Start: now, End: now},
		}); err != nil {
			return err
		}
		if err := notify.LogSystemError(ctx, h, notify.SystemError{
			ErrorType: "bootstrap_probe",
			Message:   "handler wiring probe",
			Component: "notify",
		}); err != nil {
			return err
		}
	}

	for _, h := range handlers {
		if err := h.Flush(ctx); err != nil {
			return err
		}
	}

	return nil
}

// storeOpenMode returns the store.OpenMode appropriate for a given subcommand.
// Subcommands that write data (fetch, gc, reprocess, recover) use OpenReadWrite.
// The summary subcommand uses OpenReadOnly so it can run without a process lock.
// This stub will be wired into subcommand dispatch in task 0070.
func storeOpenMode(subcommand string) store.OpenMode {
	if subcommand == "summary" {
		return store.OpenReadOnly
	}
	return store.OpenReadWrite
}

// openStoreForSubcommand opens the store with the mode appropriate for subcommand.
// It is the wiring point between subcommand dispatch and store.Open; task 0070
// will call this from each subcommand handler.
func openStoreForSubcommand(rootDir string, identity store.IMAPIdentity, subcommand string) (store.Store, error) {
	return store.Open(rootDir, identity, storeOpenMode(subcommand))
}

// loadConfig reads the TOML configuration from path, or returns an empty
// Config when path is empty.
func loadConfig(path string) (*config.Config, error) {
	if path == "" {
		return &config.Config{}, nil
	}
	data, err := os.ReadFile(path) //nolint:gosec // G304: path is an operator-supplied flag
	if err != nil {
		return nil, err
	}
	return config.Load(data)
}
