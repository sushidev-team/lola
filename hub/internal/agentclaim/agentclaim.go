// Package agentclaim is the PURE arbiter of Linear Agent Sessions: no I/O, no
// clock of its own (every call takes now), no store access beyond the map it is
// handed. It decides which one device of an org may start work on a session.
//
//	(none) ──claim──▶ claimed ──authorize──▶ authorized ──settle bound──▶ settled
//	                     │  ▲                     │
//	                     │  └─claim, same device──┘ (renews / reconciles)
//	                     ├─expiry──▶ claim by anyone, epoch+1
//	                     └─settle released──▶ released ──claim──▶ epoch+1
//
// The epoch grows on every NEW grant and fences the old holder: authorize and
// settle must name the current epoch AND the device holding it. An authorized
// claim is never handed to another device before its TTL, so a spawn that was
// authorized cannot be duplicated by a successor.
package agentclaim

import (
	"time"

	"github.com/sushidev-team/lola/hub/internal/store"
	"github.com/sushidev-team/lola/hubproto"
)

// Claim states.
const (
	StateClaimed    = "claimed"
	StateAuthorized = "authorized"
	StateSettled    = "settled"
	StateReleased   = "released"
)

// Result is the outcome of a claim.
type Result struct {
	Granted bool
	Epoch   uint64
	Reason  string
}

// Claim asks for session on behalf of dev. ownerOnline says a DIFFERENT device
// of the session's owner has a live heartbeat: a new grant then defers to it.
// It never defers an existing holder's renewal.
func Claim(m map[string]*store.AgentClaim, session, dev string, ownerOnline bool, now time.Time) Result {
	c := m[session]
	if c != nil {
		switch c.State {
		case StateSettled:
			return Result{Reason: hubproto.AgentDone}
		case StateAuthorized:
			if c.Device == dev {
				return Result{Granted: true, Epoch: c.Epoch, Reason: hubproto.AgentGranted}
			}
			if now.Before(c.Expires) {
				return Result{Reason: hubproto.AgentHeld}
			}
		case StateClaimed:
			if c.Device == dev { // a renewal, also after a lapse nobody used
				c.Expires = now.Add(hubproto.AgentClaimTTL)
				c.Updated = now
				return Result{Granted: true, Epoch: c.Epoch, Reason: hubproto.AgentGranted}
			}
			if now.Before(c.Expires) {
				return Result{Reason: hubproto.AgentHeld}
			}
		}
	}
	if ownerOnline {
		return Result{Reason: hubproto.AgentOwnerOnline}
	}
	epoch := uint64(1)
	if c != nil {
		epoch = c.Epoch + 1
	}
	m[session] = &store.AgentClaim{Device: dev, Epoch: epoch, State: StateClaimed,
		Expires: now.Add(hubproto.AgentClaimTTL), Updated: now}
	return Result{Granted: true, Epoch: epoch, Reason: hubproto.AgentGranted}
}

// Authorize turns the current grant into permission to spawn.
func Authorize(m map[string]*store.AgentClaim, session, dev string, epoch uint64, now time.Time) (bool, string) {
	c := m[session]
	if c == nil || c.Device != dev || c.Epoch != epoch {
		return false, hubproto.AgentStale
	}
	switch c.State {
	case StateClaimed, StateAuthorized:
		c.State = StateAuthorized
		c.Expires = now.Add(hubproto.AgentAuthorizedTTL)
		c.Updated = now
		return true, hubproto.AgentGranted
	case StateSettled:
		return false, hubproto.AgentDone
	}
	return false, hubproto.AgentStale
}

// Settle records how an authorized or granted claim ended.
func Settle(m map[string]*store.AgentClaim, session, dev string, epoch uint64, outcome string, now time.Time) (bool, string) {
	c := m[session]
	if c == nil || c.Device != dev || c.Epoch != epoch {
		return false, hubproto.AgentStale
	}
	if c.State != StateClaimed && c.State != StateAuthorized {
		if c.State == StateSettled && outcome == hubproto.SettleBound {
			return true, hubproto.AgentDone // idempotent
		}
		return false, hubproto.AgentStale
	}
	if outcome == hubproto.SettleBound {
		c.State = StateSettled
	} else {
		c.State = StateReleased
	}
	c.Updated = now
	return true, hubproto.AgentGranted
}

// Prune forgets settled and released claims older than the retention.
func Prune(m map[string]*store.AgentClaim, now time.Time) {
	for id, c := range m {
		if (c.State == StateSettled || c.State == StateReleased) && now.Sub(c.Updated) > hubproto.AgentClaimRetention {
			delete(m, id)
		}
	}
}
