package tui

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/sushidev-team/lola/internal/config"
)

// newNativeTestForm builds a form on a saved config that defines the given
// [[project]] entries (the native runtime's registry).
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

// The project picker writes Poll.Project (from cfg.Projects) and the form
// persists the poll.
func TestFormProjectPickerFlow(t *testing.T) {
	f, path := newNativeTestForm(t, []config.Project{
		{Name: "web", Path: "/tmp/web", Repo: "acme/web"},
	})

	// Linear-backed fields are set directly; this test drives only the
	// project picker.
	f.poll.Name, f.poll.TeamID = "P", "team-1"

	if !slices.Contains(f.fields(), fNativeProject) {
		t.Fatal("project field must be visible once a team is set")
	}
	f.openPicker(fNativeProject)
	if f.picker == nil || f.picker.field != fNativeProject {
		t.Fatalf("project picker did not open: %+v", f.picker)
	}
	f.pickerKey(keyMsg("enter")) // only option: web
	if f.poll.Project != "web" {
		t.Fatalf("poll.Project = %q, want web", f.poll.Project)
	}

	// Repo hint reflects the daemon-owned [[project]] fallback.
	if hint := f.display(fRepo); !strings.Contains(hint, "[[project]] repo") {
		t.Errorf("repo hint = %q, want [[project]] fallback wording", hint)
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
	if p.Project != "web" {
		t.Errorf("persisted poll project = %q, want web", p.Project)
	}
}

// Saving without a project surfaces a friendly error (nc.Validate also enforces
// the [[project]] reference).
func TestFormSaveRequiresProject(t *testing.T) {
	f, _ := newNativeTestForm(t, []config.Project{
		{Name: "web", Path: "/tmp/web", Repo: "acme/web"},
	})
	f.poll.Name, f.poll.TeamID = "Q", "team-1"
	// Project deliberately left unset.

	if _, ev := f.save(); ev != formNone {
		t.Fatal("save must fail without a project")
	}
	if !slices.Contains(f.errs, "project is required — pick a [[project]] entry") {
		t.Errorf("errs = %v, want project requirement", f.errs)
	}
}

// The project picker refuses to open when no [[project]] is defined and points
// the user at the fix.
func TestFormProjectPickerRefusesWithoutProjects(t *testing.T) {
	f, _ := newNativeTestForm(t, nil) // no projects defined
	f.poll.Name, f.poll.TeamID = "R", "team-1"

	f.openPicker(fNativeProject)
	if f.picker != nil {
		t.Fatal("project picker must not open without [[project]] entries")
	}
	if !strings.Contains(f.loadErr, "no [[project]] entries") {
		t.Errorf("loadErr = %q, want missing-projects hint", f.loadErr)
	}
}
