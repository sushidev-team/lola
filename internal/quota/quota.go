// Package quota reads how much of the user's SUBSCRIPTION limits the coding
// agents have used — the number a subscriber actually budgets by, since a
// subscription pays nothing per token. Neither agent offers an API for it, so
// each is read from the one place it already appears:
//
//   - claude: Claude Code hands `rate_limits.five_hour` / `seven_day`
//     (used_percentage + resets_at) to the STATUS-LINE command on stdin, and
//     writes it nowhere else — not into a transcript. lola therefore sets its
//     own status line in each session's --settings (`lola hook statusline`),
//     which records the figures (RecordClaude) and then runs the user's own
//     status-line command with the same stdin, so the pane looks unchanged.
//   - codex: every `token_count` event in a codex session log
//     (~/.codex/sessions/YYYY/MM/DD/rollout-*.jsonl) carries a `rate_limits`
//     block; LatestCodex reads the newest one.
//
// Both are SNAPSHOTS: only as fresh as the agent's last turn, which is why a
// Snapshot carries the time it was observed and every window its reset time.
// Only numbers are read — never text — and they are clamped, because both
// files are written by a process lola does not control.
//
// Stdlib-only leaf, like internal/usage.
package quota

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"time"
)

// Window is one rate-limit window.
type Window struct {
	// Label names the window for a human: "5h", "7d", "spend".
	Label       string    `json:"label"`
	UsedPercent float64   `json:"usedPercent"`
	ResetsAt    time.Time `json:"resetsAt"`
}

// Snapshot is one agent's limits as last observed.
type Snapshot struct {
	Windows []Window  `json:"windows"`
	At      time.Time `json:"at"`
	Plan    string    `json:"plan,omitempty"`
}

// Live drops windows whose reset has passed: their percentage describes a
// period that is over. ok is false when nothing is left.
func (s Snapshot) Live(now time.Time) (Snapshot, bool) {
	out := s
	out.Windows = nil
	for _, w := range s.Windows {
		if w.ResetsAt.IsZero() || w.ResetsAt.After(now) {
			out.Windows = append(out.Windows, w)
		}
	}
	return out, len(out.Windows) > 0
}

func clampPercent(v float64) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 {
		return 0
	}
	// A gateway spend limit reports above 100 once exceeded; anything past a
	// few hundred is noise, not information.
	return math.Min(v, 999)
}

func epoch(sec float64) time.Time {
	if sec <= 0 || math.IsNaN(sec) || math.IsInf(sec, 0) || sec > 1e11 {
		return time.Time{}
	}
	return time.Unix(int64(sec), 0)
}

// windowLabel names a codex window by its length.
func windowLabel(minutes int) string {
	switch {
	case minutes <= 0:
		return "limit"
	case minutes%(24*60) == 0:
		return strconv.Itoa(minutes/(24*60)) + "d"
	case minutes%60 == 0:
		return strconv.Itoa(minutes/60) + "h"
	default:
		return strconv.Itoa(minutes) + "m"
	}
}

// ---- claude ---------------------------------------------------------------

type claudeWindow struct {
	Used     *float64 `json:"used_percentage"`
	ResetsAt float64  `json:"resets_at"`
}

// StatusLineInput is the part of Claude Code's status-line stdin lola reads.
type StatusLineInput struct {
	Cwd       string `json:"cwd"`
	Workspace struct {
		CurrentDir string `json:"current_dir"`
	} `json:"workspace"`
	RateLimits *struct {
		FiveHour   *claudeWindow `json:"five_hour"`
		SevenDay   *claudeWindow `json:"seven_day"`
		SpendLimit *claudeWindow `json:"spend_limit"`
	} `json:"rate_limits"`
}

// Dir is the session's working directory as Claude Code reported it.
func (in StatusLineInput) Dir() string {
	if in.Workspace.CurrentDir != "" {
		return in.Workspace.CurrentDir
	}
	return in.Cwd
}

// ParseClaude extracts the limits from a status-line payload; ok is false
// when it carries none (an API-key user, or an older Claude Code).
func ParseClaude(in StatusLineInput, now time.Time) (Snapshot, bool) {
	if in.RateLimits == nil {
		return Snapshot{}, false
	}
	s := Snapshot{At: now}
	add := func(label string, w *claudeWindow) {
		if w == nil || w.Used == nil {
			return
		}
		s.Windows = append(s.Windows, Window{Label: label, UsedPercent: clampPercent(*w.Used), ResetsAt: epoch(w.ResetsAt)})
	}
	add("5h", in.RateLimits.FiveHour)
	add("7d", in.RateLimits.SevenDay)
	add("spend", in.RateLimits.SpendLimit)
	return s, len(s.Windows) > 0
}

// ClaudePath is where RecordClaude keeps the last snapshot, under lola's home.
func ClaudePath(home string) string { return filepath.Join(home, "state", "quota-claude.json") }

// RecordClaude writes s to path when its windows changed. The status line runs
// on every redraw, so an unchanged figure must not cost a file write each time.
func RecordClaude(path string, s Snapshot) error {
	if old, err := ReadClaude(path); err == nil && sameWindows(old.Windows, s.Windows) && s.At.Sub(old.At) < time.Minute {
		return nil
	}
	data, err := json.Marshal(s)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".quota-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

func sameWindows(a, b []Window) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Label != b[i].Label || a[i].UsedPercent != b[i].UsedPercent || !a[i].ResetsAt.Equal(b[i].ResetsAt) {
			return false
		}
	}
	return true
}

