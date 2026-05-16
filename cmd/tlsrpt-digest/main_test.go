package main

import (
	"errors"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/isseis/tlsrpt-digest/internal/config"
	"github.com/isseis/tlsrpt-digest/internal/notify"
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
