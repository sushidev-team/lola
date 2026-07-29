package daemon

// Tests for the two-axis status split: the agent axis staying truthful under
// an open PR (no hook↔observer rollup flap), delivery-fetch failure counting,
// the merge_conflict slot, the closed lifecycle, and the adoption-carried
// AtPrompt gate verification.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/sushidev-team/lola/internal/linear"
	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/scm"
	"github.com/sushidev-team/lola/internal/session"
	"github.com/sushidev-team/lola/internal/state"
)

// THE FLAP FIX: with an open PR (delivery ci_pending), a stop hook moves only
// the agent axis — the rolled-up Status must stay ci_pending through hook and
// observer cycles alike, instead of the old ci_pending→idle→ci_pending churn
// at hook cadence.
func TestPostPRHookDoesNotFlapRollup(t *testing.T) {
	d := newTestDaemon(t, nativeTestConfig(nativePoll("p1")), &linear.Fake{},
		&fakeNative{alive: map[string]bool{"lola-p1-fe-1": true}})
	(&fakeObsSeams{pr: &scm.PR{Number: 3, State: "OPEN", Mergeable: "MERGEABLE", ChecksState: "pending"}}).install(d)
	d.paneTail = func(ctx context.Context, tmuxName string, lines int) (string, error) {
		return paneUnknown, nil
	}
	seed := nativeSess("FE-1", "working")
	d.sessions.Upsert(seed)

	// Observer derives the delivery axis from the PR.
	d.observe(context.Background())
	if got := getSess(t, d, seed.ID); got.Status != "ci_pending" || got.Delivery != state.DeliveryCIPending {
		t.Fatalf("after observe: status=%q delivery=%q, want ci_pending", got.Status, got.Delivery)
	}

	// Turn ends: the agent axis moves to idle, the rollup must NOT.
	d.handleHookEvent(protocol.Request{Cmd: "hookEvent", Session: seed.ID, Event: "stop"})
	got := getSess(t, d, seed.ID)
	if got.AgentState != state.AgentIdle {
		t.Fatalf("agent axis = %q, want idle after stop", got.AgentState)
	}
	if got.Status != "ci_pending" {
		t.Fatalf("rollup = %q, want ci_pending (delivery owns the post-PR rollup)", got.Status)
	}
	if !got.AtPrompt {
		t.Fatal("stop must still open the send-keys gate")
	}

	// Next turn starts: axis back to working, rollup still stable.
	d.handleHookEvent(protocol.Request{Cmd: "hookEvent", Session: seed.ID, Event: "user_prompt"})
	got = getSess(t, d, seed.ID)
	if got.AgentState != state.AgentWorking || got.Status != "ci_pending" {
		t.Fatalf("after user_prompt: axis=%q status=%q, want working/ci_pending", got.AgentState, got.Status)
	}

	// And the observer cycle leaves both alone (no churn back and forth).
	d.observe(context.Background())
	got = getSess(t, d, seed.ID)
	if got.Status != "ci_pending" || got.AgentState != state.AgentWorking {
		t.Fatalf("after second observe: axis=%q status=%q", got.AgentState, got.Status)
	}
}

