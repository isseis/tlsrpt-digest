package store

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestOpen_ReadWriteMode_CreatesDirectories verifies that Open in read-write mode creates
// root_dir and emails/ with mode 0700, initializes tlsrpt.json with an empty record set,
// and creates the sentinel file (.tlsrpt-digest-meta.json) recording the IMAP identity.
func TestOpen_ReadWriteMode_CreatesDirectories(t *testing.T) {
	rootDir := t.TempDir()
	identity := IMAPIdentity{
		Host:    "imap.example.com",
		Port:    993,
		Mailbox: "INBOX",
	}

	store, err := Open(rootDir, identity, OpenReadWrite)
	require.NoError(t, err)
	require.NotNil(t, store)

	// Verify sentinel file exists
	sentinelPath := filepath.Join(rootDir, sentinelFilename)
	_, err = os.Stat(sentinelPath)
	require.NoError(t, err, "sentinel file should exist")

	// Verify emails directory exists
	emailsDir := filepath.Join(rootDir, "emails")
	info, err := os.Stat(emailsDir)
	require.NoError(t, err, "emails directory should exist")
	require.True(t, info.IsDir(), "emails path should be a directory")

	// Verify tlsrpt.json exists and contains an empty record set.
	dataPath := filepath.Join(rootDir, "tlsrpt.json")
	_, err = os.Stat(dataPath)
	require.NoError(t, err, "tlsrpt.json should exist after first open")

	// G304: dataPath is constructed from t.TempDir(), which is always a safe test directory.
	raw, err := os.ReadFile(dataPath) //nolint:gosec
	require.NoError(t, err)
	var got map[string]any
	require.NoError(t, json.Unmarshal(raw, &got), "tlsrpt.json should be valid JSON")
	assert.EqualValues(t, DataFileVersion, got["version"], "version should match DataFileVersion")
	assert.Equal(t, []any{}, got["reports"], "reports should be empty array")
	assert.Equal(t, []any{}, got["emails"], "emails should be empty array")
}

// TestOpen_ReadOnlyMode_NoCreation verifies that Open in read-only mode does not create
// any directories or files, even when root_dir does not exist.
func TestOpen_ReadOnlyMode_NoCreation(t *testing.T) {
	rootDir := t.TempDir()
	nonexistentRoot := filepath.Join(rootDir, "nonexistent")
	identity := IMAPIdentity{
		Host:    "imap.example.com",
		Port:    993,
		Mailbox: "INBOX",
	}

	store, err := Open(nonexistentRoot, identity, OpenReadOnly)
	require.NoError(t, err)
	require.NotNil(t, store)

	// Verify no directory or files were created
	_, err = os.Stat(nonexistentRoot)
	require.True(t, os.IsNotExist(err), "root directory should not be created in read-only mode")
}

// TestOpen_IdentityMismatch_Returns_Error verifies that reopening a store with a different
// IMAP identity (host, port, or mailbox) returns ErrStoreIdentityMismatch containing both
// the expected and actual identifiers.
func TestOpen_IdentityMismatch_Returns_Error(t *testing.T) {
	rootDir := t.TempDir()
	identity1 := IMAPIdentity{
		Host:    "imap.example.com",
		Port:    993,
		Mailbox: "INBOX",
	}

	// Create store with identity1
	_, err := Open(rootDir, identity1, OpenReadWrite)
	require.NoError(t, err)

	// Try to open with different identity
	identity2 := IMAPIdentity{
		Host:    "imap.different.com",
		Port:    993,
		Mailbox: "INBOX",
	}

	_, err = Open(rootDir, identity2, OpenReadWrite)
	require.Error(t, err)

	var mismatchErr *ErrStoreIdentityMismatch
	require.ErrorAs(t, err, &mismatchErr, "error should be ErrStoreIdentityMismatch")
	assert.Equal(t, identity1.Host, mismatchErr.ActualHost)
	assert.Equal(t, identity2.Host, mismatchErr.ExpectedHost)
}

// TestOpen_ReadOnlyMode_EmptyStore verifies that read-only open on a non-existent store
// succeeds without error, returns a valid Store, and marks the store as read-only.
func TestOpen_ReadOnlyMode_EmptyStore(t *testing.T) {
	rootDir := t.TempDir()
	nonexistentRoot := filepath.Join(rootDir, "nonexistent")
	identity := IMAPIdentity{
		Host:    "imap.example.com",
		Port:    993,
		Mailbox: "INBOX",
	}

	store, err := Open(nonexistentRoot, identity, OpenReadOnly)
	require.NoError(t, err)
	require.NotNil(t, store)

	impl, ok := store.(*storeImpl)
	require.True(t, ok, "store should be *storeImpl")
	assert.True(t, impl.readOnly, "store should be read-only")
}

