//go:build test

package store

import (
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
