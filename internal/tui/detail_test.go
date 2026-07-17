package tui

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/sushidev-team/lola/internal/protocol"
)

// detailRoot opens the detail screen for nori-app.
func detailRoot(t *testing.T) *rootModel {
	t.Helper()
	m := homeRoot(t)
	m.Update(keyMsg("enter")) // home enter → detail
	return m
}

func TestDetailRendersStatusAndActions(t *testing.T) {
	m := detailRoot(t)
	v := stripANSI(m.detailView())
	for _, want := range []string{"nori-app", "Actions", "Sessions", "Open a PR", "acme/nori"} {
		if !strings.Contains(v, want) {
			t.Errorf("detail view missing %q:\n%s", want, v)
		}
	}
}

// 's' from detail scopes the cockpit to the project's sessions.
func TestDetailSessionsScopesCockpit(t *testing.T) {
	m := detailRoot(t)
	m.Update(keyMsg("s"))
	if m.view != viewCockpit {
		t.Fatalf("view = %d, want viewCockpit", m.view)
	}
	if m.sessions.filter.Project != "nori-app" {
		t.Errorf("scoped to %q, want nori-app", m.sessions.filter.Project)
	}
}

// A not-yet-shipped action (ticket picker) flashes instead of doing nothing.
func TestDetailGatedActionFlashes(t *testing.T) {
	m := detailRoot(t)
	m.Update(keyMsg("t")) // ticket picker not shipped yet
	if !strings.Contains(m.detail.flash, "not available") {
		t.Errorf("gated action should flash a note, got %q", m.detail.flash)
	}
	if m.view != viewDetail {
		t.Errorf("a gated action must not navigate away; view=%d", m.view)
	}
}

// 'p' opens the PR picker (nori-app has a repo configured).
func TestDetailPOpensPRPicker(t *testing.T) {
	m := detailRoot(t)
	m.Update(keyMsg("p"))
	if m.view != viewPRPicker {
		t.Fatalf("view = %d, want viewPRPicker", m.view)
	}
	if m.prpick.project != "nori-app" {
		t.Errorf("picker project = %q, want nori-app", m.prpick.project)
	}
}

// esc from detail returns to home.
func TestDetailEscReturnsHome(t *testing.T) {
	m := detailRoot(t)
	m.Update(keyMsg("esc"))
	if m.view != viewHome {
		t.Fatalf("view = %d, want viewHome", m.view)
	}
}

// 'w' opens the new-worktree prompt; a branch name creates a shell via
// cmd=openManual and drops into the scoped cockpit.
func TestDetailWorktreeCreatesShell(t *testing.T) {
	m := detailRoot(t)
	m.Update(keyMsg("w"))
	if !m.detail.wtMode {
		t.Fatal("'w' should open the new-worktree prompt")
	}
	for _, r := range "feat/x" {
		m.Update(keyMsg(string(r)))
	}
	var got []protocol.Request
	fakeRequest(t, &got, mustData(t, protocol.OpenData{Message: "created feat/x"}), nil)

	_, cmd := m.Update(keyMsg("enter"))
	runCmd(t, m, cmd)

	if m.view != viewCockpit || m.sessions.filter.Project != "nori-app" {
		t.Errorf("after create, want scoped cockpit; view=%d scope=%q", m.view, m.sessions.filter.Project)
	}
	found := false
	for _, r := range got {
		if r.Cmd == "openManual" {
			var a protocol.OpenManualArgs
			_ = json.Unmarshal(r.Args, &a)
			if a.Project == "nori-app" && a.Branch == "feat/x" {
				found = true
			}
		}
	}
	if !found {
		t.Errorf("expected a cmd=openManual for feat/x, got %+v", got)
	}
}

// 'w' then tab launches an AGENT worktree (cmd=openManual with agent=true).
func TestDetailWorktreeAgentToggle(t *testing.T) {
	m := detailRoot(t)
	m.Update(keyMsg("w"))
	m.Update(keyMsg("tab")) // toggle shell → agent
	if !m.detail.wtAgent {
		t.Fatal("tab should toggle to agent launch")
	}
	for _, r := range "feat/y" {
		m.Update(keyMsg(string(r)))
	}
	var got []protocol.Request
	fakeRequest(t, &got, mustData(t, protocol.OpenData{Message: "created feat/y"}), nil)

	_, cmd := m.Update(keyMsg("enter"))
	runCmd(t, m, cmd)

	found := false
	for _, r := range got {
		if r.Cmd == "openManual" {
			var a protocol.OpenManualArgs
			_ = json.Unmarshal(r.Args, &a)
			if a.Branch == "feat/y" && a.Agent {
				found = true
			}
		}
	}
	if !found {
		t.Errorf("expected cmd=openManual with agent=true for feat/y, got %+v", got)
	}
}

// 'e' from detail opens the project editor.
func TestDetailEditOpensProjectForm(t *testing.T) {
	m := detailRoot(t)
	m.Update(keyMsg("e"))
	if m.projForm == nil {
		t.Error("'e' should open the project editor")
	}
}

// The strip lists the project's sessions decorated from cmd=sessions data.
func TestDetailStripListsProjectSessions(t *testing.T) {
	m := detailRoot(t)
	m.sessions.data = &protocol.SessionsData{Sessions: []protocol.SessionInfo{
		{ID: "s1", Project: "nori-app", Issue: "ENG-9", Title: "fix login", Status: "needs_input"},
		{ID: "s2", Project: "other", Issue: "ENG-1", Title: "elsewhere", Status: "working"},
	}}
	v := stripANSI(m.detailView())
	if !strings.Contains(v, "ENG-9") {
		t.Errorf("strip should list this project's session ENG-9:\n%s", v)
	}
	if strings.Contains(v, "ENG-1") {
		t.Errorf("strip must not list another project's session ENG-1:\n%s", v)
	}
}
