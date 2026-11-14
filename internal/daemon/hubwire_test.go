package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sushidev-team/lola/hubproto"
	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/hub"
	"github.com/sushidev-team/lola/internal/linear"
	"github.com/sushidev-team/lola/internal/scm"
	"github.com/sushidev-team/lola/internal/session"
	"github.com/sushidev-team/lola/internal/state"
	"github.com/sushidev-team/lola/internal/sysload"
)

// fakeHub is a scripted hub. Every call is recorded; nothing touches a network.
type fakeHub struct {
	mu       sync.Mutex
	offers   []hubproto.Item
	claimErr error
	beatErr  error
	lost     []string
	outErr   error // returned by Complete / Fail / Release / Progress
	// progErr is returned by Progress (nil = outErr); failErr by a Fail
	// carrying pr_url or group (an old hub's 400).
	progErr error
	failErr error
	// outErrSeq is consumed first, one error per outcome call (nil = ok).
	outErrSeq  []error
	beatErr400 bool // refuse a heartbeat carrying policy_version (an old hub)
	progresses []hubproto.ProgressRequest
	failReqs   []hubproto.FailRequest
	offerCalls int
	beats      []hubproto.HeartbeatRequest
	findings   [][]hubproto.Item
	claims     []string
	completes  []hubproto.CompleteRequest
	fails      []hubproto.FailReason
	releases   []string
	policies   int
	board      hubproto.BoardResponse
	boardErr   error
	boards     int
	org        hubproto.OrgView
	orgs       int
	stats      hubproto.StatsView
	statsCalls int
	// offerErr / findErr / policyErr script those calls' answers.
	offerErr  error
	findErr   error
	policyErr error
}

func (f *fakeHub) Board(context.Context, hubproto.State, int) (hubproto.BoardResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.boards++
	return f.board, f.boardErr
}

func (f *fakeHub) Org(context.Context) (hubproto.OrgView, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.orgs++
	return f.org, nil
}

func (f *fakeHub) Stats(context.Context) (hubproto.StatsView, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.statsCalls++
	return f.stats, nil
}

func (f *fakeHub) Heartbeat(_ context.Context, hb hubproto.HeartbeatRequest) (hubproto.HeartbeatResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.beats = append(f.beats, hb)
	if f.beatErr != nil {
		return hubproto.HeartbeatResponse{}, f.beatErr
	}
	if f.beatErr400 && hb.PolicyVersion > 0 {
		return hubproto.HeartbeatResponse{}, &hub.APIError{Status: 400, Code: hubproto.CodeBadRequest}
	}
	resp := hubproto.HeartbeatResponse{Lost: slices.Clone(f.lost)}
	for _, id := range hb.LiveLeases {
		if !slices.Contains(f.lost, id) {
			resp.Renewed = append(resp.Renewed, hubproto.Lease{ItemID: id, Device: "dev", Expires: time.Now().Add(30 * time.Minute)})
		}
	}
	return resp, nil
}

func (f *fakeHub) ReportFindings(_ context.Context, items []hubproto.Item) (hubproto.FindingsResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.findings = append(f.findings, items)
	if f.findErr != nil {
		return hubproto.FindingsResponse{}, f.findErr
	}
	return hubproto.FindingsResponse{Accepted: len(items)}, nil
}

func (f *fakeHub) Offers(context.Context) ([]hubproto.Item, int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.offerCalls++
	if f.offerErr != nil {
		return nil, 0, f.offerErr
	}
	return slices.Clone(f.offers), 0, nil
}

func (f *fakeHub) Claim(_ context.Context, id string) (hubproto.Lease, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.claims = append(f.claims, id)
	if f.claimErr != nil {
		return hubproto.Lease{}, f.claimErr
	}
	return hubproto.Lease{ItemID: id, Device: "dev", Expires: time.Now().Add(30 * time.Minute)}, nil
}

func (f *fakeHub) Complete(_ context.Context, _ string, req hubproto.CompleteRequest) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.completes = append(f.completes, req)
	return f.nextErrLocked()
}

// nextErrLocked is the scripted answer to one outcome call.
func (f *fakeHub) nextErrLocked() error {
	if len(f.outErrSeq) > 0 {
		err := f.outErrSeq[0]
		f.outErrSeq = f.outErrSeq[1:]
		return err
	}
	return f.outErr
}

func (f *fakeHub) Fail(_ context.Context, _ string, req hubproto.FailRequest) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failReqs = append(f.failReqs, req)
	if f.failErr != nil && (req.PRURL != "" || req.Group != "") {
		return f.failErr
	}
	f.fails = append(f.fails, req.Reason)
	return f.nextErrLocked()
}

func (f *fakeHub) Progress(_ context.Context, _ string, req hubproto.ProgressRequest) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.progresses = append(f.progresses, req)
	if f.progErr != nil {
		return f.progErr
	}
	return f.nextErrLocked()
}

func (f *fakeHub) Release(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.releases = append(f.releases, id)
	return f.nextErrLocked()
}

