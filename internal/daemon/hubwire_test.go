package daemon

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sushidev-team/lola/hubproto"
	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/hub"
	"github.com/sushidev-team/lola/internal/scm"
	"github.com/sushidev-team/lola/internal/session"
	"github.com/sushidev-team/lola/internal/state"
	"github.com/sushidev-team/lola/internal/sysload"
)

// fakeHub is a scripted hub. Every call is recorded; nothing touches a network.
type fakeHub struct {
	mu        sync.Mutex
	offers    []hubproto.Item
	claimErr  error
	beatErr   error
	lost      []string
	outErr    error // returned by Complete / Fail / Release
	beats     []hubproto.HeartbeatRequest
	findings  [][]hubproto.Item
	claims    []string
	completes []hubproto.CompleteRequest
	fails     []hubproto.FailReason
	releases  []string
	policies  int
}

func (f *fakeHub) Heartbeat(_ context.Context, hb hubproto.HeartbeatRequest) (hubproto.HeartbeatResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.beats = append(f.beats, hb)
	if f.beatErr != nil {
		return hubproto.HeartbeatResponse{}, f.beatErr
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
	return hubproto.FindingsResponse{Accepted: len(items)}, nil
}

func (f *fakeHub) Offers(context.Context) ([]hubproto.Item, int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
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
	return f.outErr
}

func (f *fakeHub) Fail(_ context.Context, _ string, r hubproto.FailReason) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fails = append(f.fails, r)
	return f.outErr
}

func (f *fakeHub) Release(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.releases = append(f.releases, id)
	return f.outErr
}

func (f *fakeHub) FetchPolicy(context.Context, config.HubConfig, string, string, time.Time) (hub.StoredPolicy, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.policies++
	return hub.StoredPolicy{Policy: hubproto.Policy{Version: 3}}, nil
}

// hubCalls is what a fakeHub saw, copied out from under its lock.
type hubCalls struct {
	beats     []hubproto.HeartbeatRequest
	findings  [][]hubproto.Item
	claims    []string
	completes []hubproto.CompleteRequest
	fails     []hubproto.FailReason
	releases  []string
	policies  int
}

func (f *fakeHub) snapshot() hubCalls {
	f.mu.Lock()
	defer f.mu.Unlock()
	return hubCalls{
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

func TestHubSpawnFailureReleases(t *testing.T) {
	d, fh, nat := hubFixture(t)
	nat.manualAgentErr = errors.New("worktree add failed")
	fh.offers = []hubproto.Item{advisoryItem("it1")}
	d.hubCycle(context.Background())
	got := fh.snapshot()
	if !slices.Equal(got.releases, []string{"it1"}) {
		t.Fatalf("releases = %v", got.releases)
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
	fh.offers = []hubproto.Item{advisoryItem("it1")}
	d.hubCycle(context.Background())
	fh.mu.Lock()
	fh.offers, fh.lost = nil, []string{"it1"}
	fh.mu.Unlock()
	d.hubCycle(context.Background())

	ss := hubSessions(d)
	if len(ss) != 1 || !ss[0].HubItem.LeaseLost {
		t.Fatalf("session = %+v", ss)
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

func TestHubCompleteOnceWhenPRAppears(t *testing.T) {
	d, fh, _ := hubFixture(t)
	fh.offers = []hubproto.Item{advisoryItem("it1")}
	d.hubCycle(context.Background())
	id := hubSessions(d)[0].ID
	d.sessions.Update(id, func(s *session.Session) bool {
		s.PR = &scm.PR{Number: 7, URL: "https://github.com/acme/widgets/pull/7", State: "OPEN"}
		s.SetDelivery(state.DeliveryCIPending, time.Now())
		return true
	})
	d.hubOutcomeCycle(context.Background())
	d.hubOutcomeCycle(context.Background())
	d.hubCycle(context.Background())

	got := fh.snapshot()
	if len(got.completes) != 1 {
		t.Fatalf("completes = %+v", got.completes)
	}
	if c := got.completes[0]; c.PRURL != "https://github.com/acme/widgets/pull/7" || c.Delivery != hubproto.DeliveryCIPending || c.Validate() != nil {
		t.Errorf("complete = %+v", c)
	}
	s, _ := d.sessions.Get(id)
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
