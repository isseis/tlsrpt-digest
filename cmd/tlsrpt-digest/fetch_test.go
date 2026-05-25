//go:build test

package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/isseis/tlsrpt-digest/internal/config"
	"github.com/isseis/tlsrpt-digest/internal/imap"
	imaptestutil "github.com/isseis/tlsrpt-digest/internal/imap/testutil"
	"github.com/isseis/tlsrpt-digest/internal/notify"
	"github.com/isseis/tlsrpt-digest/internal/store"
	storetestutil "github.com/isseis/tlsrpt-digest/internal/store/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── test constants ────────────────────────────────────────────────────────────

const (
	testUIDValidity uint32 = 42
	testUID1        uint32 = 101
	testUID2        uint32 = 102
)

var testDate = time.Date(2024, 1, 10, 0, 0, 0, 0, time.UTC)

// ── test helpers ──────────────────────────────────────────────────────────────

func newTestConfig() *config.Config {
	return &config.Config{
		IMAP: config.IMAPConfig{
			Host:      "imap.example.com",
			Port:      993,
			Mailbox:   "INBOX",
			FetchDays: 14,
		},
		Store: config.StoreConfig{
			RootDir: "/test/store",
		},
	}
}

// fetchTestBed bundles fakes and runner for a fetch test.
type fetchTestBed struct {
	store   *storetestutil.FakeStore
	notif   *SpyNotificationSink
	fetcher *imaptestutil.FakeMailFetcher
	runner  *fetchRunner
	now     time.Time
	boot    *BootContext
}

// newFetchTestBed creates a bed with UIDValidity already stored (normal case).
func newFetchTestBed(t *testing.T) *fetchTestBed {
	t.Helper()
	bed := newFetchTestBedBlank(t)
	uid := testUIDValidity
	bed.store.UIDValidity = &uid
	return bed
}

// newFetchTestBedBlank creates a bed with no stored UIDValidity (first-run case).
func newFetchTestBedBlank(t *testing.T) *fetchTestBed {
	t.Helper()
	now := time.Date(2024, 1, 15, 12, 0, 0, 0, time.UTC)
	fakeStore := storetestutil.NewFakeStore()
	notif := &SpyNotificationSink{}
	fakeFetcher := &imaptestutil.FakeMailFetcher{
		FetchMetaResult: imap.FetchMetaResult{UIDValidity: testUIDValidity},
		DownloadResult:  make(map[uint32][]byte),
	}

	runner := &fetchRunner{
		newMailFetcher: func(_ imap.Config) (imap.MailFetcher, error) { return fakeFetcher, nil },
		getenv: func(key string) string {
			switch key {
			case "TLSRPT_IMAP_USERNAME":
				return "user@example.com"
			case "TLSRPT_IMAP_PASSWORD":
				return "secret"
			}
			return ""
		},
		now: func() time.Time { return now },
		localEmailSize: func(_ string, uid, uidValidity uint32, _ time.Time) (int64, bool) {
			e, ok := fakeStore.Emails[storetestutil.EmailKey{UID: uid, UIDValidity: uidValidity}]
			if !ok || e.RawEML == nil {
				return 0, false
			}
			return int64(len(e.RawEML)), true
		},
		loadLocalEML: func(_ string, uid, uidValidity uint32, _ time.Time) ([]byte, error) {
			e, ok := fakeStore.Emails[storetestutil.EmailKey{UID: uid, UIDValidity: uidValidity}]
			if !ok || e.RawEML == nil {
				return nil, errors.New("eml not found")
			}
			return e.RawEML, nil
		},
	}

	boot := &BootContext{
		Config:   newTestConfig(),
		Store:    fakeStore,
		Notifier: notif,
	}

	return &fetchTestBed{
		store: fakeStore, notif: notif, fetcher: fakeFetcher,
		runner: runner, now: now, boot: boot,
	}
}

func simpleRawEML() []byte {
	return []byte("From: a@b.com\r\nSubject: test\r\n\r\nbody")
}

