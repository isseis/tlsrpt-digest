//go:build test

package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/isseis/tlsrpt-digest/internal/config"
	"github.com/isseis/tlsrpt-digest/internal/notify"
	storetestutil "github.com/isseis/tlsrpt-digest/internal/store/testutil"
	"github.com/isseis/tlsrpt-digest/internal/tlsrpt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// makeGCBoot creates a minimal BootContext for gc tests.
func makeGCBoot(t *testing.T, st *storetestutil.FakeStore, spy *SpyNotificationSink, opts cliOptions, cfg *config.Config) *BootContext {
	t.Helper()
	if cfg == nil {
		cfg = &config.Config{}
		cfg.Store.RetentionDays = 30
		cfg.Store.MaxEmailAgeDays = 30
		cfg.IMAP.Host = "imap.example.com"
		cfg.IMAP.Port = 993
		cfg.IMAP.Mailbox = "INBOX"
	}
	return &BootContext{
		Config:   cfg,
		Store:    st,
		Notifier: spy,
		Options:  opts,
	}
}

func TestGC_BeforeFlag(t *testing.T) {
	st := storetestutil.NewFakeStore()
	spy := &SpyNotificationSink{}
	now := time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)
	dur := Duration{Days: 7}
	opts := cliOptions{Before: &dur}

	runner := &gcRunner{now: func() time.Time { return now }}
	code, err := runner.Run(context.Background(), makeGCBoot(t, st, spy, opts, nil))
	require.NoError(t, err)
	assert.Equal(t, exitOK, code)
}

func TestGC_BeforeDefault(t *testing.T) {
	st := storetestutil.NewFakeStore()
	spy := &SpyNotificationSink{}
	now := time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)
	cfg := &config.Config{}
	cfg.Store.RetentionDays = 14
	cfg.Store.MaxEmailAgeDays = 30
	cfg.IMAP.Host = "imap.example.com"
	cfg.IMAP.Port = 993
	cfg.IMAP.Mailbox = "INBOX"

	runner := &gcRunner{now: func() time.Time { return now }}
	code, err := runner.Run(context.Background(), makeGCBoot(t, st, spy, cliOptions{}, cfg))
	require.NoError(t, err)
	assert.Equal(t, exitOK, code)
}

func TestGC_ReportsCutoff(t *testing.T) {
	st := storetestutil.NewFakeStore()
	// Add a report with end date older than cutoff.
	oldEnd := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	st.Reports["old"] = tlsrpt.Report{ReportID: "old", DateRange: tlsrpt.DateRange{EndDatetime: oldEnd}}
	// Keep a newer report.
	newEnd := time.Date(2026, 1, 10, 0, 0, 0, 0, time.UTC)
	st.Reports["new"] = tlsrpt.Report{ReportID: "new", DateRange: tlsrpt.DateRange{EndDatetime: newEnd}}

	now := time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)
	dur := Duration{Days: 7} // cutoff = 2026-01-08 00:00:00 UTC
	opts := cliOptions{Before: &dur}

	spy := &SpyNotificationSink{}
	runner := &gcRunner{now: func() time.Time { return now }}
	code, err := runner.Run(context.Background(), makeGCBoot(t, st, spy, opts, nil))
	require.NoError(t, err)
	assert.Equal(t, exitOK, code)
	// "old" (end=Jan1) < cutoff(Jan8), deleted. "new" (end=Jan10) >= cutoff, kept.
	_, hasOld := st.Reports["old"]
	_, hasNew := st.Reports["new"]
	assert.False(t, hasOld, "old report should be deleted")
	assert.True(t, hasNew, "new report should be kept")
}

