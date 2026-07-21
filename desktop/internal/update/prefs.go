package update

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// Prefs are the desktop app's local update preferences. They live in a small
// JSON file (NOT the daemon's config.toml — update cadence is a per-install UI
// concern the daemon and TUI never read) so the Wails service can persist the
// last-check time and a skipped version across launches.
type Prefs struct {
	AutoCheck          bool      `json:"autoCheck"`
	LastCheckTime      time.Time `json:"lastCheckTime"`
	SkippedVersion     string    `json:"skippedVersion,omitempty"`
	CheckIntervalHours int       `json:"checkIntervalHours"`
}

// DefaultPrefs auto-checks once a day.
func DefaultPrefs() Prefs {
	return Prefs{AutoCheck: true, CheckIntervalHours: 24}
}

// LoadPrefs reads prefs from path. A missing file yields DefaultPrefs (not an
// error); a corrupt file also degrades to defaults so a bad write never wedges
// the update UI. A zero/negative interval is normalised to 24h.
func LoadPrefs(path string) Prefs {
	data, err := os.ReadFile(path)
	if err != nil {
		return DefaultPrefs()
	}
	var p Prefs
	if err := json.Unmarshal(data, &p); err != nil {
		return DefaultPrefs()
	}
	if p.CheckIntervalHours <= 0 {
		p.CheckIntervalHours = 24
	}
	return p
}

// Save writes prefs atomically (temp + rename, 0600) so a crash mid-write can't
// truncate the file.
func (p Prefs) Save(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".desktop-update-*.json")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op after a successful rename
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
	return os.Rename(tmpName, path)
}

// ShouldAutoCheck reports whether enough time has passed since the last check to
// check again, respecting the AutoCheck toggle.
func (p Prefs) ShouldAutoCheck(now time.Time) bool {
	if !p.AutoCheck {
		return false
	}
	if p.LastCheckTime.IsZero() {
		return true
	}
	interval := p.CheckIntervalHours
	if interval <= 0 {
		interval = 24
	}
	return now.Sub(p.LastCheckTime) > time.Duration(interval)*time.Hour
}
