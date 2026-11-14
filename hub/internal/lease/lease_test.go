package lease

import (
	"errors"
	"testing"
	"time"

	"github.com/sushidev-team/lola/hub/internal/store"
	"github.com/sushidev-team/lola/hubproto"
)

var t0 = time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

func adv(repo string, sev hubproto.Severity) hubproto.Item {
	return hubproto.Item{Kind: hubproto.KindDepAdvisory, DepAdvisory: &hubproto.DepAdvisory{
		Repo: repo, Ecosystem: "npm", Package: "lodash", Vulnerable: "< 4.17.21",
		Fixed: "4.17.21", Advisory: "GHSA-jf85-cpcp-j695", Severity: sev,
	}}
}

func bump(repo string, pr int) hubproto.Item {
	return hubproto.Item{Kind: hubproto.KindRedBumpPR, RedBumpPR: &hubproto.RedBumpPR{
		Repo: repo, PR: pr, Branch: "dependabot/npm/lodash-4.17.21", Package: "lodash", Author: hubproto.AuthorDependabot,
	}}
}

func dev(id string) *store.Device {
	return &store.Device{ID: id, Org: "acme", Repos: []string{"acme/web"}}
}

func newItem() *store.WorkItem {
	return NewItem("i1", "acme", adv("acme/web", hubproto.SeverityHigh), "a", t0)
}

func TestClaimOnlyOpenAndOneLease(t *testing.T) {
	w := newItem()
	a, b := dev("a"), dev("b")
	l, err := Claim(w, a, t0, Config{})
	if err != nil {
		t.Fatal(err)
	}
	if l.Device != "a" || !l.Expires.Equal(t0.Add(DefaultTTL)) || w.State != hubproto.StateClaimed {
		t.Fatalf("lease %+v state %s", l, w.State)
	}
	if _, err := Claim(w, b, t0.Add(time.Minute), Config{}); !errors.Is(err, ErrConflict) {
		t.Fatalf("second claim: %v", err)
	}
	if _, err := Claim(w, a, t0.Add(time.Minute), Config{}); !errors.Is(err, ErrConflict) {
		t.Fatalf("re-claim by holder: %v", err)
	}
}

func TestRenewAndExpiry(t *testing.T) {
	w := newItem()
	a := dev("a")
	cfg := Config{TTL: 10 * time.Minute}
	if _, err := Claim(w, a, t0, cfg); err != nil {
		t.Fatal(err)
	}
	l, err := Renew(w, a, t0.Add(8*time.Minute), cfg)
	if err != nil || !l.Expires.Equal(t0.Add(18*time.Minute)) || !l.ClaimedAt.Equal(t0) {
		t.Fatalf("renew %+v %v", l, err)
	}
	if _, err := Renew(w, dev("b"), t0.Add(9*time.Minute), cfg); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("foreign renew: %v", err)
	}
	// Past expiry the item reopens without counting an attempt, but it is
	// held for the holder's grace window (2 x TTL from the expiry).
	if !Expire(w, t0.Add(18*time.Minute), cfg) {
		t.Fatal("lapsed lease not expired")
	}
	if w.State != hubproto.StateOpen || w.Lease != nil || w.Attempts != 0 || w.Grace == nil || w.Grace.Device != "a" {
		t.Fatalf("after expiry: %+v", w)
	}
	if _, err := Claim(w, dev("b"), t0.Add(19*time.Minute), cfg); !errors.Is(err, ErrConflict) {
		t.Fatalf("claim inside another device's grace: %v", err)
	}
	// The holder's late renew takes it back as the same claim.
	l, err = Renew(w, a, t0.Add(30*time.Minute), cfg)
	if err != nil || w.State != hubproto.StateClaimed || !l.ClaimedAt.Equal(t0) || w.Grace != nil {
		t.Fatalf("renew inside grace: %+v %v %+v", l, err, w)
	}
	// Silent past expiry AND grace: the item is released, the holder lost it
	// and cools down on it.
	end := t0.Add(40*time.Minute + cfg.Grace())
	if _, err := Renew(w, a, end, cfg); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("renew past grace: %v", err)
	}
	if w.State != hubproto.StateOpen || w.Grace != nil || !w.CoolingFor("a", end) || w.Attempts != 0 {
		t.Fatalf("after grace: %+v", w)
	}
	if last := w.History[len(w.History)-1]; last.Action != hubproto.AttemptExpired || last.Device != "a" {
		t.Fatalf("history %+v", w.History)
	}
	if _, err := Claim(w, dev("b"), end, cfg); err != nil {
		t.Fatalf("claim after grace: %v", err)
	}
}

