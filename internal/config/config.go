// Package config provides shared configuration types and TOML loading.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// Sentinel errors for allowed_host format validation.
var (
	ErrAllowedHostWhitespace = errors.New("notify.slack.allowed_host must not have leading/trailing whitespace")
	ErrAllowedHostScheme     = errors.New("notify.slack.allowed_host must not contain a scheme")
	ErrAllowedHostPort       = errors.New("notify.slack.allowed_host must not contain a port number")
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

// validateAllowedHost returns an error if host is non-empty but has an invalid
// format (scheme prefix, port suffix, or surrounding whitespace).
func validateAllowedHost(host string) error {
	if host == "" {
		return nil
	}
	if host != strings.TrimSpace(host) {
		return fmt.Errorf("config: %w: %q", ErrAllowedHostWhitespace, host)
	}
	if strings.Contains(host, "://") {
		return fmt.Errorf("config: %w: %q", ErrAllowedHostScheme, host)
	}
	// Reject port numbers by checking that the value parses as a pure hostname.
	// Wrapping in "//" allows url.Parse to treat it as a host.
	u, err := url.Parse("//" + host)
	if err != nil || u.Host != host {
		return fmt.Errorf("config: %w: %q", ErrAllowedHostPort, host)
	}
	if u.Hostname() != host {
		// Host contains a port (u.Hostname() strips it).
		return fmt.Errorf("config: %w: %q", ErrAllowedHostPort, host)
	}
	return nil
}
