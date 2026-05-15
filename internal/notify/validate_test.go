package notify_test

import (
	"errors"
	"testing"

	"github.com/isseis/tlsrpt-digest/internal/notify"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testAllowedHost = "hooks.slack.com"
	testSuccessURL  = "https://hooks.slack.com/services/success"
	testErrorURL    = "https://hooks.slack.com/services/error"
)

func TestValidateEnvCombination(t *testing.T) {
	tests := []struct {
		name       string
		successURL string
		errorURL   string
		wantErr    bool
	}{
		{"both set", testSuccessURL, testErrorURL, false},
		{"error only", "", testErrorURL, false},
		{"both empty", "", "", false},
		{"success only", testSuccessURL, "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := notify.ValidateEnvCombination(tt.successURL, tt.errorURL)
			if tt.wantErr {
				var ve *notify.WebhookValidationError
				require.True(t, errors.As(err, &ve))
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestValidateWebhookURL_SameURLAllowed(t *testing.T) {
	same := "https://hooks.slack.com/services/same"
	require.NoError(t, notify.ValidateWebhookURL(same, testAllowedHost))
}

func TestValidateWebhookURL_HTTPScheme(t *testing.T) {
	err := notify.ValidateWebhookURL("http://hooks.slack.com/services/xxx", testAllowedHost)
	var ve *notify.WebhookValidationError
	require.True(t, errors.As(err, &ve))
	assert.Contains(t, ve.Msg, "HTTPS")
}

func TestValidateWebhookURL_HostMismatch(t *testing.T) {
	err := notify.ValidateWebhookURL("https://evil.example.com/services/xxx", testAllowedHost)
	var ve *notify.WebhookValidationError
	require.True(t, errors.As(err, &ve))
	assert.Contains(t, ve.Msg, "does not match")
}

func TestValidateWebhookURL_BothURLsDifferentHost(t *testing.T) {
	// Both URLs must share the same hostname; tested via ValidateWebhookURL on
	// the second URL against the first URL's host. BuildHandlers enforces this
	// by calling validateBothURLs; full integration tested in handler_test.go.
	err := notify.ValidateWebhookURL("https://other.slack.com/services/error", testAllowedHost)
	var ve *notify.WebhookValidationError
	require.True(t, errors.As(err, &ve))
	assert.Contains(t, ve.Msg, "does not match")
}

func TestValidateWebhookURL_PortStripped(t *testing.T) {
	err := notify.ValidateWebhookURL("https://hooks.slack.com:443/services/xxx", testAllowedHost)
	require.NoError(t, err)
}

func TestValidateWebhookURL_CaseInsensitive(t *testing.T) {
	err := notify.ValidateWebhookURL("https://HOOKS.SLACK.COM/services/xxx", testAllowedHost)
	require.NoError(t, err)
}

func TestValidateWebhookURL_NoAllowedHost(t *testing.T) {
	err := notify.ValidateWebhookURL("https://hooks.slack.com/services/xxx", "")
	var ve *notify.WebhookValidationError
	require.True(t, errors.As(err, &ve))
	assert.Contains(t, ve.Msg, "allowed_host")
}

func TestValidateWebhookURL_BothEmpty(t *testing.T) {
	// Both URLs empty → ValidateEnvCombination returns nil (Slack disabled).
	require.NoError(t, notify.ValidateEnvCombination("", ""))
}
