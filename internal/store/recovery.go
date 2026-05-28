// Package store provides persistent storage for TLSRPT reports and emails.
package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
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
// See docs/dev/adr/0003_reset_phase_design.md for the crash-safety
// rationale behind the phase values and commit/abort boundaries.
//
// Forward (commit) progression: 1 → 2 → 3 → 4.
// Phase 1 (manifest_written) is a write-ahead log entry written before any
// destructive operation, making the reset visible to Open and AbortReset from
// that point on.  Phases 2–4 are checkpoints written after each idempotent
// operation; a crash before a checkpoint is safe to resume by re-running the
// (idempotent) operation.
//
// Backward (abort) progression: → 5 (aborting).
// AbortReset writes phase=aborting before moving any file back to root, so a
// crash mid-restore cannot be confused with a forward-progressing reset.
// ResetForRecovery refuses phase=aborting with ErrResetAbortInProgress.
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
	// resetPhaseAborting: AbortReset has been started; restore from staging is in progress
	// or pending.  Only AbortReset may resume this state; ResetForRecovery must refuse.
	resetPhaseAborting resetPhase = 5
)

// resetManifest records the state of an in-progress discard-old reset.
type resetManifest struct {
	Version         int        `json:"version"`
	CurrUIDValidity uint32     `json:"curr_uid_validity"`
	Phase           resetPhase `json:"phase"`
}

const resetManifestVersion = 1

// validateManifestPhase ensures the manifest's phase is in the known range
// (1–5).  Unknown values are rejected fail-closed so manifest/staging are
// preserved for manual inspection.
func validateManifestPhase(p resetPhase) error {
	if p < resetPhaseManifestWritten || p > resetPhaseAborting {
		return &ErrResetManifestPhaseUnknown{Got: int(p)}
	}
	return nil
}

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

// initResetManifest validates fresh-start preconditions, wipes any stale staging
// directory, creates a new staging directory, and writes the initial manifest.
// It returns the written manifest so the caller can proceed to advanceResetPhases.
func (s *storeImpl) initResetManifest(currUIDValidity uint32, stagingPath, manifestPath string) (resetManifest, error) {
	_, sentinelCurr, _, found, err := s.LoadRecoveryRequired()
	if err != nil {
		return resetManifest{}, fmt.Errorf("ResetForRecovery: load recovery-required: %w", err)
	}
	if !found {
		return resetManifest{}, ErrRecoveryRequiredMissing
	}
	if sentinelCurr != currUIDValidity {
		return resetManifest{}, &ErrRecoveryUIDValidityMismatch{Got: currUIDValidity, Expected: sentinelCurr}
	}

	// Write manifest FIRST before any file moves.
	// This makes the pending reset immediately visible to OpenReadWrite (→ ErrPendingReset)
	// and to AbortReset, so the operation is rollback-able from this point onward.
	// Caller-held process writer lock serializes this whole reset against other writers;
	// the summary consistency guard is not needed here because recovery_required is not
	// modified until commitReset.
	//
	// Wipe any stale staging dir first: a previous reset that committed but
	// failed to clean up RemoveAll(staging) could leave files there, which
	// would later make stageEmailsDir fail (rename onto a non-empty dir).
	// At this point there is no manifest, so the staging contents (if any)
	// are guaranteed stale and safe to remove.
	if err := os.RemoveAll(stagingPath); err != nil {
		return resetManifest{}, fmt.Errorf("ResetForRecovery: clean stale staging dir: %w", err)
	}
	if err := os.MkdirAll(stagingPath, dirPerm); err != nil {
		return resetManifest{}, fmt.Errorf("ResetForRecovery: create staging dir: %w", err)
	}
	mfst := resetManifest{Version: resetManifestVersion, CurrUIDValidity: currUIDValidity, Phase: resetPhaseManifestWritten}
	if err := writeResetManifest(manifestPath, mfst); err != nil {
		return resetManifest{}, fmt.Errorf("ResetForRecovery: write manifest: %w", err)
	}
	return mfst, nil
}

// removeStaleCommittedManifest removes a reset manifest whose phase is already
// committed along with any leftover staging directory.  It is used by
// ResetForRecovery to clear bookkeeping from a previous reset whose final
// cleanup failed, so a subsequent fresh-start reset proceeds cleanly.
func removeStaleCommittedManifest(stagingPath, manifestPath string) error {
	if err := os.RemoveAll(stagingPath); err != nil {
		return fmt.Errorf("ResetForRecovery: clean stale staging: %w", err)
	}
	if err := os.Remove(manifestPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("ResetForRecovery: remove stale committed manifest: %w", err)
	}
	return nil
}

