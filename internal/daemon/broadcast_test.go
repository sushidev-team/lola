package daemon

// Tests for cmd=broadcast (broadcast.go): one message queued on every LIVE agent
// of the project, typed now into the ones verifiably resting at their prompt,
// DEFERRED (never forced) for the busy ones and delivered once they are idle.

import (
	"context"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/sushidev-team/lola/internal/linear"
	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/session"
	"github.com/sushidev-team/lola/internal/state"
)

// The acceptance criterion: one message reaches every waiting agent of the
// project now; a busy one gets it queued and receives it when idle. Sessions
// that are not live agents of THIS project are never touched.
func TestBroadcastDeliversToWaitingAndDefersBusy(t *testing.T) {
	d := newTestDaemon(t, conflictConfig(), &linear.Fake{}, &fakeNative{})
	seams := &fakeReactSeams{}
	seams.install(d)

	idle1 := feedbackSess(t, d, "FE-1", "claude")
	idle2 := feedbackSess(t, d, "FE-2", "codex")
	busy := feedbackSess(t, d, "FE-3", "claude")

	dead := nativeSess("FE-4", "working")
	dead.SetAgentState(state.AgentDead, "", time.Now())
	d.sessions.Upsert(dead)
	merged := nativeSess("FE-5", "working")
	merged.SetDelivery(state.DeliveryMerged, time.Now())
	d.sessions.Upsert(merged)
	shell := nativeSess("FE-6", "working")
	shell.Agentless = true
	d.sessions.Upsert(shell)
	other := nativeSess("FE-7", "working")
	other.ID, other.TmuxName, other.Project = "p2-fe-7", "p2-fe-7", "p2"
	other.AtPrompt = true
	d.sessions.Upsert(other)

	paneFor := map[string]string{busy: paneWorking}
	d.paneTail = func(_ context.Context, target string, _ int) (string, error) {
		for id, pane := range paneFor {
			if strings.Contains(target, id) {
				return pane, nil
			}
		}
		return paneWaiting, nil
	}

	data, err := d.handleBroadcast(context.Background(), protocol.BroadcastArgs{Project: "p1", Message: "rebase on main"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(data.Delivered, ",") != strings.Join(sortedIDs(idle1, idle2), ",") {
		t.Errorf("delivered = %v, want %v", data.Delivered, sortedIDs(idle1, idle2))
	}
	if len(data.Queued) != 1 || data.Queued[0] != busy {
		t.Errorf("queued = %v, want [%s]", data.Queued, busy)
	}
	if !strings.Contains(data.Message, "delivered to 2") || !strings.Contains(data.Message, "deferred for 1") {
		t.Errorf("message = %q", data.Message)
	}
	calls := seams.sendCalls()
	if len(calls) != 2 {
		t.Fatalf("want 2 sends, got %d", len(calls))
	}
	for _, c := range calls {
		if !strings.Contains(c.text, "rebase on main") || !strings.Contains(c.text, "every coding agent working in this project") {
			t.Errorf("send = %q", c.text)
		}
	}
	for _, id := range []string{dead.ID, merged.ID, shell.ID, other.ID} {
		if got, _ := d.sessions.Get(id); got.PendingBroadcast != "" {
			t.Errorf("%s is not a live agent of p1 but got %q queued", id, got.PendingBroadcast)
		}
	}
	got, _ := d.sessions.Get(busy)
	if got.PendingBroadcast != "rebase on main" || !got.AtPrompt {
		t.Errorf("busy: pending=%q atPrompt=%v, want queued and gate untouched", got.PendingBroadcast, got.AtPrompt)
	}

	// A second broadcast while still busy is appended, not replacing the first.
	if _, err := d.handleBroadcast(context.Background(), protocol.BroadcastArgs{Project: "p1", Message: "stop touching X"}); err != nil {
		t.Fatal(err)
	}
	// The busy agent comes back to its prompt: the flush delivers both as one.
	paneFor[busy] = paneWaiting
	d.flushReviewHandoffs(context.Background(), busy)
	calls = seams.sendCalls()
	last := calls[len(calls)-1]
	if !strings.Contains(last.text, "rebase on main") || !strings.Contains(last.text, "stop touching X") {
		t.Errorf("flushed send = %q, want both queued broadcasts", last.text)
	}
	if got, _ := d.sessions.Get(busy); got.PendingBroadcast != "" {
		t.Errorf("queue must be cleared after delivery, got %q", got.PendingBroadcast)
	}
}

func sortedIDs(ids ...string) []string {
	out := append([]string(nil), ids...)
	sort.Strings(out)
	return out
}

// Pending human feedback and a pending broadcast never type into the same
// prompt back to back: one delivery per flush pass.
func TestBroadcastWaitsBehindFeedbackInOneFlush(t *testing.T) {
	d := newTestDaemon(t, conflictConfig(), &linear.Fake{}, &fakeNative{})
	seams := &fakeReactSeams{}
	seams.install(d)
	id := feedbackSess(t, d, "FE-1", "claude")
	d.sessions.Update(id, func(cur *session.Session) bool {
		cur.PendingFeedback = "- General note:\n  fix it"
		cur.PendingBroadcast = "rebase on main"
		return true
	})
	d.flushReviewHandoffs(context.Background(), id)
	if n := len(seams.sendCalls()); n != 1 {
		t.Fatalf("want exactly one send per flush, got %d", n)
	}
	if got, _ := d.sessions.Get(id); got.PendingFeedback != "" || got.PendingBroadcast != "rebase on main" {
		t.Errorf("feedback first, broadcast kept: feedback=%q broadcast=%q", got.PendingFeedback, got.PendingBroadcast)
	}
}

func TestBroadcastValidation(t *testing.T) {
	d := newTestDaemon(t, conflictConfig(), &linear.Fake{}, &fakeNative{})
	ctx := context.Background()
	for name, a := range map[string]protocol.BroadcastArgs{
		"no project":      {Message: "hi"},
		"unknown project": {Project: "nope", Message: "hi"},
		"empty message":   {Project: "p1", Message: "  \n "},
		"too long":        {Project: "p1", Message: strings.Repeat("x", broadcastMaxRunes+1)},
	} {
		if _, err := d.handleBroadcast(ctx, a); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
	data, err := d.handleBroadcast(ctx, protocol.BroadcastArgs{Project: "p1", Message: "hi"})
	if err != nil || data.Message != "no live agents in this project" {
		t.Errorf("no sessions: data=%+v err=%v", data, err)
	}
}

// The human's text goes into the pane only sanitized.
func TestBroadcastSanitizesTheMessage(t *testing.T) {
	d := newTestDaemon(t, conflictConfig(), &linear.Fake{}, &fakeNative{})
	seams := &fakeReactSeams{}
	seams.install(d)
	feedbackSess(t, d, "FE-1", "claude")
	if _, err := d.handleBroadcast(context.Background(), protocol.BroadcastArgs{Project: "p1", Message: "hi\x1b[31m\x03 there"}); err != nil {
		t.Fatal(err)
	}
	calls := seams.sendCalls()
	if len(calls) != 1 || strings.ContainsAny(calls[0].text, "\x1b\x03") {
		t.Errorf("sends = %+v, want one sanitized send", calls)
	}
}

// Past the delivery budget nothing more is typed in the request: the rest stay
// queued for the next flush, so a broadcast to many agents cannot outlast the
// client's deadline — and nothing is lost.
func TestBroadcastBudgetLeavesTheRestQueued(t *testing.T) {
	d := newTestDaemon(t, conflictConfig(), &linear.Fake{}, &fakeNative{})
	seams := &fakeReactSeams{}
	seams.install(d)
	a := feedbackSess(t, d, "FE-1", "claude")
	b := feedbackSess(t, d, "FE-2", "claude")

	old := broadcastDeliverBudget
	broadcastDeliverBudget = 0
	t.Cleanup(func() { broadcastDeliverBudget = old })

	data, err := d.handleBroadcast(context.Background(), protocol.BroadcastArgs{Project: "p1", Message: "rebase on main"})
	if err != nil {
		t.Fatal(err)
	}
	if len(data.Delivered) != 0 || strings.Join(data.Queued, ",") != strings.Join(sortedIDs(a, b), ",") {
		t.Errorf("delivered=%v queued=%v, want none delivered and both queued", data.Delivered, data.Queued)
	}
	if n := len(seams.sendCalls()); n != 0 {
		t.Errorf("want no sends past the budget, got %d", n)
	}
	d.flushReviewHandoffs(context.Background(), a)
	if got, _ := d.sessions.Get(a); got.PendingBroadcast != "" {
		t.Errorf("flush must deliver the queued broadcast, got %q", got.PendingBroadcast)
	}
}
