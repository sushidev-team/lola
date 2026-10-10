package daemon

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/gitdiff"
	"github.com/sushidev-team/lola/internal/linear"
	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/scm"
	"github.com/sushidev-team/lola/internal/session"
	"github.com/sushidev-team/lola/internal/state"
	"github.com/sushidev-team/lola/internal/worktree"
)

func bestOfConfig(mix ...string) *config.Config {
	cfg := testConfig(labelPoll("p1"))
	cfg.BestOf = config.BestOfConfig{Label: "lbl-bestof", Agents: mix}
	return cfg
}

func bestOfIssue(ident string, labels ...string) linear.Issue {
	is := testIssue(ident, 1, "2024-01-01T00:00:00Z")
	is.LabelIDs = labels
	return is
}

// A poll match carrying [best_of].label spawns the whole mix as one group:
// one session per attempt (agent + model), deduped ONCE (one label flip), with
// the flip's stripped labels recorded on every attempt.
func TestTickBestOfLabelSpawnsTheMix(t *testing.T) {
	is := bestOfIssue("FE-7", "lbl-trigger", "lbl-bestof")
	fake := &linear.Fake{Issues: []linear.Issue{is}, LabelIDsByIssue: map[string][]string{is.ID: {"lbl-trigger", "lbl-bestof"}}}
	nat := &fakeNative{}
	d := newTestDaemon(t, bestOfConfig("claude", "codex@gpt-5-codex"), fake, nat)

	res, err := d.tick(context.Background(), "p1", false)
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	if m := findMatch(t, res, "FE-7"); m.Action != "spawned" {
		t.Fatalf("match = %+v, want spawned", m)
	}
	opts := nat.spawnOptsCalls()
	if len(opts) != 2 {
		t.Fatalf("spawns = %+v, want one per attempt", opts)
	}
	if opts[0].Group == "" || opts[0].Group != opts[1].Group {
		t.Errorf("attempts must share one group: %+v", opts)
	}
	if opts[0].Agent != "claude" || opts[1].Agent != "codex" || opts[1].Model != "gpt-5-codex" || opts[1].Attempt != 2 || opts[1].Of != 2 {
		t.Errorf("attempt opts = %+v", opts)
	}
	if n := countCalls(fake.CallNames(), "SetIssueLabels"); n != 1 {
		t.Errorf("label flip fired %d times, want once for the whole group", n)
	}
	for _, id := range []string{"lola-p1-fe-7-b1", "lola-p1-fe-7-b2"} {
		s, ok := d.sessions.Get(id)
		if !ok {
			t.Fatalf("attempt %s missing from the store", id)
		}
		if s.BestOf != opts[0].Group || s.PollName != "p1" || !reflect.DeepEqual(s.RemovedLabels, []string{"lbl-trigger"}) {
			t.Errorf("attempt %s = %+v", id, s)
		}
	}
}

// A best-of issue is charged one slot per attempt, and the group must fit
// whole; an ordinary issue still dispatches into the remaining budget.
func TestTickBestOfChargesTheBudget(t *testing.T) {
	bo := bestOfIssue("FE-1", "lbl-trigger", "lbl-bestof")
	plain := bestOfIssue("FE-2", "lbl-trigger")
	plain.Priority = 2
	fake := &linear.Fake{Issues: []linear.Issue{bo, plain}}
	nat := &fakeNative{}
	cfg := bestOfConfig("claude:3")
	cfg.Projects[0].ConcurrencyCap = 2
	d := newTestDaemon(t, cfg, fake, nat)

	res, err := d.tick(context.Background(), "p1", false)
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	if m := findMatch(t, res, "FE-1"); m.Action != "skipped" || !strings.Contains(m.Reason, "best-of needs 3") {
		t.Errorf("FE-1 = %+v, want capped (needs 3)", m)
	}
	if m := findMatch(t, res, "FE-2"); m.Action != "spawned" {
		t.Errorf("FE-2 = %+v, want spawned into the remaining budget", m)
	}
	if got := nat.spawnOptsCalls(); len(got) != 1 || got[0].Group != "" {
		t.Errorf("spawns = %+v, want only the plain issue", got)
	}
}

