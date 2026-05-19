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
	"syscall"
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

// ctimeOf returns the inode change time (ctime) of the file at path.
// Falls back to time.Now() on error.
func ctimeOf(path string) time.Time {
	var stat syscall.Stat_t
	if err := syscall.Stat(path, &stat); err != nil {
		return time.Now().UTC()
	}
	return time.Unix(stat.Ctim.Sec, stat.Ctim.Nsec).UTC()
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

		savedAt := ctimeOf(path)
		sentAt := savedAt
		if dateStr := msg.Header.Get("Date"); dateStr != "" {
			if t, dateErr := mail.ParseDate(dateStr); dateErr == nil {
				sentAt = t.UTC()
			} else {
				slog.Warn("LoadEmails: failed to parse Date header, falling back to ctime",
					slog.String("path", relPath),
					slog.Any("error", dateErr),
				)
			}
		} else {
			slog.Warn("LoadEmails: Date header missing, falling back to ctime",
				slog.String("path", relPath),
			)
		}

		result = append(result, LoadedEmail{
			Message:     msg,
			UID:         uid,
			UIDValidity: uidValidity,
			SentAt:      sentAt,
			SavedAt:     savedAt,
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
func (s *storeImpl) DeleteEmailsBefore(reportCutoff, savedAtCutoff time.Time) (deleted int, err error) {
	if s.readOnly {
		return 0, ErrReadOnly
	}

	df, loadErr := s.loadDataFile()
	if loadErr != nil {
		return 0, fmt.Errorf("DeleteEmailsBefore: load data file: %w", loadErr)
	}

	var deleteErrs []error
	surviving := df.Emails[:0:0] // preserve nil-ness of backing array by starting fresh

	for _, entry := range df.Emails {
		shouldDelete := false

		// Normal deletion: report_end_date != null && report_end_date < reportCutoff
		if entry.ReportEndDate != nil && entry.ReportEndDate.Before(reportCutoff) {
			shouldDelete = true
		}

		// Forced deletion: savedAtCutoff != zero && saved_at < savedAtCutoff
		if !savedAtCutoff.IsZero() && entry.SavedAt.Before(savedAtCutoff) {
			shouldDelete = true
		}

		if !shouldDelete {
			surviving = append(surviving, entry)
			continue
		}

		// Delete the .eml file first (file deletion before index update per AC-30).
		emlPath := buildEmailPath(s.rootDir, entry.UID, entry.UIDValidity, entry.SentAt)
		if rmErr := os.Remove(emlPath); rmErr != nil && !os.IsNotExist(rmErr) {
			// File I/O error: keep index entry, aggregate error, continue (AC-32a).
			deleteErrs = append(deleteErrs, &ErrDeleteEmailFailed{
				Path:        emlPath,
				UID:         entry.UID,
				UIDValidity: entry.UIDValidity,
				SavedAt:     entry.SavedAt,
				Err:         rmErr,
			})
			surviving = append(surviving, entry)
			continue
		}
		// File deleted (or already absent — AC-31); remove the index entry.
		deleted++
	}

	// Write updated index atomically.
	df.Emails = surviving
	if saveErr := s.saveDataFile(df); saveErr != nil {
		return 0, fmt.Errorf("DeleteEmailsBefore: save data file: %w", saveErr)
	}

	// AC-32b: directory sweep for orphaned .eml files when savedAtCutoff is set.
	if !savedAtCutoff.IsZero() {
		s.sweepOrphanedEmailDirs(savedAtCutoff)
	}

	return deleted, errors.Join(deleteErrs...)
}

// sweepOrphanedEmailDirs removes {uidvalidity}/{YYYYMM} directories whose YYYYMM
// is strictly before savedAtCutoff's year-month. Errors are logged and ignored.
func (s *storeImpl) sweepOrphanedEmailDirs(savedAtCutoff time.Time) {
	cutoffYYYYMM := savedAtCutoff.UTC().Format("200601")
	emailsDir := s.emailsDirPath

	// Walk one level deep for uidvalidity dirs, then one more for YYYYMM dirs.
	uvEntries, err := os.ReadDir(emailsDir)
	if err != nil {
		slog.Warn("sweepOrphanedEmailDirs: read emails dir failed", slog.Any("error", err))
		return
	}

	for _, uvEntry := range uvEntries {
		if !uvEntry.IsDir() {
			continue
		}
		uvDir := filepath.Join(emailsDir, uvEntry.Name())
		mmEntries, err := os.ReadDir(uvDir)
		if err != nil {
			slog.Warn("sweepOrphanedEmailDirs: read uidvalidity dir failed",
				slog.String("dir", uvDir), slog.Any("error", err))
			continue
		}
		for _, mmEntry := range mmEntries {
			if !mmEntry.IsDir() {
				continue
			}
			yyyymm := mmEntry.Name()
			if yyyymm < cutoffYYYYMM {
				dirToRemove := filepath.Join(uvDir, yyyymm)
				if rmErr := os.RemoveAll(dirToRemove); rmErr != nil {
					slog.Warn("sweepOrphanedEmailDirs: remove dir failed",
						slog.String("dir", dirToRemove), slog.Any("error", rmErr))
				}
			}
		}
	}
}
