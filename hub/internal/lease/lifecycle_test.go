package lease

import (
	"errors"
	"testing"
	"time"

	"github.com/sushidev-team/lola/hub/internal/store"
	"github.com/sushidev-team/lola/hubproto"
)

var pr7 = store.Outcome{PRURL: "https://github.com/acme/web/pull/7", Delivery: hubproto.DeliveryCIPending}

func offered(w *store.WorkItem, d *store.Device, now time.Time) bool {
	return len(Offers(map[string]*store.WorkItem{w.ID: w}, d, nil, 0, now)) == 1
}

func lastAction(t *testing.T, w *store.WorkItem) store.Attempt {
	t.Helper()
	if len(w.History) == 0 {
		t.Fatal("no history")
	}
	return w.History[len(w.History)-1]
}

func TestProgressMovesToInReviewAndOwnerFinishes(t *testing.T) {
	w := newItem()
	a, b := dev("a"), dev("b")
	a.Name, a.Member = "laptop", "Ada"
	if err := Progress(w, a, pr7, t0, Config{}); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("progress without a lease: %v", err)
	}
	Claim(w, a, t0, Config{})
	if err := Progress(w, b, pr7, t0, Config{}); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("foreign progress: %v", err)
	}
	if err := Progress(w, a, pr7, t0.Add(time.Minute), Config{}); err != nil {
		t.Fatal(err)
	}
	if w.State != hubproto.StateInReview || w.Lease != nil || w.Owner != "a" || w.Outcome == nil || !w.ClaimedAt.Equal(t0) {
		t.Fatalf("after progress: %+v", w)
	}
	if h := lastAction(t, w); h.Action != hubproto.AttemptInReview || h.PRURL != pr7.PRURL || h.DeviceName != "laptop" || h.Member != "Ada" {
		t.Fatalf("history %+v", h)
	}
	// Not offered, not claimable, and a re-report never reopens it.
	if offered(w, b, t0.Add(time.Hour)) {
		t.Fatal("in_review item offered")
	}
	if _, err := Claim(w, b, t0.Add(time.Hour), Config{}); !errors.Is(err, ErrConflict) {
		t.Fatalf("claim of in_review: %v", err)
	}
	if Observe(w, "b", t0.Add(30*24*time.Hour)) || w.State != hubproto.StateInReview {
		t.Fatal("re-report reopened an in_review item")
	}
	// The owner updates the delivery (no new history line for the same PR).
	n := len(w.History)
	upd := pr7
	upd.Delivery = hubproto.DeliveryReviewPending
	if err := Progress(w, a, upd, t0.Add(2*time.Hour), Config{}); err != nil || w.Outcome.Delivery != hubproto.DeliveryReviewPending || len(w.History) != n {
		t.Fatalf("owner update: %v %+v", err, w)
	}
	if err := Complete(w, b, pr7, t0.Add(3*time.Hour), Config{}); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("foreign complete of in_review: %v", err)
	}
	if err := Complete(w, a, pr7, t0.Add(3*time.Hour), Config{}); err != nil {
		t.Fatal(err)
	}
	if w.State != hubproto.StateDone || w.Owner != "a" || !w.ClaimedAt.IsZero() {
		t.Fatalf("after complete: %+v", w)
	}
}

func TestOwnerFailsFromInReview(t *testing.T) {
	w := newItem()
	a := dev("a")
	Claim(w, a, t0, Config{})
	Progress(w, a, pr7, t0, Config{})
	esc, err := Fail(w, a, hubproto.FailCI, pr7.PRURL, t0.Add(time.Hour), Config{EscalateAfter: 1})
	if err != nil || !esc || w.State != hubproto.StateEscalated || w.Owner != "a" {
		t.Fatalf("fail from in_review: %v %v %+v", esc, err, w)
	}
	if h := lastAction(t, w); h.Action != hubproto.AttemptFailed || h.Reason != hubproto.FailCI || h.PRURL != pr7.PRURL {
		t.Fatalf("history %+v", h)
	}
	// Below escalate_after the item reopens and loses its owner.
	w2 := newItem()
	Claim(w2, a, t0, Config{})
	Progress(w2, a, pr7, t0, Config{})
	if _, err := Fail(w2, a, hubproto.FailPRClosed, "", t0, Config{EscalateAfter: 3}); err != nil || w2.State != hubproto.StateOpen || w2.Owner != "" {
		t.Fatalf("reopen from in_review: %v %+v", err, w2)
	}
}

func TestInReviewIdleReopens(t *testing.T) {
	w := newItem()
	a := dev("a")
	Claim(w, a, t0, Config{})
	Progress(w, a, pr7, t0, Config{})
	if ReopenIdle(w, t0.Add(hubproto.InReviewIdleReopen-time.Minute)) {
		t.Fatal("reopened before the idle window")
	}
	if !ReopenIdle(w, t0.Add(hubproto.InReviewIdleReopen)) {
		t.Fatal("idle in_review not reopened")
	}
	if w.State != hubproto.StateOpen || w.Owner != "" || w.Outcome != nil || w.Reopened != 1 || lastAction(t, w).Action != hubproto.AttemptReopened {
		t.Fatalf("after idle reopen: %+v", w)
	}
}

