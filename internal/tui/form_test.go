package tui

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/sushidev-team/lola/internal/config"
)

// newNativeTestForm builds a form on a saved config that defines one
// [[project]] (the native runtime's registry) and no AO configuration at all
// — the AO registry being unreachable must not matter for native polls.
func newNativeTestForm(t *testing.T, projects []config.Project) (*formModel, string) {
	t.Helper()
	t.Setenv("LOLA_HOME", t.TempDir())
	path, err := config.DefaultPath()
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		Defaults: config.Defaults{PollInterval: time.Minute, ConcurrencyCap: 1, GlobalCap: 4},
		Projects: projects,
	}
	if err := cfg.Save(path); err != nil {
		t.Fatal(err)
	}
	loaded, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	f, _ := newFormModel(loaded, nil) // returned cmd (teams fetch) is never run
	return f, path
}

// The picker flow runtime→native→project must write Poll.Runtime and
// Poll.Project, unlock the native project field, and save without any
// ao_project — even though the AO registry is unavailable.
func TestFormNativeRuntimePickerFlow(t *testing.T) {
	f, path := newNativeTestForm(t, []config.Project{
		{Name: "web", Path: "/tmp/web", Repo: "acme/web"},
	})
	if f.aoErr == "" {
		t.Fatal("precondition: AO registry must be unavailable in this test")
	}
	if f.poll.Runtime != config.RuntimeAO {
		t.Fatalf("new poll runtime = %q, want default %q", f.poll.Runtime, config.RuntimeAO)
	}
	if fs := f.fields(); slices.Contains(fs, fNativeProject) {
		t.Error("native project field must be hidden while runtime=ao")
	}

	// Linear-backed fields are set directly; this test drives only the
	// runtime/project pickers.
	f.poll.Name, f.poll.TeamID = "P", "team-1"

	if !slices.Contains(f.fields(), fRuntime) {
		t.Fatal("runtime field must be visible once a team is set")
	}
	f.openPicker(fRuntime)
	if f.picker == nil || f.picker.field != fRuntime {
		t.Fatalf("runtime picker did not open: %+v", f.picker)
	}
	if got := f.picker.opts[f.picker.cursor].id; got != config.RuntimeAO {
		t.Errorf("runtime picker cursor starts on %q, want %q", got, config.RuntimeAO)
	}
	f.pickerKey(keyMsg("j")) // ao → native
	f.pickerKey(keyMsg("enter"))
	if f.poll.Runtime != config.RuntimeNative {
		t.Fatalf("poll.Runtime = %q, want native", f.poll.Runtime)
	}
	if !slices.Contains(f.fields(), fNativeProject) {
		t.Fatal("native project field must appear for runtime=native")
	}

	f.openPicker(fNativeProject)
	if f.picker == nil || f.picker.field != fNativeProject {
		t.Fatalf("native project picker did not open: %+v", f.picker)
	}
	f.pickerKey(keyMsg("enter")) // only option: web
	if f.poll.Project != "web" {
		t.Fatalf("poll.Project = %q, want web", f.poll.Project)
	}

	// Repo hint reflects the daemon-owned fallback for native polls.
	if hint := f.display(fRepo); !strings.Contains(hint, "[[project]] repo") {
		t.Errorf("repo hint for native runtime = %q, want [[project]] fallback wording", hint)
	}

	if _, ev := f.save(); ev != formSaved {
		t.Fatalf("save failed: %v", f.errs)
	}
	got, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	p := got.PollByName("P")
	if p == nil {
		t.Fatal("poll P not persisted")
	}
	if p.Runtime != config.RuntimeNative || p.Project != "web" || p.AOProject != "" {
		t.Errorf("persisted poll = runtime %q project %q ao_project %q, want native/web/empty",
			p.Runtime, p.Project, p.AOProject)
	}
}

// runtime=ao keeps the hard ao_project requirement and the plain repo hint.
func TestFormAORuntimeStillRequiresAOProject(t *testing.T) {
	f, _ := newNativeTestForm(t, []config.Project{
		{Name: "web", Path: "/tmp/web", Repo: "acme/web"},
	})
	f.poll.Name, f.poll.TeamID = "Q", "team-1"
	f.aoErr, f.aoProjects = "", []string{"proj"} // registry reachable, project unset

	if hint := f.display(fRepo); !strings.Contains(hint, "disables open-PR checks") {
		t.Errorf("repo hint for ao runtime = %q, want open-PR-checks wording", hint)
	}
	if _, ev := f.save(); ev != formNone {
		t.Fatal("save must fail without ao_project for runtime=ao")
	}
	if !slices.Contains(f.errs, "ao_project is required") {
		t.Errorf("errs = %v, want ao_project requirement", f.errs)
	}
}

// Native project selection must survive a runtime round-trip (native → ao →
// native), and the picker refuses to open when no [[project]] is defined.
func TestFormNativeProjectPickerEdgeCases(t *testing.T) {
	f, _ := newNativeTestForm(t, nil) // no projects defined
	f.poll.Name, f.poll.TeamID = "R", "team-1"
	f.poll.Runtime = config.RuntimeNative

	f.openPicker(fNativeProject)
	if f.picker != nil {
		t.Fatal("native project picker must not open without [[project]] entries")
	}
	if !strings.Contains(f.loadErr, "no [[project]] entries") {
		t.Errorf("loadErr = %q, want missing-projects hint", f.loadErr)
	}

	// Round-trip keeps the stashed selection.
	f.cfg.Projects = []config.Project{{Name: "web", Path: "/tmp/web"}}
	f.poll.Project = "web"
	f.poll.Runtime = config.RuntimeAO
	if slices.Contains(f.fields(), fNativeProject) {
		t.Error("native project field must hide again for runtime=ao")
	}
	f.poll.Runtime = config.RuntimeNative
	if f.poll.Project != "web" {
		t.Error("switching runtime must not clear the native project selection")
	}
}
