package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/mail"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/isseis/tlsrpt-digest/internal/config"
	"github.com/isseis/tlsrpt-digest/internal/imap"
	"github.com/isseis/tlsrpt-digest/internal/mailparse"
	"github.com/isseis/tlsrpt-digest/internal/notify"
	"github.com/isseis/tlsrpt-digest/internal/store"
	"github.com/isseis/tlsrpt-digest/internal/tlsrpt"
)

// fetchContinue signals that a helper returned without stopping the run.
const fetchContinue = -1

var errFetchDownloadMissingUID = errors.New("download missing uid")

// fetchMsgState tracks per-message processing state during a fetch run.
type fetchMsgState struct {
	meta             imap.MessageMeta
	emlExistedBefore bool
	rawEML           []byte
}

// fetchRunner implements SubcommandRunner for the fetch subcommand.
type fetchRunner struct {
	newMailFetcher func(cfg imap.Config) (imap.MailFetcher, error)
	credentials    func() (username string, password config.Secret)
	now            func() time.Time
	localEmailSize func(rootDir string, uid, uidValidity uint32, internalDate time.Time) (int64, bool, error)
	loadLocalEML   func(rootDir string, uid, uidValidity uint32, internalDate time.Time) ([]byte, error)
}

func newFetchRunner() *fetchRunner {
	return &fetchRunner{
		newMailFetcher: imap.NewIMAPClient,
		credentials: func() (string, config.Secret) {
			return os.Getenv("TLSRPT_IMAP_USERNAME"), config.Secret(os.Getenv("TLSRPT_IMAP_PASSWORD"))
		},
		now: time.Now,
		localEmailSize: func(rootDir string, uid, uidValidity uint32, internalDate time.Time) (int64, bool, error) {
			info, err := os.Stat(fetchEmailPath(rootDir, uid, uidValidity, internalDate))
			if err != nil {
				if errors.Is(err, os.ErrNotExist) {
					return 0, false, nil
				}
				return 0, false, err
			}
			if !info.Mode().IsRegular() {
				return 0, false, nil
			}
			return info.Size(), true, nil
		},
		loadLocalEML: func(rootDir string, uid, uidValidity uint32, internalDate time.Time) ([]byte, error) {
			return os.ReadFile(fetchEmailPath(rootDir, uid, uidValidity, internalDate))
		},
	}
}