// One agent of the mix failing its health gate holds the whole group back
// before anything is mutated — no claim, no seen, no spawn.
func TestTickBestOfHealthGatesEveryAgent(t *testing.T) {
	is := bestOfIssue("FE-7", "lbl-trigger", "lbl-bestof")
	nat := &fakeNative{}
	d := newTestDaemon(t, bestOfConfig("claude", "codex"), &linear.Fake{Issues: []linear.Issue{is}}, nat)
	d.runtimeHealth = func(bin string) error {
		if bin == "codex" {
			return errors.New("missing codex")
		}
		return nil
	}
	res, err := d.tick(context.Background(), "p1", false)
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	if m := findMatch(t, res, "FE-7"); m.Action != "skipped" {
		t.Errorf("match = %+v, want skipped", m)
	}
	if len(nat.spawnOptsCalls()) != 0 || d.inflight.Has(is.ID) {
		t.Error("a held group must touch nothing")
	}
}

// An attempt that fails to spawn leaves the others running; the group is
// smaller, the issue still counts as dispatched.
func TestTickBestOfPartialSpawn(t *testing.T) {
	is := bestOfIssue("FE-7", "lbl-trigger", "lbl-bestof")
	nat := &fakeNative{attemptErr: map[int]error{2: errors.New("worktree add failed")}}
	d := newTestDaemon(t, bestOfConfig("claude:3"), &linear.Fake{Issues: []linear.Issue{is}}, nat)
	if _, err := d.tick(context.Background(), "p1", false); err != nil {
		t.Fatalf("tick: %v", err)
	}
	var ids []string
	for _, s := range d.sessions.Snapshot() {
		ids = append(ids, s.ID)
	}
	if want := []string{"lola-p1-fe-7-b1", "lola-p1-fe-7-b3"}; !reflect.DeepEqual(ids, want) {
		t.Errorf("sessions = %v, want %v", ids, want)
	}
	if !d.inflight.Has(is.ID) {
		t.Error("a partially spawned group is still a dispatched issue")
	}
}

// Without the label (or without a configured label) nothing changes.
func TestTickBestOfOnlyWithTheLabel(t *testing.T) {
	is := bestOfIssue("FE-7", "lbl-trigger")
	nat := &fakeNative{}
	d := newTestDaemon(t, bestOfConfig("claude:2"), &linear.Fake{Issues: []linear.Issue{is}}, nat)
	if _, err := d.tick(context.Background(), "p1", false); err != nil {
		t.Fatalf("tick: %v", err)
	}
	if got := nat.spawnOptsCalls(); len(got) != 1 || got[0].Group != "" {
		t.Errorf("spawns = %+v, want one plain spawn", got)
	}
}

func bestOfDetail(ident string) linear.IssueDetail {
	return linear.IssueDetail{ID: "uuid-" + ident, Identifier: ident, Title: "title " + ident, TeamID: "team-1", LabelIDs: []string{"lbl-trigger"}}
}

func TestHandleBestOfStartsAGroup(t *testing.T) {
	fake := &linear.Fake{Details: []linear.IssueDetail{bestOfDetail("FE-9")}, LabelIDsByIssue: map[string][]string{"uuid-FE-9": {"lbl-trigger"}}}
	nat := &fakeNative{}
	d := newTestDaemon(t, testConfig(labelPoll("p1")), fake, nat)

	data, err := d.handleBestOf(context.Background(), protocol.BestOfArgs{Issue: "FE-9", Agents: "claude@opus,opencode"})
	if err != nil {
		t.Fatalf("handleBestOf: %v", err)
	}
	if len(data.Sessions) != 2 || data.Group == "" || data.Issue != "FE-9" {
		t.Fatalf("data = %+v", data)
	}
	if !d.inflight.Has("uuid-FE-9") {
		t.Error("the issue must be claimed in-flight")
	}
	if got := fake.LabelIDsByIssue["uuid-FE-9"]; !reflect.DeepEqual(got, []string{"lbl-sent"}) {
		t.Errorf("labels = %v, want the poll's flip", got)
	}
	// A second start of the same issue is refused (it is being worked on).
	if _, err := d.handleBestOf(context.Background(), protocol.BestOfArgs{Issue: "FE-9", Agents: "claude:2"}); err == nil {
		t.Error("a second best-of of a live issue must be refused")
	}
}

