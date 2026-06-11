package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/isseis/tlsrpt-digest/internal/config"
	"github.com/isseis/tlsrpt-digest/internal/imap"
	"github.com/isseis/tlsrpt-digest/internal/notify"
	"github.com/isseis/tlsrpt-digest/internal/store"
)

// gcRunner implements SubcommandRunner for the gc subcommand.
type gcRunner struct {
	now            func() time.Time
	newMailFetcher func(cfg imap.Config) (imap.MailFetcher, error)
	credentials    func() (username string, password config.Secret)
}

func newGCRunner() *gcRunner {
	return &gcRunner{
		now:            time.Now,
		newMailFetcher: imap.NewIMAPClient,
		credentials: func() (string, config.Secret) {
			return os.Getenv("TLSRPT_IMAP_USERNAME"), config.Secret(os.Getenv("TLSRPT_IMAP_PASSWORD"))
		},
	}
}

// Run executes the gc subcommand: delete (or, in dry-run, count) old report
// records, .eml files, and IMAP messages.
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
		slog.Error("gc: recovery required; run tlsrpt-digest --config <path> recover to resolve")
		return exitError, nil
	}

	reportCutoff := gcReportCutoff(boot.Options, boot.Config, now)
	emailCutoff := gcEmailCutoff(boot.Options, boot.Config, now)
	imapEnabled := boot.Config.IMAP.RetentionDays > 0
	var imapCutoff time.Time
	if imapEnabled {
		imapCutoff = Duration{Days: boot.Config.IMAP.RetentionDays}.Cutoff(now)
	}

	// Step 2: IMAP credentials are required only for non-dry-run IMAP deletion.
	var creds IMAPCredentials
	if imapEnabled && !boot.Options.DryRun {
		username, password := r.credentials()
		if username == "" || string(password) == "" {
			logNotifyError("gc: notify system error", notifyGCSystemError(ctx, boot.Notifier, notify.SystemErrorKindIMAPCredentialsMissing, mailbox))
			return exitError, nil
		}
		creds = IMAPCredentials{Username: username, Password: password}
	}

	if boot.Options.DryRun {
		return r.runDryRun(ctx, boot, mailbox, reportCutoff, emailCutoff, imapCutoff, imapEnabled)
	}
	return r.runDelete(ctx, boot, mailbox, reportCutoff, emailCutoff, imapCutoff, imapEnabled, creds)
}

// runDelete performs the non-dry-run gc flow: delete local report records and
// .eml files, then (if enabled) delete old IMAP messages, and log combined counts.
func (r *gcRunner) runDelete(ctx context.Context, boot *BootContext, mailbox string, reportCutoff, emailCutoff, imapCutoff time.Time, imapEnabled bool, creds IMAPCredentials) (int, error) {
	reportDeleted, err := boot.Store.DeleteReportsBefore(reportCutoff)
	if err != nil {
		logNotifyError("gc: notify system error", notifyGCSystemError(ctx, boot.Notifier, gcNotifyKind(err), mailbox))
		return exitError, fmt.Errorf("gc: delete reports: %w", err)
	}

	emailDeleted, err := boot.Store.DeleteEmailsBefore(emailCutoff)
	if err != nil {
		logNotifyError("gc: notify system error", notifyGCSystemError(ctx, boot.Notifier, gcNotifyKind(err), mailbox))
		return exitError, fmt.Errorf("gc: delete emails: %w", err)
	}

	var imapDeleted int
	if imapEnabled {
		imapDeleted, err = r.deleteIMAPOlderThan(ctx, boot, mailbox, creds, imapCutoff)
		if err != nil {
			// Local deletions already completed; log their counts before returning
			// the error so they are not lost.
			slog.Info("gc: deleted records", "reports", reportDeleted, "emails", emailDeleted, "imap_messages", 0)
			return exitError, err
		}
	}

	slog.Info("gc: deleted records", "reports", reportDeleted, "emails", emailDeleted, "imap_messages", imapDeleted)
	return exitOK, nil
}

// deleteIMAPOlderThan connects to IMAP and deletes messages older than cutoff.
func (r *gcRunner) deleteIMAPOlderThan(ctx context.Context, boot *BootContext, mailbox string, creds IMAPCredentials, cutoff time.Time) (int, error) {
	fetcher, err := r.newMailFetcher(buildIMAPConfig(boot.Config, creds))
	if err != nil {
		logNotifyError("gc: notify system error", notifyGCSystemError(ctx, boot.Notifier, classifyIMAPClientError(err), mailbox))
		return 0, fmt.Errorf("gc: create imap client: %w", err)
	}
	defer func() {
		if closeErr := fetcher.Close(); closeErr != nil {
			slog.Error("gc: close imap client", "error", closeErr)
		}
	}()

	deleted, err := fetcher.DeleteOlderThan(ctx, cutoff)
	if err != nil {
		// IMAP operation errors (including ErrMailboxReadOnly) are classified
		// separately from local store errors (gcNotifyKind), so they are never
		// misreported as SystemErrorKindStorePermission.
		logNotifyError("gc: notify system error", notifyGCSystemError(ctx, boot.Notifier, notify.SystemErrorKindIMAPOperationFailed, mailbox))
		return 0, fmt.Errorf("gc: delete imap messages: %w", err)
	}
	return deleted, nil
}