// Run executes the fetch subcommand. Crash-safe: a crash between steps 11–13 may
// cause duplicate notifications on the next run, which is expected under the
// at-least-once delivery guarantee.
func (r *fetchRunner) Run(ctx context.Context, boot *BootContext) (int, error) {
	now := r.now()
	rootDir := boot.Config.Store.RootDir
	mailbox := mailboxID(boot.Config)

	// Step 1: Fail closed if recovery is pending.
	_, _, _, recoveryFound, err := boot.Store.LoadRecoveryRequired()
	if err != nil {
		slog.Error("fetch: load recovery-required", "error", err)
		logNotifyError("fetch: notify system error", notifyFetchSystemError(ctx, boot.Notifier, notify.SystemErrorKindStoreCorruption, mailbox))
		return exitError, fmt.Errorf("fetch: load recovery-required: %w", err)
	}
	if recoveryFound {
		slog.Error("fetch: recovery required; run tlsrpt-digest recover to resolve")
		logNotifyError("fetch: notify system error", notifyFetchSystemError(ctx, boot.Notifier, notify.SystemErrorKindRecoveryRequired, mailbox))
		return exitError, nil
	}

	// Step 2: Retrieve IMAP credentials.
	username, password := r.credentials()
	if username == "" || string(password) == "" {
		logNotifyError("fetch: notify system error", notifyFetchSystemError(ctx, boot.Notifier, notify.SystemErrorKindIMAPCredentialsMissing, mailbox))
		return exitError, nil
	}

	// Step 3: Connect to IMAP server.
	fetcher, err := r.newMailFetcher(buildIMAPConfig(boot.Config, IMAPCredentials{Username: username, Password: password}))
	if err != nil {
		logNotifyError("fetch: notify system error", notifyFetchSystemError(ctx, boot.Notifier, classifyIMAPClientError(err), mailbox))
		return exitError, fmt.Errorf("fetch: create imap client: %w", err)
	}
	defer func() {
		if closeErr := fetcher.Close(); closeErr != nil {
			slog.Error("fetch: close imap client", "error", closeErr)
		}
	}()

	// Step 4: Fetch message metadata from the mailbox.
	fetchResult, err := fetcher.FetchMeta(ctx, fetchSince(boot.Options, boot.Config, now))
	if err != nil {
		logNotifyError("fetch: notify system error", notifyFetchSystemError(ctx, boot.Notifier, notify.SystemErrorKindIMAPOperationFailed, mailbox))
		return exitError, fmt.Errorf("fetch: fetch meta: %w", err)
	}
	currentUID := fetchResult.UIDValidity

	// Step 5: Validate UIDVALIDITY.
	if code, err := fetchValidateUID(ctx, boot, now, currentUID, mailbox); code != fetchContinue {
		return code, err
	}

	// Step 6: Build candidate list with RFC822.SIZE mismatch warnings.
	states, err := r.buildFetchStates(ctx, boot.Notifier, fetchResult.Messages, currentUID, rootDir)
	if err != nil {
		return exitError, err
	}

	// Step 7: Download and save messages that lack a local .eml (skipped in dry-run).
	if !boot.Options.DryRun {
		if err := r.fetchDownloadAndSave(ctx, boot, states, currentUID, mailbox, fetcher); err != nil {
			return exitError, err
		}
	}

	if boot.Options.DryRun {
		slog.Info("fetch: dry-run complete; no messages downloaded, no store writes, no IMAP flags set",
			"candidate_count", len(states))
		return exitOK, nil
	}

	// Step 8: Register metadata for all messages that now have a local .eml.
	if err := boot.Store.SaveEmailMetas(buildEmailMetas(states, currentUID)); err != nil {
		return exitError, fmt.Errorf("fetch: save email metas: %w", err)
	}

	// Steps 9–10: Parse TLSRPT attachments, buffer alerts, accumulate reports.
	reports, err := r.fetchCollectReports(ctx, boot, states, currentUID, rootDir)
	if err != nil {
		return exitError, err
	}

	// Step 11: Persist all parsed reports.
	if err := boot.Store.SaveReports(reports); err != nil {
		return exitError, fmt.Errorf("fetch: save reports: %w", err)
	}

	// Step 12: Flush notifications. At-least-once guarantee: flush before MarkSeen.
	if err := boot.Notifier.Flush(ctx); err != nil {
		slog.Warn("fetch: flush notifications", "error", err)
		return exitError, fmt.Errorf("fetch: flush: %w", err)
	}

	// Step 13: Mark UNSEEN messages as seen.
	if unseenUIDs := collectUnseenUIDs(states); len(unseenUIDs) > 0 {
		if err := fetcher.MarkSeen(ctx, unseenUIDs); err != nil {
			return exitError, fmt.Errorf("fetch: mark seen: %w", err)
		}
	}

	// Step 14: Idempotent final UIDVALIDITY save.
	if err := boot.Store.SaveUIDValidity(currentUID); err != nil {
		slog.Error("fetch: save final uidvalidity", "error", err)
		return exitError, fmt.Errorf("fetch: save uidvalidity: %w", err)
	}

	return exitOK, nil
}

// fetchValidateUID checks the current UIDVALIDITY from IMAP against the stored value.
// Returns fetchContinue on success, or an exit code and error on failure.
func fetchValidateUID(ctx context.Context, boot *BootContext, now time.Time, currentUID uint32, mailbox string) (int, error) {
	storedUID, uidFound, err := boot.Store.LoadUIDValidity()
	if err != nil {
		slog.Error("fetch: load uidvalidity", "error", err)
		logNotifyError("fetch: notify system error", notifyFetchSystemError(ctx, boot.Notifier, notify.SystemErrorKindStoreCorruption, mailbox))
		return exitError, fmt.Errorf("fetch: load uidvalidity: %w", err)
	}
	if !uidFound {
		if !boot.Options.DryRun {
			if err := boot.Store.SaveUIDValidity(currentUID); err != nil {
				slog.Error("fetch: save initial uidvalidity", "error", err)
				logNotifyError("fetch: notify system error", notifyFetchSystemError(ctx, boot.Notifier, notify.SystemErrorKindStoreCorruption, mailbox))
				return exitError, fmt.Errorf("fetch: save uidvalidity: %w", err)
			}
		}
		return fetchContinue, nil
	}
	if storedUID != currentUID {
		if !boot.Options.DryRun {
			if saveErr := boot.Store.SaveRecoveryRequired(storedUID, currentUID, now); saveErr != nil {
				slog.Error("fetch: save recovery-required after uidvalidity change", "error", saveErr)
				logNotifyError("fetch: notify system error", notifyFetchSystemError(ctx, boot.Notifier, notify.SystemErrorKindStoreCorruption, mailbox))
				return exitError, fmt.Errorf("fetch: save recovery-required: %w", saveErr)
			}
		}
		slog.Error("fetch: uidvalidity changed; run tlsrpt-digest recover to resolve")
		logNotifyError("fetch: notify system error", notifyFetchSystemError(ctx, boot.Notifier, notify.SystemErrorKindUIDValidityChanged, mailbox))
		return exitError, nil
	}
	return fetchContinue, nil
}

