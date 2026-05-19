//go:build linux

// Package store provides persistent storage for TLSRPT reports and emails.
package store

import (
	"fmt"
	"syscall"
	"time"
)

// ctimeOf returns the inode change time (ctime) of the file at path via
// syscall.Stat. Returns an error if the syscall fails; callers treat failure
// as a file I/O error rather than substituting an artificial timestamp.
func ctimeOf(path string) (time.Time, error) {
	var stat syscall.Stat_t
	if err := syscall.Stat(path, &stat); err != nil {
		return time.Time{}, fmt.Errorf("ctimeOf: stat %s: %w", path, err)
	}
	return time.Unix(stat.Ctim.Sec, stat.Ctim.Nsec).UTC(), nil
}
