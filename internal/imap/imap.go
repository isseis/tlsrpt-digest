// Package imap provides IMAP client abstractions and implementations.
package imap

import (
	"context"
	"errors"
	"time"

	"github.com/isseis/tlsrpt-digest/internal/config"
)

// ErrMailboxReadOnly is returned by MarkSeen when the IMAP server grants only
// read-only access to the mailbox (SELECT responded with [READ-ONLY]). Messages
// cannot be flagged \Seen in this case.
var ErrMailboxReadOnly = errors.New("imap: mailbox is read-only")

// Config is the IMAP connection configuration.
type Config struct {
	Host            string
	Port            int
	Username        string
	Password        config.Secret
	Mailbox         string
	TLSCACert       string
	MaxMessageBytes int64
	// InsecureSkipVerify, when true, disables TLS certificate verification.
	// Intended only for integration tests against self-signed servers
	// (e.g. greenmail). Never set from production configuration paths.
	InsecureSkipVerify bool
}

// MessageMeta contains IMAP metadata without message body.
type MessageMeta struct {
	UID       uint32
	Size      uint32
	Date      time.Time // IMAP INTERNALDATE — server-assigned receive timestamp
	Seen      bool
	MessageID string
}

// FetchMetaResult is the FetchMeta return value.
type FetchMetaResult struct {
	Messages    []MessageMeta
	UIDValidity uint32
}

// MailFetcher abstracts IMAP operations.
type MailFetcher interface {
	// FetchMeta returns metadata for messages received on or after since (truncated to date).
	// IMAP SEARCH SINCE has date-level precision; the caller must filter results against a
	// local store to avoid reprocessing same-day messages already handled in a prior run.
	// The caller must also track UIDValidity and invalidate its local store when it changes.
	FetchMeta(ctx context.Context, since time.Time) (FetchMetaResult, error)
	Download(ctx context.Context, uids []uint32) (map[uint32][]byte, error)
	MarkSeen(ctx context.Context, uids []uint32) error
	Close() error

	// DeleteOlderThan deletes messages whose INTERNALDATE (truncated to date) is
	// before cutoff. If cutoff is zero, it does nothing and returns (0, nil).
	// If the server does not support UIDPLUS, it logs a warning and returns
	// (0, nil) without setting any flags.
	DeleteOlderThan(ctx context.Context, cutoff time.Time) (deleted int, err error)

	// SearchOlderThan returns the UIDs of messages whose INTERNALDATE (truncated
	// to date) is before cutoff, using a read-only (EXAMINE) selection. It does
	// not modify mailbox state. Used to preview deletion candidates in dry-run.
	SearchOlderThan(ctx context.Context, cutoff time.Time) ([]uint32, error)
}
