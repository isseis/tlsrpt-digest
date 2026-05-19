// Package store provides persistent storage for TLSRPT reports and emails.
package store

import (
	"errors"
	"fmt"
	"time"
)

// ErrReadOnly is returned when a write operation is attempted on a store
// opened in read-only mode.
var ErrReadOnly = errors.New("store: cannot write in read-only mode")

// ErrStoreIdentityMismatch is returned when the sentinel's IMAP identity
// does not match the expected identity provided to Open.
type ErrStoreIdentityMismatch struct {
	RootDir         string
	ExpectedHost    string
	ExpectedPort    int
	ExpectedMailbox string
	ActualHost      string
	ActualPort      int
	ActualMailbox   string
}

func (e *ErrStoreIdentityMismatch) Error() string {
	return fmt.Sprintf(
		"store: identity mismatch: root_dir=%s expected=%s:%d/%s actual=%s:%d/%s",
		e.RootDir,
		e.ExpectedHost, e.ExpectedPort, e.ExpectedMailbox,
		e.ActualHost, e.ActualPort, e.ActualMailbox,
	)
}

// ErrUnsupportedSchemaVersion is returned when the schema version of a file
// (sentinel or data file) is not supported.
type ErrUnsupportedSchemaVersion struct {
	File    string
	Version int
}

func (e *ErrUnsupportedSchemaVersion) Error() string {
	return fmt.Sprintf("store: unsupported schema version: file=%s version=%d", e.File, e.Version)
}

// ErrAtomicWriteFailed is returned when an atomic write operation fails.
type ErrAtomicWriteFailed struct {
	File string
	Op   string // Operation that failed (e.g., "write", "sync", "rename")
}

func (e *ErrAtomicWriteFailed) Error() string {
	return fmt.Sprintf("store: atomic write failed: file=%s op=%s", e.File, e.Op)
}

// ErrInvalidEmailPath is returned when an email path does not match
// the expected format {uidvalidity}/{YYYYMM}/{uid}.eml.
type ErrInvalidEmailPath struct {
	Path string
}

func (e *ErrInvalidEmailPath) Error() string {
	return fmt.Sprintf("store: invalid email path: path=%s", e.Path)
}

// ErrLoadEmailFailed is returned when a single email file cannot be loaded.
// Multiple failures are aggregated using errors.Join.
type ErrLoadEmailFailed struct {
	Path string
	// Underlying error (wrapped via Unwrap())
	Err error
}

func (e *ErrLoadEmailFailed) Error() string {
	return fmt.Sprintf("store: load email failed: path=%s", e.Path)
}

func (e *ErrLoadEmailFailed) Unwrap() error {
	return e.Err
}

// ErrDeleteEmailFailed is returned when a single email file cannot be deleted
// during DeleteEmailsBefore. Multiple failures are aggregated using errors.Join.
type ErrDeleteEmailFailed struct {
	Path        string
	UID         uint32
	UIDValidity uint32
	SavedAt     time.Time
	// Underlying error (wrapped via Unwrap())
	Err error
}

func (e *ErrDeleteEmailFailed) Error() string {
	return fmt.Sprintf(
		"store: delete email failed: path=%s uid=%d uidvalidity=%d saved_at=%s",
		e.Path, e.UID, e.UIDValidity, e.SavedAt.Format("2006-01-02T15:04:05Z07:00"),
	)
}

func (e *ErrDeleteEmailFailed) Unwrap() error {
	return e.Err
}
