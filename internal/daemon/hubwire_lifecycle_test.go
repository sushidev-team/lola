package daemon

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/sushidev-team/lola/hubproto"
	"github.com/sushidev-team/lola/internal/hub"
	"github.com/sushidev-team/lola/internal/notify"
	"github.com/sushidev-team/lola/internal/scm"
	"github.com/sushidev-team/lola/internal/session"
	"github.com/sushidev-team/lola/internal/state"
)

// The chore lifecycle on the daemon side: in_review progress, grace
// re-claims, outcome recovery, the renewal timer, and status / notes.

// hubNotes records notifications.
type hubNotes struct {
	mu    sync.Mutex
	notes []notify.Note
}

func (n *hubNotes) Notify(_ context.Context, note notify.Note) {
	n.mu.Lock()
	n.notes = append(n.notes, note)
	n.mu.Unlock()
}

func (n *hubNotes) titles() []string {
	n.mu.Lock()
	defer n.mu.Unlock()
	var out []string
	for _, x := range n.notes {
		out = append(out, x.Title)
	}
	return out
}

func (n *hubNotes) has(title string) bool { return slices.Contains(n.titles(), title) }

func setHubPR(d *Daemon, id string, pr *scm.PR, del state.DeliveryState) {
	d.sessions.Update(id, func(s *session.Session) bool {
		s.PR = pr
		s.SetDelivery(del, time.Now())
		return true
	})
}

func hubOpenPR(n int) *scm.PR {
	return &scm.PR{Number: n, URL: "https://github.com/acme/widgets/pull/7", State: "OPEN"}
}

// C12: a hub that answers 404 on /progress gets today's complete-on-PR, and
// the guard is stamped so the item is not reported twice.
func TestHubProgressFallsBackToCompleteOnOldHub(t *testing.T) {
	d, fh, _ := hubFixture(t)
	fh.progErr = &hub.APIError{Status: 404, Code: hubproto.CodeNotFound}
	fh.offers = []hubproto.Item{advisoryItem("it1")}
	d.hubCycle(context.Background())
	id := hubSessions(d)[0].ID
	setHubPR(d, id, hubOpenPR(7), state.DeliveryCIPending)
	d.hubOutcomeCycle(context.Background())
	d.hubOutcomeCycle(context.Background())
	got := fh.snapshot()
	if len(got.progresses) != 1 || len(got.completes) != 1 {
		t.Fatalf("progresses = %d completes = %d, want 1 / 1", len(got.progresses), len(got.completes))
	}
	if c := got.completes[0]; c.Delivery != hubproto.DeliveryCIPending || c.PRURL != "https://github.com/acme/widgets/pull/7" {
		t.Errorf("complete = %+v", c)
	}
	if s, _ := d.sessions.Get(id); s.HubItem.Reported != "complete" {
		t.Errorf("guard = %q", s.HubItem.Reported)
	}
	if !d.hubStatusData().LegacyHub {
		t.Error("status must say the hub predates the lifecycle")
	}
	if n := d.hubStatusData().PendingOutcomes; n != 0 {
		t.Errorf("pending = %d", n)
	}
}

// A closed PR fails a dep_advisory with the PR named; a red bump PR closed
// while the agent never moved its head is not_reproducible.
func TestHubClosedPROutcomes(t *testing.T) {
	adv := session.Session{ID: "x", Repo: "acme/widgets", PR: &scm.PR{Number: 7, URL: "https://github.com/acme/widgets/pull/7", State: "CLOSED"},
		HubItem: session.HubWork{ItemID: "it1", Kind: string(hubproto.KindDepAdvisory)}}
	adv.EnsureAxes()
	adv.SetDelivery(state.DeliveryClosed, time.Now())
	if o, ok := hubOutcomeFor(adv, adv.HubItem, false, false); !ok || o.Reason != string(hubproto.FailPRClosed) || o.PRURL == "" {
		t.Errorf("advisory closed = %+v", o)
	}
	for _, c := range []struct {
		head string
		want hubproto.FailReason
	}{{"aaa", hubproto.FailObsolete}, {"bbb", hubproto.FailPRClosed}} {
		s := session.Session{ID: "x", Repo: "acme/widgets",
			PR:      &scm.PR{Number: 42, URL: "https://github.com/acme/widgets/pull/42", State: "CLOSED", HeadSHA: c.head},
			HubItem: session.HubWork{ItemID: "it2", Kind: string(hubproto.KindRedBumpPR), StartHead: "aaa"}}
		s.EnsureAxes()
		s.SetDelivery(state.DeliveryClosed, time.Now())
		if o, ok := hubOutcomeFor(s, s.HubItem, false, false); !ok || o.Reason != string(c.want) {
			t.Errorf("head %s: outcome = %+v, want %s", c.head, o, c.want)
		}
	}
}

