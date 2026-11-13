package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sushidev-team/lola/internal/goalplan"
)

func TestCaptureGoalGraph(t *testing.T) {
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
		m, p := graphChanged(t)
		m.width = size.w
		m.height = size.h
		task := p.graph.output.Tasks[0]
		task.Title = "Invitation acceptance regression"
		task.Description = "Exercise expired and revoked invitation tokens through the existing acceptance endpoint."
		task.BlockedBy = []int{1}
		task.Paths = map[string][]string{"nori-app": {"tests/invitations"}}
		task.Acceptance = []string{"Expired and revoked links cannot grant access"}
		p.graph.output.Tasks = append(p.graph.output.Tasks, task)
		capture := func(state string) {
			t.Helper()
			if err := os.WriteFile(filepath.Join(dir, "graph-tui-"+size.name+"-"+state+".ansi"), []byte(strings.Join(m.goalsLines(), "\n")), 0600); err != nil {
				t.Fatal(err)
			}
		}
		capture("graph")
		m.Update(keyMsg("enter"))
		capture("task")
		focusField(t, p.editor, "paths:nori-app")
		capture("scope")
		m.Update(keyMsg("enter"))
		capture("editing")
		m.Update(keyMsg("esc"))
		m.Update(keyMsg("esc"))
		m.Update(keyMsg("m"))
		capture("notes")
		m.Update(keyMsg("esc"))
		m.Update(keyMsg("r"))
		if p.graph.review == nil {
			t.Fatal(p.err)
		}
		capture("review")
		m.Update(keyMsg("end"))
		capture("review-end")
		p.uncertain = true
		p.err = "Save response not confirmed: socket closed. Refresh the saved result before another action."
		p.scroll = 0
		capture("uncertain")
		edit := *p.graph.review
		p.record.ModelOutput = p.record.Output
		p.record.Output = &edit.Output
		p.record.Revision++
		p.record.Edits = []goalplan.Edit{{Request: edit, Revision: p.record.Revision, At: time.Date(2026, 10, 21, 18, 0, 0, 0, time.UTC)}}
		p.graph = nil
		p.mode = "review"
		p.uncertain = false
		p.err = ""
		p.scroll = 1 << 20
		capture("history")
	}
}
