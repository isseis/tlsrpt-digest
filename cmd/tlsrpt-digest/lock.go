package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/isseis/tlsrpt-digest/internal/storelock"
)

const (
	rootDirPerm      = 0o700
	rootDirGroupPerm = 0o750
)

var (
	errRootDirSymlink      = errors.New("store root directory is a symlink")
	errRootDirNotDirectory = errors.New("store root directory path is not a directory")
	errRootDirPermission   = errors.New("store root directory lacks required permissions")
)

type LockHandle = storelock.LockHandle

func AcquireExclusive(lockPath string) (LockHandle, error) {
	return storelock.Acquire(lockPath)
}

// acquireStoreWriterLock validates rootDir, creates it if absent, and acquires
// the exclusive store writer lock. Must be called before store.Open with
// OpenReadWrite or OpenRecoverReset, and the handle held until processing is complete.
// Returns storelock.ErrLockHeld if another writer process is already running.
func acquireStoreWriterLock(rootDir string) (storelock.LockHandle, error) {
	if err := validateAndEnsureRootDir(rootDir); err != nil {
		return nil, err
	}
	return storelock.Acquire(storelock.LockPath(rootDir))
}

// validateAndEnsureRootDir checks that rootDir is safe to use as a store root:
// rejects symlinks, non-directories, and unexpected permissions, and creates the
// directory (with parents) if it does not yet exist.
func validateAndEnsureRootDir(rootDir string) error {
	fi, err := os.Lstat(rootDir)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("validateAndEnsureRootDir: stat %s: %w", rootDir, err)
		}
		if err := os.MkdirAll(rootDir, rootDirPerm); err != nil {
			return fmt.Errorf("validateAndEnsureRootDir: mkdir %s: %w", rootDir, err)
		}
		fi, err = os.Lstat(rootDir)
		if err != nil {
			return fmt.Errorf("validateAndEnsureRootDir: stat %s after mkdir: %w", rootDir, err)
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("validateAndEnsureRootDir: %s: %w", rootDir, errRootDirSymlink)
		}
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("validateAndEnsureRootDir: %s: %w", rootDir, errRootDirSymlink)
	}
	if !fi.IsDir() {
		return fmt.Errorf("validateAndEnsureRootDir: %s: %w", rootDir, errRootDirNotDirectory)
	}
	perm := fi.Mode().Perm()
	if perm != rootDirPerm && perm != rootDirGroupPerm {
		return fmt.Errorf("validateAndEnsureRootDir: %s has permissions %04o, want 0700 or 0750: %w", rootDir, perm, errRootDirPermission)
	}
	return nil
}
