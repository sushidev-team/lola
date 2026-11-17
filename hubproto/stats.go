package hubproto

import (
	"strconv"
	"time"
)

// Stats: a READ-ONLY, device-facing view of the org's throughput (GET
// [PathStats]). It is counters only — no item, repo, device or member names —
// so it is the least sensitive view the hub serves. The daemon still decodes
// it with [DecodeStrictLimit] and drops the whole response if [StatsView.Validate]
// fails, like the board.

// Caps on the stats view.
const (
	// StatsDays is how many days the hub reports: the last 90, one entry per
	// day that saw activity (days with nothing may be omitted).
	StatsDays = 90
	// MaxStatsCount bounds one day's counter.
	MaxStatsCount = 1_000_000
	// MaxStatsFixSeconds bounds one day's summed fix time.
	MaxStatsFixSeconds = int64(1) << 40
	// StatsDateLayout is the layout of [StatsDay.Date] (UTC calendar day).
	StatsDateLayout = "2006-01-02"
)

// StatsDay counts one UTC day: items first opened, done, failed and escalated
// that day, and FixSecondsSum — the summed time from first seen to done of the
// items done that day (divide by Done for the mean).
type StatsDay struct {
	Date          string `json:"date"` // YYYY-MM-DD, UTC
	Opened        int    `json:"opened"`
	Done          int    `json:"done"`
	Failed        int    `json:"failed"`
	Escalated     int    `json:"escalated"`
	FixSecondsSum int64  `json:"fix_seconds_sum"`
}

// StatsView (GET /v1/stats) is the org's last [StatsDays] days, oldest first.
type StatsView struct {
	Days []StatsDay `json:"days"`
}

// Validate checks the list size, every day's date and counters, and that the
// dates strictly increase (oldest first, no repeats).
func (s StatsView) Validate() error {
	if len(s.Days) > StatsDays {
		return invalid("days", "too many")
	}
	var prev time.Time
	for i, d := range s.Days {
		field := "days[" + strconv.Itoa(i) + "]"
		t, err := time.Parse(StatsDateLayout, d.Date)
		if err != nil || t.Format(StatsDateLayout) != d.Date {
			return invalid(field+".date", "not YYYY-MM-DD")
		}
		if i > 0 && !t.After(prev) {
			return invalid(field+".date", "not after the previous day")
		}
		prev = t
		for _, c := range []struct {
			name string
			n    int
		}{{"opened", d.Opened}, {"done", d.Done}, {"failed", d.Failed}, {"escalated", d.Escalated}} {
			if c.n < 0 || c.n > MaxStatsCount {
				return invalid(field+"."+c.name, "out of range")
			}
		}
		if d.FixSecondsSum < 0 || d.FixSecondsSum > MaxStatsFixSeconds {
			return invalid(field+".fix_seconds_sum", "out of range")
		}
	}
	if n := len(s.Days); n > 1 {
		first, _ := time.Parse(StatsDateLayout, s.Days[0].Date)
		if prev.Sub(first) >= StatsDays*24*time.Hour {
			return invalid("days", "span over StatsDays")
		}
	}
	return nil
}
