package daemon

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/session"
	"github.com/sushidev-team/lola/internal/state"
)

// gatedSession is a claude session resting at its prompt with the given gate.
func gatedSession(gate session.PlanGate) session.Session {
	return session.Session{
		ID: "p1-eng-1", Source: "native", Kind: session.KindLinear, Project: "p1",
		Issue: "ENG-1", TmuxName: "p1-eng-1",
		AgentState: state.AgentIdle, Delivery: state.DeliveryNone, AtPrompt: true,
		PlanGate: gate,
	}
}

func submit(d *Daemon, id, plan string) protocol.Response {
	args, _ := json.Marshal(protocol.PlanSubmitArgs{Plan: plan})
	return d.handlePlanSubmit(protocol.Request{Cmd: "planSubmit", Session: id, Args: args})
}

func gateBlocked(t *testing.T, d *Daemon, id string) bool {
	t.Helper()
	r := d.handlePlanGate(protocol.Request{Cmd: "planGate", Session: id})
	var g protocol.PlanGateData
	if err := json.Unmarshal(r.Data, &g); err != nil {
		t.Fatal(err)
	}
	return g.Blocked
}

func TestPlanSubmitRefusedWithoutGate(t *testing.T) {
	d, _ := answerDaemon(t, gatedSession(session.PlanNone), restingPane)
	if r := submit(d, "p1-eng-1", "do it"); r.OK || !strings.Contains(r.Error, "no plan-approval gate") {
		t.Fatalf("submit without a gate = %+v", r)
	}
	if gateBlocked(t, d, "p1-eng-1") {
		t.Fatal("an ungated session must never be blocked")
	}
	if gateBlocked(t, d, "unknown-session") {
		t.Fatal("an unknown session must not be blocked (the hook runs in every claude session)")
	}
}

func TestPlanGateLifecycle(t *testing.T) {
	d, sends := answerDaemon(t, gatedSession(session.PlanPlanning), restingPane)
	if !gateBlocked(t, d, "p1-eng-1") {
		t.Fatal("planning must block edits")
	}
	if r := submit(d, "p1-eng-1", "  "); r.OK {
		t.Fatal("an empty plan must be refused")
	}
	if r := submit(d, "p1-eng-1", "## Approach\n\x1b[31mred\x1b[0m step\r\n"); !r.OK {
		t.Fatalf("submit: %+v", r)
	}
	s, _ := d.sessions.Get("p1-eng-1")
	if s.PlanGate != session.PlanSubmitted || s.PlanRound != 1 || strings.ContainsAny(s.Plan, "\x1b\r") {
		t.Fatalf("after submit: gate=%s round=%d plan=%q", s.PlanGate, s.PlanRound, s.Plan)
	}
	if !gateBlocked(t, d, "p1-eng-1") {
		t.Fatal("a submitted plan must still block edits")
	}

	// Requesting changes needs a comment, and sends the agent back to planning
	// with the feedback typed in.
	if err := d.decidePlan(context.Background(), "p1-eng-1", false, "", "lola"); err == nil {
		t.Fatal("requesting changes without a comment must be refused")
	}
	if err := d.decidePlan(context.Background(), "p1-eng-1", false, "split the migration", "lola"); err != nil {
		t.Fatal(err)
	}
	s, _ = d.sessions.Get("p1-eng-1")
	if s.PlanGate != session.PlanPlanning || s.PlanFeedback != "split the migration" {
		t.Fatalf("after reject: %+v", s)
	}
	// decidePlan flushes asynchronously; a resting agent gets the verdict now.
	d.connWg.Wait()
	if s, _ = d.sessions.Get("p1-eng-1"); len(s.PendingNotices) != 0 {
		t.Fatalf("the verdict must have been delivered: %+v", s.PendingNotices)
	}
	if len(*sends) != 1 || !strings.Contains((*sends)[0].text, "split the migration") {
		t.Fatalf("sends = %+v", *sends)
	}

	// Resubmit, approve: the gate opens and the approval is typed.
	d.sessions.Update("p1-eng-1", func(c *session.Session) bool {
		c.AgentState, c.AtPrompt = state.AgentIdle, true
		return true
	})
	if r := submit(d, "p1-eng-1", "v2"); !r.OK {
		t.Fatalf("resubmit: %+v", r)
	}
	if err := d.decidePlan(context.Background(), "p1-eng-1", true, "", "lola"); err != nil {
		t.Fatal(err)
	}
	if gateBlocked(t, d, "p1-eng-1") {
		t.Fatal("an approved plan must unblock edits")
	}
	s, _ = d.sessions.Get("p1-eng-1")
	if s.PlanRound != 2 || s.PlanGate != session.PlanApproved {
		t.Fatalf("after approve: %+v", s)
	}
	if err := d.decidePlan(context.Background(), "p1-eng-1", true, "", "lola"); err == nil {
		t.Fatal("approving twice must be refused")
	}
	if r := submit(d, "p1-eng-1", "v3"); r.OK {
		t.Fatal("submitting after approval must be refused")
	}
}

