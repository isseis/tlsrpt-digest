package notify_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/isseis/tlsrpt-digest/internal/notify"
	"github.com/isseis/tlsrpt-digest/internal/store/testutil"
	"github.com/isseis/tlsrpt-digest/internal/tlsrpt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGenerateSummary_FiltersByPeriod(t *testing.T) {
	start := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 5, 8, 0, 0, 0, 0, time.UTC)
	st := fakeStoreWithReports(
		summaryReport("before", "org-before", start.Add(-time.Hour), 10, 0),
		summaryReport("inside", "org-inside", start.Add(time.Hour), 20, 0),
		summaryReport("after", "org-after", end.Add(time.Hour), 30, 0),
	)

	summary, err := notify.GenerateSummary(context.Background(), st, start, end, nil)

	require.NoError(t, err)
	assert.Equal(t, int64(1), summary.ReportCount)
	assert.Equal(t, map[string]int64{"org-inside": 20}, summary.OrganizationStats)
}

func TestGenerateSummary_StartBoundaryExclusion(t *testing.T) {
	start := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 5, 8, 0, 0, 0, 0, time.UTC)
	st := fakeStoreWithReports(summaryReport("at-start", "org-a", start, 10, 0))

	summary, err := notify.GenerateSummary(context.Background(), st, start, end, nil)

	require.NoError(t, err)
	assert.Zero(t, summary.ReportCount)
	assert.Empty(t, summary.OrganizationStats)
}

func TestGenerateSummary_EndBoundaryInclusion(t *testing.T) {
	start := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 5, 8, 0, 0, 0, 0, time.UTC)
	st := fakeStoreWithReports(summaryReport("at-end", "org-a", end, 10, 0))

	summary, err := notify.GenerateSummary(context.Background(), st, start, end, nil)

	require.NoError(t, err)
	assert.Equal(t, int64(1), summary.ReportCount)
	assert.Equal(t, map[string]int64{"org-a": 10}, summary.OrganizationStats)
}

func TestGenerateSummary_ExcludesFailureReports(t *testing.T) {
	start := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 5, 8, 0, 0, 0, 0, time.UTC)
	st := fakeStoreWithReports(
		summaryReport("success", "org-a", start.Add(time.Hour), 10, 0),
		summaryReport("failure", "org-b", start.Add(2*time.Hour), 20, 1),
	)

	summary, err := notify.GenerateSummary(context.Background(), st, start, end, nil)

	require.NoError(t, err)
	assert.Equal(t, int64(1), summary.ReportCount)
	assert.Equal(t, map[string]int64{"org-a": 10}, summary.OrganizationStats)
}

func TestGenerateSummary_SumsSuccessfulSessions(t *testing.T) {
	start := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 5, 8, 0, 0, 0, 0, time.UTC)
	st := fakeStoreWithReports(
		summaryReport("first", "org-a", start.Add(time.Hour), 10, 0),
		summaryReport("second", "org-a", start.Add(2*time.Hour), 15, 0),
		summaryReport("third", "org-b", start.Add(3*time.Hour), 7, 0),
	)

	summary, err := notify.GenerateSummary(context.Background(), st, start, end, nil)

	require.NoError(t, err)
	assert.Equal(t, int64(3), summary.ReportCount)
	assert.Equal(t, map[string]int64{"org-a": 25, "org-b": 7}, summary.OrganizationStats)
}

func TestGenerateSummary_SumsSuccessfulSessionsAcrossPolicies(t *testing.T) {
	start := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 5, 8, 0, 0, 0, 0, time.UTC)
	report := summaryReport("multi-policy", "org-a", start.Add(time.Hour), 10, 0)
	report.Policies = append(report.Policies, tlsrpt.PolicyRecord{
		Summary: tlsrpt.Summary{TotalSuccessfulSessionCount: 15},
	})
	st := fakeStoreWithReports(report)

	summary, err := notify.GenerateSummary(context.Background(), st, start, end, nil)

	require.NoError(t, err)
	assert.Equal(t, int64(1), summary.ReportCount)
	assert.Equal(t, map[string]int64{"org-a": 25}, summary.OrganizationStats)
}

func TestGenerateSummary_PeriodInSummary(t *testing.T) {
	start := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 5, 8, 0, 0, 0, 0, time.UTC)
	st := fakeStoreWithReports(summaryReport("inside", "org-a", start.Add(time.Hour), 10, 0))

	summary, err := notify.GenerateSummary(context.Background(), st, start, end, nil)

	require.NoError(t, err)
	assert.Equal(t, notify.DateRange{Start: start, End: end}, summary.Period)
}

func TestGenerateSummary_EmptyPeriod(t *testing.T) {
	start := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 5, 8, 0, 0, 0, 0, time.UTC)
	st := fakeStoreWithReports(summaryReport("outside", "org-a", end.Add(time.Hour), 10, 0))

	summary, err := notify.GenerateSummary(context.Background(), st, start, end, nil)

	require.NoError(t, err)
	assert.Equal(t, notify.DateRange{Start: start, End: end}, summary.Period)
	assert.Zero(t, summary.ReportCount)
	assert.Empty(t, summary.OrganizationStats)
	assert.NotNil(t, summary.OrganizationStats)
}

