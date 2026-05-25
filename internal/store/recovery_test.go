//go:build test

package store

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSaveLoadUIDValidity verifies that SaveUIDValidity and LoadUIDValidity round-trip correctly.
func TestSaveLoadUIDValidity(t *testing.T) {
	s, _ := openTestStore(t)

	require.NoError(t, s.SaveUIDValidity(12345678))

	v, found, err := s.LoadUIDValidity()
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, uint32(12345678), v)
}

// TestLoadUIDValidity_NotFound verifies that LoadUIDValidity returns found=false on a fresh store.
func TestLoadUIDValidity_NotFound(t *testing.T) {
	s, _ := openTestStore(t)

	v, found, err := s.LoadUIDValidity()
	require.NoError(t, err)
	assert.False(t, found)
	assert.Equal(t, uint32(0), v)
}

// TestSaveUIDValidity_Overwrite verifies that re-calling SaveUIDValidity replaces the stored value.
func TestSaveUIDValidity_Overwrite(t *testing.T) {
	s, _ := openTestStore(t)

	require.NoError(t, s.SaveUIDValidity(111))
	require.NoError(t, s.SaveUIDValidity(222))

	v, found, err := s.LoadUIDValidity()
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, uint32(222), v)
}

// TestSaveUIDValidity_ReadOnly verifies that SaveUIDValidity returns ErrReadOnly in read-only mode.
func TestSaveUIDValidity_ReadOnly(t *testing.T) {
	rootDir := t.TempDir()
	s, err := Open(rootDir, makeTestIdentity(), OpenReadOnly)
	require.NoError(t, err)

	assert.ErrorIs(t, s.SaveUIDValidity(1), ErrReadOnly)
}

// TestSaveLoadRecoveryRequired verifies the round-trip of SaveRecoveryRequired/LoadRecoveryRequired.
func TestSaveLoadRecoveryRequired(t *testing.T) {
	s, _ := openTestStore(t)

	detectedAt := time.Date(2026, 5, 18, 10, 0, 0, 0, time.UTC)
	require.NoError(t, s.SaveRecoveryRequired(111, 222, detectedAt))

	prev, curr, got, found, err := s.LoadRecoveryRequired()
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, uint32(111), prev)
	assert.Equal(t, uint32(222), curr)
	assert.True(t, got.Equal(detectedAt))
}

// TestLoadRecoveryRequired_NotFound verifies that LoadRecoveryRequired returns found=false
// on a fresh store.
func TestLoadRecoveryRequired_NotFound(t *testing.T) {
	s, _ := openTestStore(t)

	_, _, _, found, err := s.LoadRecoveryRequired()
	require.NoError(t, err)
	assert.False(t, found)
}

// TestClearRecoveryRequired verifies that ClearRecoveryRequired removes the recovery state.
func TestClearRecoveryRequired(t *testing.T) {
	s, _ := openTestStore(t)

	detectedAt := time.Date(2026, 5, 18, 10, 0, 0, 0, time.UTC)
	require.NoError(t, s.SaveRecoveryRequired(111, 222, detectedAt))

	require.NoError(t, s.ClearRecoveryRequired())

	_, _, _, found, err := s.LoadRecoveryRequired()
	require.NoError(t, err)
	assert.False(t, found)
}

// TestClearRecoveryRequired_ReadOnly verifies that ClearRecoveryRequired returns ErrReadOnly.
func TestClearRecoveryRequired_ReadOnly(t *testing.T) {
	rootDir := t.TempDir()
	s, err := Open(rootDir, makeTestIdentity(), OpenReadOnly)
	require.NoError(t, err)

	assert.ErrorIs(t, s.ClearRecoveryRequired(), ErrReadOnly)
}

// TestApplyRecovery verifies that ApplyRecovery atomically updates uid_validity and clears
// recovery_required in a single sentinel write.
func TestApplyRecovery(t *testing.T) {
	s, _ := openTestStore(t)

	// Set up: record an initial UIDVALIDITY and a recovery-required state.
	require.NoError(t, s.SaveUIDValidity(100))
	detectedAt := time.Date(2026, 5, 18, 10, 0, 0, 0, time.UTC)
	require.NoError(t, s.SaveRecoveryRequired(100, 200, detectedAt))

	// Apply recovery with new UIDVALIDITY.
	require.NoError(t, s.ApplyRecovery(200))

	// uid_validity should be updated.
	v, found, err := s.LoadUIDValidity()
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, uint32(200), v)

	// recovery_required should be cleared.
	_, _, _, recoveryFound, err := s.LoadRecoveryRequired()
	require.NoError(t, err)
	assert.False(t, recoveryFound, "recovery_required should be cleared after ApplyRecovery")
}

// TestApplyRecovery_ReadOnly verifies that ApplyRecovery returns ErrReadOnly in read-only mode.
func TestApplyRecovery_ReadOnly(t *testing.T) {
	rootDir := t.TempDir()
	s, err := Open(rootDir, makeTestIdentity(), OpenReadOnly)
	require.NoError(t, err)

	assert.ErrorIs(t, s.ApplyRecovery(100), ErrReadOnly)
}

// TestSaveRecoveryRequired_ReadOnly verifies that SaveRecoveryRequired returns ErrReadOnly.
func TestSaveRecoveryRequired_ReadOnly(t *testing.T) {
	rootDir := t.TempDir()
	s, err := Open(rootDir, makeTestIdentity(), OpenReadOnly)
	require.NoError(t, err)

	assert.ErrorIs(t, s.SaveRecoveryRequired(1, 2, time.Now()), ErrReadOnly)
}

// openRecoverResetStore opens a store with OpenRecoverReset mode.
func openRecoverResetStore(t *testing.T) (Store, string) {
	t.Helper()
	rootDir := t.TempDir()
	s, err := Open(rootDir, makeTestIdentity(), OpenRecoverReset)
	require.NoError(t, err)
	return s, rootDir
}

