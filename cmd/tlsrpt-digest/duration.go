package main

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

var (
	errDurationEmpty           = errors.New("duration is empty")
	errDurationUnsupportedUnit = errors.New("duration unit is unsupported")
	errDurationNonPositive     = errors.New("duration must be >= 1 day")
)

// Duration represents a relative period normalized to whole days.
type Duration struct {
	Days int
}

// ParseDuration parses CLI duration values using day or week units.
func ParseDuration(s string) (Duration, error) {
	if s == "" {
		return Duration{}, errDurationEmpty
	}

	unit := s[len(s)-1]
	if unit != 'd' && unit != 'w' {
		return Duration{}, fmt.Errorf("%w: %q", errDurationUnsupportedUnit, unit)
	}

	value, err := strconv.Atoi(strings.TrimSpace(s[:len(s)-1]))
	if err != nil {
		return Duration{}, fmt.Errorf("duration: parse %q: %w", s, err)
	}
	if value <= 0 {
		return Duration{}, fmt.Errorf("%w: %q", errDurationNonPositive, s)
	}

	days := value
	if unit == 'w' {
		days *= 7
	}
	return Duration{Days: days}, nil
}

// Cutoff returns the UTC day boundary that is d.Days before now.
func (d Duration) Cutoff(now time.Time) time.Time {
	return UTCDayStart(now).AddDate(0, 0, -d.Days)
}

// UTCDayStart returns 00:00:00 UTC for the UTC date containing now.
func UTCDayStart(now time.Time) time.Time {
	utc := now.UTC()
	return time.Date(utc.Year(), utc.Month(), utc.Day(), 0, 0, 0, 0, time.UTC)
}