func TestGenerateSummary_MixedReportWarning(t *testing.T) {
	start := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 5, 8, 0, 0, 0, 0, time.UTC)
	st := fakeStoreWithReports(summaryReport("mixed", "org-mixed", start.Add(time.Hour), 42, 1))
	var buf bytes.Buffer
	debugLogger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	_, err := notify.GenerateSummary(context.Background(), st, start, end, debugLogger)

	require.NoError(t, err)
	logText := buf.String()
	assert.Contains(t, logText, "org-mixed")
	assert.Contains(t, logText, "2026-05-01")
	assert.Contains(t, logText, "2026-05-08")
	assert.Contains(t, logText, "successful_session_count=42")
}

func TestGenerateSummary_MixedReportNotInStats(t *testing.T) {
	start := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 5, 8, 0, 0, 0, 0, time.UTC)
	st := fakeStoreWithReports(
		summaryReport("success", "org-a", start.Add(time.Hour), 10, 0),
		summaryReport("mixed", "org-a", start.Add(2*time.Hour), 42, 1),
	)

	summary, err := notify.GenerateSummary(context.Background(), st, start, end, nil)

	require.NoError(t, err)
	assert.Equal(t, int64(1), summary.ReportCount)
	assert.Equal(t, map[string]int64{"org-a": 10}, summary.OrganizationStats)
}

func TestGenerateSummary_ContextCanceledBeforeStoreRead(t *testing.T) {
	start := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 5, 8, 0, 0, 0, 0, time.UTC)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	st := &trackingStore{FakeStore: storetestutil.NewFakeStore()}

	_, err := notify.GenerateSummary(ctx, st, start, end, nil)

	require.ErrorIs(t, err, context.Canceled)
	assert.False(t, st.called, "GetAllReports should not run after pre-call cancellation")
}

func TestGenerateSummary_ContextCanceledDuringLoop(t *testing.T) {
	start := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 5, 8, 0, 0, 0, 0, time.UTC)
	ctx, cancel := context.WithCancel(context.Background())
	st := fakeStoreWithReports(
		summaryReport("001-mixed", "org-mixed", start.Add(time.Hour), 42, 1),
		summaryReport("002-after-cancel", "org-a", start.Add(2*time.Hour), 10, 0),
	)
	debugLogger := slog.New(cancelOnWarnHandler{cancel: cancel})

	_, err := notify.GenerateSummary(ctx, st, start, end, debugLogger)

	require.ErrorIs(t, err, context.Canceled)
}

func TestGenerateSummary_StoreError(t *testing.T) {
	start := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 5, 8, 0, 0, 0, 0, time.UTC)
	storeErr := errors.New("store unavailable")
	st := errStoreWrapper{FakeStore: storetestutil.NewFakeStore(), err: storeErr}

	_, err := notify.GenerateSummary(context.Background(), st, start, end, nil)

	require.Error(t, err)
	assert.ErrorIs(t, err, storeErr)
	assert.True(t, strings.HasPrefix(err.Error(), "GenerateSummary: "))
}

type trackingStore struct {
	*storetestutil.FakeStore
	called bool
}

func (s *trackingStore) GetAllReports() ([]tlsrpt.Report, error) {
	s.called = true
	return s.FakeStore.GetAllReports()
}

type cancelOnWarnHandler struct {
	cancel context.CancelFunc
}

func (h cancelOnWarnHandler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= slog.LevelWarn
}

func (h cancelOnWarnHandler) Handle(_ context.Context, _ slog.Record) error {
	h.cancel()
	return nil
}

func (h cancelOnWarnHandler) WithAttrs(_ []slog.Attr) slog.Handler { return h }
func (h cancelOnWarnHandler) WithGroup(_ string) slog.Handler      { return h }

type errStoreWrapper struct {
	*storetestutil.FakeStore
	err error
}

func (e errStoreWrapper) GetAllReports() ([]tlsrpt.Report, error) {
	return nil, e.err
}

func fakeStoreWithReports(reports ...tlsrpt.Report) *storetestutil.FakeStore {
	st := storetestutil.NewFakeStore()
	for _, report := range reports {
		st.Reports[report.ReportID] = report
	}
	return st
}

func summaryReport(reportID, organization string, end time.Time, successfulSessions, failureSessions int64) tlsrpt.Report {
	return tlsrpt.Report{
		OrganizationName: organization,
		ReportID:         reportID,
		DateRange: tlsrpt.DateRange{
			StartDatetime: end.Add(-24 * time.Hour),
			EndDatetime:   end,
		},
		Policies: []tlsrpt.PolicyRecord{
			{
				Summary: tlsrpt.Summary{
					TotalSuccessfulSessionCount: successfulSessions,
					TotalFailureSessionCount:    failureSessions,
				},
			},
		},
	}
}