// TestResetForRecovery_NoRecoveryRequired returns ErrRecoveryRequiredMissing
// when the sentinel has no recovery-required entry.
func TestResetForRecovery_NoRecoveryRequired(t *testing.T) {
	s, _ := openRecoverResetStore(t)
	err := s.ResetForRecovery(42)
	assert.ErrorIs(t, err, ErrRecoveryRequiredMissing)
}

// TestResetForRecovery_CurrMismatch returns ErrRecoveryUIDValidityMismatch
// when the supplied currUIDValidity differs from the sentinel's curr_uid_validity.
func TestResetForRecovery_CurrMismatch(t *testing.T) {
	s, _ := openRecoverResetStore(t)
	require.NoError(t, s.SaveRecoveryRequired(100, 200, time.Now()))

	err := s.ResetForRecovery(999)
	var mismatchErr *ErrRecoveryUIDValidityMismatch
	require.ErrorAs(t, err, &mismatchErr)
	assert.Equal(t, uint32(999), mismatchErr.Got)
	assert.Equal(t, uint32(200), mismatchErr.Expected)
}

// TestResetForRecovery_ClearsDataAndSentinel verifies the happy path:
// after ResetForRecovery the store has no reports/emails, uid_validity is updated,
// and recovery-required is cleared.
func TestResetForRecovery_ClearsDataAndSentinel(t *testing.T) {
	s, rootDir := openRecoverResetStore(t)

	// Plant some data.
	require.NoError(t, s.SaveUIDValidity(100))
	require.NoError(t, s.SaveRecoveryRequired(100, 200, time.Now()))

	require.NoError(t, s.ResetForRecovery(200))

	// uid_validity updated.
	v, found, err := s.LoadUIDValidity()
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, uint32(200), v)

	// recovery_required cleared.
	_, _, _, recFound, err := s.LoadRecoveryRequired()
	require.NoError(t, err)
	assert.False(t, recFound)

	// Staging dir and manifest removed.
	_, err = os.Stat(resetManifestPath(rootDir))
	assert.True(t, os.IsNotExist(err), "manifest should be removed after successful reset")
	_, err = os.Stat(resetStagingPath(rootDir))
	assert.True(t, os.IsNotExist(err), "staging dir should be removed after successful reset")
}

// TestResetForRecovery_IdempotentAfterCrashBeforeCommit simulates a crash after staging
// but before commit by planting a manifest file, then verifies that re-running
// ResetForRecovery converges to the committed state.
func TestResetForRecovery_IdempotentAfterCrashBeforeCommit(t *testing.T) {
	rootDir := t.TempDir()
	s1, err := Open(rootDir, makeTestIdentity(), OpenRecoverReset)
	require.NoError(t, err)
	require.NoError(t, s1.SaveUIDValidity(100))
	require.NoError(t, s1.SaveRecoveryRequired(100, 200, time.Now()))

	// Simulate crash after staging: plant manifest at phase=emails_staged and staging dir but skip commit.
	stagingPath := resetStagingPath(rootDir)
	require.NoError(t, os.MkdirAll(stagingPath, dirPerm))
	require.NoError(t, writeResetManifest(resetManifestPath(rootDir), resetManifest{
		Version: resetManifestVersion, CurrUIDValidity: 200, Phase: resetPhaseEmailsStaged,
	}))

	// Re-open with OpenRecoverReset (manifest present, so OpenReadWrite would fail).
	s2, err := Open(rootDir, makeTestIdentity(), OpenRecoverReset)
	require.NoError(t, err)

	// ResetForRecovery should resume and complete from the staged state.
	require.NoError(t, s2.ResetForRecovery(200))

	v, found, err := s2.LoadUIDValidity()
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, uint32(200), v)

	_, _, _, recFound, err := s2.LoadRecoveryRequired()
	require.NoError(t, err)
	assert.False(t, recFound)
}

// TestResetForRecovery_CrashAtPhaseManifestWritten simulates a crash that occurs
// after the manifest is written (phase=1) but before the data file is moved to staging.
// Files are still in rootDir; re-running must stage them and converge to the clean state.
func TestResetForRecovery_CrashAtPhaseManifestWritten(t *testing.T) {
	rootDir := t.TempDir()

	s, err := Open(rootDir, makeTestIdentity(), OpenReadWrite)
	require.NoError(t, err)
	require.NoError(t, s.SaveUIDValidity(100))
	require.NoError(t, SaveReport(s, ReportInput{
		Report:      makeFullReport("report-1", time.Now()),
		UID:         1,
		UIDValidity: 100,
	}))
	require.NoError(t, s.SaveRecoveryRequired(100, 200, time.Now()))

	// Simulate crash: manifest written at phase=1, staging dir exists, no files moved.
	stagingPath := resetStagingPath(rootDir)
	require.NoError(t, os.MkdirAll(stagingPath, dirPerm))
	require.NoError(t, writeResetManifest(resetManifestPath(rootDir), resetManifest{
		Version:         resetManifestVersion,
		CurrUIDValidity: 200,
		Phase:           resetPhaseManifestWritten,
	}))

	// OpenReadWrite must fail with ErrPendingReset even at phase=1.
	_, err = Open(rootDir, makeTestIdentity(), OpenReadWrite)
	assert.ErrorIs(t, err, ErrPendingReset, "OpenReadWrite must fail closed when manifest exists at phase=1")

	s2, err := Open(rootDir, makeTestIdentity(), OpenRecoverReset)
	require.NoError(t, err)
	require.NoError(t, s2.ResetForRecovery(200))

	v, found, err := s2.LoadUIDValidity()
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, uint32(200), v)

	_, _, _, recFound, err := s2.LoadRecoveryRequired()
	require.NoError(t, err)
	assert.False(t, recFound)

	s3, err := Open(rootDir, makeTestIdentity(), OpenReadWrite)
	require.NoError(t, err)
	reports, err := s3.GetAllReports()
	require.NoError(t, err)
	assert.Empty(t, reports, "store must be empty after ResetForRecovery")
}

