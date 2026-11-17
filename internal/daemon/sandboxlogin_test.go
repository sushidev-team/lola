package daemon

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/sushidev-team/lola/internal/agent"
	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/linear"
	"github.com/sushidev-team/lola/internal/session"
	"github.com/sushidev-team/lola/internal/state"
)

// reloginNative is fakeNative plus the optional sandbox-login surface.
type reloginNative struct {
	fakeNative
	missing   map[string]bool
	available bool
	relogins  []string
}

func (n *reloginNative) SandboxLoginMissing(s session.Session) bool { return n.missing[s.ID] }
func (n *reloginNative) SandboxLoginAvailable(agent.Kind) bool      { return n.available }
func (n *reloginNative) Relogin(_ context.Context, s session.Session) (session.Session, error) {
	n.relogins = append(n.relogins, s.ID)
	n.missing[s.ID] = false
	s.TmuxName = s.ID
	return s, nil
}

func TestMaybeReloginRelaunchesOnceALoginExists(t *testing.T) {
	nat := &reloginNative{missing: map[string]bool{"s1": true}}
	d := newTestDaemon(t, testConfig(labelPoll("p1")), &linear.Fake{}, nat)
	s := session.Session{ID: "s1", Project: "p1", Agent: "claude", Sandbox: config.SandboxDocker}
	s.SetAgentState(state.AgentIdle, "", time.Now())
	d.sessions.Upsert(s)

	d.maybeRelogin(context.Background(), s)
	if len(nat.relogins) != 0 {
		t.Fatal("relaunched with no login to give it")
	}

	nat.available = true
	d.relogin = reloginState{} // the empty-handed attempt above never stamped
	d.maybeRelogin(context.Background(), s)
	if strings.Join(nat.relogins, ",") != "s1" {
		t.Fatalf("relogins = %v", nat.relogins)
	}
	if cur, _ := d.sessions.Get("s1"); cur.AgentState != state.AgentStarting {
		t.Fatalf("agent state = %q, want starting", cur.AgentState)
	}
	// Fixed now: nothing happens again.
	d.relogin = reloginState{}
	d.maybeRelogin(context.Background(), s)
	if len(nat.relogins) != 1 {
		t.Fatalf("relaunched a session that has its login: %v", nat.relogins)
	}
}

func TestMaybeReloginLeavesDeadSessionsToRevive(t *testing.T) {
	nat := &reloginNative{missing: map[string]bool{"s1": true}, available: true}
	d := newTestDaemon(t, testConfig(labelPoll("p1")), &linear.Fake{}, nat)
	s := session.Session{ID: "s1", Project: "p1", Agent: "claude", Sandbox: config.SandboxDocker}
	s.SetAgentState(state.AgentDead, "", time.Now())
	d.maybeRelogin(context.Background(), s)
	if len(nat.relogins) != 0 {
		t.Fatalf("relaunched a dead session: %v", nat.relogins)
	}
}

func TestTickHoldsASandboxedProjectWithoutALogin(t *testing.T) {
	is := testIssue("FE-1", 1, "2024-01-01T00:00:00Z")
	nat := &reloginNative{missing: map[string]bool{}}
	cfg := testConfig(labelPoll("p1"))
	cfg.Projects[0].Sandbox = config.SandboxDocker
	d := newTestDaemon(t, cfg, &linear.Fake{Issues: []linear.Issue{is}}, nat)
	_, err := d.tick(context.Background(), "p1", false)
	if err == nil || !strings.Contains(err.Error(), SandboxLoginMissingText) {
		t.Fatalf("tick err = %v, want the login refusal", err)
	}
	if len(nat.spawnCalls()) != 0 {
		t.Fatalf("spawned without a login: %v", nat.spawnCalls())
	}
	nat.available = true
	if _, err := d.tick(context.Background(), "p1", false); err != nil && strings.Contains(err.Error(), SandboxLoginMissingText) {
		t.Fatalf("still held with a login: %v", err)
	}
}