func TestExpireOnlyClaimed(t *testing.T) {
	w := newItem()
	if Expire(w, t0.Add(time.Hour), Config{}) {
		t.Fatal("open item expired")
	}
}

func TestComplete(t *testing.T) {
	w := newItem()
	a, b := dev("a"), dev("b")
	Claim(w, a, t0, Config{})
	out := store.Outcome{PRURL: "https://github.com/acme/web/pull/7", Delivery: hubproto.DeliveryCIPending}
	if err := Complete(w, b, out, t0, Config{}); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("foreign complete: %v", err)
	}
	if err := Complete(w, a, out, t0.Add(time.Minute), Config{}); err != nil {
		t.Fatal(err)
	}
	if w.State != hubproto.StateDone || w.Outcome.Device != "a" || w.Lease != nil || w.ClosedAt.IsZero() {
		t.Fatalf("after complete: %+v", w)
	}
	if err := Complete(w, a, out, t0.Add(2*time.Minute), Config{}); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("double complete: %v", err)
	}
}

func TestReleaseCountsNoAttempt(t *testing.T) {
	w := newItem()
	a := dev("a")
	Claim(w, a, t0, Config{})
	if err := Release(w, a, t0, Config{}); err != nil {
		t.Fatal(err)
	}
	if w.State != hubproto.StateOpen || w.Attempts != 0 {
		t.Fatalf("after release: %+v", w)
	}
}

func TestFailEscalatesAfterAttempts(t *testing.T) {
	w := newItem()
	a := dev("a")
	Claim(w, a, t0, Config{})
	esc, err := Fail(w, a, hubproto.FailCI, "", t0, Config{})
	if err != nil || esc || w.State != hubproto.StateOpen || w.Attempts != 1 {
		t.Fatalf("first fail: esc=%v err=%v %+v", esc, err, w)
	}
	Claim(w, a, t0, Config{})
	esc, err = Fail(w, a, hubproto.FailAgent, "", t0, Config{})
	if err != nil || !esc || w.State != hubproto.StateEscalated || w.Attempts != 2 {
		t.Fatalf("second fail: esc=%v err=%v %+v", esc, err, w)
	}
	if w.Escalation == nil || !w.Escalation.Pending || w.Escalation.Reason != ReasonAttempts {
		t.Fatalf("escalation %+v", w.Escalation)
	}
	if _, err := Claim(w, a, t0, Config{}); !errors.Is(err, ErrConflict) {
		t.Fatal("escalated item claimable")
	}
}

func TestFailLocalRefusalIsNotAnAttempt(t *testing.T) {
	for _, r := range []hubproto.FailReason{hubproto.FailRefused, hubproto.FailSandbox} {
		w := newItem()
		a := dev("a")
		Claim(w, a, t0, Config{EscalateAfter: 1})
		esc, err := Fail(w, a, r, "", t0, Config{EscalateAfter: 1})
		if err != nil || esc || w.State != hubproto.StateOpen || w.Attempts != 0 {
			t.Fatalf("%s: %+v", r, w)
		}
	}
}

func TestFailObsoleteClosesWithoutEscalation(t *testing.T) {
	w := newItem()
	a := dev("a")
	Claim(w, a, t0, Config{EscalateAfter: 1})
	esc, err := Fail(w, a, hubproto.FailObsolete, "", t0, Config{EscalateAfter: 1})
	if err != nil || esc || w.State != hubproto.StateFailed || w.Escalation != nil {
		t.Fatalf("%+v", w)
	}
}

func TestCriticalEscalatesImmediatelyButStaysOffered(t *testing.T) {
	w := NewItem("i1", "acme", adv("acme/web", hubproto.SeverityCritical), "a", t0)
	if w.State != hubproto.StateOpen || w.Escalation == nil || w.Escalation.Reason != ReasonCritical {
		t.Fatalf("%+v", w)
	}
	got := Offers(map[string]*store.WorkItem{"i1": w}, dev("a"), nil, 0, t0)
	if len(got) != 1 {
		t.Fatal("critical item not offered")
	}
	// Failing it later never opens a second escalation.
	a := dev("a")
	Claim(w, a, t0, Config{EscalateAfter: 1})
	esc, _ := Fail(w, a, hubproto.FailCI, "", t0, Config{EscalateAfter: 1})
	if esc || w.Escalation.Reason != ReasonCritical {
		t.Fatal("second escalation opened")
	}
}