func TestGraceHoldsForHolderOnly(t *testing.T) {
	w := newItem()
	a, b := dev("a"), dev("b")
	cfg := Config{TTL: 10 * time.Minute}
	Claim(w, a, t0, cfg)
	at := t0.Add(11 * time.Minute)
	if lapsed, _ := Tick(w, at, cfg); !lapsed {
		t.Fatal("lease not lapsed")
	}
	if offered(w, b, at) {
		t.Fatal("held item offered to another device")
	}
	if !offered(w, a, at) {
		t.Fatal("held item not offered to its holder")
	}
	n := len(w.History)
	l, err := Claim(w, a, at, cfg)
	if err != nil || !l.ClaimedAt.Equal(t0) || len(w.History) != n {
		t.Fatalf("take-back: %+v %v history=%d", l, err, len(w.History))
	}
	// The holder can also finish straight out of its grace window.
	Tick(w, t0.Add(30*time.Minute), cfg)
	if err := Complete(w, a, pr7, t0.Add(30*time.Minute), cfg); err != nil {
		t.Fatalf("complete inside grace: %v", err)
	}
}

func TestMaxLeaseAgeTimesOut(t *testing.T) {
	w := newItem()
	a := dev("a")
	cfg := Config{TTL: 10 * time.Minute, MaxLeaseAge: time.Hour, EscalateAfter: 3}
	Claim(w, a, t0, cfg)
	at := t0
	for i := 0; i < 5; i++ {
		at = at.Add(9 * time.Minute)
		if _, err := Renew(w, a, at, cfg); err != nil {
			t.Fatalf("renew %d: %v", i, err)
		}
	}
	if _, err := Renew(w, a, t0.Add(time.Hour), cfg); !errors.Is(err, ErrMaxAge) {
		t.Fatalf("renew past max age: %v", err)
	}
	if w.State != hubproto.StateOpen || w.Attempts != 1 || w.LastFail != hubproto.FailTimeout || !w.CoolingFor("a", t0.Add(time.Hour)) {
		t.Fatalf("after timeout: %+v", w)
	}
	if h := lastAction(t, w); h.Action != hubproto.AttemptExpired || h.Reason != hubproto.FailTimeout {
		t.Fatalf("history %+v", h)
	}
	// A take-back from grace cannot dodge the bound either.
	w2 := newItem()
	Claim(w2, a, t0, cfg)
	for at := t0.Add(9 * time.Minute); at.Before(t0.Add(time.Hour)); at = at.Add(9 * time.Minute) {
		Renew(w2, a, at, cfg) // the last one, at 54m, holds the lease until 64m
	}
	if _, err := Claim(w2, a, t0.Add(70*time.Minute), cfg); !errors.Is(err, ErrMaxAge) || w2.Attempts != 1 {
		t.Fatalf("take-back past max age: %v %+v", err, w2)
	}
}

func TestCooldownAfterLettingGo(t *testing.T) {
	cases := map[string]func(w *store.WorkItem, d *store.Device){
		"fail": func(w *store.WorkItem, d *store.Device) {
			Fail(w, d, hubproto.FailCI, "", t0, Config{EscalateAfter: 5})
		},
		"refused": func(w *store.WorkItem, d *store.Device) { Fail(w, d, hubproto.FailRefused, "", t0, Config{}) },
		"sandbox": func(w *store.WorkItem, d *store.Device) { Fail(w, d, hubproto.FailSandbox, "", t0, Config{}) },
		"release": func(w *store.WorkItem, d *store.Device) { Release(w, d, t0, Config{}) },
	}
	for name, letGo := range cases {
		w := newItem()
		a, b := dev("a"), dev("b")
		Claim(w, a, t0, Config{})
		letGo(w, a)
		if w.State != hubproto.StateOpen {
			t.Fatalf("%s: state %s", name, w.State)
		}
		if offered(w, a, t0.Add(hubproto.DeviceCooldown-time.Minute)) {
			t.Fatalf("%s: offered back inside the cooldown", name)
		}
		if !offered(w, b, t0) {
			t.Fatalf("%s: not offered to another device", name)
		}
		if !offered(w, a, t0.Add(hubproto.DeviceCooldown)) {
			t.Fatalf("%s: not offered after the cooldown", name)
		}
	}
}

func TestStaleClose(t *testing.T) {
	w := newItem()
	if Stale(w, t0.Add(47*time.Hour), 48*time.Hour) {
		t.Fatal("stale too early")
	}
	if !Stale(w, t0.Add(48*time.Hour), 48*time.Hour) || !CloseStale(w, t0.Add(48*time.Hour)) {
		t.Fatal("not closed stale")
	}
	h := lastAction(t, w)
	if w.State != hubproto.StateFailed || w.ClosedAt.IsZero() || w.Attempts != 0 || h.Action != hubproto.AttemptClosedStale || h.Device != "" {
		t.Fatalf("after stale close: %+v %+v", w, h)
	}
	// A claimed item is somebody's work: never stale.
	w2 := newItem()
	Claim(w2, dev("a"), t0, Config{})
	if Stale(w2, t0.Add(100*time.Hour), time.Hour) || CloseStale(w2, t0) {
		t.Fatal("claimed item closed stale")
	}
}

