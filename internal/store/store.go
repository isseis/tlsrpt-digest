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

// errNotImplemented is a placeholder error for stub methods that will be
// implemented in later phases (Phase 2 and Phase 3).
var errNotImplemented = errors.New("store: not implemented")

// Store represents the persistence layer for TLSRPT reports and emails.
// All operations are assumed to be called from a single writer (ensured by external scheduler).
// Read-only mode (OpenReadOnly) prevents write operations and creation of files/directories.
type Store interface {
	// SaveReports persists a batch of TLSRPT reports.
	// Implementations must handle:
	// - AC-07: Non-empty input array
	// - AC-08a: UID/UIDValidity consistency
	// - AC-09: Atomic JSON update
	// - AC-10: report_end_date extraction and max update
	SaveReports(inputs []ReportInput) error

	// SaveEmailMetas persists email metadata to the index (does not save raw .eml files).
	// Used during reprocess to sync email index after SaveReports.
	// Implementations must handle:
	// - AC-08c: UID/UIDValidity batch registration
	// - AC-09: Atomic JSON update
	SaveEmailMetas(metas []EmailMeta) error

	// GetReportsSince retrieves reports where report_end_date > since.
	// Implementations must handle:
	// - AC-11: Time-based filtering
	// - AC-12: Performance (≤1 sec for 10000 reports)
	GetReportsSince(since time.Time) ([]tlsrpt.Report, error)

	// SaveEmail saves a raw .eml file with EmailMeta index entry.
	// Creates emails/{uidvalidity}/{YYYYMM}/ directories as needed.
	// Implements AC-14..AC-18 per 03_implementation_plan.md.
	SaveEmail(uid, uidValidity uint32, sentAt, savedAt time.Time, rawEML []byte) error

	// LoadEmails retrieves all saved emails with index entries.
	// Partial failures (individual .eml parse failures) are aggregated via errors.Join.
	// Implementations must handle:
	// - AC-20: Full email load from .eml files
	// - AC-21: SavedAt extraction from file ctime
	// - AC-22: Partial failure tolerance and errors.Join
	LoadEmails() ([]LoadedEmail, error)

	// SaveUIDValidity persists the IMAP UIDVALIDITY to sentinel.
	// AC-23: Atomic sentinel update
	SaveUIDValidity(v uint32) error

	// LoadUIDValidity retrieves UIDVALIDITY from sentinel.
	// AC-24: Returns found=false if not yet set
	LoadUIDValidity() (v uint32, found bool, err error)

	// SaveRecoveryRequired saves recovery state to sentinel.
	// Indicates that UIDVALIDITY changed from prev to curr and needs manual recovery.
	// Implements F-008 (AC-33..AC-36).
	SaveRecoveryRequired(prev, curr uint32, detectedAt time.Time) error

	// LoadRecoveryRequired retrieves recovery state from sentinel.
	// Returns found=false if not in recovery state (not an error).
	LoadRecoveryRequired() (prev, curr uint32, detectedAt time.Time, found bool, err error)

	// ClearRecoveryRequired removes recovery state from sentinel.
	ClearRecoveryRequired() error

	// ApplyRecovery updates sentinel to accept curr as the new uid_validity
	// and clears the recovery_required state.
	// Must be atomic: both uid_validity and recovery_required are updated together.
	// Implements F-008 (AC-35).
	ApplyRecovery(newUIDValidity uint32) error

	// DeleteReportsBefore deletes reports where report_end_date < cutoff.
	// Implements F-007a (AC-25..AC-28).
	DeleteReportsBefore(cutoff time.Time) (deleted int, err error)

	// DeleteEmailsBefore deletes emails where:
	// - report_end_date < reportCutoff (referenced via index), AND
	// - saved_at < savedAtCutoff
	// Also cleans up empty {uidvalidity}/{YYYYMM}/ directories.
	// Implements F-007b (AC-29..AC-32).
	DeleteEmailsBefore(reportCutoff, savedAtCutoff time.Time) (deleted int, err error)
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
// In read-write mode, creates root_dir and subdirectories if they don't exist.
// In read-only mode, returns an empty store if data files don't exist.
// Implementations must handle:
// - AC-01: Existence verification
// - AC-02: Read-only mode for summary operations
// - AC-03: Initialization with identity
// - AC-04: Sentinel creation and persistence
// - AC-05: Subdirectory creation (read-write mode only)
// - AC-06: Identity verification (current vs sentinel)
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

		// Initialize the data file with empty content if it does not exist (AC-03).
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
// This satisfies AC-03 (data file initialization on first open in read-write mode).
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

// SaveReports implements Store.SaveReports.
// TODO: Phase 2 implementation
func (s *storeImpl) SaveReports(_ []ReportInput) error {
	if s.readOnly {
		return ErrReadOnly
	}
	return errNotImplemented
}

// SaveEmailMetas implements Store.SaveEmailMetas.
// TODO: Phase 2 implementation
func (s *storeImpl) SaveEmailMetas(_ []EmailMeta) error {
	if s.readOnly {
		return ErrReadOnly
	}
	return errNotImplemented
}

// GetReportsSince implements Store.GetReportsSince.
// TODO: Phase 2 implementation
func (s *storeImpl) GetReportsSince(_ time.Time) ([]tlsrpt.Report, error) {
	return nil, errNotImplemented
}

// SaveEmail implements Store.SaveEmail.
// TODO: Phase 2 implementation
func (s *storeImpl) SaveEmail(_, _ uint32, _, _ time.Time, _ []byte) error {
	if s.readOnly {
		return ErrReadOnly
	}
	return errNotImplemented
}

// LoadEmails implements Store.LoadEmails.
// TODO: Phase 3 implementation
func (s *storeImpl) LoadEmails() ([]LoadedEmail, error) {
	return nil, errNotImplemented
}

// SaveUIDValidity implements Store.SaveUIDValidity.
// TODO: Phase 3 implementation
func (s *storeImpl) SaveUIDValidity(_ uint32) error {
	if s.readOnly {
		return ErrReadOnly
	}
	return errNotImplemented
}

// LoadUIDValidity implements Store.LoadUIDValidity.
// TODO: Phase 3 implementation
func (s *storeImpl) LoadUIDValidity() (v uint32, found bool, err error) {
	return 0, false, errNotImplemented
}

// SaveRecoveryRequired implements Store.SaveRecoveryRequired.
// TODO: Phase 3 implementation
func (s *storeImpl) SaveRecoveryRequired(_, _ uint32, _ time.Time) error {
	if s.readOnly {
		return ErrReadOnly
	}
	return errNotImplemented
}

// LoadRecoveryRequired implements Store.LoadRecoveryRequired.
// TODO: Phase 3 implementation
func (s *storeImpl) LoadRecoveryRequired() (prev, curr uint32, detectedAt time.Time, found bool, err error) {
	return 0, 0, time.Time{}, false, errNotImplemented
}

// ClearRecoveryRequired implements Store.ClearRecoveryRequired.
// TODO: Phase 3 implementation
func (s *storeImpl) ClearRecoveryRequired() error {
	if s.readOnly {
		return ErrReadOnly
	}
	return errNotImplemented
}

// ApplyRecovery implements Store.ApplyRecovery.
// TODO: Phase 3 implementation
func (s *storeImpl) ApplyRecovery(_ uint32) error {
	if s.readOnly {
		return ErrReadOnly
	}
	return errNotImplemented
}

// DeleteReportsBefore implements Store.DeleteReportsBefore.
// TODO: Phase 3 implementation
func (s *storeImpl) DeleteReportsBefore(_ time.Time) (deleted int, err error) {
	if s.readOnly {
		return 0, ErrReadOnly
	}
	return 0, errNotImplemented
}

// DeleteEmailsBefore implements Store.DeleteEmailsBefore.
// TODO: Phase 3 implementation
func (s *storeImpl) DeleteEmailsBefore(_, _ time.Time) (deleted int, err error) {
	if s.readOnly {
		return 0, ErrReadOnly
	}
	return 0, errNotImplemented
}

// SaveReport is a package-level utility function that saves a single report.
// It is not part of the Store interface.
func SaveReport(s Store, input ReportInput) error {
	return s.SaveReports([]ReportInput{input})
}