// TestResetForRecovery_CrashAfterStageEmailsBeforeManifestUpdate simulates a crash
// in the window between stageEmailsDir (rename completed) and writeResetManifest(phase=3).
// The manifest is still at phase=2 but emails/ is already in staging.
// Re-running must skip the rename (emails/ absent in root) and converge cleanly.
func TestResetForRecovery_CrashAfterStageEmailsBeforeManifestUpdate(t *testing.T) {
	rootDir := t.TempDir()

	s, err := Open(rootDir, makeTestIdentity(), OpenReadWrite)
	require.NoError(t, err)
	require.NoError(t, s.SaveUIDValidity(100))
	require.NoError(t, SaveReport(s, ReportInput{
		Report:      makeFullReport("report-1", time.Now()),
		UID:         1,
		UIDValidity: 100,
	}))
	require.NoError(t, s.SaveRecoveryRequired(100, 200, time.Now()))

	// Simulate crash: both tlsrpt.json and emails/ already in staging, manifest still
	// at phase=2 (emails rename completed but manifest not yet advanced to phase=3).
	stagingPath := resetStagingPath(rootDir)
	require.NoError(t, os.MkdirAll(stagingPath, dirPerm))
	require.NoError(t, os.Rename(dataFilePath(rootDir), filepath.Join(stagingPath, "tlsrpt.json")))
	require.NoError(t, os.Rename(emailsPath(rootDir), filepath.Join(stagingPath, "emails")))
	require.NoError(t, writeResetManifest(resetManifestPath(rootDir), resetManifest{
		Version:         resetManifestVersion,
		CurrUIDValidity: 200,
		Phase:           resetPhaseDataStaged, // not yet advanced to 3
	}))

	s2, err := Open(rootDir, makeTestIdentity(), OpenRecoverReset)
	require.NoError(t, err)
	require.NoError(t, s2.ResetForRecovery(200))

	v, found, err := s2.LoadUIDValidity()
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, uint32(200), v)

	_, _, _, recFound, err := s2.LoadRecoveryRequired()
	require.NoError(t, err)
	assert.False(t, recFound)

	s3, err := Open(rootDir, makeTestIdentity(), OpenReadWrite)
	require.NoError(t, err)
	reports, err := s3.GetAllReports()
	require.NoError(t, err)
	assert.Empty(t, reports)
}

// TestResetForRecovery_UnknownPhaseFailsClosed verifies that a manifest with an
// out-of-range phase is rejected and neither the manifest nor the staging dir are
// touched, so the inconsistency can be resolved manually.
func TestResetForRecovery_UnknownPhaseFailsClosed(t *testing.T) {
	rootDir := t.TempDir()
	sRW, err := Open(rootDir, makeTestIdentity(), OpenReadWrite)
	require.NoError(t, err)
	require.NoError(t, sRW.SaveUIDValidity(100))
	require.NoError(t, sRW.SaveRecoveryRequired(100, 200, time.Now()))

	manifestPath := resetManifestPath(rootDir)
	require.NoError(t, os.WriteFile(manifestPath, []byte(`{"version":1,"curr_uid_validity":200,"phase":99}`), filePerm))
	stagingPath := resetStagingPath(rootDir)
	require.NoError(t, os.MkdirAll(stagingPath, dirPerm))

	s, err := Open(rootDir, makeTestIdentity(), OpenRecoverReset)
	require.NoError(t, err)

	err = s.ResetForRecovery(200)
	var phaseErr *ErrResetManifestPhaseUnknown
	require.ErrorAs(t, err, &phaseErr)
	assert.Equal(t, 99, phaseErr.Got)

	// Manifest and staging dir must remain untouched.
	_, err = os.Stat(manifestPath)
	assert.NoError(t, err, "manifest must be preserved on unknown phase")
	_, err = os.Stat(stagingPath)
	assert.NoError(t, err, "staging dir must be preserved on unknown phase")
}

// TestAbortReset_UnknownPhaseFailsClosed mirrors the ResetForRecovery check on the
// abort path.  An unknown phase must not be silently treated as committed or
// pre-commit; instead, fail closed for manual resolution.
func TestAbortReset_UnknownPhaseFailsClosed(t *testing.T) {
	rootDir := t.TempDir()
	sRW, err := Open(rootDir, makeTestIdentity(), OpenReadWrite)
	require.NoError(t, err)
	require.NoError(t, sRW.SaveUIDValidity(100))
	require.NoError(t, sRW.SaveRecoveryRequired(100, 200, time.Now()))

	manifestPath := resetManifestPath(rootDir)
	require.NoError(t, os.WriteFile(manifestPath, []byte(`{"version":1,"curr_uid_validity":200,"phase":99}`), filePerm))

	s, err := Open(rootDir, makeTestIdentity(), OpenRecoverReset)
	require.NoError(t, err)

	err = s.AbortReset()
	var phaseErr *ErrResetManifestPhaseUnknown
	require.ErrorAs(t, err, &phaseErr)

	// Manifest must remain.
	_, err = os.Stat(manifestPath)
	assert.NoError(t, err)
}

