package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/sushidev-team/lola/internal/goal"
)

func TestCaptureGoalAcceptance(t *testing.T) {
	dir := os.Getenv("LOLA_TUI_CAPTURE_DIR")
	if dir == "" {
		t.Skip("set LOLA_TUI_CAPTURE_DIR for offline captures")
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	for _, size := range []struct {
		name string
		w, h int
	}{{"wide", 120, 40}, {"compact", 80, 24}} {
		m, p := authorizationRoot(t)
		m.width, m.height = size.w, size.h
		capture := func(state string) {
			t.Helper()
			if err := os.WriteFile(filepath.Join(dir, "acceptance-tui-"+size.name+"-"+state+".ansi"), []byte(strings.Join(m.goalsLines(), "\n")), 0600); err != nil {
				t.Fatal(err)
			}
		}
		authorizationFill(p)
		p.editor.field("require_human_acceptance").b = true
		p.focus("require_human_acceptance")
		capture("option")
		authorizationReview(t, m, p)
		m.Update(keyMsg("end"))
		capture("authorization")
		m = acceptanceRoot(t)
		m.width, m.height = size.w, size.h
		capture("pending")
		m.Update(keyMsg("h"))
		capture("review")
		m.Update(tea.PasteMsg{Content: "Verified active invitations with a new recipient and confirmed revoked links cannot grant access."})
		capture("evidence")
		m.goals.err = "Action was not confirmed: response lost. Close the action and refresh before retrying."
		capture("uncertain")
		m.goals.confirm = nil
		m.goals.err = ""
		m.goals.scroll = 0
		g := m.goals.current()
		g.State = "accepted"
		g.Revision++
		g.Actions = nil
		g.HumanAcceptance = &goal.HumanAcceptanceView{Revision: 7, Evidence: m.goals.reason, AcceptedAt: "2026-10-21T14:00:00Z"}
		capture("accepted")
	}
}
