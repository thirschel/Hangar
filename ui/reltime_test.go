package ui

import (
	"testing"
	"time"
)

func TestHumanizeSince(t *testing.T) {
	// Fixed reference point so the table is deterministic.
	now := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name string
		when time.Time
		want string
	}{
		{"zero time omits suffix", time.Time{}, ""},
		{"exactly now", now, "now"},
		{"seconds rounds to now", now.Add(-30 * time.Second), "now"},
		{"just under a minute is now", now.Add(-59 * time.Second), "now"},
		{"one minute", now.Add(-1 * time.Minute), "1m"},
		{"several minutes", now.Add(-5 * time.Minute), "5m"},
		{"just under an hour", now.Add(-59 * time.Minute), "59m"},
		{"one hour", now.Add(-1 * time.Hour), "1h"},
		{"several hours", now.Add(-3 * time.Hour), "3h"},
		{"just under a day", now.Add(-23 * time.Hour), "23h"},
		{"one day", now.Add(-24 * time.Hour), "1d"},
		{"several days", now.Add(-3 * 24 * time.Hour), "3d"},
		{"future time clamps to now", now.Add(5 * time.Minute), "now"},
		{"far future clamps to now", now.Add(48 * time.Hour), "now"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := humanizeSince(tt.when, now); got != tt.want {
				t.Errorf("humanizeSince(%v, %v) = %q, want %q", tt.when, now, got, tt.want)
			}
		})
	}
}
