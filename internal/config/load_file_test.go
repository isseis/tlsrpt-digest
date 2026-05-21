package config_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/isseis/tlsrpt-digest/internal/config"
)

const validTOML = `
[imap]
host = "imap.example.com"
port = 993
mailbox = "INBOX"
fetch_days = 7
tls_ca_cert = ""
max_message_bytes = 0

[notify]
[notify.slack]
allowed_host = ""

[store]
root_dir = "/tmp/store"
retention_days = 30
max_email_age_days = 30

[summary]
window_days = 7
`

func writeTempConfig(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	return path
}

func TestLoadFile_EmptyPath(t *testing.T) {
	_, err := config.LoadFile("", nil)
	assert.True(t, errors.Is(err, config.ErrConfigPathEmpty))
}

func TestLoadFile_NonexistentPath(t *testing.T) {
	_, err := config.LoadFile("/nonexistent/path/config.toml", nil)
	assert.True(t, errors.Is(err, config.ErrConfigFileRead))
}

func TestLoadFile_NilLogger(t *testing.T) {
	path := writeTempConfig(t, validTOML)
	cfg, err := config.LoadFile(path, nil)
	require.NoError(t, err)
	assert.NotNil(t, cfg)
}

func TestLoadFile_ValidTOML(t *testing.T) {
	path := writeTempConfig(t, validTOML)
	cfg, err := config.LoadFile(path, nil)
	require.NoError(t, err)
	assert.Equal(t, "imap.example.com", cfg.IMAP.Host)
	assert.Equal(t, 993, cfg.IMAP.Port)
	assert.Equal(t, "INBOX", cfg.IMAP.Mailbox)
	assert.Equal(t, 7, cfg.IMAP.FetchDays)
	assert.Equal(t, 30, cfg.Store.RetentionDays)
	assert.Equal(t, 30, cfg.Store.MaxEmailAgeDays)
	assert.Equal(t, 7, cfg.Summary.WindowDays)
}

func TestLoadFile_ConsistencyWarning_RetentionGtEmailAge(t *testing.T) {
	toml := `
[imap]
host = "imap.example.com"
port = 993

[store]
retention_days = 60
max_email_age_days = 30
`
	path := writeTempConfig(t, toml)
	logger, buf := newCapturingLogger()
	cfg, err := config.LoadFile(path, logger)
	require.NoError(t, err)
	assert.NotNil(t, cfg)
	assert.True(t, strings.Contains(buf.String(), "retention_days > store.max_email_age_days"),
		"expected WARN about retention_days > max_email_age_days, got: %s", buf.String())
}

func TestLoadFile_ConsistencyWarning_FetchDaysGteRetentionDays(t *testing.T) {
	toml := `
[imap]
host = "imap.example.com"
port = 993
fetch_days = 30

[store]
retention_days = 14
`
	path := writeTempConfig(t, toml)
	logger, buf := newCapturingLogger()
	cfg, err := config.LoadFile(path, logger)
	require.NoError(t, err)
	assert.NotNil(t, cfg)
	assert.True(t, strings.Contains(buf.String(), "fetch_days >= store.retention_days"),
		"expected WARN about fetch_days >= retention_days, got: %s", buf.String())
}

func TestLoadFile_RelativeRootDir_Absolutized(t *testing.T) {
	toml := `
[imap]
host = "imap.example.com"
port = 993

[store]
root_dir = "./data"
`
	path := writeTempConfig(t, toml)
	logger, buf := newCapturingLogger()
	cfg, err := config.LoadFile(path, logger)
	require.NoError(t, err)
	assert.True(t, filepath.IsAbs(cfg.Store.RootDir),
		"expected absolute path, got: %s", cfg.Store.RootDir)
	assert.True(t, strings.Contains(buf.String(), "store.root_dir converted to absolute path"),
		"expected INFO log about absolutization, got: %s", buf.String())
}

func TestLoadFile_AbsoluteRootDir_Unchanged(t *testing.T) {
	toml := `
[imap]
host = "imap.example.com"
port = 993

[store]
root_dir = "/absolute/store"
`
	path := writeTempConfig(t, toml)
	logger, buf := newCapturingLogger()
	cfg, err := config.LoadFile(path, logger)
	require.NoError(t, err)
	assert.Equal(t, "/absolute/store", cfg.Store.RootDir)
	assert.False(t, strings.Contains(buf.String(), "store.root_dir converted to absolute path"),
		"expected no INFO log for absolute path, got: %s", buf.String())
}
