//go:build test

package store

import (
	"fmt"
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

// TestApplyRecovery_RefusesPendingReset verifies that ApplyRecovery returns ErrPendingReset
// when a reset manifest is present.  Without this guard, keep-old recovery could clear
// recovery_required while data files are in staging, leaving the store inconsistent.
func TestApplyRecovery_RefusesPendingReset(t *testing.T) {
	rootDir := t.TempDir()
	sRW, err := Open(rootDir, makeTestIdentity(), OpenReadWrite)
	require.NoError(t, err)
	require.NoError(t, sRW.SaveUIDValidity(100))
	require.NoError(t, sRW.SaveRecoveryRequired(100, 200, time.Now()))

	// Plant a phase-1 manifest to verify that ApplyRecovery refuses while a pre-commit reset is in progress.
	stagingPath := resetStagingPath(rootDir)
	require.NoError(t, os.MkdirAll(stagingPath, dirPerm))
	require.NoError(t, writeResetManifest(resetManifestPath(rootDir), resetManifest{
		Version: resetManifestVersion, CurrUIDValidity: 200, Phase: resetPhaseManifestWritten,
	}))

	s, err := Open(rootDir, makeTestIdentity(), OpenRecoverReset)
	require.NoError(t, err)

	assert.ErrorIs(t, s.ApplyRecovery(200), ErrPendingReset,
		"ApplyRecovery must refuse while a reset manifest is present")

	// recovery_required must remain set.
	_, _, _, found, err := s.LoadRecoveryRequired()
	require.NoError(t, err)
	assert.True(t, found, "recovery_required must not be cleared by a refused ApplyRecovery")
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

func assertResetConverged(t *testing.T, s Store, rootDir string) {
	t.Helper()

	v, found, err := s.LoadUIDValidity()
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, uint32(200), v)

	_, _, _, recFound, err := s.LoadRecoveryRequired()
	require.NoError(t, err)
	assert.False(t, recFound)

	pending, err := s.HasPendingReset()
	require.NoError(t, err)
	assert.False(t, pending)

	_, err = os.Stat(resetManifestPath(rootDir))
	assert.ErrorIs(t, err, os.ErrNotExist, "manifest must be removed")
	_, err = os.Stat(resetStagingPath(rootDir))
	assert.ErrorIs(t, err, os.ErrNotExist, "staging dir must be removed")

	_, err = os.Stat(dataFilePath(rootDir))
	assert.ErrorIs(t, err, os.ErrNotExist, "root data file must be absent")
	_, err = os.Stat(emailsPath(rootDir))
	assert.ErrorIs(t, err, os.ErrNotExist, "root emails dir must be absent")

	sRW, err := Open(rootDir, makeTestIdentity(), OpenReadWrite)
	require.NoError(t, err)
	reports, err := sRW.GetAllReports()
	require.NoError(t, err)
	assert.Empty(t, reports, "store must be empty after ResetForRecovery")
}

func commitSentinelForResetTest(t *testing.T, rootDir string, uidValidity uint32) {
	t.Helper()

	sentinel, _, err := loadSentinel(rootDir)
	require.NoError(t, err)
	sentinel.UIDValidity = &uidValidity
	sentinel.RecoveryRequired = nil
	require.NoError(t, saveSentinel(rootDir, sentinel))
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
	require.NoError(t, SaveReport(s, ReportInput{
		Report:      makeFullReport("report-1", time.Now()),
		UID:         1,
		UIDValidity: 100,
	}))
	require.NoError(t, s.SaveEmail(1, 100, time.Date(2026, 5, 31, 0, 0, 0, 0, time.UTC), makeTestEML("")))
	require.NoError(t, s.SaveRecoveryRequired(100, 200, time.Now()))

	require.NoError(t, s.ResetForRecovery(200))
	assertResetConverged(t, s, rootDir)
}

// TestResetForRecovery_IdempotentAfterCrashBeforeCommit simulates a crash after staging
// but before commit by planting a manifest file, then verifies that re-running
// ResetForRecovery converges to the committed state.
func TestResetForRecovery_IdempotentAfterCrashBeforeCommit(t *testing.T) {
	rootDir := t.TempDir()
	s1, err := Open(rootDir, makeTestIdentity(), OpenRecoverReset)
	require.NoError(t, err)
	require.NoError(t, s1.SaveUIDValidity(100))
	require.NoError(t, SaveReport(s1, ReportInput{
		Report:      makeFullReport("report-1", time.Now()),
		UID:         1,
		UIDValidity: 100,
	}))
	require.NoError(t, s1.SaveEmail(1, 100, time.Date(2026, 5, 31, 0, 0, 0, 0, time.UTC), makeTestEML("")))
	require.NoError(t, s1.SaveRecoveryRequired(100, 200, time.Now()))

	// Plant a phase-1 manifest with both data and emails already staged to verify
	// idempotent convergence from a partial pre-commit staging state.
	stagingPath := resetStagingPath(rootDir)
	require.NoError(t, os.MkdirAll(stagingPath, dirPerm))
	require.NoError(t, os.Rename(dataFilePath(rootDir), filepath.Join(stagingPath, "tlsrpt.json")))
	require.NoError(t, os.Rename(emailsPath(rootDir), filepath.Join(stagingPath, "emails")))
	require.NoError(t, writeResetManifest(resetManifestPath(rootDir), resetManifest{
		Version: resetManifestVersion, CurrUIDValidity: 200, Phase: resetPhaseManifestWritten,
	}))

	// Re-open with OpenRecoverReset (manifest present, so OpenReadWrite would fail).
	s2, err := Open(rootDir, makeTestIdentity(), OpenRecoverReset)
	require.NoError(t, err)

	// ResetForRecovery should resume and complete from the staged state.
	require.NoError(t, s2.ResetForRecovery(200))
	assertResetConverged(t, s2, rootDir)
}

// TestResetForRecovery_CrashAfterBothFilesStaged simulates the new C3 crash
// layout: the manifest is still at phase 1 while both root files are already in staging.
func TestResetForRecovery_CrashAfterBothFilesStaged(t *testing.T) {
	rootDir := t.TempDir()

	s, err := Open(rootDir, makeTestIdentity(), OpenReadWrite)
	require.NoError(t, err)
	require.NoError(t, s.SaveUIDValidity(100))
	require.NoError(t, SaveReport(s, ReportInput{
		Report:      makeFullReport("report-1", time.Now()),
		UID:         1,
		UIDValidity: 100,
	}))
	require.NoError(t, s.SaveEmail(1, 100, time.Date(2026, 5, 31, 0, 0, 0, 0, time.UTC), makeTestEML("")))
	require.NoError(t, s.SaveRecoveryRequired(100, 200, time.Now()))

	stagingPath := resetStagingPath(rootDir)
	require.NoError(t, os.MkdirAll(stagingPath, dirPerm))
	require.NoError(t, os.Rename(dataFilePath(rootDir), filepath.Join(stagingPath, "tlsrpt.json")))
	require.NoError(t, os.Rename(emailsPath(rootDir), filepath.Join(stagingPath, "emails")))
	require.NoError(t, writeResetManifest(resetManifestPath(rootDir), resetManifest{
		Version:         resetManifestVersion,
		CurrUIDValidity: 200,
		Phase:           resetPhaseManifestWritten,
	}))

	s2, err := Open(rootDir, makeTestIdentity(), OpenRecoverReset)
	require.NoError(t, err)
	require.NoError(t, s2.ResetForRecovery(200))
	assertResetConverged(t, s2, rootDir)
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

// TestResetForRecovery_CrashAfterStageEmailsBeforeManifestUpdate verifies convergence
// from a partial pre-commit phase-1 staging state: tlsrpt.json is already in staging
// while emails/ remains in root. ResetForRecovery must converge from this state by
// re-running the full idempotent staging sequence.
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
	require.NoError(t, s.SaveEmail(1, 100, time.Date(2026, 5, 31, 0, 0, 0, 0, time.UTC), makeTestEML("")))
	require.NoError(t, s.SaveRecoveryRequired(100, 200, time.Now()))

	// Simulate a partial pre-commit phase-1 staging state: tlsrpt.json is already in
	// staging but emails/ is still in root (crash after staging data, before staging emails).
	stagingPath := resetStagingPath(rootDir)
	require.NoError(t, os.MkdirAll(stagingPath, dirPerm))
	require.NoError(t, os.Rename(dataFilePath(rootDir), filepath.Join(stagingPath, "tlsrpt.json")))
	require.NoError(t, writeResetManifest(resetManifestPath(rootDir), resetManifest{
		Version:         resetManifestVersion,
		CurrUIDValidity: 200,
		Phase:           resetPhaseManifestWritten,
	}))

	s2, err := Open(rootDir, makeTestIdentity(), OpenRecoverReset)
	require.NoError(t, err)
	require.NoError(t, s2.ResetForRecovery(200))
	assertResetConverged(t, s2, rootDir)
}

// TestResetForRecovery_CrashAfterStageDataBeforeManifestUpdate simulates a crash after
// stageDataFile completed (tlsrpt.json already in staging) but before stageEmailsDir.
// The manifest is at phase=1 but tlsrpt.json is already in staging.
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
	// before the crash).
	stagingPath := resetStagingPath(rootDir)
	require.NoError(t, os.MkdirAll(stagingPath, dirPerm))
	require.NoError(t, os.Rename(dataFilePath(rootDir), filepath.Join(stagingPath, "tlsrpt.json")))
	require.NoError(t, writeResetManifest(resetManifestPath(rootDir), resetManifest{
		Version:         resetManifestVersion,
		CurrUIDValidity: 200,
		Phase:           resetPhaseManifestWritten,
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

// TestResetForRecovery_Phase1MissingStagingDirConverges verifies that a phase-1
// manifest can recover even when the staging directory is missing.
func TestResetForRecovery_Phase1MissingStagingDirConverges(t *testing.T) {
	rootDir := t.TempDir()

	s, err := Open(rootDir, makeTestIdentity(), OpenReadWrite)
	require.NoError(t, err)
	require.NoError(t, s.SaveUIDValidity(100))
	require.NoError(t, SaveReport(s, ReportInput{
		Report:      makeFullReport("report-1", time.Now()),
		UID:         1,
		UIDValidity: 100,
	}))
	require.NoError(t, s.SaveEmail(1, 100, time.Date(2026, 5, 31, 0, 0, 0, 0, time.UTC), makeTestEML("")))
	require.NoError(t, s.SaveRecoveryRequired(100, 200, time.Now()))
	require.NoError(t, writeResetManifest(resetManifestPath(rootDir), resetManifest{
		Version:         resetManifestVersion,
		CurrUIDValidity: 200,
		Phase:           resetPhaseManifestWritten,
	}))
	require.NoFileExists(t, resetStagingPath(rootDir))

	s2, err := Open(rootDir, makeTestIdentity(), OpenRecoverReset)
	require.NoError(t, err)
	require.NoError(t, s2.ResetForRecovery(200))
	assertResetConverged(t, s2, rootDir)
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

// TestOpen_CleansUpAfterCommitCrashWindowManifestWritten simulates the new C4
// crash window: commitReset saved the sentinel while the manifest remained at phase 1.
func TestOpen_CleansUpAfterCommitCrashWindowManifestWritten(t *testing.T) {
	rootDir := t.TempDir()
	sRW, err := Open(rootDir, makeTestIdentity(), OpenReadWrite)
	require.NoError(t, err)
	require.NoError(t, sRW.SaveUIDValidity(100))
	require.NoError(t, SaveReport(sRW, ReportInput{
		Report:      makeFullReport("report-1", time.Now()),
		UID:         1,
		UIDValidity: 100,
	}))
	require.NoError(t, sRW.SaveEmail(1, 100, time.Date(2026, 5, 31, 0, 0, 0, 0, time.UTC), makeTestEML("")))
	require.NoError(t, sRW.SaveRecoveryRequired(100, 200, time.Now()))

	stagingPath := resetStagingPath(rootDir)
	require.NoError(t, os.MkdirAll(stagingPath, dirPerm))
	require.NoError(t, os.Rename(dataFilePath(rootDir), filepath.Join(stagingPath, "tlsrpt.json")))
	require.NoError(t, os.Rename(emailsPath(rootDir), filepath.Join(stagingPath, "emails")))
	commitSentinelForResetTest(t, rootDir, 200)
	require.NoError(t, writeResetManifest(resetManifestPath(rootDir), resetManifest{
		Version:         resetManifestVersion,
		CurrUIDValidity: 200,
		Phase:           resetPhaseManifestWritten,
	}))

	s, err := Open(rootDir, makeTestIdentity(), OpenReadWrite)
	require.NoError(t, err)

	_, err = os.Stat(resetManifestPath(rootDir))
	assert.ErrorIs(t, err, os.ErrNotExist, "manifest must be removed")
	_, err = os.Stat(stagingPath)
	assert.ErrorIs(t, err, os.ErrNotExist, "staging dir must be removed")

	v, found, err := s.LoadUIDValidity()
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, uint32(200), v)

	_, _, _, recFound, err := s.LoadRecoveryRequired()
	require.NoError(t, err)
	assert.False(t, recFound)

	reports, err := s.GetAllReports()
	require.NoError(t, err)
	assert.Empty(t, reports)
}

// TestOpen_BlockedByPreCommitReset verifies that OpenReadWrite returns ErrPendingReset
// when recovery_required is set and a pre-commit reset manifest is present.
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
		Phase:           resetPhaseManifestWritten, // i.e. a pre-commit reset manifest is present
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

// TestSummaryConsistencyGuard_MissingGuardFileCreated verifies that
// AcquireSummaryConsistencyGuard creates the guard file when it is absent for an
// existing store (e.g. a store created before the guard file was introduced).
// The created guard must hold a real LOCK_SH that blocks concurrent exclusive writers.
func TestSummaryConsistencyGuard_MissingGuardFileCreated(t *testing.T) {
	s, rootDir := openTestStore(t)

	// Remove the guard file to simulate a pre-feature or manually-deleted state.
	require.NoError(t, os.Remove(guardFilePath(rootDir)))

	guard, err := s.AcquireSummaryConsistencyGuard()
	require.NoError(t, err)

	// Guard file must have been recreated.
	_, err = os.Stat(guardFilePath(rootDir))
	require.NoError(t, err, "guard file must be recreated by AcquireSummaryConsistencyGuard")

	// The guard must hold a real LOCK_SH that blocks an exclusive writer.
	done := make(chan error, 1)
	go func() {
		done <- s.SaveRecoveryRequired(1, 2, time.Now())
	}()

	select {
	case err := <-done:
		t.Fatalf("exclusive writer should have been blocked; got: %v", err)
	case <-time.After(50 * time.Millisecond):
		// Expected: goroutine is blocked.
	}

	require.NoError(t, guard.Close())
	select {
	case err := <-done:
		assert.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("exclusive writer did not complete after shared lock released")
	}
}

// TestOpen_CleansUpOrphanStagingDir verifies that Open(OpenReadWrite) removes a
// staging directory that was left behind after the manifest was already deleted.
// This covers the window where executeResetFromManifest removed the manifest
// successfully but RemoveAll(staging) failed on a previous run.
func TestOpen_CleansUpOrphanStagingDir(t *testing.T) {
	rootDir := t.TempDir()
	sRW, err := Open(rootDir, makeTestIdentity(), OpenReadWrite)
	require.NoError(t, err)
	require.NoError(t, sRW.SaveUIDValidity(100))

	// Plant an orphan staging dir (no manifest).
	stagingPath := resetStagingPath(rootDir)
	require.NoError(t, os.MkdirAll(filepath.Join(stagingPath, "emails", "100", "202601"), dirPerm))
	require.NoError(t, os.WriteFile(filepath.Join(stagingPath, "tlsrpt.json"), []byte("stale"), filePerm))

	// No manifest exists — Open(OpenReadWrite) should clean up the orphan.
	s2, err := Open(rootDir, makeTestIdentity(), OpenReadWrite)
	require.NoError(t, err)

	_, statErr := os.Stat(stagingPath)
	assert.ErrorIs(t, statErr, os.ErrNotExist, "orphan staging dir must be removed by Open(OpenReadWrite)")

	// Normal data operations continue to work.
	v, found, err := s2.LoadUIDValidity()
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, uint32(100), v)
}

func TestResetPhasePersistedNumericValues(t *testing.T) {
	assert.Equal(t, resetPhase(1), resetPhaseManifestWritten)
	assert.Equal(t, resetPhase(4), resetPhaseCommitted)
}

func TestValidateManifestPhaseRange(t *testing.T) {
	for _, phase := range []resetPhase{1, 4} {
		t.Run(fmt.Sprintf("accepts_%d", phase), func(t *testing.T) {
			assert.NoError(t, validateManifestPhase(phase))
		})
	}
	for _, phase := range []resetPhase{0, 2, 3, 5, 6, 99} {
		t.Run(fmt.Sprintf("rejects_%d", phase), func(t *testing.T) {
			var phaseErr *ErrResetManifestPhaseUnknown
			require.ErrorAs(t, validateManifestPhase(phase), &phaseErr)
			assert.Equal(t, int(phase), phaseErr.Got)
		})
	}
}

func TestHasPendingReset_NoManifest(t *testing.T) {
	s, _ := openRecoverResetStore(t)
	found, err := s.HasPendingReset()
	require.NoError(t, err)
	assert.False(t, found)
}

func TestHasPendingReset_ManifestPresent(t *testing.T) {
	s, rootDir := openRecoverResetStore(t)
	require.NoError(t, writeResetManifest(resetManifestPath(rootDir), resetManifest{
		Version:         resetManifestVersion,
		Phase:           resetPhaseManifestWritten,
		CurrUIDValidity: 42,
	}))
	found, err := s.HasPendingReset()
	require.NoError(t, err)
	assert.True(t, found)
}

// TestResetForRecovery_CommitCrashWindow_ZeroUID simulates the narrow crash window in
// commitReset where the sentinel has been saved (recovery_required cleared, new
// UIDValidity written) but the manifest has not yet been advanced to phase=committed.
// This is the state seen by handleNoRecoveryRequired in the recover subcommand,
// which calls ResetForRecovery(0).
//
// The key property being verified is that ResetForRecovery resumes using the
// CurrUIDValidity stored in the manifest (200), NOT the caller-supplied 0, and that
// the full cleanup (staging + manifest removal) completes successfully.
func TestResetForRecovery_CommitCrashWindow_ZeroUID(t *testing.T) {
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

	// Simulate the crash window: data/emails staged, sentinel committed, manifest still
	// at phase=manifest_written (commit saved sentinel but did not write phase=committed).
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
		Phase:           resetPhaseManifestWritten,
	}))

	// handleNoRecoveryRequired calls ResetForRecovery(0) because LoadRecoveryRequired
	// returns found=false but HasPendingReset returns true.
	s, err := Open(rootDir, makeTestIdentity(), OpenRecoverReset)
	require.NoError(t, err)
	require.NoError(t, s.ResetForRecovery(0))

	// Sentinel must retain the committed UIDValidity.
	v, found, err := s.LoadUIDValidity()
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, uint32(200), v)

	// Recovery-required must remain absent.
	_, _, _, recFound, err := s.LoadRecoveryRequired()
	require.NoError(t, err)
	assert.False(t, recFound)

	// Manifest and staging must be cleaned up.
	_, err = os.Stat(resetManifestPath(rootDir))
	assert.ErrorIs(t, err, os.ErrNotExist, "manifest must be removed")
	_, err = os.Stat(stagingPath)
	assert.ErrorIs(t, err, os.ErrNotExist, "staging dir must be removed")

	// Store must be openable for normal use with empty data.
	s2, err := Open(rootDir, makeTestIdentity(), OpenReadWrite)
	require.NoError(t, err)
	reports, err := s2.GetAllReports()
	require.NoError(t, err)
	assert.Empty(t, reports, "store must be empty after cleanup (old data was in staging)")
}

