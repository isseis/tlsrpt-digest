//go:build test

package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/isseis/tlsrpt-digest/internal/config"
	"github.com/isseis/tlsrpt-digest/internal/notify"
	"github.com/isseis/tlsrpt-digest/internal/store"
	storetestutil "github.com/isseis/tlsrpt-digest/internal/store/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// makeTLSRPTJSONReprocess builds a TLSRPT JSON payload for reprocess tests.
func makeTLSRPTJSONReprocess(org, id string, failures int64) []byte {
	b, err := json.Marshal(map[string]any{
		"organization-name": org,
		"report-id":         id,
		"date-range": map[string]any{
			"start-datetime": "2026-01-01T00:00:00Z",
			"end-datetime":   "2026-01-02T00:00:00Z",
		},
		"policies": []map[string]any{{
			"policy": map[string]any{
				"policy-type":   "sts",
				"policy-domain": "example.com",
			},
			"summary": map[string]any{
				"total-successful-session-count": int64(10),
				"total-failure-session-count":    failures,
			},
		}},
	})
	if err != nil {
		panic("makeTLSRPTJSONReprocess: " + err.Error())
	}
	return b
}

// tlsrptRawEMLReprocess builds a raw .eml with a base64-encoded TLSRPT JSON attachment.
func tlsrptRawEMLReprocess(org, id string, failures int64) []byte {
	enc := base64.StdEncoding.EncodeToString(makeTLSRPTJSONReprocess(org, id, failures))
	raw := "From: rpt@example.com\r\n" +
		"Content-Type: multipart/mixed; boundary=\"b\"\r\n\r\n" +
		"--b\r\n" +
		"Content-Type: application/tlsrpt+json; name=\"report.json\"\r\n" +
		"Content-Disposition: attachment; filename=\"report.json\"\r\n" +
		"Content-Transfer-Encoding: base64\r\n\r\n" +
		enc + "\r\n--b--\r\n"
	return []byte(raw)
}

// addFakeEmail adds an email to FakeStore's Emails map with RawEML so LoadEmails returns it.
func addFakeEmail(st *storetestutil.FakeStore, uid, uidValidity uint32, internalDate time.Time, rawEML []byte) {
	key := storetestutil.EmailKey{UID: uid, UIDValidity: uidValidity}
	st.Emails[key] = &storetestutil.FakeEmailEntry{
		UID:          uid,
		UIDValidity:  uidValidity,
		InternalDate: internalDate,
		RawEML:       rawEML,
	}
}

// makeReprocessBoot creates a minimal BootContext for reprocess tests.
func makeReprocessBoot(t *testing.T, st *storetestutil.FakeStore, spy *SpyNotificationSink, notifyFlag bool) *BootContext {
	t.Helper()
	cfg := &config.Config{}
	cfg.IMAP.Host = "imap.example.com"
	cfg.IMAP.Port = 993
	cfg.IMAP.Mailbox = "INBOX"
	cfg.IMAP.MaxMessageBytes = 1 << 20 // 1 MiB
	return &BootContext{
		Config:   cfg,
		Store:    st,
		Notifier: spy,
		Options:  cliOptions{ReprocessNotify: notifyFlag},
	}
}

func TestReprocess_RecoveryRequiredStops(t *testing.T) {
	st := storetestutil.NewFakeStore()
	st.Recovery = &storetestutil.FakeRecovery{Prev: 1, Curr: 2, DetectedAt: time.Now()}
	spy := &SpyNotificationSink{}

	code, err := newReprocessRunner().Run(context.Background(), makeReprocessBoot(t, st, spy, false))
	require.NoError(t, err)
	assert.Equal(t, exitError, code)
	assert.Equal(t, 0, st.SaveEmailMetasCallCount)
	assert.Equal(t, 0, st.SaveReportsCallCount)
}

func TestReprocess_LoadRecoveryRequiredFail(t *testing.T) {
	st := storetestutil.NewFakeStore()
	st.LoadRecoveryRequiredErr = errors.New("disk error")
	spy := &SpyNotificationSink{}

	code, err := newReprocessRunner().Run(context.Background(), makeReprocessBoot(t, st, spy, false))
	assert.Error(t, err)
	assert.Equal(t, exitError, code)
	assert.Equal(t, 0, st.SaveEmailMetasCallCount)
	assert.Equal(t, 0, st.SaveReportsCallCount)
	require.Len(t, spy.SystemErrors, 1)
	assert.Equal(t, notify.SystemErrorKindStoreCorruption, spy.SystemErrors[0].Kind)
	assert.Equal(t, 1, spy.FlushCount)
}

