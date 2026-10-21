package tui

import (
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/reviewprofile"
)

func TestSpecialistSettingsRoundTrip(t *testing.T) {
	m := newTestRoot(t)
	provider, _ := config.NewReviewProvider("codex-session")
	provider.Enabled = true
	m.cfg.ReviewProviders = []config.ReviewProvider{provider}
	original := reviewprofile.Profile{ID: "api-review", Kind: "backend", Provider: "codex-session", Version: "custom-2", Rubric: "  Check API errors.\n\nKeep caller contracts.\n", Enabled: true, Required: true, OnPROpen: true, Projects: []string{"nori-app"}, Paths: []string{"internal/api"}, Artifacts: []string{"api-check"}, TimeoutSeconds: 125, MaxRounds: 2}
	m.cfg.ReviewProfiles = []reviewprofile.Profile{original}
	f := newSettingsForm(m.cfgPath, m.cfg)
	f.field("global_cap").text = "5"
	if got := f.save(); got != settingsFormSaved {
		t.Fatal(got, f.err)
	}
	loaded, err := config.Load(m.cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(loaded.ReviewProfiles, []reviewprofile.Profile{original}) {
		t.Fatalf("round trip altered profile: %#v", loaded.ReviewProfiles)
	}
}

func TestSpecialistSettingsAddRemoveCancel(t *testing.T) {
	m := newTestRoot(t)
	f := newSettingsForm(m.cfgPath, m.cfg)
	f.field(pvKey("codex-session", "enabled")).b = true
	focusField(t, f, specialistAddKey)
	f.update(keyMsg("enter"))
	if f.picker == nil || len(f.picker.opts) != 5 {
		t.Fatal("missing specialty picker")
	}
	f.update(keyMsg("enter"))
	if f.cur().key != specialistKey(0, "id") || !f.dirty() {
		t.Fatal("new profile not focused / dirty")
	}
	if len(m.cfg.ReviewProfiles) != 0 {
		t.Fatal("add mutated live config")
	}
	f.addSpecialist("backend")
	if f.field(specialistKey(1, "id")).text != "backend-2" {
		t.Fatal("duplicate default ID")
	}
	// Rename one ID: internal keys must remain independent of that edit.
	f.field(specialistKey(0, "id")).text = "custom"
	focusField(t, f, specialistKey(0, "remove"))
	f.update(keyMsg("enter"))
	if f.cur().key != specialistAddKey || f.field(specialistKey(0, "id")) != nil {
		t.Fatal("remove/focus failed")
	}
	f.update(keyMsg("esc"))
	if !f.confirmDiscard {
		t.Fatal("missing discard guard")
	}
	_, event := f.update(keyMsg("n"))
	if event != settingsFormNone {
		t.Fatal("keep editing canceled form")
	}
	if got := f.save(); got != settingsFormSaved {
		t.Fatal(got, f.err)
	}
	loaded, err := config.Load(m.cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.ReviewProfiles) != 1 || loaded.ReviewProfiles[0].ID != "backend-2" || loaded.ReviewProfiles[0].Enabled {
		t.Fatalf("wrong saved profiles: %#v", loaded.ReviewProfiles)
	}
	f = newSettingsForm(m.cfgPath, loaded)
	focusField(t, f, specialistKey(0, "remove"))
	f.update(keyMsg("enter"))
	if f.save() != settingsFormSaved {
		t.Fatal(f.err)
	}
	loaded, err = config.Load(m.cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.ReviewProfiles) != 0 {
		t.Fatal("last removal did not persist")
	}
}

func TestSpecialistSettingsRejectedSavePreservesDraftAndConfig(t *testing.T) {
	for _, tc := range []struct{ name, field, value string }{
		{"disabled provider", "enabled", "true"}, {"invalid timeout", "timeout", "oops"}, {"timeout limit", "timeout", "901"}, {"round limit", "rounds", "4"}, {"oversized rubric", "rubric", strings.Repeat("a", 12001)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := newTestRoot(t)
			before, err := os.ReadFile(m.cfgPath)
			if err != nil {
				t.Fatal(err)
			}
			f := newSettingsForm(m.cfgPath, m.cfg)
			f.addSpecialist("backend")
			fld := f.field(specialistKey(0, tc.field))
			switch tc.field {
			case "enabled":
				fld.b = true
			case "rubric":
				fld.lines = []string{tc.value}
			default:
				fld.text = tc.value
			}
			if f.save() != settingsFormNone || f.err == "" {
				t.Fatal("invalid save accepted")
			}
			after, err := os.ReadFile(m.cfgPath)
			if err != nil {
				t.Fatal(err)
			}
			if string(before) != string(after) || len(m.cfg.ReviewProfiles) != 0 {
				t.Fatal("rejected save changed config")
			}
			if !f.dirty() || f.field(specialistKey(0, "id")) == nil {
				t.Fatal("draft lost")
			}
		})
	}
}

