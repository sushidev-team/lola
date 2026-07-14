package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/sushidev-team/lola/internal/ao"
	"github.com/sushidev-team/lola/internal/linear"
	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/scm"
	"github.com/sushidev-team/lola/internal/session"
	"github.com/sushidev-team/lola/internal/tmux"
)

// fakeObsSeams installs counting fakes for the observer's scm and tmux seams.
// Nothing execs gh or tmux in these tests.
type fakeObsSeams struct {
	mu        sync.Mutex
	prCalls   []string // "repo|branch"
	pr        *scm.PR
	prErr     error
	tmuxCalls int
	tmuxNames []string
	tmuxErr   error
}

func (f *fakeObsSeams) install(d *Daemon) {
	d.prForBranch = func(ctx context.Context, repo, branch string) (*scm.PR, error) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.prCalls = append(f.prCalls, repo+"|"+branch)
		if f.prErr != nil {
			return nil, f.prErr
		}
		if f.pr == nil {
			return nil, nil
		}
		pr := *f.pr
		return &pr, nil
	}
	d.tmuxSessions = func(ctx context.Context) ([]tmux.Session, error) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.tmuxCalls++
		if f.tmuxErr != nil {
			return nil, f.tmuxErr
		}
		out := make([]tmux.Session, 0, len(f.tmuxNames))
		for _, n := range f.tmuxNames {
			out = append(out, tmux.Session{Name: n})
		}
		return out, nil
	}
}

func (f *fakeObsSeams) counts() (pr int, tm int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.prCalls), f.tmuxCalls
}

func findSession(t *testing.T, snap []session.Session, id string) session.Session {
	t.Helper()
	for _, s := range snap {
		if s.ID == id {
			return s
		}
	}
	t.Fatalf("no session %q in snapshot %+v", id, snap)
	return session.Session{}
}

// --- Full cycle: tick threads the branch, observe correlates everything ----

func TestObserveCycleBranchPRTmuxAndStatus(t *testing.T) {
	is := testIssue("FE-1", 1, "2024-01-01T00:00:00Z")
	is.BranchName = "feat/fe-1-do-thing"
	fake := &linear.Fake{Issues: []linear.Issue{is}}
	aoc := &fakeAO{}
	p := seenPoll("p1")
	p.Repo = "acme/widgets"
	d := newTestDaemon(t, testConfig(p), fake, aoc)
	seams := &fakeObsSeams{
		pr: &scm.PR{Number: 7, URL: "https://github.com/acme/widgets/pull/7",
			State: "OPEN", ChecksState: "fail", ReviewDecision: "REVIEW_REQUIRED"},
		tmuxNames: []string{"other", "ao-sess-1-FE-1"},
	}
	seams.install(d)
	ctx := context.Background()

	// A real tick records identifier→branch/repo for the observer.
	if _, err := d.tick(ctx, "p1", false); err != nil {
		t.Fatalf("tick: %v", err)
	}
	if b, r := d.branchFor("FE-1"); b != is.BranchName || r != "acme/widgets" {
		t.Fatalf("branch map after tick = (%q, %q), want (%q, acme/widgets)", b, r, is.BranchName)
	}

	aoc.mu.Lock()
	aoc.sessions = []ao.SessionState{
		{ID: "sess-1", Project: "proj", IssueID: "FE-1", Status: "working"},
		{ID: "sess-2", Project: "proj", IssueID: "FE-99", Status: "working"}, // no branch known
	}
	aoc.mu.Unlock()

	d.observe(ctx)

	snap := d.sessions.Snapshot()
	if len(snap) != 2 {
		t.Fatalf("snapshot has %d sessions, want 2: %+v", len(snap), snap)
	}
	s1 := findSession(t, snap, "sess-1")
	if s1.Source != "ao" || s1.Project != "proj" || s1.Issue != "FE-1" || s1.AOStatus != "working" {
		t.Errorf("sess-1 identity = %+v, want ao/proj/FE-1/working", s1)
	}
	if s1.Branch != is.BranchName {
		t.Errorf("sess-1 branch = %q, want %q (threaded from tick)", s1.Branch, is.BranchName)
	}
	if s1.PR == nil || s1.PR.Number != 7 {
		t.Fatalf("sess-1 PR = %+v, want number 7", s1.PR)
	}
	// DeriveStatus applied: alive + open PR with failing checks → ci_failed.
	if s1.Status != "ci_failed" {
		t.Errorf("sess-1 status = %q, want ci_failed", s1.Status)
	}
	// tmux correlation: name containing the AO session ID wins.
	if s1.TmuxName != "ao-sess-1-FE-1" {
		t.Errorf("sess-1 tmuxName = %q, want ao-sess-1-FE-1", s1.TmuxName)
	}

	// Session without a known branch: no PR lookup, working (alive, no PR).
	s2 := findSession(t, snap, "sess-2")
	if s2.Branch != "" || s2.PR != nil {
		t.Errorf("sess-2 must have no branch/PR, got %+v", s2)
	}
	if s2.Status != "working" {
		t.Errorf("sess-2 status = %q, want working", s2.Status)
	}
	prCalls, _ := seams.counts()
	if prCalls != 1 {
		t.Errorf("PR lookups = %d, want exactly 1 (only sessions with branch+repo)", prCalls)
	}

	// The snapshot is persisted for restarts.
	if _, err := os.Stat(filepath.Join(d.home, "state", "sessions.json")); err != nil {
		t.Errorf("observe must persist the store: %v", err)
	}
}

