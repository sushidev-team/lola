package tui

import (
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/sushidev-team/lola/internal/goalplan"
	"github.com/sushidev-team/lola/internal/goalwork"
	"github.com/sushidev-team/lola/internal/protocol"
)

func graphFixture(t *testing.T) (*rootModel, *goalPlannerModel) {
	t.Helper()
	m := goalsRoot(t)
	p := plannerBrief(m)
	r, err := p.briefRequest(m.goals.project)
	if err != nil {
		t.Fatal(err)
	}
	record := plannerRecord(r)
	record.Input.Members = []goalplan.Member{{ID: "nori-app", Repo: "example/nori", Snapshot: goalwork.SourceSnapshot{Target: "main", Commit: strings.Repeat("a", 40), Files: []goalwork.SourceFile{{Path: "README.md"}}}}}
	record.Input.Labels = []goalplan.Label{{ID: "11111111-1111-4111-8111-111111111111", Name: "Backend"}}
	p.mode = "review"
	p.record = &record
	p.requestID = r.ID
	m.Update(keyMsg("e"))
	if p.graph == nil {
		t.Fatal("editor unavailable")
	}
	return m, p
}
func graphChanged(t *testing.T) (*rootModel, *goalPlannerModel) {
	t.Helper()
	m, p := graphFixture(t)
	m.Update(keyMsg("enter"))
	p.editor.field("title").text = "Invitation service and expiry checks"
	focusField(t, p.editor, "apply")
	m.Update(keyMsg("enter"))
	m.Update(keyMsg("m"))
	p.editor.field("reason").lines = []string{"Clarify service ownership and expiry verification"}
	focusField(t, p.editor, "apply")
	m.Update(keyMsg("enter"))
	return m, p
}

func TestGoalGraphFrozenSaveAndFreshAcceptance(t *testing.T) {
	m, p := graphChanged(t)
	original, _ := json.Marshal(p.record)
	if p.record.Output.Tasks[0].Title == p.graph.output.Tasks[0].Title {
		t.Fatal("draft mutated saved proposal")
	}
	m.Update(keyMsg("r"))
	if p.graph.review == nil || p.acknowledged {
		t.Fatal("review not frozen", p.err)
	}
	reviewed := *p.graph.review
	p.graph.output.Tasks[0].Title = "changed behind review"
	if p.graph.review.Output.Tasks[0].Title != reviewed.Output.Tasks[0].Title {
		t.Fatal("review aliases draft")
	}
	old := requestFn
	defer func() { requestFn = old }()
	calls := 0
	requestFn = func(req protocol.Request) (*protocol.Response, error) {
		calls++
		if req.Cmd == "goalEditPlan" {
			var edit goalplan.EditRequest
			if err := json.Unmarshal(req.Args, &edit); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(edit, reviewed) {
				t.Fatal("sent unreviewed data", edit)
			}
			record := *p.record
			record.Revision++
			record.Output = &edit.Output
			record.ModelOutput = p.record.Output
			record.Edits = []goalplan.Edit{{Request: edit, Revision: record.Revision, At: time.Now()}}
			return plannerResponse(t, record), nil
		}
		if req.Cmd != "goalAcceptPlan" {
			t.Fatal(req.Cmd)
		}
		var a struct {
			ID       string
			Revision uint64
		}
		_ = json.Unmarshal(req.Args, &a)
		if a.ID != p.record.ID || a.Revision != 5 {
			t.Fatal("accepted stale revision", a)
		}
		return nil, errors.New("fixture stops before local goal creation")
	}
	if _, cmd := m.Update(keyMsg("s")); cmd != nil {
		t.Fatal("saved before acknowledgement")
	}
	m.Update(keyMsg("space"))
	_, cmd := m.Update(keyMsg("s"))
	if cmd == nil || !p.busy {
		t.Fatal("save not dispatched")
	}
	if _, again := m.Update(keyMsg("s")); again != nil {
		t.Fatal("duplicate save")
	}
	now, _ := json.Marshal(p.record)
	if string(now) != string(original) {
		t.Fatal("record changed before reply")
	}
	m.Update(cmd())
	if p.graph != nil || p.record.Revision != 5 || p.mode != "review" || p.acknowledged {
		t.Fatal("saved revision not installed")
	}
	if _, cmd := m.Update(keyMsg("a")); cmd != nil {
		t.Fatal("acceptance reused old acknowledgement")
	}
	m.Update(keyMsg("space"))
	_, cmd = m.Update(keyMsg("a"))
	if cmd == nil {
		t.Fatal("no acceptance")
	}
	m.Update(cmd())
	if calls != 2 {
		t.Fatal(calls)
	}
}

