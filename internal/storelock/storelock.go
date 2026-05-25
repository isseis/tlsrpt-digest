// Package storelock provides the process-wide exclusive lock for store write operations.
//
// Writer subcommands (fetch, gc, reprocess, recover) must acquire an exclusive lock
// via Acquire before calling store.Open with OpenReadWrite or OpenRecoverReset, and
// hold it until the subcommand's processing is complete.
package storelock

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

const lockFilePerm = 0o600

// ErrLockHeld is returned by Acquire when another process already holds the lock.
var ErrLockHeld = errors.New("store lock is already held")

// LockHandle represents a held store writer lock. Call Close to release.
type LockHandle interface {
	Close() error
}

// LockPath returns the path of the store writer lock file for rootDir.
func LockPath(rootDir string) string {
	return filepath.Join(rootDir, ".tlsrpt-digest-store.lock")
}

// Acquire acquires an exclusive, non-blocking flock on lockPath.
// Returns ErrLockHeld if another process already holds the lock.
// The parent directory of lockPath must already exist.
func Acquire(lockPath string) (LockHandle, error) {
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, lockFilePerm) //nolint:gosec
	if err != nil {
		return nil, fmt.Errorf("storelock.Acquire: open lock file: %w", err)
	}
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil { //nolint:gosec
		_ = f.Close()
		if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
			return nil, ErrLockHeld
		}
		return nil, fmt.Errorf("storelock.Acquire: acquire lock: %w", err)
	}
	return &flockHandle{file: f}, nil
}

type flockHandle struct {
	file *os.File
}

func (h *flockHandle) Close() error {
	if h == nil || h.file == nil {
		return nil
	}
	f := h.file
	h.file = nil
	if err := unix.Flock(int(f.Fd()), unix.LOCK_UN); err != nil { //nolint:gosec
		_ = f.Close()
		return fmt.Errorf("storelock.LockHandle.Close: release lock: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("storelock.LockHandle.Close: close lock file: %w", err)
	}
	return nil
}
