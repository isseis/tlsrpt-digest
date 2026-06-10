package config_test

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/isseis/tlsrpt-digest/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const baseConfigTOML = `[imap]
host = "imap.example.com"
port = 993
`

func TestLoad_ValidAllowedHost(t *testing.T) {
	data := []byte(baseConfigTOML + `[notify.slack]
allowed_host = "hooks.slack.com"
`)
	cfg, err := config.Load(data)
	require.NoError(t, err)
	assert.Equal(t, "hooks.slack.com", cfg.Notify.Slack.AllowedHost)
}

func TestLoad_EmptyAllowedHost(t *testing.T) {
	data := []byte(baseConfigTOML + `[notify.slack]
allowed_host = ""
`)
	cfg, err := config.Load(data)
	require.NoError(t, err)
	assert.Equal(t, "", cfg.Notify.Slack.AllowedHost)
}

func TestLoad_MissingNotifySection(t *testing.T) {
	cfg, err := config.Load([]byte(baseConfigTOML))
	require.NoError(t, err)
	assert.Equal(t, "", cfg.Notify.Slack.AllowedHost)
}

func TestNotifySlackConfig_UnknownKey(t *testing.T) {
	// Unknown key in notify.slack must be rejected (strict decode).
	data := []byte(baseConfigTOML + `[notify.slack]
webhook_url = "https://hooks.slack.com/services/xxx"
`)
	_, err := config.Load(data)
	require.Error(t, err, "unknown key webhook_url should cause a decode error")
	assert.True(t, errors.Is(err, config.ErrConfigDecode))
}