// TestResetForRecovery_CommitCrashWindowManifestWritten_ZeroUID covers the new
// C4 window where commitReset saved the sentinel but the manifest was still phase 1.
func TestResetForRecovery_CommitCrashWindowManifestWritten_ZeroUID(t *testing.T) {
	rootDir := t.TempDir()

	sRW, err := Open(rootDir, makeTestIdentity(), OpenReadWrite)
	require.NoError(t, err)
	require.NoError(t, sRW.SaveUIDValidity(100))
	require.NoError(t, SaveReport(sRW, ReportInput{
		Report:      makeFullReport("report-1", time.Now()),
		UID:         1,
		UIDValidity: 100,
	}))
	require.NoError(t, sRW.SaveEmail(1, 100, time.Date(2026, 5, 31, 0, 0, 0, 0, time.UTC), makeTestEML("")))
	require.NoError(t, sRW.SaveRecoveryRequired(100, 200, time.Now()))

	stagingPath := resetStagingPath(rootDir)
	require.NoError(t, os.MkdirAll(stagingPath, dirPerm))
	require.NoError(t, os.Rename(dataFilePath(rootDir), filepath.Join(stagingPath, "tlsrpt.json")))
	require.NoError(t, os.Rename(emailsPath(rootDir), filepath.Join(stagingPath, "emails")))
	commitSentinelForResetTest(t, rootDir, 200)
	require.NoError(t, writeResetManifest(resetManifestPath(rootDir), resetManifest{
		Version:         resetManifestVersion,
		CurrUIDValidity: 200,
		Phase:           resetPhaseManifestWritten,
	}))

	s, err := Open(rootDir, makeTestIdentity(), OpenRecoverReset)
	require.NoError(t, err)
	require.NoError(t, s.ResetForRecovery(0))
	assertResetConverged(t, s, rootDir)
}

