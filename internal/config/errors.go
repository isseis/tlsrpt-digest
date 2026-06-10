package config

import "errors"

// Configuration loading errors.
var (
	ErrConfigPathEmpty     = errors.New("path is empty")
	ErrConfigFileRead      = errors.New("cannot read file")
	ErrConfigDecode        = errors.New("cannot decode TOML")
	ErrStoreRootDirResolve = errors.New("cannot resolve store.root_dir to absolute path")
)

// Field validation errors.
var (
	ErrInvalidIMAPHost        = errors.New("imap.host is empty")
	ErrInvalidIMAPPort        = errors.New("imap.port out of range (1-65535)")
	ErrInvalidFetchDays       = errors.New("imap.fetch_days must be >= 1")
	ErrInvalidWindowDays      = errors.New("summary.window_days must be >= 1")
	ErrInvalidRetentionDays   = errors.New("store.retention_days must be >= 1")
	ErrInvalidMaxEmailAgeDays = errors.New("store.max_email_age_days must be >= 1")
	ErrInvalidAllowedHost     = errors.New("notify.slack.allowed_host must be a plain hostname without scheme, port, or whitespace")
	ErrTLSCACertNotReadable   = errors.New("imap.tls_ca_cert cannot be read")
	ErrTLSCACertNotPEM        = errors.New("imap.tls_ca_cert is not a PEM-encoded certificate")
	ErrInvalidMaxMessageBytes = errors.New("imap.max_message_bytes must be >= 0")

	// ErrInvalidIMAPRetentionDays and ErrIMAPRetentionTooShort validate
	// imap.retention_days, distinct from ErrInvalidRetentionDays which
	// validates store.retention_days (local report retention).
	ErrInvalidIMAPRetentionDays = errors.New("imap.retention_days must be >= 0")
	ErrIMAPRetentionTooShort    = errors.New("imap.retention_days must be >= max(imap.fetch_days, summary.window_days) when enabled")
)
