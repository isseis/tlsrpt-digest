// Package store provides persistent storage for TLSRPT reports and emails.
package store

import (
	"net/mail"
	"time"

	"github.com/isseis/tlsrpt-digest/internal/tlsrpt"
)

// OpenMode represents the file opening mode for the store.
type OpenMode int

const (
	// OpenReadWrite opens the store in read-write mode.
	// Creates root_dir, emails/, tlsrpt.json, and sentinel if they don't exist.
	OpenReadWrite OpenMode = iota
	// OpenReadOnly opens the store in read-only mode.
	// Does not create any files; returns empty state if files don't exist.
	OpenReadOnly
)

// IMAPIdentity represents the IMAP server and mailbox identity.
type IMAPIdentity struct {
	Host    string // IMAP server hostname
	Port    int    // IMAP server port (typically 993 for SSL/TLS)
	Mailbox string // IMAP mailbox name (e.g., "INBOX")
}

// EmailMeta represents the metadata of a saved email.
type EmailMeta struct {
	UID         uint32    // IMAP UID
	UIDValidity uint32    // IMAP UIDVALIDITY
	SentAt      time.Time // Send date from email Date header; fallback to SavedAt if missing
	SavedAt     time.Time // File ctime (inode change time) when saved
}

// ReportInput represents a TLSRPT report to be saved along with its email context.
type ReportInput struct {
	Report      tlsrpt.Report // Parsed TLSRPT report
	UID         uint32        // IMAP UID of the email containing this report
	UIDValidity uint32        // IMAP UIDVALIDITY at the time of saving
}

// LoadedEmail represents an email loaded from storage.
type LoadedEmail struct {
	Message     *mail.Message // Parsed email message
	UID         uint32        // IMAP UID
	UIDValidity uint32        // IMAP UIDVALIDITY
	SentAt      time.Time     // Send date from email Date header; fallback to SavedAt if missing
	SavedAt     time.Time     // File ctime (inode change time) when saved
	Path        string        // Relative path within {root_dir}/emails/ (e.g., "1234567890/202605/0000000123.eml")
}

// internalDataFile represents the structure of tlsrpt.json.
// This type is internal and not exposed to callers.
// Used in Phase 2 implementation (SaveReports, GetReportsSince, DeleteReportsBefore)
type internalDataFile struct { //nolint:unused
	Version int                          `json:"version"`
	Reports []tlsrpt.Report              `json:"reports"`
	Emails  []internalEmailIndexEntry    `json:"emails"`
}

// internalEmailIndexEntry represents a single email index entry in tlsrpt.json.
// Used in Phase 2 implementation (SaveEmailMetas, LoadEmails)
type internalEmailIndexEntry struct { //nolint:unused
	UID           uint32     `json:"uid"`
	UIDValidity   uint32     `json:"uidvalidity"`
	SentAt        time.Time  `json:"sent_at"`
	SavedAt       time.Time  `json:"saved_at"`
	ReportEndDate *time.Time `json:"report_end_date"` // Null if parse failed
}

// internalSentinelFile represents the structure of .tlsrpt-digest-meta.json.
type internalSentinelFile struct {
	FormatVersion    int                    `json:"format_version"`
	IMAPHost         string                 `json:"imap_host"`
	IMAPPort         int                    `json:"imap_port"`
	IMAPMailbox      string                 `json:"imap_mailbox"`
	InitializedAt    time.Time              `json:"initialized_at"`
	UIDValidity      *uint32                `json:"uid_validity,omitempty"`      // Omitted if not yet set
	RecoveryRequired *internalRecoveryState `json:"recovery_required,omitempty"` // Omitted if not required
}

// internalRecoveryState represents the recovery_required field in sentinel.
type internalRecoveryState struct {
	PrevUIDValidity uint32    `json:"prev_uid_validity"`
	CurrUIDValidity uint32    `json:"curr_uid_validity"`
	DetectedAt      time.Time `json:"detected_at"`
}

// SentinelFormatVersion is the current sentinel file format version.
const SentinelFormatVersion = 1

// DataFileVersion is the current tlsrpt.json format version.
const DataFileVersion = 1
