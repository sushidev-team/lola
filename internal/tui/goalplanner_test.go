package tui

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/sushidev-team/lola/internal/goal"
	"github.com/sushidev-team/lola/internal/goalplan"
	"github.com/sushidev-team/lola/internal/protocol"
)

func plannerBrief(m *rootModel) *goalPlannerModel {
	m.openGoalPlanner(false)
	p := m.goals.planner
	p.editor.field("title").text = "Invite teammates"
	p.editor.field("outcome").lines = []string{"Owners can invite teammates and revoke pending invitations."}
	p.editor.field("acceptance").lines = []string{"Owners can send invitations.", "Revoked invitations cannot be accepted."}
	p.editor.field("non_goals").lines = []string{"Do not change billing roles."}
	return p
}
func plannerRecord(r goalplan.Request) goalplan.Record {
	return goalplan.Record{ID: r.ID, GoalID: goal.NewID(), Revision: 4, State: "proposed", Agent: "codex", Input: goalplan.Input{Request: r}, Output: &goalplan.Output{Unknowns: []string{"Confirm the invitation expiry policy."}, Tasks: []goalplan.ProposedTask{{TaskDraft: goal.TaskDraft{Title: "Implement invitations", Description: "Use the existing authorization boundary.", Acceptance: r.Acceptance, Apps: []string{"nori-app"}, Paths: map[string][]string{"nori-app": {"internal/invitations", "tests/invitations"}}}, Covers: []int{1, 2}}}}}
}
func plannerResponse(t *testing.T, data any) *protocol.Response {
	t.Helper()
	b, err := json.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}
	return &protocol.Response{OK: true, Data: b}
}

func TestGoalPlannerBriefAndFrozenRequest(t *testing.T) {
	m := goalsRoot(t)
	p := plannerBrief(m)
	p.editor.field("context").lines = []string{"nori-app:README.md", "nori-app:go.mod"}
	p.editor.field("milestone_name").text = "Team invitations"
	p.editor.cursor = len(p.editor.fields) - 1
	old := requestFn
	defer func() { requestFn = old }()
	calls := 0
	requestFn = func(req protocol.Request) (*protocol.Response, error) {
		calls++
		if req.Cmd != "goalPropose" {
			t.Fatal(req.Cmd)
		}
		var r goalplan.Request
		if err := json.Unmarshal(req.Args, &r); err != nil {
			t.Fatal(err)
		}
		if r.ID == "" || r.ID != p.requestID || r.Project != "nori-app" || r.Title != "Invite teammates" || r.MilestoneName != "Team invitations" || r.MaxTasks != 10 || r.TimeoutSeconds != 180 || len(r.ContextPaths["nori-app"]) != 2 {
			t.Fatalf("wrong request %#v", r)
		}
		return plannerResponse(t, plannerRecord(r)), nil
	}
	_, cmd := m.Update(keyMsg("enter"))
	if cmd == nil || p.requestID == "" || !p.busy {
		t.Fatal("request not reserved before dispatch")
	}
	p.editor.field("title").text = "edited after dispatch"
	if _, again := m.Update(keyMsg("enter")); again != nil {
		t.Fatal("double submission")
	}
	m.Update(cmd())
	if calls != 1 || p.record == nil || p.record.Input.Request.Title != "Invite teammates" || p.acknowledged {
		t.Fatal("response or snapshot wrong")
	}
	if cmd = m.refreshGoals(); cmd != nil {
		t.Fatal("overview may not replace reviewed proposal")
	}
}