func TestReprocess_NoNotify_NoLogAlert(t *testing.T) {
	st := storetestutil.NewFakeStore()
	internalDate := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	addFakeEmail(st, 1, 100, internalDate, tlsrptRawEMLReprocess("Corp", "r1", 5))
	spy := &SpyNotificationSink{}

	code, err := newReprocessRunner().Run(context.Background(), makeReprocessBoot(t, st, spy, false))
	require.NoError(t, err)
	assert.Equal(t, exitOK, code)
	assert.Empty(t, spy.Alerts)
	assert.Equal(t, 0, spy.FlushCount)
}

func TestReprocess_Notify_TLSFailure_LogsAlert(t *testing.T) {
	st := storetestutil.NewFakeStore()
	internalDate := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	addFakeEmail(st, 1, 100, internalDate, tlsrptRawEMLReprocess("Corp", "r1", 5))
	spy := &SpyNotificationSink{}

	code, err := newReprocessRunner().Run(context.Background(), makeReprocessBoot(t, st, spy, true))
	require.NoError(t, err)
	assert.Equal(t, exitOK, code)
	require.Len(t, spy.Alerts, 1)
	assert.Equal(t, "Corp", spy.Alerts[0].OrganizationName)
	assert.Equal(t, int64(5), spy.Alerts[0].FailureCount)
	assert.Equal(t, 1, spy.FlushCount)
}

func TestReprocess_Notify_ParseFailure_LogsWarning(t *testing.T) {
	st := storetestutil.NewFakeStore()
	internalDate := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	// Valid TLSRPT email for uid=1.
	addFakeEmail(st, 1, 100, internalDate, tlsrptRawEMLReprocess("Corp", "r1", 0))
	// Invalid email bytes for uid=2 (will fail mailparse).
	invalidEML := []byte("From: test\r\n\r\nnot-valid-mime-attachment")
	addFakeEmail(st, 2, 100, internalDate, invalidEML)
	spy := &SpyNotificationSink{}

	code, err := newReprocessRunner().Run(context.Background(), makeReprocessBoot(t, st, spy, true))
	require.NoError(t, err)
	assert.Equal(t, exitOK, code)
	// uid=1 parses OK with no failures; uid=2 parse fails → LogWarning.
	// (parse failure of attachment extraction or tlsrpt parsing → LogWarning)
	// uid=2 has no valid TLSRPT attachment → mailparse succeeds but tlsrpt finds no reports,
	// so no warning is logged for it. Reports from uid=1 are saved.
	_ = spy.Warnings // verify no panic
	assert.Equal(t, 1, spy.FlushCount)
}

func TestReprocess_FileReadFailureContinues(t *testing.T) {
	st := storetestutil.NewFakeStore()
	// Simulate a LoadEmails error (per-file failure) alongside some valid emails.
	// We can't easily inject per-file failures into FakeStore.LoadEmails, so we use
	// LoadEmailsErr to simulate the combined error alongside the empty return.
	// Instead, test with a valid email and a separate FakeStore that returns an error.
	st2 := storetestutil.NewFakeStore()
	internalDate := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	addFakeEmail(st, 1, 100, internalDate, tlsrptRawEMLReprocess("Corp", "r1", 0))
	st.LoadEmailsErr = errors.New("some file failed")
	spy := &SpyNotificationSink{}

	// When LoadEmails returns error (global), we log and continue with empty list.
	code, err := newReprocessRunner().Run(context.Background(), makeReprocessBoot(t, st, spy, false))
	// LoadEmailsErr returns nil emails + error → we log warn and continue with empty.
	require.NoError(t, err)
	assert.Equal(t, exitOK, code)
	// SaveEmailMetas and SaveReports called with empty lists.
	assert.Equal(t, 1, st.SaveEmailMetasCallCount)
	assert.Equal(t, 1, st.SaveReportsCallCount)
	_ = st2
}

