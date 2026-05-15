package notify_test

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/isseis/tlsrpt-digest/internal/notify"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// spyHandler records the slog.Record received by Handle().
type spyHandler struct {
	records []slog.Record
}

func (s *spyHandler) Enabled(_ context.Context, _ slog.Level) bool { return true }
func (s *spyHandler) Handle(_ context.Context, r slog.Record) error {
	s.records = append(s.records, r.Clone())
	return nil
}
func (s *spyHandler) WithAttrs(_ []slog.Attr) slog.Handler { return s }
func (s *spyHandler) WithGroup(_ string) slog.Handler      { return s }

func TestLogAlert_Level(t *testing.T) {
	var spy spyHandler
	require.NoError(t, notify.LogAlert(context.Background(), &spy, notify.Alert{
		OrganizationName: "example.com",
		PolicyType:       notify.PolicyTypeSTS,
		FailureCount:     3,
	}))
	require.Len(t, spy.records, 1)
	assert.Equal(t, slog.LevelWarn, spy.records[0].Level)
}

func TestLogSystemError_Level(t *testing.T) {
	var spy spyHandler
	require.NoError(t, notify.LogSystemError(context.Background(), &spy, notify.SystemError{
		ErrorType: "IMAPError", Message: "conn dropped", Component: "imap",
	}))
	require.Len(t, spy.records, 1)
	assert.Equal(t, slog.LevelError, spy.records[0].Level)
}

func TestLogSummary_Level(t *testing.T) {
	var spy spyHandler
	require.NoError(t, notify.LogSummary(context.Background(), &spy, notify.Summary{
		Period: notify.DateRange{Start: time.Now(), End: time.Now()},
	}))
	require.Len(t, spy.records, 1)
	assert.Equal(t, slog.LevelInfo, spy.records[0].Level)
}

func TestLogAlert_StructuredPayloadOnly(t *testing.T) {
	var spy spyHandler
	require.NoError(t, notify.LogAlert(context.Background(), &spy, notify.Alert{
		OrganizationName: "example.com",
		PolicyType:       notify.PolicyTypeSTS,
		FailureCount:     2,
	}))
	require.Len(t, spy.records, 1)
	r := spy.records[0]

	// Record must contain Alert fields only — no raw strings, no config fields.
	var foundOrgName, foundPolicyType, foundFailureCount bool
	r.Attrs(func(attr slog.Attr) bool {
		switch attr.Key {
		case "organization_name":
			foundOrgName = true
		case "policy_type":
			foundPolicyType = true
		case "failure_count":
			foundFailureCount = true
		}
		return true
	})
	assert.True(t, foundOrgName)
	assert.True(t, foundPolicyType)
	assert.True(t, foundFailureCount)
}
