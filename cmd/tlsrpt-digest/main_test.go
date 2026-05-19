//go:build test

package main

import (
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/isseis/tlsrpt-digest/internal/config"
	"github.com/isseis/tlsrpt-digest/internal/notify"
	"github.com/isseis/tlsrpt-digest/internal/store"
	storetestutil "github.com/isseis/tlsrpt-digest/internal/store/testutil"
	"github.com/isseis/tlsrpt-digest/internal/tlsrpt"
)

const (
	testAllowedHost = "hooks.slack.com"
	testSuccessURL  = "https://hooks.slack.com/services/s"
	testErrorURL    = "https://hooks.slack.com/services/e"
)

func newConfigWithAllowedHost(host string) *config.Config {
	return &config.Config{
		Notify: config.NotifyConfig{
			Slack: config.NotifySlackConfig{AllowedHost: host},
		},
	}
}

func TestBootstrap_Phase1_NoSlackHandler(t *testing.T) {
	orig := slog.Default()
	t.Cleanup(func() {
		slog.SetDefault(orig)
	})

	h := setupPhase1Logging()
	require.NotNil(t, h)

	_, isSlack := h.(*notify.SlackHandler)
	assert.False(t, isSlack)
}

func TestBootstrap_ErrorOnly_NoSuccessHandler(t *testing.T) {
	cfg := newConfigWithAllowedHost(testAllowedHost)

	handlers, err := setupNotifyHandlers("", testErrorURL, cfg, "run-1", false)
	require.NoError(t, err)
	require.Len(t, handlers, 1)
	assert.Equal(t, notify.LevelModeWarnAndAbove, handlers[0].LevelMode())
}

func TestBootstrap_Phase2_SlackAdded(t *testing.T) {
	cfg := newConfigWithAllowedHost(testAllowedHost)

	handlers, err := setupNotifyHandlers(testSuccessURL, testErrorURL, cfg, "run-2", false)
	require.NoError(t, err)
	require.Len(t, handlers, 2)

	gotModes := []notify.LevelMode{handlers[0].LevelMode(), handlers[1].LevelMode()}
	assert.ElementsMatch(t, []notify.LevelMode{notify.LevelModeExactInfo, notify.LevelModeWarnAndAbove}, gotModes)
}

func TestBootstrap_AllowedHostPropagation(t *testing.T) {
	cfg := newConfigWithAllowedHost("other.host.com")

	_, err := setupNotifyHandlers(testSuccessURL, testErrorURL, cfg, "run-3", false)
	require.Error(t, err)

	_, ok := errors.AsType[*notify.WebhookValidationError](err)
	require.True(t, ok)
}

func TestBootstrap_Phase2_ValidationFail_Abort(t *testing.T) {
	cfg := newConfigWithAllowedHost(testAllowedHost)

	_, err := setupNotifyHandlers(
		"http://hooks.slack.com/services/s",
		"http://hooks.slack.com/services/e",
		cfg,
		"run-4",
		false,
	)
	require.Error(t, err)

	_, ok := errors.AsType[*notify.WebhookValidationError](err)
	require.True(t, ok)
}

func TestBootstrap_DryRunFlag(t *testing.T) {
	cfg := newConfigWithAllowedHost(testAllowedHost)

	handlers, err := setupNotifyHandlers(testSuccessURL, testErrorURL, cfg, "run-5", true)
	require.NoError(t, err)
	require.Len(t, handlers, 2)

	for _, h := range handlers {
		assert.True(t, h.IsDryRun())
	}
}

func TestBootstrap_DryRun_NoURLs(t *testing.T) {
	cfg := newConfigWithAllowedHost("")

	handlers, err := setupNotifyHandlers("", "", cfg, "run-6", true)
	require.NoError(t, err)
	require.Len(t, handlers, 2)

	for _, h := range handlers {
		assert.True(t, h.IsDryRun())
	}

	gotModes := []notify.LevelMode{handlers[0].LevelMode(), handlers[1].LevelMode()}
	assert.ElementsMatch(t, []notify.LevelMode{notify.LevelModeExactInfo, notify.LevelModeWarnAndAbove}, gotModes)
}

func TestLoadConfig_EmptyPath(t *testing.T) {
	cfg, err := loadConfig("")
	require.NoError(t, err)
	assert.Equal(t, &config.Config{}, cfg)
}

func TestLoadConfig_ValidFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	require.NoError(t, os.WriteFile(path, []byte("[notify.slack]\nallowed_host = \"hooks.slack.com\"\n"), 0o600))

	cfg, err := loadConfig(path)
	require.NoError(t, err)
	assert.Equal(t, "hooks.slack.com", cfg.Notify.Slack.AllowedHost)
}

func TestLoadConfig_NonexistentFile(t *testing.T) {
	_, err := loadConfig("/nonexistent/path/config.toml")
	require.Error(t, err)
}

// Compile-time check: FakeStore must implement store.Store so the interface
// stays in sync as the Store contract evolves.
var _ store.Store = (*storetestutil.FakeStore)(nil)

// TestStoreOpenMode verifies that subcommands are mapped to the correct open mode.
func TestStoreOpenMode(t *testing.T) {
	assert.Equal(t, store.OpenReadWrite, storeOpenMode("fetch"))
	assert.Equal(t, store.OpenReadWrite, storeOpenMode("gc"))
	assert.Equal(t, store.OpenReadWrite, storeOpenMode("reprocess"))
	assert.Equal(t, store.OpenReadWrite, storeOpenMode("recover"))
	assert.Equal(t, store.OpenReadOnly, storeOpenMode("summary"))
}

// TestOpenStoreForSubcommand verifies that openStoreForSubcommand wires
// storeOpenMode into store.Open correctly.  Write-capable subcommands must
// succeed on SaveReport; the summary subcommand must be refused with ErrReadOnly.
func TestOpenStoreForSubcommand(t *testing.T) {
	identity := store.IMAPIdentity{Host: "imap.example.com", Port: 993, Mailbox: "INBOX"}
	endDate := time.Date(2025, 6, 15, 0, 0, 0, 0, time.UTC)
	probe := store.ReportInput{
		Report: tlsrpt.Report{
			ReportID:  "probe",
			DateRange: tlsrpt.DateRange{EndDatetime: endDate},
		},
		UID: 1, UIDValidity: 1,
	}

	cases := []struct {
		subcommand   string
		wantReadOnly bool
	}{
		{"fetch", false},
		{"gc", false},
		{"reprocess", false},
		{"recover", false},
		{"summary", true},
	}

	for _, tc := range cases {
		t.Run(tc.subcommand, func(t *testing.T) {
			s, err := openStoreForSubcommand(t.TempDir(), identity, tc.subcommand)
			require.NoError(t, err)

			writeErr := store.SaveReport(s, probe)
			if tc.wantReadOnly {
				assert.ErrorIs(t, writeErr, store.ErrReadOnly,
					"summary must use OpenReadOnly")
			} else {
				assert.NoError(t, writeErr,
					"%s must use OpenReadWrite", tc.subcommand)
			}
		})
	}
}
