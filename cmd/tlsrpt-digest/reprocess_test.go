//go:build test

package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
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

func TestParseCLI_ReprocessNotifyFlag(t *testing.T) {
	inv, err := parseCLI([]string{"reprocess", "--notify"}, io.Discard)
	require.NoError(t, err)
	assert.True(t, inv.Options.ReprocessNotify)
}

// TestReprocess_FileReadFailureContinues verifies that per-file LoadEmails failures
// (partial results + error) are treated as warnings and processing continues.
func TestReprocess_FileReadFailureContinues(t *testing.T) {
	st := storetestutil.NewFakeStore()
	internalDate := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	// uid=1: valid email (will succeed).
	addFakeEmail(st, 1, 100, internalDate, tlsrptRawEMLReprocess("Corp", "r1", 0))
	// uid=2: malformed raw bytes → mail.ReadMessage fails → per-file error in LoadEmails.
	addFakeEmail(st, 2, 100, internalDate, []byte("InvalidHeader\r\n\r\nbody"))
	spy := &SpyNotificationSink{}

	// Per-file failure: LoadEmails returns [uid=1 email] + error → continue with partial list.
	code, err := newReprocessRunner().Run(context.Background(), makeReprocessBoot(t, st, spy, false))
	require.NoError(t, err)
	assert.Equal(t, exitOK, code)
	assert.Equal(t, 1, st.SaveEmailMetasCallCount)
	assert.Equal(t, 1, st.SaveReportsCallCount)
}

func TestReprocess_AllFileReadFailuresContinueWithWarning(t *testing.T) {
	st := storetestutil.NewFakeStore()
	internalDate := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	addFakeEmail(st, 2, 100, internalDate, []byte("InvalidHeader\r\n\r\nbody"))
	spy := &SpyNotificationSink{}

	code, err := newReprocessRunner().Run(context.Background(), makeReprocessBoot(t, st, spy, true))
	require.NoError(t, err)
	assert.Equal(t, exitOK, code)
	assert.Equal(t, 1, st.SaveEmailMetasCallCount)
	assert.Equal(t, 1, st.SaveReportsCallCount)
	require.Len(t, spy.Warnings, 1)
	assert.Equal(t, notify.WarningKindParseFailure, spy.Warnings[0].Kind)
	assert.Equal(t, uint32(2), spy.Warnings[0].UID)
	assert.Equal(t, uint32(100), spy.Warnings[0].UIDValidity)
	assert.Equal(t, 1, spy.FlushCount)
}

// TestReprocess_GlobalLoadFailureExitsError verifies that a global LoadEmails failure
// (nil emails + error) causes exit 1 with a system error notification.
func TestReprocess_GlobalLoadFailureExitsError(t *testing.T) {
	for _, loadErr := range []error{
		errors.New("disk I/O error"),
		errors.Join(
			&store.ErrLoadEmailFailed{UID: 2, UIDValidity: 100, Err: errors.New("bad file")},
			errors.New("walk failed"),
		),
	} {
		t.Run(loadErr.Error(), func(t *testing.T) {
			st := storetestutil.NewFakeStore()
			st.LoadEmailsErr = loadErr
			spy := &SpyNotificationSink{}

			code, err := newReprocessRunner().Run(context.Background(), makeReprocessBoot(t, st, spy, false))
			assert.Error(t, err)
			assert.Equal(t, exitError, code)
			require.Len(t, spy.SystemErrors, 1)
			assert.Equal(t, notify.SystemErrorKindStoreCorruption, spy.SystemErrors[0].Kind)
			assert.Equal(t, 1, spy.FlushCount)
		})
	}
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