func (f *fakeHub) FetchPolicy(context.Context, config.HubConfig, string, string, time.Time) (hub.StoredPolicy, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.policies++
	if f.policyErr != nil {
		return hub.StoredPolicy{}, f.policyErr
	}
	return hub.StoredPolicy{Policy: hubproto.Policy{Version: 3}}, nil
}

// hubCalls is what a fakeHub saw, copied out from under its lock.
type hubCalls struct {
	progresses []hubproto.ProgressRequest
	failReqs   []hubproto.FailRequest
	offerCalls int
	beats      []hubproto.HeartbeatRequest
	findings   [][]hubproto.Item
	claims     []string
	completes  []hubproto.CompleteRequest
	fails      []hubproto.FailReason
	releases   []string
	policies   int
}

func (f *fakeHub) snapshot() hubCalls {
	f.mu.Lock()
	defer f.mu.Unlock()
	return hubCalls{
		progresses: slices.Clone(f.progresses), failReqs: slices.Clone(f.failReqs), offerCalls: f.offerCalls,
		beats: slices.Clone(f.beats), findings: slices.Clone(f.findings), claims: slices.Clone(f.claims),
		completes: slices.Clone(f.completes), fails: slices.Clone(f.fails), releases: slices.Clone(f.releases),
		policies: f.policies,
	}
}

func advisoryItem(id string) hubproto.Item {
	return hubproto.Item{ID: id, Kind: hubproto.KindDepAdvisory, DepAdvisory: &hubproto.DepAdvisory{
		Repo: "acme/widgets", Ecosystem: "npm", Package: "lodash", Vulnerable: "< 4.17.21",
		Fixed: "4.17.21", Advisory: "GHSA-jf85-cpcp-j695", Severity: hubproto.SeverityHigh,
	}}
}

func bumpItem(id string) hubproto.Item {
	return hubproto.Item{ID: id, Kind: hubproto.KindRedBumpPR, RedBumpPR: &hubproto.RedBumpPR{
		Repo: "acme/widgets", PR: 42, Branch: "dependabot/npm_and_yarn/lodash-4.17.21", Package: "lodash",
		Author: hubproto.AuthorDependabot,
	}}
}

// hubFixture: one opted-in, sandboxed project on a joined, enabled hub.
func hubFixture(t *testing.T) (*Daemon, *fakeHub, *fakeNative) {
	t.Helper()
	p := labelPoll("web")
	p.HubChores = true
	p.Sandbox = "docker"
	cfg := testConfig(p)
	cfg.Hub = config.HubConfig{Enabled: true, URL: "https://hub.example.com"}
	nat := &fakeNative{}
	d := newTestDaemon(t, cfg, nil, nat)
	fh := &fakeHub{}
	d.hub.newClient = func(url, dir string) (hubAPI, hub.Identity, error) {
		return fh, hub.Identity{URL: url, DeviceID: "dev", Org: "acme"}, nil
	}
	d.hub.collect = func(context.Context, []string) hub.Result { return hub.Result{} }
	// GitHub confirms every offer as offered; a red bump PR's head is "aaa".
	d.hub.verify = func(_ context.Context, it hubproto.Item) (hub.Verified, error) {
		v := hub.Verified{Item: it}
		if it.Kind == hubproto.KindRedBumpPR {
			v.Head = "aaa"
		}
		return v, nil
	}
	return d, fh, nat
}

func hubSessions(d *Daemon) []session.Session {
	var out []session.Session
	for _, s := range d.sessions.Snapshot() {
		if s.HubItem.ItemID != "" {
			out = append(out, s)
		}
	}
	return out
}

func TestHubAcceptClaimSpawnAdvisory(t *testing.T) {
	d, fh, nat := hubFixture(t)
	fh.offers = []hubproto.Item{advisoryItem("it1")}
	d.hubCycle(context.Background())

	got := fh.snapshot()
	if len(got.beats) != 1 || len(got.claims) != 1 || got.claims[0] != "it1" {
		t.Fatalf("want one heartbeat and one claim, got beats=%d claims=%v", len(got.beats), got.claims)
	}
	if hb := got.beats[0]; !slices.Equal(hb.Repos, []string{"acme/widgets"}) || hb.FreeSlots != 10 {
		t.Errorf("heartbeat = %+v", hb)
	}
	if len(nat.manualAgents) != 1 {
		t.Fatalf("want one manual agent spawn, got %+v", nat.manualAgents)
	}
	call := nat.manualAgents[0]
	if call.branch != "lola/hub-ghsa-jf85-cpcp-j695-lodash" || !strings.Contains(call.prompt, "GHSA-jf85-cpcp-j695") {
		t.Errorf("spawn = %+v", call)
	}
	ss := hubSessions(d)
	if len(ss) != 1 || ss[0].HubItem.ItemID != "it1" || ss[0].HubItem.Kind != "dep_advisory" || ss[0].HubItem.ClaimedAt.IsZero() {
		t.Fatalf("hub session = %+v", ss)
	}
	if n := d.hubClaimsToday(time.Now()); n != 1 {
		t.Errorf("claims today = %d, want 1", n)
	}

	// A second cycle neither re-claims the same item nor drops the lease.
	d.hubCycle(context.Background())
	got = fh.snapshot()
	if len(got.claims) != 1 {
		t.Errorf("re-claimed: %v", got.claims)
	}
	if lv := got.beats[1].LiveLeases; !slices.Equal(lv, []string{"it1"}) {
		t.Errorf("live leases = %v", lv)
	}
}