// A verdict recorded while the agent is mid-turn is QUEUED, never typed.
func TestPlanNoticeWaitsForRestingPrompt(t *testing.T) {
	s := gatedSession(session.PlanSubmitted)
	s.AgentState, s.AtPrompt = state.AgentWorking, false
	d, sends := answerDaemon(t, s, restingPane)
	if err := d.decidePlan(context.Background(), s.ID, true, "", "lola"); err != nil {
		t.Fatal(err)
	}
	d.connWg.Wait()
	if d.flushAgentNotices(context.Background(), s.ID) || len(*sends) != 0 {
		t.Fatal("must not type into a working agent")
	}
	cur, _ := d.sessions.Get(s.ID)
	if len(cur.PendingNotices) != 1 {
		t.Fatalf("the notice must stay queued: %+v", cur.PendingNotices)
	}

	// A pane that is not a resting prompt fails the proof even when the axes
	// say idle.
	d.sessions.Update(s.ID, func(c *session.Session) bool { c.AgentState, c.AtPrompt = state.AgentIdle, true; return true })
	d.paneTail = func(context.Context, string, int) (string, error) { return "✻ Thinking… (3s)\n", nil }
	if d.flushAgentNotices(context.Background(), s.ID) {
		t.Fatal("a pane without a resting prompt must defer")
	}
}

// Approving straight from planning waives the gate.
func TestPlanApproveWaivesFromPlanning(t *testing.T) {
	d, sends := answerDaemon(t, gatedSession(session.PlanPlanning), restingPane)
	if err := d.decidePlan(context.Background(), "p1-eng-1", false, "x", "lola"); err == nil {
		t.Fatal("requesting changes before any plan must be refused")
	}
	if err := d.decidePlan(context.Background(), "p1-eng-1", true, "", "lola"); err != nil {
		t.Fatal(err)
	}
	d.connWg.Wait()
	if len(*sends) != 1 || !strings.Contains((*sends)[0].text, "waived") {
		t.Fatalf("sends = %+v", *sends)
	}
}

func TestAppendNoticeIsBoundedAndNeverAliases(t *testing.T) {
	q := []string{"a"}
	out := appendNotice(q, "b")
	out[0] = "changed"
	if q[0] != "a" {
		t.Fatal("appendNotice must not alias its input")
	}
	for i := 0; i < maxPendingNotices+5; i++ {
		out = appendNotice(out, "x")
	}
	if len(out) != maxPendingNotices {
		t.Fatalf("len = %d, want %d", len(out), maxPendingNotices)
	}
}

func TestSanitizePlanClips(t *testing.T) {
	long := strings.Repeat("é", planMaxBytes)
	got := sanitizePlan(long)
	if !strings.HasSuffix(got, "(plan clipped by lola)") || len(got) > planMaxBytes+64 {
		t.Fatalf("clip: len=%d suffix=%q", len(got), got[len(got)-30:])
	}
	if !strings.Contains(sanitizePlan("a\n\tb"), "a\n\tb") {
		t.Fatal("newlines and tabs must survive")
	}
}
