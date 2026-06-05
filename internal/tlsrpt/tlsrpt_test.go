package tlsrpt_test

import (
	"bytes"
	"compress/gzip"
	"errors"
	"net/mail"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/isseis/tlsrpt-digest/internal/mailparse"
	"github.com/isseis/tlsrpt-digest/internal/tlsrpt"
)

// gzipOf compresses data and returns the gzip bytes.
func gzipOf(data []byte) []byte {
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	_, _ = w.Write(data)
	_ = w.Close()
	return buf.Bytes()
}

// minimalValidJSON returns the smallest valid RFC 8460 JSON with all required fields.
func minimalValidJSON() []byte {
	return []byte(`{
		"organization-name": "Example Corp",
		"report-id": "report-001",
		"date-range": {
			"start-datetime": "2024-01-01T00:00:00Z",
			"end-datetime":   "2024-01-01T23:59:59Z"
		},
		"policies": []
	}`)
}

func TestParseGzip_Valid(t *testing.T) {
	r, err := tlsrpt.ParseGzip(gzipOf(minimalValidJSON()))
	require.NoError(t, err)
	assert.Equal(t, "Example Corp", r.OrganizationName)
	assert.Equal(t, "report-001", r.ReportID)
	assert.False(t, r.DateRange.StartDatetime.IsZero())
}

func TestParseJSON_Valid(t *testing.T) {
	r, err := tlsrpt.ParseJSON(minimalValidJSON())
	require.NoError(t, err)
	assert.Equal(t, "Example Corp", r.OrganizationName)
	assert.Equal(t, "report-001", r.ReportID)
	assert.False(t, r.DateRange.StartDatetime.IsZero())
}

func TestParseGzip_InvalidGzip(t *testing.T) {
	// gzip magic bytes followed by garbage
	data := []byte{0x1f, 0x8b, 0x00, 0x01, 0x02, 0x03}
	_, err := tlsrpt.ParseGzip(data)
	require.Error(t, err)
}

func TestParseGzip_InvalidJSONAfterDecompress(t *testing.T) {
	_, err := tlsrpt.ParseGzip(gzipOf([]byte("not json")))
	require.Error(t, err)
}

func TestParseJSON_InvalidJSON(t *testing.T) {
	_, err := tlsrpt.ParseJSON([]byte("not json"))
	require.Error(t, err)
}

// maxDecompressedSize mirrors the unexported constant in the tlsrpt package.
const maxDecompressedSize = 10 * 1024 * 1024

func TestParse_SizeLimitAtBoundary(t *testing.T) {
	// Exactly at the limit: must pass the size check (error, if any, comes from JSON parsing).
	exact := make([]byte, maxDecompressedSize)

	t.Run("ParseGzip", func(t *testing.T) {
		_, err := tlsrpt.ParseGzip(gzipOf(exact))
		var sizeErr *tlsrpt.ErrDecompressedSizeLimitExceeded
		assert.False(t, errors.As(err, &sizeErr),
			"payload at exact size limit must not trigger ErrDecompressedSizeLimitExceeded")
	})

	t.Run("ParseJSON", func(t *testing.T) {
		_, err := tlsrpt.ParseJSON(exact)
		var sizeErr *tlsrpt.ErrDecompressedSizeLimitExceeded
		assert.False(t, errors.As(err, &sizeErr),
			"payload at exact size limit must not trigger ErrDecompressedSizeLimitExceeded")
	})
}

func TestParse_SizeLimitExceeded(t *testing.T) {
	large := make([]byte, maxDecompressedSize+1)

	t.Run("ParseGzip", func(t *testing.T) {
		_, err := tlsrpt.ParseGzip(gzipOf(large))
		var sizeErr *tlsrpt.ErrDecompressedSizeLimitExceeded
		require.True(t, errors.As(err, &sizeErr), "expected ErrDecompressedSizeLimitExceeded, got %v", err)
		assert.Greater(t, sizeErr.Actual, sizeErr.Limit)
	})

	t.Run("ParseJSON", func(t *testing.T) {
		_, err := tlsrpt.ParseJSON(large)
		var sizeErr *tlsrpt.ErrDecompressedSizeLimitExceeded
		require.True(t, errors.As(err, &sizeErr), "expected ErrDecompressedSizeLimitExceeded, got %v", err)
		assert.Greater(t, sizeErr.Actual, sizeErr.Limit)
	})
}