// TestLegacyPhaseFailsClosed_ResetForRecovery verifies that a manifest with a legacy
// phase (2, 3, or 5) causes ResetForRecovery to fail-closed: it returns
// ErrResetManifestPhaseUnknown and leaves both the manifest and staging dir intact.
func TestLegacyPhaseFailsClosed_ResetForRecovery(t *testing.T) {
	for _, phase := range []resetPhase{2, 3, 5} {
		t.Run(fmt.Sprintf("phase_%d", phase), func(t *testing.T) {
			rootDir := t.TempDir()
			sRW, err := Open(rootDir, makeTestIdentity(), OpenReadWrite)
			require.NoError(t, err)
			require.NoError(t, sRW.SaveUIDValidity(100))
			require.NoError(t, sRW.SaveRecoveryRequired(100, 200, time.Now()))

			manifestPath := resetManifestPath(rootDir)
			stagingPath := resetStagingPath(rootDir)
			require.NoError(t, os.MkdirAll(stagingPath, dirPerm))
			require.NoError(t, writeResetManifest(manifestPath, resetManifest{
				Version: resetManifestVersion, CurrUIDValidity: 200, Phase: phase,
			}))

			s, err := Open(rootDir, makeTestIdentity(), OpenRecoverReset)
			require.NoError(t, err)

			err = s.ResetForRecovery(200)
			var phaseErr *ErrResetManifestPhaseUnknown
			require.ErrorAs(t, err, &phaseErr, "phase %d must be fail-closed", phase)
			assert.Equal(t, int(phase), phaseErr.Got)

			_, statErr := os.Stat(manifestPath)
			assert.NoError(t, statErr, "manifest must be preserved for phase %d", phase)
			_, statErr = os.Stat(stagingPath)
			assert.NoError(t, statErr, "staging dir must be preserved for phase %d", phase)
		})
	}
}

