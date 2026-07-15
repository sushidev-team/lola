package daemon

// Tests for the P9 QA review buddy (review.go): the PR-open auto-trigger, the
// once-per-PR guard, the sanitized + idle-gated worker hand-off (with deferral),
// the clean-review path, the notify/Linear-comment sinks, graceful skip on a
// missing/erroring coderabbit, and the `lola review` force command.
//
// All seams are hermetic fakes — no coderabbit, gh, tmux, git, or network.
// fakeReview stands in for the review.Client exec seam; fakeReactSeams (from
// reactions_test.go) provides the send-keys and notifier seams.

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/linear"
	"github.com/sushidev-team/lola/internal/notify"
	"github.com/sushidev-team/lola/internal/review"
	"github.com/sushidev-team/lola/internal/session"
)

// reviewCall records one review exec (its worktree dir + base branch).
type reviewCall struct{ dir, base string }

// fakeReview installs a counting fake for the daemon's reviewRun exec seam.
type fakeReview struct {
	mu         sync.Mutex
	calls      []reviewCall
	findings   string
	err        error
	lastCtxErr error  // ctx.Err() observed by the most recent exec (nil = live ctx)
	onCall     func() // runs inside the exec, before returning (e.g. to assert the guard)
}

func (f *fakeReview) install(d *Daemon) {
	d.reviewRun = func(ctx context.Context, dir, base string) (string, error) {
		f.mu.Lock()
		f.calls = append(f.calls, reviewCall{dir, base})
		f.lastCtxErr = ctx.Err()
		hook := f.onCall
		findings, err := f.findings, f.err
		f.mu.Unlock()
		if hook != nil {
			hook()
		}
		return findings, err
	}
}

func (f *fakeReview) ctxErr() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lastCtxErr
}

func (f *fakeReview) callsCopy() []reviewCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]reviewCall(nil), f.calls...)
}

func (f *fakeReview) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

// reviewTestConfig is a native test config with the [review] buddy enabled
// (on_pr_open + send_to_agent on, comment off — the `enabled = true` defaults).
func reviewTestConfig(polls ...config.Poll) *config.Config {
	c := nativeTestConfig(polls...)
	c.Review = config.ReviewConfig{
		Enabled:        true,
		OnPROpen:       true,
		SendToAgent:    true,
		TimeoutSeconds: config.DefaultReviewTimeoutSeconds,
	}
	return c
}

// --- PR-open auto-trigger: exec against the worktree, route to worker + notify -

func TestReviewOnPROpenRunsRoutesToWorkerAndNotifies(t *testing.T) {
	d := newTestDaemon(t, reviewTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeNative{})
	seams := &fakeReactSeams{}
	seams.install(d)
	fr := &fakeReview{findings: "FINDING-XYZ: fix the nil deref"}
	// The one-shot guard must be stamped BEFORE the (long) exec so a crash can
	// never double-fire — assert it is already set while the exec runs.
	fr.onCall = func() {
		if got, _ := d.sessions.Get(runtime_id("FE-1")); got.ReviewedPR != 7 {
			t.Errorf("ReviewedPR must be stamped BEFORE the exec, got %d", got.ReviewedPR)
		}
	}
	fr.install(d)

	s := reactSess("FE-1", "review_pending", openPR(7, "MERGEABLE", "", "pass"))
	s.AtPrompt = true
	d.sessions.Upsert(s)

	d.reviewOnPROpen(context.Background(), s)

	calls := fr.callsCopy()
	if len(calls) != 1 {
		t.Fatalf("want one review exec, got %d", len(calls))
	}
	wantDir := filepath.Join(d.home, "worktrees", "proj1", s.ID)
	if calls[0].dir != wantDir || calls[0].base != "main" {
		t.Errorf("review exec = {dir %q, base %q}, want {%q, main}", calls[0].dir, calls[0].base, wantDir)
	}

	sends := seams.sendCalls()
	if len(sends) != 1 {
		t.Fatalf("want one send-keys hand-off, got %d", len(sends))
	}
	if !strings.Contains(sends[0].text, "FINDING-XYZ") {
		t.Errorf("hand-off must carry the findings, got %q", sends[0].text)
	}
	if !strings.Contains(sends[0].text, "CodeRabbit") {
		t.Errorf("hand-off must carry the review preamble, got %q", sends[0].text)
	}
	if action := seams.notesByPriority(notify.Action); len(action) != 1 {
		t.Errorf("want one Action notification, got %+v", seams.notes)
	}
	got, _ := d.sessions.Get(s.ID)
	if got.ReviewedPR != 7 {
		t.Errorf("ReviewedPR = %d, want 7", got.ReviewedPR)
	}
	if got.AtPrompt {
		t.Error("AtPrompt must be consumed after the hand-off")
	}
	if got.PendingReviewFindings != "" {
		t.Errorf("PendingReviewFindings must be clear after a delivered hand-off, got %q", got.PendingReviewFindings)
	}
}

