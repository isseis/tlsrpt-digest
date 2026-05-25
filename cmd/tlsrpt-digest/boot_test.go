//go:build test

package main

import (
	"errors"
	"fmt"
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
		IMAP: config.IMAPConfig{
			Host:    "imap.example.com",
			Port:    993,
			Mailbox: "INBOX",
		},
		Notify: config.NotifyConfig{
			Slack: config.NotifySlackConfig{AllowedHost: host},
		},
		Store: config.StoreConfig{
			RootDir: tRootDirFallback,
		},
	}
}

const tRootDirFallback = "/tmp/tlsrpt-digest-test-store"

func configForRoot(rootDir string) *config.Config {
	cfg := newConfigWithAllowedHost(testAllowedHost)
	cfg.Store.RootDir = rootDir
	return cfg
}

func secureStoreRoot(t *testing.T) string {
	t.Helper()
	rootDir := filepath.Join(t.TempDir(), "store")
	require.NoError(t, os.Mkdir(rootDir, 0o700))
	return rootDir
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

func TestBootstrap_ConfigLoadFailureDoesNotBuildNotifier(t *testing.T) {
	buildCalled := false
	_, err := Bootstrap(subcommandFetch, "missing.toml", "run-1", BootstrapOptions{
		LoadConfig: func(string) (*config.Config, error) {
			return nil, config.ErrConfigFileRead
		},
		BuildNotifier: func(config.Secret, config.Secret, *config.Config, string, bool) (NotificationSink, error) {
			buildCalled = true
			return &SpyNotificationSink{}, nil
		},
	})

	require.ErrorIs(t, err, config.ErrConfigFileRead)
	assert.False(t, buildCalled)
}

func TestSetupNotifyHandlers_ErrorOnly_NoSuccessHandler(t *testing.T) {
	cfg := newConfigWithAllowedHost(testAllowedHost)

	sink, err := setupNotifyHandlers("", config.Secret(testErrorURL), cfg, "run-1", false)
	require.NoError(t, err)
	impl := requireNotificationSinkImpl(t, sink)
	require.Len(t, impl.handlers, 1)
	assert.Equal(t, notify.LevelModeWarnAndAbove, impl.handlers[0].LevelMode())
}

func TestSetupNotifyHandlers_Phase2_SlackAdded(t *testing.T) {
	cfg := newConfigWithAllowedHost(testAllowedHost)

	sink, err := setupNotifyHandlers(config.Secret(testSuccessURL), config.Secret(testErrorURL), cfg, "run-2", false)
	require.NoError(t, err)
	impl := requireNotificationSinkImpl(t, sink)
	require.Len(t, impl.handlers, 2)

	gotModes := []notify.LevelMode{impl.handlers[0].LevelMode(), impl.handlers[1].LevelMode()}
	assert.ElementsMatch(t, []notify.LevelMode{notify.LevelModeExactInfo, notify.LevelModeWarnAndAbove}, gotModes)
}

func TestSetupNotifyHandlers_AllowedHostPropagation(t *testing.T) {
	cfg := newConfigWithAllowedHost("other.host.com")

	_, err := setupNotifyHandlers(config.Secret(testSuccessURL), config.Secret(testErrorURL), cfg, "run-3", false)
	require.Error(t, err)

	var validationErr *notify.WebhookValidationError
	require.ErrorAs(t, err, &validationErr)
}

func TestSetupNotifyHandlers_AllOrNothingValidation(t *testing.T) {
	cfg := newConfigWithAllowedHost(testAllowedHost)

	_, err := setupNotifyHandlers(
		config.Secret("https://hooks.slack.com/services/s"),
		config.Secret("http://hooks.slack.com/services/e"),
		cfg,
		"run-4",
		false,
	)
	require.Error(t, err)

	var validationErr *notify.WebhookValidationError
	require.ErrorAs(t, err, &validationErr)
}

func TestSetupNotifyHandlers_DryRunFlag(t *testing.T) {
	cfg := newConfigWithAllowedHost(testAllowedHost)

	sink, err := setupNotifyHandlers(config.Secret(testSuccessURL), config.Secret(testErrorURL), cfg, "run-5", true)
	require.NoError(t, err)
	assert.True(t, sink.IsDryRun())
}

func TestSetupNotifyHandlers_DryRunNoURLs(t *testing.T) {
	cfg := newConfigWithAllowedHost("")

	sink, err := setupNotifyHandlers("", "", cfg, "run-6", true)
	require.NoError(t, err)
	impl := requireNotificationSinkImpl(t, sink)
	require.Len(t, impl.handlers, 2)

	for _, h := range impl.handlers {
		assert.True(t, h.IsDryRun())
	}
}

func TestSetupNotifyHandlers_RequiresWebhookURLOutsideDryRun(t *testing.T) {
	cfg := newConfigWithAllowedHost(testAllowedHost)

	_, err := setupNotifyHandlers("", "", cfg, "run-required", false)

	require.ErrorIs(t, err, errSlackWebhookURLRequired)
}

func TestBootstrap_SlackURLsSecretWrappedImmediately(t *testing.T) {
	var gotSuccess config.Secret
	var gotError config.Secret
	cfg := configForRoot(secureStoreRoot(t))
	_, err := Bootstrap(subcommandFetch, "config.toml", "run-secret", BootstrapOptions{
		LoadConfig: func(string) (*config.Config, error) { return cfg, nil },
		Getenv: func(key string) string {
			switch key {
			case "TLSRPT_SLACK_WEBHOOK_URL_SUCCESS":
				return testSuccessURL
			case "TLSRPT_SLACK_WEBHOOK_URL_ERROR":
				return testErrorURL
			default:
				return ""
			}
		},
		BuildNotifier: func(successURL, errorURL config.Secret, _ *config.Config, _ string, _ bool) (NotificationSink, error) {
			gotSuccess = successURL
			gotError = errorURL
			return &SpyNotificationSink{}, nil
		},
		OpenStore: func(string, store.IMAPIdentity, store.OpenMode) (store.Store, error) {
			return storetestutil.NewFakeStore(), nil
		},
	})
	require.NoError(t, err)
	assert.Equal(t, config.Secret(testSuccessURL), gotSuccess)
	assert.Equal(t, config.Secret(testErrorURL), gotError)
	assert.Equal(t, "[REDACTED]", gotSuccess.String())
	assert.Equal(t, "[REDACTED]", gotError.String())
}

func TestBootstrap_NonFetchSubcommandsDoNotReadIMAPCredentials(t *testing.T) {
	for _, subcmd := range []SubcommandName{subcommandGC, subcommandRecover, subcommandReprocess} {
		t.Run(string(subcmd), func(t *testing.T) {
			getenvKeys := make([]string, 0)
			_, err := Bootstrap(subcmd, "config.toml", "run-no-imap", BootstrapOptions{
				LoadConfig: func(string) (*config.Config, error) { return configForRoot(secureStoreRoot(t)), nil },
				Getenv: func(key string) string {
					getenvKeys = append(getenvKeys, key)
					return ""
				},
				BuildNotifier: func(config.Secret, config.Secret, *config.Config, string, bool) (NotificationSink, error) {
					return &SpyNotificationSink{}, nil
				},
				OpenStore: func(string, store.IMAPIdentity, store.OpenMode) (store.Store, error) {
					return storetestutil.NewFakeStore(), nil
				},
			})
			require.NoError(t, err)
			assert.NotContains(t, getenvKeys, "TLSRPT_IMAP_USERNAME")
			assert.NotContains(t, getenvKeys, "TLSRPT_IMAP_PASSWORD")
		})
	}
}

func TestBootstrap_Summary_ExistingStore(t *testing.T) {
	fakeStore := storetestutil.NewFakeStore()
	buildCalled := false
	rootDir := secureStoreRoot(t)

	boot, err := Bootstrap(subcommandSummary, "config.toml", "run-summary-existing", BootstrapOptions{
		LoadConfig: func(string) (*config.Config, error) { return configForRoot(rootDir), nil },
		BuildNotifier: func(config.Secret, config.Secret, *config.Config, string, bool) (NotificationSink, error) {
			buildCalled = true
			return &SpyNotificationSink{}, nil
		},
		OpenStore: func(string, store.IMAPIdentity, store.OpenMode) (store.Store, error) {
			return fakeStore, nil
		},
	})
	require.NoError(t, err)
	defer func() { require.NoError(t, boot.Close()) }()

	assert.False(t, buildCalled, "summary bootstrap must not build notifier")
	assert.NotNil(t, boot.Store)
	assert.NotNil(t, boot.SummaryGuard)
	assert.Nil(t, boot.LockHandle, "summary bootstrap must not acquire writer lock")
}

func TestBootstrap_Summary_GuardAcquireFailure(t *testing.T) {
	guardErr := errors.New("flock: permission denied")
	fakeStore := storetestutil.NewFakeStore()
	fakeStore.AcquireSummaryConsistencyGuardErr = guardErr

	_, err := Bootstrap(subcommandSummary, "config.toml", "run-summary-guard-fail", BootstrapOptions{
		LoadConfig: func(string) (*config.Config, error) { return configForRoot(secureStoreRoot(t)), nil },
		OpenStore: func(string, store.IMAPIdentity, store.OpenMode) (store.Store, error) {
			return fakeStore, nil
		},
	})
	require.ErrorIs(t, err, guardErr)
}

func TestBootstrap_SummaryMissingStoreSkipsNotifier(t *testing.T) {
	buildCalled := false
	rootDir := filepath.Join(t.TempDir(), "missing")
	boot, err := Bootstrap(subcommandSummary, "config.toml", "run-summary", BootstrapOptions{
		LoadConfig: func(string) (*config.Config, error) { return configForRoot(rootDir), nil },
		BuildNotifier: func(config.Secret, config.Secret, *config.Config, string, bool) (NotificationSink, error) {
			buildCalled = true
			return &SpyNotificationSink{}, nil
		},
	})
	require.NoError(t, err)
	defer func() { require.NoError(t, boot.Close()) }()

	assert.False(t, buildCalled)
	assert.NotNil(t, boot.Store)
	assert.NotNil(t, boot.SummaryGuard)
	_, statErr := os.Stat(rootDir)
	assert.True(t, errors.Is(statErr, os.ErrNotExist))
}

func TestBootstrap_RejectsSymlinkRootDirBeforeNotifier(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "real")
	require.NoError(t, os.Mkdir(target, 0o700))
	link := filepath.Join(dir, "link")
	require.NoError(t, os.Symlink(target, link))
	buildCalled := false

	_, err := Bootstrap(subcommandFetch, "config.toml", "run-symlink", BootstrapOptions{
		LoadConfig: func(string) (*config.Config, error) { return configForRoot(link), nil },
		BuildNotifier: func(config.Secret, config.Secret, *config.Config, string, bool) (NotificationSink, error) {
			buildCalled = true
			return &SpyNotificationSink{}, nil
		},
	})

	require.ErrorIs(t, err, errRootDirSymlink)
	assert.False(t, buildCalled)
}

