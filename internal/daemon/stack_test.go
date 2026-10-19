package daemon

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/sushidev-team/lola/internal/linear"
	"github.com/sushidev-team/lola/internal/scm"
	"github.com/sushidev-team/lola/internal/session"
	"github.com/sushidev-team/lola/internal/state"
)

// stackSess is a stack session of p1 with two apps on the session's branch.
func stackSess(ident string) session.Session {
	s := nativeSess(ident, "working")
	s.Stack, s.Repo = true, ""
	s.IssueUUID = "uuid-" + strings.ToLower(ident)
	s.Worktree = "/wt/p1/" + s.ID
	s.AgentState, s.Delivery = state.AgentIdle, state.DeliveryNone
	s.Apps = []session.App{
		{Name: "app", Repo: "acme/app", Branch: s.Branch, Worktree: s.Worktree + "/app", Base: "main"},
		{Name: "web", Repo: "acme/web", Branch: s.Branch, Worktree: s.Worktree + "/web", Base: "develop"},
	}
	return s
}

func TestObserveAppsFetchesEachPRAndKeepsLastFactsOnFailure(t *testing.T) {
	d := newTestDaemon(t, nativeTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeNative{})
	web := &scm.PR{Number: 9, State: "OPEN", ChecksState: "fail"}
	d.prForBranch = func(_ context.Context, repo, _ string) (*scm.PR, error) {
		switch repo {
		case "acme/web":
			return web, nil
		case "acme/app":
			return nil, errors.New("gh: 502")
		}
		t.Fatalf("unexpected repo %s", repo)
		return nil, nil
	}
	d.changedFiles = func(context.Context, string, string) ([]string, error) { return []string{"x.go"}, nil }
	s := stackSess("FE-1")
	s.Apps[0].PR = &scm.PR{Number: 3, State: "OPEN"}
	snapshot := s.Apps
	facts := d.observeApps(context.Background(), s)
	cur := s
	if !applyApps(&cur, facts, time.Now()) {
		t.Fatal("a new app PR must count as a change")
	}
	if snapshot[1].PR != nil {
		t.Fatal("apply mutated a shared snapshot in place")
	}
	app, webApp := cur.Apps[0], cur.Apps[1]
	if app.PR.Number != 3 || app.PRFetchFailures != 1 {
		t.Errorf("a failed fetch must keep the last facts and count: %+v", app)
	}
	if webApp.PR != web || webApp.Delivery != state.DeliveryCIFailed {
		t.Errorf("web app = %+v", webApp)
	}
	// The session's own axis is the most urgent app's, and its PR fields are
	// that app's.
	if cur.Delivery != state.DeliveryCIFailed || cur.PR.Number != 9 || cur.Repo != "acme/web" || cur.FocusApp() != "web" {
		t.Errorf("aggregate = %s PR %+v repo %q", cur.Delivery, cur.PR, cur.Repo)
	}
}

func TestStackReactsPerAppAndNamesTheCheckout(t *testing.T) {
	d := newTestDaemon(t, reactTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeNative{})
	seams := &fakeReactSeams{failing: "WEB-CI-LOG"}
	seams.install(d)

	s := stackSess("FE-1")
	s.AtPrompt = true
	s.Apps[0].PR, s.Apps[0].Delivery = openPR(3, "MERGEABLE", "", "pass"), state.DeliveryReviewPending
	s.Apps[1].PR, s.Apps[1].Delivery = openPR(9, "MERGEABLE", "", "fail"), state.DeliveryCIFailed
	s.Reaggregate(time.Now())
	d.sessions.Upsert(s)
	cur, _ := d.sessions.Get(s.ID)
	d.react(context.Background(), cur)

	calls := seams.sendCalls()
	if len(calls) != 1 {
		t.Fatalf("want one send (the web app's CI), got %d", len(calls))
	}
	if calls[0].name != s.TmuxName {
		t.Errorf("sent to %q, want the session's one pane %q", calls[0].name, s.TmuxName)
	}
	for _, must := range []string{"`web/` checkout", "acme/web#9", "WEB-CI-LOG"} {
		if !strings.Contains(calls[0].text, must) {
			t.Errorf("reaction text missing %q:\n%s", must, calls[0].text)
		}
	}
	got, _ := d.sessions.Get(s.ID)
	if got.AtPrompt {
		t.Error("the session's gate must be consumed")
	}
	web := got.Apps[got.AppIndex("web")]
	if web.LastReactedStatus != "ci_failed" || web.CIRetries != 1 {
		t.Errorf("web lane guards = %+v", web)
	}
	if got.Apps[got.AppIndex("app")].LastReactedStatus != "" || got.LastReactedStatus != "" {
		t.Error("the reaction's guard must live on the web lane only")
	}
}

