// Package config provides shared configuration types.
package config

import "log/slog"

const redactedSecret = "[REDACTED]"

// Secret stores sensitive string values and redacts them in logs.
type Secret string

// String always returns a redacted value.
func (s Secret) String() string {
	return redactedSecret
}

// LogValue returns a redacted slog value.
func (s Secret) LogValue() slog.Value {
	return slog.StringValue(redactedSecret)
}

// Value returns the raw secret string.
func (s Secret) Value() string {
	return string(s)
}