// TestAbortReset_CrashDuringCommitRefusesAbort simulates the narrow window where
// commit has saved the sentinel (recovery_required cleared, new UIDValidity set)
// but the manifest has not yet been advanced to resetPhaseCommitted.  Allowing
// abort here would restore stale data on top of the committed sentinel, producing
// an inconsistent store; AbortReset must refuse via ErrResetNotPending so the
// caller resumes with ResetForRecovery instead.
func TestAbortReset_CrashDuringCommitRefusesAbort(t *testing.T) {
	rootDir := t.TempDir()

	sRW, err := Open(rootDir, makeTestIdentity(), OpenReadWrite)
	require.NoError(t, err)
	require.NoError(t, sRW.SaveUIDValidity(100))
	require.NoError(t, sRW.SaveRecoveryRequired(100, 200, time.Now()))

	// Stage data, then simulate "sentinel committed, manifest not yet phase=4".
	stagingPath := resetStagingPath(rootDir)
	require.NoError(t, os.MkdirAll(stagingPath, dirPerm))
	require.NoError(t, os.Rename(dataFilePath(rootDir), filepath.Join(stagingPath, "tlsrpt.json")))
	require.NoError(t, os.Rename(emailsPath(rootDir), filepath.Join(stagingPath, "emails")))
	require.NoError(t, writeResetManifest(resetManifestPath(rootDir), resetManifest{
		Version:         resetManifestVersion,
		CurrUIDValidity: 200,
		Phase:           resetPhaseEmailsStaged, // commit saved sentinel but manifest still phase=3
	}))

	// Commit the sentinel by hand (bypassing commitReset to keep manifest at phase=3).
	sentinel, _, err := loadSentinel(rootDir)
	require.NoError(t, err)
	newUID := uint32(200)
	sentinel.UIDValidity = &newUID
	sentinel.RecoveryRequired = nil
	require.NoError(t, saveSentinel(rootDir, sentinel))

	s, err := Open(rootDir, makeTestIdentity(), OpenRecoverReset)
	require.NoError(t, err)

	// AbortReset must refuse: the commit barrier (recovery_required) has been crossed.
	assert.ErrorIs(t, s.AbortReset(), ErrResetNotPending)

	// Manifest must remain so a follow-up ResetForRecovery can finalise.
	_, err = os.Stat(resetManifestPath(rootDir))
	assert.NoError(t, err, "manifest must remain after refused abort")

	// ResetForRecovery resumes correctly: idempotent commit + cleanup.
	require.NoError(t, s.ResetForRecovery(200))
	_, err = os.Stat(resetManifestPath(rootDir))
	assert.True(t, os.IsNotExist(err))
}

// TestResetForRecovery_CrashAfterStageDataBeforeManifestUpdate simulates a crash in the
// window between stageDataFile (rename completed) and writeResetManifest(phase=2).
// The manifest is still at phase=1 but tlsrpt.json is already in staging.
// Re-running must detect the missing root file, skip the rename, and converge cleanly.
func TestResetForRecovery_CrashAfterStageDataBeforeManifestUpdate(t *testing.T) {
	rootDir := t.TempDir()

	s, err := Open(rootDir, makeTestIdentity(), OpenReadWrite)
	require.NoError(t, err)
	require.NoError(t, s.SaveUIDValidity(100))
	require.NoError(t, SaveReport(s, ReportInput{
		Report:      makeFullReport("report-1", time.Now()),
		UID:         1,
		UIDValidity: 100,
	}))
	require.NoError(t, s.SaveRecoveryRequired(100, 200, time.Now()))

	// Simulate crash: manifest at phase=1, tlsrpt.json already in staging (rename completed
	// but manifest not yet advanced to phase=2).
	stagingPath := resetStagingPath(rootDir)
	require.NoError(t, os.MkdirAll(stagingPath, dirPerm))
	require.NoError(t, os.Rename(dataFilePath(rootDir), filepath.Join(stagingPath, "tlsrpt.json")))
	require.NoError(t, writeResetManifest(resetManifestPath(rootDir), resetManifest{
		Version:         resetManifestVersion,
		CurrUIDValidity: 200,
		Phase:           resetPhaseManifestWritten, // not yet advanced to 2
	}))

	// OpenReadWrite must fail closed (manifest present).
	_, err = Open(rootDir, makeTestIdentity(), OpenReadWrite)
	assert.ErrorIs(t, err, ErrPendingReset)

	s2, err := Open(rootDir, makeTestIdentity(), OpenRecoverReset)
	require.NoError(t, err)
	require.NoError(t, s2.ResetForRecovery(200))

	v, found, err := s2.LoadUIDValidity()
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, uint32(200), v)

	_, _, _, recFound, err := s2.LoadRecoveryRequired()
	require.NoError(t, err)
	assert.False(t, recFound)

	s3, err := Open(rootDir, makeTestIdentity(), OpenReadWrite)
	require.NoError(t, err)
	reports, err := s3.GetAllReports()
	require.NoError(t, err)
	assert.Empty(t, reports, "old data must be discarded (was in staging, removed by cleanup)")
}

// TestAbortReset_PhaseManifestWritten verifies AbortReset succeeds when the manifest
// is at phase=1 (files not yet moved to staging).  The files must remain in rootDir.
func TestAbortReset_PhaseManifestWritten(t *testing.T) {
	rootDir := t.TempDir()

	sRW, err := Open(rootDir, makeTestIdentity(), OpenReadWrite)
	require.NoError(t, err)
	require.NoError(t, sRW.SaveUIDValidity(100))
	require.NoError(t, sRW.SaveRecoveryRequired(100, 200, time.Now()))

	stagingPath := resetStagingPath(rootDir)
	require.NoError(t, os.MkdirAll(stagingPath, dirPerm))
	require.NoError(t, writeResetManifest(resetManifestPath(rootDir), resetManifest{
		Version:         resetManifestVersion,
		CurrUIDValidity: 200,
		Phase:           resetPhaseManifestWritten,
	}))

	s, err := Open(rootDir, makeTestIdentity(), OpenRecoverReset)
	require.NoError(t, err)
	require.NoError(t, s.AbortReset())

	// tlsrpt.json must still be in rootDir (it was never moved).
	_, err = os.Stat(dataFilePath(rootDir))
	assert.NoError(t, err, "data file must remain in rootDir after abort at phase=1")

	// Manifest must be gone.
	_, err = os.Stat(resetManifestPath(rootDir))
	assert.True(t, os.IsNotExist(err))

	// Recovery-required must still be present.
	_, _, _, recFound, err := s.LoadRecoveryRequired()
	require.NoError(t, err)
	assert.True(t, recFound)
}