func TestRevokedAndWrongOrgCanDoNothing(t *testing.T) {
	w := newItem()
	a := dev("a")
	Claim(w, a, t0, Config{})
	now := t0
	a.RevokedAt = &now
	if _, err := Renew(w, a, t0, Config{}); !errors.Is(err, ErrRevoked) {
		t.Fatalf("renew: %v", err)
	}
	if err := Complete(w, a, store.Outcome{}, t0, Config{}); !errors.Is(err, ErrRevoked) {
		t.Fatalf("complete: %v", err)
	}
	if err := Release(w, a, t0, Config{}); !errors.Is(err, ErrRevoked) {
		t.Fatalf("release: %v", err)
	}
	if _, err := Fail(w, a, hubproto.FailCI, "", t0, Config{}); !errors.Is(err, ErrRevoked) {
		t.Fatalf("fail: %v", err)
	}
	if !Revoke(w, "a") || w.State != hubproto.StateOpen {
		t.Fatal("revoke did not reopen")
	}
	if _, err := Claim(w, a, t0, Config{}); !errors.Is(err, ErrRevoked) {
		t.Fatalf("claim: %v", err)
	}
	other := &store.Device{ID: "z", Org: "beta"}
	if _, err := Claim(w, other, t0, Config{}); !errors.Is(err, ErrWrongOrg) {
		t.Fatalf("cross-org claim: %v", err)
	}
	if Offers(map[string]*store.WorkItem{"i1": w}, a, nil, 0, t0) != nil {
		t.Fatal("revoked device offered items")
	}
}

func TestObserveReopensOnlyAfterGrace(t *testing.T) {
	w := newItem()
	a := dev("a")
	Claim(w, a, t0, Config{})
	Fail(w, a, hubproto.FailCI, "", t0, Config{EscalateAfter: 5})
	Claim(w, a, t0, Config{})
	Complete(w, a, store.Outcome{PRURL: "https://github.com/acme/web/pull/1", Delivery: hubproto.DeliveryMerged}, t0, Config{})
	if Observe(w, "b", t0.Add(23*time.Hour)) || w.State != hubproto.StateDone {
		t.Fatal("reopened inside the grace")
	}
	if w.Reporters != 2 {
		t.Fatalf("reporters %d", w.Reporters)
	}
	if !Observe(w, "b", t0.Add(25*time.Hour)) {
		t.Fatal("not reopened after the grace")
	}
	if w.State != hubproto.StateOpen || w.Attempts != 0 || w.Outcome != nil || w.Reopened != 1 {
		t.Fatalf("after reopen %+v", w)
	}
	// An escalated item is a person's now; re-reports never reopen it.
	w.State = hubproto.StateEscalated
	if Observe(w, "b", t0.Add(100*time.Hour)) {
		t.Fatal("escalated item reopened")
	}
}

func TestOffersFilterAndOrder(t *testing.T) {
	items := map[string]*store.WorkItem{}
	add := func(id string, it hubproto.Item, age time.Duration) *store.WorkItem {
		w := NewItem(id, "acme", it, "a", t0.Add(-age))
		items[id] = w
		return w
	}
	add("low", adv("acme/web", hubproto.SeverityLow), 5*time.Hour)
	add("hiNew", adv("ACME/Web", hubproto.SeverityHigh), time.Hour)
	add("hiOld", adv("acme/web", hubproto.SeverityHigh), 2*time.Hour)
	add("bump", bump("acme/web", 3), 3*time.Hour)
	add("crit", adv("acme/web", hubproto.SeverityCritical), 0)
	add("otherRepo", adv("acme/api", hubproto.SeverityCritical), 0)
	x := add("otherOrg", adv("acme/web", hubproto.SeverityCritical), 0)
	x.Org = "beta"
	add("claimed", adv("acme/web", hubproto.SeverityCritical), 0).State = hubproto.StateClaimed

	got := Offers(items, dev("a"), nil, 0, t0)
	var ids []string
	for _, it := range got {
		ids = append(ids, it.ID)
	}
	want := []string{"crit", "hiOld", "hiNew", "bump", "low"}
	if len(ids) != len(want) {
		t.Fatalf("got %v want %v", ids, want)
	}
	for i := range want {
		if ids[i] != want[i] {
			t.Fatalf("got %v want %v", ids, want)
		}
	}
	got = Offers(items, dev("a"), map[hubproto.Kind]bool{hubproto.KindRedBumpPR: true}, 0, t0)
	if len(got) != 1 || got[0].ID != "bump" {
		t.Fatalf("kind filter: %+v", got)
	}
	if got := Offers(items, dev("a"), nil, 2, t0); len(got) != 2 {
		t.Fatalf("limit: %d", len(got))
	}
	for _, it := range Offers(items, dev("a"), nil, 0, t0) {
		if err := hubproto.Validate(it); err != nil {
			t.Fatalf("offered invalid item: %v", err)
		}
	}
}