func TestHubRedBumpUsesPRSessionOnBotBranch(t *testing.T) {
	d, fh, nat := hubFixture(t)
	fh.offers = []hubproto.Item{bumpItem("it2")}
	d.hubCycle(context.Background())
	if len(nat.prAgents) != 1 || nat.prAgents[0].branch != "dependabot/npm_and_yarn/lodash-4.17.21" {
		t.Fatalf("pr agent = %+v", nat.prAgents)
	}
	if len(nat.manualAgents) != 0 {
		t.Errorf("a red bump must not create a new branch")
	}
}

// A spawn that fails after the claim reports spawn_failed (an attempt the
// hub counts, so a broken project escalates) and cools the item down here.
func TestHubSpawnFailureFailsAndCoolsDown(t *testing.T) {
	d, fh, nat := hubFixture(t)
	nat.manualAgentErr = errors.New("worktree add failed")
	fh.offers = []hubproto.Item{advisoryItem("it1")}
	d.hubCycle(context.Background())
	got := fh.snapshot()
	if !slices.Equal(got.fails, []hubproto.FailReason{hubproto.FailSpawn}) || len(got.releases) != 0 {
		t.Fatalf("fails = %v releases = %v", got.fails, got.releases)
	}
	if !d.hubCoolingDown("it1", time.Now()) || d.hubCoolingDown("it1", time.Now().Add(hubproto.DeviceCooldown+time.Minute)) {
		t.Error("the item must cool down here for hubproto.DeviceCooldown")
	}
	// The hub re-offers it at once: not claimed again, no count spent.
	d.hubCycle(context.Background())
	if n := len(fh.snapshot().claims); n != 1 {
		t.Errorf("claims = %d, want 1", n)
	}
	if st := d.hubStatusData(); len(st.OfferSkips) != 1 || st.OfferSkips[0].Reason != hub.ReasonCooldown {
		t.Errorf("skips = %+v", st.OfferSkips)
	}
	if len(hubSessions(d)) != 0 {
		t.Errorf("no session may be recorded")
	}
	if n := d.hubStatusData().PendingOutcomes; n != 0 {
		t.Errorf("pending outcomes = %d", n)
	}
	// The count stays: a broken project must not claim the item every cycle.
	if n := d.hubClaimsToday(time.Now()); n != 1 {
		t.Errorf("claims today = %d", n)
	}
	if g, _ := d.slotCounts("web"); g != 0 {
		t.Errorf("slot reservation leaked: %d", g)
	}
}

func TestHubRefusedClaimRefundsCount(t *testing.T) {
	d, fh, nat := hubFixture(t)
	fh.claimErr = &hub.APIError{Status: 409, Code: hubproto.CodeConflict}
	fh.offers = []hubproto.Item{advisoryItem("it1")}
	d.hubCycle(context.Background())
	if len(nat.manualAgents) != 0 {
		t.Fatal("spawned without a claim")
	}
	if n := d.hubClaimsToday(time.Now()); n != 0 {
		t.Errorf("claims today = %d, want 0", n)
	}
}

func TestHubGatesSkipWithoutClaiming(t *testing.T) {
	cases := map[string]func(d *Daemon){
		"global cap": func(d *Daemon) {
			d.cfg.Defaults.GlobalCap = 1
			d.sessions.Upsert(session.Session{ID: "lola-web-x", Source: "native", Project: "web", AgentState: state.AgentWorking, Delivery: state.DeliveryNone})
		},
		"machine load": func(d *Daemon) {
			d.cfg.Load.MaxLoadPerCPU = 1.5
			d.spend.sample = func(context.Context) sysload.Sample {
				return sysload.Sample{Load1: 20, CPUs: 8, FreeMemPercent: 50}
			}
		},
		"health": func(d *Daemon) {
			d.runtimeHealth = func(string) error { return errors.New("missing docker") }
		},
		"daily cap": func(d *Daemon) {
			zero := 0
			d.cfg.Hub.MaxClaimsPerDay = &zero
		},
		"not sandboxed": func(d *Daemon) { d.cfg.Projects[0].Sandbox = "" },
		"not opted in":  func(d *Daemon) { d.cfg.Projects[0].HubChores = false },
		"kind rejected": func(d *Daemon) { d.cfg.Hub.AcceptKinds = []string{"red_bump_pr"} },
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			d, fh, nat := hubFixture(t)
			setup(d)
			fh.offers = []hubproto.Item{advisoryItem("it1")}
			d.hubCycle(context.Background())
			if got := fh.snapshot(); len(got.claims) != 0 {
				t.Fatalf("claimed through the %s gate: %v", name, got.claims)
			}
			if len(nat.manualAgents) != 0 {
				t.Fatalf("spawned through the %s gate", name)
			}
		})
	}
}