func TestGC_EmailsCutoff(t *testing.T) {
	st := storetestutil.NewFakeStore()
	oldDate := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	newDate := time.Date(2026, 1, 12, 0, 0, 0, 0, time.UTC)
	st.Emails[storetestutil.EmailKey{UID: 1, UIDValidity: 100}] = &storetestutil.FakeEmailEntry{
		UID: 1, UIDValidity: 100, InternalDate: oldDate,
	}
	st.Emails[storetestutil.EmailKey{UID: 2, UIDValidity: 100}] = &storetestutil.FakeEmailEntry{
		UID: 2, UIDValidity: 100, InternalDate: newDate,
	}

	now := time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)
	dur := Duration{Days: 7} // cutoff = 2026-01-08 00:00:00 UTC
	opts := cliOptions{MaxEmailAge: &dur}

	spy := &SpyNotificationSink{}
	runner := &gcRunner{now: func() time.Time { return now }}
	code, err := runner.Run(context.Background(), makeGCBoot(t, st, spy, opts, nil))
	require.NoError(t, err)
	assert.Equal(t, exitOK, code)

	_, hasOld := st.Emails[storetestutil.EmailKey{UID: 1, UIDValidity: 100}]
	_, hasNew := st.Emails[storetestutil.EmailKey{UID: 2, UIDValidity: 100}]
	assert.False(t, hasOld, "old email should be deleted")
	assert.True(t, hasNew, "new email should be kept")
}

func TestGC_MaxEmailAgeDefault(t *testing.T) {
	st := storetestutil.NewFakeStore()
	spy := &SpyNotificationSink{}
	now := time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)
	cfg := &config.Config{}
	cfg.Store.RetentionDays = 30
	cfg.Store.MaxEmailAgeDays = 7
	cfg.IMAP.Host = "imap.example.com"
	cfg.IMAP.Port = 993
	cfg.IMAP.Mailbox = "INBOX"

	oldDate := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	st.Emails[storetestutil.EmailKey{UID: 1, UIDValidity: 100}] = &storetestutil.FakeEmailEntry{
		UID: 1, UIDValidity: 100, InternalDate: oldDate,
	}

	runner := &gcRunner{now: func() time.Time { return now }}
	code, err := runner.Run(context.Background(), makeGCBoot(t, st, spy, cliOptions{}, cfg))
	require.NoError(t, err)
	assert.Equal(t, exitOK, code)
	// max_email_age_days=7, cutoff = Jan 8. Email (Jan 1) < cutoff → deleted.
	_, hasOld := st.Emails[storetestutil.EmailKey{UID: 1, UIDValidity: 100}]
	assert.False(t, hasOld)
}

func TestGC_DeleteCountLog(t *testing.T) {
	st := storetestutil.NewFakeStore()
	spy := &SpyNotificationSink{}
	now := time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)
	runner := &gcRunner{now: func() time.Time { return now }}
	code, err := runner.Run(context.Background(), makeGCBoot(t, st, spy, cliOptions{}, nil))
	require.NoError(t, err)
	assert.Equal(t, exitOK, code)
	// No notifications expected on success.
	assert.Empty(t, spy.SystemErrors)
}

func TestGC_RecoveryRequiredStops(t *testing.T) {
	st := storetestutil.NewFakeStore()
	st.Recovery = &storetestutil.FakeRecovery{Prev: 1, Curr: 2, DetectedAt: time.Now()}
	spy := &SpyNotificationSink{}

	runner := &gcRunner{now: time.Now}
	code, err := runner.Run(context.Background(), makeGCBoot(t, st, spy, cliOptions{}, nil))
	require.NoError(t, err)
	assert.Equal(t, exitError, code)
	// No deletion should happen.
	assert.Empty(t, spy.SystemErrors)
}

func TestGC_LoadRecoveryRequiredFail(t *testing.T) {
	st := storetestutil.NewFakeStore()
	st.LoadRecoveryRequiredErr = errors.New("disk error")
	spy := &SpyNotificationSink{}

	runner := &gcRunner{now: time.Now}
	code, err := runner.Run(context.Background(), makeGCBoot(t, st, spy, cliOptions{}, nil))
	assert.Error(t, err)
	assert.Equal(t, exitError, code)
	require.Len(t, spy.SystemErrors, 1)
	assert.Equal(t, notify.SystemErrorKindStoreCorruption, spy.SystemErrors[0].Kind)
	assert.Equal(t, 1, spy.FlushCount)
}

