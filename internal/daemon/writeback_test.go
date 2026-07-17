package daemon

// Tests for the P4 Linear write-back engine (writeback.go): spawn / PR-open /
// merged / blocked transitions, each fired at most once per lifecycle, each
// optional (empty config = zero Linear calls), state-based dedup, and the
// Linear-unavailable-at-spawn no-double-write guarantee. All Linear traffic goes
// through linear.Fake, which records every call.

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/linear"
	"github.com/sushidev-team/lola/internal/session"
)

// wbPoll is a label-mode poll wired with every P4 write-back field.
func wbPoll(name string) config.Poll {
	p := labelPoll(name)
	p.OnSpawnStateID = "st-doing"
	p.OnPRStateID = "st-review"
	p.OnMergedStateID = "st-done"
	p.BlockedLabelID = "lbl-blocked"
	p.CommentOnSpawn = true
	p.CommentOnPR = true
	p.CommentOnMerged = true
	p.CommentOnBlocked = true
	return p
}

// wbStatePoll is a dedup_mode=state poll: the OnSpawnStateID transition is the
// dedup, so it carries no on_sent_set_label / match_labels.
func wbStatePoll(name string) config.Poll {
	p := labelPoll(name)
	p.DedupMode = "state"
	p.OnSentSetLabel = ""
	p.MatchLabels = nil
	p.StateIDs = []string{"st-todo"}
	p.OnSpawnStateID = "st-doing"
	p.CommentOnSpawn = true
	return p
}

// wbObserveSess seeds a native session owned by poll `pollName` in the store.
func wbObserveSess(d *Daemon, ident, pollName string) session.Session {
	s := nativeSess(ident, "working")
	s.IssueUUID = "uuid-" + strings.ToLower(ident)
	s.PollName = pollName
	d.sessions.Upsert(s)
	return s
}

// --- spawn: state + comment once --------------------------------------------

func TestWriteBackSpawnSetsStateAndCommentOnce(t *testing.T) {
	is := testIssue("FE-7", 1, "2024-01-01T00:00:00Z")
	fake := &linear.Fake{
		Issues:          []linear.Issue{is},
		LabelIDsByIssue: map[string][]string{is.ID: {"lbl-trigger"}},
	}
	d := newTestDaemon(t, testConfig(wbPoll("p1")), fake, &fakeNative{})

	if _, err := d.tick(context.Background(), "p1", false); err != nil {
		t.Fatalf("tick 1: %v", err)
	}
	if got := fake.StateByIssue[is.ID]; got != "st-doing" {
		t.Errorf("spawn state = %q, want st-doing", got)
	}
	if c := fake.CommentsByIssue[is.ID]; len(c) != 1 {
		t.Fatalf("spawn comments = %d, want 1", len(c))
	}
	id := sessID(t, d)
	if s, _ := d.sessions.Get(id); !s.WBSpawnDone {
		t.Error("WBSpawnDone must be set after spawn write-back")
	}

	// Second tick: the seen guard + label flip dedup the issue, so no re-spawn
	// and therefore no duplicate state move or comment.
	names := fake.CallNames()
	if _, err := d.tick(context.Background(), "p1", false); err != nil {
		t.Fatalf("tick 2: %v", err)
	}
	if c := len(fake.CommentsByIssue[is.ID]); c != 1 {
		t.Errorf("spawn comments after 2nd tick = %d, want 1 (no duplicate)", c)
	}
	if a, b := countCalls(names, "SetIssueState"), countCalls(fake.CallNames(), "SetIssueState"); a != b {
		t.Errorf("SetIssueState calls grew on 2nd tick (%d → %d)", a, b)
	}
}

// --- state-mode dedup: state move only, no seen/label -----------------------

