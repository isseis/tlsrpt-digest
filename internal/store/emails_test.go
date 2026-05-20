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
	internalDate := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	rawEML := makeTestEML("Mon, 01 Jun 2025 00:00:00 +0000")

	require.NoError(t, s.SaveEmail(uid, uidValidity, internalDate, rawEML))

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

	internalDate := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)

	require.NoError(t, s.SaveEmail(1, 100, internalDate, makeTestEML("")))
	require.NoError(t, s.SaveEmail(999999999, 100, internalDate, makeTestEML("")))
	require.NoError(t, s.SaveEmail(4294967295, 100, internalDate, makeTestEML(""))) // max uint32

	base := filepath.Join(rootDir, "emails", "100", "202506")
	assert.FileExists(t, filepath.Join(base, "0000000001.eml"))
	assert.FileExists(t, filepath.Join(base, "0999999999.eml"))
	assert.FileExists(t, filepath.Join(base, "4294967295.eml"))
}

// TestSaveEmail_PathFormat verifies the full path format including uidvalidity and YYYYMM.
func TestSaveEmail_PathFormat(t *testing.T) {
	s, rootDir := openTestStore(t)

	internalDate := time.Date(2025, 11, 15, 12, 0, 0, 0, time.UTC)
	uid := uint32(42)
	uidValidity := uint32(555)

	require.NoError(t, s.SaveEmail(uid, uidValidity, internalDate, makeTestEML("")))

	expectedPath := filepath.Join(rootDir, "emails", "555", "202511", "0000000042.eml")
	assert.FileExists(t, expectedPath)
}

// TestSaveEmail_FilePermissions verifies that saved .eml files have 0600 permissions
// and created directories have 0700 permissions.
func TestSaveEmail_FilePermissions(t *testing.T) {
	s, rootDir := openTestStore(t)

	internalDate := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	uid := uint32(1)
	uidValidity := uint32(100)

	require.NoError(t, s.SaveEmail(uid, uidValidity, internalDate, makeTestEML("")))

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

	internalDate := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	require.NoError(t, s.SaveEmail(1, 100, internalDate, makeTestEML("")))

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

	internalDate := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	uid := uint32(1)
	uidValidity := uint32(100)

	firstContent := makeTestEML("Mon, 01 Jun 2025 00:00:00 +0000")
	secondContent := makeTestEML("Tue, 02 Jun 2025 00:00:00 +0000")

	require.NoError(t, s.SaveEmail(uid, uidValidity, internalDate, firstContent))
	require.NoError(t, s.SaveEmail(uid, uidValidity, internalDate, secondContent))

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

	internalDate := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	uid := uint32(1)

	require.NoError(t, s.SaveEmail(uid, 100, internalDate, makeTestEML("")))
	require.NoError(t, s.SaveEmail(uid, 200, internalDate, makeTestEML("")))

	assert.FileExists(t, filepath.Join(rootDir, "emails", "100", "202506", "0000000001.eml"))
	assert.FileExists(t, filepath.Join(rootDir, "emails", "200", "202506", "0000000001.eml"))
}

// TestSaveEmail_ZeroInternalDate_Error verifies that SaveEmail returns an error
// when internalDate is zero.
func TestSaveEmail_ZeroInternalDate_Error(t *testing.T) {
	s, _ := openTestStore(t)

	err := s.SaveEmail(7, 111, time.Time{}, makeTestEML(""))
	assert.Error(t, err, "zero internalDate should return an error")
}

// TestSaveEmail_Error verifies that SaveEmail returns an error when writing fails.
func TestSaveEmail_Error(t *testing.T) {
	s, rootDir := openTestStore(t)
	internalDate := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)

	// Pre-create the target path as a directory to force a write failure.
	targetDir := filepath.Join(rootDir, "emails", "100", "202506")
	require.NoError(t, os.MkdirAll(targetDir, 0o700))
	// Create a directory at the file's location.
	conflictPath := filepath.Join(targetDir, "0000000001.eml")
	require.NoError(t, os.Mkdir(conflictPath, 0o700))

	err := s.SaveEmail(1, 100, internalDate, makeTestEML(""))
	assert.Error(t, err)
}

// TestSaveEmail_ReadOnly verifies that SaveEmail returns ErrReadOnly on a read-only store.
func TestSaveEmail_ReadOnly(t *testing.T) {
	rootDir := t.TempDir()
	s, err := Open(rootDir, makeTestIdentity(), OpenReadOnly)
	require.NoError(t, err)

	internalDate := time.Now()
	err = s.SaveEmail(1, 100, internalDate, makeTestEML(""))
	assert.ErrorIs(t, err, ErrReadOnly)
}

