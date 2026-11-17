package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sushidev-team/lola/hubproto"
)

// A busy org cannot push a quiet org's events out of the log.
func TestEventLogCappedPerOrg(t *testing.T) {
	d := NewData()
	d.Log(Event{Org: "quiet", Kind: "q"})
	for i := 0; i < MaxEvents+10; i++ {
		d.Log(Event{Org: "busy", Kind: "b", Detail: string(rune('a' + i%26))})
	}
	busy, quiet := 0, 0
	for _, e := range d.Events {
		switch e.Org {
		case "busy":
			busy++
		case "quiet":
			quiet++
		}
	}
	if busy != MaxEvents || quiet != 1 {
		t.Fatalf("busy=%d quiet=%d", busy, quiet)
	}
	// The busy org lost its OLDEST events.
	if last := d.Events[len(d.Events)-1]; last.Org != "busy" || last.Detail != string(rune('a'+(MaxEvents+9)%26)) {
		t.Fatalf("newest event dropped: %+v", last)
	}
}

func TestHistoryAndCooldownHelpers(t *testing.T) {
	w := &WorkItem{}
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < hubproto.MaxItemHistory+3; i++ {
		w.AddHistory(Attempt{Action: hubproto.AttemptClaimed, At: t0.Add(time.Duration(i) * time.Minute)})
	}
	if len(w.History) != hubproto.MaxItemHistory || !w.History[0].At.Equal(t0.Add(3*time.Minute)) {
		t.Fatalf("history kept %d, oldest %v", len(w.History), w.History[0].At)
	}
	w.CoolDown("a", t0, t0.Add(time.Hour))
	if !w.CoolingFor("a", t0.Add(59*time.Minute)) || w.CoolingFor("a", t0.Add(time.Hour)) || w.CoolingFor("b", t0) {
		t.Fatal("cooldown window wrong")
	}
	// A later cooldown drops entries that ran out.
	w.CoolDown("b", t0.Add(2*time.Hour), t0.Add(3*time.Hour))
	if _, ok := w.Cooldown["a"]; ok || len(w.Cooldown) != 1 {
		t.Fatalf("expired cooldown kept: %v", w.Cooldown)
	}
}

func TestStatsDayWindow(t *testing.T) {
	d := NewData()
	day0 := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	d.StatsDay("acme", day0).Opened++
	d.StatsDay("acme", day0.Add(time.Hour)).Opened++
	d.StatsDay("beta", day0).Done++
	if got := d.Stats["acme"]; len(got) != 1 || got[0].Opened != 2 || got[0].Date != "2026-01-01" {
		t.Fatalf("acme %+v", got)
	}
	if got := d.Stats["beta"]; len(got) != 1 || got[0].Done != 1 {
		t.Fatalf("beta %+v", got)
	}
	// 90 days on, the first day falls out of the window.
	later := day0.AddDate(0, 0, hubproto.StatsDays)
	d.StatsDay("acme", later).Done++
	if got := d.Stats["acme"]; len(got) != 1 || got[0].Date != later.Format(hubproto.StatsDateLayout) {
		t.Fatalf("old day kept: %+v", got)
	}
	// A clock that stepped back counts into the newest day, never reorders.
	d.StatsDay("acme", later.AddDate(0, 0, -1)).Failed++
	if got := d.Stats["acme"]; len(got) != 1 || got[0].Failed != 1 {
		t.Fatalf("stepped-back clock: %+v", got)
	}
}

// Every successful write keeps the previous snapshot at <path>.bak, and a
// corrupt snapshot loads from it with a warning that names no content.
func TestCorruptSnapshotFallsBackToBackup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hub.json")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, org := range []string{"acme", "beta"} {
		if err := s.Update(func(d *Data) error { d.Orgs[org] = &Org{Name: org}; return nil }); err != nil {
			t.Fatal(err)
		}
	}
	st, err := os.Stat(path + ".bak")
	if err != nil {
		t.Fatalf("no backup: %v", err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("backup mode %v", st.Mode().Perm())
	}
	if err := os.WriteFile(path, []byte(`{"orgs": SECRET-ISH garbage`), 0o600); err != nil {
		t.Fatal(err)
	}
	var warned []string
	s2 := &File{path: path, data: NewData(), Warnf: func(f string, a ...any) { warned = append(warned, f) }}
	if err := s2.View(func(d *Data) error {
		// The backup is the snapshot BEFORE the last write: acme only.
		if d.Orgs["acme"] == nil || d.Orgs["beta"] != nil {
			t.Fatalf("backup content wrong: %v", d.Orgs)
		}
		return nil
	}); err != nil {
		t.Fatalf("view on a corrupt snapshot: %v", err)
	}
	if len(warned) != 1 || strings.Contains(warned[0], "SECRET") {
		t.Fatalf("warnings %q", warned)
	}
	// The next write replaces the corrupt snapshot and keeps the good backup.
	if err := s2.Update(func(d *Data) error { d.Orgs["gamma"] = &Org{Name: "gamma"}; return nil }); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path + ".bak")
	if strings.Contains(string(raw), "SECRET") {
		t.Fatal("corrupt snapshot overwrote the backup")
	}
	s3, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	_ = s3.View(func(d *Data) error {
		if d.Orgs["acme"] == nil || d.Orgs["gamma"] == nil {
			t.Fatalf("after recovery: %v", d.Orgs)
		}
		return nil
	})
	// Without a usable backup a corrupt snapshot is still an error.
	_ = os.WriteFile(path+".bak", []byte("junk"), 0o600)
	_ = os.WriteFile(path, []byte("junk"), 0o600)
	if _, err := Open(path); err == nil {
		t.Fatal("corrupt snapshot and backup opened")
	}
}