func TestHubLostLeaseMarksButKeepsSession(t *testing.T) {
	d, fh, nat := hubFixture(t)
	notes := &hubNotes{}
	d.notifier = notes
	fh.offers = []hubproto.Item{advisoryItem("it1")}
	d.hubCycle(context.Background())
	fh.mu.Lock()
	fh.offers, fh.lost = nil, []string{"it1"}
	fh.claimErr = &hub.APIError{Status: 409, Code: hubproto.CodeConflict} // another device holds it
	fh.mu.Unlock()
	d.hubCycle(context.Background())

	ss := hubSessions(d)
	if len(ss) != 1 || !ss[0].HubItem.LeaseLost || !ss[0].HubItem.LeaseTaken {
		t.Fatalf("session = %+v", ss[0].HubItem)
	}
	if h := hubSessionHub(ss[0]); h == nil || !h.LeaseTaken {
		t.Errorf("session hub info = %+v", h)
	}
	if !notes.has("Hub chore taken by another device") {
		t.Errorf("notes = %+v", notes.titles())
	}
	if len(nat.killCalls()) != 0 {
		t.Fatal("a lost lease must never kill the session")
	}
	// No longer renewed, and teardown reports nothing.
	d.hubCycle(context.Background())
	if lv := fh.snapshot().beats[2].LiveLeases; len(lv) != 0 {
		t.Errorf("lost lease still renewed: %v", lv)
	}
	d.dropSession(ss[0])
	d.hubOutcomeCycle(context.Background())
	if got := fh.snapshot(); len(got.fails)+len(got.completes) != 0 {
		t.Errorf("outcome reported for a lost lease: %+v", got)
	}
}

// A dep_advisory's PR moves the item to in_review: progress once per
// delivery, the work stays open (no lease renewed, the repo stays busy), and
// only the merge completes it.
func TestHubAdvisoryProgressThenCompleteOnMerge(t *testing.T) {
	d, fh, _ := hubFixture(t)
	fh.offers = []hubproto.Item{advisoryItem("it1")}
	d.hubCycle(context.Background())
	id := hubSessions(d)[0].ID
	setDel := func(del state.DeliveryState) {
		d.sessions.Update(id, func(s *session.Session) bool {
			s.PR = &scm.PR{Number: 7, URL: "https://github.com/acme/widgets/pull/7", State: "OPEN"}
			s.SetDelivery(del, time.Now())
			return true
		})
		d.hubOutcomeCycle(context.Background())
	}
	setDel(state.DeliveryCIPending)
	d.hubOutcomeCycle(context.Background())
	d.hubCycle(context.Background())

	got := fh.snapshot()
	if len(got.completes) != 0 || len(got.progresses) != 1 {
		t.Fatalf("completes = %d progresses = %d, want 0 / 1", len(got.completes), len(got.progresses))
	}
	if p := got.progresses[0]; p.PRURL != "https://github.com/acme/widgets/pull/7" || p.Delivery != hubproto.DeliveryCIPending || p.Validate() != nil {
		t.Errorf("progress = %+v", p)
	}
	s, _ := d.sessions.Get(id)
	if !s.HubItem.InReview || !s.HubItem.Open() || s.HubItem.Reported != "" {
		t.Fatalf("in review work = %+v", s.HubItem)
	}
	if !d.hubRepoBusy("acme/widgets") {
		t.Error("a PR in review must keep the repository busy")
	}
	if lv := got.beats[len(got.beats)-1].LiveLeases; len(lv) != 0 {
		t.Errorf("an in-review item holds no lease to renew: %v", lv)
	}
	// A new delivery is sent again; the same one is not.
	setDel(state.DeliveryReviewPending)
	setDel(state.DeliveryReviewPending)
	if n := len(fh.snapshot().progresses); n != 2 {
		t.Fatalf("progresses = %d, want 2", n)
	}
	setDel(state.DeliveryMerged)
	d.hubOutcomeCycle(context.Background())
	got = fh.snapshot()
	if len(got.completes) != 1 {
		t.Fatalf("completes = %+v", got.completes)
	}
	if c := got.completes[0]; c.PRURL != "https://github.com/acme/widgets/pull/7" || c.Delivery != hubproto.DeliveryMerged || c.Validate() != nil {
		t.Errorf("complete = %+v", c)
	}
	s, _ = d.sessions.Get(id)
	if s.HubItem.Reported != "complete" {
		t.Errorf("guard = %q", s.HubItem.Reported)
	}
	// Teardown after completing reports nothing more.
	d.dropSession(s)
	d.hubOutcomeCycle(context.Background())
	if got := fh.snapshot(); len(got.fails) != 0 || len(got.completes) != 1 {
		t.Errorf("after teardown: %+v", got)
	}
}

