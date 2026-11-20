package hubproto

import (
	"regexp"
	"time"
)

// The Linear AGENT-SESSION CLAIM. Several daemons of one org share one Linear
// app, so every one of them sees every `@lola` mention. Before it starts work a
// daemon asks the hub, which answers for exactly one of them:
//
//	POST PathAgentClaim      claim a session           -> AgentClaimResponse
//	POST PathAgentAuthorize  "I am about to spawn"     -> AgentAuthorizeResponse
//	POST PathAgentSettle     bound | released          -> AgentSettleResponse
//
// A claim carries the opaque Linear Agent Session id and nothing else about the
// work: no issue, no text, no repo, no path, no lola session id. The hub never
// parses the Linear webhook for it and never names an owner; the daemon derives
// the owner from Linear's own data and may only say who it believes that is.
// A claim is an ARBITRATION, not a command: a granted claim lets this daemon
// try, and the daemon still applies every gate of a Linear dispatch. A hub that
// grants wrongly can cause a missed or a doubled start, never code execution.
//
// FENCING. Each grant carries an epoch that only grows. The daemon asks to
// AUTHORIZE the epoch immediately before it spawns; the hub refuses any epoch
// that is no longer current or that another device now holds, so a daemon that
// stalled past its lease cannot start work a successor already took. After
// authorization the claim is never reassigned until it is settled or
// [AgentAuthorizedTTL] passes. Every field is optional on the wire to an older
// daemon: an older hub answers 404 and the daemon proceeds alone.
const (
	PathAgentClaim     = "/v1/linear/claim"
	PathAgentAuthorize = "/v1/linear/authorize"
	PathAgentSettle    = "/v1/linear/settle"

	// AgentClaimTTL is how long a granted, not yet authorized claim lasts. A
	// daemon that is only waiting (at its cap) renews it by claiming again.
	AgentClaimTTL = 60 * time.Second
	// AgentAuthorizedTTL bounds an authorized claim whose outcome never arrived
	// (the daemon died mid-spawn). After it the session may be claimed again.
	AgentAuthorizedTTL = 2 * time.Hour
	// AgentOwnerOnlineWindow is how recent a heartbeat must be for a device to
	// count as online when the hub defers to the session's owner.
	AgentOwnerOnlineWindow = 90 * time.Second
	// AgentClaimRetention is how long a settled claim is remembered.
	AgentClaimRetention = 30 * 24 * time.Hour
)

// Claim outcomes (Reason in the responses). A closed vocabulary.
const (
	AgentGranted     = "granted"      // this device holds the claim at Epoch
	AgentHeld        = "held"         // another device holds it
	AgentOwnerOnline = "owner_online" // the session's owner has a live daemon
	AgentDone        = "done"         // settled: some daemon already took it
	AgentStale       = "stale"        // authorize/settle with a superseded epoch
)

// Settle outcomes.
const (
	// SettleBound: the daemon bound a lola session to the Agent Session.
	SettleBound = "bound"
	// SettleReleased: the daemon gave up before or during the spawn; the
	// session may be claimed again.
	SettleReleased = "released"
)

var agentIDRe = regexp.MustCompile(`^[A-Za-z0-9-]{8,64}$`)

// ValidAgentSessionID reports whether s can be a Linear Agent Session id.
func ValidAgentSessionID(s string) bool { return agentIDRe.MatchString(s) }

// ValidLinearUserID reports whether s can be a Linear user id ("" is valid
// where a user is optional; callers test that themselves).
func ValidLinearUserID(s string) bool { return agentIDRe.MatchString(s) }

// AgentClaimRequest asks for the claim on one Agent Session.
type AgentClaimRequest struct {
	Session string `json:"session"`
	// Owner is the Linear user the daemon resolved as the session's owner
	// (delegate, else assignee, else creator). "" when none is known. The hub
	// uses it only to defer to that user's live daemon.
	Owner string `json:"owner,omitempty"`
}

// Validate checks the request's shape.
func (r AgentClaimRequest) Validate() error {
	if !ValidAgentSessionID(r.Session) {
		return invalid("session", "bad agent session id")
	}
	if r.Owner != "" && !ValidLinearUserID(r.Owner) {
		return invalid("owner", "bad linear user id")
	}
	return nil
}

// AgentClaimResponse answers a claim. Epoch is set only when Granted.
type AgentClaimResponse struct {
	Granted bool   `json:"granted"`
	Epoch   uint64 `json:"epoch,omitempty"`
	Reason  string `json:"reason"`
}

// AgentAuthorizeRequest asks to spawn under a granted epoch.
type AgentAuthorizeRequest struct {
	Session string `json:"session"`
	Epoch   uint64 `json:"epoch"`
}

// Validate checks the request's shape.
func (r AgentAuthorizeRequest) Validate() error {
	if !ValidAgentSessionID(r.Session) {
		return invalid("session", "bad agent session id")
	}
	if r.Epoch == 0 {
		return invalid("epoch", "zero")
	}
	return nil
}

// AgentAuthorizeResponse says whether the spawn may go ahead.
type AgentAuthorizeResponse struct {
	Authorized bool   `json:"authorized"`
	Reason     string `json:"reason"`
}

// AgentSettleRequest reports the outcome of an authorized (or granted) claim.
type AgentSettleRequest struct {
	Session string `json:"session"`
	Epoch   uint64 `json:"epoch"`
	Outcome string `json:"outcome"`
}

// Validate checks the request's shape.
func (r AgentSettleRequest) Validate() error {
	if !ValidAgentSessionID(r.Session) {
		return invalid("session", "bad agent session id")
	}
	if r.Epoch == 0 {
		return invalid("epoch", "zero")
	}
	if r.Outcome != SettleBound && r.Outcome != SettleReleased {
		return invalid("outcome", "not a settle outcome")
	}
	return nil
}

// AgentSettleResponse is the acknowledgement of a settle.
type AgentSettleResponse struct {
	Settled bool   `json:"settled"`
	Reason  string `json:"reason"`
}