// A failed gh PR fetch keeps the last known facts and COUNTS the failure so
// staleness is visible; a later success resets the counter and restamps
// PRObservedAt.
func TestObserverCountsPRFetchFailures(t *testing.T) {
	d := newTestDaemon(t, nativeTestConfig(nativePoll("p1")), &linear.Fake{},
		&fakeNative{alive: map[string]bool{"lola-p1-fe-1": true}})
	seams := &fakeObsSeams{pr: &scm.PR{Number: 3, State: "OPEN", Mergeable: "MERGEABLE", ChecksState: "pending"}}
	seams.install(d)
	d.paneTail = func(ctx context.Context, tmuxName string, lines int) (string, error) {
		return paneUnknown, nil
	}
	seed := nativeSess("FE-1", "working")
	d.sessions.Upsert(seed)

	d.observe(context.Background())
	got := getSess(t, d, seed.ID)
	if got.PRObservedAt.IsZero() || got.PRFetchFailures != 0 {
		t.Fatalf("successful fetch: observedAt=%v failures=%d", got.PRObservedAt, got.PRFetchFailures)
	}
	firstObserved := got.PRObservedAt

	seams.mu.Lock()
	seams.prErr = errors.New("gh: connection refused")
	seams.mu.Unlock()
	d.observe(context.Background())
	d.observe(context.Background())
	got = getSess(t, d, seed.ID)
	if got.PRFetchFailures != 2 {
		t.Fatalf("failures = %d, want 2", got.PRFetchFailures)
	}
	if got.Delivery != state.DeliveryCIPending || got.PR == nil {
		t.Fatalf("failed fetch must keep the last known facts, got delivery=%q pr=%v", got.Delivery, got.PR)
	}
	if !got.PRObservedAt.Equal(firstObserved) {
		t.Fatal("PRObservedAt must not advance on a failed fetch")
	}

	seams.mu.Lock()
	seams.prErr = nil
	seams.mu.Unlock()
	d.observe(context.Background())
	got = getSess(t, d, seed.ID)
	if got.PRFetchFailures != 0 {
		t.Fatalf("failures = %d, want 0 after recovery", got.PRFetchFailures)
	}
}

// Mergeable=UNKNOWN (GitHub recomputing after a push) must not flap a known
// conflict: the observer threads the previous delivery state into
// DeriveDelivery, so merge_conflict is sticky until GitHub commits.
func TestObserverConflictStickyThroughUnknown(t *testing.T) {
	d := newTestDaemon(t, nativeTestConfig(nativePoll("p1")), &linear.Fake{},
		&fakeNative{alive: map[string]bool{"lola-p1-fe-1": true}})
	seams := &fakeObsSeams{pr: &scm.PR{Number: 3, State: "OPEN", Mergeable: "CONFLICTING", ChecksState: "pass"}}
	seams.install(d)
	d.paneTail = func(ctx context.Context, tmuxName string, lines int) (string, error) {
		return paneUnknown, nil
	}
	seed := nativeSess("FE-1", "working")
	d.sessions.Upsert(seed)

	d.observe(context.Background())
	if got := getSess(t, d, seed.ID); got.Delivery != state.DeliveryMergeConflict {
		t.Fatalf("delivery = %q, want merge_conflict", got.Delivery)
	}

	seams.mu.Lock()
	seams.pr = &scm.PR{Number: 3, State: "OPEN", Mergeable: "UNKNOWN", ChecksState: "pass"}
	seams.mu.Unlock()
	d.observe(context.Background())
	if got := getSess(t, d, seed.ID); got.Delivery != state.DeliveryMergeConflict {
		t.Fatalf("delivery = %q, want merge_conflict held through UNKNOWN", got.Delivery)
	}

	seams.mu.Lock()
	seams.pr = &scm.PR{Number: 3, State: "OPEN", Mergeable: "MERGEABLE", ChecksState: "pass"}
	seams.mu.Unlock()
	d.observe(context.Background())
	if got := getSess(t, d, seed.ID); got.Delivery != state.DeliveryReviewPending {
		t.Fatalf("delivery = %q, want review_pending once GitHub commits", got.Delivery)
	}
}

// merge_conflict occupies a budget slot: the reaction engine is actively
// re-prompting that agent to rebase, so its runner is busy.
func TestNativeLiveCountedCountsMergeConflict(t *testing.T) {
	sessions := []session.Session{
		{ID: "a", Source: "native", Status: "merge_conflict"},
		{ID: "b", Source: "native", Status: "approved"},
		{ID: "c", Source: "native", Status: "working"},
	}
	if got := NativeLiveCounted(sessions); got != 2 {
		t.Fatalf("NativeLiveCounted = %d, want 2 (merge_conflict + working)", got)
	}
}

