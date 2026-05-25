package main

import (
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseDuration(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want Duration
	}{
		{name: "one day", in: "1d", want: Duration{Days: 1}},
		{name: "seven days", in: "7d", want: Duration{Days: 7}},
		{name: "one week", in: "1w", want: Duration{Days: 7}},
		{name: "four weeks", in: "4w", want: Duration{Days: 28}},
		{name: "thirty days", in: "30d", want: Duration{Days: 30}},
		{name: "largest non-overflowing week", in: fmt.Sprintf("%dw", math.MaxInt/7), want: Duration{Days: (math.MaxInt / 7) * 7}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseDuration(tt.in)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestParseDurationErrors(t *testing.T) {
	for _, in := range []string{"0d", "-1d", "-2w", "30h", "abc", ""} {
		t.Run(in, func(t *testing.T) {
			got, err := ParseDuration(in)
			assert.Equal(t, Duration{}, got)
			require.Error(t, err)
		})
	}
}

func TestParseDurationRejectsWeekOverflow(t *testing.T) {
	got, err := ParseDuration(fmt.Sprintf("%dw", math.MaxInt/7+1))

	assert.Equal(t, Duration{}, got)
	require.ErrorIs(t, err, errDurationOverflow)
}

func TestDurationCutoffUsesUTCDayStart(t *testing.T) {
	now := time.Date(2026, 5, 25, 2, 1, 0, 0, time.UTC)

	got := Duration{Days: 7}.Cutoff(now)

	assert.Equal(t, time.Date(2026, 5, 18, 0, 0, 0, 0, time.UTC), got)
	assert.NotEqual(t, now.AddDate(0, 0, -7), got)
}

func TestDurationCutoffNormalizesWeeksToUTCDays(t *testing.T) {
	now := time.Date(2026, 5, 25, 23, 59, 59, 123, time.FixedZone("JST", 9*60*60))

	got := Duration{Days: 7}.Cutoff(now)

	assert.Equal(t, time.Date(2026, 5, 18, 0, 0, 0, 0, time.UTC), got)
}

func TestUTCDayStart(t *testing.T) {
	now := time.Date(2026, 5, 25, 18, 30, 45, 123, time.FixedZone("JST", 9*60*60))

	got := UTCDayStart(now)

	assert.Equal(t, time.Date(2026, 5, 25, 0, 0, 0, 0, time.UTC), got)
}

func TestSummaryWeeklyWindowHasNoOverlapAtUTCDayBoundaries(t *testing.T) {
	window, err := ParseDuration("1w")
	require.NoError(t, err)
	now := time.Date(2000, 12, 10, 10, 0, 0, 0, time.UTC)

	start := window.Cutoff(now)
	end := UTCDayStart(now)

	assert.Equal(t, time.Date(2000, 12, 3, 0, 0, 0, 0, time.UTC), start)
	assert.Equal(t, time.Date(2000, 12, 10, 0, 0, 0, 0, time.UTC), end)
}
