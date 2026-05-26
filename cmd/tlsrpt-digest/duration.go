package main

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"time"
)

var (
	errDurationEmpty           = errors.New("duration is empty")
	errDurationUnsupportedUnit = errors.New("duration unit is unsupported")
	errDurationNonPositive     = errors.New("duration must be >= 1 day")
	errDurationOverflow        = errors.New("duration exceeds supported range")
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

	value, err := strconv.Atoi(s[:len(s)-1])
	if err != nil {
		return Duration{}, fmt.Errorf("duration: parse %q: %w", s, err)
	}
	if value <= 0 {
		return Duration{}, fmt.Errorf("%w: %q", errDurationNonPositive, s)
	}

	days := value
	if unit == 'w' {
		if value > math.MaxInt/7 {
			return Duration{}, fmt.Errorf("%w: %q", errDurationOverflow, s)
		}
		days *= 7
	}
	return Duration{Days: days}, nil
}

// Cutoff returns the UTC day boundary that is d.Days before now.
func (d Duration) Cutoff(now time.Time) time.Time {
	return UTCDayStart(now).AddDate(0, 0, -d.Days)
}

// durationFlag is a flag.Value adapter that parses into a *Duration field.
// A nil pointer target indicates the flag was not provided.
type durationFlag struct {
	ptr **Duration
}

// newDurationFlag returns a durationFlag that writes the parsed Duration into *ptr.
func newDurationFlag(ptr **Duration) durationFlag {
	return durationFlag{ptr: ptr}
}

func (f durationFlag) String() string {
	if *f.ptr == nil {
		return ""
	}
	return fmt.Sprintf("%dd", (*f.ptr).Days)
}

func (f durationFlag) Set(s string) error {
	d, err := ParseDuration(s)
	if err != nil {
		return err
	}
	*f.ptr = &d
	return nil
}

// UTCDayStart returns 00:00:00 UTC for the UTC date containing now.
func UTCDayStart(now time.Time) time.Time {
	utc := now.UTC()
	return time.Date(utc.Year(), utc.Month(), utc.Day(), 0, 0, 0, 0, time.UTC)
}