func TestParse_MissingRequiredField(t *testing.T) {
	cases := []struct {
		name  string
		field string
		json  string
	}{
		{
			name:  "organization-name",
			field: "organization-name",
			json: `{
				"report-id": "r1",
				"date-range": {"start-datetime":"2024-01-01T00:00:00Z","end-datetime":"2024-01-01T23:59:59Z"},
				"policies": []
			}`,
		},
		{
			name:  "report-id",
			field: "report-id",
			json: `{
				"organization-name": "Corp",
				"date-range": {"start-datetime":"2024-01-01T00:00:00Z","end-datetime":"2024-01-01T23:59:59Z"},
				"policies": []
			}`,
		},
		{
			name:  "date-range absent",
			field: "date-range",
			json: `{
				"organization-name": "Corp",
				"report-id": "r1",
				"policies": []
			}`,
		},
		{
			name:  "date-range missing start-datetime",
			field: "date-range",
			json: `{
				"organization-name": "Corp",
				"report-id": "r1",
				"date-range": {"end-datetime":"2024-01-01T23:59:59Z"},
				"policies": []
			}`,
		},
		{
			name:  "date-range missing end-datetime",
			field: "date-range",
			json: `{
				"organization-name": "Corp",
				"report-id": "r1",
				"date-range": {"start-datetime":"2024-01-01T00:00:00Z"},
				"policies": []
			}`,
		},
		{
			name:  "policies",
			field: "policies",
			json: `{
				"organization-name": "Corp",
				"report-id": "r1",
				"date-range": {"start-datetime":"2024-01-01T00:00:00Z","end-datetime":"2024-01-01T23:59:59Z"}
			}`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tlsrpt.ParseJSON([]byte(tc.json))
			var missingErr *tlsrpt.ErrMissingRequiredField
			require.True(t, errors.As(err, &missingErr), "expected ErrMissingRequiredField, got %v", err)
			assert.Equal(t, tc.field, missingErr.Field)
		})
	}
}

func TestParse_PoliciesFields(t *testing.T) {
	data := []byte(`{
		"organization-name": "Corp",
		"report-id": "r1",
		"date-range": {"start-datetime":"2024-01-01T00:00:00Z","end-datetime":"2024-01-01T23:59:59Z"},
		"policies": [
			{
				"policy": {
					"policy-type": "sts",
					"policy-string": ["version: STSv1"],
					"policy-domain": "example.com",
					"mx-host": ["*.mail.example.com"]
				},
				"summary": {
					"total-successful-session-count": 10,
					"total-failure-session-count": 2
				},
				"failure-details": []
			}
		]
	}`)
	r, err := tlsrpt.ParseJSON(data)
	require.NoError(t, err)
	require.Len(t, r.Policies, 1)
	p := r.Policies[0]
	assert.Equal(t, "sts", p.Policy.PolicyType)
	assert.Equal(t, []string{"version: STSv1"}, p.Policy.PolicyString)
	assert.Equal(t, "example.com", p.Policy.PolicyDomain)
	assert.Equal(t, []string{"*.mail.example.com"}, p.Policy.MXHost)
	assert.Equal(t, int64(10), p.Summary.TotalSuccessfulSessionCount)
	assert.Equal(t, int64(2), p.Summary.TotalFailureSessionCount)
}

