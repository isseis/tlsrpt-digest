package config

// Config is the top-level application configuration after defaults and
// validation have been applied.
type Config struct {
	IMAP    IMAPConfig
	Notify  NotifyConfig
	Store   StoreConfig
	Summary SummaryConfig
}

// IMAPConfig holds IMAP connection and fetch settings.
type IMAPConfig struct {
	Host            string
	Port            int
	Mailbox         string
	FetchDays       int
	TLSCACert       string
	MaxMessageBytes int64
	// RetentionDays is the IMAP message retention period in days.
	// 0 disables IMAP deletion (opt-in, default).
	RetentionDays int
}

// NotifyConfig holds notification-related configuration.
type NotifyConfig struct {
	Slack NotifySlackConfig
}

// NotifySlackConfig holds Slack notification configuration.
type NotifySlackConfig struct {
	AllowedHost string
}

// StoreConfig holds local storage settings.
type StoreConfig struct {
	RootDir         string
	RetentionDays   int
	MaxEmailAgeDays int
}

// SummaryConfig holds periodic summary settings.
type SummaryConfig struct {
	WindowDays int
}

type rawConfig struct {
	IMAP    rawIMAPConfig    `toml:"imap"`
	Notify  rawNotifyConfig  `toml:"notify"`
	Store   rawStoreConfig   `toml:"store"`
	Summary rawSummaryConfig `toml:"summary"`
}

type rawIMAPConfig struct {
	Host            *string `toml:"host"`
	Port            *int    `toml:"port"`
	Mailbox         *string `toml:"mailbox"`
	FetchDays       *int    `toml:"fetch_days"`
	TLSCACert       *string `toml:"tls_ca_cert"`
	MaxMessageBytes *int64  `toml:"max_message_bytes"`
	RetentionDays   *int    `toml:"retention_days"`
}

type rawNotifyConfig struct {
	Slack rawNotifySlackConfig `toml:"slack"`
}

type rawNotifySlackConfig struct {
	AllowedHost *string `toml:"allowed_host"`
}

type rawStoreConfig struct {
	RootDir         *string `toml:"root_dir"`
	RetentionDays   *int    `toml:"retention_days"`
	MaxEmailAgeDays *int    `toml:"max_email_age_days"`
}

type rawSummaryConfig struct {
	WindowDays *int `toml:"window_days"`
}
