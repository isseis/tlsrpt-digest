package notify

import (
	"errors"
	"testing"

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
			err := ValidateEnvCombination(tt.successURL, tt.errorURL)
			if tt.wantErr {
				_, ok := errors.AsType[*WebhookValidationError](err)
				require.True(t, ok)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestValidateWebhookURL_SameURLAllowed(t *testing.T) {
	same := "https://hooks.slack.com/services/same"
	require.NoError(t, validateWebhookURL(same, testAllowedHost))
}

func TestValidateWebhookURL_HTTPScheme(t *testing.T) {
	err := validateWebhookURL("http://hooks.slack.com/services/xxx", testAllowedHost)
	ve, ok := errors.AsType[*WebhookValidationError](err)
	require.True(t, ok)
	assert.Contains(t, ve.Msg, "HTTPS")
}

func TestValidateWebhookURL_HostMismatch(t *testing.T) {
	err := validateWebhookURL("https://evil.example.com/services/xxx", testAllowedHost)
	ve, ok := errors.AsType[*WebhookValidationError](err)
	require.True(t, ok)
	assert.Contains(t, ve.Msg, "does not match")
}

func TestValidateWebhookURL_PortStripped(t *testing.T) {
	err := validateWebhookURL("https://hooks.slack.com:443/services/xxx", testAllowedHost)
	require.NoError(t, err)
}

func TestValidateWebhookURL_CaseInsensitive(t *testing.T) {
	err := validateWebhookURL("https://HOOKS.SLACK.COM/services/xxx", testAllowedHost)
	require.NoError(t, err)
}

func TestValidateWebhookURL_NoAllowedHost(t *testing.T) {
	err := validateWebhookURL("https://hooks.slack.com/services/xxx", "")
	ve, ok := errors.AsType[*WebhookValidationError](err)
	require.True(t, ok)
	assert.Contains(t, ve.Msg, "allowed_host")
}

func TestValidateWebhookURL_BothEmpty(t *testing.T) {
	require.NoError(t, ValidateEnvCombination("", ""))
}

// TestValidateBothURLs_HostMismatch verifies AC-23 end-to-end: when success and
// error URLs resolve to different hostnames, BuildHandlers returns a
// WebhookValidationError.
//
// Note that individual host validation (AC-22) already enforces this since
// both URLs must match allowed_host; this test guards the entry path used by
// the bootstrap code.
func TestValidateBothURLs_HostMismatch(t *testing.T) {
	_, err := BuildHandlers(
		"https://hooks.slack.com/services/success",
		"https://other.slack.com/services/error",
		testAllowedHost,
		SlackHandlerOptions{RunID: "test"},
	)
	require.Error(t, err)
	_, ok := errors.AsType[*WebhookValidationError](err)
	require.True(t, ok)
}

// TestValidateBothURLs_SameHostOK is the positive counterpart: two URLs that
// share the allowed host pass without error.
func TestValidateBothURLs_SameHostOK(t *testing.T) {
	require.NoError(t, validateBothURLs(testSuccessURL, testErrorURL, testAllowedHost))
}
