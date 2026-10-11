package timefmt

import (
	"testing"
	"time"
)

func TestTimestampLabels(t *testing.T) {
	now := time.Date(2026, 10, 11, 12, 0, 0, 0, time.Local)
	for _, tc := range []struct {
		at            time.Time
		short, phrase string
	}{
		{time.Time{}, "", ""},
		{now.Add(time.Minute), "now", "just now"},
		{now.Add(-59 * time.Second), "now", "just now"},
		{now.Add(-time.Minute), "1m", "1m ago"},
		{now.Add(-59 * time.Minute), "59m", "59m ago"},
		{now.Add(-time.Hour), "1h", "1h ago"},
		{now.Add(-5*time.Hour - 4*time.Minute), "5h", "5h ago"},
		{now.Add(-24 * time.Hour), "1d", "1d ago"},
		{now.Add(-6 * 24 * time.Hour), "6d", "6d ago"},
		{now.Add(-7 * 24 * time.Hour), "Oct 4", "Oct 4"},
		{time.Date(2025, 3, 4, 12, 0, 0, 0, time.Local), "Mar 4, 2025", "Mar 4, 2025"},
	} {
		if got := Short(tc.at, now); got != tc.short {
			t.Errorf("Short(%v) = %q, want %q", tc.at, got, tc.short)
		}
		if got := Phrase(tc.at, now); got != tc.phrase {
			t.Errorf("Phrase(%v) = %q, want %q", tc.at, got, tc.phrase)
		}
	}
}
