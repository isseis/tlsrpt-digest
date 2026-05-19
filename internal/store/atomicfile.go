// Package store provides persistent storage for TLSRPT reports and emails.
package store

import (
	"os"
	"path/filepath"
)

// atomicWriteFile writes data to a file atomically by writing to a temporary file
// and then renaming it to the target path. The file is created with mode 0600.
// The operation is atomic within the same filesystem (guaranteed by rename).
// The parent directory is fsynced after the rename to ensure durability on crash.
func atomicWriteFile(targetPath string, data []byte) error {
	dir := filepath.Dir(targetPath)

	// Create a temporary file in the same directory as the target
	tmpFile, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return &ErrAtomicWriteFailed{File: targetPath, Op: "create_temp", Err: err}
	}
	tmpPath := tmpFile.Name()
	defer func() {
		_ = os.Remove(tmpPath) // Best effort cleanup; ignore error
	}()

	// Set file permissions to 0600 before writing any data
	if err := os.Chmod(tmpPath, filePerm); err != nil {
		_ = tmpFile.Close()
		return &ErrAtomicWriteFailed{File: targetPath, Op: "chmod", Err: err}
	}

	// Write data to temporary file
	if _, err := tmpFile.Write(data); err != nil {
		_ = tmpFile.Close()
		return &ErrAtomicWriteFailed{File: targetPath, Op: "write", Err: err}
	}

	// Sync to ensure data is written to disk
	if err := tmpFile.Sync(); err != nil {
		_ = tmpFile.Close()
		return &ErrAtomicWriteFailed{File: targetPath, Op: "sync", Err: err}
	}

	if err := tmpFile.Close(); err != nil {
		return &ErrAtomicWriteFailed{File: targetPath, Op: "close", Err: err}
	}

	// Atomically rename temp file to target path
	// On POSIX systems, rename is atomic within the same filesystem.
	if err := os.Rename(tmpPath, targetPath); err != nil {
		return &ErrAtomicWriteFailed{File: targetPath, Op: "rename", Err: err}
	}

	// Fsync the parent directory so the rename survives a crash.
	// Without this, the directory entry update may be lost even if the file
	// contents were synced (rename(2) atomicity does not imply durability).
	// G304: dir is derived from targetPath (an application-controlled path),
	// not from user input.
	dirFd, err := os.Open(dir) //nolint:gosec
	if err != nil {
		return &ErrAtomicWriteFailed{File: targetPath, Op: "open_dir_for_fsync", Err: err}
	}
	if syncErr := dirFd.Sync(); syncErr != nil {
		_ = dirFd.Close()
		return &ErrAtomicWriteFailed{File: targetPath, Op: "fsync_dir", Err: syncErr}
	}
	if err := dirFd.Close(); err != nil {
		return &ErrAtomicWriteFailed{File: targetPath, Op: "close_dir", Err: err}
	}

	return nil
}
