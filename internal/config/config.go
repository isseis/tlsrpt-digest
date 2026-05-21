// Package config provides shared configuration types and TOML loading.
package config

import (
	"bytes"
	"fmt"
	"regexp"

	"github.com/pelletier/go-toml/v2"
)

// reValidHostname matches a valid RFC 1123 hostname: one or more dot-separated
// labels, each of 1–63 characters consisting of ASCII letters, digits, and
// hyphens, not starting or ending with a hyphen.
var reValidHostname = regexp.MustCompile(
	`^[a-zA-Z0-9]([a-zA-Z0-9\-]{0,61}[a-zA-Z0-9])?` +
		`(\.[a-zA-Z0-9]([a-zA-Z0-9\-]{0,61}[a-zA-Z0-9])?)*$`,
)

// Load reads TOML from data, decodes it into Config with strict unknown-key
// rejection, and validates field values.
func Load(data []byte) (*Config, error) {
	var raw struct {
		Notify rawNotifyConfig `toml:"notify"`
	}
	dec := toml.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&raw); err != nil {
		return nil, fmt.Errorf("config: %w: %w", ErrConfigDecode, err)
	}
	cfg := Config{
		Notify: NotifyConfig{
			Slack: NotifySlackConfig{
				AllowedHost: stringValue(raw.Notify.Slack.AllowedHost),
			},
		},
	}
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

// validate checks semantic constraints that cannot be expressed via struct tags.
func (c *Config) validate() error {
	return validateAllowedHost(c.Notify.Slack.AllowedHost)
}

// validateAllowedHost returns an error if host is non-empty but does not match
// the RFC 1123 hostname pattern. Empty string is allowed (Slack disabled).
func validateAllowedHost(host string) error {
	if host == "" {
		return nil
	}
	if !reValidHostname.MatchString(host) {
		return fmt.Errorf("config: %w: %q", ErrInvalidAllowedHost, host)
	}
	return nil
}