func TestWriteBackStateModeDedup(t *testing.T) {
	is := testIssue("FE-3", 1, "2024-01-01T00:00:00Z")
	fake := &linear.Fake{}
	// The issue matches until its workflow state moves out of state_ids.
	fake.IssuesFunc = func(_ config.Poll, _, _ string) ([]linear.Issue, error) {
		if fake.StateByIssue[is.ID] == "st-doing" {
			return nil, nil
		}
		return []linear.Issue{is}, nil
	}
	d := newTestDaemon(t, testConfig(wbStatePoll("p1")), fake, &fakeNative{})

	if _, err := d.tick(context.Background(), "p1", false); err != nil {
		t.Fatalf("tick 1: %v", err)
	}
	if got := fake.StateByIssue[is.ID]; got != "st-doing" {
		t.Errorf("state after spawn = %q, want st-doing (the dedup move)", got)
	}
	if n := countCalls(fake.CallNames(), "SetIssueLabels"); n != 0 {
		t.Errorf("state mode must write NO labels, got %d SetIssueLabels", n)
	}
	if _, err := os.Stat(seenPath(d, "p1")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("state mode must write NO seen file on the happy path (stat err=%v)", err)
	}
	if len(nativeFakeSpawns(d)) != 1 {
		t.Fatalf("want exactly one spawn")
	}

	// Clear the in-flight claim (as reconcile would after orphan_timeout, or a
	// fresh daemon) so the second tick relies purely on the STATE move for dedup.
	d.inflight.Remove(is.ID)
	if _, err := d.tick(context.Background(), "p1", false); err != nil {
		t.Fatalf("tick 2: %v", err)
	}
	if n := len(nativeFakeSpawns(d)); n != 1 {
		t.Errorf("state move must stop re-dispatch: spawns = %d, want 1", n)
	}
}

// --- state-mode crash window: a live session blocks a second spawn ----------

// Simulates a daemon killed AFTER a state-mode spawn saved its session but
// BEFORE the post-spawn SetIssueState(OnSpawnStateID) landed. On restart the
// in-memory in-flight set is empty, state mode wrote no pre-spawn seen entry,
// and the issue never left state_ids so it still matches. The adopted live
// native session must prevent a SECOND agent being spawned on the same issue.
func TestWriteBackStateModeNoDoubleSpawnAfterCrash(t *testing.T) {
	is := testIssue("FE-3", 1, "2024-01-01T00:00:00Z")
	fake := &linear.Fake{
		IssuesFunc: func(_ config.Poll, _, _ string) ([]linear.Issue, error) {
			return []linear.Issue{is}, nil // still matches: the state move never landed
		},
	}
	nat := &fakeNative{}
	d := newTestDaemon(t, testConfig(wbStatePoll("p1")), fake, nat)

	// Post-restart store: a live native session for the issue, no in-flight
	// claim (in-memory, lost on restart), no seen file (state mode writes none).
	s := nativeSess("FE-3", "working")
	s.IssueUUID = is.ID
	s.PollName = "p1"
	d.sessions.Upsert(s)

	res, err := d.tick(context.Background(), "p1", false)
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	if n := len(nativeFakeSpawns(d)); n != 0 {
		t.Fatalf("a live native session must block a second spawn on the same issue, got %d", n)
	}
	if m := findMatch(t, res, "FE-3"); m.Action != "skipped" || m.Reason != "session-live" {
		t.Errorf("match = %+v, want skipped/session-live", m)
	}
}

// --- state-mode seen fallback is authoritative (no TTL) ----------------------

// When the spawn state move fails permanently (e.g. a stale on_spawn_state_id
// that Validate() cannot resolve), writeBackSpawn writes a seen fallback. That
// entry must dedup the issue for as long as it keeps matching — NOT expire after
// SeenTTL like a label-mode race guard — or the issue re-dispatches ~hourly
// forever once its session is gone.
func TestWriteBackStateModeSeenFallbackAuthoritativeBeyondTTL(t *testing.T) {
	is := testIssue("FE-3", 1, "2024-01-01T00:00:00Z")
	fake := &linear.Fake{
		IssuesFunc: func(_ config.Poll, _, _ string) ([]linear.Issue, error) {
			return []linear.Issue{is}, nil // never leaves state_ids
		},
	}
	nat := &fakeNative{}
	d := newTestDaemon(t, testConfig(wbStatePoll("p1")), fake, nat)

	// A fallback entry older than SeenTTL, with no live session and no in-flight
	// claim (as after the agent's pane died and reconcile cleared the claim).
	old := time.Now().Add(-2 * SeenTTL)
	if err := d.seen.save("p1", map[string]time.Time{is.ID: old}); err != nil {
		t.Fatalf("seed seen: %v", err)
	}

	res, err := d.tick(context.Background(), "p1", false)
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	if n := len(nativeFakeSpawns(d)); n != 0 {
		t.Fatalf("aged state-mode seen fallback must stay authoritative, got %d spawns", n)
	}
	if m := findMatch(t, res, "FE-3"); m.Action != "skipped" || m.Reason != "dedup-state" {
		t.Errorf("match = %+v, want skipped/dedup-state", m)
	}
	// The entry survives pruning because the issue still matches.
	seen, err := d.seen.load("p1")
	if err != nil {
		t.Fatalf("load seen: %v", err)
	}
	if _, ok := seen[is.ID]; !ok {
		t.Error("state-mode seen fallback must survive pruning while the issue still matches")
	}
}

