//go:build test

// Package imaptestutil provides test doubles for the imap package.
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
// t.Fatal is called on any error; deferred Logout and connection teardown run
// even when t.Fatal exits via runtime.Goexit.
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
// as a test failure. t.Fatal is called on any error; deferred Logout and
// connection teardown run even when t.Fatal exits via runtime.Goexit.
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
