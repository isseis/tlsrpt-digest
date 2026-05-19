// Package store provides persistent storage for TLSRPT reports and emails.
package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const sentinelFilename = ".tlsrpt-digest-meta.json"

// sentinelPath returns the path to the sentinel file within rootDir.
func sentinelPath(rootDir string) string {
	return filepath.Join(rootDir, sentinelFilename)
}

// loadSentinel loads and parses the sentinel file from rootDir.
// Returns (sentinel, found=true, err=nil) on success.
// Returns (nil, found=false, err=nil) if the file doesn't exist.
// Returns (nil, found=false, err!=nil) on other errors.
func loadSentinel(rootDir string) (*internalSentinelFile, bool, error) {
	path := sentinelPath(rootDir)
	// G304: The path is constructed from rootDir, which is an application-controlled
	// directory path set during store initialization — not user-supplied input.
	data, err := os.ReadFile(path) //nolint:gosec
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("loadSentinel: read sentinel: %w", err)
	}

	var sentinel internalSentinelFile
	if err := json.Unmarshal(data, &sentinel); err != nil {
		return nil, false, fmt.Errorf("loadSentinel: unmarshal sentinel: %w", err)
	}

	if sentinel.FormatVersion != SentinelFormatVersion {
		return nil, false, &ErrUnsupportedSchemaVersion{
			File:    path,
			Version: sentinel.FormatVersion,
		}
	}

	return &sentinel, true, nil
}

// saveSentinel writes the sentinel file to rootDir atomically.
// The file is created with mode 0600.
func saveSentinel(rootDir string, sentinel *internalSentinelFile) error {
	data, err := json.Marshal(sentinel)
	if err != nil {
		return fmt.Errorf("saveSentinel: marshal sentinel: %w", err)
	}

	path := sentinelPath(rootDir)
	if err := atomicWriteFile(path, data); err != nil {
		return fmt.Errorf("saveSentinel: atomic write: %w", err)
	}

	return nil
}

// initSentinel creates and saves a new sentinel file with the given identity.
func initSentinel(rootDir string, identity IMAPIdentity) (*internalSentinelFile, error) {
	sentinel := &internalSentinelFile{
		FormatVersion: SentinelFormatVersion,
		IMAPHost:      identity.Host,
		IMAPPort:      identity.Port,
		IMAPMailbox:   identity.Mailbox,
		InitializedAt: time.Now().UTC(),
		// UIDValidity and RecoveryRequired are omitted initially (nil)
	}

	if err := saveSentinel(rootDir, sentinel); err != nil {
		return nil, err
	}

	return sentinel, nil
}

// verifySentinelIdentity checks if the sentinel's identity matches the expected identity.
// Returns nil if they match, or *ErrStoreIdentityMismatch if they don't.
func verifySentinelIdentity(rootDir string, sentinel *internalSentinelFile, expected IMAPIdentity) error {
	if sentinel.IMAPHost != expected.Host ||
		sentinel.IMAPPort != expected.Port ||
		sentinel.IMAPMailbox != expected.Mailbox {
		return &ErrStoreIdentityMismatch{
			RootDir:         rootDir,
			ExpectedHost:    expected.Host,
			ExpectedPort:    expected.Port,
			ExpectedMailbox: expected.Mailbox,
			ActualHost:      sentinel.IMAPHost,
			ActualPort:      sentinel.IMAPPort,
			ActualMailbox:   sentinel.IMAPMailbox,
		}
	}
	return nil
}