func TestResolveAndReopenEscalated(t *testing.T) {
	w := newItem()
	a := dev("a")
	if Resolve(w, t0) == nil || Reopen(w, t0) == nil {
		t.Fatal("open item resolved or reopened")
	}
	Claim(w, a, t0, Config{EscalateAfter: 1})
	Fail(w, a, hubproto.FailCI, "", t0, Config{EscalateAfter: 1})
	if err := Reopen(w, t0.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if w.State != hubproto.StateOpen || w.Attempts != 0 || w.Escalation == nil || !offered(w, a, t0.Add(time.Hour)) {
		t.Fatalf("after reopen: %+v", w)
	}
	Claim(w, a, t0.Add(time.Hour), Config{EscalateAfter: 1})
	Fail(w, a, hubproto.FailCI, "", t0.Add(time.Hour), Config{EscalateAfter: 1})
	if err := Resolve(w, t0.Add(2*time.Hour)); err != nil || w.State != hubproto.StateDone || w.ClosedAt.IsZero() {
		t.Fatalf("resolve: %v %+v", err, w)
	}
}

func TestRevokeLetsGoOfGraceAndReview(t *testing.T) {
	cfg := Config{TTL: 10 * time.Minute}
	w := newItem()
	Claim(w, dev("a"), t0, cfg)
	Tick(w, t0.Add(11*time.Minute), cfg)
	if !Revoke(w, "a") || w.Grace != nil || !offered(w, dev("b"), t0.Add(12*time.Minute)) {
		t.Fatalf("grace kept for a revoked device: %+v", w)
	}
	w2 := newItem()
	Claim(w2, dev("a"), t0, cfg)
	Progress(w2, dev("a"), pr7, t0, cfg)
	if !Revoke(w2, "a") || w2.State != hubproto.StateOpen || w2.Owner != "" {
		t.Fatalf("in_review kept for a revoked owner: %+v", w2)
	}
}

func TestHistoryIsCapped(t *testing.T) {
	w := newItem()
	a := dev("a")
	at := t0
	for i := 0; i < hubproto.MaxItemHistory+5; i++ {
		Claim(w, a, at, Config{})
		Release(w, a, at, Config{})
		at = at.Add(time.Minute)
	}
	if len(w.History) != hubproto.MaxItemHistory || lastAction(t, w).Action != hubproto.AttemptReleased {
		t.Fatalf("history len %d", len(w.History))
	}
	for i := 1; i < len(w.History); i++ {
		if w.History[i].At.Before(w.History[i-1].At) {
			t.Fatal("history not newest last")
		}
	}
}

func TestReEscalationAfterANewEpisodeIsAnnouncedAgain(t *testing.T) {
	cfg := Config{EscalateAfter: 1}
	a := dev("a")
	fail := func(w *store.WorkItem, at time.Time) bool {
		t.Helper()
		if _, err := Claim(w, a, at, cfg); err != nil {
			t.Fatal(err)
		}
		esc, err := Fail(w, a, hubproto.FailCI, "", at, cfg)
		if err != nil {
			t.Fatal(err)
		}
		return esc
	}
	// Escalated and sent: the item has its issue.
	w := newItem()
	if !fail(w, t0) {
		t.Fatal("first escalation not opened")
	}
	w.Escalation.Pending, w.Escalation.LinearID, w.Escalation.SentAt = false, "ENG-1", t0
	// Same episode: a critical re-mark (or any second open) changes nothing.
	if OpenEscalation(w, ReasonCritical, t0) || w.Escalation.Pending {
		t.Fatal("a sent escalation re-armed within its episode")
	}
	// `items reopen`, then it fails again: announced again, fresh.
	if err := Reopen(w, t0.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	at := t0.Add(7 * time.Hour) // past the device cooldown
	if !fail(w, at) {
		t.Fatal("re-escalation after `items reopen` opened nothing")
	}
	x := w.Escalation
	if w.State != hubproto.StateEscalated || !x.Pending || x.Tries != 0 || !x.CreatedAt.Equal(at) || x.Episode != w.Reopened {
		t.Fatalf("re-armed escalation %+v", x)
	}
	// A pending one is never re-armed, whatever the episode.
	w.Reopened++
	if OpenEscalation(w, ReasonAttempts, at.Add(time.Hour)) || !x.CreatedAt.Equal(at) {
		t.Fatal("a pending escalation was replaced")
	}
	// An issue the hub already closed is announced again too.
	w2 := newItem()
	fail(w2, t0)
	w2.Escalation.Pending, w2.Escalation.LinearID, w2.Escalation.Err = false, "ENG-2", store.EscalationClosed
	if !OpenEscalation(w2, ReasonAttempts, t0.Add(time.Hour)) || w2.Escalation.Err != "" {
		t.Fatalf("closed issue not re-armed: %+v", w2.Escalation)
	}
}
