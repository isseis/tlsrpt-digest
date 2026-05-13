//go:build integration

package imap

import (
	"os"
	"strconv"
	"testing"

	"github.com/isseis/tlsrpt-digest/internal/config"
	"github.com/stretchr/testify/require"
)

func TestIntegration_EnvConfig(t *testing.T) {
	cfg := loadIntegrationConfig(t)
	require.NotEmpty(t, cfg.Host)
	require.NotEmpty(t, cfg.Username)
	require.NotEmpty(t, cfg.Password.Value())
	require.NotEmpty(t, cfg.Mailbox)
}

func loadIntegrationConfig(t *testing.T) Config {
	t.Helper()

	host := os.Getenv("IMAP_TEST_HOST")
	if host == "" {
		t.Skip("integration env is not configured")
	}

	port := 993
	if rawPort := os.Getenv("IMAP_TEST_PORT"); rawPort != "" {
		parsed, err := strconv.Atoi(rawPort)
		require.NoError(t, err)
		port = parsed
	}

	return Config{
		Host:     host,
		Port:     port,
		Username: os.Getenv("IMAP_TEST_USER"),
		Password: config.Secret(os.Getenv("IMAP_TEST_PASS")),
		Mailbox:  envOrDefault("IMAP_TEST_MAILBOX", "INBOX"),
	}
}

func envOrDefault(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
