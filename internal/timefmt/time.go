// Package timefmt formats user-facing timestamps. Keep the thresholds in sync
// with desktop/frontend/src/lib/time.ts; durations and countdowns are separate.
package timefmt

import (
	"fmt"
	"time"
)

// Short shows one relative unit, then a calendar date after a week.
// An unknown timestamp has no label; future timestamps clamp to now.
func Short(at, now time.Time) string {
	if at.IsZero() {
		return ""
	}
	d := now.Sub(at)
	switch {
	case d < time.Minute:
		return "now"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	case d < 7*24*time.Hour:
		return fmt.Sprintf("%dd", int(d.Hours())/24)
	}
	at, now = at.Local(), now.Local()
	if at.Year() != now.Year() {
		return at.Format("Jan 2, 2006")
	}
	return at.Format("Jan 2")
}

// Phrase adds ago only to relative units, never to calendar dates.
func Phrase(at, now time.Time) string {
	s := Short(at, now)
	if s == "now" {
		return "just now"
	}
	if s != "" && now.Sub(at) < 7*24*time.Hour {
		return s + " ago"
	}
	return s
}