// TestAbortReset_NoPendingReset returns ErrResetNotPending when no manifest exists.
func TestAbortReset_NoPendingReset(t *testing.T) {
	s, _ := openRecoverResetStore(t)
	assert.ErrorIs(t, s.AbortReset(), ErrResetNotPending)
}

// TestAbortReset_AfterCommit returns ErrResetNotPending when the reset is already committed.
// The fixture reflects the realistic post-commit state: recovery_required is cleared in the
// sentinel (the true commit barrier) AND the manifest is at phase=committed.
func TestAbortReset_AfterCommit(t *testing.T) {
	rootDir := t.TempDir()

	// Use OpenReadWrite first so the sentinel and data file exist.
	sRW, err := Open(rootDir, makeTestIdentity(), OpenReadWrite)
	require.NoError(t, err)
	require.NoError(t, sRW.SaveUIDValidity(100))
	require.NoError(t, sRW.SaveRecoveryRequired(100, 200, time.Now()))

	// Simulate a committed reset: clear recovery_required and update UID in the sentinel,
	// then plant a phase=committed manifest (the state after commitReset but before cleanup).
	sentinel, _, err := loadSentinel(rootDir)
	require.NoError(t, err)
	newUID := uint32(200)
	sentinel.UIDValidity = &newUID
	sentinel.RecoveryRequired = nil
	require.NoError(t, saveSentinel(rootDir, sentinel))

	require.NoError(t, writeResetManifest(resetManifestPath(rootDir), resetManifest{
		Version: resetManifestVersion, CurrUIDValidity: 200, Phase: resetPhaseCommitted,
	}))

	s, err := Open(rootDir, makeTestIdentity(), OpenRecoverReset)
	require.NoError(t, err)

	assert.ErrorIs(t, s.AbortReset(), ErrResetNotPending)
}

// TestAbortReset_RestoresOldData verifies that AbortReset restores staged data and
// removes the manifest while leaving recovery-required in the sentinel.
func TestAbortReset_RestoresOldData(t *testing.T) {
	rootDir := t.TempDir()

	// Use OpenReadWrite first so initDataFile creates tlsrpt.json, then set recovery state.
	sRW, err := Open(rootDir, makeTestIdentity(), OpenReadWrite)
	require.NoError(t, err)
	require.NoError(t, sRW.SaveUIDValidity(100))
	require.NoError(t, sRW.SaveRecoveryRequired(100, 200, time.Now()))

	s, err := Open(rootDir, makeTestIdentity(), OpenRecoverReset)
	require.NoError(t, err)

	// Simulate pre-commit pending reset: move data file to staging and write manifest at phase=data_staged.
	stagingPath := resetStagingPath(rootDir)
	require.NoError(t, os.MkdirAll(stagingPath, dirPerm))
	dataPath := dataFilePath(rootDir)
	require.NoError(t, os.Rename(dataPath, filepath.Join(stagingPath, "tlsrpt.json")))
	require.NoError(t, writeResetManifest(resetManifestPath(rootDir), resetManifest{
		Version: resetManifestVersion, CurrUIDValidity: 200, Phase: resetPhaseDataStaged,
	}))

	require.NoError(t, s.AbortReset())

	// Data file restored.
	_, err = os.Stat(dataPath)
	assert.NoError(t, err, "data file should be restored after AbortReset")

	// Manifest removed.
	_, err = os.Stat(resetManifestPath(rootDir))
	assert.True(t, os.IsNotExist(err), "manifest should be removed after AbortReset")

	// Recovery-required still present.
	_, _, _, recFound, err := s.LoadRecoveryRequired()
	require.NoError(t, err)
	assert.True(t, recFound, "recovery-required should remain after AbortReset")
}

// TestAbortReset_Idempotent verifies that a second AbortReset call (after the first
// already removed the manifest) returns ErrResetNotPending without corrupting state.
func TestAbortReset_Idempotent(t *testing.T) {
	rootDir := t.TempDir()
	s, err := Open(rootDir, makeTestIdentity(), OpenRecoverReset)
	require.NoError(t, err)
	require.NoError(t, s.SaveUIDValidity(100))
	require.NoError(t, s.SaveRecoveryRequired(100, 200, time.Now()))

	// Set up pre-commit pending reset at phase=manifest_written (nothing staged yet).
	stagingPath := resetStagingPath(rootDir)
	require.NoError(t, os.MkdirAll(stagingPath, dirPerm))
	require.NoError(t, writeResetManifest(resetManifestPath(rootDir), resetManifest{
		Version: resetManifestVersion, CurrUIDValidity: 200, Phase: resetPhaseManifestWritten,
	}))

	require.NoError(t, s.AbortReset())
	// Second call should return ErrResetNotPending.
	assert.ErrorIs(t, s.AbortReset(), ErrResetNotPending)
}