// The whole group must fit under global_cap, checked before anything happens.
func TestHandleBestOfRefusesPastGlobalCap(t *testing.T) {
	fake := &linear.Fake{Details: []linear.IssueDetail{bestOfDetail("FE-9")}}
	nat := &fakeNative{}
	cfg := testConfig(labelPoll("p1"))
	cfg.Defaults.GlobalCap = 2
	d := newTestDaemon(t, cfg, fake, nat)
	d.sessions.Upsert(nativeSess("FE-1", "working"))

	_, err := d.handleBestOf(context.Background(), protocol.BestOfArgs{Issue: "FE-9", Agents: "claude:2"})
	if err == nil || !strings.Contains(err.Error(), "global_cap") {
		t.Fatalf("err = %v, want a global_cap refusal", err)
	}
	if len(nat.spawnOptsCalls()) != 0 || d.inflight.Has("uuid-FE-9") {
		t.Error("a refused start must touch nothing")
	}
}

func TestHandleBestOfRejectsBadMix(t *testing.T) {
	d := newTestDaemon(t, testConfig(labelPoll("p1")), &linear.Fake{}, &fakeNative{})
	if _, err := d.handleBestOf(context.Background(), protocol.BestOfArgs{Issue: "FE-9", Agents: "claude"}); err == nil {
		t.Fatal("a one-attempt mix is not best-of")
	}
}

// seedGroup puts a three-attempt group for FE-5 in the store.
func seedGroup(d *Daemon) []session.Session {
	var out []session.Session
	for i, ag := range []string{"claude", "codex", "claude"} {
		s := nativeSess("FE-5", "working")
		s.ID = s.ID + "-b" + string(rune('1'+i))
		s.TmuxName = s.ID
		s.Branch += "-b" + string(rune('1'+i))
		s.IssueUUID = "uuid-FE-5"
		s.Agent = ag
		s.BestOf, s.Attempt, s.Attempts = "fe-5-grp", i+1, 3
		s.SetAgentState(state.AgentWorking, "", s.LastSeen)
		out = append(out, s)
	}
	out[0].PR = &scm.PR{Number: 11, URL: "https://github.com/acme/widgets/pull/11", State: "OPEN", IsDraft: true, ChecksState: "fail"}
	out[1].PR = &scm.PR{Number: 12, URL: "https://github.com/acme/widgets/pull/12", State: "OPEN", IsDraft: true, ChecksState: "pass"}
	out[1].ReviewTallies = map[string]session.ReviewTally{
		"claude-session": {PR: 12, Minor: 1},
		"codex-session":  {PR: 3, Blocker: 9}, // an EARLIER PR: not counted
	}
	for _, s := range out {
		d.sessions.Upsert(s)
	}
	return out
}

