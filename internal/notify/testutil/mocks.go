//go:build test

// Package notifytestutil provides test doubles for the notify package.
package notifytestutil

import (
	"context"
	"log/slog"
)

// SpyHandler implements slog.Handler and records received slog.Records.
// It also satisfies an interface compatible with notify.Flusher.
type SpyHandler struct {
	Records     []slog.Record
	FlushCalled bool
	FlushErr    error
}

// Enabled always returns true so all levels are captured.
func (s *SpyHandler) Enabled(_ context.Context, _ slog.Level) bool { return true }

// Handle clones the record and appends it to Records.
func (s *SpyHandler) Handle(_ context.Context, r slog.Record) error {
	s.Records = append(s.Records, r.Clone())
	return nil
}

// WithAttrs returns s unchanged.
func (s *SpyHandler) WithAttrs(_ []slog.Attr) slog.Handler { return s }

// WithGroup returns s unchanged.
func (s *SpyHandler) WithGroup(_ string) slog.Handler { return s }

// Flush records the call and returns FlushErr.
func (s *SpyHandler) Flush(_ context.Context) error {
	s.FlushCalled = true
	return s.FlushErr
}

// Compile-time check that SpyHandler satisfies slog.Handler.
var _ slog.Handler = (*SpyHandler)(nil)