// runtime_id resolves the store ID for a proj1 issue, matching nativeSess.
func runtime_id(ident string) string { return nativeSess(ident, "").ID }

// --- fire once per PR; a NEW PR number re-runs -------------------------------

func TestReviewFiresOncePerPRAndRerunsOnNewPR(t *testing.T) {
	d := newTestDaemon(t, reviewTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeNative{})
	(&fakeReactSeams{}).install(d)
	fr := &fakeReview{findings: "ISSUE"}
	fr.install(d)

	s := reactSess("FE-1", "review_pending", openPR(7, "MERGEABLE", "", "pass"))
	s.AtPrompt = true
	d.sessions.Upsert(s)

	d.reviewOnPROpen(context.Background(), s)
	if fr.callCount() != 1 {
		t.Fatalf("first PR-open must run the review, got %d", fr.callCount())
	}

	// Second cycle, same PR: the ReviewedPR guard must suppress it.
	got, _ := d.sessions.Get(s.ID)
	d.reviewOnPROpen(context.Background(), got)
	if fr.callCount() != 1 {
		t.Errorf("review must fire once per PR, got %d execs", fr.callCount())
	}

	// A new PR number re-triggers exactly once.
	d.sessions.Update(s.ID, func(cur *session.Session) bool {
		cur.PR = openPR(8, "MERGEABLE", "", "pass")
		cur.AtPrompt = true
		return true
	})
	got, _ = d.sessions.Get(s.ID)
	d.reviewOnPROpen(context.Background(), got)
	if fr.callCount() != 2 {
		t.Errorf("a new PR number must re-trigger the review, got %d execs", fr.callCount())
	}
	got, _ = d.sessions.Get(s.ID)
	if got.ReviewedPR != 8 {
		t.Errorf("ReviewedPR = %d, want 8 after the new PR review", got.ReviewedPR)
	}
}

// --- worker busy → deferred, not dropped; delivered when idle -----------------

func TestReviewDefersWhenWorkerBusyThenFlushes(t *testing.T) {
	d := newTestDaemon(t, reviewTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeNative{})
	seams := &fakeReactSeams{}
	seams.install(d)
	fr := &fakeReview{findings: "DEFER-ME"}
	fr.install(d)

	// Worker mid-turn: AtPrompt false.
	s := reactSess("FE-1", "review_pending", openPR(7, "MERGEABLE", "", "pass"))
	s.AtPrompt = false
	d.sessions.Upsert(s)

	d.reviewOnPROpen(context.Background(), s)

	if len(seams.sendCalls()) != 0 {
		t.Fatal("a mid-turn worker must not be sent-keys")
	}
	got, _ := d.sessions.Get(s.ID)
	if !strings.Contains(got.PendingReviewFindings, "DEFER-ME") {
		t.Errorf("findings must be stashed for later delivery, got %q", got.PendingReviewFindings)
	}
	if got.ReviewedPR != 7 {
		t.Errorf("ReviewedPR must be stamped even when the hand-off defers, got %d", got.ReviewedPR)
	}
	// The human is still notified immediately (defer only affects the worker sink).
	if len(seams.notesByPriority(notify.Action)) != 1 {
		t.Errorf("want one Action notification even on a deferred hand-off, got %+v", seams.notes)
	}

	// Worker returns to its prompt → the deferred hand-off flushes once.
	d.sessions.Update(s.ID, func(cur *session.Session) bool { cur.AtPrompt = true; return true })
	d.flushPendingReview(context.Background(), s.ID)

	sends := seams.sendCalls()
	if len(sends) != 1 || !strings.Contains(sends[0].text, "DEFER-ME") {
		t.Fatalf("deferred hand-off must flush once the worker is idle, got %+v", sends)
	}
	got, _ = d.sessions.Get(s.ID)
	if got.PendingReviewFindings != "" {
		t.Errorf("PendingReviewFindings must clear after a delivered hand-off, got %q", got.PendingReviewFindings)
	}

	// A second flush is a no-op (nothing pending).
	d.flushPendingReview(context.Background(), s.ID)
	if len(seams.sendCalls()) != 1 {
		t.Errorf("flush must not re-send a delivered hand-off, got %d sends", len(seams.sendCalls()))
	}
}