// A lost lease is claimed back (the hub's grace for the previous holder) and
// the session keeps reporting; the daily count is not spent again.
func TestHubLostLeaseIsClaimedBack(t *testing.T) {
	d, fh, _ := hubFixture(t)
	fh.offers = []hubproto.Item{advisoryItem("it1")}
	d.hubCycle(context.Background())
	fh.mu.Lock()
	fh.offers, fh.lost = nil, []string{"it1"}
	fh.mu.Unlock()
	d.hubCycle(context.Background())
	s := hubSessions(d)[0]
	if s.HubItem.LeaseLost || s.HubItem.LeaseTaken {
		t.Fatalf("a re-claimed lease must stay held: %+v", s.HubItem)
	}
	if got := fh.snapshot().claims; !slices.Equal(got, []string{"it1", "it1"}) {
		t.Errorf("claims = %v", got)
	}
	if n := d.hubClaimsToday(time.Now()); n != 1 {
		t.Errorf("claims today = %d, a grace re-claim must not count", n)
	}
	// A hub that no longer knows the item (404 with a code): lost, not taken.
	fh.mu.Lock()
	fh.claimErr = &hub.APIError{Status: 404, Code: hubproto.CodeNotFound}
	fh.mu.Unlock()
	d.hubCycle(context.Background())
	if s := hubSessions(d)[0]; !s.HubItem.LeaseLost || s.HubItem.LeaseTaken {
		t.Errorf("gone item: %+v", s.HubItem)
	}
}

// 409 lease_lost on a queued outcome claims the item back and sends once more.
func TestHubLeaseLostOutcomeReclaimsAndResends(t *testing.T) {
	d, fh, _ := hubFixture(t)
	fh.offers = []hubproto.Item{advisoryItem("it1")}
	d.hubCycle(context.Background())
	fh.mu.Lock()
	fh.offers = nil
	fh.outErrSeq = []error{&hub.APIError{Status: 409, Code: hubproto.CodeLeaseLost}, nil}
	fh.mu.Unlock()
	id := hubSessions(d)[0].ID
	setHubPR(d, id, &scm.PR{Number: 7, URL: "https://github.com/acme/widgets/pull/7", State: "MERGED"}, state.DeliveryMerged)
	d.hubOutcomeCycle(context.Background())
	got := fh.snapshot()
	if len(got.completes) != 2 || len(got.claims) != 2 {
		t.Fatalf("completes = %d claims = %d, want the complete resent after one re-claim", len(got.completes), len(got.claims))
	}
	if n := d.hubStatusData().PendingOutcomes; n != 0 {
		t.Errorf("pending = %d", n)
	}
}

