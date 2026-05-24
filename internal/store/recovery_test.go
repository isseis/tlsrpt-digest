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

	// Simulate crash after staging: plant manifest and staging dir but skip commit.
	stagingPath := resetStagingPath(rootDir)
	require.NoError(t, os.MkdirAll(stagingPath, dirPerm))
	mfstData := `{"version":1,"curr_uid_validity":200}`
	require.NoError(t, os.WriteFile(resetManifestPath(rootDir), []byte(mfstData), filePerm))

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

// TestAbortReset_NoPendingReset returns ErrResetNotPending when no manifest exists.
func TestAbortReset_NoPendingReset(t *testing.T) {
	s, _ := openRecoverResetStore(t)
	assert.ErrorIs(t, s.AbortReset(), ErrResetNotPending)
}

// TestAbortReset_AfterCommit returns ErrResetNotPending when the reset is already committed
// (manifest present but recovery-required is gone).
func TestAbortReset_AfterCommit(t *testing.T) {
	rootDir := t.TempDir()
	s, err := Open(rootDir, makeTestIdentity(), OpenRecoverReset)
	require.NoError(t, err)

	// Plant a manifest but no recovery-required (simulating committed state).
	mfstData := `{"version":1,"curr_uid_validity":200}`
	require.NoError(t, os.WriteFile(resetManifestPath(rootDir), []byte(mfstData), filePerm))

	assert.ErrorIs(t, s.AbortReset(), ErrResetNotPending)
}

// TestAbortReset_RestoresOldData verifies that AbortReset restores staged data and
// removes the manifest while leaving recovery-required in the sentinel.
func TestAbortReset_RestoresOldData(t *testing.T) {
	rootDir := t.TempDir()
	s, err := Open(rootDir, makeTestIdentity(), OpenRecoverReset)
	require.NoError(t, err)
	require.NoError(t, s.SaveUIDValidity(100))
	require.NoError(t, s.SaveRecoveryRequired(100, 200, time.Now()))

	// Simulate pre-commit pending reset: move data file to staging and write manifest.
	stagingPath := resetStagingPath(rootDir)
	require.NoError(t, os.MkdirAll(stagingPath, dirPerm))
	dataPath := dataFilePath(rootDir)
	require.NoError(t, os.Rename(dataPath, filepath.Join(stagingPath, "tlsrpt.json")))
	mfstData := `{"version":1,"curr_uid_validity":200}`
	require.NoError(t, os.WriteFile(resetManifestPath(rootDir), []byte(mfstData), filePerm))

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

	// Set up pre-commit pending reset.
	stagingPath := resetStagingPath(rootDir)
	require.NoError(t, os.MkdirAll(stagingPath, dirPerm))
	require.NoError(t, os.WriteFile(resetManifestPath(rootDir), []byte(`{"version":1,"curr_uid_validity":200}`), filePerm))

	require.NoError(t, s.AbortReset())
	// Second call should return ErrResetNotPending.
	assert.ErrorIs(t, s.AbortReset(), ErrResetNotPending)
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

// TestResetForRecovery_CleanupFailureNoDataPathImpact verifies that if cleanup is
// incomplete after a successful commit (manifest still present, staging dir still exists)
// the store opened via OpenRecoverReset presents an empty data set (empty store consistency).
func TestResetForRecovery_CleanupFailureNoDataPathImpact(t *testing.T) {
	rootDir := t.TempDir()
	s, err := Open(rootDir, makeTestIdentity(), OpenRecoverReset)
	require.NoError(t, err)
	require.NoError(t, s.SaveUIDValidity(100))
	require.NoError(t, s.SaveRecoveryRequired(100, 200, time.Now()))

	// Simulate committed state: update sentinel, but leave manifest and staging dir intact.
	sentinel, _, err := loadSentinel(rootDir)
	require.NoError(t, err)
	uid := uint32(200)
	sentinel.UIDValidity = &uid
	sentinel.RecoveryRequired = nil
	require.NoError(t, saveSentinel(rootDir, sentinel))
	// Leave manifest and staging dir intact to simulate cleanup failure.
	stagingPath := resetStagingPath(rootDir)
	require.NoError(t, os.MkdirAll(stagingPath, dirPerm))
	require.NoError(t, os.WriteFile(resetManifestPath(rootDir), []byte(`{"version":1,"curr_uid_validity":200}`), filePerm))

	// Normal OpenReadWrite is blocked by the manifest (fail-closed).
	_, err = Open(rootDir, makeTestIdentity(), OpenReadWrite)
	assert.ErrorIs(t, err, ErrPendingReset)

	// OpenRecoverReset succeeds and creates empty data files.
	s2, err := Open(rootDir, makeTestIdentity(), OpenRecoverReset)
	require.NoError(t, err)

	// Data path is empty (consistent with a clean reset).
	reports, err := s2.GetAllReports()
	require.NoError(t, err)
	assert.Empty(t, reports)

	// Calling ResetForRecovery again completes the cleanup.
	require.NoError(t, s2.ResetForRecovery(200))

	_, err = os.Stat(resetManifestPath(rootDir))
	assert.True(t, os.IsNotExist(err), "manifest should be gone after re-running ResetForRecovery")
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