// --- PR open: state + comment once ------------------------------------------

func TestWriteBackPROpenOnce(t *testing.T) {
	p := labelPoll("p1")
	p.OnPRStateID = "st-review"
	p.CommentOnPR = true
	fake := &linear.Fake{}
	nat := &fakeNative{}
	d := newTestDaemon(t, testConfig(p), fake, nat)
	seams := &fakeObsSeams{pr: openPR(9, "MERGEABLE", "", "pending")} // review_pending
	seams.install(d)

	s := wbObserveSess(d, "FE-9", "p1")
	nat.alive = map[string]bool{s.ID: true}

	d.observe(context.Background())
	if got := fake.StateByIssue[s.IssueUUID]; got != "st-review" {
		t.Errorf("PR-open state = %q, want st-review", got)
	}
	if c := fake.CommentsByIssue[s.IssueUUID]; len(c) != 1 || !strings.Contains(c[0], "pull/9") {
		t.Fatalf("PR-open comment = %v, want one referencing the PR URL", c)
	}
	if cur, _ := d.sessions.Get(s.ID); !cur.WBPRDone {
		t.Error("WBPRDone must be set after the PR-open write-back")
	}

	d.observe(context.Background()) // second cycle: guard blocks a repeat
	if c := len(fake.CommentsByIssue[s.IssueUUID]); c != 1 {
		t.Errorf("PR-open comments after 2nd cycle = %d, want 1", c)
	}
	if n := countCalls(fake.CallNames(), "SetIssueState"); n != 1 {
		t.Errorf("SetIssueState fired %d times, want once", n)
	}
}

// --- pr_requires_checks: hold "In Review" until the PR is valid -------------

// With pr_requires_checks the on_pr_* write-back must NOT fire while the PR is
// open-but-not-green (pending / failing / draft): no state move, no comment, and
// WBPRDone stays unset so a later green cycle still fires it. Once checks pass the
// transition fires exactly once, as normal.
func TestWriteBackPRRequiresChecksDefersUntilGreen(t *testing.T) {
	p := labelPoll("p1")
	p.OnPRStateID = "st-review"
	p.CommentOnPR = true
	p.PRRequiresChecks = true
	fake := &linear.Fake{}
	nat := &fakeNative{}
	d := newTestDaemon(t, testConfig(p), fake, nat)
	seams := &fakeObsSeams{pr: openPR(9, "MERGEABLE", "", "pending")} // open, checks running
	seams.install(d)

	s := wbObserveSess(d, "FE-9", "p1")
	nat.alive = map[string]bool{s.ID: true}

	// Checks pending: the PR is not valid yet, so nothing is written back.
	d.observe(context.Background())
	if got, ok := fake.StateByIssue[s.IssueUUID]; ok {
		t.Errorf("PR not green yet: state moved to %q, want no transition", got)
	}
	if c := fake.CommentsByIssue[s.IssueUUID]; len(c) != 0 {
		t.Errorf("PR not green yet: %d comment(s), want 0", len(c))
	}
	if cur, _ := d.sessions.Get(s.ID); cur.WBPRDone {
		t.Error("WBPRDone must stay unset while the PR has not passed checks")
	}
	if n := countCalls(fake.CallNames(), "SetIssueState"); n != 0 {
		t.Errorf("SetIssueState fired %d times before green, want 0", n)
	}

	// Checks go green: the same PR is now valid, so the transition fires once.
	seams.pr = openPR(9, "MERGEABLE", "", "pass")
	d.observe(context.Background())
	if got := fake.StateByIssue[s.IssueUUID]; got != "st-review" {
		t.Errorf("PR green: state = %q, want st-review", got)
	}
	if c := fake.CommentsByIssue[s.IssueUUID]; len(c) != 1 || !strings.Contains(c[0], "pull/9") {
		t.Fatalf("PR green: comment = %v, want one referencing the PR URL", c)
	}
	if cur, _ := d.sessions.Get(s.ID); !cur.WBPRDone {
		t.Error("WBPRDone must be set after the green PR write-back")
	}

	// A later cycle (still green) must not repeat the transition.
	d.observe(context.Background())
	if c := len(fake.CommentsByIssue[s.IssueUUID]); c != 1 {
		t.Errorf("PR comments after a repeat cycle = %d, want 1", c)
	}
	if n := countCalls(fake.CallNames(), "SetIssueState"); n != 1 {
		t.Errorf("SetIssueState fired %d times total, want once", n)
	}
}

