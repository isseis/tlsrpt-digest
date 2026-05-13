// Package imap provides IMAP client abstractions and implementations.
package imap

import (
	"context"
	"net/mail"
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
}

// MessageMeta contains IMAP metadata without message body.
type MessageMeta struct {
	UID       uint32
	Size      uint32
	Date      time.Time
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
	FetchMeta(ctx context.Context, since time.Time) (FetchMetaResult, error)
	Download(ctx context.Context, uids []uint32) (map[uint32]*mail.Message, error)
	MarkSeen(ctx context.Context, uids []uint32) error
	Close() error
}
