package daemon

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/sushidev-team/lola/internal/agent"
	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/linear"
	"github.com/sushidev-team/lola/internal/notify"
	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/session"
	"github.com/sushidev-team/lola/internal/state"
)

// fallbackTestConfig builds a one-project config carrying the agent_fallback
// chain on [defaults] and the reaction's auto flag.
func fallbackTestConfig(auto bool, chain []string) *config.Config {
	cfg := testConfig(labelPoll("p1"))
	cfg.Reactions.AgentFallback.Auto = auto
	cfg.Defaults.AgentFallback = chain
	return cfg
}

// quotaSession is a native Linear session whose agent is parked on a
// usage-limit banner.
func quotaSession(id, project, agentKind string) session.Session {
	return session.Session{
		ID:          id,
		Source:      "native",
		Kind:        session.KindLinear,
		Project:     project,
		Issue:       "ENG-1",
		IssueUUID:   "uuid-eng-1",
		Branch:      "lola/eng-1",
		Worktree:    "/tmp/wt/" + id,
		TmuxName:    id,
		Agent:       agentKind,
		Status:      "needs_input",
		AgentState:  state.AgentWaitingInput,
		InputReason: state.InputQuotaLimited,
	}
}

func TestNextFallback(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		chain   []string
		current agent.Kind
		tried   []string
		want    agent.Kind
		ok      bool
	}{
		{"first available", []string{"codex", "opencode"}, agent.Claude, nil, agent.Codex, true},
		{"skips current", []string{"codex", "opencode"}, agent.Codex, nil, agent.OpenCode, true},
		{"skips tried", []string{"claude", "codex"}, agent.Codex, []string{"claude"}, "", false},
		{"skips tried and current", []string{"claude", "codex", "opencode"}, agent.Codex, []string{"claude"}, agent.OpenCode, true},
		{"empty chain", nil, agent.Claude, nil, "", false},
		// A stray value degrades to claude via Parse; claude-as-current is
		// skipped like any other, so the chain yields nothing.
		{"unknown entry degrades", []string{"emacs"}, agent.Claude, nil, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := nextFallback(tc.chain, tc.current, tc.tried)
			if ok != tc.ok || got != tc.want {
				t.Errorf("nextFallback(%v, %s, %v) = (%q, %t), want (%q, %t)",
					tc.chain, tc.current, tc.tried, got, ok, tc.want, tc.ok)
			}
		})
	}
}

func TestMaybeFallbackNotifyOnly(t *testing.T) {
	nat := &fakeNative{}
	d := newTestDaemon(t, fallbackTestConfig(false, []string{"codex"}), &linear.Fake{}, nat)
	seams := &fakeReactSeams{}
	seams.install(d)

	s := quotaSession("lola-p1-eng-1", "p1", "claude")
	d.sessions.Upsert(s)
	d.maybeFallback(context.Background(), s)

	if got := nat.switchCalls(); len(got) != 0 {
		t.Fatalf("notify-only mode must not switch, got %+v", got)
	}
	urgent := seams.notesByPriority(notify.Urgent)
	if len(urgent) != 1 || !strings.Contains(urgent[0].Body, "switch the session to codex") {
		t.Fatalf("want one Urgent suggestion naming codex, got %+v", seams.notes)
	}
	cur, ok := d.sessions.Get(s.ID)
	if !ok || cur.FallbackNotified != "claude" {
		t.Fatalf("want FallbackNotified=claude stamped, got %+v", cur)
	}

	// One-shot per episode: the observer passes the freshly-read store record
	// each cycle, so the stamped guard keeps the second pass silent.
	cur2, _ := d.sessions.Get(s.ID)
	d.maybeFallback(context.Background(), cur2)
	if seams.noteCount() != 1 {
		t.Fatalf("one-shot guard re-fired: %+v", seams.notes)
	}
}

func TestMaybeFallbackAutoSwitches(t *testing.T) {
	nat := &fakeNative{}
	d := newTestDaemon(t, fallbackTestConfig(true, []string{"codex"}), &linear.Fake{}, nat)
	seams := &fakeReactSeams{}
	seams.install(d)

	s := quotaSession("lola-p1-eng-1", "p1", "claude")
	d.sessions.Upsert(s)
	d.maybeFallback(context.Background(), s)

	switches := nat.switchCalls()
	if len(switches) != 1 || switches[0] != (nativeSwitchCall{s.ID, "codex", "hit its usage limit"}) {
		t.Fatalf("want one switch to codex, got %+v", switches)
	}
	cur, ok := d.sessions.Get(s.ID)
	if !ok {
		t.Fatal("session vanished from store")
	}
	if cur.Agent != "codex" || !slices.Equal(cur.AgentsTried, []string{"claude"}) {
		t.Errorf("want Agent=codex AgentsTried=[claude], got Agent=%s tried=%v", cur.Agent, cur.AgentsTried)
	}
	if cur.FallbackNotified != "" || cur.AgentState != state.AgentStarting || cur.AtPrompt {
		t.Errorf("want a re-armed, closed-gate starting record, got %+v", cur)
	}
	action := seams.notesByPriority(notify.Action)
	if len(action) != 1 || !strings.Contains(action[0].Body, "handed to codex") {
		t.Fatalf("want one Action switch notification, got %+v", seams.notes)
	}
}