// runDryRun performs the dry-run gc flow: count local deletion candidates and,
// if IMAP retention is enabled, preview IMAP deletion candidates via SearchOlderThan.
// No records, files, or IMAP messages are deleted.
func (r *gcRunner) runDryRun(ctx context.Context, boot *BootContext, mailbox string, reportCutoff, emailCutoff, imapCutoff time.Time, imapEnabled bool) (int, error) {
	reportCount, err := boot.Store.CountReportsBefore(reportCutoff)
	if err != nil {
		logNotifyError("gc: notify system error", notifyGCSystemError(ctx, boot.Notifier, gcNotifyKind(err), mailbox))
		return exitError, fmt.Errorf("gc: count reports: %w", err)
	}

	emailCount, err := boot.Store.CountEmailsBefore(emailCutoff)
	if err != nil {
		logNotifyError("gc: notify system error", notifyGCSystemError(ctx, boot.Notifier, gcNotifyKind(err), mailbox))
		return exitError, fmt.Errorf("gc: count emails: %w", err)
	}

	// Log local counts before the IMAP preview so they are not lost if the
	// IMAP search below fails.
	logGCDryRunLocalSummary(reportCutoff, emailCutoff, reportCount, emailCount)

	if !imapEnabled {
		// imap.retention_days = 0: no IMAP connection, but still log the
		// IMAP candidate count (0) for consistency with the enabled case.
		logGCDryRunIMAPSummary(nil)
	} else {
		username, password := r.credentials()
		if username == "" || string(password) == "" {
			// Missing credentials are only an error for non-dry-run deletion;
			// in dry-run, they only disable the IMAP preview.
			slog.Warn("gc: dry-run: imap credentials missing; skipping imap deletion preview", "mailbox", mailbox)
			logGCDryRunIMAPSummary(nil)
		} else {
			imapUIDs, err := r.searchIMAPOlderThan(ctx, boot, mailbox, IMAPCredentials{Username: username, Password: password}, imapCutoff)
			if err != nil {
				return exitError, err
			}
			logGCDryRunIMAPSummary(imapUIDs)
		}
	}

	if err := boot.Notifier.Flush(ctx); err != nil {
		slog.Warn("gc: dry-run flush notifications", "error", err)
	}
	return exitOK, nil
}

// searchIMAPOlderThan connects to IMAP and previews messages older than cutoff via
// SearchOlderThan (read-only).
func (r *gcRunner) searchIMAPOlderThan(ctx context.Context, boot *BootContext, mailbox string, creds IMAPCredentials, cutoff time.Time) ([]uint32, error) {
	fetcher, err := r.newMailFetcher(buildIMAPConfig(boot.Config, creds))
	if err != nil {
		logNotifyError("gc: notify system error", notifyGCSystemError(ctx, boot.Notifier, classifyIMAPClientError(err), mailbox))
		return nil, fmt.Errorf("gc: create imap client: %w", err)
	}
	defer func() {
		if closeErr := fetcher.Close(); closeErr != nil {
			slog.Error("gc: close imap client", "error", closeErr)
		}
	}()

	uids, err := fetcher.SearchOlderThan(ctx, cutoff)
	if err != nil {
		logNotifyError("gc: notify system error", notifyGCSystemError(ctx, boot.Notifier, notify.SystemErrorKindIMAPOperationFailed, mailbox))
		return nil, fmt.Errorf("gc: search imap messages: %w", err)
	}
	return uids, nil
}

// logGCDryRunLocalSummary logs what local report records and .eml files would
// have been deleted in a real (non-dry) run, including the cutoff times used
// for each candidate set. It is logged before any IMAP preview so the local
// counts are not lost if the IMAP step fails.
func logGCDryRunLocalSummary(reportCutoff, emailCutoff time.Time, reportCount, emailCount int) {
	slog.Info("gc: dry-run: local deletion candidates; no records or files deleted",
		"would_delete_reports", reportCount,
		"report_cutoff", reportCutoff,
		"would_delete_emails", emailCount,
		"email_cutoff", emailCutoff)
}

// logGCDryRunIMAPSummary logs the IMAP messages that would have been deleted
// in a real (non-dry) run. imapUIDs is nil when credentials were unavailable.
func logGCDryRunIMAPSummary(imapUIDs []uint32) {
	sample := imapUIDs
	truncated := false
	if len(sample) > dryRunUIDSampleMax {
		sample = sample[:dryRunUIDSampleMax]
		truncated = true
	}
	slog.Info("gc: dry-run: imap deletion candidates; no messages deleted",
		"would_delete_imap_count", len(imapUIDs),
		"would_delete_imap_uids_sample", sample,
		"would_delete_imap_uids_truncated", truncated)
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
