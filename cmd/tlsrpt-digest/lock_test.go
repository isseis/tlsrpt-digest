//go:build test

package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/isseis/tlsrpt-digest/internal/storelock"
)

func TestAcquireExclusive_SecondAcquireFails(t *testing.T) {
	lockPath := storelock.LockPath(t.TempDir())

	first, err := AcquireExclusive(lockPath)
	require.NoError(t, err)
	defer func() { require.NoError(t, first.Close()) }()

	second, err := AcquireExclusive(lockPath)
	assert.Nil(t, second)
	assert.ErrorIs(t, err, storelock.ErrLockHeld)
}

func TestAcquireExclusive_ReacquireAfterClose(t *testing.T) {
	lockPath := storelock.LockPath(t.TempDir())

	first, err := AcquireExclusive(lockPath)
	require.NoError(t, err)
	require.NoError(t, first.Close())

	second, err := AcquireExclusive(lockPath)
	require.NoError(t, err)
	require.NoError(t, second.Close())
}

func TestAcquireExclusive_RequiresExistingParent(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), "missing", ".tlsrpt-digest-store.lock")

	h, err := AcquireExclusive(lockPath)
	assert.Nil(t, h)
	assert.Error(t, err)
	assert.False(t, errors.Is(err, storelock.ErrLockHeld))
}

func TestAcquireStoreWriterLock_CreatesRootAndHoldsLock(t *testing.T) {
	rootDir := filepath.Join(t.TempDir(), "store")

	h, err := acquireStoreWriterLock(rootDir)
	require.NoError(t, err)
	defer func() { require.NoError(t, h.Close()) }()

	info, err := os.Stat(rootDir)
	require.NoError(t, err)
	assert.True(t, info.IsDir())

	second, err := AcquireExclusive(storelock.LockPath(rootDir))
	assert.Nil(t, second)
	assert.ErrorIs(t, err, storelock.ErrLockHeld)
}

func TestAcquireStoreWriterLock_RejectsNonDirectory(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "notadir")
	require.NoError(t, os.WriteFile(file, []byte{}, 0o600))

	h, err := acquireStoreWriterLock(file)
	assert.Nil(t, h)
	assert.ErrorIs(t, err, errRootDirNotDirectory)
}

func TestAcquireStoreWriterLock_Allows0750(t *testing.T) {
	rootDir := filepath.Join(t.TempDir(), "store")
	require.NoError(t, os.Mkdir(rootDir, 0o750))

	h, err := acquireStoreWriterLock(rootDir)
	require.NoError(t, err)
	require.NoError(t, h.Close())
}

func TestAcquireStoreWriterLock_RejectsUnsupportedPermissions(t *testing.T) {
	rootDir := filepath.Join(t.TempDir(), "store")
	require.NoError(t, os.Mkdir(rootDir, 0o755)) // #nosec G301 -- intentionally unsupported permission fixture.

	h, err := acquireStoreWriterLock(rootDir)

	assert.Nil(t, h)
	assert.ErrorIs(t, err, errRootDirPermission)
}
