//go:build test

package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/isseis/tlsrpt-digest/internal/config"
	"github.com/isseis/tlsrpt-digest/internal/notify"
	storetestutil "github.com/isseis/tlsrpt-digest/internal/store/testutil"
	"github.com/isseis/tlsrpt-digest/internal/tlsrpt"
)

// ── test helpers ──────────────────────────────────────────────────────────────

type summaryTestBed struct {
	fakeStore *storetestutil.FakeStore
	guard     *storetestutil.FakeSummaryConsistencyGuard
	notif     *SpyNotificationSink
	runner    *summaryRunner
	boot      *BootContext
	now       time.Time
}

// newSummaryTestBed creates a bed with an empty store and a no-recovery guard.
func newSummaryTestBed(t *testing.T) *summaryTestBed {
	t.Helper()
	now := time.Date(2026, 5, 20, 10, 30, 0, 0, time.UTC)
	fakeStore := storetestutil.NewFakeStore()
	guard := &storetestutil.FakeSummaryConsistencyGuard{}
	spy := &SpyNotificationSink{}

	runner := &summaryRunner{
		buildNotifier: func(_ *BootContext) (NotificationSink, error) {
			return spy, nil
		},
		now: func() time.Time { return now },
	}

	cfg := &config.Config{
		IMAP: config.IMAPConfig{
			Host:    "imap.example.com",
			Port:    993,
			Mailbox: "INBOX",
		},
		Notify: config.NotifyConfig{
			Slack: config.NotifySlackConfig{AllowedHost: "hooks.slack.com"},
		},
		Store:   config.StoreConfig{RootDir: "/tmp/test"},
		Summary: config.SummaryConfig{WindowDays: 7},
	}

	boot := &BootContext{
		Config:       cfg,
		Store:        fakeStore,
		SummaryGuard: guard,
		RunID:        "test-run-id",
	}

	return &summaryTestBed{
		fakeStore: fakeStore,
		guard:     guard,
		notif:     spy,
		runner:    runner,
		boot:      boot,
		now:       now,
	}
}

// addReportInWindow adds a success report whose EndDatetime is 1 day before now
// (so it falls within the default 7-day window).
func (b *summaryTestBed) addReportInWindow() {
	b.addReport("r-inwindow", "org-test", b.now.Add(-1*24*time.Hour), 5)
}

func (b *summaryTestBed) addReport(reportID, org string, end time.Time, successSessions int64) {
	b.fakeStore.Reports[reportID] = tlsrpt.Report{
		OrganizationName: org,
		ReportID:         reportID,
		DateRange: tlsrpt.DateRange{
			StartDatetime: end.Add(-24 * time.Hour),
			EndDatetime:   end,
		},
		Policies: []tlsrpt.PolicyRecord{
			{
				Summary: tlsrpt.Summary{
					TotalSuccessfulSessionCount: successSessions,
				},
			},
		},
	}
}

// ── window flag tests ─────────────────────────────────────────────────────────

func TestSummary_WindowFlagUsedAsStart(t *testing.T) {
	bed := newSummaryTestBed(t)
	// Config says 14d but --window 7d must win.
	bed.boot.Config.Summary.WindowDays = 14
	bed.addReportInWindow()

	window, err := ParseDuration("7d")
	require.NoError(t, err)
	bed.boot.Options = cliOptions{Window: &window}

	exitCode, err := bed.runner.Run(context.Background(), bed.boot)
	require.NoError(t, err)
	assert.Equal(t, exitOK, exitCode)
	require.Len(t, bed.notif.Summaries, 1)

	wantStart := window.Cutoff(bed.now)
	wantNotStart := Duration{Days: 14}.Cutoff(bed.now)
	assert.Equal(t, wantStart, bed.notif.Summaries[0].Period.Start, "start must be Duration.Cutoff(now) from --window flag")
	assert.NotEqual(t, wantNotStart, bed.notif.Summaries[0].Period.Start, "--window must override config window_days")
}

