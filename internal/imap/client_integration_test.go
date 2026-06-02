//go:build integration

package imap

import (
	"net/smtp"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/oklog/ulid/v2"

	"github.com/isseis/tlsrpt-digest/internal/config"
	"github.com/stretchr/testify/require"
)

// testRunID returns a unique suffix for this test binary invocation.
var testRunID = sync.OnceValue(func() string {
	return ulid.Make().String()
})

// sanitizeIdentifier replaces characters not safe in email local-parts or IMAP
// mailbox names (keeping only alphanumerics and hyphens) with hyphens.
var sanitizeIdentifier = regexp.MustCompile(`[^a-zA-Z0-9-]`)

// testRecipientEmail returns a unique recipient email address derived from the
// test name, ensuring no collision with previous runs on the same greenmail
// instance.
func testRecipientEmail(t *testing.T) string {
	t.Helper()
	sanitized := sanitizeIdentifier.ReplaceAllString(t.Name(), "-")
	return sanitized + "-" + testRunID() + "@test.example.com"
}

// testMessageID returns a unique Message-ID for the test run.
func testMessageID(t *testing.T) string {
	t.Helper()
	sanitized := sanitizeIdentifier.ReplaceAllString(t.Name(), "-")
	return "<" + sanitized + "-" + testRunID() + "@test.example.com>"
}

// normalizeMessageID strips leading/trailing whitespace and ensures the value
// is wrapped in angle brackets exactly once.
func normalizeMessageID(id string) string {
	id = strings.TrimSpace(id)
	id = strings.TrimPrefix(id, "<")
	id = strings.TrimSuffix(id, ">")
	return "<" + id + ">"
}

// injectTestMail sends a test message via SMTP to recipient.
func injectTestMail(t *testing.T, smtpAddr, recipient, subject, body, messageID string) {
	t.Helper()
	msg := "From: from@test.example.com\r\n" +
		"To: " + recipient + "\r\n" +
		"Subject: " + subject + "\r\n" +
		"Message-ID: " + messageID + "\r\n" +
		"\r\n" +
		body
	require.NoError(t, smtp.SendMail(smtpAddr, nil, "from@test.example.com", []string{recipient}, []byte(msg)))
}

// testMailboxName returns a unique IMAP mailbox name (no @ character) derived
// from the test name. The prefix is truncated to 24 characters before the
// run-ID suffix is appended so the suffix is never cut off.
func testMailboxName(t *testing.T) string {
	t.Helper()
	sanitized := sanitizeIdentifier.ReplaceAllString(t.Name(), "-")
	prefix := sanitized
	if len(prefix) > 24 {
		prefix = prefix[:24]
	}
	return prefix + "-" + testRunID()
}

// missingFixedUserEnv returns the names of environment variables required for
// fixed-user integration tests that are missing or invalid.
func missingFixedUserEnv(getenv func(string) string) []string {
	var missing []string
	for _, key := range []string{"IMAP_TEST_HOST", "IMAP_TEST_PORT", "IMAP_TEST_USER", "IMAP_TEST_PASS", "IMAP_TEST_MAILBOX"} {
		val := getenv(key)
		if val == "" {
			missing = append(missing, key+" (empty)")
			continue
		}
		if key == "IMAP_TEST_PORT" {
			if _, err := strconv.Atoi(val); err != nil {
				missing = append(missing, key+" (not a valid integer)")
			}
		}
	}
	return missing
}

// missingSMTPEnv returns the names of environment variables required for
// SMTP-injection integration tests that are missing or invalid.
func missingSMTPEnv(getenv func(string) string) []string {
	var missing []string
	for _, key := range []string{"IMAP_TEST_HOST", "IMAP_TEST_PORT", "IMAP_TEST_SMTP_HOST", "IMAP_TEST_SMTP_PORT"} {
		val := getenv(key)
		if val == "" {
			missing = append(missing, key+" (empty)")
			continue
		}
		if key == "IMAP_TEST_PORT" {
			if _, err := strconv.Atoi(val); err != nil {
				missing = append(missing, key+" (not a valid integer)")
			}
		}
	}
	return missing
}

// requireFixedUserEnv skips the test when any fixed-user environment variable
// is missing or invalid.
func requireFixedUserEnv(t *testing.T) {
	t.Helper()
	if missing := missingFixedUserEnv(os.Getenv); len(missing) > 0 {
		t.Skip("fixed-user integration env not configured: " + strings.Join(missing, ", "))
	}
}

// requireSMTPEnv skips the test when any SMTP environment variable is missing
// or invalid.
func requireSMTPEnv(t *testing.T) {
	t.Helper()
	if missing := missingSMTPEnv(os.Getenv); len(missing) > 0 {
		t.Skip("SMTP integration env not configured: " + strings.Join(missing, ", "))
	}
}

// loadSMTPTestConfig builds an imap.Config for an SMTP-injected test user and
// returns the SMTP address.
func loadSMTPTestConfig(t *testing.T) (cfg Config, smtpAddr string) {
	t.Helper()
	requireSMTPEnv(t)

	port, err := strconv.Atoi(os.Getenv("IMAP_TEST_PORT"))
	require.NoError(t, err)

	recipient := testRecipientEmail(t)
	cfg = Config{
		Host:               os.Getenv("IMAP_TEST_HOST"),
		Port:               port,
		Username:           recipient,
		Password:           config.Secret(recipient),
		Mailbox:            "INBOX",
		InsecureSkipVerify: true,
	}
	smtpAddr = os.Getenv("IMAP_TEST_SMTP_HOST") + ":" + os.Getenv("IMAP_TEST_SMTP_PORT")
	return cfg, smtpAddr
}

