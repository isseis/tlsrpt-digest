//go:build test

package main

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const slackNotifyWebhookEnvKey = "TLSRPT_SLACK_WEBHOOK_URL_ERROR"

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
}
