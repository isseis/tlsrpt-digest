//go:build test

package imaptestutil

import (
	"crypto/tls"
	"net"
	"strconv"
	"testing"

	imapclient "github.com/emersion/go-imap/client"
	"github.com/isseis/tlsrpt-digest/internal/imap"
)

// CreateMailbox creates the named mailbox on the server configured by cfg.
// t.Fatal is called on any error; the deferred Logout runs even when t.Fatal
// exits via runtime.Goexit, closing the IMAP session and underlying connection.
func CreateMailbox(t *testing.T, cfg imap.Config, mailbox string) {
	t.Helper()
	c := dialAndLogin(t, cfg)
	defer func() {
		if err := c.Logout(); err != nil {
			t.Logf("CreateMailbox: logout: %v", err)
		}
	}()
	if err := c.Create(mailbox); err != nil {
		t.Fatalf("CreateMailbox %q: %v", mailbox, err)
	}
}

// DeleteMailbox deletes the named mailbox on the server configured by cfg.
// If the mailbox does not exist, greenmail returns an error which is treated
// as a test failure. t.Fatal is called on any error; the deferred Logout runs
// even when t.Fatal exits via runtime.Goexit, closing the IMAP session and
// underlying connection.
func DeleteMailbox(t *testing.T, cfg imap.Config, mailbox string) {
	t.Helper()
	c := dialAndLogin(t, cfg)
	defer func() {
		if err := c.Logout(); err != nil {
			t.Logf("DeleteMailbox: logout: %v", err)
		}
	}()
	if err := c.Delete(mailbox); err != nil {
		t.Fatalf("DeleteMailbox %q: %v", mailbox, err)
	}
}

// FetchUIDValidity opens the mailbox with EXAMINE, reads its UIDValidity, then
// sends IMAP CLOSE before LOGOUT. CLOSE deselects the mailbox so that another
// session can immediately DELETE it; it does not expunge messages because the
// mailbox was opened read-only (RFC 3501 §6.3.2 forbids permanent changes in a
// read-only session). t.Fatal is called on any error.
func FetchUIDValidity(t *testing.T, cfg imap.Config, mailbox string) uint32 {
	t.Helper()
	c := dialAndLogin(t, cfg)
	defer func() {
		if err := c.Logout(); err != nil {
			t.Logf("FetchUIDValidity: logout: %v", err)
		}
	}()
	status, err := c.Select(mailbox, true) // EXAMINE (read-only)
	if err != nil {
		t.Fatalf("FetchUIDValidity: EXAMINE %q: %v", mailbox, err)
	}
	if err := c.Close(); err != nil { // IMAP CLOSE: deselect without expunge
		t.Fatalf("FetchUIDValidity: CLOSE %q: %v", mailbox, err)
	}
	return status.UidValidity
}

// dialAndLogin establishes an IMAPS connection and logs in. A deferred Logout
// is NOT registered here — callers are responsible for registering it
// immediately after dialAndLogin returns so it executes even on t.Fatal.
func dialAndLogin(t *testing.T, cfg imap.Config) *imapclient.Client {
	t.Helper()
	addr := net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port))
	tlsCfg := &tls.Config{
		InsecureSkipVerify: cfg.InsecureSkipVerify, //nolint:gosec // InsecureSkipVerify is set only in integration tests via cfg.InsecureSkipVerify; production paths never set it true
		MinVersion:         tls.VersionTLS12,
	}
	c, err := imapclient.DialTLS(addr, tlsCfg)
	if err != nil {
		t.Fatalf("dialAndLogin: dial %s: %v", addr, err)
	}
	if err := c.Login(cfg.Username, cfg.Password.Value()); err != nil {
		// Best-effort logout before fataling; ignore error.
		_ = c.Logout()
		t.Fatalf("dialAndLogin: login as %s: %v", cfg.Username, err)
	}
	return c
}
