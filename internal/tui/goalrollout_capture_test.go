package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sushidev-team/lola/internal/goal"
)

func TestCaptureGoalRollout(t *testing.T) {
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
		authorizationFill(p)
		p.editor.field("compatibility").lines = []string{"Keep the previous app compatible with the new API.", "Keep new invitation features disabled until the API deployment is verified."}
		capture := func(state string) {
			t.Helper()
			if err := os.WriteFile(filepath.Join(dir, "rollout-tui-"+size.name+"-"+state+".ansi"), []byte(strings.Join(m.goalsLines(), "\n")), 0600); err != nil {
				t.Fatal(err)
			}
		}
		p.focus("compatibility")
		capture("fields")
		m.Update(keyMsg("enter"))
		capture("editing")
		m.Update(keyMsg("esc"))
		authorizationReview(t, m, p)
		for i, line := range p.reviewLines() {
			if line == "Reviewed rollout contract" {
				p.scroll = i
				break
			}
		}
		capture("review")
		p.uncertain, p.err = true, "Authorization was not confirmed: fixture response lost. Close and refresh Goals before another action."
		capture("uncertain")
		rollout := p.reviewed.Policy.Rollout
		m = acceptanceRoot(t)
		m.width, m.height = size.w, size.h
		g := m.goals.current()
		g.RequireHumanAcceptance = false
		g.Actions = nil
		g.Rollout = rollout
		g.Members = append(g.Members, goal.MemberView{ID: "app", Repo: "example/nori-app", Target: "main", Commit: strings.Repeat("b", 40), DeliveryState: "published"})
		m.goals.scroll = 999
		capture("saved")
		g.Rollout = nil
		m.goals.scroll = 999
		capture("legacy")
	}
}