func TestGoalGraphUncertainSaveRefreshAndLateReplies(t *testing.T) {
	m, p := graphChanged(t)
	m.Update(keyMsg("r"))
	reviewed := *p.graph.review
	old := requestFn
	defer func() { requestFn = old }()
	calls := []string{}
	requestFn = func(req protocol.Request) (*protocol.Response, error) {
		calls = append(calls, req.Cmd)
		return nil, errors.New("socket closed")
	}
	m.Update(keyMsg("space"))
	_, cmd := m.Update(keyMsg("s"))
	m.Update(cmd())
	if !p.uncertain || p.acknowledged || p.graph.review.EditID != reviewed.EditID {
		t.Fatal("uncertain identity lost")
	}
	for _, key := range []string{"n", "e", "m", "r", "a", "s", "space", "ctrl+c"} {
		if _, cmd := m.Update(keyMsg(key)); cmd != nil {
			t.Fatal("uncertain mutation", key)
		}
	}
	if p.mode != "graphreview" {
		t.Fatal("left frozen uncertain review")
	}
	_, cmd = m.Update(keyMsg("f"))
	m.Update(cmd())
	if !p.uncertain {
		t.Fatal("failed read unlocked")
	}
	requestFn = func(req protocol.Request) (*protocol.Response, error) {
		calls = append(calls, req.Cmd)
		return plannerResponse(t, *p.record), nil
	}
	_, cmd = m.Update(keyMsg("f"))
	msg := cmd()
	// A response from an older project/generation cannot replace another editor.
	stale := msg.(goalPlannerMsg)
	stale.gen--
	m.Update(stale)
	if !p.busy || p.graph == nil {
		t.Fatal("stale read applied")
	}
	m.Update(msg)
	if p.graph != nil || p.uncertain || p.acknowledged {
		t.Fatal("fresh read did not restore current proposal")
	}
	if !reflect.DeepEqual(calls, []string{"goalEditPlan", "goalProposal", "goalProposal"}) {
		t.Fatal(calls)
	}
}

func TestGoalGraphRequiresCorrelatedSavedEdit(t *testing.T) {
	m, p := graphChanged(t)
	m.Update(keyMsg("r"))
	m.Update(keyMsg("space"))
	old := requestFn
	defer func() { requestFn = old }()
	requestFn = func(req protocol.Request) (*protocol.Response, error) { return plannerResponse(t, *p.record), nil }
	_, cmd := m.Update(keyMsg("s"))
	m.Update(cmd())
	if !p.uncertain || p.graph == nil || !strings.Contains(p.err, "edit identity") {
		t.Fatal("unattributed response accepted")
	}
}

func TestGoalGraphDependencyMovesAndRemoval(t *testing.T) {
	m, p := graphFixture(t)
	g := p.graph
	first := g.output.Tasks[0]
	first.Title = "A"
	second := first
	second.Title = "B"
	second.BlockedBy = nil
	third := first
	third.Title = "C"
	third.BlockedBy = []int{1}
	g.output.Tasks = []goalplan.ProposedTask{first, second, third}
	m.Update(keyMsg("d"))
	if g.selected != 1 || g.output.Tasks[2].BlockedBy[0] != 2 {
		t.Fatal("references not remapped")
	}
	m.Update(keyMsg("d"))
	if !strings.Contains(p.err, "prerequisite") || g.selected != 1 {
		t.Fatal("crossed dependency")
	}
	m.Update(keyMsg("x"))
	m.Update(keyMsg("y"))
	if len(g.output.Tasks) != 3 || !strings.Contains(p.err, "depends") {
		t.Fatal("removed prerequisite")
	}
	m.Update(keyMsg("up"))
	m.Update(keyMsg("x"))
	m.Update(keyMsg("n"))
	if len(g.output.Tasks) != 3 {
		t.Fatal("unconfirmed deletion")
	}
	m.Update(keyMsg("x"))
	m.Update(keyMsg("y"))
	if len(g.output.Tasks) != 2 || g.output.Tasks[1].BlockedBy[0] != 1 {
		t.Fatal("removal references wrong")
	}
	if _, err := reorderGoalTasks(g.output.Tasks, []int{0, 0}); err == nil {
		t.Fatal("duplicate order allowed")
	}
	if _, err := reorderGoalTasks(g.output.Tasks, []int{}); err == nil {
		t.Fatal("empty graph allowed")
	}
}

