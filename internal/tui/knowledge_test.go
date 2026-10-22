package tui

import (
	"encoding/json"
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

func learningRoot(t *testing.T) *rootModel {
	t.Helper()
	m := detailRoot(t)
	m.Update(keyMsg("K"))
	p := &m.knowledge
	rows := []knowledge.Lesson{{ID: "lesson", Version: 3, Scope: knowledge.Scope{Project: p.project}, Proposal: knowledge.Proposal{Topic: "Drain pending writes before shutdown", Summary: "Wait for durable writes before stopping workers.", Rationale: "A restart must preserve accepted work.", Paths: []string{"internal/daemon"}, Evidence: []string{"Shutdown integration regression"}}, Status: knowledge.Candidate, Source: knowledge.Source{Kind: "worker", ID: "session-4", Session: "session-4"}}}
	b, _ := json.Marshal(rows)
	m.handleKnowledgeMsg(knowledgeMsg{project: p.project, gen: p.gen, seq: p.seq, op: "list", data: b})
	return m
}
func learningFocus(p *knowledgeModel, key string) {
	for i, f := range p.editor.fields {
		if f.key == key {
			p.editor.cursor = i
			return
		}
	}
}
func learningFill(p *knowledgeModel) {
	p.editor.field("topic").text = "Preserve settings inheritance"
	p.editor.field("summary").lines = []string{"Keep inherited settings linked to workspace defaults."}
	p.editor.field("rationale").lines = []string{"Saving unrelated fields must not freeze inherited values."}
	p.editor.field("paths").lines = []string{"internal/config"}
	p.editor.field("evidence").lines = []string{"Operator correction from a settings review"}
}
func learningReview(t *testing.T, m *rootModel) {
	t.Helper()
	learningFocus(&m.knowledge, "review")
	m.Update(keyMsg("enter"))
	if m.knowledge.capture == nil && m.knowledge.decision == nil {
		t.Fatal(m.knowledge.err)
	}
}
func TestLearningNavigationFilterAndScope(t *testing.T) {
	m := learningRoot(t)
	p := &m.knowledge
	if m.view != viewKnowledge || len(p.rows) != 1 {
		t.Fatal("missing learning entry")
	}
	m.handleKnowledgeMsg(knowledgeMsg{project: "other", gen: p.gen, seq: p.seq, op: "list", data: json.RawMessage(`[]`)})
	if len(p.rows) != 1 {
		t.Fatal("foreign reply replaced lessons")
	}
	m.Update(keyMsg("/"))
	m.Update(tea.PasteMsg{Content: "no match"})
	if len(p.visible()) != 0 {
		t.Fatal("search did not filter")
	}
	m.Update(keyMsg("esc"))
	p.filter = ""
	m.Update(keyMsg("enter"))
	if !p.detail || !strings.Contains(strings.Join(m.knowledgeLines(), "\n"), "Not verified") {
		t.Fatal("missing provenance")
	}
	m.Update(keyMsg("esc"))
	m.Update(keyMsg("esc"))
	if m.view != viewDetail {
		t.Fatal("back should return project")
	}
}
func TestLearningCaptureFreezesIdentityAndAuthority(t *testing.T) {
	m := learningRoot(t)
	p := &m.knowledge
	m.Update(keyMsg("n"))
	learningFill(p)
	learningReview(t, m)
	if p.capture.CaptureID == "" || p.correction {
		t.Fatal("missing stable candidate identity")
	}
	if _, cmd := m.Update(keyMsg("a")); cmd != nil {
		t.Fatal("saved without acknowledgment")
	}
	old := requestFn
	defer func() { requestFn = old }()
	calls := 0
	id := p.capture.CaptureID
	requestFn = func(req protocol.Request) (*protocol.Response, error) {
		calls++
		var a knowledge.CaptureRequest
		_ = json.Unmarshal(req.Args, &a)
		if req.Cmd != "knowledgeCapture" || a.CaptureID != id || a.Proposal.Topic != "Preserve settings inheritance" {
			t.Fatal("review changed", req.Cmd, a)
		}
		l := p.rows[0]
		l.ID = "new"
		l.Version = 1
		l.Source = knowledge.Source{Kind: "operator", ID: id}
		b, _ := json.Marshal(l)
		return &protocol.Response{OK: true, Data: b}, nil
	}
	m.Update(keyMsg("space"))
	_, cmd := m.Update(keyMsg("a"))
	if cmd == nil {
		t.Fatal("no capture command")
	}
	p.editor.field("topic").text = "Changed after review"
	if _, again := m.Update(keyMsg("a")); again != nil {
		t.Fatal("duplicate pending request")
	}
	m.Update(keyMsg("esc"))
	if p.editor == nil {
		t.Fatal("left pending operation")
	}
	reply := cmd()
	_, refresh := m.Update(reply)
	if calls != 1 || p.editor != nil || refresh == nil || !strings.Contains(p.notice, "version 1") {
		t.Fatal("capture did not settle", p.err)
	}
}
func TestLearningAcceptancePreservesExpiryAndConflicts(t *testing.T) {
	m := learningRoot(t)
	p := &m.knowledge
	expiry := time.Date(2027, 1, 2, 13, 45, 12, 123000000, time.UTC)
	p.rows[0].ExpiresAt = expiry
	p.rows[0].Code = []knowledge.CodeReference{{CodeTarget: knowledge.CodeTarget{Member: "api", Path: "internal/daemon"}}}
	conflict := p.rows[0]
	conflict.ID = "older"
	conflict.Status = knowledge.Active
	conflict.Summary = "Stop workers before waiting for writes."
	p.rows = append(p.rows, conflict)
	m.Update(keyMsg("enter"))
	m.Update(keyMsg("a"))
	p.editor.field("evidence").lines = []string{"Verified with shutdown regression checks"}
	p.editor.field("replace.older").b = true
	learningReview(t, m)
	a := p.decision
	if a.ID != "lesson" || a.Version != 3 || a.Scope.Project != p.project || !a.Expires.Equal(expiry) || len(a.Supersedes) != 1 || a.Supersedes[0] != "older" || a.CodeTargets[0].Path != "internal/daemon" {
		t.Fatal("lost exact decision", a)
	}
	m.Update(keyMsg("space"))
	m.Update(keyMsg("esc"))
	if p.acknowledged || p.decision != nil {
		t.Fatal("back retained acknowledgment")
	}
	p.editor.field("expires").text = ""
	learningReview(t, m)
	if !p.decision.Expires.IsZero() {
		t.Fatal("cannot clear expiry")
	}
	m.Update(keyMsg("esc"))
	p.editor.field("watch").lines = nil
	m.Update(keyMsg("enter"))
	if p.decision != nil || !strings.Contains(p.err, "Retain") {
		t.Fatal("lost watched references")
	}
}
func TestLearningUncertainDisputeRequiresRefresh(t *testing.T) {
	m := learningRoot(t)
	p := &m.knowledge
	m.Update(keyMsg("enter"))
	m.Update(keyMsg("d"))
	p.editor.field("evidence").lines = []string{"Integration test contradicts this finding"}
	learningReview(t, m)
	old := requestFn
	defer func() { requestFn = old }()
	calls := 0
	requestFn = func(req protocol.Request) (*protocol.Response, error) {
		calls++
		var a knowledge.Acceptance
		_ = json.Unmarshal(req.Args, &a)
		if req.Cmd != "knowledgeDispute" || a.Version != 3 || a.ID != "lesson" || a.Scope.Project != p.project {
			t.Fatal("wrong dispute", a)
		}
		return &protocol.Response{Error: "lesson changed"}, nil
	}
	m.Update(keyMsg("space"))
	_, cmd := m.Update(keyMsg("a"))
	m.Update(cmd())
	if !p.uncertain || p.acknowledged {
		t.Fatal("uncertain failure not guarded")
	}
	for _, key := range []string{"a", "space", "f"} {
		if _, cmd := m.Update(keyMsg(key)); cmd != nil {
			t.Fatal("uncertain retry")
		}
	}
	_, refresh := m.Update(keyMsg("esc"))
	if refresh == nil || p.editor != nil || calls != 1 {
		t.Fatal("missing refresh recovery")
	}
}
func TestLearningDraftPasteAndCtrlCProtection(t *testing.T) {
	m := learningRoot(t)
	p := &m.knowledge
	m.Update(keyMsg("n"))
	learningFocus(p, "summary")
	m.Update(keyMsg("enter"))
	m.Update(tea.PasteMsg{Content: "Drain pending writes\nbefore stopping workers."})
	if len(p.editor.field("summary").lines) != 2 {
		t.Fatal("multiline paste lost", p.editor.field("summary").lines)
	}
	if _, cmd := m.Update(keyMsg("ctrl+c")); cmd != nil {
		t.Fatal("Ctrl+C escaped draft")
	}
	if !p.discard {
		t.Fatal("missing discard prompt")
	}
	m.Update(keyMsg("n"))
	if !p.editor.editing {
		t.Fatal("keep editing lost input")
	}
	m.Update(keyMsg("esc"))
	m.Update(keyMsg("esc"))
	if !p.discard {
		t.Fatal("Escape discarded dirty draft")
	}
}
func TestLearningCaptureCorrectionPolicyAndLateReply(t *testing.T) {
	m := learningRoot(t)
	p := &m.knowledge
	m.Update(keyMsg("n"))
	learningFill(p)
	p.editor.field("correction").b = true
	p.editor.field("policy").b = true
	learningReview(t, m)
	if !p.correction || !p.capture.Proposal.Policy || !strings.Contains(strings.Join(p.reviewLines(), "\n"), "mandatory policy proposal") {
		t.Fatal("authority not explicit")
	}
	old := requestFn
	defer func() { requestFn = old }()
	requestFn = func(req protocol.Request) (*protocol.Response, error) {
		if req.Cmd != "knowledgeCorrect" {
			t.Fatal(req.Cmd)
		}
		return &protocol.Response{Error: "uncertain"}, nil
	}
	m.Update(keyMsg("space"))
	_, cmd := m.Update(keyMsg("a"))
	msg := cmd()
	m.enterKnowledge("other")
	m.Update(msg)
	if m.knowledge.uncertain || m.knowledge.err != "" {
		t.Fatal("old project reply leaked")
	}
}
func TestLearningExportIsReadOnlyAndPolicyCannotActivate(t *testing.T) {
	m := learningRoot(t)
	p := &m.knowledge
	p.rows[0].Policy = true
	m.Update(keyMsg("enter"))
	m.Update(keyMsg("a"))
	if p.editor != nil {
		t.Fatal("policy activation offered")
	}
	m.Update(keyMsg("esc"))
	old := requestFn
	defer func() { requestFn = old }()
	calls := 0
	requestFn = func(req protocol.Request) (*protocol.Response, error) {
		calls++
		if req.Cmd != "knowledgeExport" {
			t.Fatal(req.Cmd)
		}
		return &protocol.Response{OK: true, Data: json.RawMessage(`{"LEARNINGS.md":"# Verified lessons\n\nExample evidence"}`)}, nil
	}
	_, cmd := m.Update(keyMsg("e"))
	m.Update(cmd())
	view := strings.Join(m.knowledgeLines(), "\n")
	if calls != 1 || !strings.Contains(view, "no repository files written") || !strings.Contains(view, "Example evidence") {
		t.Fatal(view)
	}
}
func TestLearningViewportAndControls(t *testing.T) {
	for _, width := range []int{80, 120} {
		m := learningRoot(t)
		m.width = width
		m.height = 24
		p := &m.knowledge
		p.rows[0].Topic = "Untrusted \x1b[2J\u202e title"
		m.Update(keyMsg("enter"))
		m.Update(keyMsg("d"))
		p.editor.field("evidence").lines = []string{"Regression contradicts the reported behavior."}
		learningReview(t, m)
		for _, key := range []string{"home", "end", "v"} {
			m.Update(keyMsg(key))
			lines := m.knowledgeLines()
			view := strings.Join(lines, "\n")
			if len(lines) != 24 || !strings.Contains(view, "a confirm decision") || strings.Contains(view, "\x1b[2J") || strings.ContainsRune(view, '\u202e') {
				t.Fatal(view)
			}
			for _, line := range lines {
				if lipgloss.Width(line) > width {
					t.Fatal("overflow", line)
				}
			}
		}
	}
}
func TestCaptureKnowledge(t *testing.T) {
	dir := os.Getenv("LOLA_TUI_CAPTURE_DIR")
	if dir == "" {
		t.Skip("set LOLA_TUI_CAPTURE_DIR for offline fixtures")
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	for _, size := range []struct {
		name string
		w, h int
	}{{"wide", 120, 40}, {"compact", 80, 24}} {
		m := learningRoot(t)
		m.width = size.w
		m.height = size.h
		p := &m.knowledge
		capture := func(state string) {
			t.Helper()
			if err := os.WriteFile(filepath.Join(dir, "knowledge-tui-"+size.name+"-"+state+".ansi"), []byte(strings.Join(m.knowledgeLines(), "\n")), 0600); err != nil {
				t.Fatal(err)
			}
		}
		capture("list")
		m.Update(keyMsg("enter"))
		capture("lesson")
		m.Update(keyMsg("a"))
		p.editor.field("evidence").lines = []string{"Regression checks confirm durable writes survive shutdown.", "Reproduced interruption and checked the retained record."}
		p.editor.field("watch").lines = []string{"nori-app:internal/daemon"}
		capture("accept")
		learningReview(t, m)
		capture("review")
		m.Update(keyMsg("end"))
		capture("review-end")
		p.uncertain = true
		p.err = "Result not confirmed: socket closed. It may already be saved. Close and refresh lessons before another decision."
		capture("uncertain")
		p.closeForm()
		p.openForm("capture")
		learningFill(p)
		learningFocus(p, "summary")
		m.Update(keyMsg("enter"))
		capture("editing")
		m.Update(keyMsg("esc"))
		learningFocus(p, "review")
		capture("capture")
		p.editor.field("summary").lines = nil
		m.Update(keyMsg("enter"))
		capture("error")
	}
}