func TestBestOfCompare(t *testing.T) {
	d := newTestDaemon(t, testConfig(labelPoll("p1")), &linear.Fake{}, &fakeNative{})
	g := seedGroup(d)
	d.worktreeStat = func(_ context.Context, dir, base string) (gitdiff.Stat, error) {
		if base != "main" {
			t.Errorf("base = %q, want the project's default branch", base)
		}
		if strings.HasSuffix(dir, "-b3") {
			return gitdiff.Stat{}, errors.New("no merge-base")
		}
		return gitdiff.Stat{Files: 2, Additions: 30, Deletions: 4}, nil
	}

	data, err := d.handleBestOfCompare(context.Background(), protocol.BestOfCompareArgs{Issue: "fe-5"})
	if err != nil {
		t.Fatalf("compare: %v", err)
	}
	if data.Group != "fe-5-grp" || len(data.Attempts) != 3 {
		t.Fatalf("data = %+v", data)
	}
	a := data.Attempts
	if a[0].Attempt != 1 || a[1].Agent != "codex" || a[1].Checks != "pass" || !a[1].Draft || a[0].Files != 2 || a[0].Additions != 30 {
		t.Errorf("attempt facts = %+v", a)
	}
	if a[2].DiffError == "" {
		t.Error("an unreadable diff must be reported, not shown as zero")
	}
	if r := a[1].Review; r == nil || r.Passes != 1 || r.Blocker != 0 || r.Minor != 1 {
		t.Errorf("review tally = %+v, want only the current PR's pass", r)
	}
	if data.Suggested != g[1].ID {
		t.Errorf("suggested = %q, want the green attempt %q", data.Suggested, g[1].ID)
	}
	// The same group resolves from one of its sessions.
	if bySess, err := d.handleBestOfCompare(context.Background(), protocol.BestOfCompareArgs{Session: g[2].ID}); err != nil || bySess.Group != "fe-5-grp" {
		t.Errorf("by session = %+v, %v", bySess.Group, err)
	}
}

type prWrites struct {
	mu     sync.Mutex
	ready  []int
	closed []int
}

func (w *prWrites) install(d *Daemon) {
	d.markPRReady = func(_ context.Context, repo string, pr int) error {
		w.mu.Lock()
		defer w.mu.Unlock()
		w.ready = append(w.ready, pr)
		return nil
	}
	d.closePR = func(_ context.Context, repo string, pr int, comment string) error {
		w.mu.Lock()
		defer w.mu.Unlock()
		if !strings.Contains(comment, "best-of pick") {
			return errors.New("unexpected comment " + comment)
		}
		w.closed = append(w.closed, pr)
		return nil
	}
}

// A pick keeps one attempt: winner stamped, its draft PR marked ready, every
// other PR closed and every other session torn down dirty-safe — while the
// issue's in-flight claim and labels stay with the winner.
func TestBestOfPick(t *testing.T) {
	fake := &linear.Fake{}
	nat := &fakeNative{}
	d := newTestDaemon(t, testConfig(labelPoll("p1")), fake, nat)
	g := seedGroup(d)
	d.inflight.Add("uuid-FE-5", "FE-5")
	w := &prWrites{}
	w.install(d)

	res, err := d.handleBestOfPick(context.Background(), protocol.BestOfPickArgs{Session: g[1].ID})
	if err != nil {
		t.Fatalf("pick: %v", err)
	}
	if res.Winner != g[1].ID || res.PR != 12 || !reflect.DeepEqual(w.ready, []int{12}) || !reflect.DeepEqual(w.closed, []int{11}) {
		t.Errorf("res = %+v ready=%v closed=%v", res, w.ready, w.closed)
	}
	if want := []string{g[0].ID, g[2].ID}; !reflect.DeepEqual(res.Removed, want) {
		t.Errorf("removed = %v, want %v", res.Removed, want)
	}
	kills := nat.killCalls()
	if len(kills) != 2 || kills[0].force || !kills[0].removeWorktree {
		t.Errorf("kills = %+v, want two dirty-safe teardowns", kills)
	}
	if cur, ok := d.sessions.Get(g[1].ID); !ok || !cur.BestOfWinner || cur.InBestOfRace() {
		t.Errorf("winner = %+v", cur)
	}
	if _, ok := d.sessions.Get(g[0].ID); ok {
		t.Error("a discarded clean attempt must leave the store")
	}
	if !d.inflight.Has("uuid-FE-5") {
		t.Error("discarding the losers must not free the winner's issue")
	}
	if slices.Contains(fake.CallNames(), "SetIssueLabels") {
		t.Error("discarding the losers must not strip the issue's labels")
	}
	if _, err := d.handleBestOfPick(context.Background(), protocol.BestOfPickArgs{Session: g[1].ID}); err == nil {
		t.Error("a group is picked once")
	}
}

