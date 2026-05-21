package config_test

import (
	"bytes"
	"log/slog"
)

func newCapturingLogger() (*slog.Logger, *bytes.Buffer) {
	var buf bytes.Buffer
	return slog.New(slog.NewTextHandler(&buf, nil)), &buf
}

var _ = newCapturingLogger