func TestBootstrap_RejectsRootDirWithoutOwnerWrite(t *testing.T) {
	rootDir := filepath.Join(t.TempDir(), "store")
	require.NoError(t, os.Mkdir(rootDir, 0o500))
	buildCalled := false

	_, err := Bootstrap(subcommandFetch, "config.toml", "run-perm", BootstrapOptions{
		LoadConfig: func(string) (*config.Config, error) { return configForRoot(rootDir), nil },
		BuildNotifier: func(config.Secret, config.Secret, *config.Config, string, bool) (NotificationSink, error) {
			buildCalled = true
			return &SpyNotificationSink{}, nil
		},
	})

	require.ErrorIs(t, err, errRootDirPermission)
	assert.False(t, buildCalled)
}

func TestBootstrap_LockFailureNotifiesAndFlushes(t *testing.T) {
	spy := &SpyNotificationSink{}
	_, err := Bootstrap(subcommandFetch, "config.toml", "run-lock", BootstrapOptions{
		LoadConfig: func(string) (*config.Config, error) { return configForRoot(secureStoreRoot(t)), nil },
		BuildNotifier: func(config.Secret, config.Secret, *config.Config, string, bool) (NotificationSink, error) {
			return spy, nil
		},
		AcquireWriterLock: func(string) (LockHandle, error) {
			return nil, storelock.ErrLockHeld
		},
	})

	require.ErrorIs(t, err, storelock.ErrLockHeld)
	require.Len(t, spy.SystemErrors, 1)
	assert.Equal(t, notify.SystemErrorKindLockHeld, spy.SystemErrors[0].Kind)
	assert.Equal(t, 1, spy.FlushCount)
}

