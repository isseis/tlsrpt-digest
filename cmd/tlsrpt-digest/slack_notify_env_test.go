//go:build test

package main

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/isseis/tlsrpt-digest/internal/notify"
)

const slackNotifyWebhookEnvKey = notify.EnvSlackWebhookURLError

// missingSlackNotifyEnv returns the names of environment variables required
// for Slack notify integration tests that are missing or empty.
//
// When env is nil, values are read from the process environment. When env is
// non-nil, it replaces the process environment: keys absent from env are
// treated as missing even if they exist in os.Environ.
func missingSlackNotifyEnv(env map[string]string) []string {
	var val string
	if env == nil {
		val = os.Getenv(slackNotifyWebhookEnvKey)
	} else {
		val = env[slackNotifyWebhookEnvKey]
	}
	if val == "" {
		return []string{slackNotifyWebhookEnvKey + " (empty)"}
	}
	return nil
}

func TestSlackNotify_EnvRequirements(t *testing.T) {
	t.Run("webhook_url_missing", func(t *testing.T) {
		got := missingSlackNotifyEnv(map[string]string{})
		assert.Contains(t, got, slackNotifyWebhookEnvKey+" (empty)")
	})
	t.Run("webhook_url_empty_value", func(t *testing.T) {
		got := missingSlackNotifyEnv(map[string]string{slackNotifyWebhookEnvKey: ""})
		assert.Contains(t, got, slackNotifyWebhookEnvKey+" (empty)")
	})
	t.Run("webhook_url_set", func(t *testing.T) {
		env := map[string]string{slackNotifyWebhookEnvKey: "https://hooks.slack.com/services/test"}
		got := missingSlackNotifyEnv(env)
		require.Empty(t, got)
	})
	t.Run("nil_env_fallback_present", func(t *testing.T) {
		t.Setenv(slackNotifyWebhookEnvKey, "https://hooks.slack.com/services/test")
		got := missingSlackNotifyEnv(nil)
		assert.Empty(t, got)
	})
	t.Run("nil_env_fallback_missing", func(t *testing.T) {
		t.Setenv(slackNotifyWebhookEnvKey, "")
		got := missingSlackNotifyEnv(nil)
		assert.Contains(t, got, slackNotifyWebhookEnvKey+" (empty)")
	})
}

// missingSlackSummaryEnv returns the names of environment variables required
// for Slack summary integration tests that are missing or empty.
//
// When env is nil, values are read from the process environment. When env is
// non-nil, it replaces the process environment: keys absent from env are
// treated as missing even if they exist in os.Environ.
func missingSlackSummaryEnv(env map[string]string) []string {
	keys := []string{notify.EnvSlackWebhookURLSuccess, slackNotifyWebhookEnvKey}
	var missing []string
	for _, key := range keys {
		var val string
		if env == nil {
			val = os.Getenv(key)
		} else {
			val = env[key]
		}
		if val == "" {
			missing = append(missing, key+" (empty)")
		}
	}
	return missing
}

func TestSlackSummary_EnvRequirements(t *testing.T) {
	t.Run("webhook_url_missing", func(t *testing.T) {
		got := missingSlackSummaryEnv(map[string]string{})
		assert.Contains(t, got, notify.EnvSlackWebhookURLSuccess+" (empty)")
	})
	t.Run("webhook_url_empty_value", func(t *testing.T) {
		got := missingSlackSummaryEnv(map[string]string{notify.EnvSlackWebhookURLSuccess: ""})
		assert.Contains(t, got, notify.EnvSlackWebhookURLSuccess+" (empty)")
	})
	t.Run("error_webhook_url_missing", func(t *testing.T) {
		env := map[string]string{notify.EnvSlackWebhookURLSuccess: "https://hooks.slack.com/services/test"}
		got := missingSlackSummaryEnv(env)
		assert.Contains(t, got, slackNotifyWebhookEnvKey+" (empty)")
	})
	t.Run("webhook_url_set", func(t *testing.T) {
		env := map[string]string{
			notify.EnvSlackWebhookURLSuccess: "https://hooks.slack.com/services/test",
			slackNotifyWebhookEnvKey:         "https://hooks.slack.com/services/test",
		}
		got := missingSlackSummaryEnv(env)
		require.Empty(t, got)
	})
	t.Run("nil_env_fallback_present", func(t *testing.T) {
		t.Setenv(notify.EnvSlackWebhookURLSuccess, "https://hooks.slack.com/services/test")
		t.Setenv(slackNotifyWebhookEnvKey, "https://hooks.slack.com/services/test")
		got := missingSlackSummaryEnv(nil)
		assert.Empty(t, got)
	})
	t.Run("nil_env_fallback_missing", func(t *testing.T) {
		t.Setenv(notify.EnvSlackWebhookURLSuccess, "")
		t.Setenv(slackNotifyWebhookEnvKey, "")
		got := missingSlackSummaryEnv(nil)
		assert.Contains(t, got, notify.EnvSlackWebhookURLSuccess+" (empty)")
		assert.Contains(t, got, slackNotifyWebhookEnvKey+" (empty)")
	})
}