// --- Persisted facts survive a restart (and gh outages) ---------------------

// After a daemon restart the tick-fed branch map is empty, and in label mode
// the flipped trigger label keeps the issue out of tick queries forever — the
// persisted store record is the only source of Branch/Repo/PR facts left.
// The observer must fall back to it instead of clobbering the record with
// empties ("working" forever while the PR fails CI).
func TestObserveKeepsBranchAndPRFactsAcrossRestart(t *testing.T) {
	home := t.TempDir()
	t.Setenv("LOLA_HOME", home)
	aoState := []ao.SessionState{{ID: "sess-1", Project: "proj", IssueID: "FE-1", Status: "working"}}

	d1 := newDaemon(testConfig(seenPoll("p1")), &linear.Fake{}, &fakeAO{sessions: aoState}, log.New(io.Discard, "", 0), home)
	seams1 := &fakeObsSeams{pr: &scm.PR{Number: 7, URL: "u", State: "OPEN", ChecksState: "fail"}}
	seams1.install(d1)
	d1.recordBranch("FE-1", "feat/fe-1", "acme/widgets")
	d1.observe(context.Background())
	if s := findSession(t, d1.sessions.Snapshot(), "sess-1"); s.Status != "ci_failed" {
		t.Fatalf("precondition: status = %q, want ci_failed", s.Status)
	}

	// Restart: a fresh daemon on the same home. The branch map is empty and no
	// tick will refill it; on top, gh is down so the cycle has no fresh PR
	// facts either.
	d2 := newDaemon(testConfig(seenPoll("p1")), &linear.Fake{}, &fakeAO{sessions: aoState}, log.New(io.Discard, "", 0), home)
	seams2 := &fakeObsSeams{prErr: errors.New("gh wedged")}
	seams2.install(d2)
	d2.observe(context.Background())

	s := findSession(t, d2.sessions.Snapshot(), "sess-1")
	if s.Branch != "feat/fe-1" || s.Repo != "acme/widgets" {
		t.Errorf("branch/repo = %q/%q, want feat/fe-1 / acme/widgets restored from the store", s.Branch, s.Repo)
	}
	if s.PR == nil || s.PR.Number != 7 {
		t.Errorf("PR facts clobbered after restart: %+v", s.PR)
	}
	if s.Status != "ci_failed" {
		t.Errorf("status = %q, want ci_failed preserved (never flip to working on a blind cycle)", s.Status)
	}
	// The restored branch/repo drove a real lookup attempt.
	if pr, _ := seams2.counts(); pr != 1 {
		t.Errorf("PR lookups = %d, want 1 (branch+repo restored from the store)", pr)
	}

	// An AUTHORITATIVE answer (gh succeeded, found no PR) replaces the kept
	// facts — preservation must not turn into forever-stale state.
	seams2.mu.Lock()
	seams2.prErr = nil // fake now succeeds and returns pr == nil (no PR)
	seams2.mu.Unlock()
	d2.observe(context.Background())
	s = findSession(t, d2.sessions.Snapshot(), "sess-1")
	if s.PR != nil || s.Status != "working" {
		t.Errorf("authoritative no-PR answer must clear stale facts, got PR=%+v status=%q", s.PR, s.Status)
	}
}