func TestIntegration_EnvConfig(t *testing.T) {
	requireFixedUserEnv(t)
	cfg := loadIntegrationConfig(t)
	require.NotEmpty(t, cfg.Host)
	require.NotEmpty(t, cfg.Username)
	require.NotEmpty(t, cfg.Password.Value())
	require.NotEmpty(t, cfg.Mailbox)
	require.True(t, cfg.InsecureSkipVerify)
}

func TestIntegration_EnvRequirements(t *testing.T) {
	t.Run("fixed_user_host_missing", func(t *testing.T) {
		env := map[string]string{
			"IMAP_TEST_PORT":    "3993",
			"IMAP_TEST_USER":    "u",
			"IMAP_TEST_PASS":    "p",
			"IMAP_TEST_MAILBOX": "INBOX",
		}
		got := missingFixedUserEnv(func(k string) string { return env[k] })
		require.Contains(t, strings.Join(got, " "), "IMAP_TEST_HOST")
	})
	t.Run("fixed_user_port_missing", func(t *testing.T) {
		env := map[string]string{
			"IMAP_TEST_HOST":    "h",
			"IMAP_TEST_USER":    "u",
			"IMAP_TEST_PASS":    "p",
			"IMAP_TEST_MAILBOX": "INBOX",
		}
		got := missingFixedUserEnv(func(k string) string { return env[k] })
		require.Contains(t, strings.Join(got, " "), "IMAP_TEST_PORT")
	})
	t.Run("fixed_user_port_invalid", func(t *testing.T) {
		env := map[string]string{
			"IMAP_TEST_HOST":    "h",
			"IMAP_TEST_PORT":    "notanint",
			"IMAP_TEST_USER":    "u",
			"IMAP_TEST_PASS":    "p",
			"IMAP_TEST_MAILBOX": "INBOX",
		}
		got := missingFixedUserEnv(func(k string) string { return env[k] })
		require.Contains(t, strings.Join(got, " "), "IMAP_TEST_PORT")
	})
	t.Run("fixed_user_user_missing", func(t *testing.T) {
		env := map[string]string{
			"IMAP_TEST_HOST":    "h",
			"IMAP_TEST_PORT":    "3993",
			"IMAP_TEST_PASS":    "p",
			"IMAP_TEST_MAILBOX": "INBOX",
		}
		got := missingFixedUserEnv(func(k string) string { return env[k] })
		require.Contains(t, strings.Join(got, " "), "IMAP_TEST_USER")
	})
	t.Run("fixed_user_pass_missing", func(t *testing.T) {
		env := map[string]string{
			"IMAP_TEST_HOST":    "h",
			"IMAP_TEST_PORT":    "3993",
			"IMAP_TEST_USER":    "u",
			"IMAP_TEST_MAILBOX": "INBOX",
		}
		got := missingFixedUserEnv(func(k string) string { return env[k] })
		require.Contains(t, strings.Join(got, " "), "IMAP_TEST_PASS")
	})
	t.Run("fixed_user_mailbox_missing", func(t *testing.T) {
		env := map[string]string{
			"IMAP_TEST_HOST": "h",
			"IMAP_TEST_PORT": "3993",
			"IMAP_TEST_USER": "u",
			"IMAP_TEST_PASS": "p",
		}
		got := missingFixedUserEnv(func(k string) string { return env[k] })
		require.Contains(t, strings.Join(got, " "), "IMAP_TEST_MAILBOX")
	})
	t.Run("smtp_host_missing", func(t *testing.T) {
		env := map[string]string{
			"IMAP_TEST_PORT":      "3993",
			"IMAP_TEST_SMTP_PORT": "3025",
		}
		got := missingSMTPEnv(func(k string) string { return env[k] })
		require.Contains(t, strings.Join(got, " "), "IMAP_TEST_HOST")
	})
	t.Run("smtp_port_missing", func(t *testing.T) {
		env := map[string]string{
			"IMAP_TEST_HOST":      "h",
			"IMAP_TEST_PORT":      "3993",
			"IMAP_TEST_SMTP_HOST": "h",
		}
		got := missingSMTPEnv(func(k string) string { return env[k] })
		require.Contains(t, strings.Join(got, " "), "IMAP_TEST_SMTP_PORT")
	})
	t.Run("smtp_imap_port_invalid", func(t *testing.T) {
		env := map[string]string{
			"IMAP_TEST_HOST":      "h",
			"IMAP_TEST_PORT":      "notanint",
			"IMAP_TEST_SMTP_HOST": "h",
			"IMAP_TEST_SMTP_PORT": "3025",
		}
		got := missingSMTPEnv(func(k string) string { return env[k] })
		require.Contains(t, strings.Join(got, " "), "IMAP_TEST_PORT")
	})
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
		Host:               host,
		Port:               port,
		Username:           os.Getenv("IMAP_TEST_USER"),
		Password:           config.Secret(os.Getenv("IMAP_TEST_PASS")),
		Mailbox:            envOrDefault("IMAP_TEST_MAILBOX", "INBOX"),
		InsecureSkipVerify: true,
	}
}

func envOrDefault(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
