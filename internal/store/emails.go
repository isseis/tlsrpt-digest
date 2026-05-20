// Package store provides persistent storage for TLSRPT reports and emails.
package store

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/mail"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// errTargetNotRegularFile is returned when the target path for a .eml file
// already exists but is not a regular file (e.g., a directory).
var errTargetNotRegularFile = errors.New("store: target path is not a regular file")

// ErrZeroInternalDate is returned when an InternalDate (IMAP INTERNALDATE) value is zero.
var ErrZeroInternalDate = errors.New("store: InternalDate must not be zero")

// buildEmailPath returns the storage path for a .eml file.
// The uid is zero-padded to 10 digits. internalDate determines the YYYYMM directory component.
func buildEmailPath(rootDir string, uid, uidValidity uint32, internalDate time.Time) string {
	yyyymm := internalDate.UTC().Format("200601")
	filename := fmt.Sprintf("%010d.eml", uid)
	return filepath.Join(rootDir, "emails", fmt.Sprintf("%d", uidValidity), yyyymm, filename)
}

// SaveEmail implements Store.SaveEmail.
func (s *storeImpl) SaveEmail(uid, uidValidity uint32, internalDate time.Time, rawEML []byte) error {
	if s.readOnly {
		return ErrReadOnly
	}

	if internalDate.IsZero() {
		return ErrZeroInternalDate
	}

	targetPath := buildEmailPath(s.rootDir, uid, uidValidity, internalDate)

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

	// Build a set of existing {uid, uidvalidity} keys for O(1) existence checks.
	existing := make(map[emailKey]struct{}, len(df.Emails))
	for _, entry := range df.Emails {
		existing[emailKey{entry.UID, entry.UIDValidity}] = struct{}{}
	}

	// For each meta: append a new entry if none exists; skip if already present (idempotent).
	for _, meta := range metas {
		if meta.InternalDate.IsZero() {
			return ErrZeroInternalDate
		}
		key := emailKey{meta.UID, meta.UIDValidity}
		if _, ok := existing[key]; ok {
			continue
		}
		df.Emails = append(df.Emails, internalEmailIndexEntry(meta))
		existing[key] = struct{}{}
	}

	return s.saveDataFile(df)
}

// LoadEmails implements Store.LoadEmails.
func (s *storeImpl) LoadEmails() ([]LoadedEmail, error) {
	emailsDir := s.emailsDirPath
	if _, err := os.Stat(emailsDir); os.IsNotExist(err) {
		return []LoadedEmail{}, nil
	}

	var result []LoadedEmail
	var errs []error

	walkErr := filepath.WalkDir(emailsDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			errs = append(errs, &ErrLoadEmailFailed{Path: path, Err: err})
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if filepath.Ext(path) != ".eml" {
			return nil
		}

		relPath, relErr := filepath.Rel(emailsDir, path)
		if relErr != nil {
			errs = append(errs, &ErrLoadEmailFailed{Path: path, Err: relErr})
			return nil
		}

		// Parse {uidvalidity}/{YYYYMM}/{padded_uid}.eml — exactly 3 components.
		const emailPathComponents = 3
		parts := strings.Split(relPath, string(filepath.Separator))
		if len(parts) != emailPathComponents {
			errs = append(errs, &ErrLoadEmailFailed{Path: path, Err: &ErrInvalidEmailPath{Path: relPath}})
			return nil
		}

		uidValidityU64, parseErr := strconv.ParseUint(parts[0], 10, 32)
		if parseErr != nil {
			errs = append(errs, &ErrLoadEmailFailed{Path: path, Err: &ErrInvalidEmailPath{Path: relPath}})
			return nil
		}
		uidValidity := uint32(uidValidityU64)

		uidStr := strings.TrimSuffix(parts[2], ".eml")
		uidU64, parseErr := strconv.ParseUint(uidStr, 10, 32)
		if parseErr != nil {
			errs = append(errs, &ErrLoadEmailFailed{Path: path, Err: &ErrInvalidEmailPath{Path: relPath}})
			return nil
		}
		uid := uint32(uidU64)

		data, readErr := os.ReadFile(path) //nolint:gosec
		if readErr != nil {
			errs = append(errs, &ErrLoadEmailFailed{Path: path, Err: readErr})
			return nil
		}

		msg, parseErr := mail.ReadMessage(bytes.NewReader(data))
		if parseErr != nil {
			errs = append(errs, &ErrLoadEmailFailed{Path: path, Err: parseErr})
			return nil
		}

		result = append(result, LoadedEmail{
			Message:     msg,
			UID:         uid,
			UIDValidity: uidValidity,
			Path:        relPath,
		})
		return nil
	})
	if walkErr != nil {
		errs = append(errs, walkErr)
	}

	if result == nil {
		result = []LoadedEmail{}
	}
	return result, errors.Join(errs...)
}

