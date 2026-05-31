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
// Forward: 1 (manifest_written, WAL) → 4 (committed).
// Backward: → 5 (aborting, WAL entry for AbortReset).
// Legacy values 2 and 3 (data_staged, emails_staged) were written by older versions;
// they are never written by the current code but are accepted as pre-commit.
// See docs/dev/adr/0003_reset_phase_design.md for rationale.
type resetPhase int

const (
	// resetPhaseManifestWritten: manifest written, staging dir created; no files moved yet.
	resetPhaseManifestWritten resetPhase = 1
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
func (s *fileStore) initResetManifest(currUIDValidity uint32, stagingPath, manifestPath string) (resetManifest, error) {
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

	// Wipe stale staging before writing the manifest: a prior committed-but-not-cleaned
	// reset may leave files there, causing stageEmailsDir to fail on rename.
	// No manifest means any staging contents are guaranteed stale (see ADR-0003 §6).
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
// when the underlying commit has already been applied to the sentinel.
// Called from Open(OpenReadWrite); uses sentinel.recovery_required as the commit
// signal rather than the phase value, so it handles commit-window crashes correctly.
// See docs/dev/adr/0003_reset_phase_design.md §5 for the decision logic.
//
// Returns ErrPendingReset if a reset or abort is still in progress.
// Returns *ErrResetManifestVersionMismatch / *ErrResetManifestPhaseUnknown on
// unreadable manifests (fail closed for manual handling).
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
			} else if !errors.Is(statErr, os.ErrNotExist) {
				slog.Warn("store: failed to stat staging directory; manual inspection may be required",
					slog.String("path", stagingPath),
					slog.Any("error", statErr),
				)
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
		// If the manifest's CurrUIDValidity differs from the sentinel's current
		// recovery_required, the manifest is stale residue from a previous reset
		// whose cleanup partially failed. Clean it up so that the operator can
		// proceed with keep-old or other non-destructive actions without being
		// blocked by a manifest that has nothing to do with the new UIDVALIDITY event.
		if mfst.CurrUIDValidity != sentinel.RecoveryRequired.CurrUIDValidity {
			slog.Warn("store: removing stale reset manifest (CurrUIDValidity mismatch with current recovery_required)",
				slog.String("root_dir", rootDir),
				slog.Int("manifest_phase", int(mfst.Phase)),
				slog.Uint64("manifest_uid", uint64(mfst.CurrUIDValidity)),
				slog.Uint64("recovery_uid", uint64(sentinel.RecoveryRequired.CurrUIDValidity)),
			)
			if err := os.RemoveAll(resetStagingPath(rootDir)); err != nil {
				slog.Warn("store: failed to remove stale staging directory; manual cleanup may be required",
					slog.String("path", resetStagingPath(rootDir)),
					slog.Any("error", err),
				)
			}
			if err := os.Remove(manifestPath); err != nil && !errors.Is(err, os.ErrNotExist) {
				slog.Warn("store: failed to remove stale manifest; manual cleanup may be required",
					slog.String("path", manifestPath),
					slog.Any("error", err),
				)
			}
			return nil
		}
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
		slog.Warn("store: failed to remove reset manifest; manual cleanup may be required",
			slog.String("path", manifestPath),
			slog.Any("error", err),
		)
	}
	return nil
}

// ---- guard file locking ----

