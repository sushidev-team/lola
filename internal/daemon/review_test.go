package daemon

// Tests for the flexible review system's PASS shapes and transports (reviewer.go
// + review.go): the PR-open auto-trigger, the once-per-PR-per-kind guard, the
// sanitized + idle-gated worker hand-off (with deferral), the clean-review path,
// the notify / linear / github sinks, fallback chains, late binding, graceful
// skip on a missing/erroring provider, back-compat with the legacy [review]
// table, and the `lola review` force command.
//
// All seams are hermetic fakes — no coderabbit, claude, gh, tmux, git, or
// network. fakeReview stands in for a pass exec seam; fakePostPR for the github
// WRITE seam; fakeReactSeams (reactions_test.go) provides send-keys + the notifier.

import (
	"context"
	"errors"
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

// --- shared helpers (used by review_test.go and coderabbit_test.go) -----------

// setProviders installs a descriptor set directly (bypassing config resolution)
// for catalog-shaped tests. The daemon-side reviewProvider type is package-local,
// so tests can build any kind/transport/fallback combination the config package's
// unexported provKind would otherwise hide.
func setProviders(d *Daemon, provs ...reviewProvider) {
	d.mu.Lock()
	d.reviewProviders = provs
	d.mu.Unlock()
}

// syncProviders builds the descriptor set from the daemon's config (the same call
// Run/handleReload make), so legacy [review]/[coderabbit] tables synthesize into
// providers exactly as in production.
func syncProviders(d *Daemon) {
	d.mu.Lock()
	d.setReviewProvidersLocked(d.cfg)
	d.mu.Unlock()
}

func cliDesc() reviewProvider {
	return reviewProvider{
		Kind: kindCoderabbitCLI, Shape: shapePass, Enabled: true, OnPROpen: true,
		Transports: config.TransportSet{config.TransportLola}, Notify: true, SendToAgent: true, Handoff: handoffFull,
	}
}

func claudeDesc() reviewProvider {
	return reviewProvider{
		Kind: kindClaudeSession, Shape: shapePass, Enabled: true, OnPROpen: true,
		Transports: config.TransportSet{config.TransportLola}, Notify: true, SendToAgent: true, Handoff: handoffFull,
	}
}

func watchDesc() reviewProvider {
	return reviewProvider{
		Kind: kindCoderabbitWatch, Shape: shapeWatch, Enabled: true,
		Transports: config.TransportSet{config.TransportLola}, Notify: true, SendToAgent: true,
		Handoff: handoffPointer, Author: config.DefaultCodeRabbitAuthor,
	}
}

// reviewCall records one pass exec (its worktree dir + base branch).
type reviewCall struct{ dir, base string }

// fakeReview installs a counting fake for a pass exec seam (cli or claude).
type fakeReview struct {
	mu         sync.Mutex
	calls      []reviewCall
	findings   string
	err        error
	lastCtxErr error
	onCall     func()
}

func (f *fakeReview) fn() func(ctx context.Context, dir, base string) (string, error) {
	return func(ctx context.Context, dir, base string) (string, error) {
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

// install wires the fake onto the coderabbit-cli pass seam (the default kind).
func (f *fakeReview) install(d *Daemon) { f.installKind(d, kindCoderabbitCLI) }

// installKind wires the fake onto a specific pass kind's seam.
func (f *fakeReview) installKind(d *Daemon, k provKind) {
	fn := f.fn()
	d.mu.Lock()
	switch k {
	case kindClaudeSession:
		d.claudeReviewRun = fn
	default:
		d.reviewRun = fn
	}
	d.mu.Unlock()
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

// postCall records one github PR-comment post.
type postCall struct {
	repo string
	pr   int
	body string
}

// fakePostPR installs a counting fake for the github WRITE seam (d.postPRComment).
type fakePostPR struct {
	mu    sync.Mutex
	calls []postCall
	err   error
}

func (f *fakePostPR) install(d *Daemon) {
	d.mu.Lock()
	d.postPRComment = func(_ context.Context, repo string, pr int, body string) error {
		f.mu.Lock()
		f.calls = append(f.calls, postCall{repo, pr, body})
		err := f.err
		f.mu.Unlock()
		return err
	}
	d.mu.Unlock()
}

func (f *fakePostPR) callsCopy() []postCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]postCall(nil), f.calls...)
}

func (f *fakePostPR) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

// fakeLogin installs a counting fake for the self-login seam (d.authedLogin).
type fakeLogin struct {
	mu    sync.Mutex
	calls int
	login string
	err   error
}

func (f *fakeLogin) install(d *Daemon) {
	d.mu.Lock()
	d.authedLogin = func(_ context.Context) (string, error) {
		f.mu.Lock()
		f.calls++
		login, err := f.login, f.err
		f.mu.Unlock()
		return login, err
	}
	d.mu.Unlock()
}

func (f *fakeLogin) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// reviewTestConfig is a native test config with the LEGACY [review] table enabled
// (on_pr_open + send_to_agent on, comment off), so setReviewProvidersLocked
// synthesizes a coderabbit-cli provider from it — the back-compat oracle.
func reviewTestConfig(polls ...config.Project) *config.Config {
	c := nativeTestConfig(polls...)
	c.Review = config.ReviewConfig{
		Enabled:        true,
		OnPROpen:       true,
		SendToAgent:    true,
		TimeoutSeconds: config.DefaultReviewTimeoutSeconds,
	}
	return c
}

// runtime_id resolves the store ID for a p1 issue, matching nativeSess.
func runtime_id(ident string) string { return nativeSess(ident, "").ID }

// --- PR-open auto-trigger: exec against the worktree, route to worker + notify -

func TestReviewOnPROpenRunsRoutesToWorkerAndNotifies(t *testing.T) {
	d := newTestDaemon(t, reviewTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeNative{})
	syncProviders(d)
	seams := &fakeReactSeams{}
	seams.install(d)
	fr := &fakeReview{findings: "FINDING-XYZ: fix the nil deref"}
	// The chain guard must be stamped BEFORE the (long) exec — assert it is set
	// (keyed by kind) while the exec runs.
	fr.onCall = func() {
		if got, _ := d.sessions.Get(runtime_id("FE-1")); got.ReviewedPRs["coderabbit-cli"] != 7 {
			t.Errorf("ReviewedPRs[cli] must be stamped BEFORE the exec, got %d", got.ReviewedPRs["coderabbit-cli"])
		}
	}
	fr.install(d)

	s := reactSess("FE-1", "review_pending", openPR(7, "MERGEABLE", "", "pass"))
	s.AtPrompt = true
	d.sessions.Upsert(s)

	d.runReviewProviders(context.Background(), s)

	calls := fr.callsCopy()
	if len(calls) != 1 {
		t.Fatalf("want one review exec, got %d", len(calls))
	}
	wantDir := filepath.Join(d.home, "worktrees", "p1", s.ID)
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
	if got.ReviewedPRs["coderabbit-cli"] != 7 {
		t.Errorf("ReviewedPRs[cli] = %d, want 7", got.ReviewedPRs["coderabbit-cli"])
	}
	if got.AtPrompt {
		t.Error("AtPrompt must be consumed after the hand-off")
	}
	if got.PendingHandoffs["coderabbit-cli"] != "" {
		t.Errorf("PendingHandoffs[cli] must be clear after a delivered hand-off, got %q", got.PendingHandoffs["coderabbit-cli"])
	}
}

// --- fire once per PR per kind; a NEW PR number re-runs -----------------------

func TestReviewFiresOncePerPRAndRerunsOnNewPR(t *testing.T) {
	d := newTestDaemon(t, reviewTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeNative{})
	syncProviders(d)
	(&fakeReactSeams{}).install(d)
	fr := &fakeReview{findings: "ISSUE"}
	fr.install(d)

	s := reactSess("FE-1", "review_pending", openPR(7, "MERGEABLE", "", "pass"))
	s.AtPrompt = true
	d.sessions.Upsert(s)

	d.runReviewProviders(context.Background(), s)
	if fr.callCount() != 1 {
		t.Fatalf("first PR-open must run the review, got %d", fr.callCount())
	}

	got, _ := d.sessions.Get(s.ID)
	d.runReviewProviders(context.Background(), got)
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
	d.runReviewProviders(context.Background(), got)
	if fr.callCount() != 2 {
		t.Errorf("a new PR number must re-trigger the review, got %d execs", fr.callCount())
	}
	got, _ = d.sessions.Get(s.ID)
	if got.ReviewedPRs["coderabbit-cli"] != 8 {
		t.Errorf("ReviewedPRs[cli] = %d, want 8 after the new PR review", got.ReviewedPRs["coderabbit-cli"])
	}
}

// --- worker busy → deferred, not dropped; delivered when idle -----------------

func TestReviewDefersWhenWorkerBusyThenFlushes(t *testing.T) {
	d := newTestDaemon(t, reviewTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeNative{})
	syncProviders(d)
	seams := &fakeReactSeams{}
	seams.install(d)
	fr := &fakeReview{findings: "DEFER-ME"}
	fr.install(d)

	s := reactSess("FE-1", "review_pending", openPR(7, "MERGEABLE", "", "pass"))
	s.AtPrompt = false // worker mid-turn
	d.sessions.Upsert(s)

	d.runReviewProviders(context.Background(), s)

	if len(seams.sendCalls()) != 0 {
		t.Fatal("a mid-turn worker must not be sent-keys")
	}
	got, _ := d.sessions.Get(s.ID)
	if !strings.Contains(got.PendingHandoffs["coderabbit-cli"], "DEFER-ME") {
		t.Errorf("findings must be stashed for later delivery, got %q", got.PendingHandoffs["coderabbit-cli"])
	}
	if got.ReviewedPRs["coderabbit-cli"] != 7 {
		t.Errorf("ReviewedPRs[cli] must be stamped even when the hand-off defers, got %d", got.ReviewedPRs["coderabbit-cli"])
	}
	if len(seams.notesByPriority(notify.Action)) != 1 {
		t.Errorf("want one Action notification even on a deferred hand-off, got %+v", seams.notes)
	}

	// Worker returns to its prompt → the deferred hand-off flushes once.
	d.sessions.Update(s.ID, func(cur *session.Session) bool { cur.AtPrompt = true; return true })
	d.flushReviewHandoffs(context.Background(), s.ID)

	sends := seams.sendCalls()
	if len(sends) != 1 || !strings.Contains(sends[0].text, "DEFER-ME") {
		t.Fatalf("deferred hand-off must flush once the worker is idle, got %+v", sends)
	}
	got, _ = d.sessions.Get(s.ID)
	if got.PendingHandoffs["coderabbit-cli"] != "" {
		t.Errorf("PendingHandoffs[cli] must clear after a delivered hand-off, got %q", got.PendingHandoffs["coderabbit-cli"])
	}

	// A second flush is a no-op (nothing pending).
	d.flushReviewHandoffs(context.Background(), s.ID)
	if len(seams.sendCalls()) != 1 {
		t.Errorf("flush must not re-send a delivered hand-off, got %d sends", len(seams.sendCalls()))
	}
}

// --- clean review → no worker message, Info notify only -----------------------

func TestReviewCleanNoWorkerMessageInfoNotify(t *testing.T) {
	d := newTestDaemon(t, reviewTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeNative{})
	syncProviders(d)
	seams := &fakeReactSeams{}
	seams.install(d)
	fr := &fakeReview{findings: ""} // clean
	fr.install(d)

	s := reactSess("FE-1", "review_pending", openPR(7, "MERGEABLE", "", "pass"))
	s.AtPrompt = true
	d.sessions.Upsert(s)

	d.runReviewProviders(context.Background(), s)

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
	cfg := nativeTestConfig(nativePoll("p1")) // no [review], no catalog
	d := newTestDaemon(t, cfg, &linear.Fake{}, &fakeNative{})
	syncProviders(d)
	seams := &fakeReactSeams{}
	seams.install(d)

	s := reactSess("FE-1", "review_pending", openPR(7, "MERGEABLE", "", "pass"))
	s.AtPrompt = true
	d.sessions.Upsert(s)

	d.runReviewProviders(context.Background(), s) // must be a no-op

	if len(seams.sendCalls()) != 0 || seams.noteCount() != 0 {
		t.Error("review disabled must make zero send/notify calls")
	}
	if got, _ := d.sessions.Get(s.ID); got.ReviewedPRs["coderabbit-cli"] != 0 {
		t.Errorf("review disabled must not stamp any guard, got %d", got.ReviewedPRs["coderabbit-cli"])
	}
}

// --- provider error (ErrAuth) → graceful skip, no fallback, guard left set -----

func TestReviewProviderErrorGraceful(t *testing.T) {
	d := newTestDaemon(t, reviewTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeNative{})
	syncProviders(d)
	seams := &fakeReactSeams{}
	seams.install(d)
	fr := &fakeReview{err: review.ErrAuth}
	fr.install(d)

	s := reactSess("FE-1", "review_pending", openPR(7, "MERGEABLE", "", "pass"))
	s.AtPrompt = true
	d.sessions.Upsert(s)

	d.runReviewProviders(context.Background(), s) // must not panic

	if len(seams.sendCalls()) != 0 {
		t.Error("an errored review must never message the worker")
	}
	if seams.noteCount() != 0 {
		t.Error("an errored review surfaces nothing (findings untrusted/untouched on error)")
	}
	if got, _ := d.sessions.Get(s.ID); got.ReviewedPRs["coderabbit-cli"] != 7 {
		t.Errorf("ReviewedPRs[cli] must remain stamped after an errored review, got %d", got.ReviewedPRs["coderabbit-cli"])
	}
}

// --- comment_on_linear (legacy synth) → findings posted as a Linear comment ---

func TestReviewCommentsOnLinear(t *testing.T) {
	cfg := reviewTestConfig(nativePoll("p1"))
	cfg.Review.CommentOnLinear = true
	fake := &linear.Fake{}
	d := newTestDaemon(t, cfg, fake, &fakeNative{})
	syncProviders(d)
	(&fakeReactSeams{}).install(d)
	fr := &fakeReview{findings: "LINEAR-FINDING"}
	fr.install(d)

	s := reactSess("FE-1", "review_pending", openPR(7, "MERGEABLE", "", "pass"))
	s.AtPrompt = true
	d.sessions.Upsert(s)

	d.runReviewProviders(context.Background(), s)

	bodies := fake.CommentsByIssue[s.IssueUUID]
	if len(bodies) != 1 || !strings.Contains(bodies[0], "LINEAR-FINDING") {
		t.Fatalf("want one Linear comment carrying the findings, got %+v", bodies)
	}
}

// --- untrusted findings sanitized before the send-keys hand-off ---------------

func TestReviewSanitizesFindingsBeforeSend(t *testing.T) {
	d := newTestDaemon(t, reviewTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeNative{})
	syncProviders(d)
	seams := &fakeReactSeams{}
	seams.install(d)
	fr := &fakeReview{findings: "line 1\rline 2\x1b[31mRED\x1b[0m\x00\n\tKEEP"}
	fr.install(d)

	s := reactSess("FE-1", "review_pending", openPR(7, "MERGEABLE", "", "pass"))
	s.AtPrompt = true
	d.sessions.Upsert(s)

	d.runReviewProviders(context.Background(), s)

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

// --- fallback chain: primary can't answer → fallback runs, routes via primary --

func TestReviewFallbackAdvancesOnQuota(t *testing.T) {
	d := newTestDaemon(t, nativeTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeNative{})
	seams := &fakeReactSeams{}
	seams.install(d)

	// coderabbit-cli primary with a claude-session fallback; claude is fallback-only.
	cli := cliDesc()
	cli.Fallback = []provKind{kindClaudeSession}
	claude := claudeDesc()
	setProviders(d, cli, claude)

	prim := &fakeReview{err: review.ErrQuota}       // primary over quota
	fb := &fakeReview{findings: "FALLBACK-FINDING"}  // fallback answers
	prim.installKind(d, kindCoderabbitCLI)
	fb.installKind(d, kindClaudeSession)

	s := reactSess("FE-1", "review_pending", openPR(7, "MERGEABLE", "", "pass"))
	s.AtPrompt = true
	d.sessions.Upsert(s)

	d.runReviewProviders(context.Background(), s)

	if prim.callCount() != 1 {
		t.Fatalf("primary must be attempted once, got %d", prim.callCount())
	}
	if fb.callCount() != 1 {
		t.Fatalf("fallback must run after ErrQuota, got %d", fb.callCount())
	}
	// The claude fallback's findings route via the PRIMARY's transports (cli's
	// send-to-agent + notify) with the PRIMARY's preamble.
	sends := seams.sendCalls()
	if len(sends) != 1 || !strings.Contains(sends[0].text, "FALLBACK-FINDING") {
		t.Fatalf("fallback findings must route via the primary's worker sink, got %+v", sends)
	}
	if !strings.Contains(sends[0].text, "CodeRabbit") {
		t.Errorf("fallback delivery uses the PRIMARY's (coderabbit) preamble, got %q", sends[0].text)
	}
	// The chain guard is stamped on the PRIMARY kind, so re-running does not re-fire.
	got, _ := d.sessions.Get(s.ID)
	if got.ReviewedPRs["coderabbit-cli"] != 7 {
		t.Errorf("chain guard must be stamped on the PRIMARY kind, got %d", got.ReviewedPRs["coderabbit-cli"])
	}
	if got.ReviewedPRs["claude-session"] != 0 {
		t.Errorf("a fallback-only provider must not stamp its own guard, got %d", got.ReviewedPRs["claude-session"])
	}
	d.runReviewProviders(context.Background(), got)
	if prim.callCount() != 1 || fb.callCount() != 1 {
		t.Errorf("chain guard must suppress a second run, got prim=%d fb=%d", prim.callCount(), fb.callCount())
	}
}

// primary success → fallback not run.
func TestReviewFallbackNotRunOnPrimarySuccess(t *testing.T) {
	d := newTestDaemon(t, nativeTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeNative{})
	(&fakeReactSeams{}).install(d)
	cli := cliDesc()
	cli.Fallback = []provKind{kindClaudeSession}
	claude := claudeDesc()
	setProviders(d, cli, claude)

	prim := &fakeReview{findings: "PRIMARY-OK"}
	fb := &fakeReview{findings: "SHOULD-NOT-RUN"}
	prim.installKind(d, kindCoderabbitCLI)
	fb.installKind(d, kindClaudeSession)

	s := reactSess("FE-1", "review_pending", openPR(7, "MERGEABLE", "", "pass"))
	s.AtPrompt = true
	d.sessions.Upsert(s)

	d.runReviewProviders(context.Background(), s)
	if prim.callCount() != 1 || fb.callCount() != 0 {
		t.Errorf("primary success must not run the fallback, got prim=%d fb=%d", prim.callCount(), fb.callCount())
	}
}

// ErrExit / ErrAuth → graceful skip, NO fallback.
func TestReviewFallbackNotRunOnExit(t *testing.T) {
	d := newTestDaemon(t, nativeTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeNative{})
	(&fakeReactSeams{}).install(d)
	cli := cliDesc()
	cli.Fallback = []provKind{kindClaudeSession}
	claude := claudeDesc()
	setProviders(d, cli, claude)

	prim := &fakeReview{err: review.ErrExit}
	fb := &fakeReview{findings: "SHOULD-NOT-RUN"}
	prim.installKind(d, kindCoderabbitCLI)
	fb.installKind(d, kindClaudeSession)

	s := reactSess("FE-1", "review_pending", openPR(7, "MERGEABLE", "", "pass"))
	s.AtPrompt = true
	d.sessions.Upsert(s)

	d.runReviewProviders(context.Background(), s)
	if prim.callCount() != 1 || fb.callCount() != 0 {
		t.Errorf("ErrExit must NOT fall through to the fallback, got prim=%d fb=%d", prim.callCount(), fb.callCount())
	}
	if got, _ := d.sessions.Get(s.ID); got.ReviewedPRs["coderabbit-cli"] != 7 {
		t.Errorf("guard must be left set after an ErrExit skip, got %d", got.ReviewedPRs["coderabbit-cli"])
	}
}

// A fallback-only provider whose seam is unavailable (nil) is skipped; the chain
// then exhausts gracefully (per-exec self-bound: a timed-out primary still lets
// the chain advance to the next entry).
func TestReviewFallbackTimeoutThenUnavailable(t *testing.T) {
	d := newTestDaemon(t, nativeTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeNative{})
	(&fakeReactSeams{}).install(d)
	cli := cliDesc()
	cli.Fallback = []provKind{kindClaudeSession}
	claude := claudeDesc()
	setProviders(d, cli, claude)

	prim := &fakeReview{err: review.ErrTimeout}
	prim.installKind(d, kindCoderabbitCLI)
	// claude seam left nil (unavailable) → the chain advances past it and exhausts.

	s := reactSess("FE-1", "review_pending", openPR(7, "MERGEABLE", "", "pass"))
	s.AtPrompt = true
	d.sessions.Upsert(s)

	d.runReviewProviders(context.Background(), s) // must not panic
	if prim.callCount() != 1 {
		t.Fatalf("primary must be attempted once on ErrTimeout, got %d", prim.callCount())
	}
	if got, _ := d.sessions.Get(s.ID); got.ReviewedPRs["coderabbit-cli"] != 7 {
		t.Errorf("guard must be left set on an exhausted chain, got %d", got.ReviewedPRs["coderabbit-cli"])
	}
}

// --- late binding: a fake installed AFTER setup still wins --------------------

func TestReviewLateBindingSeam(t *testing.T) {
	d := newTestDaemon(t, reviewTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeNative{})
	// setReviewProvidersLocked runs first (real client nil: no coderabbit on PATH).
	syncProviders(d)
	(&fakeReactSeams{}).install(d)
	// The fake seam is installed AFTER setup; the chain reads the seam at call time.
	fr := &fakeReview{findings: "LATE"}
	fr.install(d)

	s := reactSess("FE-1", "review_pending", openPR(7, "MERGEABLE", "", "pass"))
	s.AtPrompt = true
	d.sessions.Upsert(s)

	d.runReviewProviders(context.Background(), s)
	if fr.callCount() != 1 {
		t.Errorf("a fake installed after setReviewProvidersLocked must still win (late binding), got %d", fr.callCount())
	}
}

// --- github transport: post once per PR, human full text, idempotent ----------

func TestReviewGithubSinkPostsFullTextOncePerPR(t *testing.T) {
	d := newTestDaemon(t, nativeTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeNative{})
	(&fakeReactSeams{}).install(d)
	cli := cliDesc()
	cli.Transports = config.TransportSet{config.TransportLola, config.TransportGitHub}
	cli.SendToAgent = false // isolate the github sink
	cli.Notify = false
	setProviders(d, cli)
	fp := &fakePostPR{}
	fp.install(d)

	// Untrusted findings with a CR + control byte: the github sink must post them
	// VERBATIM (human sink, no sanitize), unlike the worker sink.
	fr := &fakeReview{findings: "GH-FINDING\rwith\x00control"}
	fr.install(d)

	s := reactSess("FE-1", "review_pending", openPR(7, "MERGEABLE", "", "pass"))
	s.AtPrompt = true
	d.sessions.Upsert(s)

	d.runReviewProviders(context.Background(), s)

	calls := fp.callsCopy()
	if len(calls) != 1 {
		t.Fatalf("github sink must post once, got %d", len(calls))
	}
	if calls[0].repo != "acme/widgets" || calls[0].pr != 7 {
		t.Errorf("github post target = %s#%d, want acme/widgets#7", calls[0].repo, calls[0].pr)
	}
	if !strings.Contains(calls[0].body, "GH-FINDING") || !strings.ContainsRune(calls[0].body, '\r') {
		t.Errorf("github sink must post the FULL untrusted text (no sanitize), got %q", calls[0].body)
	}
	// Settle guard stamped → a second route does not re-post.
	got, _ := d.sessions.Get(s.ID)
	if got.PostedGitHubPRs["coderabbit-cli"] != 7 {
		t.Errorf("PostedGitHubPRs[cli] must be stamped after a successful post, got %d", got.PostedGitHubPRs["coderabbit-cli"])
	}
	d.routeFindings(context.Background(), got, cli, "GH-FINDING")
	if fp.count() != 1 {
		t.Errorf("github sink must be idempotent per PR, got %d posts", fp.count())
	}
}

// A CLEAN review never posts an (empty) github comment.
func TestReviewGithubSinkSkippedWhenClean(t *testing.T) {
	d := newTestDaemon(t, nativeTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeNative{})
	(&fakeReactSeams{}).install(d)
	cli := cliDesc()
	cli.Transports = config.TransportSet{config.TransportLola, config.TransportGitHub}
	setProviders(d, cli)
	fp := &fakePostPR{}
	fp.install(d)

	s := reactSess("FE-1", "review_pending", openPR(7, "MERGEABLE", "", "pass"))
	d.sessions.Upsert(s)

	d.routeFindings(context.Background(), s, cli, "") // clean
	if fp.count() != 0 {
		t.Errorf("a clean review must not post an empty github comment, got %d", fp.count())
	}
}

// A PERMANENT gh failure (422/403) stamps the settle guard so it never retries.
func TestReviewGithubSinkPermanentFailStampsGuard(t *testing.T) {
	d := newTestDaemon(t, nativeTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeNative{})
	(&fakeReactSeams{}).install(d)
	cli := cliDesc()
	cli.Transports = config.TransportSet{config.TransportLola, config.TransportGitHub}
	setProviders(d, cli)
	fp := &fakePostPR{err: errors.New("gh pr comment 7 --repo acme/widgets: HTTP 403: Resource not accessible by integration")}
	fp.install(d)

	s := reactSess("FE-1", "review_pending", openPR(7, "MERGEABLE", "", "pass"))
	d.sessions.Upsert(s)

	d.postGithubSink(context.Background(), s, cli, "FINDING")
	if fp.count() != 1 {
		t.Fatalf("first post must be attempted, got %d", fp.count())
	}
	got, _ := d.sessions.Get(s.ID)
	if got.PostedGitHubPRs["coderabbit-cli"] != 7 {
		t.Errorf("a permanent gh failure must SETTLE the guard, got %d", got.PostedGitHubPRs["coderabbit-cli"])
	}
	// Next cycle: no re-post (guard settled).
	d.postGithubSink(context.Background(), got, cli, "FINDING")
	if fp.count() != 1 {
		t.Errorf("a permanently-failed post must not retry, got %d posts", fp.count())
	}
}

// A TRANSIENT gh failure leaves the guard unstamped so it retries next cycle.
func TestReviewGithubSinkTransientFailRetries(t *testing.T) {
	d := newTestDaemon(t, nativeTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeNative{})
	(&fakeReactSeams{}).install(d)
	cli := cliDesc()
	cli.Transports = config.TransportSet{config.TransportLola, config.TransportGitHub}
	setProviders(d, cli)
	fp := &fakePostPR{err: errors.New("gh pr comment 7 --repo acme/widgets: HTTP 502: Bad Gateway")}
	fp.install(d)

	s := reactSess("FE-1", "review_pending", openPR(7, "MERGEABLE", "", "pass"))
	d.sessions.Upsert(s)

	d.postGithubSink(context.Background(), s, cli, "FINDING")
	got, _ := d.sessions.Get(s.ID)
	if got.PostedGitHubPRs["coderabbit-cli"] != 0 {
		t.Errorf("a transient gh failure must NOT settle the guard, got %d", got.PostedGitHubPRs["coderabbit-cli"])
	}
	d.postGithubSink(context.Background(), got, cli, "FINDING")
	if fp.count() != 2 {
		t.Errorf("a transient failure must retry next cycle, got %d posts", fp.count())
	}
}

// github on a coderabbit-watch is rejected by config validation.
func TestGithubOnWatchRejected(t *testing.T) {
	cfg := nativeTestConfig(nativePoll("p1"))
	cfg.ReviewProviders = []config.ReviewProvider{{
		Provider:   "coderabbit-watch",
		Enabled:    true,
		Transports: config.TransportSet{config.TransportGitHub, config.TransportLola},
		Author:     config.DefaultCodeRabbitAuthor,
	}}
	if err := cfg.Validate(); err == nil {
		t.Error("validation must reject the github transport on a coderabbit-watch provider")
	}
}

// --- `lola review` forces a run ignoring the guard ----------------------------

func TestHandleReviewForcesIgnoringGuard(t *testing.T) {
	d := newTestDaemon(t, reviewTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeNative{})
	syncProviders(d)
	seams := &fakeReactSeams{}
	seams.install(d)
	fr := &fakeReview{findings: "FORCED-FINDING"}
	fr.install(d)

	// The PR was ALREADY reviewed (guard set) — the auto-trigger would skip it.
	s := reactSess("FE-1", "review_pending", openPR(7, "MERGEABLE", "", "pass"))
	s.AtPrompt = true
	s.ReviewedPRs = map[string]int{"coderabbit-cli": 7}
	d.sessions.Upsert(s)

	d.runReviewProviders(context.Background(), s)
	if fr.callCount() != 0 {
		t.Fatalf("auto-trigger must respect the guard, got %d execs", fr.callCount())
	}

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
	syncProviders(d)
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
	syncProviders(d)
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
	syncProviders(d)
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

func TestHandleReviewUsesCallerCtxNotCycleBudget(t *testing.T) {
	d := newTestDaemon(t, reviewTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeNative{})
	syncProviders(d)
	(&fakeReactSeams{}).install(d)
	fr := &fakeReview{findings: "MANUAL-FINDING"}
	fr.install(d)

	s := reactSess("FE-1", "review_pending", openPR(7, "MERGEABLE", "", "pass"))
	s.AtPrompt = true
	d.sessions.Upsert(s)

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

// The in-cycle auto-trigger DOES run under the shared per-cycle budget.
func TestReviewAutoTriggerUsesCycleBudget(t *testing.T) {
	d := newTestDaemon(t, reviewTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeNative{})
	syncProviders(d)
	(&fakeReactSeams{}).install(d)
	fr := &fakeReview{findings: "AUTO"}
	fr.install(d)

	s := reactSess("FE-1", "review_pending", openPR(7, "MERGEABLE", "", "pass"))
	s.AtPrompt = true
	d.sessions.Upsert(s)

	cycleCtx, cancel := context.WithCancel(context.Background())
	cancel()
	d.setReviewCycleCtx(cycleCtx)

	d.runReviewProviders(context.Background(), s)
	if fr.callCount() != 1 {
		t.Fatalf("auto-trigger must run the exec, got %d", fr.callCount())
	}
	if fr.ctxErr() == nil {
		t.Error("auto-trigger exec must run under the shared cycle budget (cancelled here), but saw a live ctx")
	}
}

// --- full observe cycle wires the trigger + budget ----------------------------

func TestObserveNativeFiresReviewOnPROpen(t *testing.T) {
	d := newTestDaemon(t, reviewTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeNative{alive: map[string]bool{}})
	syncProviders(d)
	seams := &fakeReactSeams{}
	seams.install(d)
	fr := &fakeReview{findings: "OBS-FINDING"}
	fr.install(d)

	s := nativeSess("FE-1", "idle")
	s.IssueUUID = "uuid-fe-1"
	s.AtPrompt = true
	d.sessions.Upsert(s)
	obs := &fakeObsSeams{pr: openPR(7, "MERGEABLE", "", "pass")}
	obs.install(d)

	d.observe(context.Background())

	if fr.callCount() != 1 {
		t.Fatalf("observe must fire the review once on PR-open, got %d execs", fr.callCount())
	}
	if len(seams.sendCalls()) != 1 {
		t.Errorf("observe must route the findings to the worker, got %d sends", len(seams.sendCalls()))
	}
	if got := findSession(t, d.sessions.Snapshot(), s.ID); got.ReviewedPRs["coderabbit-cli"] != 7 {
		t.Errorf("ReviewedPRs[cli] = %d, want 7 after the observed PR-open review", got.ReviewedPRs["coderabbit-cli"])
	}
}
