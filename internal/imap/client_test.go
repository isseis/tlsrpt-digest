package imap

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"log/slog"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	goimap "github.com/emersion/go-imap"
	"github.com/stretchr/testify/require"
)

func TestBuildTLSConfigCustomCA(t *testing.T) {
	t.Parallel()

	certPath := filepath.Join(t.TempDir(), "ca.pem")
	require.NoError(t, os.WriteFile(certPath, createTestPEMCertificate(t), 0o600))

	cfg, err := buildTLSConfig(Config{TLSCACert: certPath})
	require.NoError(t, err)
	require.NotNil(t, cfg.RootCAs)
	require.Equal(t, uint16(tls.VersionTLS12), cfg.MinVersion)
	require.False(t, cfg.InsecureSkipVerify)
}

func TestBuildTLSConfigMissingFile(t *testing.T) {
	t.Parallel()

	_, err := buildTLSConfig(Config{TLSCACert: filepath.Join(t.TempDir(), "missing.pem")})
	require.Error(t, err)
}

func TestBuildTLSConfigInvalidPEM(t *testing.T) {
	t.Parallel()

	certPath := filepath.Join(t.TempDir(), "invalid.pem")
	require.NoError(t, os.WriteFile(certPath, []byte("not-pem"), 0o600))

	_, err := buildTLSConfig(Config{TLSCACert: certPath})
	require.Error(t, err)
}

func TestBuildTLSConfigSystemCA(t *testing.T) {
	t.Parallel()

	cfg, err := buildTLSConfig(Config{})
	require.NoError(t, err)
	require.Nil(t, cfg.RootCAs)
	require.False(t, cfg.InsecureSkipVerify)
	require.Equal(t, uint16(tls.VersionTLS12), cfg.MinVersion)
}

// TestBuildTLSConfigInsecureSkipVerify verifies that InsecureSkipVerify is reflected in tls.Config.
func TestBuildTLSConfigInsecureSkipVerify(t *testing.T) {
	t.Parallel()

	cfg, err := buildTLSConfig(Config{InsecureSkipVerify: true})
	require.NoError(t, err)
	require.True(t, cfg.InsecureSkipVerify)
	require.Nil(t, cfg.RootCAs)
	require.Equal(t, uint16(tls.VersionTLS12), cfg.MinVersion)
}

func TestTruncateToDate(t *testing.T) {
	t.Parallel()

	loc := time.FixedZone("+09", 9*60*60)
	in := time.Date(2026, 5, 13, 14, 23, 58, 123456, loc)

	got := truncateToDate(in)

	require.Equal(t, time.Date(2026, 5, 13, 0, 0, 0, 0, loc), got)
}

func TestIsTooLarge(t *testing.T) {
	t.Parallel()

	require.True(t, isTooLarge(101, 100))
	require.False(t, isTooLarge(100, 100))
	require.False(t, isTooLarge(999, 0))
}