// cleanupCompletedReset removes a leftover reset manifest (and any staging dir)
// when the underlying commit has already been applied to the sentinel.  It is
// called from Open(OpenReadWrite) so a crash between commit and final cleanup
// does not permanently fail-close the normal data path.
//
// Invariants used here:
//   - The sentinel's recovery_required field is the TRUE commit barrier.
//     commitReset always saves the sentinel BEFORE advancing the manifest to
//     phase=committed, so once recovery_required is nil the user-visible reset
//     is complete and the manifest/staging are leftover bookkeeping.
//   - The remaining staging contents (if any) are old data the operator
//     explicitly asked to discard via ResetForRecovery, so RemoveAll is safe.
//
// Returns:
//   - nil if there is no manifest, or the manifest was cleaned up (or cleanup failed
//     best-effort — the failure is logged as WARN and does not block the caller).
//   - ErrPendingReset if the manifest exists and recovery_required is still set
//     (i.e. a forward reset is genuinely in progress, or an AbortReset is
//     partially applied — both must be resolved via OpenRecoverReset).
//   - A *ErrResetManifestVersionMismatch / *ErrResetManifestPhaseUnknown for
//     unreadable or out-of-range manifests (fail closed for manual handling).
func cleanupCompletedReset(rootDir string) error {
	manifestPath := resetManifestPath(rootDir)
	mfst, err := readResetManifest(manifestPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			// No manifest.  Check for an orphaned staging directory.
			// A staging dir without a manifest is always stale: the manifest WAL
			// entry (phase=1) is written before any file is moved into staging, so
			// if the manifest is absent the staged contents can never be recovered
			// and are safe to discard.  This covers the narrow window where
			// RemoveAll(staging) failed after a successful manifest removal.
			stagingPath := resetStagingPath(rootDir)
			if _, statErr := os.Stat(stagingPath); statErr == nil {
				if rmErr := os.RemoveAll(stagingPath); rmErr != nil {
					slog.Warn("store: failed to remove orphaned staging directory; manual cleanup may be required",
						slog.String("path", stagingPath),
						slog.Any("error", rmErr),
					)
				}
			}
			return nil
		}
		return fmt.Errorf("cleanupCompletedReset: read manifest: %w", err)
	}
	if mfst.Version != resetManifestVersion {
		return &ErrResetManifestVersionMismatch{Got: mfst.Version, Want: resetManifestVersion}
	}
	if err := validateManifestPhase(mfst.Phase); err != nil {
		return err
	}

	sentinel, exists, err := loadSentinel(rootDir)
	if err != nil {
		return fmt.Errorf("cleanupCompletedReset: load sentinel: %w", err)
	}
	// Sentinel missing while a manifest is present is unexpected — refuse to
	// guess the commit state and let the operator resolve via recover.
	if !exists || sentinel == nil {
		return ErrPendingReset
	}
	if sentinel.RecoveryRequired != nil {
		return ErrPendingReset
	}

	// Commit happened (recovery_required cleared); the manifest is leftover.
	// Best-effort cleanup mirrors the tail of ResetForRecovery.
	slog.Warn("store: cleaning up leftover reset manifest after committed reset",
		slog.String("root_dir", rootDir),
		slog.Int("manifest_phase", int(mfst.Phase)),
	)
	if err := os.RemoveAll(resetStagingPath(rootDir)); err != nil {
		slog.Warn("store: failed to remove stale staging directory; manual cleanup may be required",
			slog.String("path", resetStagingPath(rootDir)),
			slog.Any("error", err),
		)
	}
	if err := os.Remove(manifestPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("cleanupCompletedReset: remove manifest: %w", err)
	}
	return nil
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
	return s.withGuardExclusive(func() error {
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
		return nil
	})
}

// ApplyRecovery implements Store.ApplyRecovery.
func (s *storeImpl) ApplyRecovery(newUIDValidity uint32) error {
	if s.readOnly {
		return ErrReadOnly
	}
	// Refuse while a discard-old reset is in flight.  A pending manifest means
	// that data files may have been moved to staging; clearing recovery_required
	// without completing the reset would leave the store in an inconsistent state
	// (stale staged data + new UIDValidity, no recovery_required to signal the
	// problem).  The caller must resolve the pending reset via ResetForRecovery
	// or AbortReset first.
	pending, err := s.HasPendingReset()
	if err != nil {
		return fmt.Errorf("ApplyRecovery: check pending reset: %w", err)
	}
	if pending {
		return ErrPendingReset
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
		return nil
	})
}

