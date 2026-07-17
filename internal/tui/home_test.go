package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/protocol"
)

// homeRoot builds newTestRoot but opened on the project-list home screen, the
// way Run() does.
func homeRoot(t *testing.T) *rootModel {
	t.Helper()
	m := newTestRoot(t)
	m.view = viewHome
	m.home = newHomeModel()
	m.home.repin(m.cfg)
	return m
}

// Home renders every configured project from LOCAL config, with the poll
// posture derived from config — no daemon data required.
func TestHomeRendersProjectsFromConfig(t *testing.T) {
	m := homeRoot(t)
	v := stripANSI(m.homeView())
	for _, want := range []string{"projects", "PROJECT", "nori-app", "1 on"} {
		if !strings.Contains(v, want) {
			t.Errorf("home view missing %q:\n%s", want, v)
		}
	}
}

// cmd=projects decorates the rows with live status; the fetch goes through the
// injectable requestFn seam.
func TestHomeDecoratesFromProjectsCmd(t *testing.T) {
	m := homeRoot(t)
	var got []protocol.Request
	resp := mustData(t, protocol.ProjectsData{Projects: []protocol.ProjectInfo{
		{Name: "nori-app", PathOK: true, PollCount: 2, PollsEnabled: 2, LiveCounted: 3, NeedsYou: 2, CIRed: 1},
	}})
	fakeRequest(t, &got, resp, nil)

	m.Update(fetchProjectsCmd())

	if len(got) != 1 || got[0].Cmd != "projects" {
		t.Fatalf("issued request = %+v, want cmd=projects", got)
	}
	if m.home.data == nil {
		t.Fatal("home.data not set from projectsMsg")
	}
	v := stripANSI(m.homeView())
	for _, want := range []string{"2 need", "1 ci"} {
		if !strings.Contains(v, want) {
			t.Errorf("decorated home missing %q:\n%s", want, v)
		}
	}
}

// A dial failure blanks decoration but keeps the rows (rendered from config).
func TestHomeDaemonDownStillRenders(t *testing.T) {
	m := homeRoot(t)
	fakeRequest(t, nil, nil, errDaemonDown)
	m.Update(fetchProjectsCmd())

	if !m.home.daemonDown || m.home.data != nil {
		t.Fatalf("expected daemonDown with nil data, got down=%v data=%v", m.home.daemonDown, m.home.data)
	}
	if v := stripANSI(m.homeView()); !strings.Contains(v, "nori-app") {
		t.Errorf("home must still list projects with the daemon down:\n%s", v)
	}
}

// enter on a project opens its detail screen.
func TestHomeEnterOpensDetail(t *testing.T) {
	m := homeRoot(t)
	m.Update(keyMsg("enter"))
	if m.view != viewDetail {
		t.Fatalf("view = %d, want viewDetail", m.view)
	}
	if m.detail.project != "nori-app" {
		t.Errorf("detail.project = %q, want nori-app", m.detail.project)
	}
}

// The main cockpit is the root: 'p' opens the projects pane.
func TestCockpitPOpensProjects(t *testing.T) {
	m := newTestRoot(t) // defaults to viewCockpit
	m.Update(keyMsg("p"))
	if m.view != viewHome {
		t.Fatalf("view = %d, want viewHome after 'p'", m.view)
	}
}

// esc in a project-scoped cockpit clears the scope back to the global (main)
// all-sessions view, staying in the cockpit.
func TestCockpitEscClearsProjectScope(t *testing.T) {
	m := newTestRoot(t)
	m.sessions.filter.Project = "nori-app"
	m.Update(keyMsg("esc"))
	if m.view != viewCockpit {
		t.Errorf("esc from a scoped cockpit must stay in the cockpit; view=%d", m.view)
	}
	if m.sessions.filter.Project != "" {
		t.Errorf("esc must clear the project scope, got %q", m.sessions.filter.Project)
	}
}

// esc in the projects pane returns to the main cockpit.
func TestHomeEscReturnsToCockpit(t *testing.T) {
	m := homeRoot(t)
	m.Update(keyMsg("esc"))
	if m.view != viewCockpit {
		t.Fatalf("view = %d, want viewCockpit after esc", m.view)
	}
}

// 'a' collects a name inline, creates the stub project, and opens its editor.
func TestHomeAddProject(t *testing.T) {
	m := homeRoot(t)
	m.Update(keyMsg("a"))
	if !m.home.adding {
		t.Fatal("'a' should enter add mode")
	}
	for _, r := range "ponzu" {
		m.Update(keyMsg(string(r)))
	}
	m.Update(keyMsg("enter"))

	reloaded, err := config.Load(m.cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.ProjectByName("ponzu") == nil {
		t.Error("new project not persisted to config")
	}
	if m.projForm == nil {
		t.Error("project editor should open after add")
	}
}

// 'x' then 'y' removes a project and its polls from config.
func TestHomeRemoveProject(t *testing.T) {
	m := homeRoot(t)
	m.home.selName = "nori-app"
	m.home.repin(m.cfg)

	m.Update(keyMsg("x"))
	if !m.home.confirmRemove {
		t.Fatal("'x' should ask to confirm removal")
	}
	m.Update(keyMsg("y"))

	reloaded, err := config.Load(m.cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.ProjectByName("nori-app") != nil {
		t.Error("project not removed from config (its polling goes with it)")
	}
}

// space pauses a polling project's polling.
func TestHomeTogglePolling(t *testing.T) {
	t.Setenv("LOLA_HOME", t.TempDir())
	path, err := config.DefaultPath()
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		Defaults: config.Defaults{PollInterval: time.Minute, GlobalCap: 4},
		Projects: []config.Project{{
			Name: "solo", Path: "/tmp/solo", DefaultBranch: "main",
			Enabled: true, TeamID: "t1", CycleMode: "none", MatchMode: "any",
			AssigneeMode: "anyone", DedupMode: "seen", ConcurrencyCap: 1,
		}},
	}
	if err := cfg.Save(path); err != nil {
		t.Fatal(err)
	}
	loaded, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	m := &rootModel{cfgPath: path, cfg: loaded, view: viewHome, home: newHomeModel(), width: 120, height: 40}
	m.home.repin(m.cfg)

	m.Update(keyMsg("space"))

	reloaded, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if p := reloaded.ProjectByName("solo"); p == nil || p.Enabled {
		t.Errorf("space should pause the project's polling, got %+v", p)
	}
}