func TestBootstrap_HoldsLockUntilClose(t *testing.T) {
	rootDir := filepath.Join(t.TempDir(), "store")
	boot, err := Bootstrap(subcommandFetch, "config.toml", "run-lock-hold", BootstrapOptions{
		LoadConfig: func(string) (*config.Config, error) { return configForRoot(rootDir), nil },
		BuildNotifier: func(config.Secret, config.Secret, *config.Config, string, bool) (NotificationSink, error) {
			return &SpyNotificationSink{}, nil
		},
		OpenStore: func(string, store.IMAPIdentity, store.OpenMode) (store.Store, error) {
			return storetestutil.NewFakeStore(), nil
		},
	})
	require.NoError(t, err)

	second, err := AcquireExclusive(storelock.LockPath(rootDir))
	assert.Nil(t, second)
	assert.ErrorIs(t, err, storelock.ErrLockHeld)

	require.NoError(t, boot.Close())
	second, err = AcquireExclusive(storelock.LockPath(rootDir))
	require.NoError(t, err)
	require.NoError(t, second.Close())
}

func TestBootstrap_StoreOpenFailureClassifiesSystemErrors(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want notify.SystemErrorKind
	}{
		{
			name: "identity mismatch",
			err: &store.ErrStoreIdentityMismatch{
				ExpectedHost: "imap.example.com", ExpectedPort: 993, ExpectedMailbox: "INBOX",
				ActualHost: "other.example.com", ActualPort: 993, ActualMailbox: "INBOX",
			},
			want: notify.SystemErrorKindStoreIdentityMismatch,
		},
		{name: "permission", err: fmt.Errorf("wrapped: %w", os.ErrPermission), want: notify.SystemErrorKindStorePermission},
		{name: "corruption", err: errors.New("bad json"), want: notify.SystemErrorKindStoreCorruption},
		{name: "pending reset", err: store.ErrPendingReset, want: notify.SystemErrorKindResetIncomplete},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spy := &SpyNotificationSink{}
			_, err := Bootstrap(subcommandFetch, "config.toml", "run-open-fail", BootstrapOptions{
				LoadConfig: func(string) (*config.Config, error) { return configForRoot(secureStoreRoot(t)), nil },
				BuildNotifier: func(config.Secret, config.Secret, *config.Config, string, bool) (NotificationSink, error) {
					return spy, nil
				},
				OpenStore: func(string, store.IMAPIdentity, store.OpenMode) (store.Store, error) {
					return nil, tt.err
				},
			})
			require.Error(t, err)
			require.Len(t, spy.SystemErrors, 1)
			assert.Equal(t, tt.want, spy.SystemErrors[0].Kind)
			assert.Equal(t, 1, spy.FlushCount)
		})
	}
}

