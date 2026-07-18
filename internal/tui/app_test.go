package tui

import (
	"testing"
	"time"

	"github.com/sushidev-team/lola/internal/config"
)

// tuiTestPoll is a POLLING project (a poll is a project's polling config now):
// its name is the fixture name, and it carries the repo setup + polling fields.
func tuiTestPoll(name string) config.Project {
	return config.Project{
		Name:           name,
		Path:           "/tmp/" + name,
		Repo:           "acme/" + name,
		DefaultBranch:  "main",
		Enabled:        true,
		TeamID:         "team-1",
		CycleMode:      "none",
		MatchLabels:    []string{"lbl-a"},
		MatchMode:      "any",
		AssigneeMode:   "anyone",
		ConcurrencyCap: 1,
		DedupMode:      "seen",
	}
}

// newTestRoot writes a config with a non-polling project "nori-app" (used for
// session/detail tests) plus two polling projects A and B, and builds a
// rootModel on it the way Run() does.
func newTestRoot(t *testing.T) *rootModel {
	t.Helper()
	t.Setenv("LOLA_HOME", t.TempDir())
	path, err := config.DefaultPath()
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		Defaults: config.Defaults{PollInterval: time.Minute, ConcurrencyCap: 1, GlobalCap: 4},
		Projects: []config.Project{
			{Name: "nori-app", Path: "/tmp/nori", Repo: "acme/nori", DefaultBranch: "main"},
			tuiTestPoll("A"),
			tuiTestPoll("B"),
		},
	}
	if err := cfg.Save(path); err != nil {
		t.Fatal(err)
	}
	loaded, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	// A realistic terminal size so the cockpit renders a full frame with real
	// (unclipped) columns; tests that exercise clipping send their own
	// WindowSizeMsg to override this.
	return &rootModel{cfgPath: path, cfg: loaded, list: newListModel(loaded), width: 120, height: 40}
}

// externallyDisable simulates the daemon persisting an enable-state change
// (e.g. `lola disable A` over the socket) after the TUI loaded its snapshot.
func externallyDisable(t *testing.T, path, name string) {
	t.Helper()
	ext, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	ext.PollByName(name).Enabled = false
	if err := ext.Save(path); err != nil {
		t.Fatal(err)
	}
}

// TUI-side mutations must rebase on the on-disk config, not the startup
// snapshot — otherwise they silently revert changes the daemon persisted.
func TestDeleteSelectedDoesNotClobberExternalChanges(t *testing.T) {
	m := newTestRoot(t)
	externallyDisable(t, m.cfgPath, "A")

	m.list.cursor = 2 // select B (rail lists all projects: nori-app, A, B)
	m.deleteSelected()

	got, err := config.Load(m.cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if b := got.PollByName("B"); b == nil || b.Polls() {
		t.Error("B's polling must be removed (the project itself stays)")
	}
	a := got.PollByName("A")
	if a == nil || !a.Polls() {
		t.Fatal("A's polling must survive the delete")
	}
	if a.Enabled {
		t.Error("delete must not revert A's externally persisted enabled=false")
	}
}

func TestToggleSelectedDaemonDownDoesNotClobberExternalChanges(t *testing.T) {
	m := newTestRoot(t)
	externallyDisable(t, m.cfgPath, "A")

	m.list.cursor = 2   // select B (rail lists all projects: nori-app, A, B)
	m.list.status = nil // daemon down -> direct config edit path
	m.toggleSelected()

	got, err := config.Load(m.cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if b := got.PollByName("B"); b == nil || b.Enabled {
		t.Errorf("poll B must be toggled off, got %+v", b)
	}
	if a := got.PollByName("A"); a == nil || a.Enabled {
		t.Error("toggle must not revert A's externally persisted enabled=false")
	}
}

func TestFormSaveDoesNotClobberExternalChanges(t *testing.T) {
	m := newTestRoot(t)
	externallyDisable(t, m.cfgPath, "A")

	// Edit poll B on the STALE snapshot (as a form opened earlier would).
	edited := *m.cfg.PollByName("B")
	edited.Repo = "acme/edited"
	f := &formModel{
		cfg:      m.cfg,
		origName: "B",
		poll:     edited,
		capBuf:   "1",
	}
	if _, ev := f.save(); ev != formSaved {
		t.Fatalf("save failed: %v", f.errs)
	}

	got, err := config.Load(m.cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if b := got.PollByName("B"); b == nil || b.Repo != "acme/edited" {
		t.Errorf("poll B must carry the edit, got %+v", b)
	}
	if a := got.PollByName("A"); a == nil || a.Enabled {
		t.Error("form save must not revert A's externally persisted enabled=false")
	}
}