func TestGoalGraphFormsCoverageRoutingPathsAndValidation(t *testing.T) {
	m, p := graphFixture(t)
	p.graph.reason = "Explain the changed contract"
	m.Update(keyMsg("r"))
	if !strings.Contains(p.err, "Change a task") {
		t.Fatal("unchanged graph reviewed", p.err)
	}
	m.Update(keyMsg("n"))
	if p.mode != "graphtask" || len(p.graph.output.Tasks) != 2 {
		t.Fatal("new task missing")
	}
	f := p.editor
	f.field("title").text = "Invitation regression"
	f.field("acceptance").lines = []string{"Expired tokens cannot be reused"}
	f.field("cover:1").b = true
	f.field("dep:0").b = true
	f.field("app:nori-app").b = true
	f.field("paths:nori-app").lines = []string{"tests/invitations"}
	f.field("label:" + p.record.Input.Labels[0].ID).b = true
	focusField(t, f, "apply")
	m.Update(keyMsg("enter"))
	added := p.graph.output.Tasks[1]
	if !reflect.DeepEqual(added.Covers, []int{2}) || !reflect.DeepEqual(added.BlockedBy, []int{1}) || len(added.LabelIDs) != 1 || !reflect.DeepEqual(added.Paths["nori-app"], []string{"tests/invitations"}) {
		t.Fatal(added)
	}
	p.graph.output.Tasks[0].Covers = []int{1}
	m.Update(keyMsg("r"))
	if p.mode != "graphreview" {
		t.Fatal("valid split rejected", p.err)
	}
	m.Update(keyMsg("esc"))
	if p.graph.review != nil || p.acknowledged {
		t.Fatal("review retained after edit")
	}
	p.graph.output.Tasks[1].Paths["nori-app"] = nil
	m.Update(keyMsg("r"))
	if p.mode == "graphreview" || !strings.Contains(p.err, "writable") {
		t.Fatal("read-only deliverable allowed")
	}
	p.graph.output.Tasks[1].Paths["nori-app"] = []string{"tests/invitations"}
	p.graph.output.Tasks[1].Covers = []int{1}
	m.Update(keyMsg("r"))
	if !strings.Contains(p.err, "every goal acceptance") {
		t.Fatal("uncovered criterion allowed", p.err)
	}
}

func TestGoalGraphPasteDiscardAndModeGuards(t *testing.T) {
	m, p := graphFixture(t)
	m.Update(keyMsg("enter"))
	focusField(t, p.editor, "description")
	m.Update(keyMsg("enter"))
	m.Update(tea.PasteMsg{Content: "\nSecond line\nThird line"})
	if !strings.Contains(strings.Join(p.editor.field("description").lines, "\n"), "Second line\nThird line") {
		t.Fatal("multiline paste lost")
	}
	m.Update(keyMsg("ctrl+c"))
	if p.graph.discard == "" {
		t.Fatal("ctrl-c discarded form")
	}
	before := strings.Join(p.editor.field("description").lines, "\n")
	m.Update(tea.PasteMsg{Content: "dropped"})
	if strings.Join(p.editor.field("description").lines, "\n") != before {
		t.Fatal("paste bypassed discard")
	}
	m.Update(keyMsg("n"))
	m.Update(keyMsg("esc"))
	focusField(t, p.editor, "apply")
	m.Update(keyMsg("enter"))
	if !strings.Contains(p.graph.output.Tasks[0].Description, "Second line") {
		t.Fatal("form not retained")
	}
	m.Update(keyMsg("esc"))
	if p.graph.discard != "graph" {
		t.Fatal("graph discard unguarded")
	}
	m.Update(keyMsg("n"))
	if p.graph == nil {
		t.Fatal("draft lost")
	}
	m.Update(keyMsg("esc"))
	m.Update(keyMsg("y"))
	if p.graph != nil || p.mode != "review" {
		t.Fatal("discard did not return to saved proposal")
	}
	if strings.Contains(p.record.Output.Tasks[0].Description, "Second line") {
		t.Fatal("discard mutated proposal")
	}
	for _, state := range []string{"accepted", "failed"} {
		p.record.State = state
		m.Update(keyMsg("e"))
		if p.graph != nil {
			t.Fatal("edited", state)
		}
	}
	p.record.State = "proposed"
	p.uncertain = true
	m.Update(keyMsg("e"))
	if p.graph != nil {
		t.Fatal("edited uncertain result")
	}
}

