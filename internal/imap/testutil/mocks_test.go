//go:build test

package imaptestutil

import (
	"context"
	"errors"
	"net/mail"
	"strings"
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

	msg, err := mail.ReadMessage(strings.NewReader("From: a@example.com\r\nTo: b@example.com\r\nSubject: test\r\n\r\nbody"))
	require.NoError(t, err)

	f := &FakeMailFetcher{DownloadResult: map[uint32]*mail.Message{3: msg}}
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

func TestFakeMailFetcherClose(t *testing.T) {
	t.Parallel()

	f := &FakeMailFetcher{CloseErr: errors.New("ignored")}
	require.NoError(t, f.Close())
}