// TestLegacyPhaseFailsClosed_OpenReadWrite verifies that Open(OpenReadWrite) fails-closed
// when a legacy phase manifest is present: returns ErrResetManifestPhaseUnknown and
// preserves the manifest and staging dir.
func TestLegacyPhaseFailsClosed_OpenReadWrite(t *testing.T) {
	for _, phase := range []resetPhase{2, 3, 5} {
		t.Run(fmt.Sprintf("phase_%d", phase), func(t *testing.T) {
			rootDir := t.TempDir()
			sRW, err := Open(rootDir, makeTestIdentity(), OpenReadWrite)
			require.NoError(t, err)
			require.NoError(t, sRW.SaveUIDValidity(100))
			require.NoError(t, sRW.SaveRecoveryRequired(100, 200, time.Now()))

			manifestPath := resetManifestPath(rootDir)
			stagingPath := resetStagingPath(rootDir)
			require.NoError(t, os.MkdirAll(stagingPath, dirPerm))
			require.NoError(t, writeResetManifest(manifestPath, resetManifest{
				Version: resetManifestVersion, CurrUIDValidity: 200, Phase: phase,
			}))

			_, err = Open(rootDir, makeTestIdentity(), OpenReadWrite)
			var phaseErr *ErrResetManifestPhaseUnknown
			require.ErrorAs(t, err, &phaseErr, "Open(OpenReadWrite) must fail-closed for phase %d", phase)
			assert.Equal(t, int(phase), phaseErr.Got)

			_, statErr := os.Stat(manifestPath)
			assert.NoError(t, statErr, "manifest must be preserved for phase %d", phase)
			_, statErr = os.Stat(stagingPath)
			assert.NoError(t, statErr, "staging dir must be preserved for phase %d", phase)
		})
	}
}

