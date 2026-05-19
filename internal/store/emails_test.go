//go:build test

package store

import (
	"log/slog"
	"net/mail"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// makeTestEML returns a minimal RFC 2822 email body with the given Date header value.
// dateHeader should be in RFC 2822 format (e.g., "Mon, 01 Jun 2025 00:00:00 +0000").
// Pass an empty string to omit the Date header.
func makeTestEML(dateHeader string) []byte {
	if dateHeader != "" {
		return []byte("From: sender@example.com\r\nTo: recipient@example.com\r\nDate: " + dateHeader + "\r\nSubject: Test\r\n\r\nBody.\r\n")
	}
	return []byte("From: sender@example.com\r\nTo: recipient@example.com\r\nSubject: Test\r\n\r\nBody.\r\n")
}

// TestSaveEmail_CreatesFile verifies that SaveEmail creates a parseable .eml file
// at the correct path {root_dir}/emails/{uidvalidity}/{YYYYMM}/{uid}.eml.
func TestSaveEmail_CreatesFile(t *testing.T) {
	s, rootDir := openTestStore(t)

	uid := uint32(123)
	uidValidity := uint32(99999)
	sentAt := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	savedAt := time.Now().UTC()
	rawEML := makeTestEML("Mon, 01 Jun 2025 00:00:00 +0000")

	require.NoError(t, s.SaveEmail(uid, uidValidity, sentAt, savedAt, rawEML))

	expectedPath := filepath.Join(rootDir, "emails",
		"99999", "202506", "0000000123.eml")
	require.FileExists(t, expectedPath)

	// Verify the file is parseable as an RFC 2822 message.
	// G304: expectedPath is constructed from t.TempDir(), a safe test path.
	f, err := os.Open(expectedPath) //nolint:gosec
	require.NoError(t, err)
	defer func() { _ = f.Close() }()
	_, err = mail.ReadMessage(f)
	require.NoError(t, err, "stored .eml should be parseable by mail.ReadMessage")
}

// TestSaveEmail_FileName verifies that the file name is a 10-digit zero-padded UID.
func TestSaveEmail_FileName(t *testing.T) {
	s, rootDir := openTestStore(t)

	sentAt := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	savedAt := sentAt

	require.NoError(t, s.SaveEmail(1, 100, sentAt, savedAt, makeTestEML("")))
	require.NoError(t, s.SaveEmail(999999999, 100, sentAt, savedAt, makeTestEML("")))
	require.NoError(t, s.SaveEmail(4294967295, 100, sentAt, savedAt, makeTestEML(""))) // max uint32

	base := filepath.Join(rootDir, "emails", "100", "202506")
	assert.FileExists(t, filepath.Join(base, "0000000001.eml"))
	assert.FileExists(t, filepath.Join(base, "0999999999.eml"))
	assert.FileExists(t, filepath.Join(base, "4294967295.eml"))
}

// TestSaveEmail_PathFormat verifies the full path format including uidvalidity and YYYYMM.
func TestSaveEmail_PathFormat(t *testing.T) {
	s, rootDir := openTestStore(t)

	sentAt := time.Date(2025, 11, 15, 12, 0, 0, 0, time.UTC)
	savedAt := sentAt
	uid := uint32(42)
	uidValidity := uint32(555)

	require.NoError(t, s.SaveEmail(uid, uidValidity, sentAt, savedAt, makeTestEML("")))

	expectedPath := filepath.Join(rootDir, "emails", "555", "202511", "0000000042.eml")
	assert.FileExists(t, expectedPath)
}

// TestSaveEmail_FilePermissions verifies that saved .eml files have 0600 permissions
// and created directories have 0700 permissions.
func TestSaveEmail_FilePermissions(t *testing.T) {
	s, rootDir := openTestStore(t)

	sentAt := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	savedAt := sentAt
	uid := uint32(1)
	uidValidity := uint32(100)

	require.NoError(t, s.SaveEmail(uid, uidValidity, sentAt, savedAt, makeTestEML("")))

	emlPath := filepath.Join(rootDir, "emails", "100", "202506", "0000000001.eml")
	emlInfo, err := os.Stat(emlPath)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(filePerm), emlInfo.Mode().Perm(), ".eml file should have 0600 permissions")

	// Check that the {uidvalidity}/{YYYYMM} directory has 0700 permissions.
	subdirPath := filepath.Join(rootDir, "emails", "100", "202506")
	subdirInfo, err := os.Stat(subdirPath)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(dirPerm), subdirInfo.Mode().Perm(), "email subdir should have 0700 permissions")
}

