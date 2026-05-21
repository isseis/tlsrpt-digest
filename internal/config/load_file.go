package config

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
)

// LoadFile reads a TOML configuration file at path, applies defaults,
// validates field values, and returns the resulting Config.
//
// If logger is nil, slog.Default() is used. Relative store.root_dir values
// are converted to absolute paths using the current working directory.
// Consistency issues emit WARN log entries but do not cause errors.
func LoadFile(path string, logger *slog.Logger) (*Config, error) {
	if logger == nil {
		logger = slog.Default()
	}

	if path == "" {
		return nil, fmt.Errorf("config: %w", ErrConfigPathEmpty)
	}

	data, err := os.ReadFile(path) //nolint:gosec // G304: path is an operator-supplied config flag
	if err != nil {
		return nil, fmt.Errorf("config: %w: %w", ErrConfigFileRead, err)
	}

	cfg, err := Load(data)
	if err != nil {
		return nil, err
	}

	abs, err := filepath.Abs(cfg.Store.RootDir)
	if err != nil {
		return nil, fmt.Errorf("config: %w: %w", ErrStoreRootDirResolve, err)
	}
	if abs != cfg.Store.RootDir {
		if !filepath.IsAbs(cfg.Store.RootDir) {
			logger.Info("store.root_dir converted to absolute path", "path", abs)
		}
		cfg.Store.RootDir = abs
	}

	if cfg.Store.RetentionDays > cfg.Store.MaxEmailAgeDays {
		logger.Warn("store.retention_days > store.max_email_age_days: .eml files will be deleted before report JSON, reprocess recovery may be incomplete")
	}

	if cfg.IMAP.FetchDays >= cfg.Store.RetentionDays {
		logger.Warn("imap.fetch_days >= store.retention_days: fetch window covers already-GC'd records, reprocessing may encounter missing reports")
	}

	return cfg, nil
}
