package config

import (
	"crypto/x509"
	"fmt"
	"os"
	"regexp"
)

// reValidHostname matches a valid RFC 1123 hostname.
var reValidHostname = regexp.MustCompile(
	`^[a-zA-Z0-9]([a-zA-Z0-9\-]{0,61}[a-zA-Z0-9])?` +
		`(\.[a-zA-Z0-9]([a-zA-Z0-9\-]{0,61}[a-zA-Z0-9])?)*$`,
)

func validate(cfg *Config) error {
	if cfg.IMAP.Host == "" {
		return fmt.Errorf("config: %w", ErrInvalidIMAPHost)
	}
	if cfg.IMAP.Port < 1 || cfg.IMAP.Port > 65535 {
		return fmt.Errorf("config: %w: %d", ErrInvalidIMAPPort, cfg.IMAP.Port)
	}
	if cfg.IMAP.FetchDays < 1 {
		return fmt.Errorf("config: %w: %d", ErrInvalidFetchDays, cfg.IMAP.FetchDays)
	}
	if cfg.Summary.WindowDays < 1 {
		return fmt.Errorf("config: %w: %d", ErrInvalidWindowDays, cfg.Summary.WindowDays)
	}
	if cfg.Store.RetentionDays < 1 {
		return fmt.Errorf("config: %w: %d", ErrInvalidRetentionDays, cfg.Store.RetentionDays)
	}
	if cfg.Store.MaxEmailAgeDays < 1 {
		return fmt.Errorf("config: %w: %d", ErrInvalidMaxEmailAgeDays, cfg.Store.MaxEmailAgeDays)
	}
	if cfg.IMAP.MaxMessageBytes < 0 {
		return fmt.Errorf("config: %w: %d", ErrInvalidMaxMessageBytes, cfg.IMAP.MaxMessageBytes)
	}
	if cfg.IMAP.RetentionDays < 0 {
		return fmt.Errorf("config: %w: %d", ErrInvalidIMAPRetentionDays, cfg.IMAP.RetentionDays)
	}
	if cfg.IMAP.RetentionDays > 0 && cfg.IMAP.RetentionDays < max(cfg.IMAP.FetchDays, cfg.Summary.WindowDays) {
		return fmt.Errorf("config: %w: %d", ErrIMAPRetentionTooShort, cfg.IMAP.RetentionDays)
	}
	if err := validateTLSCACert(cfg.IMAP.TLSCACert); err != nil {
		return err
	}
	return validateAllowedHost(cfg.Notify.Slack.AllowedHost)
}

func validateTLSCACert(path string) error {
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path) // #nosec G304 -- tls_ca_cert is an explicit user-configured certificate path.
	if err != nil {
		return fmt.Errorf("config: %w: %s: %w", ErrTLSCACertNotReadable, path, err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(data) {
		return fmt.Errorf("config: %w: %s", ErrTLSCACertNotPEM, path)
	}
	return nil
}

// validateAllowedHost returns an error if host is non-empty but does not match
// the RFC 1123 hostname pattern. Empty string is allowed.
func validateAllowedHost(host string) error {
	if host == "" {
		return nil
	}
	if !reValidHostname.MatchString(host) {
		return fmt.Errorf("config: %w", ErrInvalidAllowedHost)
	}
	return nil
}