// TestSaveEmailMetas_BatchInsert verifies that SaveEmailMetas inserts multiple
// entries in a single atomic write.
func TestSaveEmailMetas_BatchInsert(t *testing.T) {
	s, rootDir := openTestStore(t)

	internalDate := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	metas := []EmailMeta{
		{UID: 1, UIDValidity: 100, InternalDate: internalDate},
		{UID: 2, UIDValidity: 100, InternalDate: internalDate.Add(time.Hour)},
		{UID: 3, UIDValidity: 200, InternalDate: internalDate},
	}
	require.NoError(t, s.SaveEmailMetas(metas))

	df, err := loadDataFileFromPath(rootDir)
	require.NoError(t, err)
	assert.Len(t, df.Emails, 3)
}

// TestSaveEmailMetas_Idempotent verifies that re-calling SaveEmailMetas with existing
// {uid, uidvalidity} entries does not overwrite internal_date or saved_at.
func TestSaveEmailMetas_Idempotent(t *testing.T) {
	s, rootDir := openTestStore(t)

	internalDate := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	// First registration.
	metas := []EmailMeta{
		{UID: 1, UIDValidity: 100, InternalDate: internalDate},
	}
	require.NoError(t, s.SaveEmailMetas(metas))

	// Second registration — should not overwrite.
	metas2 := []EmailMeta{
		{UID: 1, UIDValidity: 100, InternalDate: internalDate},
	}
	require.NoError(t, s.SaveEmailMetas(metas2))

	df, err := loadDataFileFromPath(rootDir)
	require.NoError(t, err)
	require.Len(t, df.Emails, 1)

	entry := df.Emails[0]
	assert.True(t, entry.InternalDate.Equal(internalDate), "internal_date should not be overwritten by repeated SaveEmailMetas")
}

// TestSaveEmailMetas_NoPlaceholderUpdate verifies that SaveEmailMetas does not
// overwrite an existing entry's InternalDate or SavedAt.
func TestSaveEmailMetas_NoPlaceholderUpdate(t *testing.T) {
	s, rootDir := openTestStore(t)

	internalDate := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)

	// First registration.
	require.NoError(t, s.SaveEmailMetas([]EmailMeta{
		{UID: 1, UIDValidity: 100, InternalDate: internalDate},
	}))

	// Second call with different values — should not overwrite.
	differentDate := internalDate.Add(24 * time.Hour)
	require.NoError(t, s.SaveEmailMetas([]EmailMeta{
		{UID: 1, UIDValidity: 100, InternalDate: differentDate},
	}))

	df, err := loadDataFileFromPath(rootDir)
	require.NoError(t, err)
	require.Len(t, df.Emails, 1)
	entry := df.Emails[0]
	assert.True(t, entry.InternalDate.Equal(internalDate), "InternalDate must not be overwritten")
}

