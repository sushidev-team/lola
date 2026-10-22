package tui

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/sushidev-team/lola/internal/goal"
	"github.com/sushidev-team/lola/internal/protocol"
)

func acceptanceRoot(t *testing.T) *rootModel {
	t.Helper()
	m := goalsRoot(t)
	g := m.goals.current()
	g.State, g.Outcome = "ready", "Owners can invite teammates and revoke pending invitations."
	g.RequireHumanAcceptance, g.Delivered, g.Verified, g.Authorized = true, true, true, true
	g.Parent = goal.Binding{ID: "parent", Identifier: "NOR-42", Confirmed: true}
	g.Deadline = time.Date(2026, 10, 22, 16, 0, 0, 0, time.UTC)
	g.Tokens, g.TokenLimit, g.RunLimit, g.SpendLimitUSD = 32000, 100000, 20, 20
	g.Coverage = goal.UsageCoverage{Runs: 4, Partial: 2, CostUnavailable: 4}
	g.Tasks[0].State = "integrated"
	g.Actions = []string{"accept-delivery", "pause", "cancel"}
	g.Acceptance = []string{"A recipient can accept an active invitation.", "Revoked invitations cannot grant access."}
	g.Members = []goal.MemberView{{ID: "api", Repo: "example/nori-api", Target: "main", Commit: strings.Repeat("a", 40), PRURL: "https://github.com/example/nori-api/pull/7", PRNumber: 7, DeliveryState: "merged"}}
	m.Update(keyMsg("enter"))
	return m
}

func TestGoalAcceptanceUsesReviewedRevisionAndHoldsUncertainReply(t *testing.T) {
	m := acceptanceRoot(t)
	m.Update(keyMsg("h"))
	if m.goals.confirm == nil || m.goals.confirm.Action != "accept-delivery" {
		t.Fatal("missing acceptance action")
	}
	if _, cmd := m.Update(keyMsg("enter")); cmd != nil {
		t.Fatal("empty evidence submitted")
	}
	m.Update(tea.PasteMsg{Content: "Checked recipient access in the delivered app"})
	old := requestFn
	defer func() { requestFn = old }()
	calls := 0
	requestFn = func(req protocol.Request) (*protocol.Response, error) {
		calls++
		var a goal.ControlRequest
		if err := json.Unmarshal(req.Args, &a); err != nil {
			t.Fatal(err)
		}
		if req.Cmd != "goalControl" || a.ID != "g" || a.Revision != 7 || a.Action != "accept-delivery" || a.Reason != "Checked recipient access in the delivered app" {
			t.Fatalf("wrong decision: %+v", a)
		}
		return &protocol.Response{Error: "response lost"}, nil
	}
	_, cmd := m.Update(keyMsg("enter"))
	if cmd == nil {
		t.Fatal("decision not submitted")
	}
	m.Update(cmd())
	before := m.goals.reason
	m.Update(tea.PasteMsg{Content: "should not change reviewed evidence"})
	m.Update(keyMsg("x"))
	if _, cmd = m.Update(keyMsg("enter")); cmd != nil || calls != 1 || m.goals.reason != before {
		t.Fatal("uncertain decision edited or retried")
	}
	view := strings.Join(m.goalsLines(), "\n")
	if !strings.Contains(view, "submission disabled") {
		t.Fatal("uncertain recovery hidden", view)
	}
	_, cmd = m.Update(keyMsg("esc"))
	if cmd == nil || m.goals.confirm != nil {
		t.Fatal("cannot recover by read-only refresh")
	}
}

func TestGoalAcceptanceCompactReviewScrollsAndPreservesEvidence(t *testing.T) {
	m := acceptanceRoot(t)
	m.width, m.height = 80, 24
	g := m.goals.current()
	g.Acceptance = append(g.Acceptance, strings.Repeat("A long criterion remains inspectable. ", 20))
	m.Update(keyMsg("h"))
	view := strings.Join(m.goalsLines(), "\n")
	if !strings.Contains(view, "reviewed revision 7") {
		t.Fatal("review identity hidden")
	}
	m.Update(tea.PasteMsg{Content: strings.Repeat("checked ", 100) + "final evidence"})
	view = strings.Join(m.goalsLines(), "\n")
	if !strings.Contains(view, "final evidence") || !strings.Contains(view, "enter accept") {
		t.Fatal("input tail or submit control clipped", view)
	}
	m.Update(keyMsg("home"))
	view = strings.Join(m.goalsLines(), "\n")
	if !strings.Contains(view, "reviewed revision 7") || !strings.HasSuffix(m.goals.reason, "final evidence") {
		t.Fatal("review scrolling lost evidence")
	}
	m.goals.confirm = nil
	m.goals.scroll = 0
	g.HumanAcceptance = &goal.HumanAcceptanceView{Revision: 7, Evidence: "Verified access\x1b\u202e", AcceptedAt: "2026-10-21T14:00:00Z"}
	g.State = "accepted"
	g.Actions = nil
	view = stripANSI(strings.Join(m.goalsLines(), "\n"))
	if !strings.Contains(view, "Human acceptance recorded") || strings.ContainsRune(view, '\u202e') {
		t.Fatal("unsafe or missing acceptance history", view)
	}
}

func TestGoalAuthorizationReviewsHumanAcceptanceWithDelivery(t *testing.T) {
	m, p := authorizationRoot(t)
	authorizationFill(p)
	p.editor.field("require_human_acceptance").b = true
	p.editor.field("publish_pr").b = false
	if _, err := p.authorization(); err == nil {
		t.Fatal("acceptance allowed without delivery")
	}
	p.editor.field("publish_pr").b = true
	authorizationReview(t, m, p)
	if !p.reviewed.Policy.RequireHumanAcceptance {
		t.Fatal("human requirement dropped from frozen policy")
	}
	if !strings.Contains(strings.Join(p.reviewLines(), "\n"), "Require separate human acceptance after final delivery: true") {
		t.Fatal("requirement absent from review")
	}
}