func TestHubRedBumpCompletesOnlyAfterHeadMovedAndGreen(t *testing.T) {
	d, fh, _ := hubFixture(t)
	fh.offers = []hubproto.Item{bumpItem("it2")}
	d.hubCycle(context.Background())
	id := hubSessions(d)[0].ID
	setPR := func(head string, del state.DeliveryState) {
		d.sessions.Update(id, func(s *session.Session) bool {
			s.PR = &scm.PR{Number: 42, URL: "https://github.com/acme/widgets/pull/42", State: "OPEN", HeadSHA: head}
			s.SetDelivery(del, time.Now())
			return true
		})
		d.hubOutcomeCycle(context.Background())
	}
	setPR("aaa", state.DeliveryCIFailed)
	setPR("aaa", state.DeliveryReviewPending) // green without a push: not the agent's work
	if n := len(fh.snapshot().completes); n != 0 {
		t.Fatalf("completed before the agent pushed: %d", n)
	}
	setPR("bbb", state.DeliveryReviewPending)
	if n := len(fh.snapshot().completes); n != 1 {
		t.Fatalf("completes = %d, want 1", n)
	}
}

func TestHubFailOnceOnTeardownWithoutPR(t *testing.T) {
	d, fh, _ := hubFixture(t)
	fh.offers = []hubproto.Item{advisoryItem("it1")}
	d.hubCycle(context.Background())
	s := hubSessions(d)[0]
	s.EnsureAxes()
	s.SetAgentState(state.AgentDead, "", time.Now())

	// The hub is down at first: the outcome waits in the outbox.
	fh.mu.Lock()
	fh.outErr = errors.New("connection refused")
	fh.mu.Unlock()
	d.dropSession(s)
	d.hubOutcomeCycle(context.Background())
	if n := d.hubStatusData().PendingOutcomes; n != 1 {
		t.Fatalf("pending = %d, want 1", n)
	}
	fh.mu.Lock()
	fh.outErr = nil
	fh.mu.Unlock()
	d.hubOutcomeCycle(context.Background())
	d.hubOutcomeCycle(context.Background())
	d.hubSessionEnded(s) // a second teardown signal for the same item
	d.hubOutcomeCycle(context.Background())

	// One attempt while the hub was down, one that landed, then nothing: not
	// the idle cycle, not a second end signal for the same item.
	if got := fh.snapshot().fails; !slices.Equal(got, []hubproto.FailReason{hubproto.FailAgent, hubproto.FailAgent}) {
		t.Fatalf("fails = %v", got)
	}
	if n := d.hubStatusData().PendingOutcomes; n != 0 {
		t.Errorf("pending = %d", n)
	}
}

func TestHubKilledLiveSessionFailsAsRefused(t *testing.T) {
	d, fh, _ := hubFixture(t)
	fh.offers = []hubproto.Item{advisoryItem("it1")}
	d.hubCycle(context.Background())
	s := hubSessions(d)[0]
	d.dropSession(s)
	d.hubOutcomeCycle(context.Background())
	if got := fh.snapshot().fails; !slices.Equal(got, []hubproto.FailReason{hubproto.FailRefused}) {
		t.Fatalf("fails = %v", got)
	}
}

func TestHubTerminalRefusalDropsOutcome(t *testing.T) {
	d, fh, _ := hubFixture(t)
	fh.outErr = &hub.APIError{Status: 409, Code: hubproto.CodeLeaseLost}
	d.hubEnqueue(hubOutcome{ItemID: "it9", Action: hubActFail, Reason: string(hubproto.FailAgent), Since: time.Now()})
	d.hubOutcomeCycle(context.Background())
	if n := d.hubStatusData().PendingOutcomes; n != 0 {
		t.Errorf("a lease_lost refusal must not be retried, pending = %d", n)
	}
}

func TestHubDisabledDoesNothing(t *testing.T) {
	d, fh, nat := hubFixture(t)
	d.cfg.Hub.Enabled = false
	called := false
	d.hub.newClient = func(string, string) (hubAPI, hub.Identity, error) {
		called = true
		return fh, hub.Identity{}, nil
	}
	fh.offers = []hubproto.Item{advisoryItem("it1")}
	d.hubCycle(context.Background())
	d.hubOutcomeCycle(context.Background())
	if called || len(fh.snapshot().beats) != 0 || len(nat.manualAgents) != 0 {
		t.Fatalf("disabled hub acted: client=%v %+v", called, fh.snapshot())
	}
}

func TestHubNotJoinedDoesNothing(t *testing.T) {
	d, fh, _ := hubFixture(t)
	d.hub.newClient = newRealHubClient // empty LOLA_HOME: no identity
	fh.offers = []hubproto.Item{advisoryItem("it1")}
	d.hubCycle(context.Background())
	st := d.hubStatusData()
	if st.Joined || !strings.Contains(st.LastError, "not joined") {
		t.Fatalf("status = %+v", st)
	}
}

func TestHubHeartbeatFailureClaimsNothing(t *testing.T) {
	d, fh, _ := hubFixture(t)
	fh.beatErr = errors.New("timeout")
	fh.offers = []hubproto.Item{advisoryItem("it1")}
	d.hubCycle(context.Background())
	if got := fh.snapshot(); len(got.claims) != 0 {
		t.Fatalf("claimed without a heartbeat: %v", got.claims)
	}
}

