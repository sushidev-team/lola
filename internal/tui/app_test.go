package tui

import (
	"slices"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
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

// 'n' on the project rail opens a NEW project, not the selected one. The form
// creates outright (it carries every [[project]] field), so 'n' must never
// preload — 'P' is the edit path.
func TestRailNewOpensBlankProjectForm(t *testing.T) {
	m := newTestRoot(t)
	m.focus = focusPolls
	m.list.cursor = 0 // a project IS selected, to prove it is not preloaded

	m.Update(keyMsg("n"))
	if m.form == nil {
		t.Fatal("'n' should open the project form")
	}
	if !m.form.isNew {
		t.Errorf("'n' must open a NEW project, got a form on %q", m.form.origName)
	}
	if m.form.poll.Name != "" {
		t.Errorf("a new form must start unnamed, got %q", m.form.poll.Name)
	}
}

// 'P' is the edit path: it preloads the selected project.
func TestRailEditPreloadsSelectedProject(t *testing.T) {
	m := newTestRoot(t)
	m.focus = focusPolls
	m.list.cursor = 0

	want := m.selectedRailProject().Name
	m.Update(keyMsg("P"))
	if m.form == nil {
		t.Fatal("'P' should open the project form")
	}
	if m.form.isNew || m.form.origName != want {
		t.Errorf("'P' must preload %q, got isNew=%v origName=%q", want, m.form.isNew, m.form.origName)
	}
}

// Paste is routed to whatever owns keyboard input, in the same precedence as
// keystrokes. Without this dispatch a tea.PasteMsg reaches nothing at all,
// because bubbletea v2 never turns a bracketed paste into key events.
func TestRoutePasteReachesTheFocusedOverlay(t *testing.T) {
	t.Run("project form", func(t *testing.T) {
		m := newTestRoot(t)
		f, _ := newFormModel(m.cfg, nil)
		m.form = f
		f.tab = tabRepo
		f.cursor = slices.Index(f.fields(), fPath)

		m.Update(tea.PasteMsg{Content: "/tmp/pasted\n"})
		if f.poll.Path != "/tmp/pasted" {
			t.Errorf("path = %q, want the pasted value", f.poll.Path)
		}
	})

	t.Run("settings form", func(t *testing.T) {
		m := newTestRoot(t)
		s := newSettingsForm(m.cfgPath, m.cfg)
		m.settings = s
		focusField(t, s, "def_branch_prefix")

		m.Update(tea.PasteMsg{Content: "feat/"})
		if got := s.field("def_branch_prefix").text; got != "feat/" {
			t.Errorf("branch prefix = %q, want feat/", got)
		}
	})

	t.Run("home add-project prompt", func(t *testing.T) {
		m := newTestRoot(t)
		m.view = viewHome
		m.home.adding, m.home.addInput = true, ""

		m.Update(tea.PasteMsg{Content: "pasted-name\n"})
		if m.home.addInput != "pasted-name" {
			t.Errorf("addInput = %q, want pasted-name", m.home.addInput)
		}
	})

	t.Run("detail worktree branch prompt", func(t *testing.T) {
		m := newTestRoot(t)
		m.view = viewDetail
		m.detail = detailModel{project: "nori-app", wtMode: true}

		m.Update(tea.PasteMsg{Content: "feat/from-clipboard"})
		if m.detail.wtBranch != "feat/from-clipboard" {
			t.Errorf("wtBranch = %q, want the pasted branch", m.detail.wtBranch)
		}
	})

	t.Run("no text field focused is dropped", func(t *testing.T) {
		m := newTestRoot(t)
		m.view = viewHome // not adding, not filtering
		m.Update(tea.PasteMsg{Content: "stray"})
		if m.home.addInput != "" || m.home.filter != "" {
			t.Errorf("a stray paste must be dropped, got add=%q filter=%q", m.home.addInput, m.home.filter)
		}
	})
}

// Space enables/disables the selected project on the rail. Like the picker's
// multi-select, this matched " " — a key string bubbletea v2 never produces.
func TestRailSpaceTogglesEnabled(t *testing.T) {
	m := newTestRoot(t)
	m.focus = focusPolls
	m.list.cursor = slices.IndexFunc(m.railProjectPtrs(), func(p *config.Project) bool {
		return p.Polls()
	})
	if m.list.cursor < 0 {
		t.Fatal("fixture has no polling project")
	}
	before := m.selectedRailProject().Enabled

	if _, cmd := m.Update(keyMsg("space")); cmd == nil {
		t.Fatal("space on the rail must issue an enable/disable command")
	}
	if got := m.selectedRailProject().Enabled; got == before {
		t.Errorf("Enabled = %v, want it toggled from %v", got, before)
	}
}
