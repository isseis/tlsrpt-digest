package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/isseis/tlsrpt-digest/internal/notify"
)

// summaryRunner implements SubcommandRunner for the summary subcommand.
type summaryRunner struct {
	// buildNotifier receives the full BootContext so the default implementation
	// can read env vars and config without importing internal/config here.
	buildNotifier func(boot *BootContext) (NotificationSink, error)
	now           func() time.Time
}

func newSummaryRunner() *summaryRunner {
	return &summaryRunner{
		buildNotifier: defaultBuildSummaryNotifier,
		now:           time.Now,
	}
}

func (r *summaryRunner) Run(ctx context.Context, boot *BootContext) (int, error) {
	guard := boot.SummaryGuard
	mailbox := mailboxID(boot.Config)

	// Check recovery_required before aggregation. If already set, the store is
	// about to be wiped by recovery and there is no point sending a summary.
	found, err := guard.CheckRecoveryRequired(ctx)
	if err != nil {
		return exitError, fmt.Errorf("summary: check recovery required (pre-aggregation): %w", err)
	}
	if found {
		slog.Warn("recovery required: run tlsrpt-digest recover to resolve")
		notifier, buildErr := r.buildNotifier(boot)
		if buildErr != nil {
			slog.Error("summary: build notifier for recovery error", "error", buildErr)
			return exitError, fmt.Errorf("summary: build notifier: %w", buildErr)
		}
		if err := logSummarySystemError(ctx, notifier, notify.SystemErrorKindRecoveryRequired, mailbox); err != nil {
			slog.Warn("summary: notify recovery required", "error", err)
		}
		return exitError, nil
	}

	baseTime := r.now()
	start := summarySince(boot.Options.Window, boot.Config.Summary.WindowDays, baseTime)
	end := UTCDayStart(baseTime)

	summary, err := notify.GenerateSummary(ctx, boot.Store, start, end, slog.Default())
	if err != nil {
		return exitError, fmt.Errorf("summary: generate: %w", err)
	}

	if summary.ReportCount == 0 {
		slog.Info("no reports to summarize")
		return exitOK, nil
	}

	notifier, err := r.buildNotifier(boot)
	if err != nil {
		return exitError, fmt.Errorf("summary: build notifier: %w", err)
	}

	if err := notifier.LogSummary(ctx, summary); err != nil {
		slog.Warn("summary: log summary", "error", err)
	}
	if err := notifier.Flush(ctx); err != nil {
		slog.Warn("summary: flush notifications", "error", err)
	}

	return exitOK, nil
}

func logSummarySystemError(ctx context.Context, notifier NotificationSink, kind notify.SystemErrorKind, mailbox string) error {
	err := notifier.LogSystemError(ctx, notify.SystemError{
		Kind:      kind,
		Component: "summary",
		Mailbox:   mailbox,
	})
	return errors.Join(err, notifier.Flush(ctx))
}

// summarySince returns the start time for GenerateSummary, derived from --window flag or config.
func summarySince(window *Duration, windowDays int, now time.Time) time.Time {
	if window != nil {
		return window.Cutoff(now)
	}
	return Duration{Days: windowDays}.Cutoff(now)
}