func TestHubFindingsRateLimitedPerRepo(t *testing.T) {
	d, fh, _ := hubFixture(t)
	var scans [][]string
	d.hub.collect = func(_ context.Context, repos []string) hub.Result {
		scans = append(scans, slices.Clone(repos))
		return hub.Result{Items: []hubproto.Item{{Kind: hubproto.KindDepAdvisory, DepAdvisory: advisoryItem("").DepAdvisory}}}
	}
	d.hubCycle(context.Background())
	d.hubCycle(context.Background())
	if len(scans) != 1 || !slices.Equal(scans[0], []string{"acme/widgets"}) {
		t.Fatalf("scans = %v", scans)
	}
	if n := len(fh.snapshot().findings); n != 1 {
		t.Errorf("reports = %d", n)
	}
	now := time.Now().Add(hubFindingsEvery + time.Minute)
	d.hub.now = func() time.Time { return now }
	d.hubCycle(context.Background())
	if len(scans) != 2 {
		t.Errorf("an hour later the repo is due again, scans = %v", scans)
	}
}

func TestHubPolicyFetchedOnlyWithTrustedKeys(t *testing.T) {
	d, fh, _ := hubFixture(t)
	d.hubCycle(context.Background())
	if fh.snapshot().policies != 0 {
		t.Fatal("fetched a policy nothing could verify")
	}
	d.cfg.Hub.TrustedPolicyKeys = map[string]string{"k1": "x"}
	d.hubCycle(context.Background())
	d.hubCycle(context.Background())
	if n := fh.snapshot().policies; n != 1 {
		t.Fatalf("policy fetches = %d, want 1 (hourly)", n)
	}
	if v := d.hubStatusData().PolicyVersion; v != 3 {
		t.Errorf("policy version = %d", v)
	}
}

func TestHubStatusCommand(t *testing.T) {
	d, fh, _ := hubFixture(t)
	fh.offers = []hubproto.Item{advisoryItem("it1")}
	d.hubCycle(context.Background())
	st := d.hubStatusData()
	if !st.Enabled || !st.Joined || st.Org != "acme" || st.Device != "dev" || st.ClaimsToday != 1 ||
		st.MaxClaimsPerDay != 4 || st.LastHeartbeat.IsZero() || len(st.Sessions) != 1 || st.Sessions[0].ItemID != "it1" {
		t.Fatalf("status = %+v", st)
	}
}

// ---- review fixes ----------------------------------------------------------

func TestHubUnconfirmedOfferIsNotClaimed(t *testing.T) {
	for _, it := range []hubproto.Item{advisoryItem("it1"), bumpItem("it2")} {
		d, fh, nat := hubFixture(t)
		d.hub.verify = func(context.Context, hubproto.Item) (hub.Verified, error) {
			return hub.Verified{}, hub.ErrNotConfirmed
		}
		fh.offers = []hubproto.Item{it}
		d.hubCycle(context.Background())
		if got := fh.snapshot(); len(got.claims) != 0 || len(nat.prAgents)+len(nat.manualAgents) != 0 {
			t.Fatalf("%s: claimed an offer GitHub did not confirm: %+v", it.Kind, got.claims)
		}
		if n := d.hubClaimsToday(time.Now()); n != 0 {
			t.Errorf("%s: claims today = %d", it.Kind, n)
		}
	}
}

func TestHubRedBumpOnMainIsRefused(t *testing.T) {
	d, fh, nat := hubFixture(t)
	evil := bumpItem("it2")
	evil.RedBumpPR.Branch = "main"
	// GitHub says the PR's branch is the bot's, not main.
	d.hub.verify = func(_ context.Context, it hubproto.Item) (hub.Verified, error) {
		return hub.Verified{Item: bumpItem(it.ID), Head: "aaa"}, nil
	}
	fh.offers = []hubproto.Item{evil}
	d.hubCycle(context.Background())
	if len(fh.snapshot().claims) != 0 || len(nat.prAgents) != 0 {
		t.Fatalf("a branch GitHub does not confirm was claimed: %+v", nat.prAgents)
	}
	// No head commit from GitHub refuses too.
	d.hub.verify = func(_ context.Context, it hubproto.Item) (hub.Verified, error) {
		return hub.Verified{Item: it}, nil
	}
	fh.offers = []hubproto.Item{bumpItem("it3")}
	d.hubCycle(context.Background())
	if len(fh.snapshot().claims) != 0 {
		t.Fatal("claimed a red bump PR with no verified head")
	}
}

func TestHubAdvisoryBriefedFromGitHubsAlert(t *testing.T) {
	d, fh, nat := hubFixture(t)
	offered := advisoryItem("it1")
	offered.DepAdvisory.Fixed = "99.0.0"
	d.hub.verify = func(_ context.Context, it hubproto.Item) (hub.Verified, error) {
		return hub.Verified{Item: advisoryItem(it.ID)}, nil // GitHub: fixed in 4.17.21
	}
	fh.offers = []hubproto.Item{offered}
	d.hubCycle(context.Background())
	if len(nat.manualAgents) != 1 {
		t.Fatalf("spawns = %+v", nat.manualAgents)
	}
	if p := nat.manualAgents[0].prompt; strings.Contains(p, "99.0.0") || !strings.Contains(p, "4.17.21") {
		t.Errorf("briefing must carry GitHub's fixed version:\n%s", p)
	}
}

