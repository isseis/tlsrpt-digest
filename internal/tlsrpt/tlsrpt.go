// Package tlsrpt decodes and parses RFC 8460 TLSRPT report JSON.
package tlsrpt

import (
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"time"
)

const maxDecompressedSize = 10 * 1024 * 1024 // 10 MB

// ErrDecompressedSizeLimitExceeded is returned when the decompressed size exceeds the limit.
type ErrDecompressedSizeLimitExceeded struct {
	Limit  int64
	Actual int64
}

func (e *ErrDecompressedSizeLimitExceeded) Error() string {
	return fmt.Sprintf("tlsrpt: decompressed size %d exceeds limit %d", e.Actual, e.Limit)
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

// Parse decodes data (gzip-compressed or plain JSON) and parses it as an RFC 8460 report.
func Parse(data []byte) (*Report, error) {
	var jsonData []byte

	if len(data) >= 2 && data[0] == 0x1f && data[1] == 0x8b {
		// gzip path
		br := byteReader(data)
		gr, err := gzip.NewReader(
			&io.LimitedReader{R: &br, N: int64(maxDecompressedSize) + 1},
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
		jsonData = decompressed
	} else {
		// plain JSON path
		if len(data) > maxDecompressedSize {
			return nil, &ErrDecompressedSizeLimitExceeded{
				Limit:  maxDecompressedSize,
				Actual: int64(len(data)),
			}
		}
		jsonData = data
	}

	var r Report
	if err := json.Unmarshal(jsonData, &r); err != nil {
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

// byteReader wraps a byte slice as an io.Reader for gzip.NewReader.
type byteReader []byte

func (b *byteReader) Read(p []byte) (n int, err error) {
	if len(*b) == 0 {
		return 0, io.EOF
	}
	n = copy(p, *b)
	*b = (*b)[n:]
	return n, nil
}
