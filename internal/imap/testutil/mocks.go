//go:build test

package imaptestutil

import (
	"context"
	"net/mail"
	"time"

	"github.com/isseis/tlsrpt-digest/internal/imap"
)

// FakeMailFetcher is a test double for imap.MailFetcher.
type FakeMailFetcher struct {
	FetchMetaResult imap.FetchMetaResult
	FetchMetaErr    error
	FetchMetaCalls  []time.Time

	DownloadResult map[uint32]*mail.Message
	DownloadErr    error
	DownloadCalls  [][]uint32

	MarkSeenErr   error
	MarkSeenCalls [][]uint32

	CloseErr error
}

var _ imap.MailFetcher = (*FakeMailFetcher)(nil)

func (f *FakeMailFetcher) FetchMeta(_ context.Context, since time.Time) (imap.FetchMetaResult, error) {
	f.FetchMetaCalls = append(f.FetchMetaCalls, since)
	if f.FetchMetaErr != nil {
		return imap.FetchMetaResult{}, f.FetchMetaErr
	}
	return f.FetchMetaResult, nil
}

func (f *FakeMailFetcher) Download(_ context.Context, uids []uint32) (map[uint32]*mail.Message, error) {
	f.DownloadCalls = append(f.DownloadCalls, cloneUIDs(uids))
	if f.DownloadErr != nil {
		return nil, f.DownloadErr
	}
	return f.DownloadResult, nil
}

func (f *FakeMailFetcher) MarkSeen(_ context.Context, uids []uint32) error {
	f.MarkSeenCalls = append(f.MarkSeenCalls, cloneUIDs(uids))
	return f.MarkSeenErr
}

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
