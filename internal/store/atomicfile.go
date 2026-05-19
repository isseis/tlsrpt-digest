// Package store provides persistent storage for TLSRPT reports and emails.
package store

import (
	"log/slog"
	"os"
	"path/filepath"
)

// atomicWriteFile writes data to a file atomically by writing to a temporary file
// and then renaming it to the target path. The file is created with mode 0600.
// The operation is atomic within the same filesystem (guaranteed by rename).
//
// After the rename succeeds the parent directory is fsynced on a best-effort
// basis. If the fsync fails the function still returns nil because the write
// itself has already been applied; only the crash-durability guarantee is
// weakened. A warning is logged in that case.
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

	// Set file permissions to 0600 using the open file descriptor to avoid a
	// TOCTOU race between path-based Chmod and the open tmpFile.
	if err := tmpFile.Chmod(filePerm); err != nil {
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

	// Fsync the parent directory to make the rename durable across a crash.
	// This is best-effort: the file is already in place after the rename, so
	// failure here does not mean the write was lost — only that crash-durability
	// is not guaranteed. We log a warning and return nil so callers are not
	// misled into thinking the write failed.
	// G304: dir is derived from targetPath (an application-controlled path),
	// not from user input.
	dirFd, err := os.Open(dir) //nolint:gosec
	if err != nil {
		slog.Warn("atomicWriteFile: could not open parent dir for fsync; crash durability not guaranteed",
			slog.String("target", targetPath),
			slog.Any("error", err),
		)
		return nil
	}
	if syncErr := dirFd.Sync(); syncErr != nil {
		_ = dirFd.Close()
		slog.Warn("atomicWriteFile: fsync parent dir failed; crash durability not guaranteed",
			slog.String("target", targetPath),
			slog.Any("error", syncErr),
		)
		return nil
	}
	_ = dirFd.Close() // Best effort; read-only fd unlikely to fail on close

	return nil
}