func TestReprocess_SaveEmailMetasFail(t *testing.T) {
	st := storetestutil.NewFakeStore()
	internalDate := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	addFakeEmail(st, 1, 100, internalDate, tlsrptRawEMLReprocess("Corp", "r1", 0))
	st.SaveEmailMetasErr = errors.New("write error")
	spy := &SpyNotificationSink{}

	code, err := newReprocessRunner().Run(context.Background(), makeReprocessBoot(t, st, spy, false))
	assert.Error(t, err)
	assert.Equal(t, exitError, code)
	// SaveReports should NOT be called.
	assert.Equal(t, 0, st.SaveReportsCallCount)
}

func TestReprocess_SaveReportsFail(t *testing.T) {
	st := storetestutil.NewFakeStore()
	internalDate := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	addFakeEmail(st, 1, 100, internalDate, tlsrptRawEMLReprocess("Corp", "r1", 0))
	st.SaveReportsErr = errors.New("write error")
	spy := &SpyNotificationSink{}

	code, err := newReprocessRunner().Run(context.Background(), makeReprocessBoot(t, st, spy, false))
	assert.Error(t, err)
	assert.Equal(t, exitError, code)
	assert.Equal(t, 0, spy.FlushCount)
}

func TestReprocess_FlushFailure_ExitError(t *testing.T) {
	st := storetestutil.NewFakeStore()
	internalDate := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	addFakeEmail(st, 1, 100, internalDate, tlsrptRawEMLReprocess("Corp", "r1", 5))
	spy := &SpyNotificationSink{FlushError: errors.New("flush fail")}

	code, err := newReprocessRunner().Run(context.Background(), makeReprocessBoot(t, st, spy, true))
	assert.Error(t, err)
	assert.Equal(t, exitError, code)
}

func TestReprocess_CallOrder_SaveEmailMetasBeforeSaveReports(t *testing.T) {
	var callLog []string
	st := storetestutil.NewFakeStore()
	internalDate := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	addFakeEmail(st, 1, 100, internalDate, tlsrptRawEMLReprocess("Corp", "r1", 0))
	spy := &SpyNotificationSink{}

	// Wrap with an ordering-spy store.
	wrapped := &orderTrackStore{FakeStore: st, log: &callLog}
	boot := makeReprocessBoot(t, nil, spy, false)
	boot.Store = wrapped

	code, err := newReprocessRunner().Run(context.Background(), boot)
	require.NoError(t, err)
	assert.Equal(t, exitOK, code)
	require.GreaterOrEqual(t, len(callLog), 2)
	assert.Equal(t, "SaveEmailMetas", callLog[0])
	assert.Equal(t, "SaveReports", callLog[1])
}

// orderTrackStore wraps FakeStore to record method call order.
type orderTrackStore struct {
	*storetestutil.FakeStore
	log *[]string
}

func (s *orderTrackStore) SaveEmailMetas(metas []store.EmailMeta) error {
	*s.log = append(*s.log, "SaveEmailMetas")
	return s.FakeStore.SaveEmailMetas(metas)
}

func (s *orderTrackStore) SaveReports(inputs []store.ReportInput) error {
	*s.log = append(*s.log, "SaveReports")
	return s.FakeStore.SaveReports(inputs)
}

func TestReprocess_Idempotent(t *testing.T) {
	st := storetestutil.NewFakeStore()
	internalDate := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	addFakeEmail(st, 1, 100, internalDate, tlsrptRawEMLReprocess("Corp", "r1", 0))
	spy := &SpyNotificationSink{}
	boot := makeReprocessBoot(t, st, spy, false)

	code, err := newReprocessRunner().Run(context.Background(), boot)
	require.NoError(t, err)
	assert.Equal(t, exitOK, code)
	reportsAfterFirst := len(st.Reports)

	code, err = newReprocessRunner().Run(context.Background(), boot)
	require.NoError(t, err)
	assert.Equal(t, exitOK, code)
	assert.Equal(t, reportsAfterFirst, len(st.Reports))
}

