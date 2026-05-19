// Package store provides persistent storage for TLSRPT reports and emails.
package store

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
)

// Error types
var (
	errPathIsNotDir = errors.New("path exists but is not a directory")
)

// File permissions constants
const (
	// dirPerm is the permission mode for directories (0700 = owner RWX only)
	dirPerm = 0o700
	// filePerm is the permission mode for files (0600 = owner RW only)
	filePerm = 0o600
)

// ensureDirExists creates a directory with mode 0700 if it doesn't exist.
// If the directory already exists with less restrictive permissions,
// it logs a warning but does not attempt to modify the permissions.
func ensureDirExists(dirPath string) error {
	info, err := os.Stat(dirPath)
	if err == nil {
		// Directory exists; check permissions
		if !info.IsDir() {
			return fmt.Errorf("ensureDirExists: %w", errPathIsNotDir)
		}
		// Warn only if bits outside the allowed 0700 mask are set (i.e., looser permissions).
		// Stricter permissions (e.g., 0500) are silently accepted.
		if info.Mode().Perm()&^dirPerm != 0 {
			slog.Warn("ensureDirExists: directory has loose permissions, consider running chmod 0700",
				slog.String("path", dirPath),
				slog.String("current_mode", fmt.Sprintf("%04o", info.Mode().Perm())),
			)
		}
		return nil
	}

	if !os.IsNotExist(err) {
		return fmt.Errorf("ensureDirExists: stat failed: %w", err)
	}

	// Create directory with mode 0700
	if err := os.MkdirAll(dirPath, dirPerm); err != nil {
		return fmt.Errorf("ensureDirExists: mkdir failed: %w", err)
	}

	return nil
}

// checkFilePermissions logs a warning if a file has less restrictive permissions than 0600.
// Stat failures (e.g., transient I/O errors) are also logged rather than returned so that
// callers can treat permission checks as best-effort operations.
func checkFilePermissions(filePath string) {
	info, err := os.Stat(filePath)
	if err != nil {
		slog.Warn("checkFilePermissions: stat failed",
			slog.String("path", filePath),
			slog.Any("error", err),
		)
		return
	}

	// Warn only if bits outside the allowed 0600 mask are set (i.e., looser permissions).
	// Stricter permissions (e.g., 0400) are silently accepted.
	if info.Mode().Perm()&^filePerm != 0 {
		slog.Warn("checkFilePermissions: file has loose permissions, consider running chmod 0600",
			slog.String("path", filePath),
			slog.String("current_mode", fmt.Sprintf("%04o", info.Mode().Perm())),
		)
	}
}
