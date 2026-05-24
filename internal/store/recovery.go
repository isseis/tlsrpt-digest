// Package store provides persistent storage for TLSRPT reports and emails.
package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/unix"
)

// ---- internal file paths for pending reset ----

const (
	guardFilename    = ".tlsrpt-digest-summary.lock"
	manifestFilename = ".tlsrpt-digest-reset-manifest.json"
	stagingDirName   = ".tlsrpt-digest-staging"
)

func guardFilePath(rootDir string) string {
	return filepath.Join(rootDir, guardFilename)
}

func resetManifestPath(rootDir string) string {
	return filepath.Join(rootDir, manifestFilename)
}

func resetStagingPath(rootDir string) string {
	return filepath.Join(rootDir, stagingDirName)
}

// resetManifest records the state of an in-progress discard-old reset.
type resetManifest struct {
	Version         int    `json:"version"`
	CurrUIDValidity uint32 `json:"curr_uid_validity"`
}

const resetManifestVersion = 1

// writeResetManifest atomically writes the reset manifest.
func writeResetManifest(path string, m resetManifest) error {
	data, err := json.Marshal(m)
	if err != nil {
		return fmt.Errorf("writeResetManifest: marshal: %w", err)
	}
	return atomicWriteFile(path, data)
}

// readResetManifest reads and parses the reset manifest.
func readResetManifest(path string) (resetManifest, error) {
	data, err := os.ReadFile(path) //nolint:gosec // path constructed from rootDir under caller control
	if err != nil {
		return resetManifest{}, fmt.Errorf("readResetManifest: read: %w", err)
	}
	var m resetManifest
	if err := json.Unmarshal(data, &m); err != nil {
		return resetManifest{}, fmt.Errorf("readResetManifest: unmarshal: %w", err)
	}
	return m, nil
}

// ---- guard file locking ----

// withGuardExclusive opens the guard file, acquires a blocking exclusive flock,
// calls fn, and releases the lock when fn returns (or on deferred close).
// Writers that modify recovery-required in the sentinel must call this to prevent
// a race with summary processes holding a shared flock on the same file.
func (s *storeImpl) withGuardExclusive(fn func() error) error {
	guardPath := guardFilePath(s.rootDir)
	f, err := os.OpenFile(guardPath, os.O_CREATE|os.O_RDWR, filePerm) //nolint:gosec
	if err != nil {
		return fmt.Errorf("withGuardExclusive: open guard file: %w", err)
	}
	defer func() { _ = f.Close() }()
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX); err != nil { //nolint:gosec // fd fits int on all supported platforms
		return fmt.Errorf("withGuardExclusive: acquire exclusive lock: %w", err)
	}
	return fn()
}

// ---- SummaryConsistencyGuard implementation ----

type summaryConsistencyGuardImpl struct {
	rootDir string
	f       *os.File
}

func (g *summaryConsistencyGuardImpl) CheckRecoveryRequired(_ context.Context) (bool, error) {
	sentinel, exists, err := loadSentinel(g.rootDir)
	if err != nil {
		return false, fmt.Errorf("CheckRecoveryRequired: load sentinel: %w", err)
	}
	if !exists || sentinel.RecoveryRequired == nil {
		return false, nil
	}
	return true, nil
}

func (g *summaryConsistencyGuardImpl) Close() error {
	return g.f.Close()
}

// noopSummaryConsistencyGuard is returned when rootDir does not exist (empty store,
// OpenReadOnly). Recovery-required can never be set without a store directory, so
// CheckRecoveryRequired always returns false. Close is a no-op.
type noopSummaryConsistencyGuard struct{}

func (noopSummaryConsistencyGuard) CheckRecoveryRequired(_ context.Context) (bool, error) {
	return false, nil
}

func (noopSummaryConsistencyGuard) Close() error { return nil }

// ---- staging helpers ----