func TestGoalPlannerBriefPaste(t *testing.T) {
	m := goalsRoot(t)
	p := plannerBrief(m)
	focusField(t, p.editor, "acceptance")
	m.Update(keyMsg("enter"))
	m.Update(tea.PasteMsg{Content: "\nNew pasted criterion"})
	if !slices.Contains(p.editor.field("acceptance").lines, "New pasted criterion") {
		t.Fatal("planner paste not routed")
	}
	before := strings.Join(p.editor.field("acceptance").lines, "\n")
	p.busy = true
	m.Update(tea.PasteMsg{Content: "drop"})
	if strings.Join(p.editor.field("acceptance").lines, "\n") != before {
		t.Fatal("paste changed pending request")
	}
}

func TestGoalGraphViewportAndSanitization(t *testing.T) {
	for _, width := range []int{80, 120} {
		m, p := graphChanged(t)
		m.width = width
		m.height = 24
		p.graph.output.Unknowns = []string{"Untrusted \x1b[2J\u202e content"}
		for _, mode := range []string{"graph", "graphtask", "graphnotes", "graphreview"} {
			p.mode = mode
			if mode == "graphtask" {
				p.editor = p.graphTaskForm()
			}
			if mode == "graphnotes" {
				p.editor = p.graphNotesForm()
			}
			if mode == "graphreview" {
				if err := p.prepareGraphReview(); err != nil {
					t.Fatal(err)
				}
			}
			for _, uncertain := range []bool{false, true} {
				p.uncertain = uncertain
				lines := m.goalsLines()
				view := strings.Join(lines, "\n")
				if len(lines) != 24 || strings.Contains(view, "\x1b[2J") || strings.ContainsRune(view, '\u202e') {
					t.Fatal("unsafe frame", mode, view)
				}
				for _, line := range lines {
					if lipgloss.Width(line) > width {
						t.Fatal("overflow", mode, line)
					}
				}
				if uncertain && !strings.Contains(view, "f refresh saved") {
					t.Fatal("recovery not visible", view)
				}
			}
		}
	}
}

func TestGoalGraphLongHelpAndErrorsKeepControls(t *testing.T) {
	m, p := graphFixture(t)
	m.width = 80
	m.height = 24
	p.record.Input.Request.Acceptance[0] = strings.Repeat("Long criterion ", 200)
	m.Update(keyMsg("enter"))
	focusField(t, p.editor, "cover:0")
	p.err = strings.Repeat("Long response ", 200)
	view := strings.Join(m.goalsLines(), "\n")
	if !strings.Contains(view, "space toggle") || !strings.Contains(view, "esc back") || !strings.Contains(view, "Criterion 1") {
		t.Fatal("footer consumed editor or actions", view)
	}
}

func TestGoalGraphRefreshShowsPendingState(t *testing.T) {
	m, p := graphChanged(t)
	m.Update(keyMsg("r"))
	p.uncertain = true
	p.busy = true
	view := strings.Join(m.goalsLines(), "\n")
	if !strings.Contains(view, "Waiting for this operation") || strings.Contains(view, "f refresh saved proposal") {
		t.Fatal("pending refresh shows available actions", view)
	}
	if strings.Contains(view, "Open questions from the coordinator") {
		t.Fatal("operator questions misattributed")
	}
}
