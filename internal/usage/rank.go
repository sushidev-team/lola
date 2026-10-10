package usage

import (
	"sort"
	"time"
)

// How heavy is a session? A token count alone does not say: 40M is a quiet
// afternoon for one repository and a runaway for another. So a session is
// ranked against the user's OWN finished sessions (the ledger's KeepDays of
// history), which needs no tuning and follows how they actually work. Ranking
// is by list-price weight (CostUSD), not raw tokens: a subscription limit is
// spent faster by output and by bigger models, and barely by cache reads.
// Until there is enough history, fixed thresholds stand in.

// MinHistory is how many finished sessions a ranking needs before it trusts
// percentiles over the fixed fallback.
const MinHistory = 10

// Levels are the four steps of the size glyph.
const (
	LevelLight  = iota // below the median
	LevelNormal        // p50–p75
	LevelHeavy         // p75–p90
	LevelTop           // above p90
)

// fallbackLevels are the list-price weights (USD) separating the levels while
// there is too little history: roughly a small fix, a feature, a big feature.
var fallbackLevels = [3]float64{3, 10, 25}

// Burn-rate floors, in list-price USD per active hour. burnFloor keeps a quiet
// history from flagging an ordinary session; burnFallback is the threshold
// while there is too little history to know what "fast" means here.
const (
	burnFloor    = 5.0
	burnFallback = 20.0
	// burnMinSlots is how much active time a finished session needs before
	// its average rate means anything.
	burnMinSlots = 3
)

// BurnWindow is the look-back a current burn rate is measured over.
const BurnWindow = 30 * time.Minute

// Rank is where one session sits among the history.
type Rank struct {
	Level int
	// Percentile is the share of history lighter than this session, 0..100.
	// Meaningless when Of is 0 (the fallback thresholds decided Level).
	Percentile float64
	// Of is the history size the percentile is over; 0 = fallback.
	Of int
}

// RankAmong places weight among history (other sessions' weights).
func RankAmong(weight float64, history []float64) Rank {
	if len(history) < MinHistory {
		lvl := LevelLight
		for _, b := range fallbackLevels {
			if weight >= b {
				lvl++
			}
		}
		return Rank{Level: lvl}
	}
	below := 0
	for _, h := range history {
		if h < weight {
			below++
		}
	}
	p := 100 * float64(below) / float64(len(history))
	r := Rank{Percentile: p, Of: len(history)}
	switch {
	case p >= 90:
		r.Level = LevelTop
	case p >= 75:
		r.Level = LevelHeavy
	case p >= 50:
		r.Level = LevelNormal
	}
	return r
}

// HourlyRate is t's weight per ACTIVE hour; 0 when it was active too briefly
// for an average to mean anything.
func HourlyRate(t Totals) float64 {
	if t.Slots < burnMinSlots {
		return 0
	}
	return t.CostUSD / (float64(t.Slots) * SlotDuration.Hours())
}

// BurnThreshold is the hourly weight above which a session counts as burning:
// the 90th percentile of the history's active-hour rates (never below
// burnFloor), or burnFallback while there is too little history.
func BurnThreshold(rates []float64) float64 {
	if len(rates) < MinHistory {
		return burnFallback
	}
	s := append([]float64(nil), rates...)
	sort.Float64s(s)
	return max(s[(len(s)*9)/10], burnFloor)
}

// BySource folds the ledger across days into one total per source, with the
// project of its latest entry.
func (l *Ledger) BySource() map[string]Entry {
	out := map[string]Entry{}
	for _, day := range l.Days {
		for src, e := range day {
			cur := out[src]
			cur.Totals.Add(e.Totals)
			if e.Project != "" {
				cur.Project = e.Project
			}
			out[src] = cur
		}
	}
	return out
}
