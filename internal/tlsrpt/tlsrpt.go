// Package tlsrpt decodes and parses RFC 8460 TLSRPT report JSON.
package tlsrpt

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"time"
)

const maxDecompressedSize = 10 * 1024 * 1024 // 10 MB

// ErrDecompressedSizeLimitExceeded is returned when the data size exceeds the limit.
type ErrDecompressedSizeLimitExceeded struct {
	Limit  int64
	Actual int64
}

func (e *ErrDecompressedSizeLimitExceeded) Error() string {
	return fmt.Sprintf("tlsrpt: size %d exceeds limit %d", e.Actual, e.Limit)
}

// ErrMissingRequiredField is returned when a required top-level field is absent.
type ErrMissingRequiredField struct {
	Field string
}

func (e *ErrMissingRequiredField) Error() string {
	return fmt.Sprintf("tlsrpt: missing required field: %s", e.Field)
}

// Report is the top-level RFC 8460 TLSRPT report structure.
type Report struct {
	OrganizationName string         `json:"organization-name"`
	ReportID         string         `json:"report-id"`
	DateRange        DateRange      `json:"date-range"`
	Policies         []PolicyRecord `json:"policies"`
}

// HasFailure reports whether any policy record has a non-zero total-failure-session-count.
func (r *Report) HasFailure() bool {
	for i := range r.Policies {
		if r.Policies[i].Summary.TotalFailureSessionCount > 0 {
			return true
		}
	}
	return false
}

// DateRange holds the reporting period.
type DateRange struct {
	StartDatetime time.Time `json:"start-datetime"`
	EndDatetime   time.Time `json:"end-datetime"`
}

// PolicyRecord holds per-policy results.
type PolicyRecord struct {
	Policy         Policy          `json:"policy"`
	Summary        Summary         `json:"summary"`
	FailureDetails []FailureDetail `json:"failure-details"`
}

// Policy describes the evaluated policy.
type Policy struct {
	PolicyType   string   `json:"policy-type"`
	PolicyString []string `json:"policy-string"`
	PolicyDomain string   `json:"policy-domain"`
	MXHost       []string `json:"mx-host"`
}

// Summary holds aggregate session counts for a policy record.
type Summary struct {
	TotalSuccessfulSessionCount int64 `json:"total-successful-session-count"`
	TotalFailureSessionCount    int64 `json:"total-failure-session-count"`
}

// FailureDetail describes a single failure event.
type FailureDetail struct {
	ResultType            string `json:"result-type"`
	SendingMTAIP          string `json:"sending-mta-ip"`
	ReceivingMXHostname   string `json:"receiving-mx-hostname"`
	ReceivingIP           string `json:"receiving-ip"`
	FailedSessionCount    int64  `json:"failed-session-count"`
	AdditionalInformation string `json:"additional-information"`
	FailureReasonCode     string `json:"failure-reason-code"`
}

// ParseGzip decompresses gzip data and parses it as an RFC 8460 report.
// The caller determines the format from the attachment filename or Content-Type.
func ParseGzip(data []byte) (*Report, error) {
	gr, err := gzip.NewReader(
		&io.LimitedReader{R: bytes.NewReader(data), N: int64(maxDecompressedSize) + 1},
	)
	if err != nil {
		return nil, fmt.Errorf("tlsrpt: decompress: %w", err)
	}
	decompressed, readErr := io.ReadAll(gr)
	closeErr := gr.Close()
	if readErr != nil {
		return nil, fmt.Errorf("tlsrpt: decompress: %w", readErr)
	}
	if closeErr != nil {
		return nil, fmt.Errorf("tlsrpt: decompress: %w", closeErr)
	}
	if len(decompressed) > maxDecompressedSize {
		return nil, &ErrDecompressedSizeLimitExceeded{
			Limit:  maxDecompressedSize,
			Actual: int64(len(decompressed)),
		}
	}
	return parseJSON(decompressed)
}

// ParseJSON parses plain JSON data as an RFC 8460 report.
func ParseJSON(data []byte) (*Report, error) {
	if len(data) > maxDecompressedSize {
		return nil, &ErrDecompressedSizeLimitExceeded{
			Limit:  maxDecompressedSize,
			Actual: int64(len(data)),
		}
	}
	return parseJSON(data)
}

// parseJSON unmarshals JSON and validates required fields.
func parseJSON(data []byte) (*Report, error) {
	var r Report
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("tlsrpt: parse json: %w", err)
	}
	if r.OrganizationName == "" {
		return nil, &ErrMissingRequiredField{Field: "organization-name"}
	}
	if r.ReportID == "" {
		return nil, &ErrMissingRequiredField{Field: "report-id"}
	}
	if r.DateRange.StartDatetime.IsZero() && r.DateRange.EndDatetime.IsZero() {
		return nil, &ErrMissingRequiredField{Field: "date-range"}
	}
	if r.Policies == nil {
		return nil, &ErrMissingRequiredField{Field: "policies"}
	}
	return &r, nil
}
