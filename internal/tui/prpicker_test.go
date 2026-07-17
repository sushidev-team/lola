package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/sushidev-team/lola/internal/protocol"
)

// runCmd executes a tea.Cmd, recursively unwrapping tea.Batch so every leaf
// command's message is fed back into the model.
func runCmd(t *testing.T, m *rootModel, cmd tea.Cmd) {
	t.Helper()
	if cmd == nil {
		return
	}
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, c := range batch {
			runCmd(t, m, c)
		}
		return
	}
	m.Update(msg)
}

// prPickerRoot opens the PR picker for nori-app with a canned prs response.
func prPickerRoot(t *testing.T, prs []protocol.PrRow) *rootModel {
	t.Helper()
	m := detailRoot(t)
	resp := mustData(t, protocol.PrsData{Repo: "acme/nori", PRs: prs, AgeSeconds: 3})
	fakeRequest(t, nil, resp, nil)
	_, cmd := m.Update(keyMsg("p")) // detail 'p' → enterPRPicker returns the fetch cmd
	if cmd != nil {
		m.Update(cmd()) // deliver prsMsg
	}
	return m
}

func TestPRPickerRendersRows(t *testing.T) {
	m := prPickerRoot(t, []protocol.PrRow{
		{Number: 229, Title: "fix oauth token refresh", Author: "mreit", Branch: "fix/oauth", Checks: "pass", Review: "APPROVED"},
		{Number: 240, Title: "contrib fix", Author: "ext", Branch: "patch-1", IsFork: true, Checks: "pass"},
	})
	if m.view != viewPRPicker {
		t.Fatalf("view = %d, want viewPRPicker", m.view)
	}
	v := stripANSI(m.prPickerView())
	for _, want := range []string{"open PRs", "#229", "fix oauth", "mreit", "fix/oauth", "[fork]"} {
		if !strings.Contains(v, want) {
			t.Errorf("picker view missing %q:\n%s", want, v)
		}
	}
}

// enter on a PR opens its branch as a detached shell (cmd=open) and scopes the
// cockpit to the project.
func TestPRPickerEnterOpensDetachedShell(t *testing.T) {
	m := prPickerRoot(t, []protocol.PrRow{
		{Number: 229, Title: "fix oauth", Author: "mreit", Branch: "fix/oauth"},
	})
	var got []protocol.Request
	resp := mustData(t, protocol.OpenData{SessionID: "s", Branch: "fix/oauth", Message: "opened fix/oauth"})
	fakeRequest(t, &got, resp, nil)

	_, cmd := m.Update(keyMsg("enter"))
	runCmd(t, m, cmd)
	if m.view != viewCockpit {
		t.Errorf("after open, view = %d, want viewCockpit", m.view)
	}
	if m.sessions.filter.Project != "nori-app" {
		t.Errorf("cockpit should scope to nori-app, got %q", m.sessions.filter.Project)
	}
	// One of the batched commands must be the cmd=open for the PR branch.
	found := false
	for _, r := range got {
		if r.Cmd == "open" && r.Ref == "fix/oauth" && r.Project == "nori-app" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a cmd=open for fix/oauth, got %+v", got)
	}
}

// enter on an already-open branch is refused with a flash, no cmd=open.
func TestPRPickerEnterRefusesAlreadyOpen(t *testing.T) {
	m := prPickerRoot(t, []protocol.PrRow{
		{Number: 229, Title: "fix oauth", Branch: "fix/oauth", AlreadyOpen: true},
	})
	var got []protocol.Request
	fakeRequest(t, &got, mustData(t, protocol.OpenData{}), nil)

	m.Update(keyMsg("enter"))
	if m.view != viewPRPicker {
		t.Errorf("already-open PR must not navigate; view=%d", m.view)
	}
	if !strings.Contains(m.prpick.flash, "already open") {
		t.Errorf("expected an already-open flash, got %q", m.prpick.flash)
	}
	for _, r := range got {
		if r.Cmd == "open" {
			t.Error("must not issue cmd=open for an already-open branch")
		}
	}
}

// esc returns to the project detail screen.
func TestPRPickerEscReturnsToDetail(t *testing.T) {
	m := prPickerRoot(t, nil)
	m.Update(keyMsg("esc"))
	if m.view != viewDetail {
		t.Fatalf("view = %d, want viewDetail", m.view)
	}
}

// A stale prsMsg (superseded generation) is dropped.
func TestPRPickerDropsStaleResponse(t *testing.T) {
	m := prPickerRoot(t, []protocol.PrRow{{Number: 1, Branch: "b"}})
	fresh := m.prpick.data
	// A response tagged with an old generation must not overwrite current data.
	m.handlePrsMsg(prsMsg{project: "nori-app", gen: m.prpick.gen - 1, data: &protocol.PrsData{PRs: nil}})
	if m.prpick.data != fresh {
		t.Error("a stale-generation prsMsg must be ignored")
	}
}