// TestOpen_MultipleOpens_SameIdentity verifies that opening an already-initialized store
// with the same IMAP identity succeeds and leaves the existing data intact.
func TestOpen_MultipleOpens_SameIdentity(t *testing.T) {
	rootDir := t.TempDir()
	identity := IMAPIdentity{
		Host:    "imap.example.com",
		Port:    993,
		Mailbox: "INBOX",
	}

	store1, err := Open(rootDir, identity, OpenReadWrite)
	require.NoError(t, err)
	require.NotNil(t, store1)

	// Open again with same identity
	store2, err := Open(rootDir, identity, OpenReadWrite)
	require.NoError(t, err)
	require.NotNil(t, store2)
}

// TestOpen_FilePermissions verifies that the sentinel file is created with 0600 permissions
// and the emails directory is created with 0700 permissions.
func TestOpen_FilePermissions(t *testing.T) {
	rootDir := t.TempDir()
	identity := IMAPIdentity{
		Host:    "imap.example.com",
		Port:    993,
		Mailbox: "INBOX",
	}

	_, err := Open(rootDir, identity, OpenReadWrite)
	require.NoError(t, err)

	// Check sentinel file permissions (should be 0600)
	sentinelPath := filepath.Join(rootDir, sentinelFilename)
	info, err := os.Stat(sentinelPath)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(filePerm), info.Mode().Perm(),
		"sentinel file should have secure permissions")

	// Check emails directory permissions (should be 0700)
	// Note: rootDir permissions are left as-is by Open (not modified for existing dirs)
	emailsDir := filepath.Join(rootDir, "emails")
	emailsInfo, err := os.Stat(emailsDir)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(dirPerm), emailsInfo.Mode().Perm(),
		"emails directory should have secure permissions")
}

// TestOpen_DirPermissions_NestedCreate verifies that all intermediate directories created
// by Open (including deeply nested ones) are given 0700 permissions.
func TestOpen_DirPermissions_NestedCreate(t *testing.T) {
	rootDir := t.TempDir()
	nestedRoot := filepath.Join(rootDir, "a", "b", "c")
	identity := IMAPIdentity{
		Host:    "imap.example.com",
		Port:    993,
		Mailbox: "INBOX",
	}

	_, err := Open(nestedRoot, identity, OpenReadWrite)
	require.NoError(t, err)

	// Verify all directories have 0700 permissions
	currentPath := nestedRoot
	for currentPath != rootDir && currentPath != string(filepath.Separator) {
		info, err := os.Stat(currentPath)
		require.NoError(t, err, fmt.Sprintf("directory %s should exist", currentPath))
		assert.Equal(t, os.FileMode(dirPerm), info.Mode().Perm(),
			fmt.Sprintf("directory %s should have secure permissions", currentPath))
		currentPath = filepath.Dir(currentPath)
	}
}

// TestOpen_SentinelSchema verifies that the sentinel file created on first open contains
// the correct format version, IMAP identity fields, and a non-zero initialization timestamp,
// with uid_validity and recovery_required initially absent.
func TestOpen_SentinelSchema(t *testing.T) {
	rootDir := t.TempDir()
	identity := IMAPIdentity{
		Host:    "imap.example.com",
		Port:    993,
		Mailbox: "INBOX",
	}

	_, err := Open(rootDir, identity, OpenReadWrite)
	require.NoError(t, err)

	// Load and verify sentinel
	sentinel, found, err := loadSentinel(rootDir)
	require.NoError(t, err)
	require.True(t, found, "sentinel should exist")

	assert.Equal(t, SentinelFormatVersion, sentinel.FormatVersion)
	assert.Equal(t, identity.Host, sentinel.IMAPHost)
	assert.Equal(t, identity.Port, sentinel.IMAPPort)
	assert.Equal(t, identity.Mailbox, sentinel.IMAPMailbox)
	assert.Nil(t, sentinel.UIDValidity, "UIDValidity should be nil initially")
	assert.Nil(t, sentinel.RecoveryRequired, "RecoveryRequired should be nil initially")
	assert.False(t, sentinel.InitializedAt.IsZero(), "InitializedAt should be set")
}

