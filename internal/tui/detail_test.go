package tui

import (
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

// A not-yet-shipped action (PR picker) flashes instead of doing nothing.
func TestDetailGatedActionFlashes(t *testing.T) {
	m := detailRoot(t)
	m.Update(keyMsg("p")) // PR picker not shipped yet
	if !strings.Contains(m.detail.flash, "not available") {
		t.Errorf("gated action should flash a note, got %q", m.detail.flash)
	}
	if m.view != viewDetail {
		t.Errorf("a gated action must not navigate away; view=%d", m.view)
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