func TestMaybeFallbackNoChainNotifiesUnavailable(t *testing.T) {
	nat := &fakeNative{}
	d := newTestDaemon(t, fallbackTestConfig(true, nil), &linear.Fake{}, nat)
	seams := &fakeReactSeams{}
	seams.install(d)

	s := quotaSession("lola-p1-eng-1", "p1", "claude")
	d.sessions.Upsert(s)
	d.maybeFallback(context.Background(), s)

	if got := nat.switchCalls(); len(got) != 0 {
		t.Fatalf("no chain must not switch, got %+v", got)
	}
	urgent := seams.notesByPriority(notify.Urgent)
	if len(urgent) != 1 || !strings.Contains(urgent[0].Body, "no fallback agent is available") {
		t.Fatalf("want one Urgent no-fallback notification, got %+v", seams.notes)
	}
}

func TestMaybeFallbackSkipsMissingBinary(t *testing.T) {
	nat := &fakeNative{}
	d := newTestDaemon(t, fallbackTestConfig(true, []string{"codex"}), &linear.Fake{}, nat)
	seams := &fakeReactSeams{}
	seams.install(d)
	d.runtimeHealth = func(bin string) error {
		if bin == "codex" {
			return errors.New("no codex on PATH")
		}
		return nil
	}

	s := quotaSession("lola-p1-eng-1", "p1", "claude")
	d.sessions.Upsert(s)
	d.maybeFallback(context.Background(), s)

	// The health gate runs BEFORE the teardown: a missing target binary costs
	// a notification, never the running pane.
	if got := nat.switchCalls(); len(got) != 0 {
		t.Fatalf("missing binary must not switch, got %+v", got)
	}
	urgent := seams.notesByPriority(notify.Urgent)
	if len(urgent) != 1 || !strings.Contains(urgent[0].Body, "no fallback agent is available") {
		t.Fatalf("want the no-fallback notification, got %+v", seams.notes)
	}
}

func TestMaybeFallbackChainAdvancesPastTried(t *testing.T) {
	nat := &fakeNative{}
	d := newTestDaemon(t, fallbackTestConfig(true, []string{"claude", "codex", "opencode"}), &linear.Fake{}, nat)
	seams := &fakeReactSeams{}
	seams.install(d)

	s := quotaSession("lola-p1-eng-1", "p1", "codex")
	s.AgentsTried = []string{"claude"}
	d.sessions.Upsert(s)
	d.maybeFallback(context.Background(), s)

	// claude is tried, codex is current: the chain continues to opencode and
	// never loops back to a kind the session already ran.
	switches := nat.switchCalls()
	if len(switches) != 1 || switches[0].kind != "opencode" {
		t.Fatalf("want the chain to continue to opencode, got %+v", switches)
	}
}

func TestMaybeFallbackRearmsOffQuota(t *testing.T) {
	nat := &fakeNative{}
	d := newTestDaemon(t, fallbackTestConfig(true, []string{"codex"}), &linear.Fake{}, nat)
	seams := &fakeReactSeams{}
	seams.install(d)

	// A record stamped from an earlier episode but no longer quota-limited
	// (the limit reset, or a switch landed) re-arms the one-shot guard.
	s := quotaSession("lola-p1-eng-1", "p1", "claude")
	s.FallbackNotified = "claude"
	s.AgentState = state.AgentWorking
	s.InputReason = ""
	s.Status = "working"
	d.sessions.Upsert(s)
	d.maybeFallback(context.Background(), s)

	if seams.noteCount() != 0 || len(nat.switchCalls()) != 0 {
		t.Fatalf("a non-quota record must be a no-op, got notes=%+v switches=%+v", seams.notes, nat.switchCalls())
	}
	cur, _ := d.sessions.Get(s.ID)
	if cur.FallbackNotified != "" {
		t.Errorf("want the stale guard cleared, got %q", cur.FallbackNotified)
	}
}

