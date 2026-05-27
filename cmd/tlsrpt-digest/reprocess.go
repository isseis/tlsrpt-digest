package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/isseis/tlsrpt-digest/internal/mailparse"
	"github.com/isseis/tlsrpt-digest/internal/notify"
	"github.com/isseis/tlsrpt-digest/internal/store"
	"github.com/isseis/tlsrpt-digest/internal/tlsrpt"
)

// reprocessRunner implements SubcommandRunner for the reprocess subcommand.
type reprocessRunner struct{}

func newReprocessRunner() *reprocessRunner {
	return &reprocessRunner{}
}

// Run re-parses all locally stored .eml files and rebuilds the report store.
// If --notify is set, TLS failures produce alerts and parse failures produce warnings via Slack.
func (r *reprocessRunner) Run(ctx context.Context, boot *BootContext) (int, error) {
	mailbox := mailboxID(boot.Config)
	notifyEnabled := boot.Options.ReprocessNotify

	// Step 1: Fail closed if recovery is required.
	_, _, _, recoveryFound, err := boot.Store.LoadRecoveryRequired()
	if err != nil {
		slog.Error("reprocess: load recovery-required", "error", err)
		_ = notifyReprocessSystemError(ctx, boot.Notifier, notify.SystemErrorKindStoreCorruption, mailbox)
		return exitError, fmt.Errorf("reprocess: load recovery-required: %w", err)
	}
	if recoveryFound {
		slog.Error("reprocess: recovery required; run tlsrpt-digest recover to resolve")
		return exitError, nil
	}

	// Step 2: Enumerate all locally stored .eml files.
	emails, loadErr := boot.Store.LoadEmails()
	if loadErr != nil {
		loadFailures, allPerFile := collectLoadEmailFailures(loadErr)
		if !allPerFile {
			slog.Error("reprocess: load emails", "error", loadErr)
			_ = notifyReprocessSystemError(ctx, boot.Notifier, notify.SystemErrorKindStoreCorruption, mailbox)
			return exitError, fmt.Errorf("reprocess: load emails: %w", loadErr)
		}
		slog.Warn("reprocess: some emails could not be loaded", "error", loadErr)
		if notifyEnabled {
			for _, failure := range loadFailures {
				logWarnReprocess(ctx, boot.Notifier, notify.WarningKindParseFailure, failure.UID, failure.UIDValidity, "")
			}
		}
	}

	// Step 3: Build and persist email metadata index.
	metas := buildReprocessMetas(emails)
	if err := boot.Store.SaveEmailMetas(metas); err != nil {
		return exitError, fmt.Errorf("reprocess: save email metas: %w", err)
	}

	// Step 4: Parse TLSRPT attachments from each loaded email.
	reports, parseErrs := reprocessCollectReports(ctx, boot.Notifier, boot.Config.IMAP.MaxMessageBytes, emails, notifyEnabled)
	if len(parseErrs) > 0 {
		slog.Warn("reprocess: some emails could not be parsed", "error", errors.Join(parseErrs...))
	}

	// Step 5: Persist all parsed reports.
	if err := boot.Store.SaveReports(reports); err != nil {
		return exitError, fmt.Errorf("reprocess: save reports: %w", err)
	}

	// Step 6: Flush notifications only when --notify is set.
	if notifyEnabled {
		if err := boot.Notifier.Flush(ctx); err != nil {
			slog.Error("reprocess: flush notifications", "error", err)
			return exitError, fmt.Errorf("reprocess: flush: %w", err)
		}
	}

	return exitOK, nil
}

// buildReprocessMetas constructs SaveEmailMetas inputs from loaded emails.
// SaveEmailMetas is idempotent, so already-indexed entries retain their original date.
func buildReprocessMetas(emails []store.LoadedEmail) []store.EmailMeta {
	metas := make([]store.EmailMeta, 0, len(emails))
	for _, e := range emails {
		if e.InternalDate.IsZero() {
			slog.Warn("reprocess: email missing internal date; skipping meta registration", "path", e.Path)
			continue
		}
		metas = append(metas, store.EmailMeta{
			UID:          e.UID,
			UIDValidity:  e.UIDValidity,
			InternalDate: e.InternalDate,
		})
	}
	return metas
}

