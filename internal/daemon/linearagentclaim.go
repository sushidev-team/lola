package daemon

import (
	"context"
	"errors"
	"time"

	"github.com/sushidev-team/lola/hubproto"
	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/hub"
	"github.com/sushidev-team/lola/internal/linear"
)

// HUB ARBITRATION of Linear agent sessions (hubproto/agentclaim.go).
//
// Several daemons of one org share one Linear app, so all of them see every
// mention. With a joined hub, a daemon claims the Agent Session before it does
// anything visible: claim -> acknowledge at once -> authorize -> spawn ->
// settle. The hub answers for exactly one daemon, defers to the session's owner
// when that owner's daemon is online, and fences a daemon that stalled past its
// lease (a stale epoch cannot authorize). The grant is an ARBITRATION, not a
// command: the winner still applies every gate of a Linear dispatch, and the
// hub carries only the opaque session id and an owner id this daemon read from
// Linear itself.
//
// [linear_agent].hub_claim: "auto" proceeds alone when the hub cannot answer (a
// single daemon must keep working through a hub outage), "required" never
// starts without a grant, "off" never asks.

const (
	// agentClaimTimeout bounds every hub call of the flow.
	agentClaimTimeout = 8 * time.Second
	// agentUnservedAfter is how long a session may wait, unclaimed, for a member
	// who can serve its team before a daemon that cannot says so.
	agentUnservedAfter = 3 * time.Minute
)

// agentClaimer is the optional capability of a hub client (hubAPI is
// deliberately not widened: every test fake would have to grow it).
type agentClaimer interface {
	ClaimAgentSession(ctx context.Context, req hubproto.AgentClaimRequest) (hubproto.AgentClaimResponse, error)
	AuthorizeAgentSession(ctx context.Context, req hubproto.AgentAuthorizeRequest) (hubproto.AgentAuthorizeResponse, error)
	SettleAgentSession(ctx context.Context, req hubproto.AgentSettleRequest) (hubproto.AgentSettleResponse, error)
}

// agentGrant is a granted claim. A nil *agentGrant means "no arbiter in play"
// (hub off, unreachable in auto mode, or an older hub): every method is a no-op
// that allows the action.
type agentGrant struct {
	d        *Daemon
	c        agentClaimer
	session  string
	epoch    uint64
	required bool
}

// agentArbiter returns the hub client and mode when arbitration applies.
func (d *Daemon) agentArbiter() (agentClaimer, string, bool) {
	d.mu.Lock()
	cfg := d.cfg
	d.mu.Unlock()
	if cfg == nil || !cfg.Hub.Enabled || !cfg.LinearAgent.Enabled {
		return nil, "", false
	}
	mode := cfg.LinearAgent.EffectiveHubClaim()
	if mode == config.HubClaimOff {
		return nil, "", false
	}
	api, _, err := d.hub.newClient(cfg.Hub.URL, d.hubDir())
	if err != nil {
		return nil, mode, false // not joined
	}
	c, ok := api.(agentClaimer)
	return c, mode, ok
}

// agentClaimActive reports whether a hub would arbitrate (cheap, no call).
func (d *Daemon) agentClaimActive() bool {
	_, _, ok := d.agentArbiter()
	return ok
}

// claimAgentSession asks the hub for the session. proceed=false means skip it
// silently this cycle (held by another daemon, deferred to the owner, or
// required-mode without an answer); the grant may be nil with proceed=true when
// no arbiter applies.
func (d *Daemon) claimAgentSession(ctx context.Context, as linear.AgentSession) (grant *agentGrant, proceed bool) {
	c, mode, ok := d.agentArbiter()
	if !ok {
		// Not joined or an older client: auto proceeds alone, required does not.
		return nil, mode != config.HubClaimRequired
	}
	d.agent.mu.Lock()
	appID := d.agent.viewerID
	d.agent.mu.Unlock()
	cctx, cancel := context.WithTimeout(ctx, agentClaimTimeout)
	defer cancel()
	resp, err := c.ClaimAgentSession(cctx, hubproto.AgentClaimRequest{Session: as.ID, Owner: as.Owner(appID)})
	if err != nil {
		if hubStatusIs(err, 404) || errors.Is(err, hub.ErrNotJoined) {
			return nil, mode != config.HubClaimRequired // an older hub: no arbiter
		}
		d.logf("", "linear agent: hub claim for %s: %v", as.ID, err)
		return nil, mode == config.HubClaimAuto
	}
	if !resp.Granted {
		return nil, false
	}
	return &agentGrant{d: d, c: c, session: as.ID, epoch: resp.Epoch, required: mode == config.HubClaimRequired}, true
}

// authorize asks to spawn. False means do NOT spawn: a successor holds the claim
// (stale epoch), or required mode could not confirm.
func (g *agentGrant) authorize(ctx context.Context) bool {
	if g == nil {
		return true
	}
	cctx, cancel := context.WithTimeout(ctx, agentClaimTimeout)
	defer cancel()
	resp, err := g.c.AuthorizeAgentSession(cctx, hubproto.AgentAuthorizeRequest{Session: g.session, Epoch: g.epoch})
	if err != nil {
		g.d.logf("", "linear agent: hub authorize for %s: %v", g.session, err)
		return !g.required
	}
	return resp.Authorized
}

// settle reports the outcome (best effort: a lost settle only lets the claim
// lapse, which the TTLs cover).
func (g *agentGrant) settle(ctx context.Context, outcome string) {
	if g == nil {
		return
	}
	cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), agentClaimTimeout)
	defer cancel()
	if _, err := g.c.SettleAgentSession(cctx, hubproto.AgentSettleRequest{Session: g.session, Epoch: g.epoch, Outcome: outcome}); err != nil {
		g.d.logf("", "linear agent: hub settle for %s: %v", g.session, err)
	}
}

// linearUserID is this member's own Linear user id for the heartbeat: the
// configured user_id, else the human API key's viewer (cached). "" when unknown.
func (d *Daemon) linearUserID(ctx context.Context) string {
	d.mu.Lock()
	cfg := d.cfg
	cached := d.viewerID
	d.mu.Unlock()
	if cfg == nil || !cfg.LinearAgent.Enabled {
		return ""
	}
	if cfg.LinearAgent.UserID != "" {
		return cfg.LinearAgent.UserID
	}
	if cached != "" {
		return cached
	}
	api, err := d.ensureLinear()
	if err != nil || api == nil {
		return ""
	}
	id, err := d.viewer(ctx, api)
	if err != nil {
		return ""
	}
	return id
}

// agentUnserved reports whether a pending session has waited long enough that a
// daemon with no project for its team should answer it instead of staying quiet.
// An unparseable creation time counts as old: better a clear answer than none.
func agentUnserved(as linear.AgentSession, now time.Time) bool {
	t, err := time.Parse(time.RFC3339, as.CreatedAt)
	if err != nil {
		return true
	}
	return now.Sub(t) > agentUnservedAfter
}
