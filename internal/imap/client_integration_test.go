//go:build integration

package imap_test

import (
	"context"
	"net/smtp"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/isseis/tlsrpt-digest/internal/config"
	"github.com/isseis/tlsrpt-digest/internal/imap"
	imaptestutil "github.com/isseis/tlsrpt-digest/internal/imap/testutil"
	"github.com/stretchr/testify/require"
)

// testRunID returns a unique suffix for this test binary invocation.
var testRunID = sync.OnceValue(func() string {
	return ulid.Make().String()
})

// sanitizeIdentifier replaces characters not valid in an IMAP mailbox name
// (keeping only alphanumerics and hyphens) with hyphens.
var sanitizeIdentifier = regexp.MustCompile(`[^a-zA-Z0-9-]`)

// testRecipientEmail returns a per-call unique recipient email address.
func testRecipientEmail() string {
	return ulid.Make().String() + "@test.example.com"
}

// testMessageID returns a per-call unique Message-ID.
func testMessageID() string {
	return "<" + ulid.Make().String() + "@test.example.com>"
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
	return checkEnvSpecs(getenv, []envSpec{
		{"IMAP_TEST_HOST", false},
		{"IMAP_TEST_PORT", true},
		{"IMAP_TEST_USER", false},
		{"IMAP_TEST_PASS", false},
		{"IMAP_TEST_MAILBOX", false},
	})
}

// missingSMTPEnv returns the names of environment variables required for
// SMTP-injection integration tests that are missing or invalid.
func missingSMTPEnv(getenv func(string) string) []string {
	return checkEnvSpecs(getenv, []envSpec{
		{"IMAP_TEST_HOST", false},
		{"IMAP_TEST_PORT", true},
		{"IMAP_TEST_SMTP_HOST", false},
		{"IMAP_TEST_SMTP_PORT", true},
	})
}

// envSpec describes a required environment variable and whether its value must
// be parseable as an integer.
type envSpec struct {
	key       string
	mustBeInt bool
}

