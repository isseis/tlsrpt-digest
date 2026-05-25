package main

import (
	"errors"
	"fmt"
	"log/slog"
	"os"

	"github.com/isseis/tlsrpt-digest/internal/storelock"
)

const rootDirPerm = 0o700

var (
	errRootDirSymlink      = errors.New("store root directory is a symlink")
	errRootDirNotDirectory = errors.New("store root directory path is not a directory")
)

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
// rejects symlinks and non-directories, warns on loose permissions, and creates
// the directory (with parents) if it does not yet exist.
func validateAndEnsureRootDir(rootDir string) error {
	fi, err := os.Lstat(rootDir)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("acquireStoreWriterLock: stat %s: %w", rootDir, err)
		}
		if err := os.MkdirAll(rootDir, rootDirPerm); err != nil {
			return fmt.Errorf("acquireStoreWriterLock: mkdir %s: %w", rootDir, err)
		}
		fi, err = os.Lstat(rootDir)
		if err != nil {
			return fmt.Errorf("acquireStoreWriterLock: stat %s after mkdir: %w", rootDir, err)
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("acquireStoreWriterLock: %s: %w", rootDir, errRootDirSymlink)
		}
		return nil
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("acquireStoreWriterLock: %s: %w", rootDir, errRootDirSymlink)
	}
	if !fi.IsDir() {
		return fmt.Errorf("acquireStoreWriterLock: %s: %w", rootDir, errRootDirNotDirectory)
	}
	if fi.Mode().Perm()&^rootDirPerm != 0 {
		slog.Warn("acquireStoreWriterLock: directory has loose permissions, consider running chmod 0700",
			slog.String("path", rootDir),
			slog.String("current_mode", fmt.Sprintf("%04o", fi.Mode().Perm())))
	}
	return nil
}
