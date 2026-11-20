package agentclaim

import (
	"testing"
	"time"

	"github.com/sushidev-team/lola/hub/internal/store"
	"github.com/sushidev-team/lola/hubproto"
)

var t0 = time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

func TestFirstClaimWinsAndOthersAreHeld(t *testing.T) {
	m := map[string]*store.AgentClaim{}
	a := Claim(m, "s", "A", false, t0)
	b := Claim(m, "s", "B", false, t0.Add(time.Second))
	if !a.Granted || a.Epoch != 1 || b.Granted || b.Reason != hubproto.AgentHeld {
		t.Fatalf("a=%+v b=%+v", a, b)
	}
	if r := Claim(m, "s", "A", false, t0.Add(10*time.Second)); !r.Granted || r.Epoch != 1 {
		t.Fatalf("a renewal keeps the epoch: %+v", r)
	}
}

// A holder that stalls past its lease is fenced: the successor takes epoch 2 and
// the old holder can neither authorize nor settle.
func TestExpiredHolderIsFenced(t *testing.T) {
	m := map[string]*store.AgentClaim{}
	Claim(m, "s", "A", false, t0)
	late := t0.Add(2 * hubproto.AgentClaimTTL)
	b := Claim(m, "s", "B", false, late)
	if !b.Granted || b.Epoch != 2 {
		t.Fatalf("successor = %+v", b)
	}
	if ok, why := Authorize(m, "s", "A", 1, late); ok || why != hubproto.AgentStale {
		t.Fatalf("a stale holder authorized: %v %s", ok, why)
	}
	if ok, _ := Settle(m, "s", "A", 1, hubproto.SettleBound, late); ok {
		t.Fatal("a stale holder settled")
	}
	if ok, _ := Authorize(m, "s", "B", 2, late); !ok {
		t.Fatal("the current holder must authorize")
	}
}

// Once authorized, a claim is not handed on, even after the claim TTL.
func TestAuthorizedClaimIsNeverReassignedEarly(t *testing.T) {
	m := map[string]*store.AgentClaim{}
	Claim(m, "s", "A", false, t0)
	Authorize(m, "s", "A", 1, t0)
	if r := Claim(m, "s", "B", false, t0.Add(time.Hour)); r.Granted || r.Reason != hubproto.AgentHeld {
		t.Fatalf("reassigned an authorized claim: %+v", r)
	}
	// The same device (restarted) reconciles its own authorized claim.
	if r := Claim(m, "s", "A", false, t0.Add(time.Hour)); !r.Granted || r.Epoch != 1 {
		t.Fatalf("holder could not reconcile: %+v", r)
	}
	// A spawn that never reported is recoverable after the authorized TTL.
	if r := Claim(m, "s", "B", false, t0.Add(hubproto.AgentAuthorizedTTL+time.Minute)); !r.Granted || r.Epoch != 2 {
		t.Fatalf("not recoverable: %+v", r)
	}
}

func TestSettleBoundClosesAndReleasedReopens(t *testing.T) {
	m := map[string]*store.AgentClaim{}
	Claim(m, "s", "A", false, t0)
	Authorize(m, "s", "A", 1, t0)
	Settle(m, "s", "A", 1, hubproto.SettleReleased, t0)
	r := Claim(m, "s", "B", false, t0)
	if !r.Granted || r.Epoch != 2 {
		t.Fatalf("a released claim must reopen with a new epoch: %+v", r)
	}
	Authorize(m, "s", "B", 2, t0)
	Settle(m, "s", "B", 2, hubproto.SettleBound, t0)
	if r := Claim(m, "s", "C", false, t0.Add(48*time.Hour)); r.Granted || r.Reason != hubproto.AgentDone {
		t.Fatalf("a bound session must stay done: %+v", r)
	}
}

func TestOwnerDefersOnlyAFreshGrant(t *testing.T) {
	m := map[string]*store.AgentClaim{}
	if r := Claim(m, "s", "B", true, t0); r.Granted || r.Reason != hubproto.AgentOwnerOnline {
		t.Fatalf("must defer to the online owner: %+v", r)
	}
	if len(m) != 0 {
		t.Fatal("a deferral must leave no claim behind")
	}
	// The owner's own daemon is never told to defer to itself (the caller passes false).
	if r := Claim(m, "s", "OWNER", false, t0); !r.Granted {
		t.Fatalf("%+v", r)
	}
	// An existing holder's renewal is not deferred.
	if r := Claim(m, "s", "OWNER", true, t0.Add(time.Second)); !r.Granted {
		t.Fatalf("%+v", r)
	}
}

func TestPrune(t *testing.T) {
	m := map[string]*store.AgentClaim{"old": {State: StateSettled, Updated: t0}, "live": {State: StateClaimed, Updated: t0}}
	Prune(m, t0.Add(hubproto.AgentClaimRetention+time.Hour))
	if m["old"] != nil || m["live"] == nil {
		t.Fatalf("%v", m)
	}
}