// A proxy's bare 4xx (no hubproto code) is retried, spaced out, not dropped.
func TestHubProxyRefusalRetriesWithBackoff(t *testing.T) {
	d, fh, _ := hubFixture(t)
	fh.outErr = &hub.APIError{Status: 403}
	if hubTerminal(fh.outErr) || hubTerminal(&hub.APIError{Status: 404}) {
		t.Fatal("a bare proxy 4xx must not be final")
	}
	d.hubEnqueue(hubOutcome{ItemID: "it9", Action: hubActFail, Reason: string(hubproto.FailAgent), Since: time.Now()})
	d.hubOutcomeCycle(context.Background())
	d.hubOutcomeCycle(context.Background())
	if n := len(fh.snapshot().fails); n != 1 {
		t.Fatalf("fails sent = %d, want 1 before the backoff passed", n)
	}
	if n := d.hubStatusData().PendingOutcomes; n != 1 {
		t.Fatalf("pending = %d, want it kept", n)
	}
	later := time.Now().Add(2 * time.Minute)
	d.hub.now = func() time.Time { return later }
	fh.mu.Lock()
	fh.outErr = nil
	fh.mu.Unlock()
	d.hubOutcomeCycle(context.Background())
	if n := len(fh.snapshot().fails); n != 2 {
		t.Fatalf("fails sent = %d, want the retry after the backoff", n)
	}
	if n := d.hubStatusData().PendingOutcomes; n != 0 {
		t.Errorf("pending = %d", n)
	}
}

// Renewal runs on its own timer at most a third of the lease's life after
// the last heartbeat, however long poll_seconds is, and does not claim.
func TestHubRenewalTimerIgnoresPollSeconds(t *testing.T) {
	d, fh, _ := hubFixture(t)
	if _, ok := d.hubRenewIn(); ok {
		t.Fatal("no lease: no renewal timer")
	}
	poll := 3600
	d.cfg.Hub.PollSeconds = poll
	fh.offers = []hubproto.Item{advisoryItem("it1")}
	d.hubCycle(context.Background()) // lease of 30 minutes
	r, ok := d.hubRenewIn()
	if !ok || r > 10*time.Minute+time.Second || r >= d.hubInterval() {
		t.Fatalf("renew in %v (ok=%v), want <= TTL/3 and before the %v poll", r, ok, d.hubInterval())
	}
	before := fh.snapshot()
	d.hubRenewCycle(context.Background())
	got := fh.snapshot()
	if len(got.beats) != len(before.beats)+1 || got.offerCalls != before.offerCalls {
		t.Fatalf("renew pass: beats %d -> %d, offers %d -> %d", len(before.beats), len(got.beats), before.offerCalls, got.offerCalls)
	}
	if lv := got.beats[len(got.beats)-1].LiveLeases; !slices.Equal(lv, []string{"it1"}) {
		t.Errorf("renewed = %v", lv)
	}
}

// A sandboxed project without a login claims nothing, single or grouped,
// with a stable reason.
func TestHubClaimNeedsASandboxLogin(t *testing.T) {
	for _, offers := range [][]hubproto.Item{
		{advisoryItem("it1")},
		{advisoryFor("it1", "lodash", "GHSA-jf85-cpcp-j695"), advisoryFor("it2", "vite", "GHSA-587h-7wmj-h5pm")},
	} {
		d, fh, _ := hubFixture(t)
		nat := &reloginNative{missing: map[string]bool{}}
		d.native = nat
		fh.offers = offers
		d.hubCycle(context.Background())
		if got := fh.snapshot(); len(got.claims) != 0 || len(nat.manualAgents) != 0 {
			t.Fatalf("claimed without a login: %v", got.claims)
		}
		st := d.hubStatusData()
		if len(st.OfferSkips) != len(offers) || st.OfferSkips[0].Reason != hub.ReasonNoSandboxLogin {
			t.Errorf("skips = %+v", st.OfferSkips)
		}
		if n := d.hubClaimsToday(time.Now()); n != 0 {
			t.Errorf("claims today = %d", n)
		}
	}
}