// --- Every observer exec is deadline-bounded ---------------------------------

// deadlineAO wraps fakeAO and records whether observe handed its execs a
// context with a deadline (observe runs on WithoutCancel, so any deadline
// present was added per-call by observe itself).
type deadlineAO struct {
	*fakeAO
	reachableDL, sessionsDL *bool
}

func (a deadlineAO) Reachable(ctx context.Context) bool {
	_, *a.reachableDL = ctx.Deadline()
	return a.fakeAO.Reachable(ctx)
}

func (a deadlineAO) LiveSessions(ctx context.Context) ([]ao.SessionState, error) {
	_, *a.sessionsDL = ctx.Deadline()
	return a.fakeAO.LiveSessions(ctx)
}

func TestObserveExecsAreDeadlineBounded(t *testing.T) {
	var reachDL, sessDL, prDL, tmuxDL bool
	aoc := deadlineAO{
		fakeAO:      &fakeAO{sessions: []ao.SessionState{{ID: "s1", Project: "proj", IssueID: "FE-1", Status: "working"}}},
		reachableDL: &reachDL,
		sessionsDL:  &sessDL,
	}
	d := newTestDaemon(t, testConfig(seenPoll("p1")), &linear.Fake{}, aoc)
	d.prForBranch = func(ctx context.Context, repo, branch string) (*scm.PR, error) {
		_, prDL = ctx.Deadline()
		return nil, nil
	}
	d.tmuxSessions = func(ctx context.Context) ([]tmux.Session, error) {
		_, tmuxDL = ctx.Deadline()
		return nil, nil
	}
	d.recordBranch("FE-1", "feat/x", "acme/widgets")

	// context.Background has no deadline; a wedged gh/tmux/ao exec must never
	// be able to freeze the observer loop (or graceful shutdown) forever.
	d.observe(context.Background())

	if !reachDL {
		t.Error("ao Reachable must run under a per-exec deadline")
	}
	if !sessDL {
		t.Error("ao LiveSessions must run under a per-exec deadline")
	}
	if !prDL {
		t.Error("prForBranch must run under a per-exec deadline")
	}
	if !tmuxDL {
		t.Error("tmuxSessions must run under a per-exec deadline")
	}
}

// --- tmux correlation is boundary-aware ---------------------------------------

func TestTmuxNameMatches(t *testing.T) {
	cases := []struct {
		name, id string
		want     bool
	}{
		{"sess-1", "sess-1", true},
		{"ao-sess-1", "sess-1", true},
		{"ao-sess-1-FE-1", "sess-1", true},
		{"ao-sess-12", "sess-1", false}, // sess-1 is a substring of sess-12: no claim
		{"ao-sess-12", "sess-12", true},
		{"xsess-1", "sess-1", false},
		{"ao-sess-11-and-sess-1", "sess-1", true}, // later delimited occurrence still matches
		{"other", "sess-1", false},
		{"anything", "", false},
	}
	for _, c := range cases {
		if got := tmuxNameMatches(c.name, c.id); got != c.want {
			t.Errorf("tmuxNameMatches(%q, %q) = %v, want %v", c.name, c.id, got, c.want)
		}
	}
}

func TestObserveTmuxCorrelationIsBoundaryAware(t *testing.T) {
	aoc := &fakeAO{sessions: []ao.SessionState{
		{ID: "sess-1", Project: "proj", IssueID: "FE-1", Status: "working"},
		{ID: "sess-12", Project: "proj", IssueID: "FE-12", Status: "working"},
	}}
	d := newTestDaemon(t, testConfig(seenPoll("p1")), &linear.Fake{}, aoc)
	// Creation order puts sess-12's pane first in `tmux ls` — with substring
	// matching sess-1 would claim it and attach the user to the wrong agent.
	seams := &fakeObsSeams{tmuxNames: []string{"ao-sess-12", "ao-sess-1"}}
	seams.install(d)

	d.observe(context.Background())

	snap := d.sessions.Snapshot()
	if s := findSession(t, snap, "sess-1"); s.TmuxName != "ao-sess-1" {
		t.Errorf("sess-1 tmuxName = %q, want ao-sess-1 (never sess-12's pane)", s.TmuxName)
	}
	if s := findSession(t, snap, "sess-12"); s.TmuxName != "ao-sess-12" {
		t.Errorf("sess-12 tmuxName = %q, want ao-sess-12", s.TmuxName)
	}
}

