package config

const (
	defaultIMAPMailbox       = "INBOX"
	defaultIMAPFetchDays     = 14
	defaultMaxMessageBytes   = int64(1 << 20) // 1 MiB
	defaultStoreRootDir      = "./store"
	defaultSummaryWindowDays = 7
	defaultStoreRetention    = 30
	defaultMaxEmailAge       = 30
)

func applyDefaults(raw *rawConfig) Config {
	return Config{
		IMAP: IMAPConfig{
			Host:            stringValue(raw.IMAP.Host),
			Port:            intValue(raw.IMAP.Port),
			Mailbox:         stringDefault(raw.IMAP.Mailbox, defaultIMAPMailbox),
			FetchDays:       intDefault(raw.IMAP.FetchDays, defaultIMAPFetchDays),
			TLSCACert:       stringValue(raw.IMAP.TLSCACert),
			MaxMessageBytes: int64Default(raw.IMAP.MaxMessageBytes, defaultMaxMessageBytes),
		},
		Notify: NotifyConfig{
			Slack: NotifySlackConfig{
				AllowedHost: stringValue(raw.Notify.Slack.AllowedHost),
			},
		},
		Store: StoreConfig{
			RootDir:         stringDefault(raw.Store.RootDir, defaultStoreRootDir),
			RetentionDays:   intDefault(raw.Store.RetentionDays, defaultStoreRetention),
			MaxEmailAgeDays: intDefault(raw.Store.MaxEmailAgeDays, defaultMaxEmailAge),
		},
		Summary: SummaryConfig{
			WindowDays: intDefault(raw.Summary.WindowDays, defaultSummaryWindowDays),
		},
	}
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func stringDefault(value *string, fallback string) string {
	if value == nil || *value == "" {
		return fallback
	}
	return *value
}

func intValue(value *int) int {
	if value == nil {
		return 0
	}
	return *value
}

func intDefault(value *int, fallback int) int {
	if value == nil {
		return fallback
	}
	return *value
}

func int64Value(value *int64) int64 {
	if value == nil {
		return 0
	}
	return *value
}

func int64Default(value *int64, fallback int64) int64 {
	if value == nil {
		return fallback
	}
	return *value
}
