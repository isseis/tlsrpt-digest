// Package store provides persistent storage for TLSRPT reports and emails.
package store

import (
	"errors"
	"fmt"
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
	Err  error  // Underlying error
}

func (e *ErrAtomicWriteFailed) Error() string {
	return fmt.Sprintf("store: atomic write failed: file=%s op=%s: %v", e.File, e.Op, e.Err)
}

func (e *ErrAtomicWriteFailed) Unwrap() error {
	return e.Err
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
	return fmt.Sprintf("store: load email failed: path=%s: %v", e.Path, e.Err)
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
	// Underlying error (wrapped via Unwrap())
	Err error
}

func (e *ErrDeleteEmailFailed) Error() string {
	return fmt.Sprintf(
		"store: delete email failed: path=%s uid=%d uidvalidity=%d: %v",
		e.Path, e.UID, e.UIDValidity, e.Err,
	)
}

func (e *ErrDeleteEmailFailed) Unwrap() error {
	return e.Err
}

// ErrPendingReset is returned by Open(OpenReadWrite) when a pending reset manifest
// exists. Use OpenRecoverReset to open the store and resume or abort the reset.
var ErrPendingReset = errors.New("store: pending reset detected; use OpenRecoverReset to continue or abort")

// ErrRecoveryRequiredMissing is returned by ResetForRecovery when the sentinel
// does not contain a recovery-required entry.
var ErrRecoveryRequiredMissing = errors.New("store: recovery-required not present in sentinel")

// ErrRecoveryUIDValidityMismatch is returned by ResetForRecovery when the supplied
// currUIDValidity does not match the current UIDVALIDITY recorded in recovery-required.
type ErrRecoveryUIDValidityMismatch struct {
	Got      uint32
	Expected uint32
}

func (e *ErrRecoveryUIDValidityMismatch) Error() string {
	return fmt.Sprintf(
		"store: recovery uid_validity mismatch: got=%d expected=%d",
		e.Got, e.Expected,
	)
}

// ErrResetNotPending is returned by AbortReset when there is no pending reset
// (manifest absent) or when the reset has already been committed.
var ErrResetNotPending = errors.New("store: no pending reset to abort")

// ErrInvalidStoreMode is returned when an operation is called on a store opened
// in an incompatible mode (e.g., calling ResetForRecovery on an OpenReadWrite store).
var ErrInvalidStoreMode = errors.New("store: operation not valid for current open mode")

// ErrResetManifestVersionMismatch is returned by ResetForRecovery when the on-disk
// manifest was written by a different (unsupported) version of the reset protocol.
type ErrResetManifestVersionMismatch struct {
	Got  int
	Want int
}

func (e *ErrResetManifestVersionMismatch) Error() string {
	return fmt.Sprintf("store: unexpected reset manifest version: got=%d want=%d", e.Got, e.Want)
}

// ErrResetManifestPhaseUnknown is returned when the on-disk manifest carries a
// phase value outside the known range.  Treated as fail-closed so callers must
// resolve the inconsistency manually rather than risk silent cleanup.
type ErrResetManifestPhaseUnknown struct {
	Got int
}

func (e *ErrResetManifestPhaseUnknown) Error() string {
	return fmt.Sprintf("store: unknown reset manifest phase: got=%d", e.Got)
}

// ErrResetAbortInProgress is returned by ResetForRecovery when the on-disk
// manifest indicates that an AbortReset is partially applied (phase=aborting).
// In this state, AbortReset must be re-run to complete the restore and clean
// up the manifest; continuing the original reset would commit on top of data
// that AbortReset has already moved back to the root.
var ErrResetAbortInProgress = errors.New("store: abort reset in progress; re-run AbortReset to finish")