// A PR closed without merging notifies the operator exactly once, never
// send-keys, and keeps the session/worktree; its status stops shielding the
// issue from the reconcile orphan-revert.
func TestReactClosedNotifiesOnceAndUnshields(t *testing.T) {
	nat := &fakeNative{}
	d := newTestDaemon(t, reactTestConfig(nativePoll("p1")), &linear.Fake{}, nat)
	seams := &fakeReactSeams{}
	seams.install(d)

	pr := openPR(7, "MERGEABLE", "", "pass")
	pr.State = "CLOSED"
	s := reactSess("FE-1", "closed", pr)
	d.sessions.Upsert(s)

	d.react(context.Background(), s)
	if len(seams.sendCalls()) != 0 {
		t.Error("closed must never send-keys")
	}
	if len(nat.killCalls()) != 0 {
		t.Error("closed must never auto-kill")
	}
	if seams.noteCount() != 1 {
		t.Fatalf("want one closed notification, got %d", seams.noteCount())
	}
	got, _ := d.sessions.Get(s.ID)
	if got.LastReactedStatus != "closed" {
		t.Fatalf("LastReactedStatus = %q, want closed", got.LastReactedStatus)
	}

	// Second cycle: the one-shot guard suppresses a repeat.
	d.react(context.Background(), got)
	if seams.noteCount() != 1 {
		t.Errorf("closed must notify once, got %d", seams.noteCount())
	}

	// And a closed session no longer accounts for its issue in reconcile.
	if nativeSessionPresent("closed") {
		t.Error("closed must not shield its issue from the orphan revert")
	}
}

// The adoption-carried AtPrompt gate is UNVERIFIED: before the first post-
// restart send, the engine re-verifies against the live pane. A mid-turn pane
// closes the stale gate without typing; an ambiguous pane defers; a resting
// pane verifies and the send proceeds.
func TestReactSendVerifiesAdoptedGate(t *testing.T) {
	cases := []struct {
		name      string
		pane      string
		wantSends int
		wantGate  bool // AtPrompt after the attempt
	}{
		{"waiting pane verifies and sends", paneWaiting, 1, false}, // consumed by the send
		{"working pane closes the stale gate", paneWorking, 0, false},
		{"ambiguous pane defers", paneUnknown, 0, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := newTestDaemon(t, reactTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeNative{})
			seams := &fakeReactSeams{failing: "LOGS"}
			seams.install(d)
			d.paneTail = func(ctx context.Context, tmuxName string, lines int) (string, error) {
				return c.pane, nil
			}

			s := reactSess("FE-1", "ci_failed", openPR(9, "MERGEABLE", "", "fail"))
			s.AtPrompt = true
			d.sessions.Upsert(s)
			// Simulate the restart carry: gate open but unverified.
			d.sessions.Update(s.ID, func(cur *session.Session) bool {
				cur.AtPromptVerified = false
				return true
			})

			snap, _ := d.sessions.Get(s.ID)
			d.react(context.Background(), snap)

			if got := len(seams.sendCalls()); got != c.wantSends {
				t.Fatalf("sends = %d, want %d", got, c.wantSends)
			}
			got, _ := d.sessions.Get(s.ID)
			if got.AtPrompt != c.wantGate {
				t.Errorf("AtPrompt = %v, want %v", got.AtPrompt, c.wantGate)
			}
			if c.wantSends == 0 && got.LastReactedStatus == "ci_failed" && c.pane != paneWorking {
				t.Error("a deferred send must not consume the one-shot guard")
			}
		})
	}
}

// A hook event re-verifies the carried gate (it is live proof from inside the
// pane), so the cycle after a stop hook sends without any pane capture.
func TestHookEventReverifiesAdoptedGate(t *testing.T) {
	d := newTestDaemon(t, reactTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeNative{})
	seams := &fakeReactSeams{failing: "LOGS"}
	seams.install(d)
	d.paneTail = func(ctx context.Context, tmuxName string, lines int) (string, error) {
		t.Fatal("a hook-verified gate must not need a pane capture")
		return "", nil
	}

	s := reactSess("FE-1", "ci_failed", openPR(9, "MERGEABLE", "", "fail"))
	d.sessions.Upsert(s)
	d.sessions.Update(s.ID, func(cur *session.Session) bool {
		cur.AtPromptVerified = false
		return true
	})

	// The agent finishes a turn: stop opens AND verifies the gate.
	d.handleHookEvent(protocol.Request{Cmd: "hookEvent", Session: s.ID, Event: "stop"})

	snap, _ := d.sessions.Get(s.ID)
	d.react(context.Background(), snap)
	if len(seams.sendCalls()) != 1 {
		t.Fatalf("want one send after a hook-verified gate, got %d", len(seams.sendCalls()))
	}
}