func TestParse_FailureDetails(t *testing.T) {
	data := []byte(`{
		"organization-name": "Corp",
		"report-id": "r1",
		"date-range": {"start-datetime":"2024-01-01T00:00:00Z","end-datetime":"2024-01-01T23:59:59Z"},
		"policies": [
			{
				"policy": {"policy-type":"sts","policy-domain":"example.com"},
				"summary": {"total-successful-session-count":0,"total-failure-session-count":1},
				"failure-details": [
					{
						"result-type": "certificate-expired",
						"sending-mta-ip": "192.0.2.1",
						"receiving-mx-hostname": "mx.example.com",
						"receiving-ip": "198.51.100.1",
						"failed-session-count": 1,
						"additional-information": "cert expired",
						"failure-reason-code": "foo"
					}
				]
			}
		]
	}`)
	r, err := tlsrpt.ParseJSON(data)
	require.NoError(t, err)
	require.Len(t, r.Policies[0].FailureDetails, 1)
	fd := r.Policies[0].FailureDetails[0]
	assert.Equal(t, "certificate-expired", fd.ResultType)
	assert.Equal(t, "192.0.2.1", fd.SendingMTAIP)
	assert.Equal(t, "mx.example.com", fd.ReceivingMXHostname)
	assert.Equal(t, "198.51.100.1", fd.ReceivingIP)
	assert.Equal(t, int64(1), fd.FailedSessionCount)
	assert.Equal(t, "cert expired", fd.AdditionalInformation)
	assert.Equal(t, "foo", fd.FailureReasonCode)
}

func TestHasFailure_AllZero(t *testing.T) {
	r := &tlsrpt.Report{
		OrganizationName: "Corp",
		ReportID:         "r1",
		DateRange:        tlsrpt.DateRange{StartDatetime: time.Now()},
		Policies: []tlsrpt.PolicyRecord{
			{Summary: tlsrpt.Summary{TotalFailureSessionCount: 0}},
			{Summary: tlsrpt.Summary{TotalFailureSessionCount: 0}},
		},
	}
	assert.False(t, r.HasFailure())
}

func TestHasFailure_AnyNonZero(t *testing.T) {
	r := &tlsrpt.Report{
		Policies: []tlsrpt.PolicyRecord{
			{Summary: tlsrpt.Summary{TotalFailureSessionCount: 0}},
			{Summary: tlsrpt.Summary{TotalFailureSessionCount: 1}},
		},
	}
	assert.True(t, r.HasFailure())
}

func TestHasFailure_EmptyPolicies(t *testing.T) {
	assert.False(t, (&tlsrpt.Report{Policies: []tlsrpt.PolicyRecord{}}).HasFailure())
	assert.False(t, (&tlsrpt.Report{Policies: nil}).HasFailure())
}

func TestParseRealReport(t *testing.T) {
	tests := []struct {
		filename    string
		wantFailure bool
	}{
		{
			filename:    "../../testdata/tlsrpt_success.eml",
			wantFailure: false,
		},
		{
			filename:    "../../testdata/tlsrpt_failure.eml",
			wantFailure: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.filename, func(t *testing.T) {
			data, err := os.ReadFile(tc.filename)
			require.NoError(t, err)

			msg, err := mail.ReadMessage(bytes.NewReader(data))
			require.NoError(t, err)

			attachments, err := mailparse.ExtractAttachments(msg, 10<<20)
			require.NoError(t, err)

			var parsed []*tlsrpt.Report
			for _, att := range attachments {
				name := strings.ToLower(att.Filename)
				var r *tlsrpt.Report
				var parseErr error
				// Dispatch by Content-Type (RFC 8460 mandated), fall back to filename suffix.
				switch {
				case att.ContentType == "application/tlsrpt+gzip" ||
					(att.ContentType == "" && strings.HasSuffix(name, ".json.gz")):
					r, parseErr = tlsrpt.ParseGzip(att.Content)
				case att.ContentType == "application/tlsrpt+json" ||
					(att.ContentType == "" && strings.HasSuffix(name, ".json")):
					r, parseErr = tlsrpt.ParseJSON(att.Content)
				default:
					continue
				}
				require.NoError(t, parseErr)
				parsed = append(parsed, r)
			}

			require.NotEmpty(t, parsed, "no .json.gz or .json attachments found in test email")
			for _, r := range parsed {
				assert.NotEmpty(t, r.OrganizationName, "OrganizationName should be set")
				assert.NotEmpty(t, r.ReportID, "ReportID should be set")
				assert.False(t, r.DateRange.StartDatetime.IsZero(), "DateRange.StartDatetime should be set")
				assert.False(t, r.DateRange.EndDatetime.IsZero(), "DateRange.EndDatetime should be set")
				assert.NotNil(t, r.Policies, "Policies should not be nil")
				assert.Equal(t, tc.wantFailure, r.HasFailure())
			}
		})
	}
}