// TestSaveEmailMetas_AtomicWrite verifies that no temp files remain after SaveEmailMetas.
func TestSaveEmailMetas_AtomicWrite(t *testing.T) {
	s, rootDir := openTestStore(t)

	internalDate := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	require.NoError(t, s.SaveEmailMetas([]EmailMeta{
		{UID: 1, UIDValidity: 100, InternalDate: internalDate},
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

	internalDate := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	err := s.SaveEmailMetas([]EmailMeta{
		{UID: 1, UIDValidity: 100, InternalDate: internalDate},
	})
	assert.Error(t, err)
}

// TestSaveEmailMetas_ReadOnly verifies that SaveEmailMetas returns ErrReadOnly
// when called on a read-only store.
func TestSaveEmailMetas_ReadOnly(t *testing.T) {
	rootDir := t.TempDir()
	s, err := Open(rootDir, makeTestIdentity(), OpenReadOnly)
	require.NoError(t, err)

	internalDate := time.Now()
	err = s.SaveEmailMetas([]EmailMeta{
		{UID: 1, UIDValidity: 100, InternalDate: internalDate},
	})
	assert.ErrorIs(t, err, ErrReadOnly)
}

// --- LoadEmails tests (Phase 3 – 3.2) ---

// TestLoadEmails_Enumeration verifies that LoadEmails enumerates all .eml files and
// correctly derives UID and UIDValidity from the path structure.
func TestLoadEmails_Enumeration(t *testing.T) {
	s, _ := openTestStore(t)

	internalDate1 := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	internalDate2 := time.Date(2025, 7, 1, 0, 0, 0, 0, time.UTC)

	require.NoError(t, s.SaveEmail(1, 100, internalDate1, makeTestEML("Mon, 01 Jun 2025 00:00:00 +0000")))
	require.NoError(t, s.SaveEmail(2, 100, internalDate2, makeTestEML("Tue, 01 Jul 2025 00:00:00 +0000")))
	require.NoError(t, s.SaveEmail(1, 200, internalDate1, makeTestEML("Mon, 01 Jun 2025 00:00:00 +0000")))

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

// TestLoadEmails_Fields verifies that SavedAt and Path are populated correctly.
func TestLoadEmails_Fields(t *testing.T) {
	s, _ := openTestStore(t)

	internalDate := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	rawEML := makeTestEML("Mon, 01 Jun 2025 00:00:00 +0000")
	require.NoError(t, s.SaveEmail(42, 999, internalDate, rawEML))

	emails, err := s.LoadEmails()
	require.NoError(t, err)
	require.Len(t, emails, 1)

	e := emails[0]
	assert.Equal(t, uint32(42), e.UID)
	assert.Equal(t, uint32(999), e.UIDValidity)
	assert.Equal(t, "999/202506/0000000042.eml", filepath.ToSlash(e.Path))
	assert.NotNil(t, e.Message)
}

// TestLoadEmails_SkipsFailedFiles verifies that a corrupt .eml is skipped and its error
// is aggregated, while valid emails are still returned.
func TestLoadEmails_SkipsFailedFiles(t *testing.T) {
	s, rootDir := openTestStore(t)

	internalDate := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	require.NoError(t, s.SaveEmail(1, 100, internalDate, makeTestEML("Mon, 01 Jun 2025 00:00:00 +0000")))

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

	internalDate := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	endDate := time.Date(2025, 6, 15, 0, 0, 0, 0, time.UTC)
	rawEML := makeTestEML("Mon, 01 Jun 2025 00:00:00 +0000")
	require.NoError(t, s.SaveEmail(1, 100, internalDate, rawEML))

	emails, err := s.LoadEmails()
	require.NoError(t, err)
	require.Len(t, emails, 1)

	require.NoError(t, s.SaveEmailMetas([]EmailMeta{{
		UID:          emails[0].UID,
		UIDValidity:  emails[0].UIDValidity,
		InternalDate: internalDate,
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
}

// --- DeleteEmailsBefore tests ---

// saveEMLWithMeta is a test helper that saves a .eml file and registers its index entry
// (always uses uidValidity=100) with the given internalDate.
func saveEMLWithMeta(t *testing.T, s Store, uid uint32, internalDate time.Time) {
	t.Helper()
	const uidValidity = uint32(100)
	rawEML := makeTestEML(internalDate.Format("Mon, 02 Jan 2006 15:04:05 +0000"))
	require.NoError(t, s.SaveEmail(uid, uidValidity, internalDate, rawEML))
	require.NoError(t, s.SaveEmailMetas([]EmailMeta{{UID: uid, UIDValidity: uidValidity, InternalDate: internalDate}}))
}

// TestDeleteEmailsBefore_ZeroCutoff verifies that passing time.Time{} returns 0, nil immediately.
func TestDeleteEmailsBefore_ZeroCutoff(t *testing.T) {
	s, _ := openTestStore(t)

	base := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	saveEMLWithMeta(t, s, 1, base)

	deleted, err := s.DeleteEmailsBefore(time.Time{})
	require.NoError(t, err)
	assert.Equal(t, 0, deleted)
}

// TestDeleteEmailsBefore_Conditions verifies that internal_date < cutoff is deleted,
// internal_date >= cutoff is kept, and file-not-found counts as deleted.
func TestDeleteEmailsBefore_Conditions(t *testing.T) {
	s, rootDir := openTestStore(t)

	cutoff := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)

	// UID=1: internalDate before cutoff → deleted
	before := cutoff.Add(-time.Hour)
	saveEMLWithMeta(t, s, 1, before)

	// UID=2: internalDate equal to cutoff → kept
	saveEMLWithMeta(t, s, 2, cutoff)

	// UID=3: internalDate after cutoff → kept
	after := cutoff.Add(time.Hour)
	saveEMLWithMeta(t, s, 3, after)

	deleted, err := s.DeleteEmailsBefore(cutoff)
	require.NoError(t, err)
	assert.Equal(t, 1, deleted)

	// UID=1 file should be gone.
	assert.NoFileExists(t, filepath.Join(rootDir, "emails", "100",
		before.UTC().Format("200601"), "0000000001.eml"))

	// UID=2 and UID=3 files should remain.
	assert.FileExists(t, filepath.Join(rootDir, "emails", "100", "202506", "0000000002.eml"))
	assert.FileExists(t, filepath.Join(rootDir, "emails", "100",
		after.UTC().Format("200601"), "0000000003.eml"))

	// Only UID=2 and UID=3 should remain in the index.
	df, err := loadDataFileFromPath(rootDir)
	require.NoError(t, err)
	assert.Len(t, df.Emails, 2)
	uids := make(map[uint32]struct{}, 2)
	for _, e := range df.Emails {
		uids[e.UID] = struct{}{}
	}
	assert.Contains(t, uids, uint32(2))
	assert.Contains(t, uids, uint32(3))
}

// TestDeleteEmailsBefore_MissingFileIdempotent verifies that a file already gone from
// disk is treated as a non-error and its index entry is still removed.
func TestDeleteEmailsBefore_MissingFileIdempotent(t *testing.T) {
	s, rootDir := openTestStore(t)

	internalDate := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	saveEMLWithMeta(t, s, 1, internalDate.Add(-time.Hour))

	// Manually delete the file to simulate a previous partial run.
	emlPath := filepath.Join(rootDir, "emails", "100",
		internalDate.Add(-time.Hour).UTC().Format("200601"), "0000000001.eml")
	require.NoError(t, os.Remove(emlPath))

	deleted, err := s.DeleteEmailsBefore(internalDate)
	require.NoError(t, err)
	assert.Equal(t, 1, deleted, "missing file should still count as deleted")

	df, err := loadDataFileFromPath(rootDir)
	require.NoError(t, err)
	assert.Empty(t, df.Emails, "index entry should be removed even when file was already gone")
}

// TestDeleteEmailsBefore_ZeroDeleted verifies that deleting 0 records returns deleted=0, err=nil.
func TestDeleteEmailsBefore_ZeroDeleted(t *testing.T) {
	s, _ := openTestStore(t)
	deleted, err := s.DeleteEmailsBefore(time.Now())
	require.NoError(t, err)
	assert.Equal(t, 0, deleted)
}

// TestDeleteEmailsBefore_PartialFailure verifies that when one file deletion fails, the
// operation continues, the success count is correct, and the failed entry's index is kept.
func TestDeleteEmailsBefore_PartialFailure(t *testing.T) {
	s, rootDir := openTestStore(t)

	// UID=1 in 202506, UID=2 in 202507; both have internalDate before cutoff.
	internalDate1 := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	internalDate2 := time.Date(2025, 7, 1, 0, 0, 0, 0, time.UTC)
	cutoff := time.Date(2025, 9, 1, 0, 0, 0, 0, time.UTC)
	saveEMLWithMeta(t, s, 1, internalDate1)
	saveEMLWithMeta(t, s, 2, internalDate2)

	// Make UID=1's parent directory unwritable so os.Remove fails.
	parentDir := filepath.Join(rootDir, "emails", "100", "202506")
	require.NoError(t, os.Chmod(parentDir, 0o500))       //nolint:gosec
	t.Cleanup(func() { _ = os.Chmod(parentDir, 0o700) }) //nolint:gosec

	deleted, err := s.DeleteEmailsBefore(cutoff)
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

// TestDeleteEmailsBefore_EmptyDirCleanup verifies that after GC, empty
// {uidvalidity}/{YYYYMM} and {uidvalidity} directories are removed.
func TestDeleteEmailsBefore_EmptyDirCleanup(t *testing.T) {
	s, rootDir := openTestStore(t)

	internalDate := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	saveEMLWithMeta(t, s, 1, internalDate)

	cutoff := internalDate.Add(time.Hour)
	deleted, err := s.DeleteEmailsBefore(cutoff)
	require.NoError(t, err)
	assert.Equal(t, 1, deleted)

	mmDir := filepath.Join(rootDir, "emails", "100", "202506")
	uvDir := filepath.Join(rootDir, "emails", "100")
	assert.NoDirExists(t, mmDir, "{uidvalidity}/{YYYYMM} dir should be removed when empty")
	assert.NoDirExists(t, uvDir, "{uidvalidity} dir should be removed when empty")
}

// TestDeleteEmailsBefore_DirCleanupWarn verifies that when directory removal fails
// after GC, the function returns no error and a WARN is logged.
func TestDeleteEmailsBefore_DirCleanupWarn(t *testing.T) {
	spy := setDefaultSlogSpy(t)
	s, rootDir := openTestStore(t)

	// UID=1 is GC'd. Place an extra file in its YYYYMM dir that is unknown to the
	// store, so the dir is non-empty after GC and os.Remove fails with ENOTEMPTY.
	internalDate1 := time.Date(2025, 5, 1, 0, 0, 0, 0, time.UTC)
	saveEMLWithMeta(t, s, 1, internalDate1)

	// Create an extra file in the YYYYMM dir that prevents its removal.
	mmDir := filepath.Join(rootDir, "emails", "100", "202505")
	extraFile := filepath.Join(mmDir, "extra.txt")
	require.NoError(t, os.WriteFile(extraFile, []byte("keep"), 0o600)) //nolint:gosec

	cutoff := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	deleted, err := s.DeleteEmailsBefore(cutoff)
	require.NoError(t, err, "dir cleanup failure must not be returned as error")
	assert.Equal(t, 1, deleted)

	warnFound := false
	for _, r := range spy.records {
		if r.Level == slog.LevelWarn {
			warnFound = true
			break
		}
	}
	assert.True(t, warnFound, "a WARN log should be emitted when dir removal fails")
}