// A draft PR whose checks are green is still NOT valid for "In Review": drafts
// are excluded regardless of check state.
func TestWriteBackPRRequiresChecksSkipsDraft(t *testing.T) {
	p := labelPoll("p1")
	p.OnPRStateID = "st-review"
	p.CommentOnPR = true
	p.PRRequiresChecks = true
	fake := &linear.Fake{}
	nat := &fakeNative{}
	d := newTestDaemon(t, testConfig(p), fake, nat)
	draft := openPR(9, "MERGEABLE", "", "pass")
	draft.IsDraft = true
	seams := &fakeObsSeams{pr: draft}
	seams.install(d)

	s := wbObserveSess(d, "FE-9", "p1")
	nat.alive = map[string]bool{s.ID: true}

	d.observe(context.Background())
	if got, ok := fake.StateByIssue[s.IssueUUID]; ok {
		t.Errorf("draft PR: state moved to %q, want no transition", got)
	}
	if cur, _ := d.sessions.Get(s.ID); cur.WBPRDone {
		t.Error("WBPRDone must stay unset for a draft PR")
	}
}

// --- merged: write-back BEFORE cleanup drops the session --------------------

func TestWriteBackMergedBeforeCleanup(t *testing.T) {
	p := labelPoll("p1")
	p.OnMergedStateID = "st-done"
	p.CommentOnMerged = true
	cfg := reactTestConfig(p) // Merged.Auto = true
	fake := &linear.Fake{}
	nat := &fakeNative{}
	d := newTestDaemon(t, cfg, fake, nat)
	merged := openPR(7, "MERGEABLE", "APPROVED", "pass")
	merged.State = "MERGED"
	seams := &fakeObsSeams{pr: merged}
	seams.install(d)

	s := wbObserveSess(d, "FE-7", "p1")
	d.inflight.Add(s.IssueUUID, s.Issue)

	d.observe(context.Background())

	// Write-back happened: state moved + comment posted.
	if got := fake.StateByIssue[s.IssueUUID]; got != "st-done" {
		t.Errorf("merged state = %q, want st-done", got)
	}
	if c := fake.CommentsByIssue[s.IssueUUID]; len(c) != 1 {
		t.Fatalf("merged comments = %d, want 1", len(c))
	}
	// ... and cleanup then dropped the session and killed the runner.
	if len(nat.killCalls()) != 1 {
		t.Errorf("merged cleanup must Kill once, got %d", len(nat.killCalls()))
	}
	if _, ok := d.sessions.Get(s.ID); ok {
		t.Error("merged session must be dropped by cleanup after write-back")
	}
}

