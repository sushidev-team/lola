package tui

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/sushidev-team/lola/internal/goal"
	"github.com/sushidev-team/lola/internal/protocol"
)

func recoverySnapshot() goal.RecoveryView {
	g := authorizationOptions().Goal
	g.State = "blocked"
	g.SyncError = "Linear brief changed; review its retained source."
	g.Reason = "Execution is held pending reconciliation."
	b := goal.BriefContract{Title: g.Title, Outcome: g.Outcome, Acceptance: g.Acceptance, NonGoals: g.NonGoals}
	return goal.RecoveryView{Goal: g, Brief: b, PendingBrief: &goal.BriefSource{ID: "source-clarification-001", Title: "Goal — Invite teammates", Description: "Owners can invite teammates and revoke pending invitations.\n\nClarification: preserve keyboard access and keep validation next to the affected field."}, LatestProgress: &goal.ProgressUpdate{ID: "progress-001", Summary: "Execution: blocked.\n\nThe brief changed; review it before new work.", SentAt: time.Date(2026, 10, 22, 8, 0, 0, 0, time.UTC)}, Actions: []string{"sync-brief", "accept-brief", "dismiss-progress", "delivery-evidence", "retry-verification", "retry-replan"}}
}
func recoveryRoot(t *testing.T) (*rootModel, *goalRecoveryModel) {
	t.Helper()
	m := goalsRoot(t)
	m.goals.detail = true
	m.openGoalRecovery()
	p := m.goals.recovery
	raw, _ := json.Marshal(recoverySnapshot())
	m.Update(goalRecoveryMsg{project: m.goals.project, id: p.id, gen: m.goals.gen, seq: p.seq, op: "load", data: raw})
	if p.snapshot == nil {
		t.Fatal(p.err)
	}
	return m, p
}
func recoveryFocus(p *goalRecoveryModel, key string) {
	for i, f := range p.editor.fields {
		if f.key == key {
			p.editor.cursor = i
			return
		}
	}
}
func recoveryReview(t *testing.T, m *rootModel, p *goalRecoveryModel) {
	t.Helper()
	recoveryFocus(p, "review")
	m.Update(keyMsg("enter"))
	if p.reviewed == nil {
		t.Fatal(p.err)
	}
}
func TestGoalRecoveryReadAndFrozenBrief(t *testing.T) {
	m := goalsRoot(t)
	m.goals.detail = true
	old := requestFn
	t.Cleanup(func() { requestFn = old })
	var requests []protocol.Request
	requestFn = func(req protocol.Request) (*protocol.Response, error) {
		requests = append(requests, req)
		if req.Cmd == "goalRecovery" {
			return plannerResponse(t, recoverySnapshot()), nil
		}
		return &protocol.Response{OK: true}, nil
	}
	_, cmd := m.Update(keyMsg("e"))
	if cmd == nil {
		t.Fatal("no local read")
	}
	m.Update(cmd())
	p := m.goals.recovery
	if len(requests) != 1 || requests[0].Cmd != "goalRecovery" || string(requests[0].Args) != `{"id":"g"}` || m.refreshGoals() != nil {
		t.Fatal("not isolated local inspection")
	}
	m.Update(keyMsg("tab"))
	m.Update(keyMsg("enter"))
	if p.action != "accept-brief" {
		t.Fatal(p.action)
	}
	p.editor.field("reason").lines = []string{"Existing tasks cover keyboard access and field validation."}
	p.editor.field("outcome").lines = []string{"Owners can invite teammates with keyboard access and inline validation."}
	recoveryReview(t, m, p)
	want := goal.BriefAcceptance{ID: "g", Revision: 7, SourceID: "source-clarification-001", Contract: p.reviewed.contract, Reason: p.reviewed.reason}
	p.snapshot.Goal.Revision = 99
	p.snapshot.PendingBrief.ID = "mutated"
	p.snapshot.Brief.Acceptance[0] = "mutated"
	p.editor.field("outcome").lines = []string{"mutated"}
	m.Update(tea.PasteMsg{Content: "should not change review"})
	_, cmd = m.Update(keyMsg("a"))
	if cmd == nil {
		t.Fatal("no apply")
	}
	if _, again := m.Update(keyMsg("a")); again != nil {
		t.Fatal("repeat while busy")
	}
	msg := cmd()
	if requests[1].Cmd != "goalAcceptBrief" {
		t.Fatal(requests[1].Cmd)
	}
	var got goal.BriefAcceptance
	json.Unmarshal(requests[1].Args, &got)
	if !reflect.DeepEqual(want, got) || p.reviewed.snapshot.Brief.Acceptance[0] == "mutated" {
		t.Fatalf("review mutated: %+v", got)
	}
	_, reload := m.Update(msg)
	if reload == nil || !p.uncertain {
		t.Fatal("missing post-success readback lock")
	}
	m.Update(reload())
	if p.uncertain || p.reviewed != nil || len(requests) != 3 || requests[2].Cmd != "goalRecovery" {
		t.Fatal("no current-state recovery")
	}
}
func TestGoalRecoveryUncertainReloadAndStaleResponses(t *testing.T) {
	m, p := recoveryRoot(t)
	p.choose("retry-verification")
	recoveryReview(t, m, p)
	old := requestFn
	t.Cleanup(func() { requestFn = old })
	writes, reads := 0, 0
	requestFn = func(req protocol.Request) (*protocol.Response, error) {
		if req.Cmd == "goalRecovery" {
			reads++
			if reads == 1 {
				return nil, errors.New("offline")
			}
			s := recoverySnapshot()
			s.Goal.Revision = 8
			s.Actions = nil
			return plannerResponse(t, s), nil
		}
		writes++
		return nil, errors.New("reply lost")
	}
	_, cmd := m.Update(keyMsg("a"))
	m.Update(cmd())
	if !p.uncertain || p.reviewed == nil {
		t.Fatal("uncertain decision discarded")
	}
	for _, key := range []string{"a", "enter"} {
		if _, cmd = m.Update(keyMsg(key)); cmd != nil {
			t.Fatal("replayed uncertain request")
		}
	}
	_, cmd = m.Update(keyMsg("f"))
	m.Update(cmd())
	if !p.uncertain || p.reviewed == nil || !strings.Contains(p.err, "offline") {
		t.Fatal("failed reload unlocked old decision")
	}
	stale := goalRecoveryMsg{project: m.goals.project, id: p.id, gen: m.goals.gen, seq: p.seq - 1, op: "load", data: json.RawMessage(`{}`)}
	m.Update(stale)
	if !p.uncertain || p.reviewed == nil {
		t.Fatal("stale response applied")
	}
	_, cmd = m.Update(keyMsg("f"))
	m.Update(cmd())
	if p.uncertain || p.reviewed != nil || p.snapshot.Goal.Revision != 8 || writes != 1 || reads != 2 {
		t.Fatal("wrong recovery")
	}
	raw, _ := json.Marshal(recoverySnapshot())
	m.Update(goalRecoveryMsg{project: "other", id: p.id, gen: m.goals.gen, seq: p.seq, op: "load", data: raw})
	if p.snapshot.Goal.Revision != 8 {
		t.Fatal("foreign response applied")
	}
	m.Update(goalRecoveryMsg{project: m.goals.project, id: "other", gen: m.goals.gen, seq: p.seq, op: "load", data: raw})
	if p.snapshot.Goal.Revision != 8 {
		t.Fatal("wrong goal applied")
	}
}
func TestGoalRecoveryActionPayloads(t *testing.T) {
	for _, action := range []string{"sync-brief", "dismiss-progress", "delivery-evidence", "retry-verification", "retry-replan"} {
		t.Run(action, func(t *testing.T) {
			m, p := recoveryRoot(t)
			p.choose(action)
			if f := p.editor.field("reason"); f != nil {
				f.lines = []string{"Inspected retained evidence."}
			}
			if f := p.editor.field("url"); f != nil {
				f.text = "https://artifacts.example/verified"
			}
			recoveryReview(t, m, p)
			old := requestFn
			t.Cleanup(func() { requestFn = old })
			var got protocol.Request
			requestFn = func(req protocol.Request) (*protocol.Response, error) {
				got = req
				return &protocol.Response{OK: true}, nil
			}
			_, cmd := m.Update(keyMsg("a"))
			if cmd == nil {
				t.Fatal("no action")
			}
			cmd()
			var payload map[string]any
			json.Unmarshal(got.Args, &payload)
			if payload["id"] != "g" {
				t.Fatal(payload)
			}
			if action == "sync-brief" {
				if got.Cmd != "goalSyncBrief" || len(payload) != 1 {
					t.Fatal(got)
				}
				return
			}
			if payload["revision"] != float64(7) {
				t.Fatal(payload)
			}
			switch action {
			case "dismiss-progress":
				if got.Cmd != "goalDismissProgress" || payload["update_id"] != "progress-001" || payload["reason"] != "Inspected retained evidence." {
					t.Fatal(got)
				}
			case "delivery-evidence":
				if got.Cmd != "goalDeliveryEvidence" || payload["url"] != "https://artifacts.example/verified" {
					t.Fatal(got)
				}
			default:
				if got.Cmd != "goalControl" || payload["action"] != action || payload["reason"] != "Inspected retained evidence." {
					t.Fatal(got)
				}
			}
		})
	}
}
func TestGoalRecoveryValidationAndPaste(t *testing.T) {
	m, p := recoveryRoot(t)
	p.choose("accept-brief")
	if p.review() == nil {
		t.Fatal("missing reason accepted")
	}
	recoveryFocus(p, "reason")
	m.Update(keyMsg("enter"))
	m.Update(tea.PasteMsg{Content: "Existing tasks cover this.\nKeep scope unchanged."})
	m.Update(keyMsg("esc"))
	if len(p.editor.field("reason").lines) != 2 {
		t.Fatal("multiline paste lost")
	}
	p.editor.field("title").text = strings.Repeat("é", 121)
	if p.review() == nil {
		t.Fatal("UTF-8 title limit ignored")
	}
	p.editor.field("title").text = "Invite teammates"
	p.editor.field("acceptance").lines = []string{strings.Repeat("é", 1001)}
	if p.review() == nil {
		t.Fatal("UTF-8 criterion limit ignored")
	}
	p.editor.field("acceptance").lines = []string{"Keyboard access is preserved."}
	p.editor.field("reason").lines = []string{strings.Repeat("é", 1001)}
	if p.review() == nil {
		t.Fatal("UTF-8 reason limit ignored")
	}
	p.editor.field("reason").lines = []string{"Keep existing scope."}
	if err := p.review(); err != nil {
		t.Fatal(err)
	}
	p.reviewed = nil
	p.editor = nil
	p.choose("delivery-evidence")
	for _, url := range []string{"", "http://example.com", "https://user:password@example.com", "https://example.com/\nsecret"} {
		p.editor.field("url").text = url
		if p.review() == nil {
			t.Fatalf("accepted %q", url)
		}
	}
}
func TestGoalRecoveryDiscardAndInspection(t *testing.T) {
	m, p := recoveryRoot(t)
	p.choose("accept-brief")
	p.editor.field("reason").lines = []string{"Retain tasks."}
	m.Update(tea.KeyPressMsg{Code: 'v', Mod: tea.ModCtrl})
	if !p.inspect {
		t.Fatal("no context inspector")
	}
	m.Update(tea.PasteMsg{Content: "must not edit"})
	m.Update(keyMsg("esc"))
	if strings.Join(p.editor.field("reason").lines, "") != "Retain tasks." {
		t.Fatal("paste changed inspected draft")
	}
	m.Update(keyMsg("esc"))
	if !p.discard {
		t.Fatal("dirty draft dropped")
	}
	m.Update(keyMsg("n"))
	if m.goals.recovery != p || p.discard {
		t.Fatal("did not keep draft")
	}
	m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	if m.goals.recovery != p {
		t.Fatal("ctrl-c discarded draft")
	}
	m.Update(keyMsg("esc"))
	m.Update(keyMsg("y"))
	if m.goals.recovery != nil {
		t.Fatal("explicit discard did not close")
	}
}
func TestGoalRecoveryMismatchAndHistory(t *testing.T) {
	m, p := recoveryRoot(t)
	raw, _ := json.Marshal(goal.RecoveryView{Goal: goal.Overview{ID: "different", Revision: 7, Project: m.goals.project}})
	m.Update(goalRecoveryMsg{project: m.goals.project, id: p.id, gen: m.goals.gen, seq: p.seq, op: "load", data: raw})
	if p.err == "" {
		t.Fatal("mismatched snapshot accepted")
	}
	p.choose("sync-brief")
	if p.editor != nil {
		t.Fatal("stale snapshot actionable after invalid read")
	}
	s := recoverySnapshot()
	s.PendingBrief = nil
	s.LatestBrief = &goal.BriefDecisionView{SourceID: "source", Before: s.Brief, After: s.Brief, Reason: "Reviewed retained tasks", AcceptedAt: time.Now()}
	s.LatestProgress.DismissedAt = time.Now()
	s.LatestProgress.ConfirmedAt = time.Now()
	s.LatestProgress.Resolution = "Inspected Linear, keep uncertain send in history."
	s.DeliveryEvidenceURL = "https://example.com/shared"
	p.snapshot = &s
	text := strings.Join(p.snapshotLines(), "\n")
	for _, want := range []string{"Previous brief", "Accepted clarification", "Reviewed retained tasks", "Dismissed; retained in history", "Resolution: Inspected Linear", "https://example.com/shared"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q", want)
		}
	}
	if strings.Contains(text, "Delivery confirmed") {
		t.Fatal("dismissal claimed delivery")
	}
}
func TestGoalRecoveryTerminalBounds(t *testing.T) {
	for _, size := range [][2]int{{120, 40}, {80, 24}, {40, 12}} {
		m, p := recoveryRoot(t)
		m.width, m.height = size[0], size[1]
		p.choose("accept-brief")
		p.editor.field("reason").lines = []string{"Retain tasks."}
		recoveryReview(t, m, p)
		p.uncertain = true
		p.err = strings.Repeat("an unavailable daemon reply ", 200)
		lines := m.goalsLines()
		if len(lines) != m.height {
			t.Fatalf("height %d", len(lines))
		}
		for _, line := range lines {
			if lipgloss.Width(line) > m.width {
				t.Fatalf("overflow %d>%d: %s", lipgloss.Width(line), m.width, line)
			}
		}
		if text := stripANSI(strings.Join(lines, "\n")); !strings.Contains(text, "submission locked") || !strings.Contains(text, "f reload local snapshot") {
			t.Fatal("error hid recovery controls")
		}
		p.err = ""
		p.uncertain = false
		m.Update(tea.KeyPressMsg{Code: tea.KeyEnd})
		text := stripANSI(strings.Join(m.goalsLines(), "\n"))
		if !strings.Contains(text, "Reason: Retain tasks.") || !strings.Contains(text, "a confirm") {
			t.Fatal("end cannot reach reason/confirmation")
		}
	}
}