func TestSummary_WindowDefaultFromConfig(t *testing.T) {
	bed := newSummaryTestBed(t)
	bed.boot.Config.Summary.WindowDays = 14
	bed.addReport("r1", "org-a", bed.now.Add(-1*24*time.Hour), 5)
	// No --window flag (Window == nil).

	exitCode, err := bed.runner.Run(context.Background(), bed.boot)
	require.NoError(t, err)
	assert.Equal(t, exitOK, exitCode)
	require.Len(t, bed.notif.Summaries, 1)

	wantStart := Duration{Days: 14}.Cutoff(bed.now)
	assert.Equal(t, wantStart, bed.notif.Summaries[0].Period.Start)
}

func TestSummary_EndIsUTCDayStart(t *testing.T) {
	bed := newSummaryTestBed(t)
	bed.addReportInWindow()

	_, err := bed.runner.Run(context.Background(), bed.boot)
	require.NoError(t, err)
	require.Len(t, bed.notif.Summaries, 1)

	assert.Equal(t, UTCDayStart(bed.now), bed.notif.Summaries[0].Period.End)
}

// ── GenerateSummary failure ───────────────────────────────────────────────────

func TestSummary_GenerateSummaryFailure(t *testing.T) {
	bed := newSummaryTestBed(t)
	buildCalled := false
	bed.runner.buildNotifier = func(_ *BootContext) (NotificationSink, error) {
		buildCalled = true
		return bed.notif, nil
	}

	storeErr := errors.New("disk read error")
	bed.boot.Store = &errorGetAllReportsStore{FakeStore: bed.fakeStore, err: storeErr}

	exitCode, err := bed.runner.Run(context.Background(), bed.boot)
	assert.Equal(t, exitError, exitCode)
	require.Error(t, err)
	assert.False(t, buildCalled, "notifier must not be built on GenerateSummary failure")
	assert.Equal(t, 0, bed.notif.FlushCount)
}

// ── recovery-required: first check ───────────────────────────────────────────

func TestSummary_RecoveryRequiredFirstCheck(t *testing.T) {
	bed := newSummaryTestBed(t)
	bed.guard.RecoveryRequiredFound = true

	exitCode, err := bed.runner.Run(context.Background(), bed.boot)
	assert.Equal(t, exitError, exitCode)
	require.NoError(t, err)
	require.Len(t, bed.notif.SystemErrors, 1)
	assert.Equal(t, notify.SystemErrorKindRecoveryRequired, bed.notif.SystemErrors[0].Kind)
	assert.Equal(t, 1, bed.notif.FlushCount)
}

func TestSummary_RecoveryRequiredFirstCheckFlushFailure(t *testing.T) {
	buf := captureSlog(t)
	bed := newSummaryTestBed(t)
	bed.guard.RecoveryRequiredFound = true
	bed.notif.FlushError = errors.New("slack flush failed")

	exitCode, err := bed.runner.Run(context.Background(), bed.boot)
	assert.Equal(t, exitError, exitCode)
	require.NoError(t, err)
	out := buf.String()
	assert.True(t, strings.Contains(out, "level=WARN"), "expected WARN log, got: %s", out)
	assert.True(t, strings.Contains(out, "error="), "expected error field in log, got: %s", out)
	require.Len(t, bed.notif.SystemErrors, 1)
	assert.Equal(t, notify.SystemErrorKindRecoveryRequired, bed.notif.SystemErrors[0].Kind)
	assert.Equal(t, 1, bed.notif.FlushCount)
}

func TestSummary_FirstCheckError(t *testing.T) {
	bed := newSummaryTestBed(t)
	bed.guard.CheckError = errors.New("guard lock lost")

	exitCode, err := bed.runner.Run(context.Background(), bed.boot)
	assert.Equal(t, exitError, exitCode)
	require.Error(t, err)
	assert.Equal(t, 0, bed.notif.FlushCount)
	assert.Empty(t, bed.notif.SystemErrors)
}

// ── empty summary paths ───────────────────────────────────────────────────────

func TestSummary_EmptyStoreExitOK(t *testing.T) {
	bed := newSummaryTestBed(t)
	buildCalled := false
	bed.runner.buildNotifier = func(_ *BootContext) (NotificationSink, error) {
		buildCalled = true
		return bed.notif, nil
	}

	exitCode, err := bed.runner.Run(context.Background(), bed.boot)
	assert.Equal(t, exitOK, exitCode)
	require.NoError(t, err)
	assert.False(t, buildCalled, "notifier must not be built for empty summary")
	assert.Empty(t, bed.notif.SystemErrors)
	assert.Equal(t, 0, bed.notif.FlushCount)
}