// --- AO attention states outrank the PR-derived status ---------------------

func TestObserveAOAttentionStatusSurfacesVerbatim(t *testing.T) {
	for _, aoStatus := range []string{"needs_input", "no_signal"} {
		aoc := &fakeAO{sessions: []ao.SessionState{
			{ID: "s1", Project: "proj", IssueID: "FE-1", Status: aoStatus},
		}}
		d := newTestDaemon(t, testConfig(seenPoll("p1")), &linear.Fake{}, aoc)
		seams := &fakeObsSeams{pr: &scm.PR{Number: 1, State: "OPEN", ChecksState: "pending"}}
		seams.install(d)
		d.recordBranch("FE-1", "feat/x", "acme/widgets")

		d.observe(context.Background())

		s := findSession(t, d.sessions.Snapshot(), "s1")
		if s.Status != aoStatus {
			t.Errorf("status = %q, want AO's %q to outrank the PR-derived status", s.Status, aoStatus)
		}
		if s.AOStatus != aoStatus {
			t.Errorf("aoStatus = %q, want %q kept on the record", s.AOStatus, aoStatus)
		}
	}
}

// --- Merged AO status counts as not-alive for derivation --------------------

func TestObserveDeadAOSessionWithoutPRIsNoPR(t *testing.T) {
	aoc := &fakeAO{sessions: []ao.SessionState{
		{ID: "s1", Project: "proj", IssueID: "FE-1", Status: "merged"},
	}}
	d := newTestDaemon(t, testConfig(seenPoll("p1")), &linear.Fake{}, aoc)
	(&fakeObsSeams{}).install(d)

	d.observe(context.Background())

	if s := findSession(t, d.sessions.Snapshot(), "s1"); s.Status != "no_pr" {
		t.Errorf("status = %q, want no_pr (merged AO status is not alive, no PR known)", s.Status)
	}
}

// --- Failures never lose the cycle -----------------------------------------

func TestObservePRAndTmuxFailuresAreLoggedAndSkipped(t *testing.T) {
	aoc := &fakeAO{sessions: []ao.SessionState{
		{ID: "s1", Project: "proj", IssueID: "FE-1", Status: "working"},
	}}
	d := newTestDaemon(t, testConfig(seenPoll("p1")), &linear.Fake{}, aoc)
	seams := &fakeObsSeams{
		prErr:   errors.New("gh exploded"),
		tmuxErr: errors.New("no tmux here"),
	}
	seams.install(d)
	d.recordBranch("FE-1", "feat/x", "acme/widgets")

	d.observe(context.Background())

	s := findSession(t, d.sessions.Snapshot(), "s1")
	if s.PR != nil {
		t.Errorf("failed PR lookup must leave PR nil, got %+v", s.PR)
	}
	if s.Status != "working" {
		t.Errorf("status = %q, want working (alive, PR unknown)", s.Status)
	}
	if s.TmuxName != "" {
		t.Errorf("tmux failure must leave TmuxName empty, got %q", s.TmuxName)
	}
}

// --- AO down: skip silently -------------------------------------------------

func TestObserveAODownSkipsCycle(t *testing.T) {
	aoc := &fakeAO{unreachable: true}
	d := newTestDaemon(t, testConfig(seenPoll("p1")), &linear.Fake{}, aoc)
	seams := &fakeObsSeams{}
	seams.install(d)

	d.observe(context.Background())

	if len(d.sessions.Snapshot()) != 0 {
		t.Error("AO down: nothing must be upserted")
	}
	if pr, tm := seams.counts(); pr != 0 || tm != 0 {
		t.Errorf("AO down: no gh/tmux calls, got pr=%d tmux=%d", pr, tm)
	}
	if _, err := os.Stat(filepath.Join(d.home, "state", "sessions.json")); !os.IsNotExist(err) {
		t.Errorf("AO down: store must not be written, stat err = %v", err)
	}
}

// --- Retention prune ----------------------------------------------------------