// stageOldData moves tlsrpt.json and emails/ into stagingPath (must already exist).
func stageOldData(rootDir, stagingPath string) error {
	dataPath := dataFilePath(rootDir)
	if _, err := os.Stat(dataPath); err == nil {
		if err := os.Rename(dataPath, filepath.Join(stagingPath, "tlsrpt.json")); err != nil {
			return fmt.Errorf("stageOldData: move data file: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("stageOldData: stat data file: %w", err)
	}
	emailsDir := emailsPath(rootDir)
	if _, err := os.Stat(emailsDir); err == nil {
		if err := os.Rename(emailsDir, filepath.Join(stagingPath, "emails")); err != nil {
			return fmt.Errorf("stageOldData: move emails dir: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("stageOldData: stat emails dir: %w", err)
	}
	return nil
}

// restoreFromStaging moves files from stagingPath back to rootDir.
func restoreFromStaging(rootDir, stagingPath string) error {
	stagedData := filepath.Join(stagingPath, "tlsrpt.json")
	if _, err := os.Stat(stagedData); err == nil {
		if err := os.Rename(stagedData, dataFilePath(rootDir)); err != nil {
			return fmt.Errorf("restoreFromStaging: restore data file: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("restoreFromStaging: stat staged data file: %w", err)
	}
	stagedEmails := filepath.Join(stagingPath, "emails")
	if _, err := os.Stat(stagedEmails); err == nil {
		if err := os.Rename(stagedEmails, emailsPath(rootDir)); err != nil {
			return fmt.Errorf("restoreFromStaging: restore emails dir: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("restoreFromStaging: stat staged emails dir: %w", err)
	}
	return nil
}

// loadOrInitSentinelForWrite loads the sentinel for modification.
// If the sentinel file does not exist, it returns a freshly initialised
// sentinel so callers can modify and persist it without a separate nil check.
func (s *storeImpl) loadOrInitSentinelForWrite() (*internalSentinelFile, error) {
	sentinel, _, err := loadSentinel(s.rootDir)
	if err != nil {
		return nil, err
	}
	if sentinel == nil {
		sentinel = &internalSentinelFile{
			FormatVersion: SentinelFormatVersion,
			IMAPHost:      s.identity.Host,
			IMAPPort:      s.identity.Port,
			IMAPMailbox:   s.identity.Mailbox,
			InitializedAt: time.Now().UTC(),
		}
	}
	return sentinel, nil
}

// SaveUIDValidity implements Store.SaveUIDValidity.
func (s *storeImpl) SaveUIDValidity(v uint32) error {
	if s.readOnly {
		return ErrReadOnly
	}
	sentinel, err := s.loadOrInitSentinelForWrite()
	if err != nil {
		return fmt.Errorf("SaveUIDValidity: load sentinel: %w", err)
	}
	sentinel.UIDValidity = &v
	if err := saveSentinel(s.rootDir, sentinel); err != nil {
		return fmt.Errorf("SaveUIDValidity: save sentinel: %w", err)
	}
	s.sentinel = sentinel
	return nil
}

// LoadUIDValidity implements Store.LoadUIDValidity.
func (s *storeImpl) LoadUIDValidity() (v uint32, found bool, err error) {
	sentinel, exists, err := loadSentinel(s.rootDir)
	if err != nil {
		return 0, false, fmt.Errorf("LoadUIDValidity: load sentinel: %w", err)
	}
	if !exists || sentinel.UIDValidity == nil {
		return 0, false, nil
	}
	return *sentinel.UIDValidity, true, nil
}

// SaveRecoveryRequired implements Store.SaveRecoveryRequired.
func (s *storeImpl) SaveRecoveryRequired(prev, curr uint32, detectedAt time.Time) error {
	if s.readOnly {
		return ErrReadOnly
	}
	return s.withGuardExclusive(func() error {
		sentinel, err := s.loadOrInitSentinelForWrite()
		if err != nil {
			return fmt.Errorf("SaveRecoveryRequired: load sentinel: %w", err)
		}
		sentinel.RecoveryRequired = &internalRecoveryState{
			PrevUIDValidity: prev,
			CurrUIDValidity: curr,
			DetectedAt:      detectedAt,
		}
		if err := saveSentinel(s.rootDir, sentinel); err != nil {
			return fmt.Errorf("SaveRecoveryRequired: save sentinel: %w", err)
		}
		s.sentinel = sentinel
		return nil
	})
}

// LoadRecoveryRequired implements Store.LoadRecoveryRequired.
func (s *storeImpl) LoadRecoveryRequired() (prev, curr uint32, detectedAt time.Time, found bool, err error) {
	sentinel, exists, err := loadSentinel(s.rootDir)
	if err != nil {
		return 0, 0, time.Time{}, false, fmt.Errorf("LoadRecoveryRequired: load sentinel: %w", err)
	}
	if !exists || sentinel.RecoveryRequired == nil {
		return 0, 0, time.Time{}, false, nil
	}
	rs := sentinel.RecoveryRequired
	return rs.PrevUIDValidity, rs.CurrUIDValidity, rs.DetectedAt, true, nil
}

// ClearRecoveryRequired implements Store.ClearRecoveryRequired.
func (s *storeImpl) ClearRecoveryRequired() error {
	if s.readOnly {
		return ErrReadOnly
	}
	sentinel, _, err := loadSentinel(s.rootDir)
	if err != nil {
		return fmt.Errorf("ClearRecoveryRequired: load sentinel: %w", err)
	}
	if sentinel == nil {
		return nil
	}
	sentinel.RecoveryRequired = nil
	if err := saveSentinel(s.rootDir, sentinel); err != nil {
		return fmt.Errorf("ClearRecoveryRequired: save sentinel: %w", err)
	}
	s.sentinel = sentinel
	return nil
}

// ApplyRecovery implements Store.ApplyRecovery.
func (s *storeImpl) ApplyRecovery(newUIDValidity uint32) error {
	if s.readOnly {
		return ErrReadOnly
	}
	return s.withGuardExclusive(func() error {
		sentinel, err := s.loadOrInitSentinelForWrite()
		if err != nil {
			return fmt.Errorf("ApplyRecovery: load sentinel: %w", err)
		}
		sentinel.UIDValidity = &newUIDValidity
		sentinel.RecoveryRequired = nil
		if err := saveSentinel(s.rootDir, sentinel); err != nil {
			return fmt.Errorf("ApplyRecovery: save sentinel: %w", err)
		}
		s.sentinel = sentinel
		return nil
	})
}

// ResetForRecovery implements Store.ResetForRecovery.
// The operation is crash-safe: re-running after any intermediate failure converges
// to "empty store + current UIDVALIDITY + recovery-required cleared".
func (s *storeImpl) ResetForRecovery(currUIDValidity uint32) error {
	if s.mode != OpenRecoverReset {
		return ErrInvalidStoreMode
	}

	manifestPath := resetManifestPath(s.rootDir)
	stagingPath := resetStagingPath(s.rootDir)

	// Load existing manifest to detect an in-progress reset.
	mfst, manifestErr := readResetManifest(manifestPath)
	if manifestErr != nil && !errors.Is(manifestErr, os.ErrNotExist) {
		return fmt.Errorf("ResetForRecovery: read manifest: %w", manifestErr)
	}
	manifestExists := manifestErr == nil
	if manifestExists && mfst.Version != resetManifestVersion {
		return &ErrResetManifestVersionMismatch{Got: mfst.Version, Want: resetManifestVersion}
	}

	if !manifestExists {
		// Fresh start: check preconditions.
		_, sentinelCurr, _, found, err := s.LoadRecoveryRequired()
		if err != nil {
			return fmt.Errorf("ResetForRecovery: load recovery-required: %w", err)
		}
		if !found {
			return ErrRecoveryRequiredMissing
		}
		if sentinelCurr != currUIDValidity {
			return &ErrRecoveryUIDValidityMismatch{Got: currUIDValidity, Expected: sentinelCurr}
		}

		// Stage old data under the exclusive guard lock.
		if err := s.withGuardExclusive(func() error {
			if err := os.MkdirAll(stagingPath, dirPerm); err != nil {
				return fmt.Errorf("ResetForRecovery: create staging dir: %w", err)
			}
			if err := stageOldData(s.rootDir, stagingPath); err != nil {
				return err
			}
			return writeResetManifest(manifestPath, resetManifest{
				Version:         resetManifestVersion,
				CurrUIDValidity: currUIDValidity,
			})
		}); err != nil {
			return fmt.Errorf("ResetForRecovery: stage: %w", err)
		}
		mfst = resetManifest{Version: resetManifestVersion, CurrUIDValidity: currUIDValidity}
	} else {
		// Resuming: use the currUIDValidity recorded in the manifest for idempotency.
		currUIDValidity = mfst.CurrUIDValidity
	}

	// Check whether the commit already happened (recovery-required gone).
	_, _, _, found, err := s.LoadRecoveryRequired()
	if err != nil {
		return fmt.Errorf("ResetForRecovery: check recovery-required post-stage: %w", err)
	}

	if found {
		// Commit: atomically update sentinel under the exclusive guard lock.
		if err := s.withGuardExclusive(func() error {
			sentinel, err := s.loadOrInitSentinelForWrite()
			if err != nil {
				return fmt.Errorf("ResetForRecovery: commit load sentinel: %w", err)
			}
			sentinel.UIDValidity = &currUIDValidity
			sentinel.RecoveryRequired = nil
			if err := saveSentinel(s.rootDir, sentinel); err != nil {
				return fmt.Errorf("ResetForRecovery: commit save sentinel: %w", err)
			}
			s.sentinel = sentinel
			return nil
		}); err != nil {
			return err
		}
	}

	// Staging dir cleanup is best-effort: a stale staging dir is harmless to
	// normal data paths and is cleaned up on the next run.
	_ = os.RemoveAll(stagingPath)
	// Manifest removal is not best-effort: if the manifest survives, Open(OpenReadWrite)
	// will permanently return ErrPendingReset.
	if err := os.Remove(manifestPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("ResetForRecovery: remove manifest: %w", err)
	}
	return nil
}

// AbortReset implements Store.AbortReset.
// Only valid when a pre-commit pending reset manifest exists.
func (s *storeImpl) AbortReset() error {
	if s.mode != OpenRecoverReset {
		return ErrInvalidStoreMode
	}

	manifestPath := resetManifestPath(s.rootDir)
	stagingPath := resetStagingPath(s.rootDir)

	if _, err := os.Stat(manifestPath); os.IsNotExist(err) {
		return ErrResetNotPending
	}

	// If the commit already happened (recovery-required is gone), abort is no longer possible.
	_, _, _, found, err := s.LoadRecoveryRequired()
	if err != nil {
		return fmt.Errorf("AbortReset: check recovery-required: %w", err)
	}
	if !found {
		return ErrResetNotPending
	}

	// Pre-commit state: restore files from staging.
	if err := restoreFromStaging(s.rootDir, stagingPath); err != nil {
		return fmt.Errorf("AbortReset: restore from staging: %w", err)
	}
	// Remove staging dir and manifest. Recovery-required remains in the sentinel.
	_ = os.RemoveAll(stagingPath)
	if err := os.Remove(manifestPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("AbortReset: remove manifest: %w", err)
	}
	return nil
}

// AcquireSummaryConsistencyGuard implements Store.AcquireSummaryConsistencyGuard.
// When rootDir does not exist (empty-store OpenReadOnly path), a no-op guard is
// returned: recovery-required cannot be set without a store directory, so
// CheckRecoveryRequired always returns false and Close is a no-op.
func (s *storeImpl) AcquireSummaryConsistencyGuard() (SummaryConsistencyGuard, error) {
	if _, err := os.Stat(s.rootDir); os.IsNotExist(err) {
		return noopSummaryConsistencyGuard{}, nil
	}
	guardPath := guardFilePath(s.rootDir)
	f, err := os.OpenFile(guardPath, os.O_RDONLY, filePerm) //nolint:gosec
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return noopSummaryConsistencyGuard{}, nil
		}
		return nil, fmt.Errorf("AcquireSummaryConsistencyGuard: open guard file: %w", err)
	}
	if err := unix.Flock(int(f.Fd()), unix.LOCK_SH|unix.LOCK_NB); err != nil { //nolint:gosec // fd fits int on all supported platforms
		_ = f.Close()
		return nil, fmt.Errorf("AcquireSummaryConsistencyGuard: acquire shared lock: %w", err)
	}
	return &summaryConsistencyGuardImpl{rootDir: s.rootDir, f: f}, nil
}
