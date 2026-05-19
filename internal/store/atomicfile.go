// Package store provides persistent storage for TLSRPT reports and emails.
package store

import (
	"fmt"
	"os"
	"path/filepath"
)

// atomicWriteFile writes data to a file atomically by writing to a temporary file
// and then renaming it to the target path. The file is created with mode 0600.
// The operation is atomic within the same filesystem (guaranteed by rename).
func atomicWriteFile(targetPath string, data []byte) error {
	dir := filepath.Dir(targetPath)

	// Create a temporary file in the same directory as the target
	tmpFile, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return fmt.Errorf("atomicWriteFile: create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer func() {
		_ = os.Remove(tmpPath) // Best effort cleanup; ignore error
	}()

	// Set file permissions to 0600 before writing any data
	if err := os.Chmod(tmpPath, filePerm); err != nil {
		_ = tmpFile.Close()
		return fmt.Errorf("atomicWriteFile: chmod temp file: %w", err)
	}

	// Write data to temporary file
	if _, err := tmpFile.Write(data); err != nil {
		_ = tmpFile.Close()
		return fmt.Errorf("atomicWriteFile: write temp file: %w", err)
	}

	// Sync to ensure data is written to disk
	if err := tmpFile.Sync(); err != nil {
		_ = tmpFile.Close()
		return fmt.Errorf("atomicWriteFile: sync temp file: %w", err)
	}

	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("atomicWriteFile: close temp file: %w", err)
	}

	// Atomically rename temp file to target path
	// On POSIX systems, rename is atomic within the same filesystem.
	if err := os.Rename(tmpPath, targetPath); err != nil {
		return fmt.Errorf("atomicWriteFile: rename temp file to %s: %w", targetPath, err)
	}

	return nil
}
