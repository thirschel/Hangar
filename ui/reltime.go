package ui

import (
	"fmt"
	"time"
)

// humanizeSince renders the elapsed time between t and now as a compact, muted
// relative label for the sidebar (e.g. "5m", "2h", "3d"). It is dependency-free
// and deterministic for a given (t, now) pair so callers can pass an explicit now
// in tests.
//
// It returns "" for the zero time so callers can omit the suffix entirely.
// Durations are clamped at zero, so clock skew that places t in the future renders
// as "now" rather than a negative value. Sub-minute durations render as "now",
// then minutes ("Nm"), hours ("Nh"), and finally days ("Nd").
func humanizeSince(t, now time.Time) string {
	if t.IsZero() {
		return ""
	}
	d := now.Sub(t)
	if d < 0 {
		d = 0 // clamp future/negative durations to "now"
	}
	switch {
	case d < time.Minute:
		return "now"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}
