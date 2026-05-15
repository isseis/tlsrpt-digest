package config_test

import (
	"log/slog"
	"testing"

	"github.com/isseis/tlsrpt-digest/internal/config"
	"github.com/stretchr/testify/assert"
)

func TestSecret_RedactsStringAndLogValue(t *testing.T) {
	s := config.Secret("super-secret")

	assert.Equal(t, "[REDACTED]", s.String())
	assert.Equal(t, slog.StringValue("[REDACTED]"), s.LogValue())
	assert.Equal(t, "super-secret", s.Value())
}