// DeleteEmailsBefore implements Store.DeleteEmailsBefore.
func (s *storeImpl) DeleteEmailsBefore(cutoff time.Time) (deleted int, err error) {
	if s.readOnly {
		return 0, ErrReadOnly
	}

	if cutoff.IsZero() {
		return 0, nil
	}

	df, loadErr := s.loadDataFile()
	if loadErr != nil {
		return 0, fmt.Errorf("DeleteEmailsBefore: load data file: %w", loadErr)
	}

	var deleteErrs []error
	// Track GC'd entries for post-GC directory cleanup.
	var gcEntries []internalEmailIndexEntry
	surviving := df.Emails[:0] // reuse backing array; in-place filtering is safe (write index <= read index)

	for _, entry := range df.Emails {
		if !entry.InternalDate.Before(cutoff) {
			surviving = append(surviving, entry)
			continue
		}

		// Delete the .eml file first (file deletion before index update).
		emlPath := buildEmailPath(s.rootDir, entry.UID, entry.UIDValidity, entry.InternalDate)
		if rmErr := os.Remove(emlPath); rmErr != nil && !os.IsNotExist(rmErr) {
			// File I/O error: keep index entry, aggregate error, continue.
			deleteErrs = append(deleteErrs, &ErrDeleteEmailFailed{
				Path:        emlPath,
				UID:         entry.UID,
				UIDValidity: entry.UIDValidity,
				Err:         rmErr,
			})
			surviving = append(surviving, entry)
			continue
		}
		// File deleted (or already absent); remove the index entry.
		deleted++
		gcEntries = append(gcEntries, entry)
	}

	// Skip the write when no index entries were removed.
	if deleted == 0 {
		return 0, errors.Join(deleteErrs...)
	}

	// Write updated index atomically.
	df.Emails = surviving
	if saveErr := s.saveDataFile(df); saveErr != nil {
		// Physical file deletions already succeeded; return the real count so the
		// caller knows the on-disk state.
		deleteErrs = append(deleteErrs, fmt.Errorf("DeleteEmailsBefore: save data file: %w", saveErr))
		return deleted, errors.Join(deleteErrs...)
	}

	// After a successful index update, remove empty {uidvalidity}/{YYYYMM} dirs for GC'd
	// entries, then remove empty {uidvalidity} dirs. Failures are WARN only.
	s.cleanupEmptyDirs(gcEntries)

	return deleted, errors.Join(deleteErrs...)
}

// cleanupEmptyDirs removes empty {uidvalidity}/{YYYYMM} and {uidvalidity} directories
// for the given GC'd entries. Failures are logged as WARN and never returned as errors.
func (s *storeImpl) cleanupEmptyDirs(gcEntries []internalEmailIndexEntry) {
	emailsDir := s.emailsDirPath

	// Collect unique {uidvalidity}/{YYYYMM} dirs and {uidvalidity} dirs.
	type mmKey struct{ uv, mm string }
	mmDirs := make(map[mmKey]struct{}, len(gcEntries))
	uvDirs := make(map[string]struct{}, len(gcEntries))
	for _, entry := range gcEntries {
		uv := fmt.Sprintf("%d", entry.UIDValidity)
		mm := entry.InternalDate.UTC().Format("200601")
		mmDirs[mmKey{uv, mm}] = struct{}{}
		uvDirs[uv] = struct{}{}
	}

	// Remove {uidvalidity}/{YYYYMM} dirs that are now empty.
	for k := range mmDirs {
		dir := filepath.Join(emailsDir, k.uv, k.mm)
		entries, rdErr := os.ReadDir(dir)
		if rdErr != nil {
			if !os.IsNotExist(rdErr) {
				slog.Warn("DeleteEmailsBefore: read YYYYMM dir failed",
					slog.String("dir", dir), slog.Any("error", rdErr))
			}
			continue
		}
		if len(entries) == 0 {
			if rmErr := os.Remove(dir); rmErr != nil && !os.IsNotExist(rmErr) {
				slog.Warn("DeleteEmailsBefore: remove YYYYMM dir failed",
					slog.String("dir", dir), slog.Any("error", rmErr))
			}
		}
	}

	// Remove {uidvalidity} dirs that are now empty.
	for uv := range uvDirs {
		dir := filepath.Join(emailsDir, uv)
		entries, rdErr := os.ReadDir(dir)
		if rdErr != nil {
			if !os.IsNotExist(rdErr) {
				slog.Warn("DeleteEmailsBefore: read uidvalidity dir failed",
					slog.String("dir", dir), slog.Any("error", rdErr))
			}
			continue
		}
		if len(entries) == 0 {
			if rmErr := os.Remove(dir); rmErr != nil && !os.IsNotExist(rmErr) {
				slog.Warn("DeleteEmailsBefore: remove uidvalidity dir failed",
					slog.String("dir", dir), slog.Any("error", rmErr))
			}
		}
	}
}
