//go:build !linux

// Package store provides persistent storage for TLSRPT reports and emails.
package store

import (
	"fmt"
	"time"
)

// ctimeOf is not supported on non-Linux platforms. LoadEmails treats this as a
// per-file I/O error and skips the file rather than fabricating a timestamp.
func ctimeOf(path string) (time.Time, error) {
	return time.Time{}, fmt.Errorf("ctimeOf: ctime retrieval not supported on this platform: %s", path)
}
