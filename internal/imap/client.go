// Package imap provides IMAP client abstractions and implementations.
package imap

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"slices"
	"strconv"
	"time"

	goimap "github.com/emersion/go-imap"
	imapclient "github.com/emersion/go-imap/client"
)

var (
	errInvalidTLSCAPEM = errors.New("imap: invalid tls ca pem")
	errUIDNotFound     = errors.New("uid not found")
	errBodyTooLarge    = errors.New("body exceeds max_message_bytes")
)

type imapSession interface {
	Login(username, password string) error
	Logout() error
	Close() error
	Select(mailbox string, readOnly bool) (*goimap.MailboxStatus, error)
	UidSearch(criteria *goimap.SearchCriteria) ([]uint32, error)
	UidFetch(seqset *goimap.SeqSet, items []goimap.FetchItem, ch chan *goimap.Message) error
	UidStore(seqset *goimap.SeqSet, item goimap.StoreItem, flags any, ch chan *goimap.Message) error
}

type dialTLSFunc func(addr string, tlsConfig *tls.Config) (imapSession, error)

var dialTLS dialTLSFunc = func(addr string, tlsConfig *tls.Config) (imapSession, error) {
	return imapclient.DialTLS(addr, tlsConfig)
}

type imapClient struct {
	cfg     Config
	session imapSession
	// lastSelectReadOnly is true when the last Select used EXAMINE (read-only).
	// Close() consults it to avoid expunging other clients' \Deleted messages
	// (see Close for the full rationale).
	lastSelectReadOnly bool
}

var _ MailFetcher = (*imapClient)(nil)

// NewIMAPClient establishes an authenticated TLS IMAP session.
func NewIMAPClient(cfg Config) (MailFetcher, error) {
	tlsConfig, err := buildTLSConfig(cfg)
	if err != nil {
		return nil, err
	}

	mailbox := cfg.Mailbox
	if mailbox == "" {
		mailbox = "INBOX"
	}
	cfg.Mailbox = mailbox

	address := net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port))
	session, err := dialTLS(address, tlsConfig)
	if err != nil {
		return nil, fmt.Errorf("imap: connect to %s: %w", address, err)
	}

	if err := session.Login(cfg.Username, cfg.Password.Value()); err != nil {
		_ = session.Logout()
		return nil, fmt.Errorf("imap: login: %w", err)
	}

	return &imapClient{cfg: cfg, session: session}, nil
}

func buildTLSConfig(cfg Config) (*tls.Config, error) {
	tlsConfig := &tls.Config{
		MinVersion:         tls.VersionTLS12,
		InsecureSkipVerify: cfg.InsecureSkipVerify, //nolint:gosec // InsecureSkipVerify is not set from production config paths; the TestBuildIMAPConfig unit test and integration-only guard enforce this.
		RootCAs:            nil,
	}

	if cfg.TLSCACert == "" {
		return tlsConfig, nil
	}

	pemBytes, err := os.ReadFile(cfg.TLSCACert)
	if err != nil {
		return nil, fmt.Errorf("imap: read tls ca cert %q: %w", cfg.TLSCACert, err)
	}

	pool := x509.NewCertPool()
	if ok := pool.AppendCertsFromPEM(pemBytes); !ok {
		return nil, fmt.Errorf("%w: %q", errInvalidTLSCAPEM, cfg.TLSCACert)
	}

	tlsConfig.RootCAs = pool
	return tlsConfig, nil
}

func (c *imapClient) Close() error {
	// Guard against collateral expunge: RFC 3501 §6.4.2 says CLOSE permanently
	// removes every \Deleted-flagged message from the selected mailbox, but does
	// nothing destructive when the mailbox was opened read-only (EXAMINE). After a
	// read-write SELECT (MarkSeen), an unconditional CLOSE would silently expunge
	// messages that another client flagged \Deleted — data we do not own — so we
	// send only LOGOUT in that case (LOGOUT does not expunge).
	//
	// After a read-only EXAMINE, CLOSE cannot expunge anything, so we send it: this
	// is required because some servers (including greenmail) keep the mailbox in a
	// "session-open" state after LOGOUT-only, blocking a concurrent DELETE from
	// another connection.
	if c.lastSelectReadOnly {
		if err := c.session.Close(); err != nil {
			slog.Warn("imap: CLOSE before logout failed (mailbox may remain session-open)", "error", err)
		}
	}
	if err := c.session.Logout(); err != nil {
		return fmt.Errorf("imap: logout: %w", err)
	}
	return nil
}