// TestSaveEmail_Atomic verifies that no temporary files remain after SaveEmail.
func TestSaveEmail_Atomic(t *testing.T) {
	s, rootDir := openTestStore(t)

	sentAt := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	require.NoError(t, s.SaveEmail(1, 100, sentAt, sentAt, makeTestEML("")))

	emailsDir := filepath.Join(rootDir, "emails")
	err := filepath.Walk(emailsDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			name := filepath.Base(path)
			assert.False(t, len(name) > 4 && name[:4] == ".tmp",
				"no temp files should remain after SaveEmail, found: %s", path)
		}
		return nil
	})
	require.NoError(t, err)
}

// TestSaveEmail_Idempotent verifies that saving the same UID+UIDVALIDITY twice
// is a no-op — the file content remains as written by the first call.
func TestSaveEmail_Idempotent(t *testing.T) {
	s, rootDir := openTestStore(t)

	sentAt := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	uid := uint32(1)
	uidValidity := uint32(100)

	firstContent := makeTestEML("Mon, 01 Jun 2025 00:00:00 +0000")
	secondContent := makeTestEML("Tue, 02 Jun 2025 00:00:00 +0000")

	require.NoError(t, s.SaveEmail(uid, uidValidity, sentAt, sentAt, firstContent))
	require.NoError(t, s.SaveEmail(uid, uidValidity, sentAt, sentAt, secondContent))

	emlPath := filepath.Join(rootDir, "emails", "100", "202506", "0000000001.eml")
	// G304: emlPath is constructed from t.TempDir(), a safe test path.
	got, err := os.ReadFile(emlPath) //nolint:gosec
	require.NoError(t, err)
	assert.Equal(t, firstContent, got, "second save should not overwrite the first")
}

// TestSaveEmail_DifferentUIDValidity verifies that emails with different UIDVALIDITY
// values are stored in separate directories and do not collide.
func TestSaveEmail_DifferentUIDValidity(t *testing.T) {
	s, rootDir := openTestStore(t)

	sentAt := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	uid := uint32(1)

	require.NoError(t, s.SaveEmail(uid, 100, sentAt, sentAt, makeTestEML("")))
	require.NoError(t, s.SaveEmail(uid, 200, sentAt, sentAt, makeTestEML("")))

	assert.FileExists(t, filepath.Join(rootDir, "emails", "100", "202506", "0000000001.eml"))
	assert.FileExists(t, filepath.Join(rootDir, "emails", "200", "202506", "0000000001.eml"))
}

// TestSaveEmail_ZeroSentAtFallback verifies that when sentAt is zero,
// savedAt is used for the YYYYMM directory and a WARN log is emitted.
func TestSaveEmail_ZeroSentAtFallback(t *testing.T) {
	spy := setDefaultSlogSpy(t)
	s, rootDir := openTestStore(t)

	savedAt := time.Date(2025, 8, 15, 0, 0, 0, 0, time.UTC)
	uid := uint32(7)
	uidValidity := uint32(111)

	require.NoError(t, s.SaveEmail(uid, uidValidity, time.Time{}, savedAt, makeTestEML("")))

	// The file should be under the savedAt month (202508).
	expectedPath := filepath.Join(rootDir, "emails", "111", "202508", "0000000007.eml")
	assert.FileExists(t, expectedPath, "zero sentAt should fall back to savedAt month")

	// A WARN log should have been emitted.
	warnFound := false
	for _, r := range spy.records {
		if r.Level == slog.LevelWarn {
			warnFound = true
			break
		}
	}
	assert.True(t, warnFound, "a WARN log should be emitted when sentAt is zero")
}

// TestSaveEmail_Error verifies that SaveEmail returns an error when writing fails.
func TestSaveEmail_Error(t *testing.T) {
	s, rootDir := openTestStore(t)
	sentAt := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)

	// Pre-create the target path as a directory to force a write failure.
	targetDir := filepath.Join(rootDir, "emails", "100", "202506")
	require.NoError(t, os.MkdirAll(targetDir, 0o700))
	// Create a directory at the file's location.
	conflictPath := filepath.Join(targetDir, "0000000001.eml")
	require.NoError(t, os.Mkdir(conflictPath, 0o700))

	err := s.SaveEmail(1, 100, sentAt, sentAt, makeTestEML(""))
	assert.Error(t, err)
}

// TestSaveEmail_ReadOnly verifies that SaveEmail returns ErrReadOnly on a read-only store.
func TestSaveEmail_ReadOnly(t *testing.T) {
	rootDir := t.TempDir()
	s, err := Open(rootDir, makeTestIdentity(), OpenReadOnly)
	require.NoError(t, err)

	sentAt := time.Now()
	err = s.SaveEmail(1, 100, sentAt, sentAt, makeTestEML(""))
	assert.ErrorIs(t, err, ErrReadOnly)
}

