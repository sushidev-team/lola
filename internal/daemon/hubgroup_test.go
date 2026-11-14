package daemon

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/sushidev-team/lola/hubproto"
	"github.com/sushidev-team/lola/internal/hub"
	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/scm"
	"github.com/sushidev-team/lola/internal/session"
	"github.com/sushidev-team/lola/internal/state"
)

func advisoryFor(id, pkg, adv string) hubproto.Item {
	it := advisoryItem(id)
	d := *it.DepAdvisory
	d.Package, d.Advisory = pkg, adv
	it.DepAdvisory = &d
	return it
}

// A repository's advisories go to ONE agent: every item is claimed and
// leased on its own, the session and briefing are shared, the daily cap is
// charged once, and each item completes when the one PR appears.
func TestHubGroupsARepositorysAdvisories(t *testing.T) {
	d, fh, nat := hubFixture(t)
	fh.offers = []hubproto.Item{
		advisoryFor("it1", "lodash", "GHSA-jf85-cpcp-j695"),
		advisoryFor("it2", "vite", "GHSA-587h-7wmj-h5pm"),
		advisoryFor("it3", "minimist", "GHSA-xvch-5gv4-984h"),
	}
	d.hubCycle(context.Background())

	got := fh.snapshot()
	if !slices.Equal(got.claims, []string{"it1", "it2", "it3"}) {
		t.Fatalf("claims = %v", got.claims)
	}
	if len(nat.manualAgents) != 1 {
		t.Fatalf("want one agent for the repository, got %d", len(nat.manualAgents))
	}
	call := nat.manualAgents[0]
	if !strings.HasPrefix(call.branch, "lola/hub-advisories-") {
		t.Errorf("branch = %q", call.branch)
	}
	for _, adv := range []string{"GHSA-jf85-cpcp-j695", "GHSA-587h-7wmj-h5pm", "GHSA-xvch-5gv4-984h", "ONE pull request"} {
		if !strings.Contains(call.prompt, adv) {
			t.Errorf("briefing lacks %q:\n%s", adv, call.prompt)
		}
	}
	ss := hubSessions(d)
	if len(ss) != 1 || len(ss[0].HubBatch) != 2 || ss[0].Title != "hub: 3 advisories in acme/widgets" {
		t.Fatalf("sessions = %+v", ss)
	}
	if n := d.hubClaimsToday(time.Now()); n != 1 {
		t.Errorf("claims today = %d, want 1 for one session", n)
	}

	d.hubCycle(context.Background())
	got = fh.snapshot()
	if lv := got.beats[1].LiveLeases; !slices.Equal(lv, []string{"it1", "it2", "it3"}) {
		t.Errorf("live leases = %v", lv)
	}
	if len(got.claims) != 3 {
		t.Errorf("re-claimed: %v", got.claims)
	}

	id := ss[0].ID
	d.sessions.Update(id, func(s *session.Session) bool {
		s.PR = &scm.PR{Number: 9, URL: "https://github.com/acme/widgets/pull/9", State: "OPEN"}
		s.SetDelivery(state.DeliveryCIPending, time.Now())
		return true
	})
	d.hubOutcomeCycle(context.Background())
	d.hubOutcomeCycle(context.Background())
	// The open PR puts every item in review (progress), none is done yet.
	if got := fh.snapshot(); len(got.progresses) != 3 || len(got.completes) != 0 {
		t.Fatalf("progresses = %d, completes = %d, want 3 / 0", len(got.progresses), len(got.completes))
	}
	d.sessions.Update(id, func(s *session.Session) bool {
		s.SetDelivery(state.DeliveryMerged, time.Now())
		return true
	})
	d.hubOutcomeCycle(context.Background())
	d.hubOutcomeCycle(context.Background())
	if got := fh.snapshot(); len(got.completes) != 3 {
		t.Fatalf("completes = %d, want one per item", len(got.completes))
	}
	s, _ := d.sessions.Get(id)
	for _, w := range s.HubWorks() {
		if w.Reported != "complete" {
			t.Errorf("%s guard = %q", w.ItemID, w.Reported)
		}
	}
}

func TestHubGroupingOffClaimsOneByOne(t *testing.T) {
	d, fh, nat := hubFixture(t)
	off := false
	d.cfg.Hub.GroupChores = &off
	fh.offers = []hubproto.Item{advisoryFor("it1", "lodash", "GHSA-jf85-cpcp-j695"), advisoryFor("it2", "vite", "GHSA-587h-7wmj-h5pm")}
	d.hubCycle(context.Background())
	if len(nat.manualAgents) != 2 {
		t.Fatalf("want one agent per advisory, got %d", len(nat.manualAgents))
	}
}

