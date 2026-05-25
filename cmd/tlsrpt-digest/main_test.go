//go:build test

package main

import (
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/isseis/tlsrpt-digest/internal/config"
	"github.com/isseis/tlsrpt-digest/internal/notify"
	"github.com/isseis/tlsrpt-digest/internal/store"
	storetestutil "github.com/isseis/tlsrpt-digest/internal/store/testutil"
	"github.com/isseis/tlsrpt-digest/internal/storelock"
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

func TestBuildIMAPConfig(t *testing.T) {
	t.Setenv("TLSRPT_IMAP_USERNAME", "testuser")
	t.Setenv("TLSRPT_IMAP_PASSWORD", "testpass")

	cfg := &config.Config{
		IMAP: config.IMAPConfig{
			Host:            "imap.example.com",
			Port:            993,
			Mailbox:         "INBOX",
			TLSCACert:       "/etc/ssl/cert.pem",
			MaxMessageBytes: 1024,
		},
	}

	got := buildIMAPConfig(cfg)

	assert.Equal(t, "imap.example.com", got.Host)
	assert.Equal(t, 993, got.Port)
	assert.Equal(t, "INBOX", got.Mailbox)
	assert.Equal(t, "/etc/ssl/cert.pem", got.TLSCACert)
	assert.Equal(t, int64(1024), got.MaxMessageBytes)
	assert.Equal(t, "testuser", got.Username)
	assert.Equal(t, config.Secret("testpass"), got.Password)
}

func TestLoadConfig_EmptyPath(t *testing.T) {
	_, err := loadConfig("")
	require.Error(t, err)
	assert.True(t, errors.Is(err, config.ErrConfigPathEmpty))
}

func TestLoadConfig_ValidFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	require.NoError(t, os.WriteFile(path, []byte(`[imap]
host = "imap.example.com"
port = 993

[notify.slack]
allowed_host = "hooks.slack.com"
`), 0o600))

	cfg, err := loadConfig(path)
	require.NoError(t, err)
	assert.Equal(t, "hooks.slack.com", cfg.Notify.Slack.AllowedHost)
}

func TestLoadConfig_NonexistentFile(t *testing.T) {
	_, err := loadConfig("/nonexistent/path/config.toml")
	require.Error(t, err)
	assert.True(t, errors.Is(err, config.ErrConfigFileRead))
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

func TestAcquireStoreWriterLock_CreatesRootAndHoldsLock(t *testing.T) {
	rootDir := filepath.Join(t.TempDir(), "store")

	h, err := acquireStoreWriterLock(rootDir)
	require.NoError(t, err)
	defer func() { require.NoError(t, h.Close()) }()

	info, err := os.Stat(rootDir)
	require.NoError(t, err)
	assert.True(t, info.IsDir())

	second, err := storelock.Acquire(storelock.LockPath(rootDir))
	assert.Nil(t, second)
	assert.ErrorIs(t, err, storelock.ErrLockHeld)
}

func TestAcquireStoreWriterLock_ReleasesLock(t *testing.T) {
	rootDir := t.TempDir()

	h, err := acquireStoreWriterLock(rootDir)
	require.NoError(t, err)
	require.NoError(t, h.Close())

	second, err := storelock.Acquire(storelock.LockPath(rootDir))
	require.NoError(t, err)
	require.NoError(t, second.Close())
}

func TestAcquireStoreWriterLock_RejectsSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "real")
	require.NoError(t, os.Mkdir(target, 0o700))
	link := filepath.Join(dir, "link")
	require.NoError(t, os.Symlink(target, link))

	h, err := acquireStoreWriterLock(link)
	assert.Nil(t, h)
	assert.ErrorIs(t, err, errRootDirSymlink)
}

func TestAcquireStoreWriterLock_RejectsNonDirectory(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "notadir")
	require.NoError(t, os.WriteFile(file, []byte{}, 0o600))

	h, err := acquireStoreWriterLock(file)
	assert.Nil(t, h)
	assert.ErrorIs(t, err, errRootDirNotDirectory)
}

func TestAcquireStoreWriterLock_LoosePermissionsSucceeds(t *testing.T) {
	dir := t.TempDir()
	rootDir := filepath.Join(dir, "store")
	require.NoError(t, os.Mkdir(rootDir, 0o750))

	h, err := acquireStoreWriterLock(rootDir)
	require.NoError(t, err)
	require.NoError(t, h.Close())
}