// TestHasPendingReset_LegacyPhaseFailsClosed verifies that HasPendingReset fails-closed
// when a legacy phase manifest is present.
func TestHasPendingReset_LegacyPhaseFailsClosed(t *testing.T) {
	for _, phase := range []resetPhase{2, 3, 5} {
		t.Run(fmt.Sprintf("phase_%d", phase), func(t *testing.T) {
			rootDir := t.TempDir()
			sRW, err := Open(rootDir, makeTestIdentity(), OpenReadWrite)
			require.NoError(t, err)
			require.NoError(t, sRW.SaveUIDValidity(100))
			require.NoError(t, sRW.SaveRecoveryRequired(100, 200, time.Now()))

			manifestPath := resetManifestPath(rootDir)
			stagingPath := resetStagingPath(rootDir)
			require.NoError(t, os.MkdirAll(stagingPath, dirPerm))
			require.NoError(t, writeResetManifest(manifestPath, resetManifest{
				Version: resetManifestVersion, CurrUIDValidity: 200, Phase: phase,
			}))

			s, err := Open(rootDir, makeTestIdentity(), OpenRecoverReset)
			require.NoError(t, err)

			_, err = s.HasPendingReset()
			var phaseErr *ErrResetManifestPhaseUnknown
			require.ErrorAs(t, err, &phaseErr, "HasPendingReset must fail-closed for phase %d", phase)
			assert.Equal(t, int(phase), phaseErr.Got)

			_, statErr := os.Stat(manifestPath)
			assert.NoError(t, statErr, "manifest must be preserved for phase %d", phase)
			_, statErr = os.Stat(stagingPath)
			assert.NoError(t, statErr, "staging dir must be preserved for phase %d", phase)
		})
	}
}