// withGuardExclusive opens the guard file, acquires a blocking exclusive flock,
// calls fn, and releases the lock when fn returns (or on deferred close).
// Writers that modify recovery-required in the sentinel must call this to prevent
// a race with summary processes holding a shared flock on the same file.
func (s *fileStore) withGuardExclusive(fn func() error) error {
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
func (s *fileStore) loadOrInitSentinelForWrite() (*internalSentinelFile, error) {
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
func (s *fileStore) SaveUIDValidity(v uint32) error {
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
func (s *fileStore) LoadUIDValidity() (v uint32, found bool, err error) {
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
func (s *fileStore) SaveRecoveryRequired(prev, curr uint32, detectedAt time.Time) error {
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
func (s *fileStore) LoadRecoveryRequired() (prev, curr uint32, detectedAt time.Time, found bool, err error) {
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
func (s *fileStore) ClearRecoveryRequired() error {
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
func (s *fileStore) ApplyRecovery(newUIDValidity uint32) error {
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
// Drives phases 1→4→cleanup, resuming from the current manifest phase on re-run.
// Legacy pre-commit values 2 and 3 are treated as phase 1 (all staging ops re-run idempotently).
// Phase=5 (aborting) is refused with ErrResetAbortInProgress.
// See docs/dev/adr/0003_reset_phase_design.md §3–4 for crash-safety rationale.
func (s *fileStore) ResetForRecovery(currUIDValidity uint32) error {
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
		// A pre-commit manifest (phase < resetPhaseCommitted, i.e. phase 1 or legacy 2–3) whose
		// CurrUIDValidity doesn't match the caller's currUIDValidity is a stale residue from a
		// previous reset attempt (e.g. cleanupCompletedReset deleted staging but failed to remove
		// the manifest).  Remove it and start fresh so the new UIDVALIDITY is committed correctly.
		// currUIDValidity==0 is the special cleanup path used by handleNoRecoveryRequired
		// (no recovery_required in sentinel); skip the stale check in that case.
		if mfst.Phase < resetPhaseCommitted && currUIDValidity != 0 && mfst.CurrUIDValidity != currUIDValidity {
			if err := removeStaleCommittedManifest(stagingPath, manifestPath); err != nil {
				return err
			}
			manifestExists = false
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
func (s *fileStore) resumeOrCleanupCommitted(currUIDValidity uint32, stagingPath, manifestPath string) error {
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
// to committed, then removes the staging directory (best-effort) and manifest (best-effort via error return).
func (s *fileStore) executeResetFromManifest(mfst resetManifest, stagingPath, manifestPath string) error {
	currUIDValidity := mfst.CurrUIDValidity
	if err := s.advanceResetPhases(currUIDValidity, stagingPath, manifestPath); err != nil {
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
	// Manifest removal failure is returned to the caller. A phase=committed manifest
	// left behind is handled best-effort by cleanupCompletedReset on the next open,
	// but removing it here keeps the store clean for the happy path.
	if err := os.Remove(manifestPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("ResetForRecovery: remove manifest: %w", err)
	}
	return nil
}

// advanceResetPhases drives any pre-commit manifest to committed in a single pass.
// All staging operations are idempotent (absent files are no-ops), so re-running
// from any intermediate file-layout state converges to the same result.
// No intermediate checkpoint manifests are written; commitReset writes phase=4 once.
func (s *fileStore) advanceResetPhases(currUIDValidity uint32, stagingPath, manifestPath string) error {
	// MkdirAll is defensive: staging dir should already exist from the initial write,
	// but guard against edge cases (e.g. partial RemoveAll from a previous run).
	if err := os.MkdirAll(stagingPath, dirPerm); err != nil {
		return fmt.Errorf("advanceResetPhases: ensure staging dir: %w", err)
	}
	if err := stageDataFile(s.rootDir, stagingPath); err != nil {
		return fmt.Errorf("advanceResetPhases: stage data file: %w", err)
	}
	if err := stageEmailsDir(s.rootDir, stagingPath); err != nil {
		return fmt.Errorf("advanceResetPhases: stage emails dir: %w", err)
	}
	if err := s.commitReset(manifestPath, currUIDValidity); err != nil {
		return fmt.Errorf("advanceResetPhases: commit: %w", err)
	}
	return nil
}

// commitReset atomically clears recovery_required, writes the new UIDValidity, and
// advances the manifest to resetPhaseCommitted under the exclusive guard lock.
// The guard serializes against concurrent summary processes reading recovery_required;
// see docs/dev/developer_guide/process_locking.md §3.
func (s *fileStore) commitReset(manifestPath string, currUIDValidity uint32) error {
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
// Valid for pre-commit phases (phase < resetPhaseCommitted, i.e. phase 1 or legacy 2–3) and
// the aborting phase (5); phase=4 returns ErrResetNotPending.
// Advances the manifest to phase=aborting before restoring any file (WAL entry).
// See docs/dev/adr/0003_reset_phase_design.md §4 for rationale.
func (s *fileStore) AbortReset() error {
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
		// recovery_required is the commit barrier: commitReset saves the sentinel
		// before advancing to phase=4, so a missing recovery_required means the
		// commit already happened even if the manifest is still pre-commit (phase 1, or legacy 2–3).
		_, sentinelCurr, _, found, err := s.LoadRecoveryRequired()
		if err != nil {
			return fmt.Errorf("AbortReset: check recovery-required: %w", err)
		}
		if !found {
			return ErrResetNotPending
		}
		// Stale manifest: CurrUIDValidity doesn't match the current recovery_required.
		// Restoring from this manifest's staging would put data from the wrong epoch
		// back into the store root. Clean up the stale manifest and report no active reset.
		if mfst.CurrUIDValidity != sentinelCurr {
			slog.Warn("store: removing stale reset manifest in AbortReset (CurrUIDValidity mismatch)",
				slog.Uint64("manifest_uid", uint64(mfst.CurrUIDValidity)),
				slog.Uint64("recovery_uid", uint64(sentinelCurr)),
			)
			if err := os.RemoveAll(stagingPath); err != nil {
				slog.Warn("store: failed to remove stale staging directory",
					slog.String("path", stagingPath),
					slog.Any("error", err),
				)
			}
			if err := os.Remove(manifestPath); err != nil && !errors.Is(err, os.ErrNotExist) {
				slog.Warn("store: failed to remove stale manifest",
					slog.String("path", manifestPath),
					slog.Any("error", err),
				)
			}
			return ErrResetNotPending
		}

		// Write-ahead: mark aborting before moving any file, so a crash mid-restore
		// cannot be misread as a forward-progressing reset by ResetForRecovery.
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
// Returns true for any non-committed manifest: pre-commit (phase 1, or legacy 2–3) and aborting (phase 5).
// Phase=committed is leftover cleanup bookkeeping (sentinel already committed),
// not an active reset, so it returns false.
// Returns an error for unknown versions or out-of-range phases (fail closed).
func (s *fileStore) HasPendingReset() (bool, error) {
	manifestPath := resetManifestPath(s.rootDir)
	mfst, err := readResetManifest(manifestPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, fmt.Errorf("HasPendingReset: %w", err)
	}
	if mfst.Version != resetManifestVersion {
		return false, &ErrResetManifestVersionMismatch{Got: mfst.Version, Want: resetManifestVersion}
	}
	if err := validateManifestPhase(mfst.Phase); err != nil {
		return false, err
	}
	return mfst.Phase != resetPhaseCommitted, nil
}

// AcquireSummaryConsistencyGuard implements Store.AcquireSummaryConsistencyGuard.
// When rootDir does not exist (empty-store OpenReadOnly path), a no-op guard is
// returned: recovery-required cannot be set without a store directory, so
// CheckRecoveryRequired always returns false and Close is a no-op.
// See docs/dev/developer_guide/process_locking.md §3 for the concurrency design.
func (s *fileStore) AcquireSummaryConsistencyGuard() (SummaryConsistencyGuard, error) {
	if _, err := os.Stat(s.rootDir); errors.Is(err, os.ErrNotExist) {
		return noopSummaryConsistencyGuard{}, nil
	}
	guardPath := guardFilePath(s.rootDir)
	f, err := os.OpenFile(guardPath, os.O_RDONLY, filePerm) //nolint:gosec
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("AcquireSummaryConsistencyGuard: open guard file: %w", err)
		}
		// Guard file absent for an existing store (store pre-dates the guard file, or
		// file was manually removed). Attempt to create it so we can hold a real LOCK_SH.
		// Fail closed on error: proceeding without a guard could miss a concurrent
		// recovery_required write.
		f, err = os.OpenFile(guardPath, os.O_CREATE|os.O_RDWR, filePerm) //nolint:gosec
		if err != nil {
			return nil, fmt.Errorf("AcquireSummaryConsistencyGuard: create guard file: %w", err)
		}
	}
	if err := unix.Flock(int(f.Fd()), unix.LOCK_SH); err != nil { //nolint:gosec // fd fits int on all supported platforms
		_ = f.Close()
		return nil, fmt.Errorf("AcquireSummaryConsistencyGuard: acquire shared lock: %w", err)
	}
	return &summaryConsistencyGuardImpl{rootDir: s.rootDir, f: f}, nil
}