// ResetForRecovery implements Store.ResetForRecovery.
//
// Crash safety: the initial manifest (phase=manifest_written) is written BEFORE any
// destructive file operation, so OpenReadWrite fails with ErrPendingReset and
// AbortReset can roll back from any point.  Subsequent phases are checkpoints
// written AFTER each (idempotent) operation completes; re-running after any crash
// resumes from the last durable phase and converges to "empty store + current
// UIDVALIDITY + recovery-required cleared".
//
// Forward progression: manifest_written(1) → data_staged(2) → emails_staged(3) →
// committed(4) → cleanup.
//
// If the manifest is found in the aborting phase (5), this method refuses with
// ErrResetAbortInProgress: the operator must finish AbortReset first to restore
// data back to root before any further action.
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
	if manifestExists {
		if mfst.Version != resetManifestVersion {
			return &ErrResetManifestVersionMismatch{Got: mfst.Version, Want: resetManifestVersion}
		}
		if err := validateManifestPhase(mfst.Phase); err != nil {
			return err
		}
		// A partially-applied AbortReset must be completed by AbortReset, not
		// converted into a forward commit: at this point old data may already be
		// restored to root, so committing would leave stale records in the store.
		if mfst.Phase == resetPhaseAborting {
			return ErrResetAbortInProgress
		}
		// A committed manifest is a leftover from a previous reset that succeeded
		// but whose cleanupCompletedReset failed best-effort.  The sentinel is already
		// correct; if no new recovery_required exists, just clean up and return.
		// If a new recovery_required was written since then, fall through to fresh-start.
		if mfst.Phase == resetPhaseCommitted {
			return s.resumeOrCleanupCommitted(currUIDValidity, stagingPath, manifestPath)
		}
	}

	if !manifestExists {
		var err error
		mfst, err = s.initResetManifest(currUIDValidity, stagingPath, manifestPath)
		if err != nil {
			return err
		}
	}

	return s.executeResetFromManifest(mfst, stagingPath, manifestPath)
}

// resumeOrCleanupCommitted handles a phase=committed manifest found at the start of
// ResetForRecovery.  It removes the stale manifest and staging, then either:
//   - returns nil when no new recovery_required exists (cleanup was all that was needed), or
//   - initiates a fresh-start reset when a new recovery_required has been written since
//     the committed cleanup failed.
func (s *storeImpl) resumeOrCleanupCommitted(currUIDValidity uint32, stagingPath, manifestPath string) error {
	if err := removeStaleCommittedManifest(stagingPath, manifestPath); err != nil {
		return err
	}
	_, _, _, found, err := s.LoadRecoveryRequired()
	if err != nil {
		return fmt.Errorf("ResetForRecovery: reload recovery-required after committed cleanup: %w", err)
	}
	if !found {
		return nil // sentinel already correct; nothing left to do
	}
	// A new recovery_required has been written; run a full fresh-start reset.
	mfst, err := s.initResetManifest(currUIDValidity, stagingPath, manifestPath)
	if err != nil {
		return err
	}
	return s.executeResetFromManifest(mfst, stagingPath, manifestPath)
}

// executeResetFromManifest drives advanceResetPhases from the manifest's current phase
// to committed, then removes the staging directory (best-effort) and manifest (required).
func (s *storeImpl) executeResetFromManifest(mfst resetManifest, stagingPath, manifestPath string) error {
	currUIDValidity := mfst.CurrUIDValidity
	if err := s.advanceResetPhases(mfst.Phase, currUIDValidity, stagingPath, manifestPath); err != nil {
		return err
	}
	// Staging dir cleanup is best-effort: a stale staging dir is harmless to normal
	// data paths and is cleaned up on the next run.
	if err := os.RemoveAll(stagingPath); err != nil {
		slog.Warn("store: failed to remove staging directory after reset; manual cleanup may be required",
			slog.String("path", stagingPath),
			slog.Any("error", err),
		)
	}
	// Manifest removal is required: if the manifest survives, Open(OpenReadWrite)
	// will permanently return ErrPendingReset.
	if err := os.Remove(manifestPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("ResetForRecovery: remove manifest: %w", err)
	}
	return nil
}

// advanceResetPhases drives the phase progression from the given phase to committed.
// Each step performs an idempotent file operation and then writes the new phase to
// the manifest as a checkpoint.  A crash before the checkpoint leaves the manifest
// at the previous phase; on resume the operation re-runs idempotently and the
// checkpoint write is retried.
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
		return writeResetManifest(manifestPath, resetManifest{
			Version: resetManifestVersion, CurrUIDValidity: currUIDValidity,
			Phase: resetPhaseCommitted,
		})
	})
}