// TestLegacyPhaseFailsClosed_ApplyRecovery verifies that ApplyRecovery fails-closed
// (via HasPendingReset → validateManifestPhase) when a legacy phase manifest is present.
func TestLegacyPhaseFailsClosed_ApplyRecovery(t *testing.T) {
	for _, phase := range []resetPhase{2, 3, 5} {
		t.Run(fmt.Sprintf("phase_%d", phase), func(t *testing.T) {
			rootDir := t.TempDir()
			sRW, err := Open(rootDir, makeTestIdentity(), OpenReadWrite)
			require.NoError(t, err)
			require.NoError(t, sRW.SaveUIDValidity(100))
			require.NoError(t, sRW.SaveRecoveryRequired(100, 200, time.Now()))

			manifestPath := resetManifestPath(rootDir)
			stagingPath := resetStagingPath(rootDir)
			require.NoError(t, os.MkdirAll(stagingPath, dirPerm))
			require.NoError(t, writeResetManifest(manifestPath, resetManifest{
				Version: resetManifestVersion, CurrUIDValidity: 200, Phase: phase,
			}))

			s, err := Open(rootDir, makeTestIdentity(), OpenRecoverReset)
			require.NoError(t, err)

			err = s.ApplyRecovery(200)
			var phaseErr *ErrResetManifestPhaseUnknown
			require.ErrorAs(t, err, &phaseErr, "ApplyRecovery must fail-closed for phase %d", phase)
			assert.Equal(t, int(phase), phaseErr.Got)

			_, statErr := os.Stat(manifestPath)
			assert.NoError(t, statErr, "manifest must be preserved for phase %d", phase)
			_, statErr = os.Stat(stagingPath)
			assert.NoError(t, statErr, "staging dir must be preserved for phase %d", phase)
		})
	}
}