func TestGoalPlannerAcceptanceRevisionAndUncertainRecovery(t *testing.T) {
	m := goalsRoot(t)
	p := plannerBrief(m)
	r, err := p.briefRequest(m.goals.project)
	if err != nil {
		t.Fatal(err)
	}
	record := plannerRecord(r)
	p.record = &record
	p.mode = "review"
	p.requestID = r.ID
	old := requestFn
	defer func() { requestFn = old }()
	calls := []string{}
	requestFn = func(req protocol.Request) (*protocol.Response, error) {
		calls = append(calls, req.Cmd)
		if req.Cmd == "goalAcceptPlan" {
			var a struct {
				ID       string
				Revision uint64
			}
			if err := json.Unmarshal(req.Args, &a); err != nil {
				t.Fatal(err)
			}
			if a.ID != record.ID || a.Revision != 4 {
				t.Fatalf("accepted unreviewed revision %+v", a)
			}
			return nil, errors.New("socket closed")
		}
		if req.Cmd != "goalProposal" {
			t.Fatal(req.Cmd)
		}
		record.Revision = 5
		return plannerResponse(t, record), nil
	}
	if _, cmd := m.Update(keyMsg("a")); cmd != nil {
		t.Fatal("accepted without acknowledgment")
	}
	m.Update(keyMsg("space"))
	_, cmd := m.Update(keyMsg("a"))
	if cmd == nil {
		t.Fatal("no accept action")
	}
	m.Update(cmd())
	if !p.uncertain || p.acknowledged || !strings.Contains(p.err, "Refresh") {
		t.Fatal("uncertain acceptance was hidden")
	}
	m.Update(keyMsg("space"))
	if _, cmd = m.Update(keyMsg("a")); cmd != nil {
		t.Fatal("uncertain acceptance retried")
	}
	_, cmd = m.Update(keyMsg("f"))
	if p.record != nil {
		t.Fatal("old result remained actionable during reload")
	}
	m.Update(cmd())
	if p.record == nil || p.record.Revision != 5 || p.uncertain || p.acknowledged {
		t.Fatal("fresh review not required")
	}
	if !reflect.DeepEqual(calls, []string{"goalAcceptPlan", "goalProposal"}) {
		t.Fatal(calls)
	}
}

func TestGoalPlannerSavedRecoveryAndAcceptedNavigation(t *testing.T) {
	m := goalsRoot(t)
	r := goalplan.Request{ID: goal.NewID(), Project: m.goals.project, Title: "Saved goal", Outcome: "Deliver invitations", Acceptance: []string{"Invitation accepted"}, MaxTasks: 10, TimeoutSeconds: 180}
	record := plannerRecord(r)
	old := requestFn
	defer func() { requestFn = old }()
	commands := []string{}
	requestFn = func(req protocol.Request) (*protocol.Response, error) {
		commands = append(commands, req.Cmd)
		switch req.Cmd {
		case "goalProposals":
			return plannerResponse(t, []goalplan.Summary{{ID: r.ID, Project: r.Project, Title: r.Title, State: "proposed"}}), nil
		case "goalProposal":
			return plannerResponse(t, record), nil
		case "goalAcceptPlan":
			return plannerResponse(t, goal.Goal{ID: record.GoalID, Project: r.Project}), nil
		case "goalOverview":
			return plannerResponse(t, []goal.Overview{{ID: record.GoalID, Project: r.Project, Title: r.Title}}), nil
		default:
			t.Fatal(req.Cmd)
			return nil, nil
		}
	}
	_, cmd := m.Update(keyMsg("b"))
	m.Update(cmd())
	if m.goals.planner.mode != "saved" || len(m.goals.planner.rows) != 1 {
		t.Fatal("saved proposals missing")
	}
	_, cmd = m.Update(keyMsg("enter"))
	m.Update(cmd())
	m.Update(keyMsg("space"))
	_, cmd = m.Update(keyMsg("a"))
	m.Update(cmd())
	if m.goals.planner.record.State != "accepted" {
		t.Fatal("local goal not identified")
	}
	_, cmd = m.Update(keyMsg("g"))
	if cmd == nil {
		t.Fatal("missing overview refresh")
	}
	m.Update(cmd())
	if m.goals.planner != nil || m.goals.selected != record.GoalID || !m.goals.detail {
		t.Fatal("accepted goal not opened")
	}
	if !reflect.DeepEqual(commands, []string{"goalProposals", "goalProposal", "goalAcceptPlan", "goalOverview"}) {
		t.Fatal(commands)
	}
}

func TestGoalPlannerLateRepliesAndBusyExit(t *testing.T) {
	m := goalsRoot(t)
	m.goals.loading = true
	p := plannerBrief(m)
	if m.goals.loading {
		t.Fatal("invalidated load still blocks refresh")
	}
	p.busy = true
	p.mode = "review"
	p.requestID = goal.NewID()
	p.seq = 1
	stale := goalPlannerMsg{project: m.goals.project, gen: m.goals.gen, seq: p.seq, op: "propose", id: p.requestID, err: "late failure"}
	_, cmd := m.Update(keyMsg("esc"))
	if cmd == nil || m.goals.planner != nil {
		t.Fatal("cannot leave running request")
	}
	m.openGoalPlanner(false)
	newp := m.goals.planner
	m.Update(stale)
	if newp.err != "" || newp.busy {
		t.Fatal("late response polluted new brief")
	}
	stale.gen = m.goals.gen
	stale.project = "another-project"
	stale.seq = newp.seq
	m.Update(stale)
	if newp.err != "" {
		t.Fatal("cross-project reply applied")
	}
}