// A lost lease on one grouped item leaves the others renewed and reported.
func TestHubGroupLostLeaseIsPerItem(t *testing.T) {
	d, fh, _ := hubFixture(t)
	fh.offers = []hubproto.Item{advisoryFor("it1", "lodash", "GHSA-jf85-cpcp-j695"), advisoryFor("it2", "vite", "GHSA-587h-7wmj-h5pm")}
	d.hubCycle(context.Background())
	fh.mu.Lock()
	fh.lost = []string{"it2"}
	fh.claimErr = &hub.APIError{Status: 409, Code: hubproto.CodeConflict} // another device holds it
	fh.mu.Unlock()
	d.hubCycle(context.Background())
	s := hubSessions(d)[0]
	if s.HubItem.LeaseLost || !s.HubBatch[0].LeaseLost {
		t.Fatalf("lease state: %+v / %+v", s.HubItem, s.HubBatch)
	}
	d.dropSession(s)
	d.hubOutcomeCycle(context.Background())
	if got := fh.snapshot(); len(got.fails) != 1 {
		t.Fatalf("fails = %+v, want only the item still held", got.fails)
	}
}

// New advisories for a repository wait while its grouped session works.
func TestHubGroupWaitsForTheRunningSession(t *testing.T) {
	d, fh, nat := hubFixture(t)
	fh.offers = []hubproto.Item{advisoryFor("it1", "lodash", "GHSA-jf85-cpcp-j695"), advisoryFor("it2", "vite", "GHSA-587h-7wmj-h5pm")}
	d.hubCycle(context.Background())
	fh.mu.Lock()
	fh.offers = append(fh.offers, advisoryFor("it3", "minimist", "GHSA-xvch-5gv4-984h"), advisoryFor("it4", "devalue", "GHSA-x5rw-q4pp-hg5g"))
	fh.mu.Unlock()
	d.hubCycle(context.Background())
	if len(nat.manualAgents) != 1 {
		t.Fatalf("a second session started for the same repository: %d", len(nat.manualAgents))
	}
}

// A person's "update all dependencies" widens the grouped briefing with the
// chosen levels and their note.
func TestHubGroupClaimCarriesTheUpdateScope(t *testing.T) {
	d, fh, nat := hubFixture(t)
	items := []hubproto.Item{advisoryFor("it1", "lodash", "GHSA-jf85-cpcp-j695"), advisoryFor("it2", "vite", "GHSA-587h-7wmj-h5pm")}
	fh.offers = nil
	snap, ok := d.hubConfig()
	if !ok {
		t.Fatal("no hub config")
	}
	scope := &protocol.HubUpdateScope{Patch: true, Minor: true, Note: "Leave the PHP side alone."}
	if _, err := d.hubClaimGroup(context.Background(), fh, items, snap, hubClaimReq{human: true, scope: scope}); err != nil {
		t.Fatal(err)
	}
	prompt := nat.manualAgents[0].prompt
	for _, want := range []string{"GHSA-587h-7wmj-h5pm", "newest patch, minor release", "Leave the PHP side alone."} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt lacks %q:\n%s", want, prompt)
		}
	}
}

// A failed grouped session reports every item with ONE fail group, so the
// hub escalates them as one issue; the fail names the PR when one exists.
func TestHubGroupFailCarriesOneGroupAndThePR(t *testing.T) {
	d, fh, _ := hubFixture(t)
	fh.offers = []hubproto.Item{advisoryFor("it1", "lodash", "GHSA-jf85-cpcp-j695"), advisoryFor("it2", "vite", "GHSA-587h-7wmj-h5pm")}
	d.hubCycle(context.Background())
	s := hubSessions(d)[0]
	s.PR = &scm.PR{Number: 9, URL: "https://github.com/acme/widgets/pull/9", State: "CLOSED"}
	s.EnsureAxes()
	s.SetDelivery(state.DeliveryClosed, time.Now())
	d.sessions.Upsert(s)
	d.hubOutcomeCycle(context.Background())
	reqs := fh.snapshot().failReqs
	if len(reqs) != 2 {
		t.Fatalf("fails = %+v", reqs)
	}
	g := reqs[0].Group
	if !hubproto.ValidFailGroup(g) || reqs[1].Group != g {
		t.Errorf("fail groups = %q / %q, want one valid group", g, reqs[1].Group)
	}
	for _, r := range reqs {
		if r.Reason != hubproto.FailPRClosed || r.PRURL != "https://github.com/acme/widgets/pull/9" {
			t.Errorf("fail = %+v", r)
		}
	}
}

// A grouped spawn that fails reports spawn_failed per item, with the group.
func TestHubGroupSpawnFailureFailsEveryItem(t *testing.T) {
	d, fh, nat := hubFixture(t)
	nat.manualAgentErr = errors.New("worktree add failed")
	fh.offers = []hubproto.Item{advisoryFor("it1", "lodash", "GHSA-jf85-cpcp-j695"), advisoryFor("it2", "vite", "GHSA-587h-7wmj-h5pm")}
	d.hubCycle(context.Background())
	reqs := fh.snapshot().failReqs
	if len(reqs) != 2 || reqs[0].Reason != hubproto.FailSpawn || reqs[0].Group == "" || reqs[0].Group != reqs[1].Group {
		t.Fatalf("fails = %+v", reqs)
	}
	if !d.hubCoolingDown("it1", time.Now()) || !d.hubCoolingDown("it2", time.Now()) {
		t.Error("a failed spawn must cool both items down locally")
	}
}
