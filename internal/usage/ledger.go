package usage

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// Ledger is spend per DAY per SOURCE, persisted so a day's total survives a
// session being torn down and a daemon restart — a budget that forgot every
// session the moment its worktree was cleaned up would let a busy day spend
// past it. A source is a session id, or a fixed key for lola's own helpers.
//
// Every write REPLACES a (day, source) entry with the scanner's absolute total
// for it, never adds to it, so re-scanning the same transcripts any number of
// times is idempotent.
type Ledger struct {
	Days map[string]map[string]Entry `json:"days"`
}

// Entry is one source's spend on one day, with the project it counts against
// ("" for lola's helpers, which count only against the global budget).
// Agent is the session's coding agent ("" = claude), which picks the Scale it
// is ranked on.
type Entry struct {
	Project string `json:"project,omitempty"`
	Agent   string `json:"agent,omitempty"`
	Totals
}

// KeepDays is how much history the ledger keeps; older days are pruned.
const KeepDays = 35

// LoadLedger reads path; a missing file is an empty ledger.
func LoadLedger(path string) (*Ledger, error) {
	l := &Ledger{Days: map[string]map[string]Entry{}}
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return l, nil
	}
	if err != nil {
		return l, err
	}
	if err := json.Unmarshal(data, l); err != nil {
		return &Ledger{Days: map[string]map[string]Entry{}}, err
	}
	if l.Days == nil {
		l.Days = map[string]map[string]Entry{}
	}
	return l, nil
}

// Save writes the ledger atomically (temp + rename, 0600).
func (l *Ledger) Save(path string) error {
	data, err := json.Marshal(l)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".usage-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// Set records source's absolute usage for day, reporting whether it changed.
func (l *Ledger) Set(day, source string, e Entry) bool {
	if l.Days == nil {
		l.Days = map[string]map[string]Entry{}
	}
	m := l.Days[day]
	if m == nil {
		m = map[string]Entry{}
		l.Days[day] = m
	}
	if m[source] == e {
		return false
	}
	m[source] = e
	return true
}

// Day sums one day: the global total and the total per project.
func (l *Ledger) Day(day string) (Totals, map[string]Totals) {
	var all Totals
	by := map[string]Totals{}
	for _, e := range l.Days[day] {
		all.Add(e.Totals)
		if e.Project != "" {
			t := by[e.Project]
			t.Add(e.Totals)
			by[e.Project] = t
		}
	}
	return all, by
}

// Prune drops days older than keep days before now, reporting whether any were.
func (l *Ledger) Prune(now time.Time, keep int) bool {
	cutoff := now.AddDate(0, 0, -keep).Format(DayFormat)
	days := make([]string, 0, len(l.Days))
	for d := range l.Days {
		days = append(days, d)
	}
	sort.Strings(days)
	changed := false
	for _, d := range days {
		if d < cutoff {
			delete(l.Days, d)
			changed = true
		}
	}
	return changed
}