// TestResetForRecovery_StaleUIDMismatchManifestReset verifies the UID-mismatch cleanup path
// in cleanupCompletedReset: when a phase-1 manifest has a CurrUIDValidity that doesn't match
// the current recovery_required, the stale manifest and staging are removed and the reset
// converges via a fresh start.
func TestResetForRecovery_StaleUIDMismatchManifestReset(t *testing.T) {
	rootDir := t.TempDir()

	s, err := Open(rootDir, makeTestIdentity(), OpenReadWrite)
	require.NoError(t, err)
	require.NoError(t, s.SaveUIDValidity(100))
	require.NoError(t, SaveReport(s, ReportInput{
		Report:      makeFullReport("report-1", time.Now()),
		UID:         1,
		UIDValidity: 100,
	}))
	require.NoError(t, s.SaveEmail(1, 100, time.Date(2026, 5, 31, 0, 0, 0, 0, time.UTC), makeTestEML("")))
	require.NoError(t, s.SaveRecoveryRequired(100, 200, time.Now()))

	// Plant a phase-1 manifest with a stale CurrUIDValidity (150) that doesn't match
	// the current recovery_required (200). cleanupCompletedReset's UID-mismatch check
	// removes the stale manifest and staging before ResetForRecovery starts fresh.
	stagingPath := resetStagingPath(rootDir)
	require.NoError(t, os.MkdirAll(stagingPath, dirPerm))
	require.NoError(t, os.WriteFile(filepath.Join(stagingPath, "tlsrpt.json"), []byte("stale"), 0o600))
	require.NoError(t, writeResetManifest(resetManifestPath(rootDir), resetManifest{
		Version:         resetManifestVersion,
		CurrUIDValidity: 150, // mismatches current recovery_required (200)
		Phase:           resetPhaseManifestWritten,
	}))

	s2, err := Open(rootDir, makeTestIdentity(), OpenRecoverReset)
	require.NoError(t, err)
	require.NoError(t, s2.ResetForRecovery(200))
	assertResetConverged(t, s2, rootDir)
}
