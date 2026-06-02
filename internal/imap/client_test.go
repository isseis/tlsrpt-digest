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

// TestBuildTLSConfigInsecureSkipVerify verifies that InsecureSkipVerify is reflected in tls.Config (requirement F-001, AC-01).
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

type fakeSession struct {
	selectMailboxStatus *goimap.MailboxStatus
	selectErr           error
	uidFetchFn          func(seqset *goimap.SeqSet, items []goimap.FetchItem, ch chan *goimap.Message) error
}

func (f *fakeSession) Login(_, _ string) error { return nil }
func (f *fakeSession) Logout() error           { return nil }

func (f *fakeSession) Select(_ string, _ bool) (*goimap.MailboxStatus, error) {
	if f.selectErr != nil {
		return nil, f.selectErr
	}
	if f.selectMailboxStatus != nil {
		return f.selectMailboxStatus, nil
	}
	return &goimap.MailboxStatus{Name: "INBOX"}, nil
}

//revive:disable:var-naming
func (f *fakeSession) UidSearch(_ *goimap.SearchCriteria) ([]uint32, error) {
	return nil, errors.New("not implemented")
}

func (f *fakeSession) UidFetch(seqset *goimap.SeqSet, items []goimap.FetchItem, ch chan *goimap.Message) error {
	if f.uidFetchFn != nil {
		return f.uidFetchFn(seqset, items, ch)
	}
	close(ch)
	return nil
}

func (f *fakeSession) UidStore(_ *goimap.SeqSet, _ goimap.StoreItem, _ any, _ chan *goimap.Message) error {
	return nil
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