func TestNotifySlackConfig_AllowedHostValidation(t *testing.T) {
	tests := []struct {
		name    string
		host    string
		wantErr bool
	}{
		{"valid hostname", "hooks.slack.com", false},
		{"empty (Slack disabled)", "", false},
		{"with scheme", "https://hooks.slack.com", true},
		{"with port", "hooks.slack.com:443", true},
		{"leading space", " hooks.slack.com", true},
		{"trailing space", "hooks.slack.com ", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data := []byte(baseConfigTOML + "[notify.slack]\nallowed_host = \"" + tt.host + "\"\n")
			_, err := config.Load(data)
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestNotifySlackConfig_AllowedHostSecretNotInError(t *testing.T) {
	data := []byte(baseConfigTOML + `[notify.slack]
allowed_host = "https://hooks.slack.com/services/SECRET"
`)
	_, err := config.Load(data)
	require.Error(t, err)
	assert.True(t, errors.Is(err, config.ErrInvalidAllowedHost))
	assert.NotContains(t, err.Error(), "https://hooks.slack.com/services/SECRET")
	assert.NotContains(t, err.Error(), "SECRET")
}

func TestLoad_AllFields(t *testing.T) {
	data := []byte(`[imap]
host = "imap.example.com"
port = 143
mailbox = "tls-reports"
fetch_days = 3
tls_ca_cert = ""
max_message_bytes = 12345
retention_days = 30

[notify.slack]
allowed_host = "hooks.slack.com"

[store]
root_dir = "/var/lib/tlsrpt"
retention_days = 12
max_email_age_days = 9

[summary]
window_days = 5
`)
	cfg, err := config.Load(data)
	require.NoError(t, err)

	assert.Equal(t, "imap.example.com", cfg.IMAP.Host)
	assert.Equal(t, 143, cfg.IMAP.Port)
	assert.Equal(t, "tls-reports", cfg.IMAP.Mailbox)
	assert.Equal(t, 3, cfg.IMAP.FetchDays)
	assert.Equal(t, "", cfg.IMAP.TLSCACert)
	assert.Equal(t, int64(12345), cfg.IMAP.MaxMessageBytes)
	assert.Equal(t, 30, cfg.IMAP.RetentionDays)
	assert.Equal(t, "hooks.slack.com", cfg.Notify.Slack.AllowedHost)
	assert.Equal(t, "/var/lib/tlsrpt", cfg.Store.RootDir)
	assert.Equal(t, 12, cfg.Store.RetentionDays)
	assert.Equal(t, 9, cfg.Store.MaxEmailAgeDays)
	assert.Equal(t, 5, cfg.Summary.WindowDays)
}

func TestLoad_TOMLSyntaxError(t *testing.T) {
	_, err := config.Load([]byte(`[imap`))
	require.Error(t, err)
	assert.True(t, errors.Is(err, config.ErrConfigDecode))
}

func TestLoad_UnknownTopLevelKey(t *testing.T) {
	data := []byte(`unknown = true
` + baseConfigTOML)
	_, err := config.Load(data)
	require.Error(t, err)
	assert.True(t, errors.Is(err, config.ErrConfigDecode))
}

func TestLoad_DefaultMaxMessageBytes1MiB(t *testing.T) {
	cfg, err := config.Load([]byte(baseConfigTOML))
	require.NoError(t, err)
	assert.Equal(t, int64(1<<20), cfg.IMAP.MaxMessageBytes)
}

func TestLoad_ExplicitZeroMaxMessageBytesUnlimited(t *testing.T) {
	data := []byte(`[imap]
host = "imap.example.com"
port = 993
max_message_bytes = 0
`)
	cfg, err := config.Load(data)
	require.NoError(t, err)
	assert.Equal(t, int64(0), cfg.IMAP.MaxMessageBytes)
}

func TestLoad_IMAPHostValidation(t *testing.T) {
	tests := []struct {
		name string
		data string
	}{
		{
			name: "empty",
			data: `[imap]
host = ""
port = 993
`,
		},
		{
			name: "missing",
			data: `[imap]
port = 993
`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := config.Load([]byte(tt.data))
			require.Error(t, err)
			assert.True(t, errors.Is(err, config.ErrInvalidIMAPHost))
		})
	}
}

func TestLoad_IMAPPortValidation(t *testing.T) {
	errorCases := []struct {
		name string
		data string
	}{
		{
			name: "zero",
			data: `[imap]
host = "imap.example.com"
port = 0
`,
		},
		{
			name: "negative",
			data: `[imap]
host = "imap.example.com"
port = -1
`,
		},
		{
			name: "too high",
			data: `[imap]
host = "imap.example.com"
port = 65536
`,
		},
		{
			name: "missing",
			data: `[imap]
host = "imap.example.com"
`,
		},
	}

	for _, tt := range errorCases {
		t.Run(tt.name, func(t *testing.T) {
			_, err := config.Load([]byte(tt.data))
			require.Error(t, err)
			assert.True(t, errors.Is(err, config.ErrInvalidIMAPPort))
		})
	}

	for _, port := range []int{1, 443, 65535} {
		t.Run("valid", func(t *testing.T) {
			data := []byte(fmt.Sprintf(`[imap]
host = "imap.example.com"
port = %d
`, port))
			_, err := config.Load(data)
			require.NoError(t, err)
		})
	}
}

func TestLoad_IMAPPasswordInTOML(t *testing.T) {
	data := []byte(baseConfigTOML + `password = "secret-value"
`)
	_, err := config.Load(data)
	require.Error(t, err)
	assert.True(t, errors.Is(err, config.ErrConfigDecode))
	assert.NotContains(t, err.Error(), "secret-value")
}

func TestLoad_FetchDaysValidation(t *testing.T) {
	tests := []struct {
		name    string
		value   int
		wantErr bool
	}{
		{name: "zero", value: 0, wantErr: true},
		{name: "negative", value: -1, wantErr: true},
		{name: "one", value: 1, wantErr: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data := []byte(fmt.Sprintf(`[imap]
host = "imap.example.com"
port = 993
fetch_days = %d
`, tt.value))
			_, err := config.Load(data)
			if tt.wantErr {
				require.Error(t, err)
				assert.True(t, errors.Is(err, config.ErrInvalidFetchDays))
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestLoad_TLSCACert(t *testing.T) {
	dir := t.TempDir()
	invalidPEM := filepath.Join(dir, "invalid.pem")
	validPEM := filepath.Join(dir, "valid.pem")
	require.NoError(t, os.WriteFile(invalidPEM, []byte("not pem"), 0o600))
	require.NoError(t, os.WriteFile(validPEM, []byte(testCertificatePEM), 0o600))

	tests := []struct {
		name    string
		path    string
		wantErr error
	}{
		{name: "missing file", path: filepath.Join(dir, "missing.pem"), wantErr: config.ErrTLSCACertNotReadable},
		{name: "invalid pem", path: invalidPEM, wantErr: config.ErrTLSCACertNotPEM},
		{name: "valid pem", path: validPEM, wantErr: nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data := []byte(fmt.Sprintf(`[imap]
host = "imap.example.com"
port = 993
tls_ca_cert = %q
`, tt.path))
			_, err := config.Load(data)
			if tt.wantErr != nil {
				require.Error(t, err)
				assert.True(t, errors.Is(err, tt.wantErr))
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestLoad_DaysValidation(t *testing.T) {
	tests := []struct {
		name   string
		table  string
		key    string
		err    error
		values []int
	}{
		{name: "window days", table: "summary", key: "window_days", err: config.ErrInvalidWindowDays, values: []int{0, -1}},
		{name: "retention days", table: "store", key: "retention_days", err: config.ErrInvalidRetentionDays, values: []int{0, -1}},
		{name: "max email age days", table: "store", key: "max_email_age_days", err: config.ErrInvalidMaxEmailAgeDays, values: []int{0, -1}},
	}

	for _, tt := range tests {
		for _, value := range tt.values {
			t.Run(tt.name, func(t *testing.T) {
				data := []byte(baseConfigTOML + fmt.Sprintf(`
[%s]
%s = %d
`, tt.table, tt.key, value))
				_, err := config.Load(data)
				require.Error(t, err)
				assert.True(t, errors.Is(err, tt.err))
			})
		}
		t.Run(tt.name+" lower bound", func(t *testing.T) {
			data := []byte(baseConfigTOML + fmt.Sprintf(`
[%s]
%s = 1
`, tt.table, tt.key))
			_, err := config.Load(data)
			require.NoError(t, err)
		})
	}
}

func TestLoad_IMAPRetentionDaysValidation(t *testing.T) {
	tests := []struct {
		name    string
		value   int
		wantErr error
	}{
		{name: "negative", value: -1, wantErr: config.ErrInvalidIMAPRetentionDays},
		{name: "zero disables", value: 0, wantErr: nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data := []byte(baseConfigTOML + fmt.Sprintf("retention_days = %d\n", tt.value))
			_, err := config.Load(data)
			if tt.wantErr != nil {
				require.Error(t, err)
				assert.True(t, errors.Is(err, tt.wantErr))
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestLoad_IMAPRetentionTooShort(t *testing.T) {
	tests := []struct {
		name           string
		extraIMAP      string
		summarySection string
		retentionDays  int
		wantErr        error
	}{
		{
			name:          "default fetch_days and window_days, below max",
			retentionDays: 13,
			wantErr:       config.ErrIMAPRetentionTooShort,
		},
		{
			name:          "default fetch_days and window_days, equal to max",
			retentionDays: 14,
			wantErr:       nil,
		},
		{
			name:           "window_days dominates, below max",
			extraIMAP:      "fetch_days = 5\n",
			summarySection: "[summary]\nwindow_days = 20\n",
			retentionDays:  19,
			wantErr:        config.ErrIMAPRetentionTooShort,
		},
		{
			name:           "window_days dominates, equal to max",
			extraIMAP:      "fetch_days = 5\n",
			summarySection: "[summary]\nwindow_days = 20\n",
			retentionDays:  20,
			wantErr:        nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data := []byte(baseConfigTOML + tt.extraIMAP + fmt.Sprintf("retention_days = %d\n", tt.retentionDays) + tt.summarySection)
			_, err := config.Load(data)
			if tt.wantErr != nil {
				require.Error(t, err)
				assert.True(t, errors.Is(err, tt.wantErr))
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestLoad_MaxMessageBytesValidation(t *testing.T) {
	data := []byte(`[imap]
host = "imap.example.com"
port = 993
max_message_bytes = -1
`)
	_, err := config.Load(data)
	require.Error(t, err)
	assert.True(t, errors.Is(err, config.ErrInvalidMaxMessageBytes))
}

func TestLoad_Default_MailboxINBOX(t *testing.T) {
	tests := []struct {
		name string
		toml string
	}{
		{"absent", baseConfigTOML},
		{"empty string", baseConfigTOML + "mailbox = \"\"\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := config.Load([]byte(tt.toml))
			require.NoError(t, err)
			assert.Equal(t, "INBOX", cfg.IMAP.Mailbox)
		})
	}
}

func TestLoad_Default_FetchDays14(t *testing.T) {
	cfg, err := config.Load([]byte(baseConfigTOML))
	require.NoError(t, err)
	assert.Equal(t, 14, cfg.IMAP.FetchDays)
}

func TestLoad_Default_IMAPRetentionDays0(t *testing.T) {
	cfg, err := config.Load([]byte(baseConfigTOML))
	require.NoError(t, err)
	assert.Equal(t, 0, cfg.IMAP.RetentionDays)
}

func TestLoad_Default_StoreRootDir(t *testing.T) {
	tests := []struct {
		name string
		toml string
	}{
		{"absent", baseConfigTOML},
		{"empty string", baseConfigTOML + "[store]\nroot_dir = \"\"\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := config.Load([]byte(tt.toml))
			require.NoError(t, err)
			assert.Equal(t, "./store", cfg.Store.RootDir)
		})
	}
}

func TestLoad_Default_TLSCACertEmpty(t *testing.T) {
	// Key assertion: absent tls_ca_cert must not trigger validation errors.
	cfg, err := config.Load([]byte(baseConfigTOML))
	require.NoError(t, err)
	assert.Equal(t, "", cfg.IMAP.TLSCACert)
}

func TestLoad_Default_WindowDays7(t *testing.T) {
	cfg, err := config.Load([]byte(baseConfigTOML))
	require.NoError(t, err)
	assert.Equal(t, 7, cfg.Summary.WindowDays)
}

func TestLoad_Default_RetentionDays30(t *testing.T) {
	cfg, err := config.Load([]byte(baseConfigTOML))
	require.NoError(t, err)
	assert.Equal(t, 30, cfg.Store.RetentionDays)
}

func TestLoad_Default_MaxEmailAgeDays30(t *testing.T) {
	cfg, err := config.Load([]byte(baseConfigTOML))
	require.NoError(t, err)
	assert.Equal(t, 30, cfg.Store.MaxEmailAgeDays)
}

const testCertificatePEM = `-----BEGIN CERTIFICATE-----
MIICCDCCAXGgAwIBAgIUNWu49yb0AKVxDsP02RBRe9jokjUwDQYJKoZIhvcNAQEL
BQAwFjEUMBIGA1UEAwwLZXhhbXBsZS5jb20wHhcNMjYwNTIxMTI1MzIwWhcNMjYw
NTIyMTI1MzIwWjAWMRQwEgYDVQQDDAtleGFtcGxlLmNvbTCBnzANBgkqhkiG9w0B
AQEFAAOBjQAwgYkCgYEA40WtAbWylBOUB1wbBTqe6jAC6T9q6ma+jsKAvqEqBdJx
lh3CvBugucfT2DRjL5wF/wEL64FVv2MxEy9wk8n/AJEwWeJzeclsiNbAqKH+DtA9
WSeWVI3XsJ6Xkhz5dPdVkHVjRH2JteM0fy8GgZc+e9DZo1eib/m8umIfzvrPLmkC
AwEAAaNTMFEwHQYDVR0OBBYEFNConafP4P1vhMHzYqc+m7JrzLBKMB8GA1UdIwQY
MBaAFNConafP4P1vhMHzYqc+m7JrzLBKMA8GA1UdEwEB/wQFMAMBAf8wDQYJKoZI
hvcNAQELBQADgYEAjz6LsQH4krhPWq39oW2AU9i38tFyZnapjwIvCuMbUdezYuoL
TlA8RmJ5D34oIy4KnCpCydjfQJ6WTx/VlHLDCgxX13tNF7pfa2JOqrQ39ozQ2ZmW
oxia/v4guS/xFA4MAbGTJsIQetT5iE6sDiwQK1tceTNO+WjnzENjUqCgvB4=
-----END CERTIFICATE-----
`