// AbortReset implements Store.AbortReset.
//
// Valid when the manifest is in any pre-commit phase (1–3) or already in the
// aborting phase (5) from a previously-crashed AbortReset.  Phase=4 (committed)
// returns ErrResetNotPending.
//
// Crash safety: before any file is restored, the manifest is advanced to
// resetPhaseAborting.  This single durable checkpoint switches the manifest from
// "forward-progressing reset" to "abort in progress", so a crash mid-restore
// (or between restore and manifest removal) cannot be misinterpreted as a
// pending forward reset.  ResetForRecovery refuses phase=aborting with
// ErrResetAbortInProgress, forcing the operator to re-run AbortReset, which
// idempotently completes the restore and removes the manifest.
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
	if err := validateManifestPhase(mfst.Phase); err != nil {
		return err
	}

	if mfst.Phase == resetPhaseCommitted {
		return ErrResetNotPending
	}

	if mfst.Phase != resetPhaseAborting {
		// Forward-progressing manifest (phases 1–3): check the commit barrier and
		// transition the manifest to the aborting phase.
		//
		// The commit barrier is sentinel.recovery_required: commit writes the sentinel
		// first and only then advances the manifest to phase=4, so a crash between
		// sentinel save and phase=4 write leaves the manifest at phase=3 with the
		// sentinel already committed.  Treating that state as abortable would restore
		// stale data on top of the new sentinel, producing a
		// "new UIDValidity + cleared recovery-required + old data" inconsistency.
		// Therefore: if recovery_required is gone, the commit happened — refuse.
		_, _, _, found, err := s.LoadRecoveryRequired()
		if err != nil {
			return fmt.Errorf("AbortReset: check recovery-required: %w", err)
		}
		if !found {
			return ErrResetNotPending
		}

		// Mark the manifest as aborting BEFORE touching any file.  This is the
		// write-ahead checkpoint for the abort path: once it is durable, any later
		// crash leaves the manifest at phase=aborting, which only AbortReset may
		// resume (ResetForRecovery refuses with ErrResetAbortInProgress).
		//
		// withGuardExclusive is NOT acquired here: AbortReset never writes to the
		// sentinel, so there is no risk of a summary process observing a partial
		// sentinel update.  The sentinel's recovery_required field is left intact
		// throughout, ensuring that CheckRecoveryRequired keeps returning true until
		// a subsequent ResetForRecovery or ApplyRecovery commits the sentinel.
		if err := writeResetManifest(manifestPath, resetManifest{
			Version:         resetManifestVersion,
			CurrUIDValidity: mfst.CurrUIDValidity,
			Phase:           resetPhaseAborting,
		}); err != nil {
			return fmt.Errorf("AbortReset: mark aborting: %w", err)
		}
	}

	// Aborting phase: restore staged files back to rootDir (idempotent).
	// restoreFromStaging handles ErrNotExist gracefully, so it is safe to re-run
	// on a previously-crashed abort where staging may already be empty/removed.
	if err := restoreFromStaging(s.rootDir, stagingPath); err != nil {
		return fmt.Errorf("AbortReset: restore from staging: %w", err)
	}
	// Recovery-required remains in the sentinel so the caller can retry.
	if err := os.RemoveAll(stagingPath); err != nil {
		slog.Warn("store: failed to remove staging directory after abort; manual cleanup may be required",
			slog.String("path", stagingPath),
			slog.Any("error", err),
		)
	}
	if err := os.Remove(manifestPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("AbortReset: remove manifest: %w", err)
	}
	return nil
}

// HasPendingReset implements Store.HasPendingReset.
func (s *storeImpl) HasPendingReset() (bool, error) {
	manifestPath := resetManifestPath(s.rootDir)
	_, err := readResetManifest(manifestPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, fmt.Errorf("HasPendingReset: %w", err)
	}
	return true, nil
}

// AcquireSummaryConsistencyGuard implements Store.AcquireSummaryConsistencyGuard.
// When rootDir does not exist (empty-store OpenReadOnly path), a no-op guard is
// returned: recovery-required cannot be set without a store directory, so
// CheckRecoveryRequired always returns false and Close is a no-op.
func (s *storeImpl) AcquireSummaryConsistencyGuard() (SummaryConsistencyGuard, error) {
	if _, err := os.Stat(s.rootDir); errors.Is(err, os.ErrNotExist) {
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
	if err := unix.Flock(int(f.Fd()), unix.LOCK_SH); err != nil { //nolint:gosec // fd fits int on all supported platforms
		_ = f.Close()
		return nil, fmt.Errorf("AcquireSummaryConsistencyGuard: acquire shared lock: %w", err)
	}
	return &summaryConsistencyGuardImpl{rootDir: s.rootDir, f: f}, nil
}