// --- clean review → no worker message, Info notify only -----------------------

func TestReviewCleanNoWorkerMessageInfoNotify(t *testing.T) {
	d := newTestDaemon(t, reviewTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeNative{})
	seams := &fakeReactSeams{}
	seams.install(d)
	fr := &fakeReview{findings: ""} // clean
	fr.install(d)

	s := reactSess("FE-1", "review_pending", openPR(7, "MERGEABLE", "", "pass"))
	s.AtPrompt = true
	d.sessions.Upsert(s)

	d.reviewOnPROpen(context.Background(), s)

	if fr.callCount() != 1 {
		t.Fatalf("review must run, got %d execs", fr.callCount())
	}
	if len(seams.sendCalls()) != 0 {
		t.Error("a clean review must never message the worker")
	}
	if info := seams.notesByPriority(notify.Info); len(info) != 1 {
		t.Errorf("want one Info notification for a clean review, got %+v", seams.notes)
	}
	if action := seams.notesByPriority(notify.Action); len(action) != 0 {
		t.Errorf("a clean review must not fire an Action notification, got %+v", action)
	}
}

// --- review disabled → no exec, no crash --------------------------------------

func TestReviewDisabledNoCall(t *testing.T) {
	cfg := nativeTestConfig(nativePoll("p1")) // Review left at its zero value (disabled)
	d := newTestDaemon(t, cfg, &linear.Fake{}, &fakeNative{})
	seams := &fakeReactSeams{}
	seams.install(d)
	// reviewRun stays nil (Run would leave it nil when disabled/unavailable).

	s := reactSess("FE-1", "review_pending", openPR(7, "MERGEABLE", "", "pass"))
	s.AtPrompt = true
	d.sessions.Upsert(s)

	d.reviewOnPROpen(context.Background(), s) // must be a no-op

	if len(seams.sendCalls()) != 0 || seams.noteCount() != 0 {
		t.Error("review disabled must make zero send/notify calls")
	}
	if got, _ := d.sessions.Get(s.ID); got.ReviewedPR != 0 {
		t.Errorf("review disabled must not stamp ReviewedPR, got %d", got.ReviewedPR)
	}
}

// --- coderabbit error → graceful skip (no worker message, guard left set) -----

func TestReviewCoderabbitErrorGraceful(t *testing.T) {
	d := newTestDaemon(t, reviewTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeNative{})
	seams := &fakeReactSeams{}
	seams.install(d)
	fr := &fakeReview{err: review.ErrAuth}
	fr.install(d)

	s := reactSess("FE-1", "review_pending", openPR(7, "MERGEABLE", "", "pass"))
	s.AtPrompt = true
	d.sessions.Upsert(s)

	d.reviewOnPROpen(context.Background(), s) // must not panic

	if len(seams.sendCalls()) != 0 {
		t.Error("an errored review must never message the worker")
	}
	if seams.noteCount() != 0 {
		t.Error("an errored review surfaces nothing (findings untrusted/untouched on error)")
	}
	// The guard stays set: a re-review loop is a human/CI concern, not automatic.
	if got, _ := d.sessions.Get(s.ID); got.ReviewedPR != 7 {
		t.Errorf("ReviewedPR must remain stamped after an errored review, got %d", got.ReviewedPR)
	}
}

// --- comment_on_linear → findings posted as a Linear comment ------------------

func TestReviewCommentsOnLinear(t *testing.T) {
	cfg := reviewTestConfig(nativePoll("p1"))
	cfg.Review.CommentOnLinear = true
	fake := &linear.Fake{}
	d := newTestDaemon(t, cfg, fake, &fakeNative{})
	(&fakeReactSeams{}).install(d)
	fr := &fakeReview{findings: "LINEAR-FINDING"}
	fr.install(d)

	s := reactSess("FE-1", "review_pending", openPR(7, "MERGEABLE", "", "pass"))
	s.AtPrompt = true
	d.sessions.Upsert(s)

	d.reviewOnPROpen(context.Background(), s)

	bodies := fake.CommentsByIssue[s.IssueUUID]
	if len(bodies) != 1 || !strings.Contains(bodies[0], "LINEAR-FINDING") {
		t.Fatalf("want one Linear comment carrying the findings, got %+v", bodies)
	}
}

// --- untrusted findings sanitized before the send-keys hand-off ---------------