func TestClose_SendsIMAPCloseOnlyAfterEXAMINE(t *testing.T) {
	t.Parallel()

	// After FetchMeta (EXAMINE, lastSelectReadOnly=true), Close must send IMAP CLOSE.
	t.Run("after_FetchMeta_sends_CLOSE", func(t *testing.T) {
		t.Parallel()
		// uidSearchResult is nil by default → FetchMeta returns empty, no UidFetch.
		s := &fakeSession{selectMailboxStatus: &goimap.MailboxStatus{Name: "INBOX"}}
		c := &imapClient{cfg: Config{Mailbox: "INBOX"}, session: s}
		_, err := c.FetchMeta(context.Background(), time.Time{})
		require.NoError(t, err)
		require.NoError(t, c.Close())
		require.True(t, s.closeCalled, "IMAP CLOSE must be sent after EXAMINE (FetchMeta)")
	})

	// Download uses EXAMINE (read-only, BODY.PEEK), so Close must send IMAP CLOSE.
	t.Run("after_Download_sends_CLOSE", func(t *testing.T) {
		t.Parallel()
		s := &fakeSession{
			selectMailboxStatus: &goimap.MailboxStatus{Name: "INBOX"},
			uidFetchFn: func(_ *goimap.SeqSet, _ []goimap.FetchItem, ch chan *goimap.Message) error {
				msg := &goimap.Message{Uid: 1, Body: map[*goimap.BodySectionName]goimap.Literal{}}
				section := &goimap.BodySectionName{Peek: true}
				msg.Body[section] = newByteLiteral("body")
				ch <- msg
				close(ch)
				return nil
			},
		}
		c := &imapClient{cfg: Config{Mailbox: "INBOX"}, session: s}
		_, err := c.Download(context.Background(), []uint32{1})
		require.NoError(t, err)
		require.NoError(t, c.Close())
		require.True(t, s.closeCalled, "IMAP CLOSE must be sent after EXAMINE (Download)")
	})

	// After MarkSeen (SELECT, lastSelectReadOnly=false), Close must NOT send IMAP CLOSE.
	t.Run("after_MarkSeen_no_CLOSE", func(t *testing.T) {
		t.Parallel()
		s := &fakeSession{
			selectMailboxStatus: &goimap.MailboxStatus{Name: "INBOX"},
		}
		c := &imapClient{cfg: Config{Mailbox: "INBOX"}, session: s}
		require.NoError(t, c.MarkSeen(context.Background(), []uint32{1}))
		require.NoError(t, c.Close())
		require.False(t, s.closeCalled, "IMAP CLOSE must NOT be sent after SELECT (MarkSeen)")
	})

	// A fresh client that never called Select must not send IMAP CLOSE.
	t.Run("fresh_client_no_CLOSE", func(t *testing.T) {
		t.Parallel()
		s := &fakeSession{}
		c := &imapClient{cfg: Config{Mailbox: "INBOX"}, session: s}
		require.NoError(t, c.Close())
		require.False(t, s.closeCalled, "IMAP CLOSE must NOT be sent on a fresh client")
	})
}

func TestDownloadMissingUID(t *testing.T) {
	t.Parallel()

	body := "From: a@example.com\r\nTo: b@example.com\r\nSubject: x\r\n\r\nhello"
	s := &fakeSession{
		selectMailboxStatus: &goimap.MailboxStatus{Name: "INBOX"},
		uidFetchFn: func(_ *goimap.SeqSet, _ []goimap.FetchItem, ch chan *goimap.Message) error {
			defer close(ch)
			msg := &goimap.Message{Uid: 10, Body: map[*goimap.BodySectionName]goimap.Literal{}}
			section := &goimap.BodySectionName{Peek: true}
			msg.Body[section] = newByteLiteral(body)
			ch <- msg
			return nil
		},
	}

	c := &imapClient{cfg: Config{Mailbox: "INBOX"}, session: s}
	_, err := c.Download(context.Background(), []uint32{10, 11})
	require.Error(t, err)
	require.Contains(t, err.Error(), "uid not found: 11")
}

func TestImapClient_DeleteOlderThan_ZeroCutoff(t *testing.T) {
	t.Parallel()

	s := &fakeSession{supportErr: errors.New("must not be called")}
	c := &imapClient{cfg: Config{Mailbox: "INBOX"}, session: s}

	deleted, err := c.DeleteOlderThan(context.Background(), time.Time{})
	require.NoError(t, err)
	require.Zero(t, deleted)
}

func TestImapClient_DeleteOlderThan_UIDPLUSUnsupported(t *testing.T) {
	var logBuf bytes.Buffer
	oldLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelInfo})))
	t.Cleanup(func() { slog.SetDefault(oldLogger) })

	s := &fakeSession{supportResult: map[string]bool{"UIDPLUS": false}}
	c := &imapClient{cfg: Config{Mailbox: "INBOX"}, session: s}

	deleted, err := c.DeleteOlderThan(context.Background(), time.Now())
	require.NoError(t, err)
	require.Zero(t, deleted)
	require.Empty(t, s.uidStoreCalls)
	require.Empty(t, s.uidExpungeCalls)
	require.Contains(t, logBuf.String(), "level=WARN")
}

