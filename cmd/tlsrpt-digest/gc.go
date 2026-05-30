package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/isseis/tlsrpt-digest/internal/config"
	"github.com/isseis/tlsrpt-digest/internal/notify"
	"github.com/isseis/tlsrpt-digest/internal/store"
)

// gcRunner implements SubcommandRunner for the gc subcommand.
type gcRunner struct {
	now func() time.Time
}

func newGCRunner() *gcRunner {
	return &gcRunner{now: time.Now}
}

// Run executes the gc subcommand: delete old report records and .eml files.
func (r *gcRunner) Run(ctx context.Context, boot *BootContext) (int, error) {
	now := r.now()
	mailbox := mailboxID(boot.Config)

	// Step 1: Fail closed if recovery is required.
	_, _, _, recoveryFound, err := boot.Store.LoadRecoveryRequired()
	if err != nil {
		slog.Error("gc: load recovery-required", "error", err)
		logNotifyError("gc: notify system error", notifyGCSystemError(ctx, boot.Notifier, notify.SystemErrorKindStoreCorruption, mailbox))
		return exitError, fmt.Errorf("gc: load recovery-required: %w", err)
	}
	if recoveryFound {
		slog.Error("gc: recovery required; run tlsrpt-digest recover to resolve")
		return exitError, nil
	}

	// Step 2: Delete report records before the configured cutoff.
	reportCutoff := gcReportCutoff(boot.Options, boot.Config, now)
	reportDeleted, err := boot.Store.DeleteReportsBefore(reportCutoff)
	if err != nil {
		logNotifyError("gc: notify system error", notifyGCSystemError(ctx, boot.Notifier, gcNotifyKind(err), mailbox))
		return exitError, fmt.Errorf("gc: delete reports: %w", err)
	}

	// Step 3: Delete .eml files before the configured cutoff.
	emailCutoff := gcEmailCutoff(boot.Options, boot.Config, now)
	emailDeleted, err := boot.Store.DeleteEmailsBefore(emailCutoff)
	if err != nil {
		logNotifyError("gc: notify system error", notifyGCSystemError(ctx, boot.Notifier, gcNotifyKind(err), mailbox))
		return exitError, fmt.Errorf("gc: delete emails: %w", err)
	}

	// Step 4: Log deletion counts.
	slog.Info("gc: deleted records", "reports", reportDeleted, "emails", emailDeleted)
	return exitOK, nil
}

// gcReportCutoff returns the cutoff for deleting report records, from --before flag or config.
func gcReportCutoff(opts cliOptions, cfg *config.Config, now time.Time) time.Time {
	if opts.Before != nil {
		return opts.Before.Cutoff(now)
	}
	return Duration{Days: cfg.Store.RetentionDays}.Cutoff(now)
}

// gcEmailCutoff returns the cutoff for deleting .eml files, from --max-email-age flag or config.
func gcEmailCutoff(opts cliOptions, cfg *config.Config, now time.Time) time.Time {
	if opts.MaxEmailAge != nil {
		return opts.MaxEmailAge.Cutoff(now)
	}
	return Duration{Days: cfg.Store.MaxEmailAgeDays}.Cutoff(now)
}

// gcNotifyKind maps a store error to the appropriate SystemErrorKind.
func gcNotifyKind(err error) notify.SystemErrorKind {
	if errors.Is(err, store.ErrDataCorrupted) {
		return notify.SystemErrorKindStoreCorruption
	}
	return notify.SystemErrorKindStorePermission
}

// notifyGCSystemError logs a system error with component "gc" and flushes.
// It returns any notification failure to the caller for logging.
func notifyGCSystemError(ctx context.Context, notifier NotificationSink, kind notify.SystemErrorKind, mailbox string) error {
	if notifier == nil {
		return nil
	}
	return errors.Join(
		notifier.LogSystemError(ctx, notify.SystemError{
			Kind:      kind,
			Component: "gc",
			Mailbox:   mailbox,
		}),
		notifier.Flush(ctx),
	)
}