// A merged session whose worktree cleanup keeps failing must not re-comment: the
// WBMergedDone guard is set before cleanup, so later cycles retry the Kill only.
func TestWriteBackMergedNoRecommentWhenCleanupFails(t *testing.T) {
	p := labelPoll("p1")
	p.OnMergedStateID = "st-done"
	p.CommentOnMerged = true
	cfg := reactTestConfig(p)
	fake := &linear.Fake{}
	nat := &fakeNative{killErr: errors.New("worktree busy")}
	d := newTestDaemon(t, cfg, fake, nat)
	merged := openPR(7, "MERGEABLE", "APPROVED", "pass")
	merged.State = "MERGED"
	(&fakeObsSeams{pr: merged}).install(d)

	s := wbObserveSess(d, "FE-7", "p1")

	d.observe(context.Background())
	d.observe(context.Background()) // cleanup failed on cycle 1; retried on cycle 2

	if c := len(fake.CommentsByIssue[s.IssueUUID]); c != 1 {
		t.Errorf("merged comment fired %d times across two cycles, want exactly 1", c)
	}
	if k := len(nat.killCalls()); k < 2 {
		t.Errorf("failed cleanup must be retried: Kill calls = %d, want >= 2", k)
	}
}

// --- escalation (blocked): label + comment once -----------------------------

func TestWriteBackEscalationBlockedOnce(t *testing.T) {
	p := labelPoll("p1")
	p.BlockedLabelID = "lbl-blocked"
	p.CommentOnBlocked = true
	cfg := reactTestConfig(p)
	cfg.Reactions.CIFailed.Retries = 0 // escalate on the first ci_failed
	fake := &linear.Fake{LabelIDsByIssue: map[string][]string{"uuid-fe-9": {"lbl-x"}}}
	nat := &fakeNative{}
	d := newTestDaemon(t, cfg, fake, nat)
	(&fakeObsSeams{pr: openPR(9, "MERGEABLE", "", "fail")}).install(d) // ci_failed

	s := wbObserveSess(d, "FE-9", "p1")
	nat.alive = map[string]bool{s.ID: true}

	d.observe(context.Background())

	if cur, _ := d.sessions.Get(s.ID); !cur.Escalated {
		t.Fatal("session must have escalated on ci_failed with 0 retries")
	}
	got := fake.LabelIDsByIssue[s.IssueUUID]
	if !containsStr(got, "lbl-blocked") {
		t.Errorf("blocked label not added: labels = %v", got)
	}
	if c := fake.CommentsByIssue[s.IssueUUID]; len(c) != 1 {
		t.Fatalf("blocked comments = %d, want 1", len(c))
	}
	if cur, _ := d.sessions.Get(s.ID); !cur.WBBlockedDone {
		t.Error("WBBlockedDone must be set")
	}

	setLabels := countCalls(fake.CallNames(), "SetIssueLabels")
	d.observe(context.Background()) // second cycle: guard blocks a repeat
	if c := len(fake.CommentsByIssue[s.IssueUUID]); c != 1 {
		t.Errorf("blocked comment fired again, count = %d, want 1", c)
	}
	if n := countCalls(fake.CallNames(), "SetIssueLabels"); n != setLabels {
		t.Errorf("blocked label re-applied on 2nd cycle (%d → %d)", setLabels, n)
	}
}

// --- optional: empty write-back config makes zero write-back calls ----------

// TestPollForSessionSkipsNonLinearKinds pins the Phase-0 write-back gate: only a
// Linear-bound session (linear kind + issue UUID) resolves a poll's P4 config.
// A pr/manual/keyless session sharing the project with a poll must resolve nil,
// so the write-back path can never drive a Linear API call against an empty UUID.
func TestPollForSessionSkipsNonLinearKinds(t *testing.T) {
	d := newTestDaemon(t, testConfig(wbStatePoll("p1")), &linear.Fake{}, &fakeNative{})

	linSess := session.Session{ID: "lola-proj1-eng-9", Source: "native", Kind: session.KindLinear, Project: "proj1", Issue: "ENG-9", IssueUUID: "uuid-9"}
	if p := d.pollForSession(linSess); p == nil {
		t.Fatalf("a linear session must resolve its project's poll for write-back")
	}

	nonLinear := []session.Session{
		{ID: "lola-proj1-pr-7", Source: "native", Kind: session.KindPR, Agentless: true, Project: "proj1", Branch: "pr-7"},
		{ID: "lola-proj1-pr-a", Source: "native", Kind: session.KindPR, Project: "proj1", Branch: "feat/x"},
		{ID: "lola-proj1-open-y", Source: "native", Kind: session.KindManual, Project: "proj1", Branch: "feat/y"},
		{ID: "lola-legacy-manual", Source: "native", Manual: true, Project: "proj1", Branch: "up"},
		{ID: "lola-keyless", Source: "native", Project: "proj1"}, // no UUID → fail closed to pr
	}
	for _, s := range nonLinear {
		if p := d.pollForSession(s); p != nil {
			t.Errorf("non-linear session %s resolved poll %q; must be nil (would write back against an empty UUID)", s.ID, p.Name)
		}
	}
}

