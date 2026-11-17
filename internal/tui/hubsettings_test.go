package tui

import (
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/sushidev-team/lola/internal/config"
)

// The Team hub tab pre-fills the effective values without freezing a default
// into the file, and saves the opt-ins it shows.
func TestHubSettingsRoundTrip(t *testing.T) {
	m := newTestRoot(t)
	f := newSettingsForm(m.cfgPath, m.cfg)
	if f.dirty() {
		t.Fatal("a freshly opened form must not be dirty")
	}
	if got := f.field(hubClaimsKey).text; got != "4" {
		t.Errorf("max chores prefill = %q, want the default 4", got)
	}
	if !f.field(hubSandboxKey).b {
		t.Error("require_sandbox must show its safe default (on)")
	}
	if f.field(hubChoresKey("nori-app")) == nil {
		t.Fatal("no per-project hub_chores toggle")
	}
	tabTo(t, f, stHub)
	if out := f.view(); !strings.Contains(out, "Team hub") || !strings.Contains(out, "Projects that take chores") {
		t.Errorf("tab not rendered:\n%s", out)
	}

	// Saving untouched writes no [hub] key at all.
	if ev := f.save(); ev != settingsFormSaved {
		t.Fatalf("save = %v, err=%q", ev, f.err)
	}
	if src, _ := os.ReadFile(m.cfgPath); strings.Contains(string(src), "[hub]") {
		t.Fatalf("an untouched tab froze defaults into the file:\n%s", src)
	}

	f = newSettingsForm(m.cfgPath, m.cfg)
	f.field(hubChoresKey("nori-app")).b = true
	f.field(hubClaimsKey).text = "2"
	f.field(hubKindsKey).lines = []string{"red_bump_pr"}
	if ev := f.save(); ev != settingsFormSaved {
		t.Fatalf("save = %v, err=%q", ev, f.err)
	}
	got, err := config.Load(m.cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if got.Hub.ClaimsPerDay() != 2 || !slices.Equal(got.Hub.AcceptKinds, []string{"red_bump_pr"}) ||
		got.Hub.RequireSandbox != nil || !got.ProjectByName("nori-app").HubChores {
		t.Errorf("saved hub = %+v, chores %v", got.Hub, got.ProjectByName("nori-app").HubChores)
	}
}

// A refused save (enabled without a url) rolls the tab back off the config.
func TestHubSettingsRejectedSaveRollsBack(t *testing.T) {
	m := newTestRoot(t)
	f := newSettingsForm(m.cfgPath, m.cfg)
	f.field(hubEnabledKey).b = true
	f.field(hubSandboxKey).b = false
	if ev := f.save(); ev == settingsFormSaved || !strings.Contains(f.err, "hub.url") {
		t.Fatalf("save = %v, err=%q", ev, f.err)
	}
	if m.cfg.Hub.Enabled || m.cfg.Hub.RequireSandbox != nil {
		t.Errorf("the refused values stayed on the config: %+v", m.cfg.Hub)
	}
	f.field(hubEnabledKey).b = false
	f.field(hubClaimsKey).text = "x"
	if ev := f.save(); ev == settingsFormSaved || !strings.Contains(f.err, "whole number") {
		t.Fatalf("bad number: %v %q", ev, f.err)
	}
}