// checkEnvSpecs validates a list of env var specs and returns the names of
// variables that are missing or fail their type constraint.
func checkEnvSpecs(getenv func(string) string, specs []envSpec) []string {
	var missing []string
	for _, s := range specs {
		val := getenv(s.key)
		if val == "" {
			missing = append(missing, s.key+" (empty)")
			continue
		}
		if s.mustBeInt {
			if _, err := strconv.Atoi(val); err != nil {
				missing = append(missing, s.key+" (not a valid integer)")
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
func loadSMTPTestConfig(t *testing.T) (cfg imap.Config, smtpAddr string) {
	t.Helper()
	requireSMTPEnv(t)

	port, err := strconv.Atoi(os.Getenv("IMAP_TEST_PORT"))
	require.NoError(t, err)

	recipient := testRecipientEmail()
	cfg = imap.Config{
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

func loadIntegrationConfig(t *testing.T) imap.Config {
	t.Helper()
	requireFixedUserEnv(t)

	port, err := strconv.Atoi(os.Getenv("IMAP_TEST_PORT"))
	require.NoError(t, err)

	return imap.Config{
		Host:               os.Getenv("IMAP_TEST_HOST"),
		Port:               port,
		Username:           os.Getenv("IMAP_TEST_USER"),
		Password:           config.Secret(os.Getenv("IMAP_TEST_PASS")),
		Mailbox:            os.Getenv("IMAP_TEST_MAILBOX"),
		InsecureSkipVerify: true,
	}
}

func TestIntegration_EnvConfig(t *testing.T) {
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
	t.Run("smtp_smtp_host_missing", func(t *testing.T) {
		env := map[string]string{
			"IMAP_TEST_HOST":      "h",
			"IMAP_TEST_PORT":      "3993",
			"IMAP_TEST_SMTP_PORT": "3025",
		}
		got := missingSMTPEnv(func(k string) string { return env[k] })
		require.Contains(t, strings.Join(got, " "), "IMAP_TEST_SMTP_HOST")
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
	t.Run("smtp_smtp_port_invalid", func(t *testing.T) {
		env := map[string]string{
			"IMAP_TEST_HOST":      "h",
			"IMAP_TEST_PORT":      "3993",
			"IMAP_TEST_SMTP_HOST": "h",
			"IMAP_TEST_SMTP_PORT": "notanint",
		}
		got := missingSMTPEnv(func(k string) string { return env[k] })
		require.Contains(t, strings.Join(got, " "), "IMAP_TEST_SMTP_PORT")
	})
	t.Run("fixed_user_all_valid", func(t *testing.T) {
		env := map[string]string{
			"IMAP_TEST_HOST":    "h",
			"IMAP_TEST_PORT":    "3993",
			"IMAP_TEST_USER":    "u",
			"IMAP_TEST_PASS":    "p",
			"IMAP_TEST_MAILBOX": "INBOX",
		}
		got := missingFixedUserEnv(func(k string) string { return env[k] })
		require.Empty(t, got)
	})
	t.Run("smtp_all_valid", func(t *testing.T) {
		env := map[string]string{
			"IMAP_TEST_HOST":      "h",
			"IMAP_TEST_PORT":      "3993",
			"IMAP_TEST_SMTP_HOST": "h",
			"IMAP_TEST_SMTP_PORT": "3025",
		}
		got := missingSMTPEnv(func(k string) string { return env[k] })
		require.Empty(t, got)
	})
}

// TestIntegration_EmptyInbox verifies FetchMeta on an empty fixed-user mailbox.
func TestIntegration_EmptyInbox(t *testing.T) {
	cfg := loadIntegrationConfig(t)
	client, err := imap.NewIMAPClient(cfg)
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })

	result, err := client.FetchMeta(context.Background(), time.Now().AddDate(-1, 0, 0))
	require.NoError(t, err)
	require.Empty(t, result.Messages)
	require.Positive(t, result.UIDValidity)
}

// TestIntegration_FetchMeta verifies FetchMeta retrieves metadata of an injected message.
func TestIntegration_FetchMeta(t *testing.T) {
	cfg, smtpAddr := loadSMTPTestConfig(t)
	messageID := testMessageID()
	injectTestMail(t, smtpAddr, cfg.Username, "fetch-meta-test", "test body", messageID)

	client, err := imap.NewIMAPClient(cfg)
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })

	result, err := client.FetchMeta(context.Background(), time.Now().AddDate(-1, 0, 0))
	require.NoError(t, err)

	var found *imap.MessageMeta
	for i := range result.Messages {
		if normalizeMessageID(result.Messages[i].MessageID) == normalizeMessageID(messageID) {
			found = &result.Messages[i]
			break
		}
	}
	require.NotNil(t, found, "injected message not found in FetchMeta result")
	require.Positive(t, found.UID)
	require.Positive(t, found.Size)
}

// TestIntegration_Download verifies Download retrieves full message body.
func TestIntegration_Download(t *testing.T) {
	cfg, smtpAddr := loadSMTPTestConfig(t)
	messageID := testMessageID()
	injectTestMail(t, smtpAddr, cfg.Username, "download-test", "test body", messageID)

	client, err := imap.NewIMAPClient(cfg)
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })

	ctx := context.Background()
	since := time.Now().AddDate(-1, 0, 0)
	result, err := client.FetchMeta(ctx, since)
	require.NoError(t, err)

	var uid uint32
	for _, meta := range result.Messages {
		if normalizeMessageID(meta.MessageID) == normalizeMessageID(messageID) {
			uid = meta.UID
			break
		}
	}
	require.NotZero(t, uid, "injected message not found in FetchMeta result")

	bodies, err := client.Download(ctx, []uint32{uid})
	require.NoError(t, err)
	require.Contains(t, bodies, uid, "downloaded map must contain the requested UID")
	require.Contains(t, string(bodies[uid]), "Subject: download-test")
}

// TestIntegration_MarkSeen verifies MarkSeen sets the Seen flag.
func TestIntegration_MarkSeen(t *testing.T) {
	cfg, smtpAddr := loadSMTPTestConfig(t)
	messageID := testMessageID()
	injectTestMail(t, smtpAddr, cfg.Username, "mark-seen-test", "test body", messageID)

	ctx := context.Background()
	since := time.Now().AddDate(-1, 0, 0)

	client, err := imap.NewIMAPClient(cfg)
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })

	result, err := client.FetchMeta(ctx, since)
	require.NoError(t, err)

	var uid uint32
	for _, meta := range result.Messages {
		if normalizeMessageID(meta.MessageID) == normalizeMessageID(messageID) {
			require.False(t, meta.Seen)
			uid = meta.UID
			break
		}
	}
	require.NotZero(t, uid, "injected message not found in FetchMeta result")

	require.NoError(t, client.MarkSeen(ctx, []uint32{uid}))

	client2, err := imap.NewIMAPClient(cfg)
	require.NoError(t, err)
	t.Cleanup(func() { _ = client2.Close() })

	result2, err := client2.FetchMeta(ctx, since)
	require.NoError(t, err)
	for _, meta := range result2.Messages {
		if normalizeMessageID(meta.MessageID) == normalizeMessageID(messageID) {
			require.True(t, meta.Seen)
			return
		}
	}
	t.Fatal("injected message not found in second FetchMeta result")
}