func TestGoalPlannerInvalidBriefAndDiscard(t *testing.T) {
	m := goalsRoot(t)
	p := plannerBrief(m)
	for _, field := range []string{"max_tasks", "timeout"} {
		before := p.editor.field(field).text
		p.editor.field(field).text = "0"
		if _, err := p.briefRequest(m.goals.project); err == nil {
			t.Fatal("explicit zero silently defaulted", field)
		}
		p.editor.field(field).text = before
	}
	p.editor.field("context").lines = []string{"../README.md"}
	if _, err := p.briefRequest(m.goals.project); err == nil {
		t.Fatal("invalid context accepted")
	}
	p.editor.field("context").lines = nil
	focusField(t, p.editor, "outcome")
	m.Update(keyMsg("enter"))
	m.Update(keyMsg("ctrl+s"))
	if p.editor.editing {
		t.Fatal("ctrl-s must finish editing, not invoke settings save")
	}
	m.Update(keyMsg("esc"))
	if !p.discard {
		t.Fatal("missing discard guard")
	}
	m.Update(keyMsg("n"))
	if m.goals.planner != p || p.discard {
		t.Fatal("keep editing lost draft")
	}
	m.Update(keyMsg("esc"))
	m.Update(keyMsg("y"))
	if m.goals.planner != nil {
		t.Fatal("discard did not leave planner")
	}
}

func TestGoalPlannerRejectsMismatchedOrMalformedResults(t *testing.T) {
	for _, kind := range []string{"project", "id", "repair", "missing plan", "malformed"} {
		t.Run(kind, func(t *testing.T) {
			m := goalsRoot(t)
			p := plannerBrief(m)
			r, _ := p.briefRequest(m.goals.project)
			record := plannerRecord(r)
			switch kind {
			case "project":
				record.Input.Request.Project = "other"
			case "id":
				record.ID = "other"
			case "repair":
				record.Input.Repair = &goalplan.RepairContext{}
			case "missing plan":
				record.Output = nil
			}
			data, _ := json.Marshal(record)
			if kind == "malformed" {
				data = []byte("{")
			}
			p.mode = "review"
			p.requestID = r.ID
			p.busy = true
			p.seq = 1
			m.Update(goalPlannerMsg{project: m.goals.project, gen: m.goals.gen, seq: 1, op: "propose", id: r.ID, data: data})
			if !p.uncertain || p.record != nil || p.busy {
				t.Fatal("invalid result accepted")
			}
		})
	}
}

func TestGoalPlannerSavedSelectionFollowsWrappedRows(t *testing.T) {
	m := goalsRoot(t)
	m.width = 80
	m.height = 24
	p := plannerBrief(m)
	p.mode = "saved"
	for i := 0; i < 30; i++ {
		p.rows = append(p.rows, goalplan.Summary{Title: strings.Repeat("A long proposal title ", 10), State: "proposed"})
	}
	p.rows[29].Title = "Selected final proposal"
	p.selected = 29
	view := strings.Join(m.goalsLines(), "\n")
	if !strings.Contains(view, "> Selected final proposal") {
		t.Fatal("wrapped saved rows hid selection", view)
	}
}

func TestGoalPlannerCtrlCProtectsUnsentBriefAndLineEditing(t *testing.T) {
	m := goalsRoot(t)
	p := plannerBrief(m)
	original := p.editor.snapshot()
	if _, cmd := m.Update(keyMsg("ctrl+c")); cmd != nil || m.goals.planner != p {
		t.Fatal("ctrl-c quit unsent brief")
	}
	focusField(t, p.editor, "outcome")
	m.Update(keyMsg("enter"))
	if _, cmd := m.Update(keyMsg("ctrl+c")); cmd != nil || !p.editor.editing || p.editor.snapshot() != original {
		t.Fatal("ctrl-c lost or changed active line editor")
	}
	m.width, m.height = 80, 24
	view := strings.Join(m.goalsLines(), "\n")
	if !strings.Contains(view, "↑↓ line · enter new line · esc done") || strings.Contains(view, "tab field") {
		t.Fatal("line editing shows wrong controls", view)
	}
	m.Update(keyMsg("esc"))
	m.Update(keyMsg("esc"))
	if !p.discard {
		t.Fatal("escape must still offer draft discard")
	}
	m.Update(keyMsg("ctrl+c"))
	if !p.discard || m.goals.planner != p {
		t.Fatal("ctrl-c bypassed discard guard")
	}
	m.Update(keyMsg("y"))
	if _, cmd := m.Update(keyMsg("ctrl+c")); cmd == nil {
		t.Fatal("ctrl-c should still quit outside a planner")
	}
}
