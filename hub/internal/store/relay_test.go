package store

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestStoreRelayEntryMatchesConstantTime(t *testing.T) {
	now := time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)
	cur, prev := "aa", "bb"
	e := &RelayEntry{RV: "r", BearerHash: cur, PrevHash: prev, PrevUntil: now.Add(time.Hour)}
	for _, c := range []struct {
		name string
		e    *RelayEntry
		in   string
		want bool
	}{
		{"current", e, cur, true},
		{"previous in grace", e, prev, true},
		{"unknown", e, "cc", false},
		{"empty", e, "", false},
		{"previous expired", &RelayEntry{BearerHash: cur, PrevHash: prev, PrevUntil: now}, prev, false},
		{"no previous, empty input", &RelayEntry{BearerHash: cur}, "", false},
		{"empty stored hash, empty input", &RelayEntry{}, "", false},
		{"window live", &RelayEntry{BearerHash: cur, ExpiresAt: now.Add(time.Minute)}, cur, true},
		{"window expired", &RelayEntry{BearerHash: cur, ExpiresAt: now}, cur, false},
	} {
		if got := c.e.Matches(c.in, now); got != c.want {
			t.Errorf("%s: Matches = %v, want %v", c.name, got, c.want)
		}
	}
	if !(&RelayEntry{ExpiresAt: now.Add(1)}).Window() || (&RelayEntry{}).Window() {
		t.Error("Window")
	}
	if !(&RelayEntry{}).Live(now) || (&RelayEntry{ExpiresAt: now}).Live(now) {
		t.Error("Live")
	}
}

func TestStoreOldSnapshotLoads(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hub.json")
	old := `{"orgs":{"acme":{"name":"acme","created_at":"2026-01-01T00:00:00Z"}},"devices":{},"tokens":{},"items":{},"events":[]}`
	if err := os.WriteFile(path, []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	err = s.Update(func(d *Data) error {
		if d.Relay == nil || len(d.Relay) != 0 || d.Orgs["acme"] == nil {
			t.Fatalf("old snapshot: relay=%v", d.Relay)
		}
		d.Relay["rv1"] = &RelayEntry{RV: "rv1", Device: "d1", Org: "acme", BearerHash: "h"}
		d.Relay["rv0"] = &RelayEntry{RV: "rv0", Device: "d1", Org: "acme"}
		d.Relay["rv2"] = &RelayEntry{RV: "rv2", Device: "d2", Org: "acme"}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	s2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	_ = s2.View(func(d *Data) error {
		got := d.RelayEntriesOf("d1")
		if len(got) != 2 || got[0].RV != "rv0" || got[1].RV != "rv1" || got[1].BearerHash != "h" {
			t.Fatalf("RelayEntriesOf = %+v", got)
		}
		if len(d.RelayEntriesOf("none")) != 0 {
			t.Fatal("unknown device has entries")
		}
		return nil
	})
}

func TestStoreNewDataHasRelayMap(t *testing.T) {
	d := NewData()
	if d.Relay == nil {
		t.Fatal("NewData left Relay nil")
	}
	d.Relay["x"] = &RelayEntry{RV: "x"}
}