func TestBootstrap_PendingResetAdvice(t *testing.T) {
	spy := &SpyNotificationSink{}
	_, err := Bootstrap(subcommandGC, "config.toml", "run-pending", BootstrapOptions{
		LoadConfig: func(string) (*config.Config, error) { return configForRoot(secureStoreRoot(t)), nil },
		BuildNotifier: func(config.Secret, config.Secret, *config.Config, string, bool) (NotificationSink, error) {
			return spy, nil
		},
		OpenStore: func(string, store.IMAPIdentity, store.OpenMode) (store.Store, error) {
			return nil, store.ErrPendingReset
		},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "recover --mode discard-old --yes")
	assert.Contains(t, err.Error(), "recover --abort-reset --yes")
	require.Len(t, spy.SystemErrors, 1)
	assert.Equal(t, notify.SystemErrorKindResetIncomplete, spy.SystemErrors[0].Kind)
	assert.Equal(t, 1, spy.FlushCount)
}

func TestBuildIMAPConfig(t *testing.T) {
	cfg := &config.Config{
		IMAP: config.IMAPConfig{
			Host:            "imap.example.com",
			Port:            993,
			Mailbox:         "INBOX",
			TLSCACert:       "/etc/ssl/cert.pem",
			MaxMessageBytes: 1024,
		},
	}

	got := buildIMAPConfig(cfg, IMAPCredentials{Username: "testuser", Password: config.Secret("testpass")})

	assert.Equal(t, "imap.example.com", got.Host)
	assert.Equal(t, 993, got.Port)
	assert.Equal(t, "INBOX", got.Mailbox)
	assert.Equal(t, "/etc/ssl/cert.pem", got.TLSCACert)
	assert.Equal(t, int64(1024), got.MaxMessageBytes)
	assert.Equal(t, "testuser", got.Username)
	assert.Equal(t, config.Secret("testpass"), got.Password)
}

func TestLoadConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	require.NoError(t, os.WriteFile(path, []byte(`[imap]
host = "imap.example.com"
port = 993

[notify.slack]
allowed_host = "hooks.slack.com"
`), 0o600))

	cfg, err := loadConfig(path, slog.Default())
	require.NoError(t, err)
	assert.Equal(t, "hooks.slack.com", cfg.Notify.Slack.AllowedHost)

	_, err = loadConfig("", slog.Default())
	assert.True(t, errors.Is(err, config.ErrConfigPathEmpty))

	_, err = loadConfig("/nonexistent/path/config.toml", slog.Default())
	assert.True(t, errors.Is(err, config.ErrConfigFileRead))
}

func requireNotificationSinkImpl(t *testing.T, sink NotificationSink) *notificationSink {
	t.Helper()
	impl, ok := sink.(*notificationSink)
	require.True(t, ok)
	return impl
}