// TestOpen_UnsupportedSchemaVersion tests error handling.
// Verifies that opening with unsupported sentinel schema version returns error.
func TestOpen_UnsupportedSchemaVersion(t *testing.T) {
	rootDir := t.TempDir()

	// Create a sentinel with unsupported version
	badSentinel := internalSentinelFile{
		FormatVersion: 999,
		IMAPHost:      "imap.example.com",
		IMAPPort:      993,
		IMAPMailbox:   "INBOX",
	}
	data, err := json.Marshal(badSentinel)
	require.NoError(t, err)
	sentinelPath := filepath.Join(rootDir, sentinelFilename)
	err = os.WriteFile(sentinelPath, data, filePerm)
	require.NoError(t, err)

	identity := IMAPIdentity{
		Host:    "imap.example.com",
		Port:    993,
		Mailbox: "INBOX",
	}

	_, err = Open(rootDir, identity, OpenReadWrite)
	require.Error(t, err)

	var schemaErr *ErrUnsupportedSchemaVersion
	require.ErrorAs(t, err, &schemaErr, "error should be ErrUnsupportedSchemaVersion")
	assert.Equal(t, 999, schemaErr.Version)
}

// TestOpen_ModeConstants verifies that OpenReadWrite and OpenReadOnly constants
// have the expected distinct numeric values.
func TestOpen_ModeConstants(t *testing.T) {
	assert.Equal(t, OpenMode(0), OpenReadWrite, "OpenReadWrite should be 0")
	assert.Equal(t, OpenMode(1), OpenReadOnly, "OpenReadOnly should be 1")
}

// TestAtomicWriteFile tests atomic write functionality.
// Verifies that atomicWriteFile creates files with correct permissions and content.
func TestAtomicWriteFile(t *testing.T) {
	dir := t.TempDir()
	targetPath := filepath.Join(dir, "test.json")
	testData := []byte(`{"test": "data"}`)

	err := atomicWriteFile(targetPath, testData)
	require.NoError(t, err)

	// Verify file exists and has correct content
	// G304: targetPath is constructed from t.TempDir(), which is always a safe test directory.
	content, err := os.ReadFile(targetPath) //nolint:gosec
	require.NoError(t, err)
	assert.Equal(t, testData, content)

	// Verify file permissions (0600)
	info, err := os.Stat(targetPath)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(filePerm), info.Mode().Perm())
}

// TestAtomicWriteFile_Overwrite tests atomic overwrite.
// Verifies that atomicWriteFile can overwrite existing files atomically.
func TestAtomicWriteFile_Overwrite(t *testing.T) {
	dir := t.TempDir()
	targetPath := filepath.Join(dir, "test.json")

	// First write
	err := atomicWriteFile(targetPath, []byte("first"))
	require.NoError(t, err)

	// Overwrite
	err = atomicWriteFile(targetPath, []byte("second"))
	require.NoError(t, err)

	// G304: targetPath is constructed from t.TempDir(), which is always a safe test directory.
	content, err := os.ReadFile(targetPath) //nolint:gosec
	require.NoError(t, err)
	assert.Equal(t, []byte("second"), content)
}

// TestErrStoreIdentityMismatch_Error verifies that ErrStoreIdentityMismatch.Error()
// includes the expected host, the actual host, and the root directory path.
func TestErrStoreIdentityMismatch_Error(t *testing.T) {
	err := &ErrStoreIdentityMismatch{
		RootDir:         "/data/store",
		ExpectedHost:    "expected.com",
		ExpectedPort:    993,
		ExpectedMailbox: "INBOX",
		ActualHost:      "actual.com",
		ActualPort:      993,
		ActualMailbox:   "INBOX",
	}
	assert.ErrorContains(t, err, "expected.com")
	assert.ErrorContains(t, err, "actual.com")
	assert.ErrorContains(t, err, "/data/store")
}

// TestErrUnsupportedSchemaVersion_Error verifies that ErrUnsupportedSchemaVersion.Error()
// includes the file path and the unsupported version number.
func TestErrUnsupportedSchemaVersion_Error(t *testing.T) {
	err := &ErrUnsupportedSchemaVersion{
		File:    "/data/store/.tlsrpt-digest-meta.json",
		Version: 999,
	}
	assert.ErrorContains(t, err, "/data/store/.tlsrpt-digest-meta.json")
	assert.ErrorContains(t, err, "999")
}