func TestReviewSanitizesFindingsBeforeSend(t *testing.T) {
	d := newTestDaemon(t, reviewTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeNative{})
	seams := &fakeReactSeams{}
	seams.install(d)
	// Findings carrying a CR (the send-keys submit vector), ANSI escapes, and a
	// NUL — all injectable via attacker-authored diff content.
	fr := &fakeReview{findings: "line 1\rline 2\x1b[31mRED\x1b[0m\x00\n\tKEEP"}
	fr.install(d)

	s := reactSess("FE-1", "review_pending", openPR(7, "MERGEABLE", "", "pass"))
	s.AtPrompt = true
	d.sessions.Upsert(s)

	d.reviewOnPROpen(context.Background(), s)

	calls := seams.sendCalls()
	if len(calls) != 1 {
		t.Fatalf("want one send-keys, got %d", len(calls))
	}
	got := calls[0].text
	if strings.ContainsRune(got, '\r') {
		t.Errorf("hand-off must not contain CR (submit vector): %q", got)
	}
	if strings.ContainsRune(got, '\x1b') || strings.Contains(got, "[31m") {
		t.Errorf("hand-off must be stripped of ANSI escapes: %q", got)
	}
	if strings.ContainsRune(got, '\x00') {
		t.Errorf("hand-off must not contain other control bytes: %q", got)
	}
	if !strings.Contains(got, "KEEP") || !strings.Contains(got, "\n\tKEEP") {
		t.Errorf("hand-off must keep visible text and legitimate LF/TAB: %q", got)
	}
}

// --- `lola review` forces a run ignoring the ReviewedPR guard -----------------

func TestHandleReviewForcesIgnoringGuard(t *testing.T) {
	d := newTestDaemon(t, reviewTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeNative{})
	seams := &fakeReactSeams{}
	seams.install(d)
	fr := &fakeReview{findings: "FORCED-FINDING"}
	fr.install(d)

	// The PR was ALREADY reviewed (guard set) — the auto-trigger would skip it.
	s := reactSess("FE-1", "review_pending", openPR(7, "MERGEABLE", "", "pass"))
	s.AtPrompt = true
	s.ReviewedPR = 7
	d.sessions.Upsert(s)

	// The auto-trigger is indeed suppressed by the guard.
	d.reviewOnPROpen(context.Background(), s)
	if fr.callCount() != 0 {
		t.Fatalf("auto-trigger must respect the guard, got %d execs", fr.callCount())
	}

	// The manual command forces it anyway.
	data, err := d.handleReview(context.Background(), s.ID)
	if err != nil {
		t.Fatalf("handleReview: %v", err)
	}
	if fr.callCount() != 1 {
		t.Fatalf("`lola review` must force a run ignoring the guard, got %d execs", fr.callCount())
	}
	if !data.Ran || data.Clean || !strings.Contains(data.Findings, "FORCED-FINDING") {
		t.Errorf("review data = %+v, want ran with the findings", data)
	}
	if len(seams.sendCalls()) != 1 {
		t.Errorf("forced review must route to the worker too, got %d sends", len(seams.sendCalls()))
	}
}

func TestHandleReviewSkippedWhenDisabled(t *testing.T) {
	d := newTestDaemon(t, nativeTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeNative{})
	// reviewRun nil (disabled).
	s := reactSess("FE-1", "review_pending", openPR(7, "MERGEABLE", "", "pass"))
	d.sessions.Upsert(s)

	data, err := d.handleReview(context.Background(), s.ID)
	if err != nil {
		t.Fatalf("handleReview must not error when review is disabled, got %v", err)
	}
	if data.Ran || data.Skipped == "" {
		t.Errorf("review data = %+v, want skipped/not-enabled", data)
	}
}

func TestHandleReviewCleanOutcome(t *testing.T) {
	d := newTestDaemon(t, reviewTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeNative{})
	(&fakeReactSeams{}).install(d)
	fr := &fakeReview{findings: ""}
	fr.install(d)

	s := reactSess("FE-1", "review_pending", openPR(7, "MERGEABLE", "", "pass"))
	d.sessions.Upsert(s)

	data, err := d.handleReview(context.Background(), s.ID)
	if err != nil {
		t.Fatalf("handleReview: %v", err)
	}
	if !data.Ran || !data.Clean {
		t.Errorf("review data = %+v, want ran+clean", data)
	}
}

func TestHandleReviewUnknownSession(t *testing.T) {
	d := newTestDaemon(t, reviewTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeNative{})
	fr := &fakeReview{}
	fr.install(d)
	if _, err := d.handleReview(context.Background(), "ghost"); err == nil {
		t.Error("handleReview must error for an unknown session")
	}
	if fr.callCount() != 0 {
		t.Error("unknown session must not run a review")
	}
}

