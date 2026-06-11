// Package store provides persistent storage for TLSRPT reports and emails.
package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/isseis/tlsrpt-digest/internal/tlsrpt"
)

// Store represents the persistence layer for TLSRPT reports and emails.
// Write operations require a single writer; cmd-layer callers must hold the
// process-level store writer lock before opening a read-write store.
// See docs/dev/developer_guide/process_locking.md for the locking design.
type Store interface {
	// SaveReports persists a batch of TLSRPT reports in a single atomic write.
	// Reports are UPSERT'd by report-id (a duplicate replaces the existing entry).
	// The email index is not modified. Returns an error if the write fails.
	SaveReports(inputs []ReportInput) error

	// SaveEmailMetas persists email metadata to the index in a single atomic write
	// (does not save raw .eml files). For each entry, {uid, uidvalidity, internal_date}
	// is registered. Existing entries for the same {uid, uidvalidity} are left unchanged
	// (idempotent). Calling this once after all SaveEmail calls avoids per-email JSON reads
	// and writes. Used during reprocess to sync the index.
	// Returns ErrZeroInternalDate if any entry has a zero InternalDate.
	SaveEmailMetas(metas []EmailMeta) error

	// GetAllReports retrieves all stored reports without filtering.
	// Callers are responsible for any date-range or failure filtering.
	// Returns an empty slice (not an error) when no reports are stored.
	GetAllReports() ([]tlsrpt.Report, error)

	// SaveEmail saves a raw .eml file to
	// {root_dir}/emails/{uidvalidity}/{YYYYMM}/{uid}.eml
	// (uid zero-padded to 10 digits; YYYYMM derived from internalDate).
	// Creates subdirectories as needed (mode 0700). The write is atomic (temp file + rename).
	// If a file already exists at the computed path, the call is a no-op (idempotent, no
	// error returned). The path is determined by uid, uidvalidity, and internalDate together;
	// callers must always pass the same internalDate for a given uid+uidvalidity pair (IMAP
	// INTERNALDATE is stable, so this is guaranteed in normal operation).
	// Returns an error if internalDate is zero.
	// Does not update the email index; call SaveEmailMetas after all SaveEmail calls.
	SaveEmail(uid, uidValidity uint32, internalDate time.Time, rawEML []byte) error

	// LoadEmails recursively enumerates all .eml files under {root_dir}/emails/,
	// deriving uid and uidvalidity from the {uidvalidity}/{YYYYMM}/{uid}.eml path.
	// Each entry includes the parsed *mail.Message, UID, UIDValidity, and Path.
	// Individual file-read or parse failures are collected via errors.Join and returned
	// alongside any successfully loaded emails.
	LoadEmails() ([]LoadedEmail, error)

	// SaveUIDValidity persists the IMAP UIDVALIDITY value to the sentinel file
	// using an atomic write (temp file + rename).
	SaveUIDValidity(v uint32) error

	// LoadUIDValidity retrieves the UIDVALIDITY value from the sentinel file.
	// Returns found=false (not an error) if no value has been stored yet.
	LoadUIDValidity() (v uint32, found bool, err error)

	// SaveRecoveryRequired records in the sentinel that UIDVALIDITY changed from prev
	// to curr at detectedAt, signalling that manual recovery is required before further
	// fetch or summary operations. The write is atomic (temp file + rename).
	SaveRecoveryRequired(prev, curr uint32, detectedAt time.Time) error

	// LoadRecoveryRequired retrieves recovery state from sentinel.
	// Returns found=false if not in recovery state (not an error).
	LoadRecoveryRequired() (prev, curr uint32, detectedAt time.Time, found bool, err error)

	// ClearRecoveryRequired removes recovery state from sentinel.
	ClearRecoveryRequired() error

	// ApplyRecovery updates uid_validity to newUIDValidity and clears the
	// recovery_required field in a single atomic read-modify-write on the sentinel.
	// Using two separate calls (SaveUIDValidity + ClearRecoveryRequired) risks leaving
	// the sentinel inconsistent on a crash between the two writes; always use this
	// method when both fields must change together.
	ApplyRecovery(newUIDValidity uint32) error

	// ResetForRecovery discards old data and advances uid_validity to currUIDValidity.
	// It requires recovery-required to be present in the sentinel with a matching current
	// UIDVALIDITY. The operation is crash-safe: re-running after a partial failure
	// converges to "empty store + current UIDVALIDITY + recovery-required cleared".
	// Only valid on stores opened with OpenRecoverReset.
	// The caller must hold the process-level store writer lock until this method returns.
	ResetForRecovery(currUIDValidity uint32) error

	// HasPendingReset reports whether a reset is in progress (pre-commit phase 1).
	// A committed manifest (phase=committed) is leftover cleanup bookkeeping rather than
	// an active reset, so it returns false for that phase.
	// Returns (true, nil) when a pre-commit reset manifest is present.
	// Returns (false, nil) when no manifest is found or the manifest is committed.
	HasPendingReset() (bool, error)

	// AcquireSummaryConsistencyGuard acquires a shared flock on the guard file and
	// returns a SummaryConsistencyGuard. While held, writes to recovery-required
	// in the sentinel are blocked, keeping CheckRecoveryRequired results stable.
	// The caller must call Close() after use.
	// See docs/dev/developer_guide/process_locking.md §3 for the concurrency design.
	AcquireSummaryConsistencyGuard() (SummaryConsistencyGuard, error)

	// DeleteReportsBefore deletes all report records whose date-range.end-datetime < cutoff
	// and returns the number of deleted records. Returns deleted=0 without error if no
	// records match. The updated JSON is written atomically (temp file + rename).
	// Idempotent: re-running with the same cutoff has no effect once matching records
	// are removed.
	DeleteReportsBefore(cutoff time.Time) (deleted int, err error)

	// DeleteEmailsBefore deletes .eml files whose internal_date < cutoff.
	// Returns 0, nil immediately if cutoff is zero.
	// .eml files are deleted first, then the index is updated atomically. This
	// ordering ensures a crash leaves "file gone, index entry present" rather than
	// "entry gone, file orphaned", so the next run can self-heal idempotently.
	// Files already absent from disk are treated as successfully deleted (idempotent).
	// Individual file-delete errors are collected via errors.Join and do not abort
	// the operation. After a successful index update, empty {uidvalidity}/{YYYYMM}
	// and {uidvalidity} directories are removed; cleanup failures are logged as WARN
	// and never returned as errors.
	DeleteEmailsBefore(cutoff time.Time) (deleted int, err error)

	// CountReportsBefore returns the number of report records whose
	// date-range.end-datetime < cutoff, without deleting them. The predicate
	// mirrors DeleteReportsBefore exactly. Works on read-only stores.
	CountReportsBefore(cutoff time.Time) (count int, err error)

	// CountEmailsBefore returns the number of .eml files whose internal_date < cutoff,
	// without deleting them. Returns 0, nil immediately if cutoff is zero. The predicate
	// mirrors DeleteEmailsBefore exactly. Works on read-only stores.
	CountEmailsBefore(cutoff time.Time) (count int, err error)
}

