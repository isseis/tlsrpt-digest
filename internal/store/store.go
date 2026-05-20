// Package store provides persistent storage for TLSRPT reports and emails.
package store

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/isseis/tlsrpt-digest/internal/tlsrpt"
)

// Store represents the persistence layer for TLSRPT reports and emails.
// All operations are assumed to be called from a single writer (ensured by external scheduler).
// Read-only mode (OpenReadOnly) prevents write operations and creation of files/directories.
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
	SaveEmailMetas(metas []EmailMeta) error

	// GetReportsSince retrieves all reports whose date-range.end-datetime >= since.
	// Filtering is by the report period end time, not by the storage time.
	// Returns an empty slice (not an error) when no reports match.
	GetReportsSince(since time.Time) ([]tlsrpt.Report, error)

	// SaveEmail saves a raw .eml file to
	// {root_dir}/emails/{uidvalidity}/{YYYYMM}/{uid}.eml
	// (uid zero-padded to 10 digits; YYYYMM derived from internalDate).
	// Creates subdirectories as needed (mode 0700). The write is atomic (temp file + rename).
	// If the file already exists for the same uid and uidvalidity, the call is a no-op
	// (idempotent, no error returned). Returns an error if internalDate is zero.
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
}

// storeImpl is the concrete implementation of Store.
type storeImpl struct {
	rootDir       string
	identity      IMAPIdentity
	mode          OpenMode
	readOnly      bool
	dataPath      string
	emailsDirPath string
	sentinel      *internalSentinelFile
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
//
// If the sentinel already exists, its stored IMAP identity is verified against the
// supplied identity. A mismatch returns an error containing both the expected and
// actual identifiers along with rootDir.
func Open(rootDir string, identity IMAPIdentity, mode OpenMode) (Store, error) {
	// Determine if read-only based on mode
	readOnly := mode == OpenReadOnly

	// In read-write mode, ensure directories exist
	if !readOnly {
		if err := ensureDirExists(rootDir); err != nil {
			return nil, fmt.Errorf("Open: ensure root dir: %w", err)
		}

		emailsDir := emailsPath(rootDir)
		if err := ensureDirExists(emailsDir); err != nil {
			return nil, fmt.Errorf("Open: ensure emails dir: %w", err)
		}

		// Initialize the data file with empty content if it does not exist.
		if err := initDataFile(rootDir); err != nil {
			return nil, fmt.Errorf("Open: init data file: %w", err)
		}
	}

	// Load or initialize sentinel
	sentinel, sentinelExists, err := loadSentinel(rootDir)
	if err != nil {
		return nil, fmt.Errorf("Open: load sentinel: %w", err)
	}

	if sentinelExists {
		// Verify identity matches
		if err := verifySentinelIdentity(rootDir, sentinel, identity); err != nil {
			return nil, err
		}
	} else {
		// In read-only mode, don't create sentinel; return empty store
		if readOnly {
			sentinel = &internalSentinelFile{
				FormatVersion: SentinelFormatVersion,
				IMAPHost:      identity.Host,
				IMAPPort:      identity.Port,
				IMAPMailbox:   identity.Mailbox,
			}
		} else {
			// In read-write mode, initialize sentinel
			newSentinel, err := initSentinel(rootDir, identity)
			if err != nil {
				return nil, fmt.Errorf("Open: init sentinel: %w", err)
			}
			sentinel = newSentinel
		}
	}

	// Check permissions on existing sentinel file and warn if loose.
	if sentinelExists {
		checkFilePermissions(sentinelPath(rootDir))
	}

	// Check permissions on the data file when it already exists.
	// initDataFile creates it with 0600, but a pre-existing file may have looser
	// permissions that went undetected until now.
	if _, statErr := os.Stat(dataFilePath(rootDir)); statErr == nil {
		checkFilePermissions(dataFilePath(rootDir))
	}

	store := &storeImpl{
		rootDir:       rootDir,
		identity:      identity,
		mode:          mode,
		readOnly:      readOnly,
		dataPath:      dataFilePath(rootDir),
		emailsDirPath: emailsPath(rootDir),
		sentinel:      sentinel,
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
	} else if !os.IsNotExist(err) {
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