func TestImapClient_DeleteOlderThan_Success(t *testing.T) {
	t.Parallel()

	cutoff := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	s := &fakeSession{
		supportResult:       map[string]bool{"UIDPLUS": true},
		selectMailboxStatus: &goimap.MailboxStatus{Name: "INBOX", ReadOnly: false},
		uidSearchResult:     []uint32{5, 6},
	}
	c := &imapClient{cfg: Config{Mailbox: "INBOX"}, session: s}

	deleted, err := c.DeleteOlderThan(context.Background(), cutoff)
	require.NoError(t, err)
	require.Equal(t, 2, deleted)
	require.NotEmpty(t, s.selectReadOnlyCalls)
	require.False(t, s.selectReadOnlyCalls[len(s.selectReadOnlyCalls)-1], "DeleteOlderThan must use read-write SELECT")
	require.Equal(t, truncateToDate(cutoff), s.uidSearchCriteria.Before)
	require.Len(t, s.uidStoreCalls, 1)
	require.Equal(t, []any{goimap.DeletedFlag}, s.uidStoreCalls[0].flags)
	require.Len(t, s.uidExpungeCalls, 1)
	require.True(t, s.uidExpungeCalls[0].Contains(5))
	require.True(t, s.uidExpungeCalls[0].Contains(6))
	require.False(t, s.uidExpungeCalls[0].Contains(7))
}

func TestImapClient_DeleteOlderThan_EmptySearch(t *testing.T) {
	t.Parallel()

	s := &fakeSession{
		supportResult:       map[string]bool{"UIDPLUS": true},
		selectMailboxStatus: &goimap.MailboxStatus{Name: "INBOX", ReadOnly: false},
		uidSearchResult:     nil,
	}
	c := &imapClient{cfg: Config{Mailbox: "INBOX"}, session: s}

	deleted, err := c.DeleteOlderThan(context.Background(), time.Now())
	require.NoError(t, err)
	require.Zero(t, deleted)
	require.Empty(t, s.uidStoreCalls)
	require.Empty(t, s.uidExpungeCalls)
}

func TestImapClient_DeleteOlderThan_ReadOnly(t *testing.T) {
	t.Parallel()

	s := &fakeSession{
		supportResult:       map[string]bool{"UIDPLUS": true},
		selectMailboxStatus: &goimap.MailboxStatus{Name: "INBOX", ReadOnly: true},
	}
	c := &imapClient{cfg: Config{Mailbox: "INBOX"}, session: s}

	_, err := c.DeleteOlderThan(context.Background(), time.Now())
	require.ErrorIs(t, err, ErrMailboxReadOnly)
}

func TestImapClient_DeleteOlderThan_SupportError(t *testing.T) {
	t.Parallel()

	supportErr := errors.New("capability error")
	s := &fakeSession{supportErr: supportErr}
	c := &imapClient{cfg: Config{Mailbox: "INBOX"}, session: s}

	_, err := c.DeleteOlderThan(context.Background(), time.Now())
	require.ErrorIs(t, err, supportErr)
}

func TestImapClient_SearchOlderThan_ZeroCutoff(t *testing.T) {
	t.Parallel()

	s := &fakeSession{selectErr: errors.New("must not be called")}
	c := &imapClient{cfg: Config{Mailbox: "INBOX"}, session: s}

	uids, err := c.SearchOlderThan(context.Background(), time.Time{})
	require.NoError(t, err)
	require.Equal(t, []uint32{}, uids)
}

