package store

import (
	"time"

	"github.com/sushidev-team/lola/hubproto"
)

// StatsDay returns the org's counters for now's UTC day, creating the entry
// when it is the first event of the day. Days older than hubproto.StatsDays
// are dropped on the way, so the list never outgrows the window the stats
// view serves. The pointer is valid until the next StatsDay call.
func (d *Data) StatsDay(org string, now time.Time) *hubproto.StatsDay {
	if d.Stats == nil {
		d.Stats = map[string][]hubproto.StatsDay{}
	}
	date := now.UTC().Format(hubproto.StatsDateLayout)
	oldest := StatsOldest(now)
	days := d.Stats[org]
	keep := days[:0:0]
	for _, s := range days {
		if s.Date >= oldest {
			keep = append(keep, s)
		}
	}
	if n := len(keep); n > 0 && keep[n-1].Date == date {
		d.Stats[org] = keep
		return &keep[n-1]
	}
	// A clock that stepped back must not reorder the list: count into the
	// newest day instead of inserting before it.
	if n := len(keep); n > 0 && keep[n-1].Date > date {
		d.Stats[org] = keep
		return &keep[n-1]
	}
	keep = append(keep, hubproto.StatsDay{Date: date})
	d.Stats[org] = keep
	return &keep[len(keep)-1]
}

// StatsOldest is the oldest date (hubproto.StatsDateLayout) inside the stats
// window ending on now's UTC day.
func StatsOldest(now time.Time) string {
	return now.UTC().AddDate(0, 0, -(hubproto.StatsDays - 1)).Format(hubproto.StatsDateLayout)
}
