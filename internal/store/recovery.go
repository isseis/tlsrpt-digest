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

// resetPhase records how far a discard-old reset has progressed.
// Each transition is written to the manifest before the corresponding file operation,
// so a crash at any point leaves a consistent manifest for idempotent resume.
type resetPhase int

const (
	// resetPhaseManifestWritten: manifest written, staging dir created; no files moved yet.
	resetPhaseManifestWritten resetPhase = 1
	// resetPhaseDataStaged: tlsrpt.json moved to staging (or was absent).
	resetPhaseDataStaged resetPhase = 2
	// resetPhaseEmailsStaged: emails/ moved to staging (or was absent).
	resetPhaseEmailsStaged resetPhase = 3
	// resetPhaseCommitted: sentinel committed (recovery_required cleared).
	resetPhaseCommitted resetPhase = 4
)

// resetManifest records the state of an in-progress discard-old reset.
type resetManifest struct {
	Version         int        `json:"version"`
	CurrUIDValidity uint32     `json:"curr_uid_validity"`
	Phase           resetPhase `json:"phase"`
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

// stageDataFile moves tlsrpt.json into stagingPath if it exists in rootDir.
func stageDataFile(rootDir, stagingPath string) error {
	dataPath := dataFilePath(rootDir)
	if _, err := os.Stat(dataPath); err == nil {
		if err := os.Rename(dataPath, filepath.Join(stagingPath, "tlsrpt.json")); err != nil {
			return fmt.Errorf("stageDataFile: move data file: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("stageDataFile: stat data file: %w", err)
	}
	return nil
}

// stageEmailsDir moves emails/ into stagingPath if it exists in rootDir.
func stageEmailsDir(rootDir, stagingPath string) error {
	emailsDir := emailsPath(rootDir)
	if _, err := os.Stat(emailsDir); err == nil {
		if err := os.Rename(emailsDir, filepath.Join(stagingPath, "emails")); err != nil {
			return fmt.Errorf("stageEmailsDir: move emails dir: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("stageEmailsDir: stat emails dir: %w", err)
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
//
// Crash safety: the manifest is written BEFORE any destructive file operation so
// that OpenReadWrite fails with ErrPendingReset and AbortReset can roll back from
// any point.  Each subsequent phase is recorded in the manifest before the
// corresponding operation; re-running after any crash resumes from the last
// durable phase and converges to "empty store + current UIDVALIDITY +
// recovery-required cleared".
//
// Phase progression: manifest_written(1) → data_staged(2) → emails_staged(3) →
// committed(4) → cleanup.
func (s *storeImpl) ResetForRecovery(currUIDValidity uint32) error {
	if s.mode != OpenRecoverReset {
		return ErrInvalidStoreMode
	}

	manifestPath := resetManifestPath(s.rootDir)
	stagingPath := resetStagingPath(s.rootDir)

	mfst, manifestErr := readResetManifest(manifestPath)
	if manifestErr != nil && !errors.Is(manifestErr, os.ErrNotExist) {
		return fmt.Errorf("ResetForRecovery: read manifest: %w", manifestErr)
	}
	manifestExists := manifestErr == nil
	if manifestExists && mfst.Version != resetManifestVersion {
		return &ErrResetManifestVersionMismatch{Got: mfst.Version, Want: resetManifestVersion}
	}

	if !manifestExists {
		// Fresh start: validate preconditions before touching anything.
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

		// Write manifest FIRST (before any file moves) under the exclusive guard lock.
		// This makes the pending reset immediately visible to OpenReadWrite (→ ErrPendingReset)
		// and to AbortReset, so the operation is rollback-able from this point onward.
		if err := s.withGuardExclusive(func() error {
			if err := os.MkdirAll(stagingPath, dirPerm); err != nil {
				return fmt.Errorf("ResetForRecovery: create staging dir: %w", err)
			}
			return writeResetManifest(manifestPath, resetManifest{
				Version:         resetManifestVersion,
				CurrUIDValidity: currUIDValidity,
				Phase:           resetPhaseManifestWritten,
			})
		}); err != nil {
			return fmt.Errorf("ResetForRecovery: write manifest: %w", err)
		}
		mfst = resetManifest{Version: resetManifestVersion, CurrUIDValidity: currUIDValidity, Phase: resetPhaseManifestWritten}
	}

	// Use CurrUIDValidity from manifest for idempotency on resume.
	currUIDValidity = mfst.CurrUIDValidity

	// Legacy manifests written by old code have Phase=0 (field absent in JSON).
	// They were written after staging completed but before commit, so treat as emails_staged.
	phase := mfst.Phase
	if phase == 0 {
		phase = resetPhaseEmailsStaged
	}

	if err := s.advanceResetPhases(phase, currUIDValidity, stagingPath, manifestPath); err != nil {
		return err
	}

	// Staging dir cleanup is best-effort: a stale staging dir is harmless to normal
	// data paths and is cleaned up on the next run.
	_ = os.RemoveAll(stagingPath)
	// Manifest removal is required: if the manifest survives, Open(OpenReadWrite)
	// will permanently return ErrPendingReset.
	if err := os.Remove(manifestPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("ResetForRecovery: remove manifest: %w", err)
	}
	return nil
}

// advanceResetPhases drives the phase progression from the given phase to committed.
// Each step writes the next phase to the manifest before the corresponding operation,
// so a crash inside any step is safe to resume.
func (s *storeImpl) advanceResetPhases(phase resetPhase, currUIDValidity uint32, stagingPath, manifestPath string) error {
	if phase <= resetPhaseManifestWritten {
		// MkdirAll is defensive: staging dir should already exist from the initial write,
		// but guard against edge cases (e.g. partial RemoveAll from a previous run).
		if err := os.MkdirAll(stagingPath, dirPerm); err != nil {
			return fmt.Errorf("advanceResetPhases: ensure staging dir: %w", err)
		}
		if err := stageDataFile(s.rootDir, stagingPath); err != nil {
			return fmt.Errorf("advanceResetPhases: stage data file: %w", err)
		}
		if err := writeResetManifest(manifestPath, resetManifest{
			Version: resetManifestVersion, CurrUIDValidity: currUIDValidity,
			Phase: resetPhaseDataStaged,
		}); err != nil {
			return fmt.Errorf("advanceResetPhases: advance to data_staged: %w", err)
		}
		phase = resetPhaseDataStaged
	}

	if phase <= resetPhaseDataStaged {
		if err := stageEmailsDir(s.rootDir, stagingPath); err != nil {
			return fmt.Errorf("advanceResetPhases: stage emails dir: %w", err)
		}
		if err := writeResetManifest(manifestPath, resetManifest{
			Version: resetManifestVersion, CurrUIDValidity: currUIDValidity,
			Phase: resetPhaseEmailsStaged,
		}); err != nil {
			return fmt.Errorf("advanceResetPhases: advance to emails_staged: %w", err)
		}
		phase = resetPhaseEmailsStaged
	}

	if phase <= resetPhaseEmailsStaged {
		if err := s.commitReset(manifestPath, currUIDValidity); err != nil {
			return fmt.Errorf("advanceResetPhases: commit: %w", err)
		}
	}
	return nil
}

// commitReset atomically clears recovery_required, writes the new UIDValidity, and
// advances the manifest to resetPhaseCommitted under the exclusive guard lock.
func (s *storeImpl) commitReset(manifestPath string, currUIDValidity uint32) error {
	return s.withGuardExclusive(func() error {
		sentinel, err := s.loadOrInitSentinelForWrite()
		if err != nil {
			return fmt.Errorf("commitReset: load sentinel: %w", err)
		}
		sentinel.UIDValidity = &currUIDValidity
		sentinel.RecoveryRequired = nil
		if err := saveSentinel(s.rootDir, sentinel); err != nil {
			return fmt.Errorf("commitReset: save sentinel: %w", err)
		}
		s.sentinel = sentinel
		return writeResetManifest(manifestPath, resetManifest{
			Version: resetManifestVersion, CurrUIDValidity: currUIDValidity,
			Phase: resetPhaseCommitted,
		})
	})
}

// AbortReset implements Store.AbortReset.
// Only valid when a pre-commit pending reset manifest exists (phases 1–3).
func (s *storeImpl) AbortReset() error {
	if s.mode != OpenRecoverReset {
		return ErrInvalidStoreMode
	}

	manifestPath := resetManifestPath(s.rootDir)
	stagingPath := resetStagingPath(s.rootDir)

	mfst, err := readResetManifest(manifestPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ErrResetNotPending
		}
		return fmt.Errorf("AbortReset: read manifest: %w", err)
	}
	if mfst.Version != resetManifestVersion {
		return &ErrResetManifestVersionMismatch{Got: mfst.Version, Want: resetManifestVersion}
	}

	// Determine whether the reset is still pre-commit.
	// Legacy manifests (Phase=0) were written by old code after staging but before commit;
	// fall back to the recovery_required sentinel field to distinguish.
	phase := mfst.Phase
	if phase == 0 {
		_, _, _, found, err := s.LoadRecoveryRequired()
		if err != nil {
			return fmt.Errorf("AbortReset: check recovery-required: %w", err)
		}
		if !found {
			return ErrResetNotPending
		}
		phase = resetPhaseEmailsStaged
	}

	if phase >= resetPhaseCommitted {
		return ErrResetNotPending
	}

	// Pre-commit state: restore staged files back to rootDir (idempotent).
	// restoreFromStaging handles ErrNotExist gracefully, so it is safe regardless
	// of which phase the crash occurred in.
	if err := restoreFromStaging(s.rootDir, stagingPath); err != nil {
		return fmt.Errorf("AbortReset: restore from staging: %w", err)
	}
	// Recovery-required remains in the sentinel so the caller can retry.
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
