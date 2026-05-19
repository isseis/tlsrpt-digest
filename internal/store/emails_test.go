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

// --- LoadEmails tests (Phase 3 – 3.2) ---

// TestLoadEmails_Enumeration verifies that LoadEmails enumerates all .eml files and
// correctly derives UID and UIDValidity from the path structure.
func TestLoadEmails_Enumeration(t *testing.T) {
	s, _ := openTestStore(t)

	sentAt1 := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	sentAt2 := time.Date(2025, 7, 1, 0, 0, 0, 0, time.UTC)

	require.NoError(t, s.SaveEmail(1, 100, sentAt1, sentAt1, makeTestEML("Mon, 01 Jun 2025 00:00:00 +0000")))
	require.NoError(t, s.SaveEmail(2, 100, sentAt2, sentAt2, makeTestEML("Tue, 01 Jul 2025 00:00:00 +0000")))
	require.NoError(t, s.SaveEmail(1, 200, sentAt1, sentAt1, makeTestEML("Mon, 01 Jun 2025 00:00:00 +0000")))

	emails, err := s.LoadEmails()
	require.NoError(t, err)
	assert.Len(t, emails, 3)

	byKey := make(map[emailKey]LoadedEmail)
	for _, e := range emails {
		byKey[emailKey{e.UID, e.UIDValidity}] = e
	}
	assert.Contains(t, byKey, emailKey{1, 100})
	assert.Contains(t, byKey, emailKey{2, 100})
	assert.Contains(t, byKey, emailKey{1, 200})
}

// TestLoadEmails_Fields verifies that SentAt, SavedAt, and Path are populated correctly.
func TestLoadEmails_Fields(t *testing.T) {
	s, _ := openTestStore(t)

	sentAt := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	rawEML := makeTestEML("Mon, 01 Jun 2025 00:00:00 +0000")
	require.NoError(t, s.SaveEmail(42, 999, sentAt, sentAt, rawEML))

	emails, err := s.LoadEmails()
	require.NoError(t, err)
	require.Len(t, emails, 1)

	e := emails[0]
	assert.Equal(t, uint32(42), e.UID)
	assert.Equal(t, uint32(999), e.UIDValidity)
	assert.Equal(t, "999/202506/0000000042.eml", filepath.ToSlash(e.Path))
	assert.False(t, e.SavedAt.IsZero(), "SavedAt from ctime should be non-zero")
	assert.WithinDuration(t, time.Now(), e.SavedAt, 10*time.Second, "SavedAt should be close to now")
	assert.True(t, e.SentAt.Equal(sentAt.UTC()), "SentAt should match Date header")
	assert.NotNil(t, e.Message)
}

// TestLoadEmails_SentAtFallback verifies that a missing Date header causes SentAt to fall
// back to SavedAt (ctime) and emits a WARN log.
func TestLoadEmails_SentAtFallback(t *testing.T) {
	spy := setDefaultSlogSpy(t)
	s, _ := openTestStore(t)

	sentAt := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	rawEML := makeTestEML("") // no Date header
	require.NoError(t, s.SaveEmail(1, 100, sentAt, sentAt, rawEML))

	emails, err := s.LoadEmails()
	require.NoError(t, err)
	require.Len(t, emails, 1)

	e := emails[0]
	assert.True(t, e.SentAt.Equal(e.SavedAt),
		"SentAt should equal SavedAt when Date header is missing")

	warnFound := false
	for _, r := range spy.records {
		if r.Level == slog.LevelWarn {
			warnFound = true
			break
		}
	}
	assert.True(t, warnFound, "a WARN log should be emitted for missing Date header")
}

// TestLoadEmails_SkipsFailedFiles verifies that a corrupt .eml is skipped and its error
// is aggregated, while valid emails are still returned.
func TestLoadEmails_SkipsFailedFiles(t *testing.T) {
	s, rootDir := openTestStore(t)

	sentAt := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	require.NoError(t, s.SaveEmail(1, 100, sentAt, sentAt, makeTestEML("Mon, 01 Jun 2025 00:00:00 +0000")))

	// Inject an empty (unparseable) .eml file directly.
	emptyEML := filepath.Join(rootDir, "emails", "100", "202506", "0000000002.eml")
	require.NoError(t, os.WriteFile(emptyEML, []byte{}, 0o600))

	emails, err := s.LoadEmails()
	assert.Error(t, err, "should return aggregated error for the corrupt file")
	assert.Len(t, emails, 1, "the valid email should still be returned")

	var loadErr *ErrLoadEmailFailed
	assert.ErrorAs(t, err, &loadErr)
}

