package tui

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/sushidev-team/lola/internal/goal"
	"github.com/sushidev-team/lola/internal/protocol"
)

func goalsRoot(t *testing.T) *rootModel {
	m := detailRoot(t)
	m.Update(keyMsg("g"))
	m.handleGoalsMsg(goalsMsg{project: m.goals.project, gen: m.goals.gen, rows: []goal.Overview{{ID: "g", Title: "Invitations", Project: "nori-app", Revision: 7, State: "running", Actions: []string{"pause", "cancel"}, Tasks: []goal.TaskView{{ID: "t", Title: "API"}}}}})
	return m
}
func TestGoalsNavigationAndStaleLoad(t *testing.T) {
	m := goalsRoot(t)
	if m.view != viewGoals {
		t.Fatal("missing goal entry")
	}
	m.handleGoalsMsg(goalsMsg{project: "other", gen: m.goals.gen, rows: []goal.Overview{{ID: "bad"}}})
	m.handleGoalsMsg(goalsMsg{project: m.goals.project, gen: m.goals.gen - 1, rows: []goal.Overview{{ID: "bad"}}})
	if m.goals.selected != "g" {
		t.Fatal("stale response replaced goal")
	}
	m.Update(keyMsg("enter"))
	if !m.goals.detail || !strings.Contains(stripANSI(strings.Join(m.goalsLines(), "\n")), "Invitations") {
		t.Fatal("goal not inspectable")
	}
	m.Update(keyMsg("esc"))
	if m.view != viewGoals || m.goals.detail {
		t.Fatal("back should show list")
	}
	m.Update(keyMsg("esc"))
	if m.view != viewDetail {
		t.Fatal("back should show project")
	}
}
func TestGoalsControlPinsReviewedRevisionAndNeverRetries(t *testing.T) {
	m := goalsRoot(t)
	m.Update(keyMsg("enter"))
	m.Update(keyMsg("p"))
	if m.goals.confirm == nil || m.goals.confirm.Revision != 7 {
		t.Fatal("action not pinned")
	}
	if m.refreshGoals() != nil {
		t.Fatal("poll refreshed during review")
	}
	old := requestFn
	defer func() { requestFn = old }()
	calls := 0
	requestFn = func(req protocol.Request) (*protocol.Response, error) {
		calls++
		var a goal.ControlRequest
		if err := json.Unmarshal(req.Args, &a); err != nil {
			t.Fatal(err)
		}
		if req.Cmd != "goalControl" || a.ID != "g" || a.Revision != 7 || a.Action != "pause" {
			t.Fatalf("wrong target %+v", a)
		}
		return &protocol.Response{Error: "goal changed; reload before steering"}, nil
	}
	_, cmd := m.Update(keyMsg("enter"))
	if cmd == nil {
		t.Fatal("no action")
	}
	m.Update(cmd())
	if calls != 1 || m.goals.confirm == nil || !strings.Contains(m.goals.err, "reload") {
		t.Fatal("stale action not surfaced")
	}
	_, cmd = m.Update(keyMsg("enter"))
	if cmd != nil {
		t.Fatal("uncertain action retried")
	}
	m.Update(keyMsg("esc"))
	if m.goals.confirm != nil {
		t.Fatal("cannot exit failed action")
	}
}
func TestGoalsReadFailureDisablesControlsAndTextIsSafe(t *testing.T) {
	m := goalsRoot(t)
	m.goals.err = "daemon unavailable"
	m.Update(keyMsg("enter"))
	m.Update(keyMsg("c"))
	if m.goals.confirm != nil {
		t.Fatal("failed read advertised action")
	}
	if got := goalText("hi\x1b[31mRED\x1b[0m\n\u202e"); strings.ContainsRune(got, '\x1b') || strings.ContainsRune(got, '\u202e') {
		t.Fatal("terminal control leaked")
	}
}
