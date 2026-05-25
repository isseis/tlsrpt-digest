package notify

import (
	"context"
	"time"
)

// PolicyType represents an RFC 8460 policy-type value.
type PolicyType string

// RFC 8460 policy-type constants.
const (
	PolicyTypeSTS           PolicyType = "sts"
	PolicyTypeTLSA          PolicyType = "tlsa"
	PolicyTypeNoPolicyFound PolicyType = "no-policy-found"
	// PolicyTypeUnknown is used for unrecognised or missing policy-type values.
	PolicyTypeUnknown PolicyType = ""
)

// DateRange represents a reporting period with start and end timestamps.
// It mirrors tlsrpt.DateRange but is defined here to keep internal/notify
// independent of internal/tlsrpt.
type DateRange struct {
	Start time.Time
	End   time.Time
}

// Alert is the notification payload for a TLS failure event.
// It contains only public information; no sensitive fields.
type Alert struct {
	OrganizationName string
	PolicyType       PolicyType
	FailureCount     int64
	DateRange        DateRange
}

// WarningKind classifies a non-failure fetch warning.
type WarningKind string

const (
	// WarningKindSizeMismatch indicates the IMAP RFC822.SIZE differs from the local file size.
	WarningKindSizeMismatch WarningKind = "size_mismatch"
	// WarningKindParseFailure indicates a TLSRPT attachment could not be parsed.
	WarningKindParseFailure WarningKind = "parse_failure"
)

// Warning is the notification payload for a non-failure fetch warning event.
// It contains only public information; no sensitive fields.
type Warning struct {
	Kind        WarningKind
	UID         uint32
	UIDValidity uint32
	MessageID   string
}

// SystemErrorKind classifies a system-level error for Slack notification.
type SystemErrorKind string

// SystemErrorKind classification constants. Only the listed values are valid.
const (
	SystemErrorKindLockHeld                SystemErrorKind = "lock_held"
	SystemErrorKindStoreIdentityMismatch   SystemErrorKind = "store_identity_mismatch"
	SystemErrorKindStorePermission         SystemErrorKind = "store_permission"
	SystemErrorKindStoreCorruption         SystemErrorKind = "store_corruption"
	SystemErrorKindIMAPCredentialsMissing  SystemErrorKind = "imap_credentials_missing"
	SystemErrorKindIMAPConnectFailed       SystemErrorKind = "imap_connect_failed"
	SystemErrorKindIMAPAuthFailed          SystemErrorKind = "imap_auth_failed"
	SystemErrorKindIMAPOperationFailed     SystemErrorKind = "imap_operation_failed"
	SystemErrorKindUIDValidityChanged      SystemErrorKind = "uidvalidity_changed"
	SystemErrorKindRecoveryRequired        SystemErrorKind = "recovery_required"
	SystemErrorKindResetIncomplete         SystemErrorKind = "reset_incomplete"
	SystemErrorKindNotificationFlushFailed SystemErrorKind = "notification_flush_failed"
)

// SystemError is the notification payload for a system-level error event.
// It contains only public classification and component information;
// no raw error strings, file paths, server responses, or credentials.
type SystemError struct {
	Kind      SystemErrorKind
	Component string
	Mailbox   string
}

// Summary is the notification payload for a periodic summary event.
// OrganizationStats maps organization name to total successful session count.
// OrganizationCount is derived via len(OrganizationStats) and is not stored.
type Summary struct {
	Period            DateRange
	OrganizationStats map[string]int64
	ReportCount       int64
}

// Flusher is implemented by SlackHandler in addition to slog.Handler.
// Flush sends all buffered records as one or more Slack messages.
type Flusher interface {
	Flush(ctx context.Context) error
}