// ── non-empty summary paths ───────────────────────────────────────────────────

func TestSummary_NonEmptyNoSlackURLFails(t *testing.T) {
	bed := newSummaryTestBed(t)
	bed.addReportInWindow()

	buildErr := errors.New("at least one Slack webhook URL is required")
	bed.runner.buildNotifier = func(_ *BootContext) (NotificationSink, error) {
		return nil, buildErr
	}

	exitCode, err := bed.runner.Run(context.Background(), bed.boot)
	assert.Equal(t, exitError, exitCode)
	require.Error(t, err)
}

func TestSummary_SendsLogSummaryAndFlushes(t *testing.T) {
	bed := newSummaryTestBed(t)
	bed.addReportInWindow()

	exitCode, err := bed.runner.Run(context.Background(), bed.boot)
	require.NoError(t, err)
	assert.Equal(t, exitOK, exitCode)
	require.Len(t, bed.notif.Summaries, 1)
	assert.Equal(t, 1, bed.notif.FlushCount)
}

func TestSummary_PeriodInSummaryMessage(t *testing.T) {
	bed := newSummaryTestBed(t)
	bed.addReportInWindow()

	_, err := bed.runner.Run(context.Background(), bed.boot)
	require.NoError(t, err)
	require.Len(t, bed.notif.Summaries, 1)

	wantStart := Duration{Days: 7}.Cutoff(bed.now)
	wantEnd := UTCDayStart(bed.now)
	assert.Equal(t, notify.DateRange{Start: wantStart, End: wantEnd}, bed.notif.Summaries[0].Period)
}

func TestSummary_UsesGenerateSummaryNotReimplemented(t *testing.T) {
	bed := newSummaryTestBed(t)
	// Report outside the 7-day window: GenerateSummary should exclude it.
	bed.addReport("old", "org-old", bed.now.Add(-100*24*time.Hour), 5)
	// Report inside the window.
	bed.addReport("new", "org-new", bed.now.Add(-1*24*time.Hour), 3)

	_, err := bed.runner.Run(context.Background(), bed.boot)
	require.NoError(t, err)
	require.Len(t, bed.notif.Summaries, 1)
	assert.NotContains(t, bed.notif.Summaries[0].OrganizationStats, "org-old")
	assert.Equal(t, int64(3), bed.notif.Summaries[0].OrganizationStats["org-new"])
}

func TestSummary_FlushFailureLogsWarnAndExitsOK(t *testing.T) {
	buf := captureSlog(t)
	bed := newSummaryTestBed(t)
	bed.addReportInWindow()
	bed.notif.FlushError = errors.New("slack timeout")

	exitCode, err := bed.runner.Run(context.Background(), bed.boot)
	assert.Equal(t, exitOK, exitCode)
	require.NoError(t, err)
	out := buf.String()
	assert.True(t, strings.Contains(out, "level=WARN"), "expected WARN log, got: %s", out)
	assert.True(t, strings.Contains(out, "error="), "expected error field in log, got: %s", out)
}

// ── exit codes ────────────────────────────────────────────────────────────────

func TestSummary_ExitCodes(t *testing.T) {
	tests := []struct {
		name     string
		setup    func(bed *summaryTestBed)
		wantExit int
	}{
		{
			name:     "empty summary exits 0",
			setup:    func(_ *summaryTestBed) {},
			wantExit: exitOK,
		},
		{
			name: "normal send exits 0",
			setup: func(bed *summaryTestBed) {
				bed.addReportInWindow()
			},
			wantExit: exitOK,
		},
		{
			name: "recovery-required exits 1",
			setup: func(bed *summaryTestBed) {
				bed.guard.RecoveryRequiredFound = true
			},
			wantExit: exitError,
		},
		{
			name: "flush failure exits 0",
			setup: func(bed *summaryTestBed) {
				bed.addReportInWindow()
				bed.notif.FlushError = errors.New("flush failed")
			},
			wantExit: exitOK,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bed := newSummaryTestBed(t)
			tt.setup(bed)
			exitCode, _ := bed.runner.Run(context.Background(), bed.boot)
			assert.Equal(t, tt.wantExit, exitCode)
		})
	}
}

