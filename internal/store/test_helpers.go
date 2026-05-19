//go:build test

package store

import (
	"context"
	"log/slog"
	"testing"
)

// spyHandler is a slog.Handler that records all log records for assertion in tests.
type spyHandler struct {
	records []slog.Record
}

func (s *spyHandler) Enabled(_ context.Context, _ slog.Level) bool { return true }
func (s *spyHandler) Handle(_ context.Context, r slog.Record) error {
	s.records = append(s.records, r.Clone())
	return nil
}
func (s *spyHandler) WithAttrs(_ []slog.Attr) slog.Handler { return s }
func (s *spyHandler) WithGroup(_ string) slog.Handler      { return s }

// setDefaultSlogSpy installs a spy as the default slog handler and returns it.
// The original default handler is restored when the test finishes.
func setDefaultSlogSpy(t *testing.T) *spyHandler {
	t.Helper()
	orig := slog.Default()
	spy := &spyHandler{}
	slog.SetDefault(slog.New(spy))
	t.Cleanup(func() { slog.SetDefault(orig) })
	return spy
}
