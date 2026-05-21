// Package config provides shared configuration types and TOML loading.
package config

import (
	"bytes"
	"fmt"

	"github.com/pelletier/go-toml/v2"
)

// Load reads TOML from data, decodes it into Config with strict unknown-key
// rejection, and validates field values.
func Load(data []byte) (*Config, error) {
	var raw rawConfig
	dec := toml.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&raw); err != nil {
		return nil, fmt.Errorf("config: %w: %w", ErrConfigDecode, err)
	}
	cfg := applyDefaults(&raw)
	if err := validate(&cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}
