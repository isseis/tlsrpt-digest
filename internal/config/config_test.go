package config_test

import (
	"errors"
	"testing"

	"github.com/isseis/tlsrpt-digest/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoad_ValidAllowedHost(t *testing.T) {
	data := []byte(`[notify.slack]
allowed_host = "hooks.slack.com"
`)
	cfg, err := config.Load(data)
	require.NoError(t, err)
	assert.Equal(t, "hooks.slack.com", cfg.Notify.Slack.AllowedHost)
}

func TestLoad_EmptyAllowedHost(t *testing.T) {
	data := []byte(`[notify.slack]
allowed_host = ""
`)
	cfg, err := config.Load(data)
	require.NoError(t, err)
	assert.Equal(t, "", cfg.Notify.Slack.AllowedHost)
}

func TestLoad_MissingNotifySection(t *testing.T) {
	cfg, err := config.Load([]byte(``))
	require.NoError(t, err)
	assert.Equal(t, "", cfg.Notify.Slack.AllowedHost)
}

func TestNotifySlackConfig_UnknownKey(t *testing.T) {
	// Unknown key in notify.slack must be rejected (strict decode).
	data := []byte(`[notify.slack]
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
			data := []byte("[notify.slack]\nallowed_host = \"" + tt.host + "\"\n")
			_, err := config.Load(data)
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}
