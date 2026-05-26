package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
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

	// Step 2: First CheckRecoveryRequired before aggregation.
	found, err := guard.CheckRecoveryRequired(ctx)
	if err != nil {
		return exitError, fmt.Errorf("summary: check recovery required: %w", err)
	}
	if found {
		fmt.Fprintln(os.Stderr, "recovery required: run tlsrpt-digest recover to resolve")
		notifier, buildErr := r.buildNotifier(boot)
		if buildErr != nil {
			slog.Error("summary: build notifier for recovery error", "error", buildErr)
			return exitError, fmt.Errorf("summary: build notifier: %w", buildErr)
		}
		if err := logSummarySystemError(ctx, notifier, notify.SystemErrorKindRecoveryRequired, mailbox); err != nil {
			return exitError, fmt.Errorf("summary: notify recovery required: %w", err)
		}
		return exitError, nil
	}

	// Step 3: Aggregate reports over the summary window.
	now := r.now()
	start := summarySince(boot.Options.Window, boot.Config.Summary.WindowDays, now)
	end := UTCDayStart(now)

	summary, err := notify.GenerateSummary(ctx, boot.Store, start, end, nil)
	if err != nil {
		return exitError, fmt.Errorf("summary: generate: %w", err)
	}

	// Step 4a: Empty summary — second check without building a notifier.
	if summary.ReportCount == 0 {
		found2, err2 := guard.CheckRecoveryRequired(ctx)
		if err2 != nil {
			return exitError, fmt.Errorf("summary: check recovery required: %w", err2)
		}
		if found2 {
			fmt.Fprintln(os.Stderr, "recovery required: run tlsrpt-digest recover to resolve")
			return exitError, nil
		}
		slog.Info("no reports to summarize")
		return exitOK, nil
	}

	// Step 4b: Non-empty summary — build notifier.
	notifier, err := r.buildNotifier(boot)
	if err != nil {
		return exitError, fmt.Errorf("summary: build notifier: %w", err)
	}

	// Step 5: Second CheckRecoveryRequired immediately before sending.
	found3, err3 := guard.CheckRecoveryRequired(ctx)
	if err3 != nil {
		notifyErr := logSummarySystemError(ctx, notifier, notify.SystemErrorKindStoreCorruption, mailbox)
		return exitError, errors.Join(
			fmt.Errorf("summary: check recovery required before send: %w", err3),
			notifyErr,
		)
	}
	if found3 {
		fmt.Fprintln(os.Stderr, "recovery required: run tlsrpt-digest recover to resolve")
		if err := logSummarySystemError(ctx, notifier, notify.SystemErrorKindRecoveryRequired, mailbox); err != nil {
			return exitError, fmt.Errorf("summary: notify recovery required before send: %w", err)
		}
		return exitError, nil
	}

	// Send the summary.
	if err := notifier.LogSummary(ctx, summary); err != nil {
		return exitError, fmt.Errorf("summary: log summary: %w", err)
	}
	if err := notifier.Flush(ctx); err != nil {
		slog.Error("summary: flush notifications", "error", err)
		return exitError, fmt.Errorf("summary: flush: %w", err)
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