// --- manual `lola review` uses its OWN ctx, not the cycle budget --------------

// The manual command runs on its own socket-handler goroutine, concurrently with
// the observe loop. If it adopted the observe cycle's shared review budget ctx, a
// concurrently-finishing cycle would cancel the in-flight manual exec — surfacing
// a spurious failure and (worse) leaving the ReviewedPR guard stamped, so neither
// path re-reviews. It must run under its own caller ctx instead.
func TestHandleReviewUsesCallerCtxNotCycleBudget(t *testing.T) {
	d := newTestDaemon(t, reviewTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeNative{})
	(&fakeReactSeams{}).install(d)
	fr := &fakeReview{findings: "MANUAL-FINDING"}
	fr.install(d)

	s := reactSess("FE-1", "review_pending", openPR(7, "MERGEABLE", "", "pass"))
	s.AtPrompt = true
	d.sessions.Upsert(s)

	// Model an observe cycle that installed a review budget ctx and has since
	// finished, firing its deferred cancel — the ctx is now cancelled but still
	// installed (a cycle running concurrently on the observe goroutine).
	cycleCtx, cancel := context.WithCancel(context.Background())
	cancel()
	d.setReviewCycleCtx(cycleCtx)

	data, err := d.handleReview(context.Background(), s.ID)
	if err != nil {
		t.Fatalf("manual review must not fail because a concurrent cycle's budget was cancelled: %v", err)
	}
	if got := fr.ctxErr(); got != nil {
		t.Errorf("manual review exec must run under its own live caller ctx, not the cancelled cycle budget; ctx.Err() = %v", got)
	}
	if !data.Ran || !strings.Contains(data.Findings, "MANUAL-FINDING") {
		t.Errorf("manual review data = %+v, want ran with the findings", data)
	}
}

// The complement of the above: the in-cycle auto-trigger DOES run under the
// shared per-cycle budget, so a shutdown/cycle cancellation aborts its exec.
func TestReviewAutoTriggerUsesCycleBudget(t *testing.T) {
	d := newTestDaemon(t, reviewTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeNative{})
	(&fakeReactSeams{}).install(d)
	fr := &fakeReview{findings: "AUTO"}
	fr.install(d)

	s := reactSess("FE-1", "review_pending", openPR(7, "MERGEABLE", "", "pass"))
	s.AtPrompt = true
	d.sessions.Upsert(s)

	cycleCtx, cancel := context.WithCancel(context.Background())
	cancel()
	d.setReviewCycleCtx(cycleCtx)

	// A live caller ctx: only the cycle budget is cancelled. The auto-trigger must
	// still adopt the cycle budget, so the exec sees the cancellation.
	d.reviewOnPROpen(context.Background(), s)
	if fr.callCount() != 1 {
		t.Fatalf("auto-trigger must run the exec, got %d", fr.callCount())
	}
	if fr.ctxErr() == nil {
		t.Error("auto-trigger exec must run under the shared cycle budget (cancelled here), but saw a live ctx")
	}
}

// --- full observe cycle wires the trigger + budget ----------------------------

// A PR-open observed through the real observe cycle fires the review once and
// routes it, proving the observer wiring (budget + trigger) is live.
func TestObserveNativeFiresReviewOnPROpen(t *testing.T) {
	d := newTestDaemon(t, reviewTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeNative{alive: map[string]bool{}})
	seams := &fakeReactSeams{}
	seams.install(d)
	fr := &fakeReview{findings: "OBS-FINDING"}
	fr.install(d)

	s := nativeSess("FE-1", "idle")
	s.IssueUUID = "uuid-fe-1"
	s.AtPrompt = true
	d.sessions.Upsert(s)
	// The observer's PR seam reports an open PR; a dead pane keeps status stable.
	obs := &fakeObsSeams{pr: openPR(7, "MERGEABLE", "", "pass")}
	obs.install(d)

	d.observe(context.Background())

	if fr.callCount() != 1 {
		t.Fatalf("observe must fire the review once on PR-open, got %d execs", fr.callCount())
	}
	if len(seams.sendCalls()) != 1 {
		t.Errorf("observe must route the findings to the worker, got %d sends", len(seams.sendCalls()))
	}
	if got := findSession(t, d.sessions.Snapshot(), s.ID); got.ReviewedPR != 7 {
		t.Errorf("ReviewedPR = %d, want 7 after the observed PR-open review", got.ReviewedPR)
	}
}