func TestStackCleanupWaitsForEveryApp(t *testing.T) {
	nat := &fakeNative{}
	d := newTestDaemon(t, reactTestConfig(nativePoll("p1")), &linear.Fake{}, nat)
	(&fakeReactSeams{}).install(d)

	s := stackSess("FE-1")
	merged := &scm.PR{Number: 3, State: "MERGED"}
	s.Apps[0].PR, s.Apps[0].Delivery = merged, state.DeliveryMerged
	s.Apps[1].Touched = true // web has changes but no PR yet
	s.Reaggregate(time.Now())
	if s.Delivery == state.DeliveryMerged {
		t.Fatal("an app with unshipped changes must keep the stack open")
	}
	d.sessions.Upsert(s)
	d.react(context.Background(), s)
	if calls := nat.killCalls(); len(calls) != 0 {
		t.Fatalf("cleanup ran while an app still has work: %+v", calls)
	}

	// Web shipped too: merged, and confirmed by a lookup after the merge.
	s.Apps[1].Touched = false
	s.Apps[1].PR, s.Apps[1].Delivery = &scm.PR{Number: 8, State: "MERGED"}, state.DeliveryMerged
	s.Reaggregate(time.Now())
	after := s.DeliverySince.Add(time.Second)
	s.Apps[0].PRObservedAt, s.Apps[1].PRObservedAt = after, after
	d.sessions.Upsert(s)
	d.react(context.Background(), s)
	if calls := nat.killCalls(); len(calls) != 1 {
		t.Fatalf("cleanup did not run once every app merged: %+v", calls)
	}
}

func TestStackReviewsQueuePerAppLane(t *testing.T) {
	d := newTestDaemon(t, nativeTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeNative{})
	s := stackSess("FE-1")
	s.Apps[0].PR = openPR(3, "MERGEABLE", "", "pass")
	s.Apps[1].PR = openPR(9, "MERGEABLE", "", "pass")
	d.sessions.Upsert(s)
	for _, lane := range s.Lanes() {
		v, ok := d.sessions.Get(lane.ID)
		if !ok || v.Lane != lane.Lane || v.PR == nil || v.Worktree != lane.Worktree {
			t.Fatalf("lane %s does not resolve to its app: %+v", lane.ID, v)
		}
	}
	// A job queued for a lane runs against that app: its worktree and base.
	web, _ := d.sessions.Get(session.LaneID(s.ID, "web"))
	if base := d.laneBase(web); base != "develop" {
		t.Errorf("web lane base = %q, want develop", base)
	}
}

func TestStackAppsAndFocusOnTheWire(t *testing.T) {
	d := newTestDaemon(t, nativeTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeNative{})
	s := stackSess("FE-1")
	s.Apps[1].PR, s.Apps[1].Delivery = openPR(9, "MERGEABLE", "", "fail"), state.DeliveryCIFailed
	s.Apps[0].Touched = true
	s.Reaggregate(time.Now())
	d.sessions.Upsert(s)
	for _, got := range d.sessionsData().Sessions {
		if got.ID != s.ID {
			continue
		}
		if !got.Stack || got.FocusApp != "web" || got.PRNumber != 9 || len(got.Apps) != 2 {
			t.Fatalf("wire = %+v", got)
		}
		if got.Apps[0].Name != "app" || !got.Apps[0].Touched || got.Apps[1].Number != 9 || got.Apps[1].Delivery != "ci_failed" {
			t.Errorf("apps = %+v", got.Apps)
		}
		return
	}
	t.Fatal("stack session missing from the wire")
}