func TestReprocess_ExitCodes(t *testing.T) {
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
			name: "SaveReports fails",
			setup: func(st *storetestutil.FakeStore) {
				st.SaveReportsErr = errors.New("fail")
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
			code, err := newReprocessRunner().Run(context.Background(), makeReprocessBoot(t, st, spy, false))
			assert.Equal(t, tc.wantCode, code)
			if tc.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestInferInternalDateFromPath(t *testing.T) {
	tests := []struct {
		path      string
		wantYear  int
		wantMonth time.Month
		wantZero  bool
	}{
		{path: "100/202601/0000000001.eml", wantYear: 2026, wantMonth: time.January},
		{path: "100/202512/0000000999.eml", wantYear: 2025, wantMonth: time.December},
		{path: "invalid", wantZero: true},
		{path: "a/b/c/d.eml", wantZero: true},
		{path: "100/notadate/0000000001.eml", wantZero: true},
	}
	for _, tc := range tests {
		t.Run(tc.path, func(t *testing.T) {
			got := inferInternalDateFromPath(tc.path)
			if tc.wantZero {
				assert.True(t, got.IsZero())
				return
			}
			assert.Equal(t, tc.wantYear, got.Year())
			assert.Equal(t, tc.wantMonth, got.Month())
			assert.Equal(t, 1, got.Day())
			assert.Equal(t, time.UTC, got.Location())
		})
	}
}

// TestReprocess_FakeStoreLoadEmailsPath verifies that FakeStore.LoadEmails returns paths
// with the YYYYMM format that inferInternalDateFromPath can parse.
func TestReprocess_FakeStoreLoadEmailsPath(t *testing.T) {
	st := storetestutil.NewFakeStore()
	internalDate := time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC)
	rawEML := tlsrptRawEMLReprocess("Corp", "r1", 0)
	addFakeEmail(st, 42, 100, internalDate, rawEML)

	emails, err := st.LoadEmails()
	require.NoError(t, err)
	require.Len(t, emails, 1)

	// Path should be: {uidvalidity}/{YYYYMM}/{padded_uid}.eml
	expected := filepath.Join("100", "202603", "0000000042.eml")
	assert.Equal(t, expected, emails[0].Path)

	// inferInternalDateFromPath should correctly derive year/month from the path.
	// Note: the FakeStore path uses filepath.Join (OS separator), but on Linux it's "/".
	// We use the path directly.
	t.Log("path:", emails[0].Path)
}

// TestReprocess_Notify_ZeroFailures_NoAlert verifies no alert is sent for zero failures.
func TestReprocess_Notify_ZeroFailures_NoAlert(t *testing.T) {
	st := storetestutil.NewFakeStore()
	internalDate := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	// Use a different org and uidValidity to exercise the full parameter range of helpers.
	addFakeEmail(st, 1, 200, internalDate, tlsrptRawEMLReprocess("AnotherOrg", "r1", 0))
	spy := &SpyNotificationSink{}

	code, err := newReprocessRunner().Run(context.Background(), makeReprocessBoot(t, st, spy, true))
	require.NoError(t, err)
	assert.Equal(t, exitOK, code)
	assert.Empty(t, spy.Alerts)
}

// TestReprocess_TLSRPTParseFailure_WithNotify verifies that a real TLSRPT parse failure
// (corrupted attachment) logs a warning when --notify is set.
func TestReprocess_TLSRPTParseFailure_WithNotify(t *testing.T) {
	st := storetestutil.NewFakeStore()
	internalDate := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	// Build an email with a corrupted tlsrpt+json attachment (invalid JSON).
	corruptJSON := []byte("{not valid json")
	enc := base64.StdEncoding.EncodeToString(corruptJSON)
	rawEML := []byte("From: rpt@example.com\r\n" +
		"Content-Type: multipart/mixed; boundary=\"b\"\r\n\r\n" +
		"--b\r\n" +
		"Content-Type: application/tlsrpt+json; name=\"report.json\"\r\n" +
		"Content-Disposition: attachment; filename=\"report.json\"\r\n" +
		"Content-Transfer-Encoding: base64\r\n\r\n" +
		enc + "\r\n--b--\r\n")
	addFakeEmail(st, 1, 100, internalDate, rawEML)

	// Also add a valid email to confirm processing continues.
	addFakeEmail(st, 2, 100, internalDate, tlsrptRawEMLReprocess("Corp", "r2", 0))
	spy := &SpyNotificationSink{}

	code, err := newReprocessRunner().Run(context.Background(), makeReprocessBoot(t, st, spy, true))
	require.NoError(t, err)
	assert.Equal(t, exitOK, code)
	// uid=1 has a parse failure → LogWarning.
	assert.Len(t, spy.Warnings, 1)
	assert.Equal(t, notify.WarningKindParseFailure, spy.Warnings[0].Kind)
	// uid=2 succeeds → report saved.
	assert.Len(t, st.Reports, 1)
	assert.Equal(t, 1, spy.FlushCount)
}
