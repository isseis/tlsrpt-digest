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

func TestLogSummary_OrganizationStats_Serialized(t *testing.T) {
	var spy spyHandler
	require.NoError(t, notify.LogSummary(context.Background(), &spy, notify.Summary{
		Period: notify.DateRange{Start: time.Now(), End: time.Now()},
		OrganizationStats: map[string]int64{
			"org-b": 20,
			"org-a": 10,
		},
		ReportCount: 2,
	}))
	require.Len(t, spy.records, 1)

	stats := summaryOrganizationStats(t, spy.records[0])
	assert.Equal(t, map[string]int64{"org-a": 10, "org-b": 20}, stats)
}

func TestLogSummary_OrganizationStats_SortedKeys(t *testing.T) {
	var spy spyHandler
	require.NoError(t, notify.LogSummary(context.Background(), &spy, notify.Summary{
		Period: notify.DateRange{Start: time.Now(), End: time.Now()},
		OrganizationStats: map[string]int64{
			"org-c": 30,
			"org-a": 10,
			"org-b": 20,
		},
	}))
	require.Len(t, spy.records, 1)

	group := summaryOrganizationStatsGroup(t, spy.records[0])
	keys := make([]string, 0, len(group))
	for _, attr := range group {
		keys = append(keys, attr.Key)
	}
	assert.Equal(t, []string{"org-a", "org-b", "org-c"}, keys)
}

func TestLogSummary_EmptyOrganizationStats(t *testing.T) {
	var spy spyHandler
	require.NotPanics(t, func() {
		require.NoError(t, notify.LogSummary(context.Background(), &spy, notify.Summary{
			Period:            notify.DateRange{Start: time.Now(), End: time.Now()},
			OrganizationStats: map[string]int64{},
		}))
	})
	require.Len(t, spy.records, 1)
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

func summaryOrganizationStats(t *testing.T, record slog.Record) map[string]int64 {
	t.Helper()
	stats := make(map[string]int64)
	for _, attr := range summaryOrganizationStatsGroup(t, record) {
		stats[attr.Key] = attr.Value.Int64()
	}
	return stats
}

func summaryOrganizationStatsGroup(t *testing.T, record slog.Record) []slog.Attr {
	t.Helper()
	var group []slog.Attr
	record.Attrs(func(attr slog.Attr) bool {
		if attr.Key == "organization_stats" {
			require.Equal(t, slog.KindGroup, attr.Value.Kind())
			group = attr.Value.Group()
		}
		return true
	})
	require.NotNil(t, group, "organization_stats group not found")
	return group
}
