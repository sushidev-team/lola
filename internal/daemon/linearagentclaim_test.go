package daemon

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/sushidev-team/lola/hubproto"
	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/hub"
	"github.com/sushidev-team/lola/internal/linear"
)

// arbiter is a one-session stand-in for the hub: the first device to claim wins,
// authorize must name the holder's epoch.
type arbiter struct {
	mu        sync.Mutex
	holder    string
	epoch     uint64
	denyAuth  bool
	claimErr  error
	settled   []string
	authCalls int
}

// arbClient is one device's view of the arbiter, as a hubAPI + agentClaimer.
type arbClient struct {
	hubAPI
	a   *arbiter
	dev string
}

func (c arbClient) ClaimAgentSession(_ context.Context, r hubproto.AgentClaimRequest) (hubproto.AgentClaimResponse, error) {
	c.a.mu.Lock()
	defer c.a.mu.Unlock()
	if c.a.claimErr != nil {
		return hubproto.AgentClaimResponse{}, c.a.claimErr
	}
	if c.a.holder == "" || c.a.holder == c.dev {
		if c.a.holder == "" {
			c.a.epoch++
		}
		c.a.holder = c.dev
		return hubproto.AgentClaimResponse{Granted: true, Epoch: c.a.epoch, Reason: hubproto.AgentGranted}, nil
	}
	return hubproto.AgentClaimResponse{Reason: hubproto.AgentHeld}, nil
}

func (c arbClient) AuthorizeAgentSession(_ context.Context, r hubproto.AgentAuthorizeRequest) (hubproto.AgentAuthorizeResponse, error) {
	c.a.mu.Lock()
	defer c.a.mu.Unlock()
	c.a.authCalls++
	ok := !c.a.denyAuth && c.a.holder == c.dev && r.Epoch == c.a.epoch
	return hubproto.AgentAuthorizeResponse{Authorized: ok, Reason: hubproto.AgentGranted}, nil
}

func (c arbClient) SettleAgentSession(_ context.Context, r hubproto.AgentSettleRequest) (hubproto.AgentSettleResponse, error) {
	c.a.mu.Lock()
	defer c.a.mu.Unlock()
	c.a.settled = append(c.a.settled, c.dev+":"+r.Outcome)
	return hubproto.AgentSettleResponse{Settled: true}, nil
}

func claimDaemon(t *testing.T, a *arbiter, dev, mode string) (*Daemon, *fakeAgentAPI, *fakeNative) {
	t.Helper()
	p := config.Project{Name: "app", Path: "/tmp/app", Repo: "acme/app", DefaultBranch: "main", TeamID: "team-1"}
	d, api, nat := agentDaemon(t, p)
	d.cfg.Hub = config.HubConfig{Enabled: true, URL: "https://hub.example"}
	d.cfg.LinearAgent.HubClaim = mode
	d.hub.newClient = func(url, _ string) (hubAPI, hub.Identity, error) {
		return arbClient{a: a, dev: dev}, hub.Identity{URL: url, DeviceID: dev, Org: "acme"}, nil
	}
	api.sessions = []linear.AgentSession{pendingSession("as-1", "ENG-7", "team-1")}
	return d, api, nat
}

// Two daemons, one mention: only the claim holder acknowledges, spawns and
// settles; the other stays silent.
func TestHubClaimLetsExactlyOneDaemonStart(t *testing.T) {
	a := &arbiter{}
	d1, api1, nat1 := claimDaemon(t, a, "dev-1", "")
	d2, api2, nat2 := claimDaemon(t, a, "dev-2", "")

	d1.linearAgentCycle(context.Background())
	d2.linearAgentCycle(context.Background())

	if len(nat1.spawnCalls()) != 1 || len(nat2.spawnCalls()) != 0 {
		t.Fatalf("spawns: dev-1=%d dev-2=%d", len(nat1.spawnCalls()), len(nat2.spawnCalls()))
	}
	if len(api2.kinds("as-1")) != 0 {
		t.Fatalf("the daemon without the claim must stay silent: %v", api2.kinds("as-1"))
	}
	if got := api1.kinds("as-1"); len(got) < 2 || got[0] != "thought" {
		t.Fatalf("the holder must acknowledge first, then bind: %v", got)
	}
	if len(a.settled) != 1 || a.settled[0] != "dev-1:bound" {
		t.Fatalf("settled = %v", a.settled)
	}
}

// A daemon whose epoch went stale is refused at authorize and spawns nothing.
func TestHubClaimStaleEpochDoesNotSpawn(t *testing.T) {
	a := &arbiter{denyAuth: true}
	d, _, nat := claimDaemon(t, a, "dev-1", "")
	d.linearAgentCycle(context.Background())
	if len(nat.spawnCalls()) != 0 || a.authCalls != 1 {
		t.Fatalf("spawns=%d authorize calls=%d", len(nat.spawnCalls()), a.authCalls)
	}
}

// A hub that cannot answer: auto keeps a lone daemon working, required refuses.
func TestHubClaimOutageAutoProceedsRequiredDoesNot(t *testing.T) {
	for _, tc := range []struct {
		mode  string
		spawn int
	}{{config.HubClaimAuto, 1}, {config.HubClaimRequired, 0}} {
		a := &arbiter{claimErr: errors.New("hub down")}
		d, _, nat := claimDaemon(t, a, "dev-1", tc.mode)
		d.linearAgentCycle(context.Background())
		if got := len(nat.spawnCalls()); got != tc.spawn {
			t.Fatalf("%s: spawns = %d, want %d", tc.mode, got, tc.spawn)
		}
	}
}

// An older hub (404) is no arbiter in auto mode; "off" never asks.
func TestHubClaimOldHubAndOff(t *testing.T) {
	old := &arbiter{claimErr: &hub.APIError{Status: 404}}
	d, _, nat := claimDaemon(t, old, "dev-1", "")
	d.linearAgentCycle(context.Background())
	if len(nat.spawnCalls()) != 1 {
		t.Fatalf("an older hub must not block a lone daemon: %d", len(nat.spawnCalls()))
	}
	held := &arbiter{holder: "someone-else", epoch: 1}
	d2, _, nat2 := claimDaemon(t, held, "dev-1", config.HubClaimOff)
	d2.linearAgentCycle(context.Background())
	if len(nat2.spawnCalls()) != 1 {
		t.Fatalf("hub_claim = off must not ask the hub: %d", len(nat2.spawnCalls()))
	}
}

// The status says how this daemon is arbitrating and how many sessions it left
// to someone else.
func TestLinearAgentStatusShowsOwnershipAndWaiting(t *testing.T) {
	a := &arbiter{holder: "someone-else", epoch: 1}
	d, _, _ := claimDaemon(t, a, "dev-1", "")
	d.cfg.LinearAgent.OnlyWhenAssignedToMe = false
	d.linearAgentCycle(context.Background())
	st := d.linearAgentStatus()
	if st == nil || st.HubClaim != config.HubClaimAuto || st.Waiting != 1 {
		t.Fatalf("status = %+v", st)
	}
}
