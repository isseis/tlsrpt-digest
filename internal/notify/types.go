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

// SystemError is the notification payload for a system-level error event.
type SystemError struct {
	ErrorType string
	Message   string
	Component string
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
