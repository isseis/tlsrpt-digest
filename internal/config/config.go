// Package config provides shared configuration types and TOML loading.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"regexp"

	"github.com/pelletier/go-toml/v2"
)

// ErrInvalidAllowedHost is returned when notify.slack.allowed_host is not a
// plain RFC 1123 hostname (no scheme, port number, or surrounding whitespace).
var ErrInvalidAllowedHost = errors.New("notify.slack.allowed_host must be a plain hostname without scheme, port, or whitespace")

// reValidHostname matches a valid RFC 1123 hostname: one or more dot-separated
// labels, each of 1–63 characters consisting of ASCII letters, digits, and
// hyphens, not starting or ending with a hyphen.
var reValidHostname = regexp.MustCompile(
	`^[a-zA-Z0-9]([a-zA-Z0-9\-]{0,61}[a-zA-Z0-9])?` +
		`(\.[a-zA-Z0-9]([a-zA-Z0-9\-]{0,61}[a-zA-Z0-9])?)*$`,
)

// NotifySlackConfig holds the Slack notification configuration loaded from TOML.
// Webhook URLs are not stored here; they are read from environment variables.
type NotifySlackConfig struct {
	// AllowedHost is the permitted hostname for Slack webhook URLs (no port).
	// Empty string disables Slack notifications without error.
	AllowedHost string `toml:"allowed_host"`
}

// NotifyConfig holds notification-related configuration loaded from TOML.
type NotifyConfig struct {
	Slack NotifySlackConfig `toml:"slack"`
}

// Config is the top-level application configuration loaded from a TOML file.
type Config struct {
	Notify NotifyConfig `toml:"notify"`
}

// Load reads TOML from data, decodes it into Config with strict unknown-key
// rejection, and validates field values.
func Load(data []byte) (*Config, error) {
	var cfg Config
	dec := toml.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("config: decode failed: %w", err)
	}
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
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