// TestSaveEmailMetas_BatchInsert verifies that SaveEmailMetas inserts multiple
// entries in a single atomic write.
func TestSaveEmailMetas_BatchInsert(t *testing.T) {
	s, rootDir := openTestStore(t)

	sentAt := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	savedAt := sentAt.Add(time.Minute)

	metas := []EmailMeta{
		{UID: 1, UIDValidity: 100, SentAt: sentAt, SavedAt: savedAt},
		{UID: 2, UIDValidity: 100, SentAt: sentAt.Add(time.Hour), SavedAt: savedAt},
		{UID: 3, UIDValidity: 200, SentAt: sentAt, SavedAt: savedAt},
	}
	require.NoError(t, s.SaveEmailMetas(metas))

	df, err := loadDataFileFromPath(rootDir)
	require.NoError(t, err)
	assert.Len(t, df.Emails, 3)
}

// TestSaveEmailMetas_Idempotent verifies that re-calling SaveEmailMetas with existing
// {uid, uidvalidity} entries does not overwrite saved_at or reset report_end_date.
func TestSaveEmailMetas_Idempotent(t *testing.T) {
	s, rootDir := openTestStore(t)

	sentAt := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	savedAt := sentAt.Add(time.Minute)

	// First registration.
	metas := []EmailMeta{
		{UID: 1, UIDValidity: 100, SentAt: sentAt, SavedAt: savedAt},
	}
	require.NoError(t, s.SaveEmailMetas(metas))

	// Simulate SaveReports setting report_end_date.
	endDate := time.Date(2025, 6, 15, 0, 0, 0, 0, time.UTC)
	require.NoError(t, SaveReport(s, ReportInput{
		Report:      makeFullReport("r1", endDate),
		UID:         1,
		UIDValidity: 100,
	}))

	// Second registration with different saved_at — should not overwrite.
	differentSavedAt := savedAt.Add(24 * time.Hour)
	metas2 := []EmailMeta{
		{UID: 1, UIDValidity: 100, SentAt: sentAt, SavedAt: differentSavedAt},
	}
	require.NoError(t, s.SaveEmailMetas(metas2))

	df, err := loadDataFileFromPath(rootDir)
	require.NoError(t, err)
	require.Len(t, df.Emails, 1)

	entry := df.Emails[0]
	assert.True(t, entry.SavedAt.Equal(savedAt), "saved_at should not be overwritten by repeated SaveEmailMetas")
	require.NotNil(t, entry.ReportEndDate, "report_end_date should not be reset")
	assert.True(t, entry.ReportEndDate.Equal(endDate), "report_end_date should remain unchanged")
}

// TestSaveEmailMetas_OrphanRescue verifies that an email entry that was not registered
// in the index (e.g., crash before the first SaveEmailMetas call) is rescued on the
// next SaveEmailMetas call.
func TestSaveEmailMetas_OrphanRescue(t *testing.T) {
	s, rootDir := openTestStore(t)

	sentAt := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	savedAt := sentAt.Add(time.Minute)

	// Register only UID=1 in the first call (simulating UID=2 being orphaned).
	require.NoError(t, s.SaveEmailMetas([]EmailMeta{
		{UID: 1, UIDValidity: 100, SentAt: sentAt, SavedAt: savedAt},
	}))

	// Second call includes UID=2 — it should be registered.
	require.NoError(t, s.SaveEmailMetas([]EmailMeta{
		{UID: 1, UIDValidity: 100, SentAt: sentAt, SavedAt: savedAt},
		{UID: 2, UIDValidity: 100, SentAt: sentAt, SavedAt: savedAt},
	}))

	df, err := loadDataFileFromPath(rootDir)
	require.NoError(t, err)
	assert.Len(t, df.Emails, 2, "orphaned email should be rescued on next SaveEmailMetas call")
}

