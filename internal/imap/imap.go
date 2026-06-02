// Package imap provides IMAP client abstractions and implementations.
package imap

import (
	"context"
	"time"

	"github.com/isseis/tlsrpt-digest/internal/config"
)

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
}