func TestHubRedBumpStartHeadStampedAtClaim(t *testing.T) {
	d, fh, _ := hubFixture(t)
	fh.offers = []hubproto.Item{bumpItem("it2")}
	d.hubCycle(context.Background())
	ss := hubSessions(d)
	if len(ss) != 1 || ss[0].HubItem.StartHead != "aaa" {
		t.Fatalf("start head = %+v", ss)
	}
	// The first head lola ever sees is already the agent's push (the hub was
	// unreachable meanwhile): it still counts as moved.
	d.sessions.Update(ss[0].ID, func(s *session.Session) bool {
		s.PR = &scm.PR{Number: 42, URL: "https://github.com/acme/widgets/pull/42", State: "OPEN", HeadSHA: "bbb"}
		s.SetDelivery(state.DeliveryReviewPending, time.Now())
		return true
	})
	d.hubOutcomeCycle(context.Background())
	if n := len(fh.snapshot().completes); n != 1 {
		t.Fatalf("completes = %d, want 1", n)
	}
}

func TestHubRedBumpTeardownOutcomes(t *testing.T) {
	cases := []struct {
		name  string
		head  string
		del   state.DeliveryState
		agent state.AgentState
		want  hubproto.FailReason
	}{
		{"unmoved red, person stopped", "aaa", state.DeliveryCIFailed, state.AgentWorking, hubproto.FailRefused},
		{"unmoved red, agent died", "aaa", state.DeliveryCIFailed, state.AgentDead, hubproto.FailAgent},
		{"unmoved green", "aaa", state.DeliveryReviewPending, state.AgentWorking, hubproto.FailObsolete},
		{"moved red", "bbb", state.DeliveryCIFailed, state.AgentWorking, hubproto.FailCI},
	}
	for _, c := range cases {
		s := session.Session{ID: "x", Repo: "acme/widgets",
			HubItem: session.HubWork{ItemID: "it2", Kind: string(hubproto.KindRedBumpPR), StartHead: "aaa"},
			PR:      &scm.PR{Number: 42, URL: "https://github.com/acme/widgets/pull/42", State: "OPEN", HeadSHA: c.head}}
		s.EnsureAxes()
		s.SetAgentState(c.agent, "", time.Now())
		s.SetDelivery(c.del, time.Now())
		o, ok := hubOutcomeFor(s, s.HubItem, true, false)
		if !ok || o.Action != hubActFail || o.Reason != string(c.want) {
			t.Errorf("%s: outcome = %+v, want fail:%s", c.name, o, c.want)
		}
	}
	// No start head at all and still red: never not_reproducible.
	s := session.Session{ID: "x", Repo: "acme/widgets",
		HubItem: session.HubWork{ItemID: "it2", Kind: string(hubproto.KindRedBumpPR)},
		PR:      &scm.PR{Number: 42, URL: "https://github.com/acme/widgets/pull/42", State: "OPEN", HeadSHA: "aaa"}}
	s.EnsureAxes()
	s.SetDelivery(state.DeliveryCIFailed, time.Now())
	if o, _ := hubOutcomeFor(s, s.HubItem, true, false); o.Reason == string(hubproto.FailObsolete) {
		t.Errorf("unknown start head reported not_reproducible: %+v", o)
	}
}

func TestHubAdoptionKeepsHubItem(t *testing.T) {
	d := newTestDaemon(t, nativeTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeNative{})
	prev := nativeSess("FE-1", "working")
	work := session.HubWork{ItemID: "it1", Kind: "red_bump_pr", ClaimedAt: time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC),
		StartHead: "aaa", Reported: "complete", LeaseLost: true}
	prev.HubItem = work
	d.sessions.Upsert(prev)
	d.native = &fakeNative{adopted: []session.Session{
		{ID: prev.ID, Source: "native", Project: "p1", Issue: "FE-1", TmuxName: prev.ID, Status: "working"},
	}}
	d.adoptNativeSessions(context.Background())
	got := findSession(t, d.sessions.Snapshot(), prev.ID)
	if got.HubItem.ItemID != work.ItemID || got.HubItem.StartHead != "aaa" || got.HubItem.Reported != "complete" ||
		!got.HubItem.LeaseLost || !got.HubItem.ClaimedAt.Equal(work.ClaimedAt) {
		t.Fatalf("hub claim lost across adoption: %+v", got.HubItem)
	}
}