// buildFetchStates builds per-message state, logging RFC822.SIZE mismatches as warnings.
func (r *fetchRunner) buildFetchStates(ctx context.Context, notifier NotificationSink, msgs []imap.MessageMeta, currentUID uint32, rootDir string) ([]fetchMsgState, error) {
	states := make([]fetchMsgState, len(msgs))
	for i, meta := range msgs {
		localSize, emlExists, err := r.localEmailSize(rootDir, meta.UID, currentUID, meta.Date)
		if err != nil {
			return nil, fmt.Errorf("fetch: check local email size for UID %d: %w", meta.UID, err)
		}
		if emlExists && localSize != int64(meta.Size) {
			logWarn(ctx, notifier, notify.WarningKindSizeMismatch, meta.UID, currentUID, meta.MessageID, "fetch")
		}
		states[i] = fetchMsgState{meta: meta, emlExistedBefore: emlExists}
	}
	return states, nil
}

// fetchDownloadAndSave downloads messages lacking a local .eml and saves them to the store.
// It updates states[i].rawEML for each successfully downloaded entry.
func (r *fetchRunner) fetchDownloadAndSave(ctx context.Context, boot *BootContext, states []fetchMsgState, currentUID uint32, mailbox string, fetcher imap.MailFetcher) error {
	var downloadUIDs []uint32
	for _, s := range states {
		if !s.emlExistedBefore {
			downloadUIDs = append(downloadUIDs, s.meta.UID)
		}
	}
	if len(downloadUIDs) == 0 {
		return nil
	}

	downloaded, err := fetcher.Download(ctx, downloadUIDs)
	if err != nil {
		logNotifyError("fetch: notify system error", notifyFetchSystemError(ctx, boot.Notifier, notify.SystemErrorKindIMAPOperationFailed, mailbox))
		return fmt.Errorf("fetch: download: %w", err)
	}

	for i := range states {
		if states[i].emlExistedBefore {
			continue
		}
		rawEML, ok := downloaded[states[i].meta.UID]
		if !ok {
			logNotifyError("fetch: notify system error", notifyFetchSystemError(ctx, boot.Notifier, notify.SystemErrorKindIMAPOperationFailed, mailbox))
			return fmt.Errorf("fetch: %w: %d", errFetchDownloadMissingUID, states[i].meta.UID)
		}
		if int64(len(rawEML)) != int64(states[i].meta.Size) {
			logWarn(ctx, boot.Notifier, notify.WarningKindSizeMismatch, states[i].meta.UID, currentUID, states[i].meta.MessageID, "fetch")
		}
		if err := boot.Store.SaveEmail(states[i].meta.UID, currentUID, states[i].meta.Date, rawEML); err != nil {
			return fmt.Errorf("fetch: save email %d: %w", states[i].meta.UID, err)
		}
		states[i].rawEML = rawEML
	}
	return nil
}

// fetchCollectReports parses TLSRPT attachments, buffers alerts for UNSEEN messages,
// and returns the accumulated report inputs.
func (r *fetchRunner) fetchCollectReports(ctx context.Context, boot *BootContext, states []fetchMsgState, currentUID uint32, rootDir string) ([]store.ReportInput, error) {
	var reports []store.ReportInput
	for i := range states {
		// SEEN + pre-existing: already fully processed in a prior run.
		if states[i].meta.Seen && states[i].emlExistedBefore {
			continue
		}
		if states[i].rawEML == nil {
			if !states[i].emlExistedBefore || states[i].meta.Seen {
				// SEEN + no prior .eml with UID absent from download result: skip.
				continue
			}
			// Crash recovery: UNSEEN + pre-existing .eml — load from disk.
			rawEML, err := r.loadLocalEML(rootDir, states[i].meta.UID, currentUID, states[i].meta.Date)
			if err != nil {
				slog.Error("fetch: load local eml", "uid", states[i].meta.UID, "error", err)
				return nil, fmt.Errorf("fetch: load local eml %d: %w", states[i].meta.UID, err)
			}
			states[i].rawEML = rawEML
		}
		parsedMsg, err := mail.ReadMessage(bytes.NewReader(states[i].rawEML))
		if err != nil {
			logWarn(ctx, boot.Notifier, notify.WarningKindParseFailure, states[i].meta.UID, currentUID, states[i].meta.MessageID, "fetch")
			continue
		}
		attachments, err := mailparse.ExtractAttachments(parsedMsg, boot.Config.IMAP.MaxMessageBytes)
		if err != nil {
			logWarn(ctx, boot.Notifier, notify.WarningKindParseFailure, states[i].meta.UID, currentUID, states[i].meta.MessageID, "fetch")
			continue
		}
		reports = append(reports, r.processAttachments(ctx, boot, attachments, &states[i], currentUID)...)
	}
	return reports, nil
}

