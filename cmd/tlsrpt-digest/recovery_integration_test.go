//go:build integration

package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/stretchr/testify/require"

	"github.com/isseis/tlsrpt-digest/internal/config"
	"github.com/isseis/tlsrpt-digest/internal/imap"
	imaptestutil "github.com/isseis/tlsrpt-digest/internal/imap/testutil"
	"github.com/isseis/tlsrpt-digest/internal/store"
)

// recoveryTestMailboxName returns a per-call unique IMAP mailbox name.
func recoveryTestMailboxName() string {
	return ulid.Make().String()
}

// missingRecoveryEnv returns the names of environment variables required for
// recovery integration tests that are missing or invalid.
func missingRecoveryEnv(getenv func(string) string) []string {
	return checkRecoveryEnvSpecs(getenv, []recoveryEnvSpec{
		{"IMAP_TEST_HOST", false},
		{"IMAP_TEST_PORT", true},
		{"IMAP_TEST_USER", false},
		{"IMAP_TEST_PASS", false},
		{"IMAP_TEST_MAILBOX", false},
	})
}

// recoveryEnvSpec describes a required environment variable and whether its
// value must be parseable as an integer.
type recoveryEnvSpec struct {
	key       string
	mustBeInt bool
}

// checkRecoveryEnvSpecs validates a list of env var specs and returns the
// names of variables that are missing or fail their type constraint.
func checkRecoveryEnvSpecs(getenv func(string) string, specs []recoveryEnvSpec) []string {
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

// loadRecoveryTestEnv skips the test when any required environment variable is
// missing or invalid, then propagates IMAP credentials to the env keys that
// the fetch subcommand reads.
func loadRecoveryTestEnv(t *testing.T) {
	t.Helper()
	if missing := missingRecoveryEnv(os.Getenv); len(missing) > 0 {
		t.Skip("recovery integration env not configured: " + strings.Join(missing, ", "))
	}
	t.Setenv("TLSRPT_IMAP_USERNAME", os.Getenv("IMAP_TEST_USER"))
	t.Setenv("TLSRPT_IMAP_PASSWORD", os.Getenv("IMAP_TEST_PASS"))
}

// buildTestConfigTOML writes a minimal config.toml to a temp file and returns
// its path. The config connects to the given IMAP host/port/mailbox, uses
// rootDir as the store location, and sets an empty Slack allowed_host so that
// Bootstrap succeeds without a real webhook URL.
func buildTestConfigTOML(t *testing.T, rootDir, imapHost string, imapPort int, mailbox string) string {
	t.Helper()
	content := fmt.Sprintf(`[imap]
host = %q
port = %d
mailbox = %q

[store]
root_dir = %q

[notify.slack]
allowed_host = ""
`, imapHost, imapPort, mailbox, rootDir)
	path := t.TempDir() + "/config.toml"
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	return path
}

// insecureMailFetcherFactory wraps imap.NewIMAPClient with InsecureSkipVerify=true
// for integration tests against self-signed servers (e.g. greenmail).
func insecureMailFetcherFactory(cfg imap.Config) (imap.MailFetcher, error) {
	cfg.InsecureSkipVerify = true
	return imap.NewIMAPClient(cfg)
}

func TestIntegration_RecoveryEnvRequirements(t *testing.T) {
	t.Run("host_missing", func(t *testing.T) {
		env := map[string]string{
			"IMAP_TEST_PORT":    "3993",
			"IMAP_TEST_USER":    "u",
			"IMAP_TEST_PASS":    "p",
			"IMAP_TEST_MAILBOX": "INBOX",
		}
		got := missingRecoveryEnv(func(k string) string { return env[k] })
		require.Contains(t, strings.Join(got, " "), "IMAP_TEST_HOST")
	})
	t.Run("port_missing", func(t *testing.T) {
		env := map[string]string{
			"IMAP_TEST_HOST":    "h",
			"IMAP_TEST_USER":    "u",
			"IMAP_TEST_PASS":    "p",
			"IMAP_TEST_MAILBOX": "INBOX",
		}
		got := missingRecoveryEnv(func(k string) string { return env[k] })
		require.Contains(t, strings.Join(got, " "), "IMAP_TEST_PORT")
	})
	t.Run("port_invalid", func(t *testing.T) {
		env := map[string]string{
			"IMAP_TEST_HOST":    "h",
			"IMAP_TEST_PORT":    "notanint",
			"IMAP_TEST_USER":    "u",
			"IMAP_TEST_PASS":    "p",
			"IMAP_TEST_MAILBOX": "INBOX",
		}
		got := missingRecoveryEnv(func(k string) string { return env[k] })
		require.Contains(t, strings.Join(got, " "), "IMAP_TEST_PORT")
	})
	t.Run("user_missing", func(t *testing.T) {
		env := map[string]string{
			"IMAP_TEST_HOST":    "h",
			"IMAP_TEST_PORT":    "3993",
			"IMAP_TEST_PASS":    "p",
			"IMAP_TEST_MAILBOX": "INBOX",
		}
		got := missingRecoveryEnv(func(k string) string { return env[k] })
		require.Contains(t, strings.Join(got, " "), "IMAP_TEST_USER")
	})
	t.Run("pass_missing", func(t *testing.T) {
		env := map[string]string{
			"IMAP_TEST_HOST":    "h",
			"IMAP_TEST_PORT":    "3993",
			"IMAP_TEST_USER":    "u",
			"IMAP_TEST_MAILBOX": "INBOX",
		}
		got := missingRecoveryEnv(func(k string) string { return env[k] })
		require.Contains(t, strings.Join(got, " "), "IMAP_TEST_PASS")
	})
	t.Run("mailbox_missing", func(t *testing.T) {
		env := map[string]string{
			"IMAP_TEST_HOST": "h",
			"IMAP_TEST_PORT": "3993",
			"IMAP_TEST_USER": "u",
			"IMAP_TEST_PASS": "p",
		}
		got := missingRecoveryEnv(func(k string) string { return env[k] })
		require.Contains(t, strings.Join(got, " "), "IMAP_TEST_MAILBOX")
	})
	t.Run("all_valid", func(t *testing.T) {
		env := map[string]string{
			"IMAP_TEST_HOST":    "h",
			"IMAP_TEST_PORT":    "3993",
			"IMAP_TEST_USER":    "u",
			"IMAP_TEST_PASS":    "p",
			"IMAP_TEST_MAILBOX": "INBOX",
		}
		got := missingRecoveryEnv(func(k string) string { return env[k] })
		require.Empty(t, got)
	})
	t.Run("credential_propagation", func(t *testing.T) {
		t.Setenv("IMAP_TEST_HOST", "h")
		t.Setenv("IMAP_TEST_PORT", "3993")
		t.Setenv("IMAP_TEST_USER", "testuser@example.com")
		t.Setenv("IMAP_TEST_PASS", "testpass")
		t.Setenv("IMAP_TEST_MAILBOX", "INBOX")
		loadRecoveryTestEnv(t)
		require.Equal(t, "testuser@example.com", os.Getenv("TLSRPT_IMAP_USERNAME"))
		require.Equal(t, "testpass", os.Getenv("TLSRPT_IMAP_PASSWORD"))
	})
}

// TestIntegration_Recovery_KeepOld verifies that fetch detects a UIDVALIDITY
// change and that recover --mode keep-old resolves the mismatch.
func TestIntegration_Recovery_KeepOld(t *testing.T) {
	loadRecoveryTestEnv(t)
	// Store root requires 0700 or 0750; t.TempDir creates 0755.
	rootDir := filepath.Join(t.TempDir(), "store")
	require.NoError(t, os.Mkdir(rootDir, 0o700))

	imapHost := os.Getenv("IMAP_TEST_HOST")
	imapPort, err := strconv.Atoi(os.Getenv("IMAP_TEST_PORT"))
	require.NoError(t, err)

	fixedCfg := imap.Config{
		Host:               imapHost,
		Port:               imapPort,
		Username:           os.Getenv("IMAP_TEST_USER"),
		Password:           config.Secret(os.Getenv("IMAP_TEST_PASS")),
		InsecureSkipVerify: true,
	}

	mailbox := recoveryTestMailboxName()
	imaptestutil.CreateMailbox(t, fixedCfg, mailbox)
	t.Cleanup(func() { imaptestutil.DeleteMailbox(t, fixedCfg, mailbox) })

	configPath := buildTestConfigTOML(t, rootDir, imapHost, imapPort, mailbox)

	fr := newFetchRunner()
	fr.newMailFetcher = insecureMailFetcherFactory
	withCommandRunners(t, map[SubcommandName]SubcommandRunner{
		subcommandFetch:   fr,
		subcommandRecover: newRecoverRunner(),
	})

	// Initial fetch records UIDVALIDITY.
	require.Equal(t, exitOK, runCLI(context.Background(), []string{"fetch", "-config", configPath, "-dry-run"}, io.Discard, BootstrapOptions{}))

	// greenmail assigns UIDVALIDITY from the current Unix timestamp (second
	// resolution). Wait until the Unix second ticks so the recreated mailbox gets a different value.
	for start := time.Now().Unix(); time.Now().Unix() == start; {
		time.Sleep(10 * time.Millisecond)
	}

	// DELETE + CREATE changes UIDVALIDITY.
	imaptestutil.DeleteMailbox(t, fixedCfg, mailbox)
	imaptestutil.CreateMailbox(t, fixedCfg, mailbox)

	// Re-fetch detects the mismatch and exits with an error.
	require.Equal(t, exitError, runCLI(context.Background(), []string{"fetch", "-config", configPath, "-dry-run"}, io.Discard, BootstrapOptions{}))

	// Verify that recovery-required is persisted in the store.
	s, err := store.Open(rootDir, store.IMAPIdentity{Host: imapHost, Port: imapPort, Mailbox: mailbox}, store.OpenReadOnly)
	require.NoError(t, err)
	_, _, _, found, storeErr := s.LoadRecoveryRequired()
	require.NoError(t, storeErr)
	require.True(t, found, "store must have recovery-required set after UIDVALIDITY mismatch")

	// recover --mode keep-old resolves the mismatch.
	require.Equal(t, exitOK, runCLI(context.Background(), []string{"recover", "-config", configPath, "-mode", "keep-old"}, io.Discard, BootstrapOptions{}))

	// A subsequent fetch succeeds.
	require.Equal(t, exitOK, runCLI(context.Background(), []string{"fetch", "-config", configPath, "-dry-run"}, io.Discard, BootstrapOptions{}))
}

// TestIntegration_Recovery_DiscardOld verifies that recover --mode discard-old
// --yes resolves a UIDVALIDITY mismatch.
func TestIntegration_Recovery_DiscardOld(t *testing.T) {
	loadRecoveryTestEnv(t)
	// Store root requires 0700 or 0750; t.TempDir creates 0755.
	rootDir := filepath.Join(t.TempDir(), "store")
	require.NoError(t, os.Mkdir(rootDir, 0o700))

	imapHost := os.Getenv("IMAP_TEST_HOST")
	imapPort, err := strconv.Atoi(os.Getenv("IMAP_TEST_PORT"))
	require.NoError(t, err)

	fixedCfg := imap.Config{
		Host:               imapHost,
		Port:               imapPort,
		Username:           os.Getenv("IMAP_TEST_USER"),
		Password:           config.Secret(os.Getenv("IMAP_TEST_PASS")),
		InsecureSkipVerify: true,
	}

	mailbox := recoveryTestMailboxName()
	imaptestutil.CreateMailbox(t, fixedCfg, mailbox)
	t.Cleanup(func() { imaptestutil.DeleteMailbox(t, fixedCfg, mailbox) })

	configPath := buildTestConfigTOML(t, rootDir, imapHost, imapPort, mailbox)

	fr := newFetchRunner()
	fr.newMailFetcher = insecureMailFetcherFactory
	withCommandRunners(t, map[SubcommandName]SubcommandRunner{
		subcommandFetch:   fr,
		subcommandRecover: newRecoverRunner(),
	})

	// Initial fetch records UIDVALIDITY.
	require.Equal(t, exitOK, runCLI(context.Background(), []string{"fetch", "-config", configPath, "-dry-run"}, io.Discard, BootstrapOptions{}))

	// greenmail assigns UIDVALIDITY from the current Unix timestamp (second
	// resolution). Wait until the Unix second ticks so the recreated mailbox gets a different value.
	for start := time.Now().Unix(); time.Now().Unix() == start; {
		time.Sleep(10 * time.Millisecond)
	}

	// DELETE + CREATE changes UIDVALIDITY.
	imaptestutil.DeleteMailbox(t, fixedCfg, mailbox)
	imaptestutil.CreateMailbox(t, fixedCfg, mailbox)

	// Re-fetch detects the mismatch and exits with an error.
	require.Equal(t, exitError, runCLI(context.Background(), []string{"fetch", "-config", configPath, "-dry-run"}, io.Discard, BootstrapOptions{}))

	// Verify that recovery-required is persisted in the store.
	s, err := store.Open(rootDir, store.IMAPIdentity{Host: imapHost, Port: imapPort, Mailbox: mailbox}, store.OpenReadOnly)
	require.NoError(t, err)
	_, _, _, found, storeErr := s.LoadRecoveryRequired()
	require.NoError(t, storeErr)
	require.True(t, found, "store must have recovery-required set after UIDVALIDITY mismatch")

	// recover --mode discard-old --yes resolves the mismatch.
	require.Equal(t, exitOK, runCLI(context.Background(), []string{"recover", "-config", configPath, "-mode", "discard-old", "-yes"}, io.Discard, BootstrapOptions{}))

	// A subsequent fetch succeeds.
	require.Equal(t, exitOK, runCLI(context.Background(), []string{"fetch", "-config", configPath, "-dry-run"}, io.Discard, BootstrapOptions{}))
}