// TestLoadEmails_EmptyStore verifies that LoadEmails returns an empty slice (not error)
// when no emails have been saved.
func TestLoadEmails_EmptyStore(t *testing.T) {
	s, _ := openTestStore(t)

	emails, err := s.LoadEmails()
	require.NoError(t, err)
	assert.NotNil(t, emails)
	assert.Empty(t, emails)
}

// TestLoadEmails_ReprocessIntegration verifies the full reprocess flow:
// LoadEmails → SaveEmailMetas → SaveReports results in consistent store state.
func TestLoadEmails_ReprocessIntegration(t *testing.T) {
	s, rootDir := openTestStore(t)

	sentAt := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	endDate := time.Date(2025, 6, 15, 0, 0, 0, 0, time.UTC)
	rawEML := makeTestEML("Mon, 01 Jun 2025 00:00:00 +0000")
	require.NoError(t, s.SaveEmail(1, 100, sentAt, sentAt, rawEML))

	emails, err := s.LoadEmails()
	require.NoError(t, err)
	require.Len(t, emails, 1)

	require.NoError(t, s.SaveEmailMetas([]EmailMeta{{
		UID:         emails[0].UID,
		UIDValidity: emails[0].UIDValidity,
		SentAt:      emails[0].SentAt,
		SavedAt:     emails[0].SavedAt,
	}}))
	require.NoError(t, SaveReport(s, ReportInput{
		Report:      makeFullReport("r1", endDate),
		UID:         emails[0].UID,
		UIDValidity: emails[0].UIDValidity,
	}))

	df, err := loadDataFileFromPath(rootDir)
	require.NoError(t, err)
	assert.Len(t, df.Reports, 1)
	assert.Len(t, df.Emails, 1)
	require.NotNil(t, df.Emails[0].ReportEndDate)
	assert.True(t, df.Emails[0].ReportEndDate.Equal(endDate))
}

// --- DeleteEmailsBefore tests (Phase 3 – 3.6) ---

// saveEMLWithMeta is a test helper that saves a .eml file and registers its index entry
// (always uses uidValidity=100) with the given sentAt, savedAt, and optionally a
// report_end_date via SaveReports.
func saveEMLWithMeta(t *testing.T, s Store, uid uint32, sentAt, savedAt time.Time, reportEndDate *time.Time) {
	t.Helper()
	const uidValidity = uint32(100)
	rawEML := makeTestEML(sentAt.Format("Mon, 02 Jan 2006 15:04:05 +0000"))
	require.NoError(t, s.SaveEmail(uid, uidValidity, sentAt, savedAt, rawEML))
	require.NoError(t, s.SaveEmailMetas([]EmailMeta{{UID: uid, UIDValidity: uidValidity, SentAt: sentAt, SavedAt: savedAt}}))
	if reportEndDate != nil {
		require.NoError(t, SaveReport(s, ReportInput{
			Report:      makeFullReport("r-"+string(rune('a'+uid%26)), *reportEndDate),
			UID:         uid,
			UIDValidity: uidValidity,
		}))
	}
}

