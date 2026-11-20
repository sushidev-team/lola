package api

import (
	"net/http"
	"testing"

	"github.com/sushidev-team/lola/hubproto"
)

const (
	agentSession = "0b8d6a5e-1c2d-4e3f-9a8b-7c6d5e4f3a2b"
	userAna      = "aaaaaaaa-0000-4000-8000-000000000001"
)

func (d *daemon) beat(user string) {
	d.e.t.Helper()
	r := d.do(http.MethodPost, hubproto.PathHeartbeat, hubproto.HeartbeatRequest{
		FreeSlots: 1, Agents: []string{"claude"}, Repos: []string{}, LiveLeases: []string{}, LinearUserID: user,
	})
	if r.status != 200 {
		d.e.t.Fatalf("heartbeat: %d %s", r.status, r.body)
	}
}

func (d *daemon) claim(owner string) hubproto.AgentClaimResponse {
	d.e.t.Helper()
	r := d.do(http.MethodPost, hubproto.PathAgentClaim, hubproto.AgentClaimRequest{Session: agentSession, Owner: owner})
	if r.status != 200 {
		d.e.t.Fatalf("claim: %d %s", r.status, r.body)
	}
	var out hubproto.AgentClaimResponse
	r.decode(d.e.t, &out)
	return out
}

// Ana's daemon is online: Ben's claim for Ana's session is deferred, Ana's wins,
// and once Ana authorizes, nobody else can take it.
func TestAgentClaimDefersToTheOwnersLiveDaemon(t *testing.T) {
	e := newEnv(t)
	ana, ben := e.newDaemon(), e.newDaemon()
	ana.mustEnroll("acme")
	ben.mustEnroll("acme")
	ana.beat(userAna)
	ben.beat("")

	if r := ben.claim(userAna); r.Granted || r.Reason != hubproto.AgentOwnerOnline {
		t.Fatalf("ben = %+v", r)
	}
	ga := ana.claim(userAna)
	if !ga.Granted || ga.Epoch != 1 {
		t.Fatalf("ana = %+v", ga)
	}
	if r := ben.claim(userAna); r.Granted || r.Reason != hubproto.AgentHeld {
		t.Fatalf("ben while held = %+v", r)
	}
	au := ana.do(http.MethodPost, hubproto.PathAgentAuthorize, hubproto.AgentAuthorizeRequest{Session: agentSession, Epoch: ga.Epoch})
	var ar hubproto.AgentAuthorizeResponse
	au.decode(t, &ar)
	if !ar.Authorized {
		t.Fatalf("authorize = %s", au.body)
	}
	// Ben cannot authorize or settle a claim he does not hold.
	bad := ben.do(http.MethodPost, hubproto.PathAgentAuthorize, hubproto.AgentAuthorizeRequest{Session: agentSession, Epoch: ga.Epoch})
	bad.decode(t, &ar)
	if ar.Authorized {
		t.Fatal("a non-holder authorized")
	}
	st := ana.do(http.MethodPost, hubproto.PathAgentSettle, hubproto.AgentSettleRequest{Session: agentSession, Epoch: ga.Epoch, Outcome: hubproto.SettleBound})
	var sr hubproto.AgentSettleResponse
	st.decode(t, &sr)
	if !sr.Settled {
		t.Fatalf("settle = %s", st.body)
	}
	if r := ben.claim(""); r.Granted || r.Reason != hubproto.AgentDone {
		t.Fatalf("after bound = %+v", r)
	}
}

// When the owner's daemon has stopped heartbeating, the next one takes over.
func TestAgentClaimFallsBackWhenTheOwnerIsOffline(t *testing.T) {
	e := newEnv(t)
	ana, ben := e.newDaemon(), e.newDaemon()
	ana.mustEnroll("acme")
	ben.mustEnroll("acme")
	ana.beat(userAna)
	e.clk.Add(2 * hubproto.AgentOwnerOnlineWindow)
	ben.beat("")
	if r := ben.claim(userAna); !r.Granted {
		t.Fatalf("ben = %+v", r)
	}
}

// A claim is scoped to the device's org and carries no other request field.
func TestAgentClaimRejectsBadIDsAndIsOrgScoped(t *testing.T) {
	e := newEnv(t)
	a, b := e.newDaemon(), e.newDaemon()
	a.mustEnroll("acme")
	b.mustEnroll("beta")
	if r := a.do(http.MethodPost, hubproto.PathAgentClaim, hubproto.AgentClaimRequest{Session: "x"}); r.status != http.StatusBadRequest {
		t.Fatalf("bad id: %d", r.status)
	}
	if !a.claim("").Granted || !b.claim("").Granted {
		t.Fatal("two orgs must each hold their own claim on the same id")
	}
}
