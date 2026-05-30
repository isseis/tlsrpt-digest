//go:build test

package main

import (
	"bytes"
	"log/slog"
	"testing"
)

// captureSlog redirects the default slog logger to an in-memory buffer and
// returns the buffer. The original default logger is restored via t.Cleanup.
// The handler captures log records at LevelInfo and above (INFO, WARN, ERROR).
func captureSlog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})))
	t.Cleanup(func() { slog.SetDefault(old) })
	return &buf
}