// A dead agent is reported agent_gave_up when the loop sees it, not at
// teardown; the session is not stopped, and a person is told.
func TestHubDeadAgentFailsWithoutTeardown(t *testing.T) {
	d, fh, nat := hubFixture(t)
	notes := &hubNotes{}
	d.notifier = notes
	fh.offers = []hubproto.Item{advisoryItem("it1")}
	d.hubCycle(context.Background())
	if !notes.has("Hub chore claimed") {
		t.Errorf("an automatic claim must be told: %v", notes.titles())
	}
	fh.mu.Lock()
	fh.offers = nil
	fh.mu.Unlock()
	id := hubSessions(d)[0].ID
	d.sessions.Update(id, func(s *session.Session) bool {
		s.SetAgentState(state.AgentDead, "", time.Now())
		return true
	})
	d.hubRenewCycle(context.Background())
	d.hubRenewCycle(context.Background())
	if got := fh.snapshot().fails; !slices.Equal(got, []hubproto.FailReason{hubproto.FailAgent}) {
		t.Fatalf("fails = %v", got)
	}
	if _, ok := d.sessions.Get(id); !ok || len(nat.killCalls()) != 0 {
		t.Fatal("the hub loop must never stop a session")
	}
	if !notes.has("Hub chore failed") {
		t.Errorf("notes = %v", notes.titles())
	}
	if !d.hubCoolingDown("it1", time.Now()) {
		t.Error("a failed item cools down here")
	}
}

// A claim older than the max lease age reports timeout once and is no longer
// renewed; the session keeps running.
func TestHubChoreAgeLimitReportsTimeoutOnce(t *testing.T) {
	d, fh, nat := hubFixture(t)
	fh.offers = []hubproto.Item{advisoryItem("it1")}
	d.hubCycle(context.Background())
	fh.mu.Lock()
	fh.offers = nil
	fh.mu.Unlock()
	later := time.Now().Add(hubproto.DefaultMaxLeaseAge + time.Minute)
	d.hub.now = func() time.Time { return later }
	d.hubCycle(context.Background())
	d.hubCycle(context.Background())
	got := fh.snapshot()
	if !slices.Equal(got.fails, []hubproto.FailReason{hubproto.FailTimeout}) {
		t.Fatalf("fails = %v", got.fails)
	}
	if lv := got.beats[len(got.beats)-1].LiveLeases; len(lv) != 0 {
		t.Errorf("a timed-out claim is still renewed: %v", lv)
	}
	if len(hubSessions(d)) != 1 || len(nat.killCalls()) != 0 {
		t.Error("the session must keep running")
	}
}

// GitHub is asked only after the slot is reserved: a capped machine spends
// no gh call on an offer.
func TestHubVerifyRunsAfterTheSlotGate(t *testing.T) {
	d, fh, _ := hubFixture(t)
	d.cfg.Defaults.GlobalCap = 1
	d.sessions.Upsert(session.Session{ID: "lola-web-x", Source: "native", Project: "web", AgentState: state.AgentWorking, Delivery: state.DeliveryNone})
	verified := 0
	d.hub.verify = func(_ context.Context, it hubproto.Item) (hub.Verified, error) {
		verified++
		return hub.Verified{Item: it}, nil
	}
	fh.offers = []hubproto.Item{advisoryItem("it1")}
	d.hubCycle(context.Background())
	if verified != 0 || len(fh.snapshot().claims) != 0 {
		t.Fatalf("verified %d offers while capped", verified)
	}
	if st := d.hubStatusData(); len(st.OfferSkips) != 1 || st.OfferSkips[0].Reason != hub.ReasonCapped {
		t.Errorf("skips = %+v", st.OfferSkips)
	}
}

// The heartbeat carries the applied policy version; an old hub that refuses
// it gets the old frame instead of losing the heartbeat.
func TestHubHeartbeatPolicyVersion(t *testing.T) {
	d, fh, _ := hubFixture(t)
	d.teamPolicy.version, d.teamPolicy.applied = 4, true
	d.hubCycle(context.Background())
	if b := fh.snapshot().beats; len(b) != 1 || b[0].PolicyVersion != 4 {
		t.Fatalf("beats = %+v", b)
	}
	fh.mu.Lock()
	fh.beatErr400 = true
	fh.mu.Unlock()
	d.hubCycle(context.Background())
	b := fh.snapshot().beats
	if len(b) != 3 || b[2].PolicyVersion != 0 {
		t.Fatalf("beats = %+v, want a retry without policy_version", b)
	}
	if st := d.hubStatusData(); st.LastHeartbeat.IsZero() || !st.LegacyHub {
		t.Errorf("status = %+v", st)
	}
	// While the hub is legacy the field is not sent at all.
	d.hubCycle(context.Background())
	if b := fh.snapshot().beats; len(b) != 4 || b[3].PolicyVersion != 0 {
		t.Errorf("beats = %+v", b)
	}
}

