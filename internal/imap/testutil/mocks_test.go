//go:build test

package imaptestutil

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/isseis/tlsrpt-digest/internal/imap"
	"github.com/stretchr/testify/require"
)

func TestFakeMailFetcherFetchMeta(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC().Round(0)
	fetchMetaResult := imap.FetchMetaResult{Messages: []imap.MessageMeta{{UID: 10, Seen: false}}, UIDValidity: 123}
	f := &FakeMailFetcher{FetchMetaResult: fetchMetaResult}

	got, err := f.FetchMeta(context.Background(), now)
	require.NoError(t, err)
	require.Equal(t, fetchMetaResult, got)
	require.Equal(t, []time.Time{now}, f.FetchMetaCalls)
}

func TestFakeMailFetcherDownload(t *testing.T) {
	t.Parallel()

	raw := []byte("From: a@example.com\r\nTo: b@example.com\r\nSubject: test\r\n\r\nbody")
	f := &FakeMailFetcher{DownloadResult: map[uint32][]byte{3: raw}}
	uids := []uint32{3, 9}

	got, derr := f.Download(context.Background(), uids)
	require.NoError(t, derr)
	require.Equal(t, f.DownloadResult, got)
	require.Len(t, f.DownloadCalls, 1)
	require.Equal(t, []uint32{3, 9}, f.DownloadCalls[0])

	uids[0] = 99
	require.Equal(t, []uint32{3, 9}, f.DownloadCalls[0])
}

func TestFakeMailFetcherMarkSeen(t *testing.T) {
	t.Parallel()

	f := &FakeMailFetcher{}
	uids := []uint32{5, 8}

	err := f.MarkSeen(context.Background(), uids)
	require.NoError(t, err)
	require.Equal(t, [][]uint32{{5, 8}}, f.MarkSeenCalls)

	uids[0] = 1
	require.Equal(t, [][]uint32{{5, 8}}, f.MarkSeenCalls)
}

func TestFakeMailFetcherDeleteOlderThan(t *testing.T) {
	t.Parallel()

	t.Run("records calls and returns result", func(t *testing.T) {
		t.Parallel()
		cutoff := time.Now()
		f := &FakeMailFetcher{DeleteOlderThanResult: 3}

		got, err := f.DeleteOlderThan(context.Background(), cutoff)
		require.NoError(t, err)
		require.Equal(t, 3, got)
		require.Equal(t, []time.Time{cutoff}, f.DeleteOlderThanCalls)
	})

	t.Run("injected error", func(t *testing.T) {
		t.Parallel()
		deleteErr := errors.New("delete error")
		f := &FakeMailFetcher{DeleteOlderThanErr: deleteErr}

		got, err := f.DeleteOlderThan(context.Background(), time.Now())
		require.ErrorIs(t, err, deleteErr)
		require.Zero(t, got)
	})
}

func TestFakeMailFetcherSearchOlderThan(t *testing.T) {
	t.Parallel()

	t.Run("records calls and returns result", func(t *testing.T) {
		t.Parallel()
		cutoff := time.Now()
		f := &FakeMailFetcher{SearchOlderThanResult: []uint32{5, 6}}

		got, err := f.SearchOlderThan(context.Background(), cutoff)
		require.NoError(t, err)
		require.Equal(t, []uint32{5, 6}, got)
		require.Equal(t, []time.Time{cutoff}, f.SearchOlderThanCalls)
	})

	t.Run("injected error", func(t *testing.T) {
		t.Parallel()
		searchErr := errors.New("search error")
		f := &FakeMailFetcher{SearchOlderThanErr: searchErr}

		got, err := f.SearchOlderThan(context.Background(), time.Now())
		require.ErrorIs(t, err, searchErr)
		require.Nil(t, got)
	})
}

func TestFakeMailFetcherClose(t *testing.T) {
	t.Parallel()

	t.Run("no error", func(t *testing.T) {
		t.Parallel()
		f := &FakeMailFetcher{}
		require.NoError(t, f.Close())
	})

	t.Run("injected error", func(t *testing.T) {
		t.Parallel()
		closeErr := errors.New("close error")
		f := &FakeMailFetcher{CloseErr: closeErr}
		require.ErrorIs(t, f.Close(), closeErr)
	})
}