func TestObservePrunesSessionsOlderThanRetention(t *testing.T) {
	home := t.TempDir()
	t.Setenv("LOLA_HOME", home)
	// Pre-seed the on-disk store with one stale and one fresh session before
	// the daemon (and its store) is constructed.
	old := time.Now().Add(-25 * time.Hour).Format(time.RFC3339)
	fresh := time.Now().Add(-time.Minute).Format(time.RFC3339)
	stateDir := filepath.Join(home, "state")
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	blob := `[
	  {"id":"stale","source":"ao","first_seen":"` + old + `","last_seen":"` + old + `"},
	  {"id":"fresh","source":"ao","first_seen":"` + fresh + `","last_seen":"` + fresh + `"}
	]`
	if err := os.WriteFile(filepath.Join(stateDir, "sessions.json"), []byte(blob), 0o600); err != nil {
		t.Fatal(err)
	}

	d := newDaemon(testConfig(seenPoll("p1")), &linear.Fake{}, &fakeAO{}, log.New(io.Discard, "", 0), home)
	(&fakeObsSeams{}).install(d)

	d.observe(context.Background())

	snap := d.sessions.Snapshot()
	if len(snap) != 1 || snap[0].ID != "fresh" {
		t.Fatalf("snapshot after prune = %+v, want only the fresh session", snap)
	}
}

// --- Branch map bounded -------------------------------------------------------

func TestObservePrunesBranchMapButKeepsLiveAndInflight(t *testing.T) {
	aoc := &fakeAO{sessions: []ao.SessionState{
		{ID: "s1", Project: "proj", IssueID: "FE-LIVE", Status: "working"},
	}}
	d := newTestDaemon(t, testConfig(seenPoll("p1")), &linear.Fake{}, aoc)
	(&fakeObsSeams{}).install(d)

	d.recordBranch("FE-LIVE", "feat/live", "acme/widgets")
	d.recordBranch("FE-INFLIGHT", "feat/inflight", "acme/widgets")
	d.recordBranch("FE-GONE", "feat/gone", "acme/widgets")
	d.inflight.Add("uuid-FE-INFLIGHT", "FE-INFLIGHT")
	// Backdate all entries past the mid-tick grace so this test exercises the
	// live/in-flight retention rules, not the freshness grace.
	backdateBranches(d, branchPruneGrace+time.Minute)

	d.observe(context.Background())

	if b, _ := d.branchFor("FE-LIVE"); b == "" {
		t.Error("branch for a live AO session must survive the prune")
	}
	if b, _ := d.branchFor("FE-INFLIGHT"); b == "" {
		t.Error("branch for an in-flight dispatch must survive the prune")
	}
	if b, _ := d.branchFor("FE-GONE"); b != "" {
		t.Errorf("branch absent from AO and in-flight must be pruned, got %q", b)
	}
}

// backdateBranches rewinds every branch map entry's recording time by age.
func backdateBranches(d *Daemon, age time.Duration) {
	d.branchMu.Lock()
	defer d.branchMu.Unlock()
	for ident, bi := range d.branches {
		bi.at = time.Now().Add(-age)
		d.branches[ident] = bi
	}
}

// A freshly recorded branch that is neither live in AO nor in-flight is
// exactly the state of an issue waiting later in a tick's dispatch queue
// (branches are recorded for ALL matches before the per-issue spawns run).
// An observer cycle firing mid-tick must not prune it.
func TestPruneBranchesGraceKeepsFreshEntries(t *testing.T) {
	d := newTestDaemon(t, testConfig(seenPoll("p1")), &linear.Fake{}, &fakeAO{})
	d.recordBranch("FE-QUEUED", "feat/queued", "acme/widgets")

	d.pruneBranches(map[string]bool{})
	if b, _ := d.branchFor("FE-QUEUED"); b == "" {
		t.Fatal("freshly recorded branch must survive a mid-tick prune (grace)")
	}

	backdateBranches(d, branchPruneGrace+time.Minute)
	d.pruneBranches(map[string]bool{})
	if b, _ := d.branchFor("FE-QUEUED"); b != "" {
		t.Errorf("stale unclaimed branch must still be pruned, got %q", b)
	}
}