func makeTLSRPTJSON(org, id string, failures int64) []byte {
	b, err := json.Marshal(map[string]any{
		"organization-name": org,
		"report-id":         id,
		"date-range": map[string]any{
			"start-datetime": "2024-01-01T00:00:00Z",
			"end-datetime":   "2024-01-02T00:00:00Z",
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
		panic("makeTLSRPTJSON: " + err.Error())
	}
	return b
}

func tlsrptRawEML(org, id string, failures int64) []byte {
	enc := base64.StdEncoding.EncodeToString(makeTLSRPTJSON(org, id, failures))
	raw := "From: rpt@example.com\r\n" +
		"Content-Type: multipart/mixed; boundary=\"b\"\r\n\r\n" +
		"--b\r\n" +
		"Content-Type: application/tlsrpt+json; name=\"report.json\"\r\n" +
		"Content-Disposition: attachment; filename=\"report.json\"\r\n" +
		"Content-Transfer-Encoding: base64\r\n\r\n" +
		enc + "\r\n--b--\r\n"
	return []byte(raw)
}

// ── FetchMeta since ───────────────────────────────────────────────────────────

func TestFetchSince_UsesConfigFetchDays(t *testing.T) {
	bed := newFetchTestBed(t)
	_, err := bed.runner.Run(context.Background(), bed.boot)
	require.NoError(t, err)
	require.Len(t, bed.fetcher.FetchMetaCalls, 1)
	// FetchDays=14, now=2024-01-15 → 2024-01-01
	assert.Equal(t, time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC), bed.fetcher.FetchMetaCalls[0])
}

func TestFetchSince_FlagOverridesConfig(t *testing.T) {
	bed := newFetchTestBed(t)
	bed.boot.Options.Since = "7d"
	_, err := bed.runner.Run(context.Background(), bed.boot)
	require.NoError(t, err)
	require.Len(t, bed.fetcher.FetchMetaCalls, 1)
	// --since 7d, now=2024-01-15 → 2024-01-08
	assert.Equal(t, time.Date(2024, 1, 8, 0, 0, 0, 0, time.UTC), bed.fetcher.FetchMetaCalls[0])
}

func TestFetchSince_FlagIgnoresConfigFetchDays(t *testing.T) {
	bed := newFetchTestBed(t)
	bed.boot.Config.IMAP.FetchDays = 30
	bed.boot.Options.Since = "3d"
	_, err := bed.runner.Run(context.Background(), bed.boot)
	require.NoError(t, err)
	require.Len(t, bed.fetcher.FetchMetaCalls, 1)
	assert.Equal(t, time.Date(2024, 1, 12, 0, 0, 0, 0, time.UTC), bed.fetcher.FetchMetaCalls[0])
}

// ── UIDVALIDITY ───────────────────────────────────────────────────────────────

func TestFetch_UIDValidityFirstRun_SavesAndContinues(t *testing.T) {
	bed := newFetchTestBedBlank(t)
	code, err := bed.runner.Run(context.Background(), bed.boot)
	require.NoError(t, err)
	assert.Equal(t, exitOK, code)
	require.NotNil(t, bed.store.UIDValidity)
	assert.Equal(t, testUIDValidity, *bed.store.UIDValidity)
}

func TestFetch_UIDValidityMatch_Continues(t *testing.T) {
	bed := newFetchTestBed(t)
	code, err := bed.runner.Run(context.Background(), bed.boot)
	require.NoError(t, err)
	assert.Equal(t, exitOK, code)
}

func TestFetch_UIDValidityMismatch_SavesRecoveryAndExits(t *testing.T) {
	bed := newFetchTestBed(t)
	bed.fetcher.FetchMetaResult.UIDValidity = 99

	code, err := bed.runner.Run(context.Background(), bed.boot)
	require.NoError(t, err)
	assert.Equal(t, exitError, code)
	require.NotNil(t, bed.store.Recovery)
	assert.Equal(t, testUIDValidity, bed.store.Recovery.Prev)
	assert.Equal(t, uint32(99), bed.store.Recovery.Curr)
	require.Len(t, bed.notif.SystemErrors, 1)
	assert.Equal(t, notify.SystemErrorKindUIDValidityChanged, bed.notif.SystemErrors[0].Kind)
	assert.Equal(t, "fetch", bed.notif.SystemErrors[0].Component)
	assert.Equal(t, 1, bed.notif.FlushCount)
}

func TestFetch_LoadUIDValidityFails_ReportsStoreCorruption(t *testing.T) {
	bed := newFetchTestBed(t)
	bed.boot.Store = newErrStore(storetestutil.NewFakeStore(), errStoreOpts{
		loadUIDValidityErr: errors.New("db error"),
		uidValidity:        testUIDValidity,
	})

	code, err := bed.runner.Run(context.Background(), bed.boot)
	require.Error(t, err)
	assert.Equal(t, exitError, code)
	require.Len(t, bed.notif.SystemErrors, 1)
	assert.Equal(t, notify.SystemErrorKindStoreCorruption, bed.notif.SystemErrors[0].Kind)
	assert.Equal(t, 1, bed.notif.FlushCount)
}

func TestFetch_UIDValidityFirstSaveFails_Exits(t *testing.T) {
	bed := newFetchTestBedBlank(t)
	bed.boot.Store = newErrStore(storetestutil.NewFakeStore(), errStoreOpts{
		saveUIDValidityErr: errors.New("disk full"),
	})

	code, err := bed.runner.Run(context.Background(), bed.boot)
	require.Error(t, err)
	assert.Equal(t, exitError, code)
	assert.Empty(t, bed.fetcher.DownloadCalls)
}

func TestFetch_SaveRecoveryRequiredFails_ReportsStoreCorruption(t *testing.T) {
	bed := newFetchTestBed(t)
	bed.fetcher.FetchMetaResult.UIDValidity = 99
	bed.boot.Store = newErrStore(storetestutil.NewFakeStore(), errStoreOpts{
		saveRecoveryRequiredErr: errors.New("write error"),
		uidValidity:             testUIDValidity,
	})

	code, err := bed.runner.Run(context.Background(), bed.boot)
	require.Error(t, err)
	assert.Equal(t, exitError, code)
	require.Len(t, bed.notif.SystemErrors, 1)
	assert.Equal(t, notify.SystemErrorKindStoreCorruption, bed.notif.SystemErrors[0].Kind)
	assert.Equal(t, 1, bed.notif.FlushCount)
}

// ── Recovery guard ────────────────────────────────────────────────────────────

func TestFetch_RecoveryRequired_StopsImmediately(t *testing.T) {
	bed := newFetchTestBed(t)
	_ = bed.store.SaveRecoveryRequired(41, testUIDValidity, bed.now)

	code, err := bed.runner.Run(context.Background(), bed.boot)
	require.NoError(t, err)
	assert.Equal(t, exitError, code)
	assert.Empty(t, bed.fetcher.FetchMetaCalls)
	require.Len(t, bed.notif.SystemErrors, 1)
	assert.Equal(t, notify.SystemErrorKindRecoveryRequired, bed.notif.SystemErrors[0].Kind)
	assert.Equal(t, 1, bed.notif.FlushCount)
}

func TestFetch_LoadRecoveryRequiredFails_ReportsStoreCorruption(t *testing.T) {
	bed := newFetchTestBed(t)
	bed.boot.Store = newErrStore(storetestutil.NewFakeStore(), errStoreOpts{
		loadRecoveryRequiredErr: errors.New("io error"),
		uidValidity:             testUIDValidity,
	})

	code, err := bed.runner.Run(context.Background(), bed.boot)
	require.Error(t, err)
	assert.Equal(t, exitError, code)
	assert.Empty(t, bed.fetcher.FetchMetaCalls)
	require.Len(t, bed.notif.SystemErrors, 1)
	assert.Equal(t, notify.SystemErrorKindStoreCorruption, bed.notif.SystemErrors[0].Kind)
	assert.Equal(t, 1, bed.notif.FlushCount)
}

// ── IMAP connection ───────────────────────────────────────────────────────────

func TestFetch_IMAPConnectFails(t *testing.T) {
	bed := newFetchTestBed(t)
	bed.runner.newMailFetcher = func(_ imap.Config) (imap.MailFetcher, error) {
		return nil, errors.New("imap: connect to imap.example.com:993: refused")
	}

	code, err := bed.runner.Run(context.Background(), bed.boot)
	require.Error(t, err)
	assert.Equal(t, exitError, code)
	require.Len(t, bed.notif.SystemErrors, 1)
	assert.Equal(t, notify.SystemErrorKindIMAPConnectFailed, bed.notif.SystemErrors[0].Kind)
	assert.Equal(t, 1, bed.notif.FlushCount)
}

func TestFetch_IMAPAuthFails(t *testing.T) {
	bed := newFetchTestBed(t)
	bed.runner.newMailFetcher = func(_ imap.Config) (imap.MailFetcher, error) {
		return nil, errors.New("imap: login: authentication failed")
	}

	code, err := bed.runner.Run(context.Background(), bed.boot)
	require.Error(t, err)
	assert.Equal(t, exitError, code)
	require.Len(t, bed.notif.SystemErrors, 1)
	assert.Equal(t, notify.SystemErrorKindIMAPAuthFailed, bed.notif.SystemErrors[0].Kind)
	assert.Equal(t, 1, bed.notif.FlushCount)
}

func TestFetch_IMAPClientClosedOnSuccess(t *testing.T) {
	bed := newFetchTestBed(t)
	var closed bool
	bed.runner.newMailFetcher = func(_ imap.Config) (imap.MailFetcher, error) {
		return &closeSpy{FakeMailFetcher: bed.fetcher, closed: &closed}, nil
	}

	code, err := bed.runner.Run(context.Background(), bed.boot)
	require.NoError(t, err)
	assert.Equal(t, exitOK, code)
	assert.True(t, closed)
}

func TestFetch_IMAPClientClosedOnFetchMetaFailure(t *testing.T) {
	bed := newFetchTestBed(t)
	var closed bool
	bad := &imaptestutil.FakeMailFetcher{FetchMetaErr: errors.New("timeout")}
	bed.runner.newMailFetcher = func(_ imap.Config) (imap.MailFetcher, error) {
		return &closeSpy{FakeMailFetcher: bad, closed: &closed}, nil
	}

	code, err := bed.runner.Run(context.Background(), bed.boot)
	require.Error(t, err)
	assert.Equal(t, exitError, code)
	assert.True(t, closed)
}

func TestFetch_FetchMetaFails_ReportsOperationFailed(t *testing.T) {
	bed := newFetchTestBed(t)
	bed.fetcher.FetchMetaErr = errors.New("network timeout")

	code, err := bed.runner.Run(context.Background(), bed.boot)
	require.Error(t, err)
	assert.Equal(t, exitError, code)
	require.Len(t, bed.notif.SystemErrors, 1)
	assert.Equal(t, notify.SystemErrorKindIMAPOperationFailed, bed.notif.SystemErrors[0].Kind)
	assert.Equal(t, 1, bed.notif.FlushCount)
	assert.Empty(t, bed.fetcher.MarkSeenCalls)
}

func TestFetch_CredentialsMissing(t *testing.T) {
	tests := map[string]func(string) string{
		"both missing": func(_ string) string { return "" },
		"username missing": func(key string) string {
			if key == "TLSRPT_IMAP_PASSWORD" {
				return "secret"
			}
			return ""
		},
		"password missing": func(key string) string {
			if key == "TLSRPT_IMAP_USERNAME" {
				return "user@example.com"
			}
			return ""
		},
	}

	for name, getenv := range tests {
		t.Run(name, func(t *testing.T) {
			bed := newFetchTestBed(t)
			bed.runner.getenv = getenv
			connectCalled := false
			bed.runner.newMailFetcher = func(_ imap.Config) (imap.MailFetcher, error) {
				connectCalled = true
				return bed.fetcher, nil
			}

			code, err := bed.runner.Run(context.Background(), bed.boot)
			require.NoError(t, err)
			assert.Equal(t, exitError, code)
			require.Len(t, bed.notif.SystemErrors, 1)
			assert.Equal(t, notify.SystemErrorKindIMAPCredentialsMissing, bed.notif.SystemErrors[0].Kind)
			assert.Equal(t, 1, bed.notif.FlushCount)
			assert.False(t, connectCalled)
			assert.Empty(t, bed.fetcher.FetchMetaCalls)
		})
	}
}

// ── 4-way selection table ─────────────────────────────────────────────────────

func TestFetch_SEENEMLExists_Skipped(t *testing.T) {
	bed := newFetchTestBed(t)
	raw := simpleRawEML()
	bed.store.Emails[storetestutil.EmailKey{UID: testUID1, UIDValidity: testUIDValidity}] = &storetestutil.FakeEmailEntry{
		UID: testUID1, UIDValidity: testUIDValidity, InternalDate: testDate, RawEML: raw,
	}
	bed.fetcher.FetchMetaResult.Messages = []imap.MessageMeta{
		{UID: testUID1, Size: uint32(len(raw)), Date: testDate, Seen: true},
	}

	code, err := bed.runner.Run(context.Background(), bed.boot)
	require.NoError(t, err)
	assert.Equal(t, exitOK, code)
	assert.Empty(t, bed.fetcher.DownloadCalls)
	assert.Empty(t, bed.fetcher.MarkSeenCalls)
}

func TestFetch_UNSEENNoEML_DownloadedAndMarkedSeen(t *testing.T) {
	bed := newFetchTestBed(t)
	raw := []byte("X-Custom: folded\r\n\tvalue\r\nX-Custom: second\r\n\r\nbody")
	bed.fetcher.FetchMetaResult.Messages = []imap.MessageMeta{
		{UID: testUID1, Size: uint32(len(raw)), Date: testDate, Seen: false, MessageID: "m1"},
	}
	bed.fetcher.DownloadResult[testUID1] = raw

	code, err := bed.runner.Run(context.Background(), bed.boot)
	require.NoError(t, err)
	assert.Equal(t, exitOK, code)
	require.Len(t, bed.fetcher.DownloadCalls, 1)
	assert.Equal(t, []uint32{testUID1}, bed.fetcher.DownloadCalls[0])
	require.Len(t, bed.fetcher.MarkSeenCalls, 1)
	assert.Equal(t, []uint32{testUID1}, bed.fetcher.MarkSeenCalls[0])
	saved := bed.store.Emails[storetestutil.EmailKey{UID: testUID1, UIDValidity: testUIDValidity}]
	require.NotNil(t, saved)
	assert.Equal(t, raw, saved.RawEML)
}

func TestFetch_UNSEENEMLExists_NoDownloadProcessedAndMarkedSeen(t *testing.T) {
	bed := newFetchTestBed(t)
	raw := simpleRawEML()
	bed.store.Emails[storetestutil.EmailKey{UID: testUID1, UIDValidity: testUIDValidity}] = &storetestutil.FakeEmailEntry{
		UID: testUID1, UIDValidity: testUIDValidity, InternalDate: testDate, RawEML: raw,
	}
	bed.fetcher.FetchMetaResult.Messages = []imap.MessageMeta{
		{UID: testUID1, Size: uint32(len(raw)), Date: testDate, Seen: false, MessageID: "m1"},
	}

	code, err := bed.runner.Run(context.Background(), bed.boot)
	require.NoError(t, err)
	assert.Equal(t, exitOK, code)
	assert.Empty(t, bed.fetcher.DownloadCalls)
	require.Len(t, bed.fetcher.MarkSeenCalls, 1)
	assert.Equal(t, []uint32{testUID1}, bed.fetcher.MarkSeenCalls[0])
}

func TestFetch_UNSEENEMLExists_DiskReadFails_LogsWarningAndContinues(t *testing.T) {
	bed := newFetchTestBed(t)
	raw := tlsrptRawEML("Corp2", "r2", 0)
	// msg1: UNSEEN + EML in store, but loadLocalEML will fail for it
	bed.store.Emails[storetestutil.EmailKey{UID: testUID1, UIDValidity: testUIDValidity}] = &storetestutil.FakeEmailEntry{
		UID: testUID1, UIDValidity: testUIDValidity, InternalDate: testDate, RawEML: simpleRawEML(),
	}
	// msg2: UNSEEN + no EML, will be downloaded normally
	bed.fetcher.FetchMetaResult.Messages = []imap.MessageMeta{
		{UID: testUID1, Size: uint32(len(simpleRawEML())), Date: testDate, Seen: false, MessageID: "m1"},
		{UID: testUID2, Size: uint32(len(raw)), Date: testDate, Seen: false, MessageID: "m2"},
	}
	bed.fetcher.DownloadResult[testUID2] = raw
	// Inject a broken loadLocalEML for UID1
	bed.runner.loadLocalEML = func(_ string, uid, _ uint32, _ time.Time) ([]byte, error) {
		if uid == testUID1 {
			return nil, errors.New("disk read error")
		}
		return nil, errors.New("eml not found")
	}

	code, err := bed.runner.Run(context.Background(), bed.boot)
	require.NoError(t, err)
	assert.Equal(t, exitOK, code)
	// parse_failure warning for msg1
	require.Len(t, bed.notif.Warnings, 1)
	assert.Equal(t, notify.WarningKindParseFailure, bed.notif.Warnings[0].Kind)
	assert.Equal(t, testUID1, bed.notif.Warnings[0].UID)
	// msg2 still processed and report saved
	assert.Len(t, bed.store.Reports, 1)
	// both UIDs still marked seen
	require.Len(t, bed.fetcher.MarkSeenCalls, 1)
	assert.Contains(t, bed.fetcher.MarkSeenCalls[0], testUID1)
	assert.Contains(t, bed.fetcher.MarkSeenCalls[0], testUID2)
}

func TestFetch_SEENNoEML_DownloadedNoMarkSeen(t *testing.T) {
	bed := newFetchTestBed(t)
	bed.fetcher.FetchMetaResult.Messages = []imap.MessageMeta{
		{UID: testUID1, Size: 100, Date: testDate, Seen: true, MessageID: "m1"},
	}
	bed.fetcher.DownloadResult[testUID1] = simpleRawEML()

	code, err := bed.runner.Run(context.Background(), bed.boot)
	require.NoError(t, err)
	assert.Equal(t, exitOK, code)
	require.Len(t, bed.fetcher.DownloadCalls, 1)
	assert.Empty(t, bed.fetcher.MarkSeenCalls)
}

func TestFetch_SEENNoEMLWithFailure_NoLogAlert(t *testing.T) {
	bed := newFetchTestBed(t)
	raw := tlsrptRawEML("Corp", "r1", 5)
	bed.fetcher.FetchMetaResult.Messages = []imap.MessageMeta{
		{UID: testUID1, Size: uint32(len(raw)), Date: testDate, Seen: true, MessageID: "m1"},
	}
	bed.fetcher.DownloadResult[testUID1] = raw

	code, err := bed.runner.Run(context.Background(), bed.boot)
	require.NoError(t, err)
	assert.Equal(t, exitOK, code)
	assert.Empty(t, bed.notif.Alerts, "SEEN message: no alert even with failures")
}

// ── RFC822.SIZE mismatch ──────────────────────────────────────────────────────

func TestFetch_SizeMismatch_UNSEENNoEML_LogsWarning(t *testing.T) {
	bed := newFetchTestBed(t)
	raw := simpleRawEML()
	bed.fetcher.FetchMetaResult.Messages = []imap.MessageMeta{
		{UID: testUID1, Size: uint32(len(raw) + 100), Date: testDate, Seen: false, MessageID: "m1"},
	}
	bed.fetcher.DownloadResult[testUID1] = raw

	code, err := bed.runner.Run(context.Background(), bed.boot)
	require.NoError(t, err)
	assert.Equal(t, exitOK, code)
	require.Len(t, bed.notif.Warnings, 1)
	assert.Equal(t, notify.WarningKindSizeMismatch, bed.notif.Warnings[0].Kind)
	assert.Equal(t, testUID1, bed.notif.Warnings[0].UID)
}

func TestFetch_SizeMismatch_SEENEMLExists_LogsWarningAndSkips(t *testing.T) {
	bed := newFetchTestBed(t)
	raw := simpleRawEML()
	bed.store.Emails[storetestutil.EmailKey{UID: testUID1, UIDValidity: testUIDValidity}] = &storetestutil.FakeEmailEntry{
		UID: testUID1, UIDValidity: testUIDValidity, InternalDate: testDate, RawEML: raw,
	}
	bed.fetcher.FetchMetaResult.Messages = []imap.MessageMeta{
		{UID: testUID1, Size: uint32(len(raw) + 100), Date: testDate, Seen: true, MessageID: "m1"},
	}

	code, err := bed.runner.Run(context.Background(), bed.boot)
	require.NoError(t, err)
	assert.Equal(t, exitOK, code)
	require.Len(t, bed.notif.Warnings, 1)
	assert.Equal(t, notify.WarningKindSizeMismatch, bed.notif.Warnings[0].Kind)
	assert.Empty(t, bed.fetcher.DownloadCalls)
}

// ── Failure alerts ────────────────────────────────────────────────────────────

func TestFetch_UNSEENWithFailure_LogsAlert(t *testing.T) {
	bed := newFetchTestBed(t)
	raw := tlsrptRawEML("Corp", "r1", 5)
	bed.fetcher.FetchMetaResult.Messages = []imap.MessageMeta{
		{UID: testUID1, Size: uint32(len(raw)), Date: testDate, Seen: false, MessageID: "m1"},
	}
	bed.fetcher.DownloadResult[testUID1] = raw

	code, err := bed.runner.Run(context.Background(), bed.boot)
	require.NoError(t, err)
	assert.Equal(t, exitOK, code)
	require.Len(t, bed.notif.Alerts, 1)
	assert.Equal(t, "Corp", bed.notif.Alerts[0].OrganizationName)
	assert.Equal(t, int64(5), bed.notif.Alerts[0].FailureCount)
}

func TestFetch_UNSEENZeroFailures_NoAlert(t *testing.T) {
	bed := newFetchTestBed(t)
	raw := tlsrptRawEML("Corp", "r1", 0)
	bed.fetcher.FetchMetaResult.Messages = []imap.MessageMeta{
		{UID: testUID1, Size: uint32(len(raw)), Date: testDate, Seen: false, MessageID: "m1"},
	}
	bed.fetcher.DownloadResult[testUID1] = raw

	code, err := bed.runner.Run(context.Background(), bed.boot)
	require.NoError(t, err)
	assert.Equal(t, exitOK, code)
	assert.Empty(t, bed.notif.Alerts)
}

// ── Parse failures ────────────────────────────────────────────────────────────

func TestFetch_ParseFailure_LogsWarningAndContinues(t *testing.T) {
	bed := newFetchTestBed(t)

	// msg1: malformed TLSRPT JSON
	badRaw := "From: a@b.com\r\n" +
		"Content-Type: multipart/mixed; boundary=\"b\"\r\n\r\n" +
		"--b\r\n" +
		"Content-Type: application/tlsrpt+json; name=\"r.json\"\r\n" +
		"Content-Disposition: attachment; filename=\"r.json\"\r\n" +
		"Content-Transfer-Encoding: base64\r\n\r\n" +
		base64.StdEncoding.EncodeToString([]byte("not json")) + "\r\n--b--\r\n"
	// msg2: valid TLSRPT
	goodRaw := tlsrptRawEML("Corp2", "r2", 0)

	bed.fetcher.FetchMetaResult.Messages = []imap.MessageMeta{
		{UID: testUID1, Size: uint32(len(badRaw)), Date: testDate, Seen: false, MessageID: "bad"},
		{UID: testUID2, Size: uint32(len(goodRaw)), Date: testDate, Seen: false, MessageID: "good"},
	}
	bed.fetcher.DownloadResult[testUID1] = []byte(badRaw)
	bed.fetcher.DownloadResult[testUID2] = goodRaw

	code, err := bed.runner.Run(context.Background(), bed.boot)
	require.NoError(t, err)
	assert.Equal(t, exitOK, code)

	require.Len(t, bed.notif.Warnings, 1)
	assert.Equal(t, notify.WarningKindParseFailure, bed.notif.Warnings[0].Kind)
	assert.Equal(t, testUID1, bed.notif.Warnings[0].UID)

	// Both messages are still marked seen
	require.Len(t, bed.fetcher.MarkSeenCalls, 1)
	assert.Contains(t, bed.fetcher.MarkSeenCalls[0], testUID1)
	assert.Contains(t, bed.fetcher.MarkSeenCalls[0], testUID2)
	assert.Len(t, bed.store.Reports, 1)
}

// ── Store failures ────────────────────────────────────────────────────────────

func TestFetch_DownloadFails_ExitsWithoutSave(t *testing.T) {
	bed := newFetchTestBed(t)
	bed.fetcher.FetchMetaResult.Messages = []imap.MessageMeta{
		{UID: testUID1, Size: 100, Date: testDate, Seen: false},
	}
	bed.fetcher.DownloadErr = errors.New("network error")

	code, err := bed.runner.Run(context.Background(), bed.boot)
	require.Error(t, err)
	assert.Equal(t, exitError, code)
	assert.Empty(t, bed.store.Emails)
	assert.Empty(t, bed.fetcher.MarkSeenCalls)
	require.Len(t, bed.notif.SystemErrors, 1)
	assert.Equal(t, notify.SystemErrorKindIMAPOperationFailed, bed.notif.SystemErrors[0].Kind)
	assert.Equal(t, 1, bed.notif.FlushCount)
}

func TestFetch_DownloadMissingUID_ExitsWithoutMarkSeen(t *testing.T) {
	bed := newFetchTestBed(t)
	bed.fetcher.FetchMetaResult.Messages = []imap.MessageMeta{
		{UID: testUID1, Size: 100, Date: testDate, Seen: false},
	}

	code, err := bed.runner.Run(context.Background(), bed.boot)
	require.Error(t, err)
	assert.Equal(t, exitError, code)
	assert.Empty(t, bed.store.Emails)
	assert.Empty(t, bed.fetcher.MarkSeenCalls)
	require.Len(t, bed.notif.SystemErrors, 1)
	assert.Equal(t, notify.SystemErrorKindIMAPOperationFailed, bed.notif.SystemErrors[0].Kind)
	assert.Equal(t, 1, bed.notif.FlushCount)
}

func TestFetch_SaveEmailFails_ExitsBeforeMetas(t *testing.T) {
	bed := newFetchTestBed(t)
	bed.fetcher.FetchMetaResult.Messages = []imap.MessageMeta{
		{UID: testUID1, Size: 100, Date: testDate, Seen: false},
	}
	bed.fetcher.DownloadResult[testUID1] = simpleRawEML()

	cs := &countingStore{FakeStore: storetestutil.NewFakeStore()}
	uid := testUIDValidity
	cs.UIDValidity = &uid
	cs.saveEmailErr = errors.New("disk full")
	bed.boot.Store = cs

	code, err := bed.runner.Run(context.Background(), bed.boot)
	require.Error(t, err)
	assert.Equal(t, exitError, code)
	assert.Equal(t, 0, cs.saveEmailMetasCount, "SaveEmailMetas must not be called")
	assert.Empty(t, bed.fetcher.MarkSeenCalls)
}

func TestFetch_SaveEmailMetasFails_NoSaveReportsOrMarkSeen(t *testing.T) {
	bed := newFetchTestBed(t)
	// Use TLSRPT content so SaveReports would be called if not properly blocked.
	raw := tlsrptRawEML("Corp", "r1", 1)
	bed.fetcher.FetchMetaResult.Messages = []imap.MessageMeta{
		{UID: testUID1, Size: uint32(len(raw)), Date: testDate, Seen: false},
	}
	bed.fetcher.DownloadResult[testUID1] = raw

	cs := &countingStore{FakeStore: storetestutil.NewFakeStore()}
	uid := testUIDValidity
	cs.UIDValidity = &uid
	cs.saveEmailMetasErr = errors.New("write error")
	bed.boot.Store = cs

	code, err := bed.runner.Run(context.Background(), bed.boot)
	require.Error(t, err)
	assert.Equal(t, exitError, code)
	assert.Equal(t, 0, cs.saveReportsCount, "SaveReports must not be called")
	assert.Empty(t, bed.fetcher.MarkSeenCalls)
}

func TestFetch_SaveReportsFails_NoFlushOrMarkSeen(t *testing.T) {
	bed := newFetchTestBed(t)
	raw := tlsrptRawEML("Corp", "r1", 1)
	bed.fetcher.FetchMetaResult.Messages = []imap.MessageMeta{
		{UID: testUID1, Size: uint32(len(raw)), Date: testDate, Seen: false},
	}
	bed.fetcher.DownloadResult[testUID1] = raw

	cs := &countingStore{FakeStore: storetestutil.NewFakeStore()}
	uid := testUIDValidity
	cs.UIDValidity = &uid
	cs.saveReportsErr = errors.New("db error")
	bed.boot.Store = cs

	code, err := bed.runner.Run(context.Background(), bed.boot)
	require.Error(t, err)
	assert.Equal(t, exitError, code)
	assert.Equal(t, 0, bed.notif.FlushCount, "Flush must not be called")
	assert.Empty(t, bed.fetcher.MarkSeenCalls)
}

// ── At-least-once guarantee ───────────────────────────────────────────────────

func TestFetch_FlushFails_MarkSeenNotCalled(t *testing.T) {
	bed := newFetchTestBed(t)
	bed.fetcher.FetchMetaResult.Messages = []imap.MessageMeta{
		{UID: testUID1, Size: 100, Date: testDate, Seen: false},
	}
	bed.fetcher.DownloadResult[testUID1] = simpleRawEML()
	bed.notif.FlushError = errors.New("network error")

	code, err := bed.runner.Run(context.Background(), bed.boot)
	require.Error(t, err)
	assert.Equal(t, exitError, code)
	assert.Empty(t, bed.fetcher.MarkSeenCalls)
}

func TestFetch_MarkSeenFails_SaveUIDValidityNotCalled(t *testing.T) {
	bed := newFetchTestBed(t) // UIDValidity=42 already stored
	bed.fetcher.FetchMetaResult.Messages = []imap.MessageMeta{
		{UID: testUID1, Size: 100, Date: testDate, Seen: false},
	}
	bed.fetcher.DownloadResult[testUID1] = simpleRawEML()
	bed.fetcher.MarkSeenErr = errors.New("IMAP error")

	cs := &countingStore{FakeStore: storetestutil.NewFakeStore()}
	uid := testUIDValidity
	cs.UIDValidity = &uid
	bed.boot.Store = cs

	code, err := bed.runner.Run(context.Background(), bed.boot)
	require.Error(t, err)
	assert.Equal(t, exitError, code)
	// For found=true+match case, step 5 does NOT call SaveUIDValidity.
	// So the only SaveUIDValidity call would be the final one (step 14), which is after MarkSeen.
	assert.Equal(t, 0, cs.saveUIDValidityCount, "final SaveUIDValidity must not be called after MarkSeen failure")
}

func TestFetch_FinalSaveUIDValidityFails_Exits(t *testing.T) {
	bed := newFetchTestBed(t)
	cs := &countingStore{FakeStore: storetestutil.NewFakeStore()}
	uid := testUIDValidity
	cs.UIDValidity = &uid
	cs.saveUIDValidityErr = errors.New("disk full")
	bed.boot.Store = cs

	code, err := bed.runner.Run(context.Background(), bed.boot)
	require.Error(t, err)
	assert.Equal(t, exitError, code)
}

// ── Batch call counts ─────────────────────────────────────────────────────────

func TestFetch_SaveEmailMetas_CalledOnce(t *testing.T) {
	bed := newFetchTestBed(t)
	bed.fetcher.FetchMetaResult.Messages = []imap.MessageMeta{
		{UID: testUID1, Size: 100, Date: testDate, Seen: false},
		{UID: testUID2, Size: 100, Date: testDate, Seen: false},
	}
	bed.fetcher.DownloadResult[testUID1] = simpleRawEML()
	bed.fetcher.DownloadResult[testUID2] = simpleRawEML()

	cs := &countingStore{FakeStore: storetestutil.NewFakeStore()}
	uid := testUIDValidity
	cs.UIDValidity = &uid
	bed.boot.Store = cs

	code, err := bed.runner.Run(context.Background(), bed.boot)
	require.NoError(t, err)
	assert.Equal(t, exitOK, code)
	assert.Equal(t, 1, cs.saveEmailMetasCount)
}

func TestFetch_SaveReports_CalledWithAllParsedReports(t *testing.T) {
	bed := newFetchTestBed(t)
	raw1 := tlsrptRawEML("Corp1", "r1", 1)
	raw2 := tlsrptRawEML("Corp2", "r2", 0)
	bed.fetcher.FetchMetaResult.Messages = []imap.MessageMeta{
		{UID: testUID1, Size: uint32(len(raw1)), Date: testDate, Seen: false},
		{UID: testUID2, Size: uint32(len(raw2)), Date: testDate, Seen: false},
	}
	bed.fetcher.DownloadResult[testUID1] = raw1
	bed.fetcher.DownloadResult[testUID2] = raw2

	code, err := bed.runner.Run(context.Background(), bed.boot)
	require.NoError(t, err)
	assert.Equal(t, exitOK, code)
	assert.Len(t, bed.store.Reports, 2)
}

func TestFetch_FinalSaveUIDValidity_CalledOnce(t *testing.T) {
	bed := newFetchTestBed(t) // UIDValidity=42 already stored

	cs := &countingStore{FakeStore: storetestutil.NewFakeStore()}
	uid := testUIDValidity
	cs.UIDValidity = &uid
	bed.boot.Store = cs

	code, err := bed.runner.Run(context.Background(), bed.boot)
	require.NoError(t, err)
	assert.Equal(t, exitOK, code)
	// Only the final SaveUIDValidity call (step 14); step 5 is skipped for found=true+match.
	assert.Equal(t, 1, cs.saveUIDValidityCount)
}

func TestFetch_NormalRun_ExitsZero(t *testing.T) {
	bed := newFetchTestBed(t)
	code, err := bed.runner.Run(context.Background(), bed.boot)
	require.NoError(t, err)
	assert.Equal(t, exitOK, code)
}

// ── test doubles ──────────────────────────────────────────────────────────────

// closeSpy wraps FakeMailFetcher to record Close calls.
type closeSpy struct {
	*imaptestutil.FakeMailFetcher
	closed *bool
}

func (s *closeSpy) Close() error {
	*s.closed = true
	return s.FakeMailFetcher.Close()
}

// errStoreOpts holds error-injection configuration for errStore.
type errStoreOpts struct {
	uidValidity             uint32
	loadRecoveryRequiredErr error
	loadUIDValidityErr      error
	saveUIDValidityErr      error
	saveRecoveryRequiredErr error
}

// errStore wraps FakeStore and injects errors for specific methods.
// Only override methods that need error injection; all others delegate via embedding.
type errStore struct {
	*storetestutil.FakeStore
	opts errStoreOpts
}

func newErrStore(base *storetestutil.FakeStore, opts errStoreOpts) *errStore {
	if opts.uidValidity != 0 {
		v := opts.uidValidity
		base.UIDValidity = &v
	}
	return &errStore{FakeStore: base, opts: opts}
}

func (e *errStore) LoadRecoveryRequired() (uint32, uint32, time.Time, bool, error) {
	if e.opts.loadRecoveryRequiredErr != nil {
		return 0, 0, time.Time{}, false, e.opts.loadRecoveryRequiredErr
	}
	return e.FakeStore.LoadRecoveryRequired()
}

func (e *errStore) LoadUIDValidity() (uint32, bool, error) {
	if e.opts.loadUIDValidityErr != nil {
		return 0, false, e.opts.loadUIDValidityErr
	}
	return e.FakeStore.LoadUIDValidity()
}

func (e *errStore) SaveUIDValidity(v uint32) error {
	if e.opts.saveUIDValidityErr != nil {
		return e.opts.saveUIDValidityErr
	}
	return e.FakeStore.SaveUIDValidity(v)
}

func (e *errStore) SaveRecoveryRequired(prev, curr uint32, detectedAt time.Time) error {
	if e.opts.saveRecoveryRequiredErr != nil {
		return e.opts.saveRecoveryRequiredErr
	}
	return e.FakeStore.SaveRecoveryRequired(prev, curr, detectedAt)
}

// countingStore tracks call counts and injects errors for specific methods.
type countingStore struct {
	*storetestutil.FakeStore
	saveEmailErr       error
	saveEmailMetasErr  error
	saveReportsErr     error
	saveUIDValidityErr error

	saveEmailMetasCount  int
	saveReportsCount     int
	saveUIDValidityCount int
}

func (c *countingStore) SaveEmail(uid, uidValidity uint32, internalDate time.Time, rawEML []byte) error {
	if c.saveEmailErr != nil {
		return c.saveEmailErr
	}
	return c.FakeStore.SaveEmail(uid, uidValidity, internalDate, rawEML)
}

func (c *countingStore) SaveEmailMetas(metas []store.EmailMeta) error {
	c.saveEmailMetasCount++
	if c.saveEmailMetasErr != nil {
		return c.saveEmailMetasErr
	}
	return c.FakeStore.SaveEmailMetas(metas)
}

func (c *countingStore) SaveReports(inputs []store.ReportInput) error {
	c.saveReportsCount++
	if c.saveReportsErr != nil {
		return c.saveReportsErr
	}
	return c.FakeStore.SaveReports(inputs)
}

func (c *countingStore) SaveUIDValidity(v uint32) error {
	c.saveUIDValidityCount++
	if c.saveUIDValidityErr != nil {
		return c.saveUIDValidityErr
	}
	return c.FakeStore.SaveUIDValidity(v)
}

// Compile-time interface checks.
var (
	_ store.Store = (*errStore)(nil)
	_ store.Store = (*countingStore)(nil)
)