// An old hub refusing a fail's pr_url / group gets the reason alone.
func TestHubFailExtrasDroppedForOldHub(t *testing.T) {
	d, fh, _ := hubFixture(t)
	fh.failErr = &hub.APIError{Status: 400, Code: hubproto.CodeBadRequest}
	d.hubEnqueue(hubOutcome{ItemID: "it9", Action: hubActFail, Reason: string(hubproto.FailPRClosed),
		PRURL: "https://github.com/acme/widgets/pull/7", Group: "abcdef0123456789", Since: time.Now()})
	d.hubOutcomeCycle(context.Background())
	reqs := fh.snapshot().failReqs
	if len(reqs) != 2 || reqs[1].PRURL != "" || reqs[1].Group != "" || reqs[1].Reason != hubproto.FailPRClosed {
		t.Fatalf("fail requests = %+v", reqs)
	}
	if n := d.hubStatusData().PendingOutcomes; n != 0 {
		t.Errorf("pending = %d", n)
	}
}

// Status shows each repo's findings health, a successful cycle clears the
// last error and stamps the last sync.
func TestHubStatusHealthAndLastSync(t *testing.T) {
	d, fh, _ := hubFixture(t)
	d.hub.collect = func(context.Context, []string) hub.Result {
		return hub.Result{Skipped: []string{"acme/widgets"}, Health: []hub.RepoHealth{{Repo: "acme/widgets", Alerts: "no_access", PullRequests: "ok"}}}
	}
	fh.beatErr = errors.New("connection refused")
	d.hubCycle(context.Background())
	st := d.hubStatusData()
	if st.LastError == "" || !st.LastSync.IsZero() {
		t.Fatalf("after a failed cycle: %+v", st)
	}
	fh.mu.Lock()
	fh.beatErr = nil
	fh.mu.Unlock()
	d.hubCycle(context.Background())
	st = d.hubStatusData()
	if st.LastError != "" || st.LastSync.IsZero() {
		t.Errorf("a clean cycle must clear the error: %+v", st)
	}
	if len(st.FindingsHealth) != 1 || st.FindingsHealth[0].Alerts != "no_access" || st.FindingsHealth[0].Repo != "acme/widgets" {
		t.Errorf("health = %+v", st.FindingsHealth)
	}
}

// The renewal pass reports an outcome a 404-less delivery change produced:
// in-review items are not renewed; their complete goes out as the owner's.
func TestHubInReviewCompleteIsNotRenewed(t *testing.T) {
	d, fh, _ := hubFixture(t)
	fh.offers = []hubproto.Item{advisoryItem("it1")}
	d.hubCycle(context.Background())
	fh.mu.Lock()
	fh.offers = nil
	fh.outErr = errors.New("connection refused")
	fh.mu.Unlock()
	id := hubSessions(d)[0].ID
	d.sessions.Update(id, func(s *session.Session) bool {
		s.HubItem.InReview, s.HubItem.Progress = true, "review_pending"
		return true
	})
	setHubPR(d, id, &scm.PR{Number: 7, URL: "https://github.com/acme/widgets/pull/7", State: "MERGED"}, state.DeliveryMerged)
	d.hubCycle(context.Background())
	if lv := fh.snapshot().beats[1].LiveLeases; len(lv) != 0 {
		t.Errorf("an in-review item's unsent complete must not renew a lease: %v", lv)
	}
}