// ReadClaude reads the last recorded snapshot.
func ReadClaude(path string) (Snapshot, error) {
	var s Snapshot
	data, err := os.ReadFile(path)
	if err != nil {
		return s, err
	}
	if err := json.Unmarshal(data, &s); err != nil {
		return Snapshot{}, err
	}
	for i := range s.Windows {
		s.Windows[i].UsedPercent = clampPercent(s.Windows[i].UsedPercent)
	}
	return s, nil
}

// ---- codex ----------------------------------------------------------------

// CodexHome is $CODEX_HOME, else ~/.codex; "" when no home dir resolves.
func CodexHome() string {
	if d := os.Getenv("CODEX_HOME"); d != "" {
		return d
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".codex")
}

const (
	// codexTail is how much of a session log's end is read for its last
	// rate_limits event: token_count events come every turn, so the last one
	// is near the end, and a log can be tens of megabytes.
	codexTail = 512 << 10
	// codexFiles bounds how many recent logs are tried before giving up.
	codexFiles = 6
	// codexDays bounds how many day directories are listed.
	codexDays = 7
)

type codexWindow struct {
	Used     *float64 `json:"used_percent"`
	Minutes  int      `json:"window_minutes"`
	ResetsAt float64  `json:"resets_at"`
}

type codexLine struct {
	Timestamp string `json:"timestamp"`
	Payload   struct {
		RateLimits *struct {
			Primary   *codexWindow `json:"primary"`
			Secondary *codexWindow `json:"secondary"`
			Plan      string       `json:"plan_type"`
		} `json:"rate_limits"`
	} `json:"payload"`
}

var rateKey = []byte(`"rate_limits":{`)

// LatestCodex reads the newest rate_limits event from codex's session logs
// under root (CodexHome). ok is false when none is found.
func LatestCodex(root string) (Snapshot, bool) {
	if root == "" {
		return Snapshot{}, false
	}
	for _, f := range recentLogs(filepath.Join(root, "sessions")) {
		if s, ok := lastCodexLimits(f); ok {
			return s, true
		}
	}
	return Snapshot{}, false
}

// recentLogs lists the newest rollout logs, newest first, walking the
// YYYY/MM/DD tree from the end instead of the whole history.
func recentLogs(dir string) []string {
	var days []string
	for _, y := range descDirs(dir) {
		for _, m := range descDirs(y) {
			for _, d := range descDirs(m) {
				days = append(days, d)
				if len(days) >= codexDays {
					goto listed
				}
			}
		}
	}
listed:
	type logFile struct {
		path string
		mod  time.Time
	}
	var files []logFile
	for _, d := range days {
		ents, _ := os.ReadDir(d)
		for _, e := range ents {
			if e.Type().IsRegular() && filepath.Ext(e.Name()) == ".jsonl" {
				if info, err := e.Info(); err == nil {
					files = append(files, logFile{filepath.Join(d, e.Name()), info.ModTime()})
				}
			}
		}
		if len(files) >= codexFiles {
			break
		}
	}
	sort.Slice(files, func(i, j int) bool { return files[i].mod.After(files[j].mod) })
	out := make([]string, 0, codexFiles)
	for i := 0; i < len(files) && i < codexFiles; i++ {
		out = append(out, files[i].path)
	}
	return out
}

func descDirs(dir string) []string {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for i := len(ents) - 1; i >= 0; i-- { // ReadDir sorts by name; dates sort lexically
		if ents[i].IsDir() {
			out = append(out, filepath.Join(dir, ents[i].Name()))
		}
	}
	return out
}

func lastCodexLimits(path string) (Snapshot, bool) {
	f, err := os.Open(path)
	if err != nil {
		return Snapshot{}, false
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return Snapshot{}, false
	}
	off := max(info.Size()-codexTail, 0)
	buf := make([]byte, info.Size()-off)
	if _, err := f.ReadAt(buf, off); err != nil && !errors.Is(err, io.EOF) {
		return Snapshot{}, false
	}
	lines := bytes.Split(buf, []byte{'\n'})
	for i := len(lines) - 1; i >= 0; i-- {
		if !bytes.Contains(lines[i], rateKey) {
			continue
		}
		var l codexLine
		if json.Unmarshal(lines[i], &l) != nil || l.Payload.RateLimits == nil {
			continue
		}
		rl := l.Payload.RateLimits
		s := Snapshot{Plan: rl.Plan}
		if ts, err := time.Parse(time.RFC3339Nano, l.Timestamp); err == nil {
			s.At = ts
		} else {
			s.At = info.ModTime()
		}
		for _, w := range []*codexWindow{rl.Primary, rl.Secondary} {
			if w != nil && w.Used != nil {
				s.Windows = append(s.Windows, Window{Label: windowLabel(w.Minutes), UsedPercent: clampPercent(*w.Used), ResetsAt: epoch(w.ResetsAt)})
			}
		}
		if len(s.Windows) > 0 {
			return s, true
		}
	}
	return Snapshot{}, false
}