func TestWriteBackNoConfigNoLinearWrites(t *testing.T) {
	is := testIssue("FE-1", 1, "2024-01-01T00:00:00Z")
	fake := &linear.Fake{
		Issues:          []linear.Issue{is},
		LabelIDsByIssue: map[string][]string{is.ID: {"lbl-trigger"}},
	}
	nat := &fakeNative{}
	d := newTestDaemon(t, reactTestConfig(labelPoll("p1")), fake, nat) // plain poll, no P4 fields

	if _, err := d.tick(context.Background(), "p1", false); err != nil {
		t.Fatalf("tick: %v", err)
	}
	// Drive the session through a merged observe cycle too.
	merged := openPR(7, "MERGEABLE", "APPROVED", "pass")
	merged.State = "MERGED"
	(&fakeObsSeams{pr: merged}).install(d)
	d.observe(context.Background())

	if n := countCalls(fake.CallNames(), "SetIssueState"); n != 0 {
		t.Errorf("no-config poll made %d SetIssueState calls, want 0", n)
	}
	if n := countCalls(fake.CallNames(), "CreateComment"); n != 0 {
		t.Errorf("no-config poll made %d CreateComment calls, want 0", n)
	}
}

// --- Linear-unavailable at spawn: no crash, no double-write -----------------

func TestWriteBackSpawnLinearFailureNoDoubleWrite(t *testing.T) {
	is := testIssue("FE-3", 1, "2024-01-01T00:00:00Z")
	fake := &linear.Fake{
		IssuesFunc: func(_ config.Poll, _, _ string) ([]linear.Issue, error) {
			return []linear.Issue{is}, nil // always matches (state move fails below)
		},
		Errs: map[string]error{
			"SetIssueState": errors.New("linear down"),
			"CreateComment": errors.New("linear down"),
		},
	}
	nat := &fakeNative{}
	d := newTestDaemon(t, testConfig(wbStatePoll("p1")), fake, nat)

	if _, err := d.tick(context.Background(), "p1", false); err != nil {
		t.Fatalf("tick 1 must not fail on a write-back error: %v", err)
	}
	if n := len(nativeFakeSpawns(d)); n != 1 {
		t.Fatalf("spawn should have succeeded once, got %d", n)
	}
	// State move failed → seen fallback keeps the issue from re-dispatching.
	if _, err := os.Stat(seenPath(d, "p1")); err != nil {
		t.Errorf("state-move failure must fall back to a seen entry: %v", err)
	}

	d.inflight.Remove(is.ID) // as reconcile would; force the dedup to rely on seen
	stateAttempts := countCalls(fake.CallNames(), "SetIssueState")
	commentAttempts := countCalls(fake.CallNames(), "CreateComment")
	if _, err := d.tick(context.Background(), "p1", false); err != nil {
		t.Fatalf("tick 2: %v", err)
	}
	if n := len(nativeFakeSpawns(d)); n != 1 {
		t.Errorf("seen fallback must block re-dispatch: spawns = %d, want 1", n)
	}
	if n := countCalls(fake.CallNames(), "SetIssueState"); n != stateAttempts {
		t.Errorf("SetIssueState retried on 2nd tick (%d → %d)", stateAttempts, n)
	}
	if n := countCalls(fake.CallNames(), "CreateComment"); n != commentAttempts {
		t.Errorf("CreateComment retried on 2nd tick (%d → %d): double-comment risk", commentAttempts, n)
	}
}

// helpers ---------------------------------------------------------------------

func sessID(t *testing.T, d *Daemon) string {
	t.Helper()
	snap := d.sessions.Snapshot()
	if len(snap) != 1 {
		t.Fatalf("want exactly one session, got %d", len(snap))
	}
	return snap[0].ID
}

func nativeFakeSpawns(d *Daemon) []nativeSpawnCall {
	return d.native.(*fakeNative).spawnCalls()
}

func containsStr(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}
