//go:build test

package storelock

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLockPath(t *testing.T) {
	got := LockPath("/tmp/store")
	assert.Equal(t, filepath.Join("/tmp/store", ".tlsrpt-digest-store.lock"), got)
}

func TestAcquire_SecondAcquireFails(t *testing.T) {
	lockPath := LockPath(t.TempDir())

	first, err := Acquire(lockPath)
	require.NoError(t, err)
	defer func() { require.NoError(t, first.Close()) }()

	second, err := Acquire(lockPath)
	assert.Nil(t, second)
	assert.ErrorIs(t, err, ErrLockHeld)
}

func TestAcquire_ReacquireAfterClose(t *testing.T) {
	lockPath := LockPath(t.TempDir())

	first, err := Acquire(lockPath)
	require.NoError(t, err)
	require.NoError(t, first.Close())

	second, err := Acquire(lockPath)
	require.NoError(t, err)
	require.NoError(t, second.Close())
}

func TestLockHandleClose_Idempotent(t *testing.T) {
	lockPath := LockPath(t.TempDir())

	h, err := Acquire(lockPath)
	require.NoError(t, err)
	require.NoError(t, h.Close())
	require.NoError(t, h.Close())
}

func TestAcquire_RequiresExistingParent(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), "missing", ".tlsrpt-digest-store.lock")

	h, err := Acquire(lockPath)
	assert.Nil(t, h)
	assert.Error(t, err)
	assert.False(t, errors.Is(err, ErrLockHeld))
}
