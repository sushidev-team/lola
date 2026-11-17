package tui

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/sushidev-team/lola/internal/knowledge"
	"github.com/sushidev-team/lola/internal/protocol"
)

func improvementFixture(project string) knowledge.Improvement {
	return knowledge.Improvement{ID: "proposal-1", Revision: strings.Repeat("a", 64), Scope: knowledge.Scope{Project: project, Paths: []string{"resources/js/components/ui/dialog.tsx"}}, Topic: "Restore focus after closing dialogs", Problem: "Return keyboard focus to the control that opened the dialog.", Trigger: "recurring", Occurrences: 2, State: "proposed", Suggestions: []knowledge.PreventionIdea{{Kind: "regression_test", Change: "Add keyboard dismissal coverage to the shared dialog.", Validation: "Reproduce focus loss, then verify Escape and close-button return focus."}}}
}
func improvementRow(p knowledge.Improvement) knowledge.ImprovementRecord {
	return knowledge.ImprovementRecord{ID: p.ID, Revision: p.Revision, Scope: p.Scope, Topic: p.Topic, State: p.State, Available: true}
}
func improvementReply(data any) (*protocol.Response, error) {
	raw, err := json.Marshal(data)
	return &protocol.Response{OK: true, Data: raw}, err
}
func improvementRoot(t *testing.T) (*rootModel, *improvementModel) {
	t.Helper()
	m := learningRoot(t)
	_, cmd := m.Update(keyMsg("i"))
	if cmd == nil {
		t.Fatal("missing entry")
	}
	p := m.knowledge.improvements
	improvementReceive(m, "catalog", knowledge.ImprovementCatalog{Records: []knowledge.ImprovementRecord{improvementRow(improvementFixture(p.scope.Project))}, Total: 1})
	_, cmd = m.Update(keyMsg("enter"))
	if cmd == nil {
		t.Fatal("missing inspect")
	}
	improvementReceive(m, "inspect", improvementInspection{Proposal: ptrImprovement(improvementFixture(p.scope.Project))})
	return m, p
}
func ptrImprovement(p knowledge.Improvement) *knowledge.Improvement { return &p }
func improvementReceive(m *rootModel, op string, value any) tea.Cmd {
	raw, _ := json.Marshal(value)
	p := m.knowledge.improvements
	return m.handleImprovementMsg(improvementMsg{project: m.knowledge.project, gen: m.knowledge.gen, seq: p.seq, op: op, data: raw})
}
func improvementRun(t *testing.T, m *rootModel, cmd tea.Cmd) {
	t.Helper()
	if cmd == nil {
		t.Fatal("missing request")
	}
	for cmd != nil {
		msg := cmd()
		_, cmd = m.Update(msg)
	}
}
func improvementReviewForm(t *testing.T, m *rootModel, p *improvementModel) {
	t.Helper()
	p.editor.field("evidence").lines = []string{"Tracked in NOR-42 after reproducing keyboard focus loss.", "Keep the existing interaction behavior."}
	p.editor.cursor = 1
	m.Update(keyMsg("enter"))
	if p.reviewed == nil {
		t.Fatal(p.err)
	}
}
func TestImprovementsExactReadsAndPagination(t *testing.T) {
	m, p := improvementRoot(t)
	old := requestFn
	t.Cleanup(func() { requestFn = old })
	calls := []protocol.Request{}
	requestFn = func(req protocol.Request) (*protocol.Response, error) {
		calls = append(calls, req)
		var a struct {
			Scope               knowledge.Scope
			ID, Revision, After string
			Version             int
		}
		if err := json.Unmarshal(req.Args, &a); err != nil {
			t.Fatal(err)
		}
		if a.Scope.Project != p.scope.Project || a.Scope.GoalID != "" {
			t.Fatal(a)
		}
		switch req.Cmd {
		case "knowledgeImprovementSources":
			if a.ID != "proposal-1" || a.Revision != strings.Repeat("a", 64) {
				t.Fatal(a)
			}
			if a.After == "" {
				return improvementReply(knowledge.ImprovementSources{ID: a.ID, Revision: a.Revision, Scope: a.Scope, References: []knowledge.Reference{{ID: "source-a", Version: 2}}, Total: 2, NextAfter: "source-a"})
			}
			if a.After != "source-a" {
				t.Fatal(a)
			}
			return improvementReply(knowledge.ImprovementSources{ID: a.ID, Revision: a.Revision, Scope: a.Scope, References: []knowledge.Reference{{ID: "source-b", Version: 1}}, Total: 2})
		case "knowledgeVersion":
			if a.ID != "source-b" || a.Version != 1 {
				t.Fatal(a)
			}
			return improvementReply(knowledge.Lesson{ID: a.ID, Version: a.Version, Scope: a.Scope, Status: knowledge.Candidate, Proposal: knowledge.Proposal{Topic: "Historical source", Summary: "Recorded evidence"}})
		}
		t.Fatal(req.Cmd)
		return nil, nil
	}
	_, cmd := m.Update(keyMsg("s"))
	improvementRun(t, m, cmd)
	_, cmd = m.Update(keyMsg("n"))
	improvementRun(t, m, cmd)
	if len(p.sources.References) != 2 {
		t.Fatal(p.sources)
	}
	m.Update(keyMsg("down"))
	_, cmd = m.Update(keyMsg("enter"))
	improvementRun(t, m, cmd)
	if p.lesson == nil || p.lesson.Version != 1 || !strings.Contains(strings.Join(m.knowledgeLines(), "\n"), "current eligibility may differ") {
		t.Fatal("missing source history")
	}
	m.Update(keyMsg("esc"))
	if p.mode != "sources" {
		t.Fatal(p.mode)
	}
	if len(calls) != 3 {
		t.Fatal(calls)
	}
}
func TestImprovementsFrozenDecisionAndUncertainReadback(t *testing.T) {
	m, p := improvementRoot(t)
	m.Update(keyMsg("t"))
	improvementReviewForm(t, m, p)
	p.proposal.Revision = "mutated"
	p.proposal.Scope.Paths[0] = "other"
	old := requestFn
	t.Cleanup(func() { requestFn = old })
	writes := 0
	failRead := true
	requestFn = func(req protocol.Request) (*protocol.Response, error) {
		switch req.Cmd {
		case "knowledgeDecideImprovement":
			writes++
			var a knowledge.ImprovementTriage
			_ = json.Unmarshal(req.Args, &a)
			if a.ID != "proposal-1" || a.Revision != strings.Repeat("a", 64) || a.Scope.Paths[0] != "resources/js/components/ui/dialog.tsx" || a.Decision != "tracked" {
				t.Fatal(a)
			}
			return nil, errors.New("reply lost after save")
		case "knowledgeImprovement":
			if failRead {
				return nil, errors.New("offline")
			}
			v := improvementFixture(p.scope.Project)
			v.State = "tracked"
			return improvementReply(v)
		case "knowledgeImprovementDecisions":
			return improvementReply([]knowledge.ImprovementDecision{{Scope: p.scope, Revision: strings.Repeat("a", 64), Decision: "tracked", Evidence: "Tracked in NOR-42"}})
		}
		t.Fatal(req.Cmd)
		return nil, nil
	}
	_, cmd := m.Update(keyMsg("a"))
	improvementRun(t, m, cmd)
	if !p.uncertain {
		t.Fatal("uncertain not locked")
	}
	if _, cmd = m.Update(keyMsg("a")); cmd != nil {
		t.Fatal("repeated uncertain write")
	}
	_, cmd = m.Update(keyMsg("f"))
	improvementRun(t, m, cmd)
	if !p.uncertain || p.reviewed == nil {
		t.Fatal("failed read released lock")
	}
	failRead = false
	_, cmd = m.Update(keyMsg("f"))
	improvementRun(t, m, cmd)
	if p.uncertain || p.reviewed != nil || p.proposal.State != "tracked" || p.rows[0].State != "tracked" || writes != 1 {
		t.Fatal("readback failed", p, writes)
	}
}
func TestImprovementsTriagePayloadsAndConfirmedReadback(t *testing.T) {
	for key, decision := range map[string]string{"t": "tracked", "d": "dismissed", "r": "reopened"} {
		t.Run(decision, func(t *testing.T) {
			m, p := improvementRoot(t)
			m.Update(keyMsg(key))
			improvementReviewForm(t, m, p)
			old := requestFn
			t.Cleanup(func() { requestFn = old })
			calls := []string{}
			requestFn = func(req protocol.Request) (*protocol.Response, error) {
				calls = append(calls, req.Cmd)
				switch req.Cmd {
				case "knowledgeDecideImprovement":
					var a knowledge.ImprovementTriage
					_ = json.Unmarshal(req.Args, &a)
					if a.Decision != decision || a.Evidence == "" {
						t.Fatal(a)
					}
					return improvementReply(improvementFixture(p.scope.Project))
				case "knowledgeImprovement":
					return improvementReply(improvementFixture(p.scope.Project))
				case "knowledgeImprovementDecisions":
					return improvementReply([]knowledge.ImprovementDecision{})
				}
				t.Fatal(req.Cmd)
				return nil, nil
			}
			_, cmd := m.Update(keyMsg("a"))
			improvementRun(t, m, cmd)
			if p.mode != "detail" || p.reviewed != nil || p.uncertain || strings.Join(calls, ",") != "knowledgeDecideImprovement,knowledgeImprovement,knowledgeImprovementDecisions" {
				t.Fatal(calls, p)
			}
		})
	}
}
func TestImprovementsValidationPasteAndDraftProtection(t *testing.T) {
	m, p := improvementRoot(t)
	m.Update(keyMsg("d"))
	if p.review() == nil {
		t.Fatal("empty evidence accepted")
	}
	p.editor.field("evidence").lines = []string{strings.Repeat("é", 1001)}
	if p.review() == nil {
		t.Fatal("byte limit ignored")
	}
	p.editor.field("evidence").lines = []string{"invalid\x1bcontrol evidence"}
	if p.review() == nil {
		t.Fatal("control accepted")
	}
	p.editor.field("evidence").lines = nil
	m.Update(keyMsg("enter"))
	m.Update(tea.PasteMsg{Content: "Verified old evidence.\nThe claim no longer reproduces."})
	m.Update(keyMsg("esc"))
	if len(p.editor.field("evidence").lines) != 2 {
		t.Fatal("paste lost")
	}
	p.editor.cursor = 1
	m.Update(keyMsg("enter"))
	if p.reviewed == nil {
		t.Fatal(p.err)
	}
	saved := p.reviewed.Request.Evidence
	m.Update(tea.PasteMsg{Content: "do not change reviewed"})
	m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	if !p.discard {
		t.Fatal("review lost without discard")
	}
	m.Update(keyMsg("n"))
	if p.reviewed == nil || p.reviewed.Request.Evidence != saved {
		t.Fatal("keep lost review")
	}
	m.Update(keyMsg("esc"))
	m.Update(keyMsg("esc"))
	if !p.discard {
		t.Fatal("editable draft lost")
	}
	m.Update(keyMsg("y"))
	if p.editor != nil {
		t.Fatal("discard failed")
	}
}
func TestImprovementsWithdrawnHistoryAndRevisionBoundExport(t *testing.T) {
	m, p := improvementRoot(t)
	old := requestFn
	t.Cleanup(func() { requestFn = old })
	requestFn = func(req protocol.Request) (*protocol.Response, error) {
		if req.Cmd != "knowledgeExportImprovement" {
			t.Fatal(req.Cmd)
		}
		var a struct {
			ID, Revision string
			Scope        knowledge.Scope
		}
		_ = json.Unmarshal(req.Args, &a)
		if a.ID != p.proposal.ID || a.Revision != p.proposal.Revision || !sameLearningScope(a.Scope, p.scope) {
			t.Fatal(a)
		}
		return improvementReply(map[string]string{"IMPROVEMENT.md": "Reviewed proposal"})
	}
	_, cmd := m.Update(keyMsg("e"))
	improvementRun(t, m, cmd)
	if p.files["IMPROVEMENT.md"] != "Reviewed proposal" {
		t.Fatal(p.files)
	}
	m.Update(keyMsg("esc"))
	requestFn = func(req protocol.Request) (*protocol.Response, error) { return nil, errors.New("revision changed") }
	_, cmd = m.Update(keyMsg("e"))
	improvementRun(t, m, cmd)
	if len(p.files) != 0 || p.err == "" {
		t.Fatal("stale export remains")
	}
	m.Update(keyMsg("t"))
	if p.editor != nil {
		t.Fatal("error allowed triage")
	}
	p.err = ""
	p.record.Available = false
	p.proposal = nil
	p.history = []knowledge.ImprovementDecision{{Scope: p.scope, Revision: "old-revision", Decision: "dismissed", Evidence: "Contradicted by reproduction", At: time.Now()}}
	for _, key := range []string{"t", "d", "r", "e", "s"} {
		if _, cmd = m.Update(keyMsg(key)); cmd != nil || p.editor != nil {
			t.Fatal("withdrawn editable", key)
		}
	}
	requestFn = func(req protocol.Request) (*protocol.Response, error) {
		var a struct{ Revision string }
		_ = json.Unmarshal(req.Args, &a)
		if req.Cmd != "knowledgeImprovementSources" || a.Revision != "old-revision" {
			t.Fatal(req, a)
		}
		return improvementReply(knowledge.ImprovementSources{ID: p.record.ID, Revision: a.Revision, Scope: p.scope, References: []knowledge.Reference{{ID: "old-source", Version: 1}}, Total: 1})
	}
	m.Update(keyMsg("h"))
	_, cmd = m.Update(keyMsg("enter"))
	improvementRun(t, m, cmd)
	if p.sources.Revision != "old-revision" {
		t.Fatal(p.sources)
	}
}
func TestImprovementsScopePagesAndStaleReplies(t *testing.T) {
	m, p := improvementRoot(t)
	oldGen := m.knowledge.gen
	m.Update(keyMsg("esc"))
	m.Update(keyMsg("g"))
	m.Update(tea.PasteMsg{Content: "goal-1"})
	p.editor.cursor = 1
	_, cmd := m.Update(keyMsg("enter"))
	old := requestFn
	t.Cleanup(func() { requestFn = old })
	page := 0
	requestFn = func(req protocol.Request) (*protocol.Response, error) {
		var a struct {
			Scope knowledge.Scope
			After string
		}
		_ = json.Unmarshal(req.Args, &a)
		if req.Cmd != "knowledgeImprovementCatalog" || a.Scope.GoalID != "goal-1" {
			t.Fatal(req, a)
		}
		r := improvementRow(improvementFixture(p.scope.Project))
		r.Scope.GoalID = "goal-1"
		page++
		if page == 1 {
			return improvementReply(knowledge.ImprovementCatalog{Records: []knowledge.ImprovementRecord{r}, Total: 2, NextAfter: r.ID})
		}
		if a.After != r.ID {
			t.Fatal(a)
		}
		r.ID = "proposal-2"
		return improvementReply(knowledge.ImprovementCatalog{Records: []knowledge.ImprovementRecord{r}, Total: 2})
	}
	improvementRun(t, m, cmd)
	_, cmd = m.Update(keyMsg("n"))
	improvementRun(t, m, cmd)
	if len(p.rows) != 2 {
		t.Fatal(p.rows)
	}
	raw, _ := json.Marshal(knowledge.ImprovementCatalog{})
	m.handleImprovementMsg(improvementMsg{project: m.knowledge.project, gen: oldGen - 1, seq: p.seq, op: "catalog", data: raw})
	if len(p.rows) != 2 {
		t.Fatal("stale reply applied")
	}
	foreign := improvementRow(improvementFixture("foreign"))
	improvementReceive(m, "catalog", knowledge.ImprovementCatalog{Records: []knowledge.ImprovementRecord{foreign}})
	if p.err == "" || len(p.rows) != 2 {
		t.Fatal("foreign rows applied")
	}
}
func TestImprovementsRejectWrongSourceAndLesson(t *testing.T) {
	m, p := improvementRoot(t)
	m.Update(keyMsg("s"))
	improvementReceive(m, "sources", knowledge.ImprovementSources{ID: p.record.ID, Revision: "wrong", Scope: p.scope})
	if p.err == "" || p.mode != "detail" {
		t.Fatal("stale sources accepted")
	}
	p.err = ""
	p.sources = &knowledge.ImprovementSources{ID: p.record.ID, Revision: p.record.Revision, Scope: p.scope, References: []knowledge.Reference{{ID: "source", Version: 2}}}
	p.mode = "sources"
	m.Update(keyMsg("enter"))
	improvementReceive(m, "lesson", knowledge.Lesson{ID: "source", Version: 3, Scope: p.scope})
	if p.err == "" || p.lesson != nil {
		t.Fatal("wrong version accepted")
	}
}
func TestImprovementsTerminalBoundsAndHistoryScroll(t *testing.T) {
	for _, size := range [][2]int{{120, 40}, {80, 24}, {40, 12}} {
		m, p := improvementRoot(t)
		m.width, m.height = size[0], size[1]
		m.Update(keyMsg("t"))
		improvementReviewForm(t, m, p)
		p.uncertain = true
		p.err = strings.Repeat("long error ", 80)
		lines := m.knowledgeLines()
		if len(lines) > size[1] {
			t.Fatal("height")
		}
		for _, line := range lines {
			if lipgloss.Width(line) > size[0] {
				t.Fatal("width", line)
			}
		}
		joined := strings.Join(lines, "\n")
		if !strings.Contains(joined, "reload") || !strings.Contains(joined, "repeat") {
			t.Fatal("recovery keys hidden", joined)
		}
	}
	m, p := improvementRoot(t)
	m.width, m.height = 80, 24
	p.mode = "history"
	p.history = []knowledge.ImprovementDecision{{Scope: p.scope, Revision: "history-tail", Evidence: strings.Repeat("long evidence ", 200)}}
	m.Update(keyMsg("end"))
	if !strings.Contains(strings.Join(m.knowledgeLines(), "\n"), "history-tail") {
		t.Fatal("selected anchor prevents reading history end")
	}
}
func TestCaptureImprovements(t *testing.T) {
	dir := os.Getenv("LOLA_TUI_CAPTURE_DIR")
	if dir == "" {
		t.Skip("offline capture opt-in")
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	for _, size := range []struct {
		name string
		w, h int
	}{{"wide", 120, 40}, {"compact", 80, 24}} {
		m, p := improvementRoot(t)
		m.width, m.height = size.w, size.h
		capture := func(state string) {
			t.Helper()
			if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("improvements-tui-%s-%s.ansi", size.name, state)), []byte(strings.Join(m.knowledgeLines(), "\n")), 0600); err != nil {
				t.Fatal(err)
			}
		}
		p.mode = "catalog"
		capture("catalog")
		p.mode = "detail"
		capture("inspect")
		m.Update(keyMsg("t"))
		p.editor.field("evidence").lines = []string{"Tracked in NOR-42 after reproducing keyboard focus loss.", "Keep the existing interaction behavior."}
		capture("edit")
		improvementReviewForm(t, m, p)
		capture("review")
		p.scroll = 1 << 20
		capture("review-end")
		m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
		capture("discard")
		m.Update(keyMsg("n"))
		p.uncertain = true
		p.err = "Decision not confirmed: reply lost after save. It may be saved; reload, do not repeat."
		capture("uncertain")
		p.uncertain = false
		p.err = ""
		p.reviewed = nil
		p.editor = nil
		p.mode = "history"
		p.scroll = 0
		p.history = []knowledge.ImprovementDecision{{Scope: p.scope, Revision: p.record.Revision, Decision: "tracked", Evidence: "Tracked in NOR-42 after reproducing keyboard focus loss.", At: time.Date(2026, 10, 22, 10, 0, 0, 0, time.UTC)}}
		capture("history")
		p.mode = "lesson"
		p.lesson = &knowledge.Lesson{ID: "source-a", Version: 2, Scope: p.scope, Status: knowledge.Candidate, Proposal: knowledge.Proposal{Topic: p.proposal.Topic, Summary: p.proposal.Problem, Evidence: []string{"Keyboard interaction recording"}}, Source: knowledge.Source{Kind: "worker", ID: "session-a", Session: "session-a"}}
		capture("source")
		p.mode = "export"
		p.files = map[string]string{"IMPROVEMENT.md": "# Restore focus after closing dialogs\n\nReproduce keyboard focus loss before changing shared code.\nVerify Escape and the close button return focus to the trigger.\nReview in an ordinary task branch; no rules installed."}
		capture("export")
		p.mode = "detail"
		p.proposal = nil
		p.record.Available = false
		capture("withdrawn")
		p.mode = "catalog"
		m.Update(keyMsg("g"))
		capture("scope")
	}
}
