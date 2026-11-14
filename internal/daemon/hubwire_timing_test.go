package daemon

import (
	"context"
	"testing"
	"time"

	"github.com/sushidev-team/lola/hubproto"
	"github.com/sushidev-team/lola/internal/hub"
	"github.com/sushidev-team/lola/internal/scm"
	"github.com/sushidev-team/lola/internal/session"
	"github.com/sushidev-team/lola/internal/state"
)

// The loop's timing and the legacy-hub detection: the whole cycle keeps its
// own deadline, renewals back off after failures, an in-review item is kept
// alive, and a current hub's coded refusals never downgrade it.

// A renewal pass never pushes the whole cycle back: with poll_seconds at or
// past a third of the lease life the cycle still runs on its own deadline.
func TestHubNextWakeKeepsTheFullCycleDeadline(t *testing.T) {
	t0 := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	nextFull := t0.Add(600 * time.Second) // poll_seconds = 600, lease 15m
	now := t0
	renewals := 0
	for range 10 {
		renewIn := 300 * time.Second // a third of 15m after the last beat
		wait, renew := hubNextWake(now, nextFull, renewIn, true)
		now = now.Add(wait)
		if !renew {
			break
		}
		renewals++
	}
	if renewals != 1 || !now.Equal(nextFull) {
		t.Fatalf("renewals = %d, full cycle at %v, want 1 renewal then the cycle at %v", renewals, now, nextFull)
	}
	// Overdue: run the cycle at once.
	if wait, renew := hubNextWake(nextFull.Add(time.Minute), nextFull, time.Minute, true); wait != 0 || renew {
		t.Errorf("overdue cycle: wait %v renew %v", wait, renew)
	}
	// No lease: only the cycle.
	if wait, renew := hubNextWake(t0, nextFull, 0, false); wait != 600*time.Second || renew {
		t.Errorf("no lease: wait %v renew %v", wait, renew)
	}
}

// A failing heartbeat backs the renewal timer off from the last ATTEMPT
// instead of asking again every hubMinRenew; a success resets it.
func TestHubRenewalBacksOffAfterFailedHeartbeats(t *testing.T) {
	d, fh, _ := hubFixture(t)
	fh.offers = []hubproto.Item{advisoryItem("it1")}
	d.hubCycle(context.Background())
	fh.mu.Lock()
	fh.offers = nil
	fh.beatErr = &hub.APIError{Status: 503}
	fh.mu.Unlock()
	// The last success is long past: renewal is due at once.
	d.hub.mu.Lock()
	d.hub.lastBeat = time.Now().Add(-time.Hour)
	d.hub.mu.Unlock()
	if r, _ := d.hubRenewIn(); r != hubMinRenew {
		t.Fatalf("due renewal in %v, want %v", r, hubMinRenew)
	}
	var waits []time.Duration
	for range 3 {
		d.hubRenewCycle(context.Background())
		r, ok := d.hubRenewIn()
		if !ok {
			t.Fatal("lease dropped")
		}
		waits = append(waits, r)
	}
	for i, want := range []time.Duration{30 * time.Second, time.Minute, 2 * time.Minute} {
		if waits[i] < want-2*time.Second || waits[i] > want {
			t.Fatalf("waits = %v, want ~30s, 1m, 2m", waits)
		}
	}
	// Capped at a third of the lease life.
	if b := hubRenewBackoff(20, 15*time.Minute); b != 5*time.Minute {
		t.Errorf("cap = %v", b)
	}
	fh.mu.Lock()
	fh.beatErr = nil
	fh.mu.Unlock()
	d.hubRenewCycle(context.Background())
	if r, _ := d.hubRenewIn(); r < 9*time.Minute {
		t.Errorf("after a success renew in %v, want a third of the lease life", r)
	}
}

// A current hub's 400 bad_request to a heartbeat (too many leases) is not an
// old hub: the retry without policy_version fails too, so nothing downgrades.
func TestHubHeartbeat400WithoutTheFieldIsNotLegacy(t *testing.T) {
	d, fh, _ := hubFixture(t)
	d.teamPolicy.version, d.teamPolicy.applied = 4, true
	fh.beatErr = &hub.APIError{Status: 400, Code: hubproto.CodeBadRequest}
	d.hubCycle(context.Background())
	if b := fh.snapshot().beats; len(b) != 2 || b[1].PolicyVersion != 0 {
		t.Fatalf("beats = %+v, want one retry without policy_version", b)
	}
	if d.hubStatusData().LegacyHub {
		t.Fatal("a refusal the retry shares must not mark the hub legacy")
	}
}

