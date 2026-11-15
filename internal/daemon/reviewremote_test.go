package daemon

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/linear"
	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/scm"
	"github.com/sushidev-team/lola/internal/session"
)

// TestPhoneForcedReviewRunsDetached: a phone's forced review answers at once
// that it started, survives the connection's context being cancelled, refuses
// a second one while the first runs, and reports running on the row.
func TestPhoneForcedReviewRunsDetached(t *testing.T) {
	d := newTestDaemon(t, nativeTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeNative{})
	s := nativeSess("FE-1", "idle")
	d.sessions.Upsert(s)
	setProviders(d, claudeDesc())
	release := make(chan struct{})
	done := make(chan error, 1)
	d.runDetachedReview = func(ctx context.Context, id, kind string) (protocol.ReviewData, error) {
		<-release
		done <- ctx.Err()
		return protocol.ReviewData{Ran: true, Clean: true}, nil
	}
	conn, hangUp := context.WithCancel(context.WithValue(context.Background(), remoteCallKey{}, true))
	r := d.handle(conn, protocol.Request{Cmd: "review", Session: s.ID})
	var data protocol.ReviewData
	if err := json.Unmarshal(r.Data, &data); !r.OK || err != nil || !data.Started || data.Ran {
		t.Fatalf("not started detached: %+v %s", r, r.Data)
	}
	if !d.reviewRunningFor(s.ID) || !rowFor(t, d, s.ID).ReviewRunning {
		t.Fatal("row does not show the running review")
	}
	if r := d.handle(conn, protocol.Request{Cmd: "review", Session: s.ID}); r.OK || r.Error != errReviewRunning.Error() {
		t.Fatalf("second review not refused: %+v", r)
	}
	hangUp() // the app went to the background
	close(release)
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("the phone's hang-up cancelled the pass: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("detached pass never ran")
	}
	deadline := time.Now().Add(5 * time.Second)
	for d.reviewRunningFor(s.ID) {
		if time.Now().After(deadline) {
			t.Fatal("guard never released")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestDesktopForcedReviewStaysSynchronous: without the remote mark the review
// still runs on the request and answers with its outcome.
func TestDesktopForcedReviewStaysSynchronous(t *testing.T) {
	d := newTestDaemon(t, nativeTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeNative{})
	s := nativeSess("FE-1", "idle")
	d.sessions.Upsert(s)
	setProviders(d) // nothing enabled: answered synchronously as skipped
	d.runDetachedReview = func(context.Context, string, string) (protocol.ReviewData, error) {
		t.Fatal("desktop review was detached")
		return protocol.ReviewData{}, nil
	}
	r := d.handle(context.Background(), protocol.Request{Cmd: "review", Session: s.ID})
	var data protocol.ReviewData
	if err := json.Unmarshal(r.Data, &data); !r.OK || err != nil || data.Started || data.Skipped == "" {
		t.Fatalf("%+v %s", r, r.Data)
	}
	// A phone asking for a provider that cannot run is answered the same way.
	remote := context.WithValue(context.Background(), remoteCallKey{}, true)
	r = d.handle(remote, protocol.Request{Cmd: "review", Session: s.ID})
	if err := json.Unmarshal(r.Data, &data); !r.OK || err != nil || data.Started || data.Skipped == "" {
		t.Fatalf("phone: %+v %s", r, r.Data)
	}
}

// TestRowCarriesCurrentPRReviewTallies: only tallies for the current PR reach
// the row and the phone's projection, sorted by kind.
func TestRowCarriesCurrentPRReviewTallies(t *testing.T) {
	d := newTestDaemon(t, nativeTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeNative{})
	s := nativeSess("FE-1", "idle")
	s.PR = &scm.PR{Number: 7}
	at := time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)
	s.ReviewTallies = map[string]session.ReviewTally{
		"codex-session":  {PR: 7, Major: 2, Minor: 1, At: at},
		"claude-session": {PR: 7, At: at},
		"coderabbit-cli": {PR: 6, Blocker: 3, At: at},
	}
	d.sessions.Upsert(s)
	got := remoteSessionInfo(rowFor(t, d, s.ID)).ReviewTallies
	if len(got) != 2 || got[0].Kind != "claude-session" || got[0].Major+got[0].Minor+got[0].Blocker+got[0].Other != 0 || got[1].Kind != "codex-session" || got[1].Major != 2 || got[1].Minor != 1 || !got[1].At.Equal(at) {
		t.Fatalf("%+v", got)
	}
	s.PR = nil
	if reviewTallyInfos(s) != nil {
		t.Fatal("tallies without a PR")
	}
}

func rowFor(t *testing.T, d *Daemon, id string) protocol.SessionInfo {
	t.Helper()
	r := d.handle(context.Background(), protocol.Request{Cmd: "sessions"})
	var data protocol.SessionsData
	if err := json.Unmarshal(r.Data, &data); err != nil {
		t.Fatal(err)
	}
	for _, si := range data.Sessions {
		if si.ID == id {
			return si
		}
	}
	t.Fatalf("no row for %s", id)
	return protocol.SessionInfo{}
}

// TestDetachedReviewBudgetsTheWholeChain: the chain runs the primary and then
// each fallback in sequence, so the detached deadline covers their sum, with
// the default for a provider that names no timeout.
func TestDetachedReviewBudgetsTheWholeChain(t *testing.T) {
	d := newTestDaemon(t, nativeTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeNative{})
	s := nativeSess("FE-1", "idle")
	d.sessions.Upsert(s)
	primary := claudeDesc()
	primary.TimeoutSeconds = 900
	primary.Fallback = []provKind{provKind("codex-session"), "opencode-session"}
	fb := passDesc(provKind("codex-session"))
	fb.TimeoutSeconds = 600
	setProviders(d, primary, fb) // opencode-session is unknown: default timeout
	want := time.Duration(900+600+config.DefaultReviewTimeoutSeconds) * time.Second
	if got := d.reviewChainBudget(primary); got != want {
		t.Fatalf("budget %s, want %s", got, want)
	}
	deadline := make(chan time.Time, 1)
	d.runDetachedReview = func(ctx context.Context, id, kind string) (protocol.ReviewData, error) {
		dl, _ := ctx.Deadline()
		deadline <- dl
		return protocol.ReviewData{}, nil
	}
	before := time.Now()
	remote := context.WithValue(context.Background(), remoteCallKey{}, true)
	if r := d.handle(remote, protocol.Request{Cmd: "review", Session: s.ID}); !r.OK {
		t.Fatalf("%+v", r)
	}
	select {
	case dl := <-deadline:
		if dl.Before(before.Add(want + reviewDetachMargin)) {
			t.Fatalf("deadline %s covers less than the chain (%s)", dl.Sub(before), want+reviewDetachMargin)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("detached pass never ran")
	}
	d.drainConnWork()
}

// TestDetachedReviewEndsWithShutdown: daemon shutdown cancels a running
// detached pass, drainConnWork waits for it, and a pass asked for after the
// drain began is refused.
func TestDetachedReviewEndsWithShutdown(t *testing.T) {
	d := newTestDaemon(t, nativeTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeNative{})
	s := nativeSess("FE-1", "idle")
	d.sessions.Upsert(s)
	setProviders(d, claudeDesc())
	root, stop := context.WithCancel(context.Background())
	defer stop()
	d.shutdownCtx = root
	ended := make(chan struct{})
	d.runDetachedReview = func(ctx context.Context, id, kind string) (protocol.ReviewData, error) {
		<-ctx.Done()
		time.Sleep(50 * time.Millisecond) // the pass winds down after the cancel
		close(ended)
		return protocol.ReviewData{}, ctx.Err()
	}
	remote := context.WithValue(context.Background(), remoteCallKey{}, true)
	if r := d.handle(remote, protocol.Request{Cmd: "review", Session: s.ID}); !r.OK {
		t.Fatalf("%+v", r)
	}
	stop() // shutdown
	drained := make(chan struct{})
	go func() { d.drainConnWork(); close(drained) }()
	select {
	case <-drained:
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown did not drain the detached pass")
	}
	select {
	case <-ended:
	default:
		t.Fatal("drain returned before the pass ended")
	}
	if d.reviewRunningFor(s.ID) {
		t.Fatal("guard left set after shutdown")
	}
	if r := d.handle(remote, protocol.Request{Cmd: "review", Session: s.ID}); r.OK {
		t.Fatalf("pass started while draining: %+v", r)
	}
}

// TestDetachedReviewsAreCapped: at most maxDetachedReviews phone passes run
// daemon-wide, and a phone cannot stack a pass on a session whose worker or
// desktop pass is already running.
func TestDetachedReviewsAreCapped(t *testing.T) {
	d := newTestDaemon(t, nativeTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeNative{})
	setProviders(d, claudeDesc())
	release := make(chan struct{})
	d.runDetachedReview = func(ctx context.Context, id, kind string) (protocol.ReviewData, error) {
		<-release
		return protocol.ReviewData{}, nil
	}
	remote := context.WithValue(context.Background(), remoteCallKey{}, true)
	var ids []string
	for i := 1; i <= maxDetachedReviews+1; i++ {
		s := nativeSess("FE-"+string(rune('0'+i)), "idle")
		d.sessions.Upsert(s)
		ids = append(ids, s.ID)
	}
	for _, id := range ids[:maxDetachedReviews] {
		if r := d.handle(remote, protocol.Request{Cmd: "review", Session: id}); !r.OK {
			t.Fatalf("%s: %+v", id, r)
		}
	}
	last := ids[maxDetachedReviews]
	if r := d.handle(remote, protocol.Request{Cmd: "review", Session: last}); r.OK || r.Error != errReviewsBusy.Error() {
		t.Fatalf("cap not enforced: %+v", r)
	}
	close(release)
	d.drainConnWork()
	d.shutMu.Lock()
	d.draining = false // the drain above only waited for the passes
	d.shutMu.Unlock()

	// A worker or desktop pass on a lane of the session is running.
	d.reviewMu.Lock()
	d.reviewActive = map[string]int{last + "~web": 1}
	d.reviewMu.Unlock()
	if r := d.handle(remote, protocol.Request{Cmd: "review", Session: last}); r.OK || r.Error != errReviewRunning.Error() {
		t.Fatalf("phone pass stacked on a running one: %+v", r)
	}
}
