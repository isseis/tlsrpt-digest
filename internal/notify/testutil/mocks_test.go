//go:build test

package notifytestutil_test

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/isseis/tlsrpt-digest/internal/notify/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSpyHandler_RecordsHandle(t *testing.T) {
	var spy notifytestutil.SpyHandler
	r := slog.NewRecord(time.Now(), slog.LevelWarn, "test", 0)
	require.NoError(t, spy.Handle(context.Background(), r))
	assert.Len(t, spy.Records, 1)
	assert.Equal(t, slog.LevelWarn, spy.Records[0].Level)
}

func TestSpyHandler_FlushCalled(t *testing.T) {
	var spy notifytestutil.SpyHandler
	require.NoError(t, spy.Flush(context.Background()))
	assert.True(t, spy.FlushCalled)
}

func TestSpyHandler_RecordClone(t *testing.T) {
	// Mutating the original record after Handle must not change the stored copy.
	var spy notifytestutil.SpyHandler
	r := slog.NewRecord(time.Now(), slog.LevelWarn, "original", 0)
	require.NoError(t, spy.Handle(context.Background(), r))
	// The stored message should still be "original" regardless of what the
	// caller does with r after the call.
	assert.Equal(t, "original", spy.Records[0].Message)
}
