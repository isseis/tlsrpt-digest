package notify

import (
	"fmt"
	"net/url"
	"strings"
)

// ValidateEnvCombination checks whether the combination of successURL and
// errorURL is valid.
//   - success-only: returns WebhookValidationError (error notifications would be missed)
//   - both empty: returns nil (Slack disabled)
//   - error-only or both set: returns nil (valid)
func ValidateEnvCombination(successURL, errorURL string) error {
	if successURL != "" && errorURL == "" {
		return &WebhookValidationError{
			Msg: "TLSRPT_SLACK_WEBHOOK_URL_SUCCESS is set but TLSRPT_SLACK_WEBHOOK_URL_ERROR is not; " +
				"error notifications must be enabled to prevent silent failures",
		}
	}
	return nil
}

// validateWebhookURL verifies that webhookURL uses HTTPS and that its hostname
// matches allowedHost (case-insensitive, port-stripped comparison).
func validateWebhookURL(webhookURL, allowedHost string) error {
	if webhookURL == "" {
		return &WebhookValidationError{Msg: "webhook URL must not be empty"}
	}
	u, err := url.Parse(webhookURL)
	if err != nil {
		return &WebhookValidationError{Msg: fmt.Sprintf("invalid webhook URL: %v", err)}
	}
	if u.Scheme != "https" {
		return &WebhookValidationError{
			Msg: fmt.Sprintf("webhook URL must use HTTPS scheme, got %q", u.Scheme),
		}
	}
	if u.Host == "" {
		return &WebhookValidationError{Msg: "webhook URL must have a host"}
	}
	if allowedHost == "" {
		return &WebhookValidationError{Msg: "notify.slack.allowed_host is not configured"}
	}
	hostname := strings.ToLower(u.Hostname())
	if hostname != strings.ToLower(allowedHost) {
		return &WebhookValidationError{
			Msg: fmt.Sprintf("webhook URL host %q does not match allowed_host %q", hostname, allowedHost),
		}
	}
	return nil
}

// validateBothURLs checks that successURL and errorURL use the same hostname.
// It is called by BuildHandlers after each individual URL passes validation.
func validateBothURLs(successURL, errorURL, allowedHost string) error {
	if err := validateWebhookURL(successURL, allowedHost); err != nil {
		return err
	}
	if err := validateWebhookURL(errorURL, allowedHost); err != nil {
		return err
	}
	su, _ := url.Parse(successURL)
	eu, _ := url.Parse(errorURL)
	if !strings.EqualFold(su.Hostname(), eu.Hostname()) {
		return &WebhookValidationError{
			Msg: fmt.Sprintf(
				"success and error webhook URLs must use the same host; got %q and %q",
				su.Hostname(), eu.Hostname(),
			),
		}
	}
	return nil
}