func TestHubReclaimReportsAgainAndDropsStaleRelease(t *testing.T) {
	d, fh, _ := hubFixture(t)
	t0 := time.Now().Add(-time.Hour)
	// An earlier claim of it1: its release still waits, and an even earlier
	// claim of it1 already settled.
	d.hubEnqueue(hubOutcome{ItemID: "it1", ClaimedAt: t0, Action: hubActRelease, Since: t0})
	d.hub.mu.Lock()
	d.hub.st.Settled = append(d.hub.st.Settled, hubOutcome{ItemID: "it1", ClaimedAt: t0.Add(-time.Hour)}.key())
	d.hub.mu.Unlock()
	fh.mu.Lock()
	fh.outErr = &hub.APIError{Status: 503}
	fh.offers = []hubproto.Item{advisoryItem("it1")}
	fh.mu.Unlock()
	d.hubCycle(context.Background()) // the release fails (503), then it1 is claimed again
	fh.mu.Lock()
	fh.outErr, fh.offers = nil, nil
	relBefore := len(fh.releases)
	fh.mu.Unlock()
	d.hubOutcomeCycle(context.Background())
	if got := fh.snapshot(); len(got.releases) != relBefore {
		t.Fatalf("a stale release of an earlier claim was sent after the re-claim: %v", got.releases)
	}
	// The new claim's outcome is reported, not swallowed by the old settle.
	id := hubSessions(d)[0].ID
	d.sessions.Update(id, func(s *session.Session) bool {
		s.PR = &scm.PR{Number: 7, URL: "https://github.com/acme/widgets/pull/7", State: "MERGED"}
		s.SetDelivery(state.DeliveryMerged, time.Now())
		return true
	})
	d.hubOutcomeCycle(context.Background())
	if n := len(fh.snapshot().completes); n != 1 {
		t.Fatalf("completes = %d, want 1", n)
	}
}

func TestHubStoppedItemIsNotReclaimed(t *testing.T) {
	d, fh, _ := hubFixture(t)
	fh.offers = []hubproto.Item{advisoryItem("it1")}
	d.hubCycle(context.Background())
	d.dropSession(hubSessions(d)[0]) // a person stopped it: fail:refused
	d.hubCycle(context.Background()) // the hub re-offers it at once
	if got := fh.snapshot(); len(got.claims) != 1 {
		t.Fatalf("a stopped item was claimed again: %v", got.claims)
	}
	later := time.Now().Add(hubCooldown + time.Minute)
	d.hub.now = func() time.Time { return later }
	d.hubCycle(context.Background())
	if got := fh.snapshot(); len(got.claims) != 2 {
		t.Fatalf("after the cooldown the item is claimable again: %v", got.claims)
	}
}

func TestHubUnsentOutcomeKeepsLeaseRenewed(t *testing.T) {
	d, fh, _ := hubFixture(t)
	fh.offers = []hubproto.Item{advisoryItem("it1")}
	d.hubCycle(context.Background())
	fh.mu.Lock()
	fh.offers, fh.outErr = nil, &hub.APIError{Status: 502}
	fh.mu.Unlock()
	id := hubSessions(d)[0].ID
	d.sessions.Update(id, func(s *session.Session) bool {
		s.PR = &scm.PR{Number: 7, URL: "https://github.com/acme/widgets/pull/7", State: "MERGED"}
		s.SetDelivery(state.DeliveryMerged, time.Now())
		return true
	})
	d.hubCycle(context.Background())
	got := fh.snapshot()
	if len(got.completes) != 1 {
		t.Fatalf("completes = %d", len(got.completes))
	}
	if s, _ := d.sessions.Get(id); s.HubItem.Reported != "complete" {
		t.Fatalf("guard = %q", s.HubItem.Reported)
	}
	d.hubCycle(context.Background())
	if lv := fh.snapshot().beats[2].LiveLeases; !slices.Equal(lv, []string{"it1"}) {
		t.Errorf("a lease with an unsent outcome must stay renewed, live = %v", lv)
	}
}

func TestHubTerminalRetriesProxyThrottling(t *testing.T) {
	for _, st := range []int{408, 429} {
		if hubTerminal(&hub.APIError{Status: st}) {
			t.Errorf("status %d without a code must be retried", st)
		}
	}
	if !hubTerminal(&hub.APIError{Status: 409, Code: hubproto.CodeLeaseLost}) {
		t.Error("409 lease_lost is final")
	}
}

func TestHubPolicyVersionFromStoreAfterRestart(t *testing.T) {
	d, _, _ := hubFixture(t)
	pub, priv, err := hubproto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	p, err := hubproto.SignPolicy(priv, "k1", hubproto.Policy{Org: "acme", Version: 5, IssuedAt: time.Now().UTC().Truncate(time.Second),
		Settings: hubproto.Settings{EscalateAfter: 2}})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(hub.StoredPolicy{Policy: p, VerifiedAt: time.Now()})
	if err := os.MkdirAll(d.hubDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d.hubDir(), hub.PolicyFile), b, 0o600); err != nil {
		t.Fatal(err)
	}
	d.cfg.Hub.TrustedPolicyKeys = map[string]string{"k1": hubproto.EncodePublicKey(pub)}
	// A fetch was made recently (before the restart): none is due now.
	d.hub.mu.Lock()
	d.hub.loaded = true
	d.hub.st.PolicyAt = time.Now()
	d.hub.mu.Unlock()
	d.hubCycle(context.Background())
	if v := d.hubStatusData().PolicyVersion; v != 5 {
		t.Errorf("policy version = %d, want the stored 5", v)
	}
}