func TestImapClient_SearchOlderThan_UsesExamine(t *testing.T) {
	t.Parallel()

	s := &fakeSession{
		selectMailboxStatus: &goimap.MailboxStatus{Name: "INBOX", ReadOnly: true},
		uidSearchResult:     []uint32{7},
	}
	c := &imapClient{cfg: Config{Mailbox: "INBOX"}, session: s}

	uids, err := c.SearchOlderThan(context.Background(), time.Now())
	require.NoError(t, err)
	require.Equal(t, []uint32{7}, uids)
	require.NotEmpty(t, s.selectReadOnlyCalls)
	require.True(t, s.selectReadOnlyCalls[len(s.selectReadOnlyCalls)-1], "SearchOlderThan must use EXAMINE (read-only)")
	require.Empty(t, s.uidStoreCalls)
	require.Empty(t, s.uidExpungeCalls)
}

//revive:disable-next-line:var-naming // matches fakeSession.UidStore naming convention
type fakeUidStoreCall struct {
	seqset *goimap.SeqSet
	item   goimap.StoreItem
	flags  any
}

type fakeSession struct {
	selectMailboxStatus *goimap.MailboxStatus
	selectErr           error
	uidSearchResult     []uint32
	uidSearchErr        error
	uidSearchCriteria   *goimap.SearchCriteria
	uidFetchFn          func(seqset *goimap.SeqSet, items []goimap.FetchItem, ch chan *goimap.Message) error
	uidStoreCalls       []fakeUidStoreCall
	supportResult       map[string]bool
	supportErr          error
	uidExpungeCalls     []*goimap.SeqSet
	uidExpungeErr       error
	selectReadOnlyCalls []bool
	closeCalled         bool
}

func (f *fakeSession) Login(_, _ string) error { return nil }
func (f *fakeSession) Logout() error           { return nil }
func (f *fakeSession) Close() error            { f.closeCalled = true; return nil }

func (f *fakeSession) Select(_ string, readOnly bool) (*goimap.MailboxStatus, error) {
	f.selectReadOnlyCalls = append(f.selectReadOnlyCalls, readOnly)
	if f.selectErr != nil {
		return nil, f.selectErr
	}
	if f.selectMailboxStatus != nil {
		return f.selectMailboxStatus, nil
	}
	return &goimap.MailboxStatus{Name: "INBOX"}, nil
}

//revive:disable:var-naming
func (f *fakeSession) UidSearch(criteria *goimap.SearchCriteria) ([]uint32, error) {
	f.uidSearchCriteria = criteria
	if f.uidSearchErr != nil {
		return nil, f.uidSearchErr
	}
	return f.uidSearchResult, nil
}

func (f *fakeSession) UidFetch(seqset *goimap.SeqSet, items []goimap.FetchItem, ch chan *goimap.Message) error {
	if f.uidFetchFn != nil {
		return f.uidFetchFn(seqset, items, ch)
	}
	close(ch)
	return nil
}

func (f *fakeSession) UidStore(seqset *goimap.SeqSet, item goimap.StoreItem, flags any, _ chan *goimap.Message) error {
	f.uidStoreCalls = append(f.uidStoreCalls, fakeUidStoreCall{seqset: seqset, item: item, flags: flags})
	return nil
}

func (f *fakeSession) Support(name string) (bool, error) {
	if f.supportErr != nil {
		return false, f.supportErr
	}
	return f.supportResult[name], nil
}

func (f *fakeSession) UidExpunge(seqset *goimap.SeqSet) error {
	f.uidExpungeCalls = append(f.uidExpungeCalls, seqset)
	return f.uidExpungeErr
}

//revive:enable:var-naming

type byteLiteral struct {
	reader *bytes.Reader
}

func newByteLiteral(v string) *byteLiteral {
	return &byteLiteral{reader: bytes.NewReader([]byte(v))}
}

func (b *byteLiteral) Len() int {
	return b.reader.Len()
}

func (b *byteLiteral) Read(p []byte) (int, error) {
	return b.reader.Read(p)
}

func createTestPEMCertificate(t *testing.T) []byte {
	t.Helper()

	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			CommonName: "test-ca",
		},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		BasicConstraintsValid: true,
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign,
	}

	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &privateKey.PublicKey, privateKey)
	require.NoError(t, err)

	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}
