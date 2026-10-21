package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sushidev-team/lola/internal/goalplan"
	"github.com/sushidev-team/lola/internal/goalwork"
)

func TestCaptureGoalPlanner(t *testing.T) {
	dir := os.Getenv("LOLA_TUI_CAPTURE_DIR")
	if dir == "" {
		t.Skip("set LOLA_TUI_CAPTURE_DIR to render offline fixtures")
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	for _, size := range []struct {
		name string
		w, h int
	}{{"wide", 120, 40}, {"compact", 80, 24}} {
		m := goalsRoot(t)
		m.width = size.w
		m.height = size.h
		p := plannerBrief(m)
		p.editor.field("context").lines = []string{"nori-app:README.md"}
		capture := func(state string) {
			t.Helper()
			if err := os.WriteFile(filepath.Join(dir, "planner-tui-"+size.name+"-"+state+".ansi"), []byte(strings.Join(m.goalsLines(), "\n")), 0600); err != nil {
				t.Fatal(err)
			}
		}
		capture("brief")
		focusField(t, p.editor, "outcome")
		m.Update(keyMsg("enter"))
		capture("editing")
		m.Update(keyMsg("esc"))
		p.editor.cursor = len(p.editor.fields) - 1
		capture("request")
		r, err := p.briefRequest(m.goals.project)
		if err != nil {
			t.Fatal(err)
		}
		record := plannerRecord(r)
		record.Input.Members = []goalplan.Member{{ID: "nori-app", Repo: "example/nori", Snapshot: goalwork.SourceSnapshot{Target: "main", Commit: strings.Repeat("a", 40), Files: []goalwork.SourceFile{{Path: "README.md"}}}}}
		p.mode = "review"
		p.requestID = r.ID
		p.record = &record
		capture("review")
		p.scroll = 12
		capture("tasks")
		p.uncertain = true
		p.err = "Acceptance response was not confirmed: socket closed. Refresh the saved result before another action."
		capture("uncertain")
		p.err = ""
		p.mode = "saved"
		p.rows = []goalplan.Summary{{ID: r.ID, Project: r.Project, Title: r.Title, State: "proposed"}}
		capture("saved")
		m.goals.planner = nil
		m.goals.current().State = "draft"
		m.goals.current().MilestoneName = "Invitations"
		m.Update(keyMsg("enter"))
		m.Update(keyMsg("u"))
		capture("publication")
	}
}

func TestGoalPlannerViewportAndSafeReview(t *testing.T) {
	for _, width := range []int{80, 120} {
		m := goalsRoot(t)
		m.width = width
		m.height = 24
		p := plannerBrief(m)
		r, _ := p.briefRequest(m.goals.project)
		record := plannerRecord(r)
		record.Output.Unknowns = []string{"Untrusted \x1b[2J\u202e text"}
		p.record = &record
		p.mode = "review"
		p.requestID = r.ID
		lines := m.goalsLines()
		view := strings.Join(lines, "\n")
		if len(lines) != 24 || !strings.Contains(view, "space acknowledge") || !strings.Contains(view, "f refresh result") || strings.Contains(view, "\x1b[2J") || strings.ContainsRune(view, '\u202e') {
			t.Fatal("review frame unsafe or controls missing", view)
		}
		m.Update(keyMsg("end"))
		view = strings.Join(m.goalsLines(), "\n")
		if !strings.Contains(view, "authorize execution") {
			t.Fatal("cannot reach complete review", view)
		}
		m.Update(keyMsg("v"))
		if !p.raw || !strings.Contains(strings.Join(p.reviewLines(), "\n"), `"input"`) {
			t.Fatal("full source record unavailable")
		}
	}
}