// The structured hook payload lands on the record: notification message +
// classified reason, tool name, transcript path. A nil payload (old hook
// binary) behaves exactly like before.
func TestHookPayloadIngestion(t *testing.T) {
	d := newTestDaemon(t, nativeTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeNative{})
	s := nativeSess("FE-1", "working")
	d.sessions.Upsert(s)

	d.handleHookEvent(protocol.Request{
		Cmd: "hookEvent", Session: s.ID, Event: "tool_use",
		Hook: &protocol.HookPayload{ToolName: "Bash", TranscriptPath: "/t/x.jsonl"},
	})
	got := getSess(t, d, s.ID)
	if got.CurrentTool != "Bash" || got.TranscriptPath != "/t/x.jsonl" {
		t.Fatalf("tool_use payload not ingested: tool=%q transcript=%q", got.CurrentTool, got.TranscriptPath)
	}

	d.handleHookEvent(protocol.Request{
		Cmd: "hookEvent", Session: s.ID, Event: "notification",
		Hook: &protocol.HookPayload{Message: "Claude needs your permission to use Bash", Reason: "permission_request"},
	})
	got = getSess(t, d, s.ID)
	if got.InputReason != state.InputPermission {
		t.Fatalf("InputReason = %q, want permission_prompt", got.InputReason)
	}
	if got.LastNotification == "" {
		t.Fatal("LastNotification not recorded")
	}
	if got.CurrentTool != "" {
		t.Fatal("leaving working must clear CurrentTool")
	}

	// A bare idle-notification classifies as idle_notification.
	d.handleHookEvent(protocol.Request{Cmd: "hookEvent", Session: s.ID, Event: "user_prompt"})
	d.handleHookEvent(protocol.Request{
		Cmd: "hookEvent", Session: s.ID, Event: "notification",
		Hook: &protocol.HookPayload{Message: "Claude is waiting for your input"},
	})
	got = getSess(t, d, s.ID)
	if got.InputReason != state.InputIdleNotify {
		t.Fatalf("InputReason = %q, want idle_notification", got.InputReason)
	}

	// Nil payload: still a full transition, no field noise.
	d.handleHookEvent(protocol.Request{Cmd: "hookEvent", Session: s.ID, Event: "stop"})
	got = getSess(t, d, s.ID)
	if got.AgentState != state.AgentIdle || !got.AtPrompt {
		t.Fatalf("nil-payload stop: axis=%q atPrompt=%v", got.AgentState, got.AtPrompt)
	}
}

// Adoption marks the carried gate unverified end-to-end.
func TestAdoptMarksGateUnverified(t *testing.T) {
	home := t.TempDir()
	t.Setenv("LOLA_HOME", home)
	d := newTestDaemon(t, nativeTestConfig(nativePoll("p1")), &linear.Fake{}, nil)

	prior := nativeSess("FE-1", "idle")
	prior.AtPrompt = true
	d.sessions.Upsert(prior) // Upsert trusts a legacy record (AtPromptVerified true via EnsureAxes)

	adopted := prior
	adopted.SetAgentState(state.AgentWorking, "", time.Now())
	d.native = &fakeNative{adopted: []session.Session{adopted}}
	d.adoptNativeSessions(context.Background())

	got := getSess(t, d, prior.ID)
	if !got.AtPrompt {
		t.Fatal("adoption must carry the AtPrompt gate forward")
	}
	if got.AtPromptVerified {
		t.Fatal("an adoption-carried gate must be UNVERIFIED until a live signal confirms it")
	}
}