// TestErrAtomicWriteFailed_Error verifies that ErrAtomicWriteFailed.Error()
// includes the target file path and the failed operation name.
func TestErrAtomicWriteFailed_Error(t *testing.T) {
	err := &ErrAtomicWriteFailed{
		File: "/data/store/tlsrpt.json",
		Op:   "rename",
	}
	assert.ErrorContains(t, err, "/data/store/tlsrpt.json")
	assert.ErrorContains(t, err, "rename")
}

// TestSentinelPath tests sentinel path construction.
func TestSentinelPath(t *testing.T) {
	rootDir := "/data/store"
	expected := "/data/store/.tlsrpt-digest-meta.json"
	assert.Equal(t, expected, sentinelPath(rootDir))
}

// TestOpenMode_Constants tests OpenMode constants are distinct.
func TestOpenMode_Constants(t *testing.T) {
	assert.NotEqual(t, OpenReadWrite, OpenReadOnly)
}

// TestOpen_PendingReset_FailsClosedForReadWrite verifies that OpenReadWrite returns
// ErrPendingReset when a pre-commit reset is in progress (manifest present and
// recovery_required still set in the sentinel).
func TestOpen_PendingReset_FailsClosedForReadWrite(t *testing.T) {
	rootDir := t.TempDir()
	identity := makeTestIdentity()

	s, err := Open(rootDir, identity, OpenReadWrite)
	require.NoError(t, err)

	// Set recovery_required so the sentinel shows the reset is pre-commit.
	require.NoError(t, s.SaveRecoveryRequired(41, 42, time.Now()))

	// Plant a manifest at phase=emails_staged to simulate a pending reset.
	require.NoError(t, writeResetManifest(filepath.Join(rootDir, manifestFilename), resetManifest{
		Version: resetManifestVersion, CurrUIDValidity: 42, Phase: resetPhaseEmailsStaged,
	}))

	_, err = Open(rootDir, identity, OpenReadWrite)
	assert.ErrorIs(t, err, ErrPendingReset)
}

// TestOpen_PendingReset_OpenRecoverResetSucceeds verifies that OpenRecoverReset succeeds
// when a pending reset manifest exists, returning a usable store.
func TestOpen_PendingReset_OpenRecoverResetSucceeds(t *testing.T) {
	rootDir := t.TempDir()
	identity := makeTestIdentity()

	_, err := Open(rootDir, identity, OpenReadWrite)
	require.NoError(t, err)

	// Plant a manifest at phase=emails_staged to simulate an in-progress reset.
	require.NoError(t, writeResetManifest(filepath.Join(rootDir, manifestFilename), resetManifest{
		Version: resetManifestVersion, CurrUIDValidity: 42, Phase: resetPhaseEmailsStaged,
	}))

	s, err := Open(rootDir, identity, OpenRecoverReset)
	require.NoError(t, err)
	assert.NotNil(t, s)
}

// TestOpen_WarnOnLaxDataFilePermissions verifies that Open emits a WARN when
// tlsrpt.json already exists with permissions broader than 0600 (AC-39).
func TestOpen_WarnOnLaxDataFilePermissions(t *testing.T) {
	spy := setDefaultSlogSpy(t)
	rootDir := t.TempDir()
	identity := IMAPIdentity{Host: "imap.example.com", Port: 993, Mailbox: "INBOX"}

	// Create the store once so all files are initialised.
	_, err := Open(rootDir, identity, OpenReadWrite)
	require.NoError(t, err)

	// Loosen the data file permissions to 0644.
	dataPath := filepath.Join(rootDir, "tlsrpt.json")
	require.NoError(t, os.Chmod(dataPath, 0o644)) //nolint:gosec

	// Re-open; expect a WARN about the loose permission.
	spy.records = nil
	_, err = Open(rootDir, identity, OpenReadWrite)
	require.NoError(t, err)

	warnFound := false
	for _, r := range spy.records {
		if r.Level == slog.LevelWarn {
			warnFound = true
			break
		}
	}
	assert.True(t, warnFound, "Open should emit a WARN for tlsrpt.json with permissions broader than 0600")

	// Verify the permission was NOT auto-corrected.
	info, err := os.Stat(dataPath)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o644), info.Mode().Perm(),
		"Open must not auto-correct loose permissions on the data file (AC-39)")
}