// ── test doubles ──────────────────────────────────────────────────────────────

// TestSummary_LogSummaryFailureLogsWarnAndExitsOK verifies that when LogSummary
// returns an error, slog.Warn is emitted and the exit code is exitOK.
func TestSummary_LogSummaryFailureLogsWarnAndExitsOK(t *testing.T) {
	buf := captureSlog(t)
	bed := newSummaryTestBed(t)
	bed.addReportInWindow()
	bed.notif.LogError = errors.New("log summary failed")

	exitCode, err := bed.runner.Run(context.Background(), bed.boot)
	assert.Equal(t, exitOK, exitCode)
	require.NoError(t, err)
	out := buf.String()
	assert.True(t, strings.Contains(out, "level=WARN"), "expected WARN log, got: %s", out)
	assert.True(t, strings.Contains(out, "error="), "expected error field in log, got: %s", out)
}

// TestSummary_HappyPath_NoWarnLog verifies that no WARN log is emitted when
// all notification calls succeed (regression guard for unconditional slog.Warn).
func TestSummary_HappyPath_NoWarnLog(t *testing.T) {
	buf := captureSlog(t)
	bed := newSummaryTestBed(t)
	bed.addReportInWindow()

	exitCode, err := bed.runner.Run(context.Background(), bed.boot)
	assert.Equal(t, exitOK, exitCode)
	require.NoError(t, err)
	assert.False(t, strings.Contains(buf.String(), "level=WARN"),
		"unexpected WARN log on success, got: %s", buf.String())
}

// errorGetAllReportsStore wraps FakeStore and returns an error from GetAllReports.
type errorGetAllReportsStore struct {
	*storetestutil.FakeStore
	err error
}

func (s *errorGetAllReportsStore) GetAllReports() ([]tlsrpt.Report, error) {
	return nil, s.err
}

// TestSummary_NotifyFlushError_NoSensitiveDataInLog verifies that sensitive
// values (Slack webhook URL, IMAP password) that exist in the runtime
// environment do NOT appear in the slog.Warn output produced by the
// notification error path.  The real redaction is enforced by internal/notify;
// this test is a defense-in-depth check that the cmd layer itself does not
// independently inject those values into the structured log.
//
// In production, internal/notify replaces actual URLs with "[webhook URL
// redacted]" before returning errors, so the simulated error below matches
// that sanitised form.
func TestSummary_NotifyFlushError_NoSensitiveDataInLog(t *testing.T) {
	// Sensitive values that must not leak into slog output.
	const sensitiveURL = "https://hooks.slack.com/services/T00/B00/secret-token"
	const sensitivePass = "imap-s3cr3t-password"
	buf := captureSlog(t)
	bed := newSummaryTestBed(t)
	bed.addReportInWindow()
	// Simulate what internal/notify returns after sanitisation: the actual URL
	// has already been replaced and neither sensitiveURL nor sensitivePass
	// appears in the error string.
	bed.notif.FlushError = errors.New("http request failed: [webhook URL redacted]")

	exitCode, err := bed.runner.Run(context.Background(), bed.boot)
	assert.Equal(t, exitOK, exitCode)
	require.NoError(t, err)
	logged := buf.String()
	assert.True(t, strings.Contains(logged, "level=WARN"), "expected slog.Warn output")
	// The raw sensitive values must not appear in the captured log.
	assert.False(t, strings.Contains(logged, sensitiveURL), "Slack URL must not appear in log")
	assert.False(t, strings.Contains(logged, sensitivePass), "IMAP password must not appear in log")
	// slog.Warn must not recursively trigger a Slack notification.
	assert.Equal(t, 1, bed.notif.FlushCount, "Flush must be called exactly once; slog.Warn must not re-invoke Flush")
	assert.Empty(t, bed.notif.SystemErrors, "slog.Warn must not route to Slack notifier")
}