// TestDeleteEmailsBefore_Conditions verifies that normal and forced deletion conditions
// work independently, and that entries matching neither condition are preserved.
func TestDeleteEmailsBefore_Conditions(t *testing.T) {
	s, rootDir := openTestStore(t)

	base := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	reportCutoff := base
	savedAtCutoff := base

	// UID=1: report_end_date before reportCutoff → normal deletion
	endBefore := base.Add(-time.Hour)
	saveEMLWithMeta(t, s, 1, base, base.Add(-2*time.Hour), &endBefore)

	// UID=2: saved_at before savedAtCutoff → forced deletion
	saveEMLWithMeta(t, s, 2, base, base.Add(-time.Hour), nil)

	// UID=3: neither condition → keep
	endAfter := base.Add(time.Hour)
	saveEMLWithMeta(t, s, 3, base, base.Add(time.Hour), &endAfter)

	deleted, err := s.DeleteEmailsBefore(reportCutoff, savedAtCutoff)
	require.NoError(t, err)
	assert.Equal(t, 2, deleted)

	// Check that UID=3's file remains.
	emlPath := filepath.Join(rootDir, "emails", "100", "202506", "0000000003.eml")
	assert.FileExists(t, emlPath)

	// UID=1 and UID=2 should be gone.
	assert.NoFileExists(t, filepath.Join(rootDir, "emails", "100", "202506", "0000000001.eml"))
	assert.NoFileExists(t, filepath.Join(rootDir, "emails", "100", "202506", "0000000002.eml"))

	// Only UID=3 should remain in the index.
	df, err := loadDataFileFromPath(rootDir)
	require.NoError(t, err)
	assert.Len(t, df.Emails, 1)
	assert.Equal(t, uint32(3), df.Emails[0].UID)
}

// TestDeleteEmailsBefore_ZeroSavedAtCutoff verifies that passing time.Time{} for
// savedAtCutoff disables forced deletion.
func TestDeleteEmailsBefore_ZeroSavedAtCutoff(t *testing.T) {
	s, _ := openTestStore(t)

	base := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	// UID=1: only satisfies forced deletion (saved_at < cutoff, but reportEndDate is nil)
	saveEMLWithMeta(t, s, 1, base, base.Add(-time.Hour), nil)

	// Pass zero savedAtCutoff: forced deletion disabled
	deleted, err := s.DeleteEmailsBefore(base, time.Time{})
	require.NoError(t, err)
	assert.Equal(t, 0, deleted)
}

// TestDeleteEmailsBefore_NullReportEndDate verifies that entries with null report_end_date
// are excluded from normal deletion.
func TestDeleteEmailsBefore_NullReportEndDate(t *testing.T) {
	s, _ := openTestStore(t)

	base := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	saveEMLWithMeta(t, s, 1, base, base, nil) // null report_end_date

	deleted, err := s.DeleteEmailsBefore(base.Add(time.Hour), time.Time{})
	require.NoError(t, err)
	assert.Equal(t, 0, deleted, "null report_end_date should not match normal deletion")
}

// TestDeleteEmailsBefore_MissingFileIdempotent verifies that a file already gone from
// disk is treated as a non-error and its index entry is still removed.
func TestDeleteEmailsBefore_MissingFileIdempotent(t *testing.T) {
	s, rootDir := openTestStore(t)

	base := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	endBefore := base.Add(-time.Hour)
	saveEMLWithMeta(t, s, 1, base, base, &endBefore)

	// Manually delete the file to simulate a previous partial run.
	emlPath := filepath.Join(rootDir, "emails", "100", "202506", "0000000001.eml")
	require.NoError(t, os.Remove(emlPath))

	deleted, err := s.DeleteEmailsBefore(base, time.Time{})
	require.NoError(t, err)
	assert.Equal(t, 1, deleted, "missing file should still count as deleted")

	df, err := loadDataFileFromPath(rootDir)
	require.NoError(t, err)
	assert.Empty(t, df.Emails, "index entry should be removed even when file was already gone")
}

// TestDeleteEmailsBefore_ZeroDeleted verifies that deleting 0 records returns deleted=0, err=nil.
func TestDeleteEmailsBefore_ZeroDeleted(t *testing.T) {
	s, _ := openTestStore(t)
	deleted, err := s.DeleteEmailsBefore(time.Now(), time.Time{})
	require.NoError(t, err)
	assert.Equal(t, 0, deleted)
}