// processAttachments parses TLSRPT attachments from one message and returns valid reports.
func (r *fetchRunner) processAttachments(ctx context.Context, boot *BootContext, attachments []mailparse.Attachment, s *fetchMsgState, currentUID uint32) []store.ReportInput {
	var reports []store.ReportInput
	for _, att := range attachments {
		report, err := parseTLSRPTAttachment(att)
		if report == nil && err == nil {
			continue // not a TLSRPT attachment
		}
		if err != nil {
			logWarn(ctx, boot.Notifier, notify.WarningKindParseFailure, s.meta.UID, currentUID, s.meta.MessageID, "fetch")
			continue
		}
		if !s.meta.Seen && report.HasFailure() {
			logAlerts(ctx, boot.Notifier, report, "fetch")
		}
		reports = append(reports, store.ReportInput{
			Report:      *report,
			UID:         s.meta.UID,
			UIDValidity: currentUID,
		})
	}
	return reports
}

// parseTLSRPTAttachment dispatches to ParseGzip or ParseJSON based on Content-Type,
// falling back to filename extension for senders that use a generic content-type.
// Returns (nil, nil) if the attachment is not a TLSRPT report.
func parseTLSRPTAttachment(att mailparse.Attachment) (*tlsrpt.Report, error) {
	switch strings.ToLower(att.ContentType) {
	case "application/tlsrpt+gzip":
		return tlsrpt.ParseGzip(att.Content)
	case "application/tlsrpt+json":
		return tlsrpt.ParseJSON(att.Content)
	}
	fn := strings.ToLower(att.Filename)
	if strings.HasSuffix(fn, ".json.gz") {
		return tlsrpt.ParseGzip(att.Content)
	}
	if strings.HasSuffix(fn, ".json") {
		return tlsrpt.ParseJSON(att.Content)
	}
	return nil, nil
}

// buildEmailMetas builds a SaveEmailMetas input slice from states with local .eml files.
func buildEmailMetas(states []fetchMsgState, currentUID uint32) []store.EmailMeta {
	var metas []store.EmailMeta
	for _, s := range states {
		if s.emlExistedBefore || s.rawEML != nil {
			metas = append(metas, store.EmailMeta{
				UID:          s.meta.UID,
				UIDValidity:  currentUID,
				InternalDate: s.meta.Date,
			})
		}
	}
	return metas
}

// collectUnseenUIDs returns the UIDs of all messages that were UNSEEN at fetch time.
func collectUnseenUIDs(states []fetchMsgState) []uint32 {
	var uids []uint32
	for _, s := range states {
		if !s.meta.Seen {
			uids = append(uids, s.meta.UID)
		}
	}
	return uids
}

// fetchSince returns the since time for FetchMeta, derived from --since flag or config.
func fetchSince(opts cliOptions, cfg *config.Config, now time.Time) time.Time {
	if opts.Since != nil {
		return opts.Since.Cutoff(now)
	}
	return Duration{Days: cfg.IMAP.FetchDays}.Cutoff(now)
}

// fetchEmailPath returns the storage path for a .eml file, matching the store package layout.
func fetchEmailPath(rootDir string, uid, uidValidity uint32, internalDate time.Time) string {
	yyyymm := internalDate.UTC().Format("200601")
	return filepath.Join(rootDir, "emails", fmt.Sprintf("%d", uidValidity), yyyymm, fmt.Sprintf("%010d.eml", uid))
}

// classifyIMAPClientError maps a connection or login error to a SystemErrorKind.
func classifyIMAPClientError(err error) notify.SystemErrorKind {
	if strings.Contains(err.Error(), "login") {
		return notify.SystemErrorKindIMAPAuthFailed
	}
	return notify.SystemErrorKindIMAPConnectFailed
}

// notifyFetchSystemError logs a system error with component "fetch" and flushes.
// It returns any notification failure to the caller for logging.
func notifyFetchSystemError(ctx context.Context, notifier NotificationSink, kind notify.SystemErrorKind, mailbox string) error {
	if notifier == nil {
		return nil
	}
	return errors.Join(
		notifier.LogSystemError(ctx, notify.SystemError{
			Kind:      kind,
			Component: "fetch",
			Mailbox:   mailbox,
		}),
		notifier.Flush(ctx),
	)
}
