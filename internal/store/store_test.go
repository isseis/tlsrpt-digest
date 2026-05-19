package store

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestOpen_ReadWriteMode_CreatesDirectories tests AC-01, AC-03, AC-04, AC-05.
// Verifies that Open with OpenReadWrite mode creates root_dir, emails/, and sentinel.
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
}

// TestOpen_ReadOnlyMode_NoCreation tests AC-02.
// Verifies that Open with OpenReadOnly mode does not create directories if they don't exist.
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

// TestOpen_IdentityVerification tests AC-06.
// Verifies that Open with mismatched identity returns ErrStoreIdentityMismatch.
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

// TestOpen_ReadOnlyMode_ReadEmptyStore tests AC-02.
// Verifies that read-only open on non-existent store doesn't error and returns valid Store.
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

// TestOpen_MultipleOpens_SameIdentity tests AC-04, AC-06.
// Verifies that opening the same store twice with same identity succeeds.
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

// TestOpen_FilePermissions tests AC-37.
// Verifies that sentinel and emails directory are created with secure permissions.
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

// TestOpen_DirPermissions tests AC-38.
// Verifies that MkdirAll respects 0700 for newly created intermediate directories.
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

// TestOpen_SentinelSchema tests AC-01, AC-03.
// Verifies that sentinel is created with correct schema and identity.
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

// TestOpen_ModeConstants tests AC-01, AC-02.
// Verifies that OpenReadWrite and OpenReadOnly constants are defined correctly.
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

// TestErrStoreIdentityMismatch_Error tests error message format.
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

	msg := err.Error()
	assert.Contains(t, msg, "identity mismatch")
	assert.Contains(t, msg, "expected.com")
	assert.Contains(t, msg, "actual.com")
}

// TestErrUnsupportedSchemaVersion_Error tests error message format.
func TestErrUnsupportedSchemaVersion_Error(t *testing.T) {
	err := &ErrUnsupportedSchemaVersion{
		File:    "/data/store/.tlsrpt-digest-meta.json",
		Version: 999,
	}

	msg := err.Error()
	assert.Contains(t, msg, "unsupported schema version")
	assert.Contains(t, msg, "999")
}

// TestErrAtomicWriteFailed_Error tests error message format.
func TestErrAtomicWriteFailed_Error(t *testing.T) {
	err := &ErrAtomicWriteFailed{
		File: "/data/store/tlsrpt.json",
		Op:   "rename",
	}

	msg := err.Error()
	assert.Contains(t, msg, "atomic write failed")
	assert.Contains(t, msg, "rename")
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