// TestDeleteEmailsBefore_PartialFailure verifies that when one file deletion fails, the
// operation continues, the success count is correct, and the failed entry's index is kept.
func TestDeleteEmailsBefore_PartialFailure(t *testing.T) {
	s, rootDir := openTestStore(t)

	// Both emails are in different YYYYMM directories so we can block one.
	// UID=1 in 202506, UID=2 in 202507; both have report_end_date before reportCutoff.
	sentAt1 := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	sentAt2 := time.Date(2025, 7, 1, 0, 0, 0, 0, time.UTC)
	reportCutoff := time.Date(2025, 9, 1, 0, 0, 0, 0, time.UTC)
	endDate1 := sentAt1.Add(-time.Hour)
	endDate2 := sentAt2.Add(-time.Hour)
	saveEMLWithMeta(t, s, 1, sentAt1, sentAt1, &endDate1)
	saveEMLWithMeta(t, s, 2, sentAt2, sentAt2, &endDate2)

	// Make UID=1's parent directory unwritable so os.Remove fails.
	parentDir := filepath.Join(rootDir, "emails", "100", "202506")
	require.NoError(t, os.Chmod(parentDir, 0o500))       //nolint:gosec
	t.Cleanup(func() { _ = os.Chmod(parentDir, 0o700) }) //nolint:gosec

	deleted, err := s.DeleteEmailsBefore(reportCutoff, time.Time{})
	assert.Error(t, err, "should return aggregated error")
	assert.Equal(t, 1, deleted, "UID=2 should be counted as deleted")

	var delErr *ErrDeleteEmailFailed
	assert.ErrorAs(t, err, &delErr)

	// UID=1 index entry should remain; UID=2 should be removed.
	_ = os.Chmod(parentDir, 0o700) //nolint:gosec
	df, err2 := loadDataFileFromPath(rootDir)
	require.NoError(t, err2)
	assert.Len(t, df.Emails, 1)
	assert.Equal(t, uint32(1), df.Emails[0].UID)
}

// TestDeleteEmailsBefore_Sweep verifies that AC-32b orphan directory sweep removes
// YYYYMM directories older than savedAtCutoff.
func TestDeleteEmailsBefore_Sweep(t *testing.T) {
	s, rootDir := openTestStore(t)

	base := time.Date(2025, 3, 1, 0, 0, 0, 0, time.UTC)
	savedAtCutoff := time.Date(2025, 4, 1, 0, 0, 0, 0, time.UTC)

	// Save email in 202503 – will be deleted by index-based deletion first.
	endBefore := base.Add(-time.Hour)
	saveEMLWithMeta(t, s, 1, base, base, &endBefore)

	// Create an orphaned directory in 202502 (no index entry).
	orphanDir := filepath.Join(rootDir, "emails", "100", "202502")
	require.NoError(t, os.MkdirAll(orphanDir, 0o700))
	orphanFile := filepath.Join(orphanDir, "0000000099.eml")
	require.NoError(t, os.WriteFile(orphanFile, makeTestEML(""), 0o600))

	deleted, err := s.DeleteEmailsBefore(base, savedAtCutoff)
	require.NoError(t, err)
	assert.Equal(t, 1, deleted)

	// Orphaned 202502 directory should be swept away.
	assert.NoDirExists(t, orphanDir, "orphaned 202502 dir should be removed by sweep")
	// 202503 directory content should also be gone (from index-based deletion).
	assert.NoFileExists(t, filepath.Join(rootDir, "emails", "100", "202503", "0000000001.eml"))
}

// TestDeleteEmailsBefore_SweepNotCalledWhenZero verifies that the directory sweep is not
// executed when savedAtCutoff is zero.
func TestDeleteEmailsBefore_SweepNotCalledWhenZero(t *testing.T) {
	s, rootDir := openTestStore(t)

	base := time.Date(2025, 3, 1, 0, 0, 0, 0, time.UTC)
	// Create an orphaned directory in 202502 (no index entry).
	orphanDir := filepath.Join(rootDir, "emails", "100", "202502")
	require.NoError(t, os.MkdirAll(orphanDir, 0o700))

	_, err := s.DeleteEmailsBefore(base, time.Time{})
	require.NoError(t, err)

	// Orphaned directory should NOT be swept (savedAtCutoff is zero).
	assert.DirExists(t, orphanDir, "orphaned dir should remain when savedAtCutoff is zero")
}