// A loser with uncommitted work keeps its worktree and record (dead).
func TestBestOfPickKeepsDirtyLoser(t *testing.T) {
	nat := &fakeNative{killErr: worktree.ErrDirty}
	d := newTestDaemon(t, testConfig(labelPoll("p1")), &linear.Fake{}, nat)
	g := seedGroup(d)
	(&prWrites{}).install(d)

	res, err := d.handleBestOfPick(context.Background(), protocol.BestOfPickArgs{Session: g[0].ID})
	if err != nil {
		t.Fatalf("pick: %v", err)
	}
	if len(res.Kept) != 2 || len(res.Removed) != 0 {
		t.Fatalf("res = %+v, want both losers kept", res)
	}
	if cur, ok := d.sessions.Get(g[2].ID); !ok || cur.AgentState != state.AgentDead {
		t.Errorf("kept loser = %+v, want on record and dead", cur)
	}
}

// Until a pick, an attempt's PR-open write-back is held; after it, the
// winner's fires on the next cycle.
func TestWriteBackHeldDuringBestOfRace(t *testing.T) {
	p := labelPoll("p1")
	p.OnPRStateID = "st-review"
	p.CommentOnPR = true
	fake := &linear.Fake{}
	nat := &fakeNative{}
	d := newTestDaemon(t, testConfig(p), fake, nat)
	seams := &fakeObsSeams{pr: openPR(9, "MERGEABLE", "", "pending")}
	seams.install(d)

	s := wbObserveSess(d, "FE-9", "p1")
	d.sessions.Update(s.ID, func(cur *session.Session) bool {
		cur.BestOf, cur.Attempt, cur.Attempts = "fe-9-grp", 1, 2
		return true
	})
	nat.alive = map[string]bool{s.ID: true}

	d.observe(context.Background())
	if got := fake.StateByIssue[s.IssueUUID]; got != "" {
		t.Fatalf("racing attempt moved the issue to %q", got)
	}
	d.sessions.Update(s.ID, func(cur *session.Session) bool { cur.BestOfWinner = true; return true })
	d.observe(context.Background())
	if got := fake.StateByIssue[s.IssueUUID]; got != "st-review" {
		t.Errorf("after the pick state = %q, want st-review", got)
	}
}

// Killing ONE best-of attempt by hand must not strip the issue's sent label
// while a sibling still works it.
func TestKillOneAttemptKeepsLabelForSibling(t *testing.T) {
	fake := &linear.Fake{LabelIDsByIssue: map[string][]string{"uuid-FE-5": {"lbl-sent"}}}
	nat := &fakeNative{}
	d := newTestDaemon(t, testConfig(labelPoll("p1")), fake, nat)
	g := seedGroup(d)
	if _, err := d.handleKill(context.Background(), g[0].ID, false); err != nil {
		t.Fatalf("kill: %v", err)
	}
	if got := fake.LabelIDsByIssue["uuid-FE-5"]; !reflect.DeepEqual(got, []string{"lbl-sent"}) {
		t.Errorf("labels = %v, want the sent label kept for the siblings", got)
	}
}

func TestRecordReviewTally(t *testing.T) {
	d := newTestDaemon(t, testConfig(labelPoll("p1")), &linear.Fake{}, &fakeNative{})
	s := nativeSess("FE-3", "working")
	s.PR = &scm.PR{Number: 4, State: "OPEN"}
	d.sessions.Upsert(s)
	d.recordReviewTally(s.ID, "claude-session", 4, "**[blocker]** `a.go:1` — x\n\n**[major]** `b.go:2` — y\n")
	d.recordReviewTally(s.ID, "codex-session", 4, "")
	d.recordReviewTally(s.ID, "coderabbit-cli", 0, "**[blocker]** x") // no PR: ignored
	cur, _ := d.sessions.Get(s.ID)
	got := reviewTallyFor(cur)
	if want := (&protocol.BestOfReviewTally{Passes: 2, Blocker: 1, Major: 1}); !reflect.DeepEqual(got, want) {
		t.Errorf("tally = %+v, want %+v", got, want)
	}
}
