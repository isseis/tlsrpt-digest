//go:build test

// Package imaptestutil provides test doubles and integration-test helpers for the imap package.
package imaptestutil

import (
	"context"
	"time"

	"github.com/isseis/tlsrpt-digest/internal/imap"
)

// FakeMailFetcher is a test double for imap.MailFetcher.
type FakeMailFetcher struct {
	FetchMetaResult imap.FetchMetaResult
	FetchMetaErr    error
	FetchMetaCalls  []time.Time

	DownloadResult map[uint32][]byte
	DownloadErr    error
	DownloadCalls  [][]uint32

	MarkSeenErr   error
	MarkSeenCalls [][]uint32

	DeleteOlderThanResult int
	DeleteOlderThanErr    error
	DeleteOlderThanCalls  []time.Time

	SearchOlderThanResult []uint32
	SearchOlderThanErr    error
	SearchOlderThanCalls  []time.Time

	CloseErr error
}

var _ imap.MailFetcher = (*FakeMailFetcher)(nil)

// FetchMeta implements imap.MailFetcher.
func (f *FakeMailFetcher) FetchMeta(_ context.Context, since time.Time) (imap.FetchMetaResult, error) {
	f.FetchMetaCalls = append(f.FetchMetaCalls, since)
	if f.FetchMetaErr != nil {
		return imap.FetchMetaResult{}, f.FetchMetaErr
	}
	return f.FetchMetaResult, nil
}

// Download implements imap.MailFetcher.
func (f *FakeMailFetcher) Download(_ context.Context, uids []uint32) (map[uint32][]byte, error) {
	f.DownloadCalls = append(f.DownloadCalls, cloneUIDs(uids))
	if f.DownloadErr != nil {
		return nil, f.DownloadErr
	}
	return f.DownloadResult, nil
}

// MarkSeen implements imap.MailFetcher.
func (f *FakeMailFetcher) MarkSeen(_ context.Context, uids []uint32) error {
	f.MarkSeenCalls = append(f.MarkSeenCalls, cloneUIDs(uids))
	return f.MarkSeenErr
}

// DeleteOlderThan implements imap.MailFetcher.
func (f *FakeMailFetcher) DeleteOlderThan(_ context.Context, cutoff time.Time) (int, error) {
	f.DeleteOlderThanCalls = append(f.DeleteOlderThanCalls, cutoff)
	if f.DeleteOlderThanErr != nil {
		return 0, f.DeleteOlderThanErr
	}
	return f.DeleteOlderThanResult, nil
}

// SearchOlderThan implements imap.MailFetcher.
func (f *FakeMailFetcher) SearchOlderThan(_ context.Context, cutoff time.Time) ([]uint32, error) {
	f.SearchOlderThanCalls = append(f.SearchOlderThanCalls, cutoff)
	if f.SearchOlderThanErr != nil {
		return nil, f.SearchOlderThanErr
	}
	return f.SearchOlderThanResult, nil
}

// Close implements imap.MailFetcher.
func (f *FakeMailFetcher) Close() error {
	return f.CloseErr
}

func cloneUIDs(uids []uint32) []uint32 {
	if uids == nil {
		return nil
	}
	out := make([]uint32, len(uids))
	copy(out, uids)
	return out
}