// TestSaveEmailMetas_MinimalEntryRescue verifies that when SaveReports creates a minimal
// index entry (with zero SentAt/SavedAt and only ReportEndDate set), a subsequent call to
// SaveEmailMetas fills in the missing SentAt/SavedAt without resetting ReportEndDate.
func TestSaveEmailMetas_MinimalEntryRescue(t *testing.T) {
	s, rootDir := openTestStore(t)

	sentAt := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	savedAt := sentAt.Add(time.Minute)
	endDate := time.Date(2025, 6, 15, 0, 0, 0, 0, time.UTC)

	// SaveReports first: creates a minimal index entry with zero SentAt/SavedAt.
	require.NoError(t, SaveReport(s, ReportInput{
		Report:      makeFullReport("r1", endDate),
		UID:         1,
		UIDValidity: 100,
	}))

	// Verify the minimal entry was created.
	df, err := loadDataFileFromPath(rootDir)
	require.NoError(t, err)
	require.Len(t, df.Emails, 1)
	assert.True(t, df.Emails[0].SentAt.IsZero(), "minimal entry should have zero SentAt")
	assert.True(t, df.Emails[0].SavedAt.IsZero(), "minimal entry should have zero SavedAt")
	require.NotNil(t, df.Emails[0].ReportEndDate)

	// SaveEmailMetas: should fill in SentAt/SavedAt and preserve ReportEndDate.
	require.NoError(t, s.SaveEmailMetas([]EmailMeta{
		{UID: 1, UIDValidity: 100, SentAt: sentAt, SavedAt: savedAt},
	}))

	df, err = loadDataFileFromPath(rootDir)
	require.NoError(t, err)
	require.Len(t, df.Emails, 1)
	entry := df.Emails[0]
	assert.True(t, entry.SentAt.Equal(sentAt), "SentAt should be filled in by SaveEmailMetas")
	assert.True(t, entry.SavedAt.Equal(savedAt), "SavedAt should be filled in by SaveEmailMetas")
	require.NotNil(t, entry.ReportEndDate, "ReportEndDate must not be reset")
	assert.True(t, entry.ReportEndDate.Equal(endDate), "ReportEndDate should be preserved")
}

// TestSaveEmailMetas_ZeroSentAtNormalization verifies that when SaveEmailMetas receives
// a meta with zero SentAt, it falls back to SavedAt and emits a WARN log.
func TestSaveEmailMetas_ZeroSentAtNormalization(t *testing.T) {
	spy := setDefaultSlogSpy(t)
	s, rootDir := openTestStore(t)

	savedAt := time.Date(2025, 8, 15, 0, 0, 0, 0, time.UTC)
	require.NoError(t, s.SaveEmailMetas([]EmailMeta{
		{UID: 5, UIDValidity: 200, SentAt: time.Time{}, SavedAt: savedAt},
	}))

	df, err := loadDataFileFromPath(rootDir)
	require.NoError(t, err)
	require.Len(t, df.Emails, 1)
	assert.True(t, df.Emails[0].SentAt.Equal(savedAt),
		"zero SentAt should be normalized to SavedAt in the index entry")

	warnFound := false
	for _, r := range spy.records {
		if r.Level == slog.LevelWarn {
			warnFound = true
			break
		}
	}
	assert.True(t, warnFound, "a WARN log should be emitted when SentAt is zero")
}

// TestSaveEmailMetas_AtomicWrite verifies that no temp files remain after SaveEmailMetas.
func TestSaveEmailMetas_AtomicWrite(t *testing.T) {
	s, rootDir := openTestStore(t)

	sentAt := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	require.NoError(t, s.SaveEmailMetas([]EmailMeta{
		{UID: 1, UIDValidity: 100, SentAt: sentAt, SavedAt: sentAt},
	}))

	entries, err := os.ReadDir(rootDir)
	require.NoError(t, err)
	for _, e := range entries {
		name := e.Name()
		assert.False(t, len(name) > 4 && name[:4] == ".tmp",
			"no temp files should remain after SaveEmailMetas, found: %s", name)
	}
}

// TestSaveEmailMetas_WriteError verifies that SaveEmailMetas returns an error
// when the data directory is read-only.
func TestSaveEmailMetas_WriteError(t *testing.T) {
	s, rootDir := openTestStore(t)

	require.NoError(t, os.Chmod(rootDir, 0o500))       //nolint:gosec
	t.Cleanup(func() { _ = os.Chmod(rootDir, 0o700) }) //nolint:gosec

	sentAt := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	err := s.SaveEmailMetas([]EmailMeta{
		{UID: 1, UIDValidity: 100, SentAt: sentAt, SavedAt: sentAt},
	})
	assert.Error(t, err)
}

// TestSaveEmailMetas_ReadOnly verifies that SaveEmailMetas returns ErrReadOnly
// when called on a read-only store.
func TestSaveEmailMetas_ReadOnly(t *testing.T) {
	rootDir := t.TempDir()
	s, err := Open(rootDir, makeTestIdentity(), OpenReadOnly)
	require.NoError(t, err)

	sentAt := time.Now()
	err = s.SaveEmailMetas([]EmailMeta{
		{UID: 1, UIDValidity: 100, SentAt: sentAt, SavedAt: sentAt},
	})
	assert.ErrorIs(t, err, ErrReadOnly)
}