func TestGC_DeleteReportsFailureNotifies(t *testing.T) {
	st := storetestutil.NewFakeStore()
	st.DeleteReportsBeforeErr = errors.New("write error")
	spy := &SpyNotificationSink{}

	runner := &gcRunner{now: time.Now}
	code, err := runner.Run(context.Background(), makeGCBoot(t, st, spy, cliOptions{}, nil))
	assert.Error(t, err)
	assert.Equal(t, exitError, code)
	require.Len(t, spy.SystemErrors, 1)
	assert.Equal(t, notify.SystemErrorKindStorePermission, spy.SystemErrors[0].Kind)
	assert.Equal(t, "gc", spy.SystemErrors[0].Component)
	assert.Equal(t, 1, spy.FlushCount)
}

func TestGC_DeleteEmailsFailureNotifies(t *testing.T) {
	st := storetestutil.NewFakeStore()
	st.DeleteEmailsBeforeErr = errors.New("write error")
	spy := &SpyNotificationSink{}

	runner := &gcRunner{now: time.Now}
	code, err := runner.Run(context.Background(), makeGCBoot(t, st, spy, cliOptions{}, nil))
	assert.Error(t, err)
	assert.Equal(t, exitError, code)
	require.Len(t, spy.SystemErrors, 1)
	assert.Equal(t, notify.SystemErrorKindStorePermission, spy.SystemErrors[0].Kind)
	assert.Equal(t, 1, spy.FlushCount)
}

func TestGC_DeleteReportsFailDoesNotCallDeleteEmails(t *testing.T) {
	st := storetestutil.NewFakeStore()
	st.DeleteReportsBeforeErr = errors.New("write error")
	spy := &SpyNotificationSink{}

	runner := &gcRunner{now: time.Now}
	_, _ = runner.Run(context.Background(), makeGCBoot(t, st, spy, cliOptions{}, nil))
	assert.Equal(t, 0, st.DeleteEmailsBeforeCallCount, "DeleteEmailsBefore should not be called when DeleteReportsBefore fails")
}

func TestGC_Idempotent(t *testing.T) {
	st := storetestutil.NewFakeStore()
	spy := &SpyNotificationSink{}
	now := time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)
	dur := Duration{Days: 7}
	opts := cliOptions{Before: &dur}

	runner := &gcRunner{now: func() time.Time { return now }}
	boot := makeGCBoot(t, st, spy, opts, nil)

	// First run.
	code, err := runner.Run(context.Background(), boot)
	require.NoError(t, err)
	assert.Equal(t, exitOK, code)

	// Second run: nothing to delete.
	code, err = runner.Run(context.Background(), boot)
	require.NoError(t, err)
	assert.Equal(t, exitOK, code)
}

func TestGC_ExitCodes(t *testing.T) {
	tests := []struct {
		name     string
		setup    func(*storetestutil.FakeStore)
		wantCode int
		wantErr  bool
	}{
		{
			name:     "normal completion",
			setup:    func(_ *storetestutil.FakeStore) {},
			wantCode: exitOK,
		},
		{
			name: "recovery required",
			setup: func(st *storetestutil.FakeStore) {
				st.Recovery = &storetestutil.FakeRecovery{Prev: 1, Curr: 2}
			},
			wantCode: exitError,
			wantErr:  false,
		},
		{
			name: "DeleteReportsBefore fails",
			setup: func(st *storetestutil.FakeStore) {
				st.DeleteReportsBeforeErr = errors.New("fail")
			},
			wantCode: exitError,
			wantErr:  true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			st := storetestutil.NewFakeStore()
			tc.setup(st)
			spy := &SpyNotificationSink{}
			runner := &gcRunner{now: time.Now}
			code, err := runner.Run(context.Background(), makeGCBoot(t, st, spy, cliOptions{}, nil))
			assert.Equal(t, tc.wantCode, code)
			if tc.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
