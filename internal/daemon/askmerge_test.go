package daemon

// Tests for cmd=askMerge (askmerge.go): the branch name is the one outside
// value in the instruction, the send goes through the resting-prompt gate, and
// a refusal types nothing.

import (
	"context"
	"strings"
	"testing"

	"github.com/sushidev-team/lola/internal/linear"
	"github.com/sushidev-team/lola/internal/state"
)

// restingSess is a session with no PR trouble at all, resting at its prompt:
// askMerge has no delivery-axis entry condition.
func restingSess(d *Daemon, ident string) string {
	s := reactSess(ident, "working", nil)
	s.AtPrompt = true
	s.Branch = "feature/" + ident
	d.sessions.Upsert(s)
	return s.ID
}

func TestAskMergeAsksAgentToMergeTheNamedBranch(t *testing.T) {
	d := newTestDaemon(t, conflictConfig(), &linear.Fake{}, &fakeNative{})
	seams := &fakeReactSeams{}
	seams.install(d)
	id := restingSess(d, "FE-1")

	data, err := d.handleAskMerge(context.Background(), id, "main")
	if err != nil {
		t.Fatalf("handleAskMerge: %v", err)
	}
	if data.Branch != "main" || !strings.Contains(data.Message, "main") {
		t.Errorf("data = %+v, want the named branch", data)
	}
	calls := seams.sendCalls()
	if len(calls) != 1 {
		t.Fatalf("want exactly one send-keys, got %+v", calls)
	}
	if !strings.Contains(calls[0].text, "git merge main\n") {
		t.Errorf("instruction must merge the named branch, got:\n%s", calls[0].text)
	}
	if strings.Contains(calls[0].text, "fetch") {
		t.Errorf("instruction must not fetch: the human picked the host's ref, got:\n%s", calls[0].text)
	}
	got, _ := d.sessions.Get(id)
	if got.AtPrompt {
		t.Error("the send-keys gate must be consumed by a delivered request")
	}
	if got.AgentState != state.AgentWorking {
		t.Errorf("AgentState = %q, want working", got.AgentState)
	}
	if got.LastReactedStatus != "" {
		t.Errorf("LastReactedStatus = %q; askMerge is no reaction", got.LastReactedStatus)
	}
}

func TestAskMergeAcceptsARemoteTrackingBranch(t *testing.T) {
	d := newTestDaemon(t, conflictConfig(), &linear.Fake{}, &fakeNative{})
	seams := &fakeReactSeams{}
	seams.install(d)
	id := restingSess(d, "FE-1")
	if _, err := d.handleAskMerge(context.Background(), id, "origin/release-1.2"); err != nil {
		t.Fatal(err)
	}
	if calls := seams.sendCalls(); len(calls) != 1 || !strings.Contains(calls[0].text, "git merge origin/release-1.2\n") {
		t.Fatalf("sends = %+v", calls)
	}
}

// The branch name is typed into an agent that may hand it to a shell: anything
// that could read as an option, a revision expression or a second command is
// refused before the gate is even asked.
func TestAskMergeRefusesUnsafeBranchNames(t *testing.T) {
	for _, ref := range []string{
		"", "-X theirs", "--abort", "main; rm -rf .", "main && x", "a b", "main\nrm", "$(x)", "`x`",
		"main~1", "main^", "a..b", "HEAD@{1}", "a:b", "/main", "main/", "main.lock", "a//b", "it's",
		strings.Repeat("a", askMergeRefMax+1),
	} {
		d := newTestDaemon(t, conflictConfig(), &linear.Fake{}, &fakeNative{})
		seams := &fakeReactSeams{}
		seams.install(d)
		id := restingSess(d, "FE-1")
		if _, err := d.handleAskMerge(context.Background(), id, ref); err == nil {
			t.Errorf("ref %q was accepted", ref)
		}
		if n := len(seams.sendCalls()); n != 0 {
			t.Errorf("ref %q: %d sends, want none", ref, n)
		}
		if got, _ := d.sessions.Get(id); !got.AtPrompt {
			t.Errorf("ref %q consumed the gate", ref)
		}
	}
}

func TestAskMergeRefusesTheSessionsOwnBranchAndStacks(t *testing.T) {
	d := newTestDaemon(t, conflictConfig(), &linear.Fake{}, &fakeNative{})
	seams := &fakeReactSeams{}
	seams.install(d)
	id := restingSess(d, "FE-1")
	if _, err := d.handleAskMerge(context.Background(), id, "feature/FE-1"); err == nil {
		t.Error("merging a branch into itself was accepted")
	}
	stack := reactSess("FE-2", "working", nil)
	stack.AtPrompt, stack.Stack = true, true
	d.sessions.Upsert(stack)
	if _, err := d.handleAskMerge(context.Background(), stack.ID, "main"); err == nil {
		t.Error("a stack session was accepted")
	}
	if _, err := d.handleAskMerge(context.Background(), "nope", "main"); err == nil {
		t.Error("an unknown session was accepted")
	}
	if n := len(seams.sendCalls()); n != 0 {
		t.Errorf("%d sends, want none", n)
	}
}

// Like resolveConflict it refuses instead of deferring, and says why.
func TestAskMergeRefusesAMidTurnAgent(t *testing.T) {
	d, seams, g, _ := gateDaemon(t)
	id := restingSess(d, "FE-2")
	g.pane, g.cursor = gateWork, atComposer
	_, err := d.handleAskMerge(context.Background(), id, "main")
	if err == nil || !strings.Contains(err.Error(), "needs an idle agent") {
		t.Fatalf("err = %v, want the idle-agent refusal", err)
	}
	if n := len(seams.sendCalls()); n != 0 {
		t.Errorf("%d sends, want none", n)
	}
}
