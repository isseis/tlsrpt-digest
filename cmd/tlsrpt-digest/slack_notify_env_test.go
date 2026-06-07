//go:build test

package main

import (
	"os"
	"slices"
	"testing"
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
		if !slices.Contains(got, slackNotifyWebhookEnvKey+" (empty)") {
			t.Errorf("expected %q in missing list, got %v", slackNotifyWebhookEnvKey+" (empty)", got)
		}
	})
	t.Run("webhook_url_set", func(t *testing.T) {
		env := map[string]string{slackNotifyWebhookEnvKey: "https://hooks.slack.com/services/test"}
		got := missingSlackNotifyEnv(env)
		if len(got) != 0 {
			t.Errorf("expected empty missing list, got %v", got)
		}
	})
}
