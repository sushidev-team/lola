package daemon

// Tests for the MANUAL conflict resolution (resolveconflict.go, cmd=resolve
// Conflict): the delivery-axis guard, the project's default_branch landing in
// the instruction, the send-keys gate (nothing is typed at a mid-turn agent),
// and the guard/axis bookkeeping a delivered request leaves behind.

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/linear"
	"github.com/sushidev-team/lola/internal/state"
)

// conflictSess is a session whose PR conflicts on the DELIVERY axis, resting at
// its prompt — the state the button is drawn in.
func conflictSess(t *testing.T, d *Daemon, ident string) string {
	t.Helper()
	s := reactSess(ident, "merge_conflict", openPR(7, "CONFLICTING", "", "pass"))
	s.AtPrompt = true
	s.SetDelivery(state.DeliveryMergeConflict, time.Now())
	d.sessions.Upsert(s)
	return s.ID
}

// conflictConfig is the native test config with a project whose default_branch
// is NOT "main", so a hard-coded fallback cannot pass the assertions below.
func conflictConfig() *config.Config {
	p := nativePoll("p1")
	p.DefaultBranch = "develop"
	return nativeTestConfig(p)
}

func TestResolveConflictAsksAgentToMergeTheDefaultBranch(t *testing.T) {
	d := newTestDaemon(t, conflictConfig(), &linear.Fake{}, &fakeNative{})
	seams := &fakeReactSeams{}
	seams.install(d)
	id := conflictSess(t, d, "FE-1")

	data, err := d.handleResolveConflict(context.Background(), id)
	if err != nil {
		t.Fatalf("handleResolveConflict: %v", err)
	}
	if data.Branch != "develop" {
		t.Errorf("Branch = %q, want develop", data.Branch)
	}
	calls := seams.sendCalls()
	if len(calls) != 1 {
		t.Fatalf("want exactly one send-keys, got %+v", calls)
	}
	if !strings.Contains(calls[0].text, "git merge origin/develop") {
		t.Errorf("instruction must name the project's default branch, got:\n%s", calls[0].text)
	}

	got, _ := d.sessions.Get(id)
	if got.AtPrompt {
		t.Error("the send-keys gate must be consumed by a delivered request")
	}
	if got.LastReactedStatus != "merge_conflict" {
		t.Errorf("LastReactedStatus = %q, want merge_conflict (the auto reaction must not send on top)", got.LastReactedStatus)
	}
	if got.AgentState != state.AgentWorking {
		t.Errorf("AgentState = %q, want working", got.AgentState)
	}
}

// The automatic reaction must not pile a rebase prompt on top of the merge a
// human just asked for: the manual path stamps the same one-shot guard.
func TestResolveConflictSuppressesTheAutomaticReaction(t *testing.T) {
	d := newTestDaemon(t, conflictConfig(), &linear.Fake{}, &fakeNative{})
	d.cfg.Reactions = config.ReactionsConfig{
		MergeConflict: config.Reaction{Auto: true, Message: config.DefaultMergeConflictMessage},
	}
	seams := &fakeReactSeams{}
	seams.install(d)
	id := conflictSess(t, d, "FE-2")

	if _, err := d.handleResolveConflict(context.Background(), id); err != nil {
		t.Fatalf("handleResolveConflict: %v", err)
	}
	got, _ := d.sessions.Get(id)
	d.react(context.Background(), got)
	if n := len(seams.sendCalls()); n != 1 {
		t.Errorf("want the manual send only, got %d sends", n)
	}
}

// A PR that does not conflict is refused: the delivery axis is the fact, and a
// merge prompt at a healthy PR costs a worker a whole turn.
func TestResolveConflictRefusesASessionWithoutAConflict(t *testing.T) {
	d := newTestDaemon(t, conflictConfig(), &linear.Fake{}, &fakeNative{})
	seams := &fakeReactSeams{}
	seams.install(d)

	s := reactSess("FE-3", "ci_pending", openPR(9, "MERGEABLE", "", "pending"))
	s.AtPrompt = true
	s.SetDelivery(state.DeliveryCIPending, time.Now())
	d.sessions.Upsert(s)

	if _, err := d.handleResolveConflict(context.Background(), s.ID); err == nil {
		t.Fatal("want an error for a session with no merge conflict")
	}
	if n := len(seams.sendCalls()); n != 0 {
		t.Errorf("nothing may be typed, got %d sends", n)
	}
}

// Mid-turn: the pane says the agent is working, so nothing is typed and the
// caller is told — the manual path never defers silently.
func TestResolveConflictRefusesAMidTurnAgent(t *testing.T) {
	d := newTestDaemon(t, conflictConfig(), &linear.Fake{}, &fakeNative{})
	seams := &fakeReactSeams{}
	seams.install(d)
	d.paneTail = func(context.Context, string, int) (string, error) { return paneWorking, nil }
	id := conflictSess(t, d, "FE-4")

	_, err := d.handleResolveConflict(context.Background(), id)
	if err == nil {
		t.Fatal("want an error while the agent is mid-turn")
	}
	if !strings.Contains(err.Error(), "prompt") {
		t.Errorf("error should say why: %v", err)
	}
	if n := len(seams.sendCalls()); n != 0 {
		t.Errorf("nothing may be typed at a mid-turn agent, got %d sends", n)
	}
	if got, _ := d.sessions.Get(id); !got.AtPrompt {
		t.Error("a refused request must not consume the gate")
	}
}

// A modal covering the pane is the case the pane proof exists for: the axes read
// idle, the composer is not there, and typing would answer a dialog.
func TestResolveConflictRefusesAModalPane(t *testing.T) {
	d := newTestDaemon(t, conflictConfig(), &linear.Fake{}, &fakeNative{})
	seams := &fakeReactSeams{}
	seams.install(d)
	d.paneTail = func(context.Context, string, int) (string, error) { return paneModal, nil }
	id := conflictSess(t, d, "FE-5")

	if _, err := d.handleResolveConflict(context.Background(), id); err == nil {
		t.Fatal("want an error while a modal owns the pane")
	}
	if n := len(seams.sendCalls()); n != 0 {
		t.Errorf("nothing may be typed into a modal, got %d sends", n)
	}
}

func TestResolveConflictRejectsUnknownSession(t *testing.T) {
	d := newTestDaemon(t, conflictConfig(), &linear.Fake{}, &fakeNative{})
	if _, err := d.handleResolveConflict(context.Background(), "nope"); err == nil {
		t.Fatal("want an error for an unknown session")
	}
}