func TestTickDryRunDoesNotRecordBranches(t *testing.T) {
	is := testIssue("FE-1", 1, "2024-01-01T00:00:00Z")
	is.BranchName = "feat/fe-1"
	fake := &linear.Fake{Issues: []linear.Issue{is}}
	p := seenPoll("p1")
	p.Repo = "acme/widgets"
	d := newTestDaemon(t, testConfig(p), fake, &fakeAO{})

	if _, err := d.tick(context.Background(), "p1", true); err != nil {
		t.Fatalf("dry-run tick: %v", err)
	}
	if b, _ := d.branchFor("FE-1"); b != "" {
		t.Errorf("dry run must not record branches, got %q", b)
	}
}

// --- sessions cmd serves the cache -------------------------------------------

func TestSessionsCmdServesCacheWithoutLiveExec(t *testing.T) {
	aoc := &fakeAO{sessions: []ao.SessionState{
		{ID: "s1", Project: "proj", IssueID: "FE-1", Status: "working"},
	}}
	d := newTestDaemon(t, testConfig(seenPoll("p1")), &linear.Fake{}, aoc)
	seams := &fakeObsSeams{
		pr: &scm.PR{Number: 12, URL: "https://github.com/acme/widgets/pull/12",
			State: "OPEN", ChecksState: "pass", ReviewDecision: "APPROVED"},
		tmuxNames: []string{"lola-s1"},
	}
	seams.install(d)
	d.recordBranch("FE-1", "feat/fe-1", "acme/widgets")
	d.observe(context.Background())
	prBefore, tmBefore := seams.counts()

	resp := d.handle(context.Background(), protocol.Request{Cmd: "sessions"})
	if !resp.OK {
		t.Fatalf("sessions cmd failed: %s", resp.Error)
	}
	var data protocol.SessionsData
	if err := json.Unmarshal(resp.Data, &data); err != nil {
		t.Fatalf("bad sessions data: %v", err)
	}
	if len(data.Sessions) != 1 {
		t.Fatalf("sessions = %+v, want 1", data.Sessions)
	}
	si := data.Sessions[0]
	if si.ID != "s1" || si.Project != "proj" || si.Issue != "FE-1" || si.Branch != "feat/fe-1" {
		t.Errorf("session info identity = %+v", si)
	}
	if si.PRNumber != 12 || si.PRURL == "" || si.Checks != "pass" || si.Review != "APPROVED" {
		t.Errorf("session info PR fields = %+v", si)
	}
	if si.Status != "approved" {
		t.Errorf("status = %q, want approved (alive + APPROVED + pass)", si.Status)
	}
	if si.TmuxName != "lola-s1" {
		t.Errorf("tmuxName = %q, want lola-s1", si.TmuxName)
	}
	if si.Age == "" {
		t.Error("age must be rendered")
	}

	// Serving the cache must not have touched gh or tmux again.
	if prAfter, tmAfter := seams.counts(); prAfter != prBefore || tmAfter != tmBefore {
		t.Errorf("sessions cmd must serve the cache: gh %d→%d tmux %d→%d",
			prBefore, prAfter, tmBefore, tmAfter)
	}
	aoc.mu.Lock()
	aoc.unreachable = true // even with AO down the cache still answers
	aoc.mu.Unlock()
	if resp := d.handle(context.Background(), protocol.Request{Cmd: "sessions"}); !resp.OK {
		t.Errorf("sessions cmd must answer from cache with AO down: %s", resp.Error)
	}
}

// --- Shutdown stops the loop --------------------------------------------------

func TestObserveLoopStopsOnShutdown(t *testing.T) {
	d := newTestDaemon(t, testConfig(seenPoll("p1")), &linear.Fake{}, &fakeAO{unreachable: true})
	(&fakeObsSeams{}).install(d)

	ctx, cancel := context.WithCancel(context.Background())
	d.wg.Add(1)
	go d.observeLoop(ctx)
	cancel()

	done := make(chan struct{})
	go func() {
		d.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("observeLoop did not stop on shutdown (goroutine leak)")
	}
}

// --- Age formatting -------------------------------------------------------------

func TestFormatAge(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{-time.Second, "0s"},
		{42 * time.Second, "42s"},
		{12 * time.Minute, "12m"},
		{3*time.Hour + 5*time.Minute, "3h05m"},
		{50 * time.Hour, "2d2h"},
	}
	for _, c := range cases {
		if got := formatAge(c.d); got != c.want {
			t.Errorf("formatAge(%v) = %q, want %q", c.d, got, c.want)
		}
	}
}
