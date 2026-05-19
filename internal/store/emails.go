// Package store provides persistent storage for TLSRPT reports and emails.
package store

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"
)

// errTargetNotRegularFile is returned when the target path for a .eml file
// already exists but is not a regular file (e.g., a directory).
var errTargetNotRegularFile = errors.New("store: target path is not a regular file")

// buildEmailPath returns the storage path for a .eml file.
// The uid is zero-padded to 10 digits. sentAt determines the YYYYMM directory component.
func buildEmailPath(rootDir string, uid, uidValidity uint32, sentAt time.Time) string {
	yyyymm := sentAt.UTC().Format("200601")
	filename := fmt.Sprintf("%010d.eml", uid)
	return filepath.Join(rootDir, "emails", fmt.Sprintf("%d", uidValidity), yyyymm, filename)
}

// SaveEmail implements Store.SaveEmail.
func (s *storeImpl) SaveEmail(uid, uidValidity uint32, sentAt, savedAt time.Time, rawEML []byte) error {
	if s.readOnly {
		return ErrReadOnly
	}

	dateForPath := sentAt
	if dateForPath.IsZero() {
		slog.Warn("SaveEmail: sentAt is zero, falling back to savedAt for directory path",
			slog.Uint64("uid", uint64(uid)),
			slog.Uint64("uidvalidity", uint64(uidValidity)),
		)
		dateForPath = savedAt
	}

	targetPath := buildEmailPath(s.rootDir, uid, uidValidity, dateForPath)

	// Idempotent: if the path already exists as a regular file, return without error.
	// If the path exists but is not a regular file (e.g., a directory), treat it
	// as a write failure.
	if info, err := os.Stat(targetPath); err == nil {
		if info.Mode().IsRegular() {
			return nil
		}
		return fmt.Errorf("%w: %s", errTargetNotRegularFile, targetPath)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("SaveEmail: stat: %w", err)
	}

	// Ensure parent directory exists (mode 0700).
	parentDir := filepath.Dir(targetPath)
	if err := ensureDirExists(parentDir); err != nil {
		return fmt.Errorf("SaveEmail: ensure dir: %w", err)
	}

	// Write atomically (mode 0600).
	return atomicWriteFile(targetPath, rawEML)
}

// SaveEmailMetas implements Store.SaveEmailMetas.
func (s *storeImpl) SaveEmailMetas(metas []EmailMeta) error {
	if s.readOnly {
		return ErrReadOnly
	}

	df, err := s.loadDataFile()
	if err != nil {
		return fmt.Errorf("SaveEmailMetas: load data file: %w", err)
	}

	// Build an index of existing entries for O(1) lookup by {uid, uidvalidity}.
	// Maps to the slice position so we can update in-place.
	existing := make(map[emailKey]int, len(df.Emails))
	for i, entry := range df.Emails {
		existing[emailKey{entry.UID, entry.UIDValidity}] = i
	}

	// For each meta: update existing minimal entries (those with zero SentAt/SavedAt
	// created by SaveReports), or append a new entry if none exists.
	for _, meta := range metas {
		// Normalize zero SentAt: fall back to SavedAt with a WARN log, consistent
		// with the SaveEmail fallback behaviour.
		sentAt := meta.SentAt
		if sentAt.IsZero() {
			slog.Warn("SaveEmailMetas: SentAt is zero, falling back to SavedAt",
				slog.Uint64("uid", uint64(meta.UID)),
				slog.Uint64("uidvalidity", uint64(meta.UIDValidity)),
			)
			sentAt = meta.SavedAt
		}

		key := emailKey{meta.UID, meta.UIDValidity}
		if i, ok := existing[key]; ok {
			// Update SentAt/SavedAt only if the entry is a minimal placeholder
			// (zero values written by SaveReports before SaveEmailMetas ran).
			if df.Emails[i].SentAt.IsZero() {
				df.Emails[i].SentAt = sentAt
			}
			if df.Emails[i].SavedAt.IsZero() {
				df.Emails[i].SavedAt = meta.SavedAt
			}
		} else {
			df.Emails = append(df.Emails, internalEmailIndexEntry{
				UID:         meta.UID,
				UIDValidity: meta.UIDValidity,
				SentAt:      sentAt,
				SavedAt:     meta.SavedAt,
			})
			existing[key] = len(df.Emails) - 1
		}
	}

	return s.saveDataFile(df)
}

// LoadEmails implements Store.LoadEmails.
// TODO: Phase 3 implementation
func (s *storeImpl) LoadEmails() ([]LoadedEmail, error) {
	return nil, errNotImplemented
}

// DeleteEmailsBefore implements Store.DeleteEmailsBefore.
// TODO: Phase 3 implementation
func (s *storeImpl) DeleteEmailsBefore(_, _ time.Time) (deleted int, err error) {
	if s.readOnly {
		return 0, ErrReadOnly
	}
	return 0, errNotImplemented
}