func (c *imapClient) FetchMeta(ctx context.Context, since time.Time) (FetchMetaResult, error) {
	if err := ctx.Err(); err != nil {
		return FetchMetaResult{}, fmt.Errorf("imap: fetch meta: %w", err)
	}

	// Use EXAMINE (read-only) since FetchMeta does not modify any messages.
	mailboxStatus, err := c.session.Select(c.cfg.Mailbox, true)
	if err != nil {
		return FetchMetaResult{}, fmt.Errorf("imap: select mailbox %s: %w", c.cfg.Mailbox, err)
	}
	c.lastSelectReadOnly = true

	criteria := goimap.NewSearchCriteria()
	criteria.Since = truncateToDate(since)
	uids, err := c.session.UidSearch(criteria)
	if err != nil {
		return FetchMetaResult{}, fmt.Errorf("imap: fetch meta: %w", err)
	}

	if len(uids) == 0 {
		return FetchMetaResult{Messages: []MessageMeta{}, UIDValidity: mailboxStatus.UidValidity}, nil
	}

	seqSet := uidsToSeqSet(uids)
	fetchItems := []goimap.FetchItem{goimap.FetchUid, goimap.FetchRFC822Size, goimap.FetchFlags, goimap.FetchEnvelope, goimap.FetchInternalDate}
	ch := make(chan *goimap.Message, len(uids))

	fetchErrCh := make(chan error, 1)
	go func() {
		fetchErrCh <- c.session.UidFetch(seqSet, fetchItems, ch)
	}()

	metas := make([]MessageMeta, 0, len(uids))
	for msg := range ch {
		if msg == nil {
			continue
		}
		if msg.InternalDate.IsZero() {
			slog.Warn("imap: skip message with missing internaldate", "uid", msg.Uid)
			continue
		}
		if msg.Envelope == nil {
			slog.Warn("imap: skip message with missing envelope", "uid", msg.Uid)
			continue
		}

		meta := MessageMeta{
			UID:       msg.Uid,
			Size:      msg.Size,
			Date:      msg.InternalDate,
			Seen:      slices.Contains(msg.Flags, goimap.SeenFlag),
			MessageID: msg.Envelope.MessageId,
		}

		if isTooLarge(meta.Size, c.cfg.MaxMessageBytes) {
			slog.Warn("imap: skip message larger than max_message_bytes", "uid", meta.UID, "date", meta.Date, "size", meta.Size, "max_message_bytes", c.cfg.MaxMessageBytes)
			continue
		}

		metas = append(metas, meta)
	}

	if err := <-fetchErrCh; err != nil {
		return FetchMetaResult{}, fmt.Errorf("imap: fetch meta: %w", err)
	}

	return FetchMetaResult{Messages: metas, UIDValidity: mailboxStatus.UidValidity}, nil
}

func (c *imapClient) Download(ctx context.Context, uids []uint32) (map[uint32][]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("imap: download: %w", err)
	}
	if len(uids) == 0 {
		return map[uint32][]byte{}, nil
	}

	// Use EXAMINE (read-only): Download fetches bodies with BODY.PEEK and never
	// modifies any message, so a read-write SELECT is unnecessary.
	if _, err := c.session.Select(c.cfg.Mailbox, true); err != nil {
		return nil, fmt.Errorf("imap: select mailbox %s: %w", c.cfg.Mailbox, err)
	}
	c.lastSelectReadOnly = true

	seqSet := uidsToSeqSet(uids)
	section := &goimap.BodySectionName{Peek: true}
	items := []goimap.FetchItem{goimap.FetchUid, section.FetchItem()}

	ch := make(chan *goimap.Message, len(uids))
	fetchErrCh := make(chan error, 1)
	go func() {
		fetchErrCh <- c.session.UidFetch(seqSet, items, ch)
	}()

	out := make(map[uint32][]byte, len(uids))
	for msg := range ch {
		if msg == nil {
			continue
		}

		body := msg.GetBody(section)
		if body == nil {
			for _, literal := range msg.Body {
				body = literal
				break
			}
		}
		if body == nil {
			continue
		}

		var r io.Reader = body
		if c.cfg.MaxMessageBytes > 0 {
			r = io.LimitReader(body, c.cfg.MaxMessageBytes+1)
		}
		raw, err := io.ReadAll(r)
		if err != nil {
			return nil, fmt.Errorf("imap: download: read uid %d: %w", msg.Uid, err)
		}
		if c.cfg.MaxMessageBytes > 0 && int64(len(raw)) > c.cfg.MaxMessageBytes {
			return nil, fmt.Errorf("imap: download: uid %d: %w", msg.Uid, errBodyTooLarge)
		}
		out[msg.Uid] = raw
	}

	if err := <-fetchErrCh; err != nil {
		return nil, fmt.Errorf("imap: download: %w", err)
	}

	if missingUID, ok := firstMissingUID(uids, out); ok {
		return nil, fmt.Errorf("imap: download: %w: %d", errUIDNotFound, missingUID)
	}

	return out, nil
}

func (c *imapClient) MarkSeen(ctx context.Context, uids []uint32) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("imap: mark seen: %w", err)
	}
	if len(uids) == 0 {
		return nil
	}

	if _, err := c.session.Select(c.cfg.Mailbox, false); err != nil {
		return fmt.Errorf("imap: select mailbox %s: %w", c.cfg.Mailbox, err)
	}
	c.lastSelectReadOnly = false

	seqSet := uidsToSeqSet(uids)
	storeItem := goimap.FormatFlagsOp(goimap.AddFlags, false)
	if err := c.session.UidStore(seqSet, storeItem, []any{goimap.SeenFlag}, nil); err != nil {
		return fmt.Errorf("imap: mark seen: %w", err)
	}
	return nil
}

func truncateToDate(v time.Time) time.Time {
	return time.Date(v.Year(), v.Month(), v.Day(), 0, 0, 0, 0, v.Location())
}

func uidsToSeqSet(uids []uint32) *goimap.SeqSet {
	seqSet := new(goimap.SeqSet)
	if len(uids) > 0 {
		seqSet.AddNum(uids...)
	}
	return seqSet
}

func isTooLarge(size uint32, maxBytes int64) bool {
	if maxBytes <= 0 {
		return false
	}
	return int64(size) > maxBytes
}

func firstMissingUID(requested []uint32, got map[uint32][]byte) (uint32, bool) {
	for _, uid := range requested {
		if _, ok := got[uid]; !ok {
			return uid, true
		}
	}
	return 0, false
}