func TestHandleSwitchAgentRefusals(t *testing.T) {
	newD := func() (*Daemon, *fakeNative) {
		nat := &fakeNative{}
		d := newTestDaemon(t, fallbackTestConfig(false, nil), &linear.Fake{}, nat)
		seams := &fakeReactSeams{}
		seams.install(d)
		return d, nat
	}

	t.Run("empty args", func(t *testing.T) {
		d, _ := newD()
		if _, err := d.handleSwitchAgent(context.Background(), protocol.SwitchAgentArgs{Session: "x"}); err == nil {
			t.Fatal("want an error for a missing agent kind")
		}
	})
	t.Run("invalid kind", func(t *testing.T) {
		d, _ := newD()
		_, err := d.handleSwitchAgent(context.Background(), protocol.SwitchAgentArgs{Session: "x", Agent: "emacs"})
		if err == nil || !strings.Contains(err.Error(), "unknown agent kind") {
			t.Fatalf("want unknown-kind refusal, got %v", err)
		}
	})
	t.Run("unknown session", func(t *testing.T) {
		d, _ := newD()
		_, err := d.handleSwitchAgent(context.Background(), protocol.SwitchAgentArgs{Session: "nope", Agent: "codex"})
		if err == nil || !strings.Contains(err.Error(), "unknown session") {
			t.Fatalf("want unknown-session refusal, got %v", err)
		}
	})
	t.Run("agentless shell", func(t *testing.T) {
		d, _ := newD()
		s := quotaSession("lola-p1-eng-1", "p1", "")
		s.Agentless = true
		d.sessions.Upsert(s)
		_, err := d.handleSwitchAgent(context.Background(), protocol.SwitchAgentArgs{Session: s.ID, Agent: "codex"})
		if err == nil || !strings.Contains(err.Error(), "no coding agent") {
			t.Fatalf("want agentless refusal, got %v", err)
		}
	})
	t.Run("same kind", func(t *testing.T) {
		d, _ := newD()
		s := quotaSession("lola-p1-eng-1", "p1", "claude")
		d.sessions.Upsert(s)
		_, err := d.handleSwitchAgent(context.Background(), protocol.SwitchAgentArgs{Session: s.ID, Agent: "claude"})
		if err == nil || !strings.Contains(err.Error(), "already runs") {
			t.Fatalf("want same-kind refusal, got %v", err)
		}
	})
	t.Run("target binary missing", func(t *testing.T) {
		d, nat := newD()
		s := quotaSession("lola-p1-eng-1", "p1", "claude")
		d.sessions.Upsert(s)
		d.runtimeHealth = func(bin string) error {
			if bin == "codex" {
				return errors.New("no codex on PATH")
			}
			return nil
		}
		_, err := d.handleSwitchAgent(context.Background(), protocol.SwitchAgentArgs{Session: s.ID, Agent: "codex"})
		if err == nil || !strings.Contains(err.Error(), "runtime not ready") {
			t.Fatalf("want a runtime-not-ready refusal, got %v", err)
		}
		if got := nat.switchCalls(); len(got) != 0 {
			t.Fatalf("the refusal must precede any teardown, got switches %+v", got)
		}
	})
}

func TestHandleSwitchAgentHappyPath(t *testing.T) {
	nat := &fakeNative{}
	d := newTestDaemon(t, fallbackTestConfig(false, nil), &linear.Fake{}, nat)
	seams := &fakeReactSeams{}
	seams.install(d)

	// A mid-turn agent switches too: the old pane is replaced, not typed
	// into, so there is deliberately no idle gate on the manual path.
	s := quotaSession("lola-p1-eng-1", "p1", "claude")
	s.AgentState = state.AgentWorking
	s.InputReason = ""
	s.Status = "working"
	d.sessions.Upsert(s)

	data, err := d.handleSwitchAgent(context.Background(), protocol.SwitchAgentArgs{Session: s.ID, Agent: "codex"})
	if err != nil {
		t.Fatalf("handleSwitchAgent: %v", err)
	}
	if data.Agent != "codex" || !strings.Contains(data.Message, "claude → codex") {
		t.Errorf("unexpected data: %+v", data)
	}
	switches := nat.switchCalls()
	if len(switches) != 1 || switches[0] != (nativeSwitchCall{s.ID, "codex", "switched by the operator"}) {
		t.Fatalf("want one operator switch, got %+v", switches)
	}
	cur, _ := d.sessions.Get(s.ID)
	if cur.Agent != "codex" || !slices.Equal(cur.AgentsTried, []string{"claude"}) || cur.AgentState != state.AgentStarting {
		t.Errorf("store not carried over: %+v", cur)
	}
}