// TestAbortReset_ResumesFromAbortingPhase simulates a crash that occurred AFTER
// AbortReset durably wrote phase=aborting but BEFORE restoreFromStaging completed.
// Re-running AbortReset must finish the restore and remove the manifest.
func TestAbortReset_ResumesFromAbortingPhase(t *testing.T) {
	rootDir := t.TempDir()

	sRW, err := Open(rootDir, makeTestIdentity(), OpenReadWrite)
	require.NoError(t, err)
	require.NoError(t, sRW.SaveUIDValidity(100))
	require.NoError(t, sRW.SaveRecoveryRequired(100, 200, time.Now()))

	// State: data is in staging (mid-restore not yet completed), manifest at phase=aborting,
	// recovery_required still set.
	stagingPath := resetStagingPath(rootDir)
	require.NoError(t, os.MkdirAll(stagingPath, dirPerm))
	dataPath := dataFilePath(rootDir)
	require.NoError(t, os.Rename(dataPath, filepath.Join(stagingPath, "tlsrpt.json")))
	require.NoError(t, writeResetManifest(resetManifestPath(rootDir), resetManifest{
		Version:         resetManifestVersion,
		CurrUIDValidity: 200,
		Phase:           resetPhaseAborting,
	}))

	s, err := Open(rootDir, makeTestIdentity(), OpenRecoverReset)
	require.NoError(t, err)
	require.NoError(t, s.AbortReset())

	// Data restored to rootDir.
	_, err = os.Stat(dataPath)
	assert.NoError(t, err, "data file must be restored on AbortReset resume from phase=aborting")

	// Manifest removed.
	_, err = os.Stat(resetManifestPath(rootDir))
	assert.True(t, os.IsNotExist(err))

	// Recovery-required still present (caller must retry).
	_, _, _, recFound, err := s.LoadRecoveryRequired()
	require.NoError(t, err)
	assert.True(t, recFound)
}

// TestAbortReset_CrashAfterRestoreBeforeManifestRemoval simulates the exact crash
// window that motivated the aborting-phase fix: AbortReset has already restored
// staged files back to rootDir and removed the staging dir, but crashed before
// removing the manifest.  With the fix, the manifest is at phase=aborting (set
// before any file move), so:
//
//   - ResetForRecovery refuses with ErrResetAbortInProgress — committing on top
//     of the restored data would leave stale records in the new store.
//   - AbortReset succeeds idempotently and removes the manifest.
func TestAbortReset_CrashAfterRestoreBeforeManifestRemoval(t *testing.T) {
	rootDir := t.TempDir()

	sRW, err := Open(rootDir, makeTestIdentity(), OpenReadWrite)
	require.NoError(t, err)
	require.NoError(t, sRW.SaveUIDValidity(100))
	require.NoError(t, SaveReport(sRW, ReportInput{
		Report:      makeFullReport("report-1", time.Now()),
		UID:         1,
		UIDValidity: 100,
	}))
	require.NoError(t, sRW.SaveRecoveryRequired(100, 200, time.Now()))

	// Crash state: files are already restored to rootDir (they are still there because
	// the staging+restore round-trip is idempotent and never deleted them); staging
	// dir does not exist; manifest is at phase=aborting; recovery-required still set.
	require.NoError(t, writeResetManifest(resetManifestPath(rootDir), resetManifest{
		Version:         resetManifestVersion,
		CurrUIDValidity: 200,
		Phase:           resetPhaseAborting,
	}))

	s, err := Open(rootDir, makeTestIdentity(), OpenRecoverReset)
	require.NoError(t, err)

	// ResetForRecovery must refuse: committing here would discard the just-restored
	// data and leave a "new UIDValidity + cleared recovery + empty store" state while
	// the operator's intent was to abort.
	assert.ErrorIs(t, s.ResetForRecovery(200), ErrResetAbortInProgress)

	// Manifest must remain so the operator can re-run AbortReset to finish.
	_, err = os.Stat(resetManifestPath(rootDir))
	assert.NoError(t, err)

	// Sentinel must remain untouched (recovery_required still set).
	_, _, _, recFound, err := s.LoadRecoveryRequired()
	require.NoError(t, err)
	assert.True(t, recFound)

	// AbortReset finishes idempotently (restoreFromStaging is a no-op since staging
	// is absent) and removes the manifest.
	require.NoError(t, s.AbortReset())
	_, err = os.Stat(resetManifestPath(rootDir))
	assert.True(t, os.IsNotExist(err))

	// Original data still present in rootDir.
	s2, err := Open(rootDir, makeTestIdentity(), OpenReadWrite)
	require.NoError(t, err)
	reports, err := s2.GetAllReports()
	require.NoError(t, err)
	assert.Len(t, reports, 1, "old data must remain after the abort completes")
}

// TestResetForRecovery_RefusesAbortingPhase verifies that ResetForRecovery rejects
// a manifest in the aborting phase rather than continuing to commit.  This is the
// safety check that prevents a partially-applied AbortReset from being silently
// converted into a forward commit.
func TestResetForRecovery_RefusesAbortingPhase(t *testing.T) {
	rootDir := t.TempDir()

	sRW, err := Open(rootDir, makeTestIdentity(), OpenReadWrite)
	require.NoError(t, err)
	require.NoError(t, sRW.SaveUIDValidity(100))
	require.NoError(t, sRW.SaveRecoveryRequired(100, 200, time.Now()))

	require.NoError(t, writeResetManifest(resetManifestPath(rootDir), resetManifest{
		Version:         resetManifestVersion,
		CurrUIDValidity: 200,
		Phase:           resetPhaseAborting,
	}))

	s, err := Open(rootDir, makeTestIdentity(), OpenRecoverReset)
	require.NoError(t, err)

	assert.ErrorIs(t, s.ResetForRecovery(200), ErrResetAbortInProgress)

	// Manifest must remain so AbortReset can finish.
	_, err = os.Stat(resetManifestPath(rootDir))
	assert.NoError(t, err)

	// Sentinel must remain untouched (recovery_required still set, UIDValidity unchanged).
	v, found, err := s.LoadUIDValidity()
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, uint32(100), v)
	_, _, _, recFound, err := s.LoadRecoveryRequired()
	require.NoError(t, err)
	assert.True(t, recFound)
}