// reprocessCollectReports parses TLSRPT attachments from all loaded emails.
// When sendNotifications is true, TLS failures produce alerts and parse failures produce warnings.
func reprocessCollectReports(ctx context.Context, notifier NotificationSink, maxBytes int64, emails []store.LoadedEmail, sendNotifications bool) ([]store.ReportInput, []error) {
	var reports []store.ReportInput
	var parseErrs []error

	for _, e := range emails {
		attachments, err := mailparse.ExtractAttachments(e.Message, maxBytes)
		if err != nil {
			parseErrs = append(parseErrs, fmt.Errorf("reprocess: parse attachments uid=%d: %w", e.UID, err))
			if sendNotifications {
				logWarnReprocess(ctx, notifier, notify.WarningKindParseFailure, e.UID, e.UIDValidity, "")
			}
			continue
		}
		for _, att := range attachments {
			report, err := parseTLSRPTAttachment(att)
			if report == nil && err == nil {
				continue
			}
			if err != nil {
				parseErrs = append(parseErrs, fmt.Errorf("reprocess: parse tlsrpt uid=%d: %w", e.UID, err))
				if sendNotifications {
					logWarnReprocess(ctx, notifier, notify.WarningKindParseFailure, e.UID, e.UIDValidity, "")
				}
				continue
			}
			if sendNotifications && report.HasFailure() {
				reprocessSendAlerts(ctx, notifier, report)
			}
			reports = append(reports, store.ReportInput{
				Report:      *report,
				UID:         e.UID,
				UIDValidity: e.UIDValidity,
			})
		}
	}
	return reports, parseErrs
}

func collectLoadEmailFailures(err error) ([]*store.ErrLoadEmailFailed, bool) {
	if err == nil {
		return nil, true
	}
	var failures []*store.ErrLoadEmailFailed
	allPerFile := collectLoadEmailFailuresInto(err, &failures)
	return failures, allPerFile && len(failures) > 0
}

func collectLoadEmailFailuresInto(err error, failures *[]*store.ErrLoadEmailFailed) bool {
	type multiUnwrapper interface {
		Unwrap() []error
	}
	if joined, ok := err.(multiUnwrapper); ok {
		children := joined.Unwrap()
		if len(children) == 0 {
			return false
		}
		allPerFile := true
		for _, child := range children {
			allPerFile = collectLoadEmailFailuresInto(child, failures) && allPerFile
		}
		return allPerFile
	}
	if loadFailure, ok := errors.AsType[*store.ErrLoadEmailFailed](err); ok {
		*failures = append(*failures, loadFailure)
		return true
	}
	return false
}

// reprocessSendAlerts logs one alert per failing policy in the report.
func reprocessSendAlerts(ctx context.Context, notifier NotificationSink, report *tlsrpt.Report) {
	for _, policy := range report.Policies {
		if policy.Summary.TotalFailureSessionCount <= 0 {
			continue
		}
		if err := notifier.LogAlert(ctx, notify.Alert{
			OrganizationName: report.OrganizationName,
			PolicyType:       notify.PolicyType(policy.Policy.PolicyType),
			FailureCount:     policy.Summary.TotalFailureSessionCount,
			DateRange: notify.DateRange{
				Start: report.DateRange.StartDatetime,
				End:   report.DateRange.EndDatetime,
			},
		}); err != nil {
			slog.Error("reprocess: log alert", "error", err)
		}
	}
}

// logWarnReprocess buffers a reprocess warning; logs errors from LogWarning but does not abort.
func logWarnReprocess(ctx context.Context, notifier NotificationSink, kind notify.WarningKind, uid, uidValidity uint32, messageID string) {
	if notifier == nil {
		return
	}
	if err := notifier.LogWarning(ctx, notify.Warning{
		Kind:        kind,
		UID:         uid,
		UIDValidity: uidValidity,
		MessageID:   messageID,
	}); err != nil {
		slog.Error("reprocess: log warning", "error", err)
	}
}

// notifyReprocessSystemError logs a system error with component "reprocess" and flushes.
func notifyReprocessSystemError(ctx context.Context, notifier NotificationSink, kind notify.SystemErrorKind, mailbox string) error {
	if notifier == nil {
		return nil
	}
	err := notifier.LogSystemError(ctx, notify.SystemError{
		Kind:      kind,
		Component: "reprocess",
		Mailbox:   mailbox,
	})
	return errors.Join(err, notifier.Flush(ctx))
}