// fileStore is the concrete implementation of Store.
type fileStore struct {
	rootDir       string
	identity      IMAPIdentity
	mode          OpenMode
	readOnly      bool
	dataPath      string
	emailsDirPath string
}

// Open opens the store at rootDir with the given identity in the specified mode.
//
// In read-write mode:
//   - Creates rootDir and emails/ subdirectory (mode 0700) if they do not exist.
//   - Initializes tlsrpt.json with an empty record set if it does not exist.
//   - Creates the sentinel file (.tlsrpt-digest-meta.json) if it does not exist,
//     recording the IMAP identity (host, port, mailbox) and the current time.
//
// In read-only mode (OpenReadOnly):
//   - No files or directories are created.
//   - Missing data files are treated as empty state (no reports, no index).
//   - rootDir is NOT validated for symlinks, directory type, or permissions.
//     Callers that need those guarantees must call validateAndEnsureRootDir
//     (cmd layer) before Open.  Read-only mode is used by the summary subcommand,
//     which intentionally skips that check because it treats a missing rootDir as
//     an empty store rather than an error.
//
// If the sentinel already exists, its stored IMAP identity is verified against the
// supplied identity. A mismatch returns an error containing both the expected and
// actual identifiers along with rootDir.
func Open(rootDir string, identity IMAPIdentity, mode OpenMode) (Store, error) {
	readOnly := mode == OpenReadOnly

	// cleanupCompletedReset: removes a leftover manifest/staging when the commit
	// already landed in the sentinel, without blocking for a live reset in flight.
	// See ADR-0003 §5 for the sentinel-based decision logic.
	if mode == OpenReadWrite {
		if err := cleanupCompletedReset(rootDir); err != nil {
			return nil, err
		}
	}

	if !readOnly {
		if err := ensureDirExists(rootDir); err != nil {
			return nil, fmt.Errorf("Open: ensure root dir: %w", err)
		}

		// OpenRecoverReset skips emails dir creation and data file initialisation:
		// these files may have been moved to staging by an interrupted ResetForRecovery,
		// and re-creating them here would clobber the staged old data on the next rename.
		if mode != OpenRecoverReset {
			emailsDir := emailsPath(rootDir)
			if err := ensureDirExists(emailsDir); err != nil {
				return nil, fmt.Errorf("Open: ensure emails dir: %w", err)
			}

			// Initialize the data file with empty content if it does not exist.
			if err := initDataFile(rootDir); err != nil {
				return nil, fmt.Errorf("Open: init data file: %w", err)
			}
		}

		// Create the guard file so that read-only opens can acquire a shared lock
		// without needing O_CREATE (which would be a write on a read-only mount).
		guardPath := guardFilePath(rootDir)
		f, err := os.OpenFile(guardPath, os.O_CREATE|os.O_RDWR, filePerm) //nolint:gosec
		if err != nil {
			return nil, fmt.Errorf("Open: create guard file: %w", err)
		}
		_ = f.Close()
	}

	sentinel, sentinelExists, err := loadSentinel(rootDir)
	if err != nil {
		return nil, fmt.Errorf("Open: load sentinel: %w", err)
	}

	if sentinelExists {
		if err := verifySentinelIdentity(rootDir, sentinel, identity); err != nil {
			return nil, err
		}
	} else if !readOnly {
		// Read-only mode leaves the sentinel absent (empty store is valid).
		if _, err := initSentinel(rootDir, identity); err != nil {
			return nil, fmt.Errorf("Open: init sentinel: %w", err)
		}
	}

	if sentinelExists {
		checkFilePermissions(sentinelPath(rootDir))
	}

	// initDataFile creates data file with 0600, but a pre-existing file may have
	// looser permissions that went undetected until now.
	if _, statErr := os.Stat(dataFilePath(rootDir)); statErr == nil {
		checkFilePermissions(dataFilePath(rootDir))
	}

	store := &fileStore{
		rootDir:       rootDir,
		identity:      identity,
		mode:          mode,
		readOnly:      readOnly,
		dataPath:      dataFilePath(rootDir),
		emailsDirPath: emailsPath(rootDir),
	}

	return store, nil
}

// emailsPath returns the path to the emails directory within rootDir.
func emailsPath(rootDir string) string {
	return filepath.Join(rootDir, "emails")
}

// dataFilePath returns the path to the JSON data file within rootDir.
func dataFilePath(rootDir string) string {
	return filepath.Join(rootDir, "tlsrpt.json")
}

// initDataFile creates tlsrpt.json with an empty record set if it does not already exist.
func initDataFile(rootDir string) error {
	path := dataFilePath(rootDir)
	if _, err := os.Stat(path); err == nil {
		return nil // Already exists; leave it untouched.
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("initDataFile: stat: %w", err)
	}

	empty := internalDataFile{
		Version: DataFileVersion,
		Reports: []tlsrpt.Report{},
		Emails:  []internalEmailIndexEntry{},
	}
	data, err := json.Marshal(empty)
	if err != nil {
		return fmt.Errorf("initDataFile: marshal: %w", err)
	}
	return atomicWriteFile(path, data)
}

// SaveReport is a package-level utility function that saves a single report.
// It is not part of the Store interface.
func SaveReport(s Store, input ReportInput) error {
	return s.SaveReports([]ReportInput{input})
}