// TestResetForRecovery_WipesStaleStaging verifies that a stale staging directory
// left over from a previously-completed reset (whose RemoveAll silently failed)
// does not break a new ResetForRecovery.  Without the fresh-start cleanup, the
// second reset's stageEmailsDir would fail with ENOTEMPTY when renaming onto a
// non-empty stale staging/emails directory.
func TestResetForRecovery_WipesStaleStaging(t *testing.T) {
	rootDir := t.TempDir()

	sRW, err := Open(rootDir, makeTestIdentity(), OpenReadWrite)
	require.NoError(t, err)
	require.NoError(t, sRW.SaveUIDValidity(100))
	require.NoError(t, SaveReport(sRW, ReportInput{
		Report:      makeFullReport("report-1", time.Now()),
		UID:         1,
		UIDValidity: 100,
	}))
	require.NoError(t, sRW.SaveRecoveryRequired(100, 200, time.Now()))

	// Plant stale staging contents that would conflict with a fresh stage attempt
	// (staging/emails non-empty would break rename(rootDir/emails -> staging/emails)).
	stagingPath := resetStagingPath(rootDir)
	require.NoError(t, os.MkdirAll(filepath.Join(stagingPath, "emails", "100", "202401"), dirPerm))
	require.NoError(t, os.WriteFile(filepath.Join(stagingPath, "emails", "100", "202401", "stale.eml"), []byte("stale"), filePerm))
	require.NoError(t, os.WriteFile(filepath.Join(stagingPath, "tlsrpt.json"), []byte("stale"), filePerm))

	s, err := Open(rootDir, makeTestIdentity(), OpenRecoverReset)
	require.NoError(t, err)
	require.NoError(t, s.ResetForRecovery(200))

	// Reset succeeded end-to-end.
	_, err = os.Stat(stagingPath)
	assert.True(t, os.IsNotExist(err), "staging dir must be removed after successful reset")
	_, err = os.Stat(resetManifestPath(rootDir))
	assert.True(t, os.IsNotExist(err), "manifest must be removed after successful reset")

	v, found, err := s.LoadUIDValidity()
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, uint32(200), v)

	_, _, _, recFound, err := s.LoadRecoveryRequired()
	require.NoError(t, err)
	assert.False(t, recFound)

	s2, err := Open(rootDir, makeTestIdentity(), OpenReadWrite)
	require.NoError(t, err)
	reports, err := s2.GetAllReports()
	require.NoError(t, err)
	assert.Empty(t, reports)
}

// TestSummaryConsistencyGuard_NoopOnMissingRootDir verifies that AcquireSummaryConsistencyGuard
// returns a no-op guard (not an error) when rootDir does not exist, as required for the
// empty-store OpenReadOnly path used by the summary subcommand on first run.
func TestSummaryConsistencyGuard_NoopOnMissingRootDir(t *testing.T) {
	rootDir := t.TempDir()
	nonexistentRoot := filepath.Join(rootDir, "nonexistent")
	s, err := Open(nonexistentRoot, makeTestIdentity(), OpenReadOnly)
	require.NoError(t, err)

	guard, err := s.AcquireSummaryConsistencyGuard()
	require.NoError(t, err, "AcquireSummaryConsistencyGuard must not fail when rootDir is absent")
	require.NotNil(t, guard)

	found, err := guard.CheckRecoveryRequired(context.Background())
	require.NoError(t, err)
	assert.False(t, found, "no-op guard must return found=false")

	assert.NoError(t, guard.Close())
}

// TestSummaryConsistencyGuard_CheckRecoveryRequired verifies that CheckRecoveryRequired
// reads the sentinel on each call and reflects the current state.
func TestSummaryConsistencyGuard_CheckRecoveryRequired(t *testing.T) {
	s, rootDir := openTestStore(t)

	guard, err := s.AcquireSummaryConsistencyGuard()
	require.NoError(t, err)
	t.Cleanup(func() { _ = guard.Close() })

	// No recovery-required initially.
	found, err := guard.CheckRecoveryRequired(context.Background())
	require.NoError(t, err)
	assert.False(t, found)

	// Simulate a writer updating the sentinel by writing directly (bypassing the
	// guard-lock path), as a separate process would do after we release the lock.
	sentinel, _, err := loadSentinel(rootDir)
	require.NoError(t, err)
	sentinel.RecoveryRequired = &internalRecoveryState{
		PrevUIDValidity: 1,
		CurrUIDValidity: 2,
		DetectedAt:      time.Now(),
	}
	require.NoError(t, saveSentinel(rootDir, sentinel))

	// Guard should now see recovery-required on the next check.
	found, err = guard.CheckRecoveryRequired(context.Background())
	require.NoError(t, err)
	assert.True(t, found)
}