// TestIntegration_UIDValidity_Stable verifies UIDValidity is stable across consecutive FetchMeta calls.
func TestIntegration_UIDValidity_Stable(t *testing.T) {
	cfg := loadIntegrationConfig(t)

	client, err := imap.NewIMAPClient(cfg)
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })

	ctx := context.Background()
	since := time.Now().AddDate(-1, 0, 0)

	r1, err := client.FetchMeta(ctx, since)
	require.NoError(t, err)
	r2, err := client.FetchMeta(ctx, since)
	require.NoError(t, err)
	require.Equal(t, r1.UIDValidity, r2.UIDValidity)
}

// TestIntegration_UIDValidity_Change verifies UIDValidity changes after mailbox DELETE and CREATE.
func TestIntegration_UIDValidity_Change(t *testing.T) {
	fixedCfg := loadIntegrationConfig(t)
	mailbox := testMailboxName(t)

	imaptestutil.CreateMailbox(t, fixedCfg, mailbox)
	t.Cleanup(func() { imaptestutil.DeleteMailbox(t, fixedCfg, mailbox) })

	testCfg := fixedCfg
	testCfg.Mailbox = mailbox

	// FetchUIDValidity uses EXAMINE + IMAP CLOSE so greenmail releases the
	// mailbox before the subsequent DELETE (LOGOUT alone leaves it in use).
	v1 := imaptestutil.FetchUIDValidity(t, testCfg, mailbox)

	// greenmail assigns UIDVALIDITY from the current Unix timestamp (second resolution).
	// Wait one second so that the recreated mailbox gets a strictly later timestamp.
	time.Sleep(time.Second)
	imaptestutil.DeleteMailbox(t, fixedCfg, mailbox)
	imaptestutil.CreateMailbox(t, fixedCfg, mailbox)

	v2 := imaptestutil.FetchUIDValidity(t, testCfg, mailbox)
	require.NotEqual(t, v1, v2)
}

// TestIntegration_DeleteOlderThan verifies DeleteOlderThan removes a message
// whose INTERNALDATE is before cutoff, or records greenmail's UIDPLUS support
// status if the server does not support UID EXPUNGE.
func TestIntegration_DeleteOlderThan(t *testing.T) {
	cfg, smtpAddr := loadSMTPTestConfig(t)
	messageID := testMessageID()
	injectTestMail(t, smtpAddr, cfg.Username, "delete-older-than-test", "test body", messageID)

	ctx := context.Background()

	client1, err := imap.NewIMAPClient(cfg)
	require.NoError(t, err)
	t.Cleanup(func() { _ = client1.Close() })

	result, err := client1.FetchMeta(ctx, time.Now().AddDate(-1, 0, 0))
	require.NoError(t, err)

	var wantUID uint32
	for _, meta := range result.Messages {
		if normalizeMessageID(meta.MessageID) == normalizeMessageID(messageID) {
			wantUID = meta.UID
			break
		}
	}
	require.NotZero(t, wantUID, "injected message not found in FetchMeta result")

	// cutoff is tomorrow so the message's INTERNALDATE (today) is before
	// truncateToDate(cutoff) (tomorrow 00:00).
	cutoff := time.Now().AddDate(0, 0, 1)
	deleted, err := client1.DeleteOlderThan(ctx, cutoff)
	require.NoError(t, err)

	client2, err := imap.NewIMAPClient(cfg)
	require.NoError(t, err)
	t.Cleanup(func() { _ = client2.Close() })

	gotUIDs, err := client2.SearchOlderThan(ctx, cutoff)
	require.NoError(t, err)

	if deleted > 0 {
		require.NotContains(t, gotUIDs, wantUID, "message must be deleted when UIDPLUS is supported")
	} else {
		require.Contains(t, gotUIDs, wantUID, "message must remain when UIDPLUS is not supported")
	}
	t.Logf("greenmail UIDPLUS support: deleted=%d (>0 means UIDPLUS supported)", deleted)
}

// TestIntegration_SearchOlderThan_ReadOnly verifies SearchOlderThan is
// idempotent: repeated calls on the same session return the same UIDs,
// confirming EXAMINE does not mutate mailbox state.
func TestIntegration_SearchOlderThan_ReadOnly(t *testing.T) {
	cfg, smtpAddr := loadSMTPTestConfig(t)
	messageID := testMessageID()
	injectTestMail(t, smtpAddr, cfg.Username, "search-older-than-test", "test body", messageID)

	ctx := context.Background()
	cutoff := time.Now().AddDate(0, 0, 1)

	client, err := imap.NewIMAPClient(cfg)
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })

	uids1, err := client.SearchOlderThan(ctx, cutoff)
	require.NoError(t, err)
	uids2, err := client.SearchOlderThan(ctx, cutoff)
	require.NoError(t, err)
	require.Equal(t, uids1, uids2)
}