func TestSpecialistSettingsProjectPickerAndRubricEditing(t *testing.T) {
	m := newTestRoot(t)
	m.cfg.Projects[0].Label = "Nori app"
	f := newSettingsForm(m.cfgPath, m.cfg)
	f.addSpecialist("uiux")
	focusField(t, f, specialistKey(0, "projects"))
	f.update(keyMsg("enter"))
	if f.picker.opts[0].label != "Nori app" {
		t.Fatal("picker shows identity instead of label")
	}
	f.update(keyMsg("space"))
	f.update(keyMsg("enter"))
	if got := f.field(specialistKey(0, "projects")).lines; len(got) != 1 || got[0] != "nori-app" {
		t.Fatal(got)
	}
	focusField(t, f, specialistKey(0, "rubric"))
	f.update(keyMsg("enter"))
	f.update(keyMsg("x"))
	f.update(keyMsg("enter"))
	f.update(keyMsg("y"))
	f.update(keyMsg("esc"))
	profiles, err := f.buildSpecialists()
	if err != nil {
		t.Fatal(err)
	}
	if profiles[0].Rubric != "x\ny" {
		t.Fatalf("rubric=%q", profiles[0].Rubric)
	}
	if len(profiles[0].RequiredArtifacts()) != 2 {
		t.Fatal("built-in evidence lost")
	}
	for _, budget := range []int{16, 29} {
		focusField(t, f, specialistKey(0, "remove"))
		view := stripANSI(f.scrolledView(budget))
		if !strings.Contains(view, "Remove specialist") || !strings.Contains(view, "ctrl-s save") {
			t.Fatalf("focused action/footer missing: %s", view)
		}
	}
}

func TestSpecialistSettingsPendingLabelLoadKeepsDiscardGuard(t *testing.T) {
	m := newTestRoot(t)
	f := newSettingsForm(m.cfgPath, m.cfg)
	f.addSpecialist("backend")
	f.update(workspaceLabelsMsg{})
	f.update(keyMsg("esc"))
	if !f.confirmDiscard {
		t.Fatal("async label load erased specialist edit baseline")
	}
}

func TestSpecialistSettingsCompactModalShowsNavigationAndError(t *testing.T) {
	m := newTestRoot(t)
	m.width, m.height = 80, 24
	f := newSettingsForm(m.cfgPath, m.cfg)
	m.settings = f
	f.addSpecialist("backend")
	f.field(specialistKey(0, "enabled")).b = true
	if f.save() != settingsFormNone {
		t.Fatal("missing provider was accepted")
	}
	view := stripANSI(m.settingsFormModal())
	for _, text := range []string{"Review", "provider (structured rubric execution)", "ctrl-s save", "esc cancel"} {
		if !strings.Contains(view, text) {
			t.Fatalf("missing %q: %s", text, view)
		}
	}
}

func TestSpecialistSettingsSaveFailureAndLegacyMigration(t *testing.T) {
	m := newTestRoot(t)
	m.cfg.ReviewProfiles = []reviewprofile.Profile{{ID: "backend", Kind: "backend", Provider: "codex-session", Version: "1", Rubric: "custom\n\ntext"}}
	f := newSettingsForm(m.cfgPath, m.cfg)
	f.addSpecialist("uiux")
	f.cfgPath = t.TempDir() // cannot atomically replace a directory with config data
	if f.save() != settingsFormNone || !strings.Contains(f.err, "save failed") {
		t.Fatal(f.err)
	}
	if len(m.cfg.ReviewProfiles) != 1 || len(f.specialistKeys) != 2 {
		t.Fatal("failed write did not preserve live config and draft independently")
	}
	m.cfg.Review = config.ReviewConfig{Enabled: true}
	f = newSettingsForm(m.cfgPath, m.cfg)
	focusField(t, f, specialistAddKey)
	f.update(keyMsg("enter"))
	if f.picker != nil {
		t.Fatal("legacy guard allowed profile editing")
	}
	f.update(keyMsg("m"))
	if f.reviewLegacy || f.err != "" {
		t.Fatal("migration failed", f.err)
	}
	profiles, err := f.buildSpecialists()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(profiles, m.cfg.ReviewProfiles) {
		t.Fatal("migration lost existing profile")
	}
}