// TestOpen_CleansUpCommittedManifest_PhaseCommitted verifies cleanup when the manifest
// is at the explicit phase=committed (4), the canonical post-commit leftover state
// produced by new code when RemoveAll/Remove crashes before completing.
func TestOpen_CleansUpCommittedManifest_PhaseCommitted(t *testing.T) {
	rootDir := t.TempDir()
	sRW, err := Open(rootDir, makeTestIdentity(), OpenReadWrite)
	require.NoError(t, err)
	require.NoError(t, sRW.SaveUIDValidity(100))
	require.NoError(t, sRW.SaveRecoveryRequired(100, 200, time.Now()))

	// Committed sentinel + phase=committed manifest.
	sentinel, _, err := loadSentinel(rootDir)
	require.NoError(t, err)
	newUID := uint32(200)
	sentinel.UIDValidity = &newUID
	sentinel.RecoveryRequired = nil
	require.NoError(t, saveSentinel(rootDir, sentinel))
	stagingPath := resetStagingPath(rootDir)
	require.NoError(t, os.MkdirAll(stagingPath, dirPerm))
	require.NoError(t, writeResetManifest(resetManifestPath(rootDir), resetManifest{
		Version:         resetManifestVersion,
		CurrUIDValidity: 200,
		Phase:           resetPhaseCommitted,
	}))

	s2, err := Open(rootDir, makeTestIdentity(), OpenReadWrite)
	require.NoError(t, err)

	_, err = os.Stat(resetManifestPath(rootDir))
	assert.True(t, os.IsNotExist(err), "manifest must be removed by Open")
	_, err = os.Stat(stagingPath)
	assert.True(t, os.IsNotExist(err), "staging dir must be removed by Open")

	reports, err := s2.GetAllReports()
	require.NoError(t, err)
	assert.Empty(t, reports)
}

// TestOpen_CleansUpAfterCommitCrashWindow simulates the narrow commit-crash window:
// commitReset has saved the sentinel (recovery_required cleared, new UIDValidity set)
// but crashed before advancing the manifest from phase=emails_staged to phase=committed.
// OpenReadWrite must detect the committed state via the sentinel and clean up the
// leftover manifest and staging, not return ErrPendingReset.
func TestOpen_CleansUpAfterCommitCrashWindow(t *testing.T) {
	rootDir := t.TempDir()
	sRW, err := Open(rootDir, makeTestIdentity(), OpenReadWrite)
	require.NoError(t, err)
	require.NoError(t, sRW.SaveUIDValidity(100))
	require.NoError(t, SaveReport(sRW, ReportInput{
		Report:      makeFullReport("report-1", time.Now()),
		UID:         1,
		UIDValidity: 100,
	}))
	require.NoError(t, sRW.SaveRecoveryRequired(100, 200, time.Now()))

	// Move data to staging, then commit sentinel by hand, leaving manifest at phase=3.
	stagingPath := resetStagingPath(rootDir)
	require.NoError(t, os.MkdirAll(stagingPath, dirPerm))
	require.NoError(t, os.Rename(dataFilePath(rootDir), filepath.Join(stagingPath, "tlsrpt.json")))
	require.NoError(t, os.Rename(emailsPath(rootDir), filepath.Join(stagingPath, "emails")))
	sentinel, _, err := loadSentinel(rootDir)
	require.NoError(t, err)
	newUID := uint32(200)
	sentinel.UIDValidity = &newUID
	sentinel.RecoveryRequired = nil
	require.NoError(t, saveSentinel(rootDir, sentinel))
	require.NoError(t, writeResetManifest(resetManifestPath(rootDir), resetManifest{
		Version:         resetManifestVersion,
		CurrUIDValidity: 200,
		Phase:           resetPhaseEmailsStaged, // crash before advancing to committed
	}))

	// OpenReadWrite detects committed state from sentinel and cleans up.
	s, err := Open(rootDir, makeTestIdentity(), OpenReadWrite)
	require.NoError(t, err)

	_, err = os.Stat(resetManifestPath(rootDir))
	assert.True(t, os.IsNotExist(err), "manifest must be removed")
	_, err = os.Stat(stagingPath)
	assert.True(t, os.IsNotExist(err), "staging dir must be removed")

	v, found, err := s.LoadUIDValidity()
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, uint32(200), v)

	// Old data was in staging; removing staging discards it — empty store.
	reports, err := s.GetAllReports()
	require.NoError(t, err)
	assert.Empty(t, reports)
}

// TestOpen_BlockedByPreCommitReset verifies that OpenReadWrite still returns
// ErrPendingReset when recovery_required is set (i.e. the reset is genuinely
// in-progress or an AbortReset is partially applied).
func TestOpen_BlockedByPreCommitReset(t *testing.T) {
	rootDir := t.TempDir()
	sRW, err := Open(rootDir, makeTestIdentity(), OpenReadWrite)
	require.NoError(t, err)
	require.NoError(t, sRW.SaveUIDValidity(100))
	require.NoError(t, sRW.SaveRecoveryRequired(100, 200, time.Now()))

	stagingPath := resetStagingPath(rootDir)
	require.NoError(t, os.MkdirAll(stagingPath, dirPerm))
	require.NoError(t, writeResetManifest(resetManifestPath(rootDir), resetManifest{
		Version:         resetManifestVersion,
		CurrUIDValidity: 200,
		Phase:           resetPhaseEmailsStaged,
	}))

	_, err = Open(rootDir, makeTestIdentity(), OpenReadWrite)
	assert.ErrorIs(t, err, ErrPendingReset)
}

// TestSummaryConsistencyGuard_BlocksExclusiveWriter verifies that a shared guard lock
// blocks an exclusive writer (simulating a concurrent fetch) until Close is called.
func TestSummaryConsistencyGuard_BlocksExclusiveWriter(t *testing.T) {
	s, _ := openTestStore(t)

	guard, err := s.AcquireSummaryConsistencyGuard()
	require.NoError(t, err)

	// Try to acquire exclusive lock on the same guard file from a goroutine.
	done := make(chan error, 1)
	go func() {
		done <- s.SaveRecoveryRequired(1, 2, time.Now())
	}()

	// The goroutine should be blocked (give it a moment, then verify it hasn't finished).
	select {
	case err := <-done:
		t.Fatalf("exclusive writer should have been blocked; got: %v", err)
	case <-time.After(50 * time.Millisecond):
		// Expected: goroutine is blocked.
	}

	// Releasing the shared lock should unblock the writer.
	require.NoError(t, guard.Close())

	select {
	case err := <-done:
		assert.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("exclusive writer did not complete after shared lock released")
	}
}