// A current hub answers 404 not_found to a progress for an item it no
// longer knows; the fallback complete is a 404 too, so the hub stays current.
func TestHubProgress404ForAnUnknownItemIsNotLegacy(t *testing.T) {
	d, fh, _ := hubFixture(t)
	nf := &hub.APIError{Status: 404, Code: hubproto.CodeNotFound}
	fh.progErr, fh.outErr = nf, nf
	d.hubEnqueue(hubOutcome{ItemID: "it9", Action: hubActProgress, PRURL: "https://github.com/acme/widgets/pull/7",
		Delivery: string(hubproto.DeliveryCIPending), Since: time.Now()})
	d.hubOutcomeCycle(context.Background())
	got := fh.snapshot()
	if len(got.progresses) != 1 || len(got.completes) != 1 {
		t.Fatalf("progresses = %d completes = %d", len(got.progresses), len(got.completes))
	}
	if d.hubStatusData().LegacyHub {
		t.Fatal("a purged item must not mark the hub legacy")
	}
}

// A refused pr_url proves nothing (a current hub refuses one that is not the
// item's PR): the reason alone goes out and the hub stays current. A refused
// group alone does prove an old hub.
func TestHubFailRefusalMarksLegacyOnlyForAGroup(t *testing.T) {
	d, fh, _ := hubFixture(t)
	fh.failErr = &hub.APIError{Status: 400, Code: hubproto.CodeBadRequest}
	d.hubEnqueue(hubOutcome{ItemID: "it8", Action: hubActFail, Reason: string(hubproto.FailPRClosed),
		PRURL: "https://github.com/acme/other/pull/7", Since: time.Now()})
	d.hubOutcomeCycle(context.Background())
	if reqs := fh.snapshot().failReqs; len(reqs) != 2 || reqs[1].PRURL != "" {
		t.Fatalf("fail requests = %+v", reqs)
	}
	if d.hubStatusData().LegacyHub {
		t.Fatal("a refused pr_url must not mark the hub legacy")
	}
	d.hubEnqueue(hubOutcome{ItemID: "it9", Action: hubActFail, Reason: string(hubproto.FailAgent),
		Group: "abcdef0123456789", Since: time.Now()})
	d.hubOutcomeCycle(context.Background())
	if !d.hubStatusData().LegacyHub {
		t.Fatal("a refused group is an old hub")
	}
}

// An in-review item whose delivery does not change is reported again every
// hubProgressRefresh, so the hub does not reopen it as abandoned.
func TestHubInReviewProgressIsRefreshed(t *testing.T) {
	t0 := time.Now()
	s := session.Session{ID: "x", Repo: "acme/widgets", PR: &scm.PR{Number: 7, URL: "https://github.com/acme/widgets/pull/7", State: "OPEN"}}
	s.EnsureAxes()
	s.SetDelivery(state.DeliveryReviewPending, t0)
	w := session.HubWork{ItemID: "it1", Kind: string(hubproto.KindDepAdvisory), Progress: "review_pending", InReview: true, ProgressAt: t0}
	if _, ok := hubProgressFor(s, w, false, t0.Add(hubProgressRefresh-time.Minute)); ok {
		t.Fatal("refreshed before it was due")
	}
	o, ok := hubProgressFor(s, w, false, t0.Add(hubProgressRefresh))
	if !ok || o.Action != hubActProgress || o.Delivery != "review_pending" || !o.NoLease {
		t.Fatalf("refresh = %+v %v", o, ok)
	}
	if hubProgressRefresh >= hubproto.InReviewIdleReopen/2 {
		t.Errorf("refresh %v is too close to the hub's %v", hubProgressRefresh, hubproto.InReviewIdleReopen)
	}
	// Not yet acknowledged (no review yet): no refresh, the first one is queued.
	w.InReview = false
	if _, ok := hubProgressFor(s, w, false, t0.Add(30*24*time.Hour)); ok {
		t.Error("refreshed before the hub acknowledged review")
	}
	// A work from before ProgressAt existed is refreshed once now.
	w.InReview, w.ProgressAt = true, time.Time{}
	if _, ok := hubProgressFor(s, w, false, t0); !ok {
		t.Error("a pre-ProgressAt in-review work must refresh")
	}
}

// End to end: the refresh is queued once per period, stamped at queue time.
func TestHubInReviewRefreshQueuedOncePerPeriod(t *testing.T) {
	d, fh, _ := hubFixture(t)
	fh.offers = []hubproto.Item{advisoryItem("it1")}
	d.hubCycle(context.Background())
	fh.mu.Lock()
	fh.offers = nil
	fh.mu.Unlock()
	id := hubSessions(d)[0].ID
	setHubPR(d, id, hubOpenPR(7), state.DeliveryReviewPending)
	d.hubOutcomeCycle(context.Background())
	d.hubOutcomeCycle(context.Background())
	if n := len(fh.snapshot().progresses); n != 1 {
		t.Fatalf("progresses = %d, want 1", n)
	}
	if s, _ := d.sessions.Get(id); !s.HubItem.InReview || s.HubItem.ProgressAt.IsZero() {
		t.Fatalf("work = %+v", s.HubItem)
	}
	later := time.Now().Add(hubProgressRefresh + time.Hour)
	d.hub.now = func() time.Time { return later }
	d.hubOutcomeCycle(context.Background())
	d.hubOutcomeCycle(context.Background())
	if n := len(fh.snapshot().progresses); n != 2 {
		t.Fatalf("progresses = %d, want one refresh", n)
	}
}
