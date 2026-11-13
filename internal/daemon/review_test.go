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
	"time"

	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/linear"
	"github.com/sushidev-team/lola/internal/notify"
	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/review"
	"github.com/sushidev-team/lola/internal/reviewagent"
	"github.com/sushidev-team/lola/internal/session"
	"github.com/sushidev-team/lola/internal/state"
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

// kindClaudeSession is the claude agent pass, named here for the many tests that
// build a chain around it. It is deliberately NOT a constant in reviewer.go: the
// daemon names only the kinds it special-cases, and this one is no longer among
// them (its dispatch, labels and client all come from its config-side family).
const kindClaudeSession provKind = "claude-session"

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

func (f *fakeReview) fn() passRun {
	return func(ctx context.Context, _, dir, base string) (string, error) {
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

// installKind wires the fake onto a specific pass kind's seam. Every kind goes
// through the same map, so a fake for a new one needs no case here.
func (f *fakeReview) installKind(d *Daemon, k provKind) { d.setPassRun(k, f.fn()) }

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

// --- idle-notify parking is deliverable; a permission prompt is not -----------

// parkSession puts a stored session in the "agent stopped for input" shape the
// notification hook produces: AtPrompt closed, the agent axis parked with the
// given reason (handleHookEvent's "notification" branch).
func parkSession(t *testing.T, d *Daemon, id string, reason state.InputReason) {
	t.Helper()
	d.sessions.Update(id, func(cur *session.Session) bool {
		cur.SetAgentState(state.AgentWaitingInput, "", time.Now())
		cur.InputReason = reason
		cur.AtPrompt = false
		cur.AtPromptVerified = true
		return true
	})
}

// A hand-off deferred at PR-open must still land once Claude Code's idle
// notification has parked the worker — that notification CLOSES AtPrompt, so
// before handoffDeliverable this stash could only ever be delivered in the sliver
// between the Stop hook and the notification, and in practice never was.
func TestReviewFlushesToIdleNotifyParkedWorker(t *testing.T) {
	d := newTestDaemon(t, reviewTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeNative{})
	syncProviders(d)
	seams := &fakeReactSeams{}
	seams.install(d)
	fr := &fakeReview{findings: "PARKED-FINDING"}
	fr.install(d)

	s := reactSess("FE-1", "review_pending", openPR(7, "MERGEABLE", "", "pass"))
	s.AtPrompt = false // mid-turn at PR-open, as it always is in production
	d.sessions.Upsert(s)
	d.runReviewProviders(context.Background(), s)
	if len(seams.sendCalls()) != 0 {
		t.Fatal("a mid-turn worker must not be sent-keys")
	}

	// The turn ends and Claude Code parks the agent with its idle nudge.
	parkSession(t, d, s.ID, state.InputIdleNotify)
	d.flushReviewHandoffs(context.Background(), s.ID)

	sends := seams.sendCalls()
	if len(sends) != 1 || !strings.Contains(sends[0].text, "PARKED-FINDING") {
		t.Fatalf("an idle-notify parked worker must receive the deferred hand-off, got %+v", sends)
	}
	got, _ := d.sessions.Get(s.ID)
	if got.PendingHandoffs["coderabbit-cli"] != "" {
		t.Errorf("PendingHandoffs[cli] must clear after delivery, got %q", got.PendingHandoffs["coderabbit-cli"])
	}
	// The send resumed the agent: the axis must say so, which is also what stops
	// the widened gate from re-delivering on the next cycle.
	if got.AgentState != state.AgentWorking {
		t.Errorf("AgentState = %q after a delivered hand-off, want working", got.AgentState)
	}
	d.flushReviewHandoffs(context.Background(), s.ID)
	if len(seams.sendCalls()) != 1 {
		t.Errorf("a delivered hand-off must not re-send, got %d sends", len(seams.sendCalls()))
	}
}

// idleSession puts a stored session in the shape the observer's PANE reconcile
// produces: the axis resting on AgentIdle with the AtPrompt gate closed and no
// input reason. No hook is involved, so AtPromptVerified stays whatever an
// earlier hook left it — which is why this state needs live pane proof.
func idleSession(t *testing.T, d *Daemon, id string) {
	t.Helper()
	d.sessions.Update(id, func(cur *session.Session) bool {
		cur.SetAgentState(state.AgentIdle, "", time.Now())
		cur.InputReason = ""
		cur.AtPrompt = false
		cur.AtPromptVerified = true // a stale hook verdict must NOT be taken as proof
		return true
	})
}

// A worker the pane reconcile parked on AgentIdle (AtPrompt closed, no
// notification) must receive its deferred hand-off once its pane shows a
// prompt. Before this case existed such a session was unreachable and its
// findings sat in PendingHandoffs for the life of the session.
func TestReviewFlushesToPaneIdleWorker(t *testing.T) {
	d := newTestDaemon(t, reviewTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeNative{})
	syncProviders(d)
	seams := &fakeReactSeams{}
	seams.install(d)
	fr := &fakeReview{findings: "PANE-IDLE-FINDING"}
	fr.install(d)
	d.paneTail = func(context.Context, string, int) (string, error) { return paneWaiting, nil }

	s := reactSess("FE-1", "review_pending", openPR(7, "MERGEABLE", "", "pass"))
	s.AtPrompt = false // mid-turn at PR-open
	d.sessions.Upsert(s)
	d.runReviewProviders(context.Background(), s)
	if len(seams.sendCalls()) != 0 {
		t.Fatal("a mid-turn worker must not be sent-keys")
	}

	idleSession(t, d, s.ID)
	d.flushReviewHandoffs(context.Background(), s.ID)

	sends := seams.sendCalls()
	if len(sends) != 1 || !strings.Contains(sends[0].text, "PANE-IDLE-FINDING") {
		t.Fatalf("a pane-idle worker must receive the deferred hand-off, got %+v", sends)
	}
	if got, _ := d.sessions.Get(s.ID); got.PendingHandoffs["coderabbit-cli"] != "" {
		t.Errorf("PendingHandoffs[cli] must clear after delivery, got %q", got.PendingHandoffs["coderabbit-cli"])
	}
}

// The pane is the ONLY evidence for a pane-derived idle: a stale AtPromptVerified
// from an earlier hook must not authorize a send into a pane that is mid-turn.
func TestReviewPaneIdleRequiresLivePaneProof(t *testing.T) {
	for _, tc := range []struct {
		name string
		pane string
		err  error
	}{
		{"mid-turn pane", paneWorking, nil},
		{"unreadable pane", paneUnknown, nil},
		{"capture fails", "", errors.New("boom")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := newTestDaemon(t, reviewTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeNative{})
			syncProviders(d)
			seams := &fakeReactSeams{}
			seams.install(d)
			d.paneTail = func(context.Context, string, int) (string, error) { return tc.pane, tc.err }

			s := reactSess("FE-1", "review_pending", openPR(7, "MERGEABLE", "", "pass"))
			s.PendingHandoffs = map[string]string{string(kindCoderabbitCLI): "NEEDS-PROOF"}
			d.sessions.Upsert(s)
			idleSession(t, d, s.ID)

			d.flushReviewHandoffs(context.Background(), s.ID)
			if sends := seams.sendCalls(); len(sends) != 0 {
				t.Fatalf("without a waiting pane nothing may be typed, got %+v", sends)
			}
			if got, _ := d.sessions.Get(s.ID); got.PendingHandoffs["coderabbit-cli"] == "" {
				t.Error("the stash must survive an unproven flush")
			}
		})
	}
}

// A permission prompt stays untouchable: typing findings there answers the
// approval question with prose.
func TestReviewNeverFlushesToPermissionPrompt(t *testing.T) {
	d := newTestDaemon(t, reviewTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeNative{})
	syncProviders(d)
	seams := &fakeReactSeams{}
	seams.install(d)
	fr := &fakeReview{findings: "DO-NOT-TYPE"}
	fr.install(d)

	s := reactSess("FE-1", "review_pending", openPR(7, "MERGEABLE", "", "pass"))
	s.AtPrompt = false
	d.sessions.Upsert(s)
	d.runReviewProviders(context.Background(), s)

	parkSession(t, d, s.ID, state.InputPermission)
	d.flushReviewHandoffs(context.Background(), s.ID)

	if sends := seams.sendCalls(); len(sends) != 0 {
		t.Fatalf("a worker waiting on a permission decision must never be typed into, got %+v", sends)
	}
	got, _ := d.sessions.Get(s.ID)
	if !strings.Contains(got.PendingHandoffs["coderabbit-cli"], "DO-NOT-TYPE") {
		t.Errorf("the stash must survive an undeliverable flush, got %q", got.PendingHandoffs["coderabbit-cli"])
	}
}

// A restart carries the stash but marks the gate UNVERIFIED. An idle-notify
// parked worker must still be verifiable against its live pane — an
// AtPrompt-only "still open" check inside ensurePromptVerified could never
// re-verify one (the notification already closed AtPrompt), stranding the
// hand-off for the life of the session.
func TestReviewFlushVerifiesCarriedGateOnParkedWorker(t *testing.T) {
	d := newTestDaemon(t, reviewTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeNative{})
	syncProviders(d)
	seams := &fakeReactSeams{}
	seams.install(d)
	d.paneTail = func(context.Context, string, int) (string, error) { return paneWaiting, nil }

	s := reactSess("FE-1", "review_pending", openPR(7, "MERGEABLE", "", "pass"))
	s.PendingHandoffs = map[string]string{string(kindCoderabbitCLI): "CARRIED-FINDING"}
	d.sessions.Upsert(s)
	parkSession(t, d, s.ID, state.InputIdleNotify)
	// Adoption's carry: the gate survives the restart, unverified.
	d.sessions.Update(s.ID, func(cur *session.Session) bool { cur.AtPromptVerified = false; return true })

	d.flushReviewHandoffs(context.Background(), s.ID)

	sends := seams.sendCalls()
	if len(sends) != 1 || !strings.Contains(sends[0].text, "CARRIED-FINDING") {
		t.Fatalf("a waiting pane must verify the carried gate and deliver, got %+v", sends)
	}
}

// A mid-turn pane still blocks the same delivery: verification is what decides,
// not the widened gate.
func TestReviewFlushDefersWhenCarriedGatePaneIsWorking(t *testing.T) {
	d := newTestDaemon(t, reviewTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeNative{})
	syncProviders(d)
	seams := &fakeReactSeams{}
	seams.install(d)
	d.paneTail = func(context.Context, string, int) (string, error) { return paneWorking, nil }

	s := reactSess("FE-1", "review_pending", openPR(7, "MERGEABLE", "", "pass"))
	s.PendingHandoffs = map[string]string{string(kindCoderabbitCLI): "CARRIED-FINDING"}
	d.sessions.Upsert(s)
	parkSession(t, d, s.ID, state.InputIdleNotify)
	d.sessions.Update(s.ID, func(cur *session.Session) bool { cur.AtPromptVerified = false; return true })

	d.flushReviewHandoffs(context.Background(), s.ID)

	if sends := seams.sendCalls(); len(sends) != 0 {
		t.Fatalf("a working pane must block the hand-off, got %+v", sends)
	}
	got, _ := d.sessions.Get(s.ID)
	if !strings.Contains(got.PendingHandoffs[string(kindCoderabbitCLI)], "CARRIED-FINDING") {
		t.Errorf("the stash must survive, got %q", got.PendingHandoffs[string(kindCoderabbitCLI)])
	}
}

// Two kinds pending on a parked worker: exactly ONE lands per pass. Without the
// early return they would both type into the same prompt, since an idle-notify
// delivery consumes no AtPrompt gate.
func TestReviewFlushDeliversOneKindPerPass(t *testing.T) {
	d := newTestDaemon(t, nativeTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeNative{})
	seams := &fakeReactSeams{}
	seams.install(d)
	setProviders(d, cliDesc(), claudeDesc())

	s := reactSess("FE-1", "review_pending", openPR(7, "MERGEABLE", "", "pass"))
	s.AtPrompt = false
	s.PendingHandoffs = map[string]string{
		string(kindCoderabbitCLI): "STASH-CLI",
		string(kindClaudeSession): "STASH-CLAUDE",
	}
	d.sessions.Upsert(s)
	parkSession(t, d, s.ID, state.InputIdleNotify)

	d.flushReviewHandoffs(context.Background(), s.ID)
	if n := len(seams.sendCalls()); n != 1 {
		t.Fatalf("want exactly one hand-off per flush pass, got %d", n)
	}
	got, _ := d.sessions.Get(s.ID)
	if len(got.PendingHandoffs) != 1 {
		t.Errorf("the undelivered kind must stay stashed, got %+v", got.PendingHandoffs)
	}
}

// The Stop hook flushes immediately — the 30s observer cadence is a backstop, not
// the delivery path.
func TestStopHookFlushesPendingHandoff(t *testing.T) {
	d := newTestDaemon(t, reviewTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeNative{})
	syncProviders(d)
	seams := &fakeReactSeams{}
	seams.install(d)

	s := reactSess("FE-1", "review_pending", openPR(7, "MERGEABLE", "", "pass"))
	s.AtPrompt = false
	s.PendingHandoffs = map[string]string{string(kindCoderabbitCLI): "STOP-HOOK-FINDING"}
	d.sessions.Upsert(s)

	d.handleHookEvent(protocol.Request{Cmd: "hookEvent", Session: s.ID, Event: "stop"})

	// The flush is async (a hook must never block the agent's turn), so poll.
	deadline := time.Now().Add(2 * time.Second)
	for len(seams.sendCalls()) == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	sends := seams.sendCalls()
	if len(sends) != 1 || !strings.Contains(sends[0].text, "STOP-HOOK-FINDING") {
		t.Fatalf("the Stop hook must deliver the pending hand-off, got %+v", sends)
	}
	got, _ := d.sessions.Get(s.ID)
	if got.PendingHandoffs[string(kindCoderabbitCLI)] != "" {
		t.Errorf("PendingHandoffs must clear after the stop-hook delivery, got %q", got.PendingHandoffs[string(kindCoderabbitCLI)])
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
	fb := &fakeReview{findings: "FALLBACK-FINDING"} // fallback answers
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
	// An exhausted chain never ANSWERED, so the guard is released for a bounded
	// retry (see noteReviewOutcome) rather than locking the PR out forever.
	if got, _ := d.sessions.Get(s.ID); got.ReviewedPRs["coderabbit-cli"] != 0 {
		t.Errorf("guard must be released for a retry after an exhausted chain, got %d", got.ReviewedPRs["coderabbit-cli"])
	}
}

// A provider that keeps timing out is retried a bounded number of times and
// then left alone: the guard stays stamped so the PR stops re-burning the full
// timeout every observe cycle.
func TestReviewTimeoutRetriesThenGivesUp(t *testing.T) {
	d := newTestDaemon(t, nativeTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeNative{})
	(&fakeReactSeams{}).install(d)
	setProviders(d, cliDesc())
	fr := &fakeReview{err: review.ErrTimeout}
	fr.installKind(d, kindCoderabbitCLI)

	s := reactSess("FE-1", "review_pending", openPR(7, "MERGEABLE", "", "pass"))
	s.AtPrompt = true
	d.sessions.Upsert(s)

	for i := 1; i <= reviewMaxAttempts; i++ {
		cur, _ := d.sessions.Get(s.ID)
		d.runReviewProviders(context.Background(), cur)
		if fr.callCount() != i {
			t.Fatalf("attempt %d: exec count = %d, want %d", i, fr.callCount(), i)
		}
	}
	if got, _ := d.sessions.Get(s.ID); got.ReviewedPRs["coderabbit-cli"] != 7 {
		t.Errorf("guard must stay stamped after %d failed attempts, got %d", reviewMaxAttempts, got.ReviewedPRs["coderabbit-cli"])
	}
	// The guard now suppresses any further attempt.
	cur, _ := d.sessions.Get(s.ID)
	d.runReviewProviders(context.Background(), cur)
	if fr.callCount() != reviewMaxAttempts {
		t.Errorf("exec count = %d, want no attempt past the %d-attempt ceiling", fr.callCount(), reviewMaxAttempts)
	}
}

// A successful pass clears the attempt counter, so a LATER failure on the same
// PR still gets its full retry budget.
func TestReviewSuccessClearsTheAttemptBudget(t *testing.T) {
	d := newTestDaemon(t, nativeTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeNative{})
	(&fakeReactSeams{}).install(d)
	setProviders(d, cliDesc())
	fr := &fakeReview{err: review.ErrTimeout}
	fr.installKind(d, kindCoderabbitCLI)

	s := reactSess("FE-1", "review_pending", openPR(7, "MERGEABLE", "", "pass"))
	s.AtPrompt = true
	d.sessions.Upsert(s)

	cur, _ := d.sessions.Get(s.ID)
	d.runReviewProviders(context.Background(), cur) // 1 failed attempt
	ok := &fakeReview{findings: "FOUND"}
	ok.installKind(d, kindCoderabbitCLI)
	cur, _ = d.sessions.Get(s.ID)
	d.runReviewProviders(context.Background(), cur) // answers → budget cleared
	if key := reviewFailKey(s.ID, kindCoderabbitCLI, 7); d.reviewFails[key] != 0 {
		t.Errorf("attempt counter = %d after a successful pass, want 0", d.reviewFails[key])
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

// The github sink is the ONLY sink that reshapes the text: it posts
// reviewmd-rendered Markdown (heading + collapsed details) while the worker
// hand-off and the notification keep the provider's raw findings.
func TestReviewGithubSinkRendersMarkdownAgentKeepsRaw(t *testing.T) {
	d := newTestDaemon(t, nativeTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeNative{})
	seams := &fakeReactSeams{}
	seams.install(d)
	claude := claudeDesc()
	claude.Transports = config.TransportSet{config.TransportLola, config.TransportGitHub}
	setProviders(d, claude)
	fp := &fakePostPR{}
	fp.install(d)

	findings := "**[blocker]** `app/x.go:12` — nil deref on the error path\n" +
		"- **What:** `load()` returns a nil client.\n" +
		"- **Fix:** check the error before use.\n"

	s := reactSess("FE-1", "review_pending", openPR(7, "MERGEABLE", "", "pass"))
	s.AtPrompt, s.AtPromptVerified = true, true
	d.sessions.Upsert(s)

	d.routeFindings(context.Background(), s, claude, findings)

	calls := fp.callsCopy()
	if len(calls) != 1 {
		t.Fatalf("github sink must post once, got %d", len(calls))
	}
	body := calls[0].body
	for _, want := range []string{
		"> [!CAUTION]\n> **Claude review** — 🛑 1 blocker", // alert tally, per-kind label
		"<details>", "</details>",
		// The location links to the session's own repo + branch.
		`<a href="https://github.com/acme/widgets/blob/lola/fe-1/app/x.go#L12"><code>app/x.go:12</code></a>`,
		"- **Fix:** check the error before use.", // substance preserved
	} {
		if !strings.Contains(body, want) {
			t.Errorf("github body missing %q:\n%s", want, body)
		}
	}

	// The worker hand-off is untouched by the renderer: raw findings, no HTML.
	sends := seams.sendCalls()
	if len(sends) != 1 {
		t.Fatalf("want one worker hand-off, got %d", len(sends))
	}
	if strings.Contains(sends[0].text, "<details>") || !strings.Contains(sends[0].text, "**[blocker]**") {
		t.Errorf("worker hand-off must keep the RAW findings, got %q", sends[0].text)
	}
	// So is the notification head.
	notes := seams.notesByPriority(notify.Action)
	if len(notes) != 1 || strings.Contains(notes[0].Body, "<details>") {
		t.Errorf("notify sink must keep the raw findings, got %+v", notes)
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

func TestHandleReviewRunsUnderItsCallerCtx(t *testing.T) {
	d := newTestDaemon(t, reviewTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeNative{})
	syncProviders(d)
	(&fakeReactSeams{}).install(d)
	fr := &fakeReview{findings: "MANUAL-FINDING"}
	fr.install(d)

	s := reactSess("FE-1", "review_pending", openPR(7, "MERGEABLE", "", "pass"))
	s.AtPrompt = true
	d.sessions.Upsert(s)

	data, err := d.handleReview(context.Background(), s.ID)
	if err != nil {
		t.Fatalf("manual review failed: %v", err)
	}
	if got := fr.ctxErr(); got != nil {
		t.Errorf("manual review exec must run under its own live caller ctx; ctx.Err() = %v", got)
	}
	if !data.Ran || !strings.Contains(data.Findings, "MANUAL-FINDING") {
		t.Errorf("manual review data = %+v, want ran with the findings", data)
	}
}

// A pass NEVER runs on the observe loop: the observer queues it, and only the
// review worker execs it. A claude-session pass takes minutes, so running it
// inline stalled tmux liveness, PR facts and reactions for every other session.
func TestObserveQueuesThePassInsteadOfRunningIt(t *testing.T) {
	d := newTestDaemon(t, reviewTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeNative{})
	syncProviders(d)
	(&fakeReactSeams{}).install(d)
	fr := &fakeReview{findings: "AUTO"}
	fr.install(d)

	s := reactSess("FE-1", "review_pending", openPR(7, "MERGEABLE", "", "pass"))
	s.AtPrompt = true
	d.sessions.Upsert(s)

	d.queueReviewProviders(context.Background(), s)
	if fr.callCount() != 0 {
		t.Fatalf("the observer must not exec the pass itself, got %d execs", fr.callCount())
	}
	if got := findSession(t, d.sessions.Snapshot(), s.ID); got.ReviewedPRs["coderabbit-cli"] != 0 {
		t.Errorf("queueing must not stamp the guard (the run does), got %d", got.ReviewedPRs["coderabbit-cli"])
	}

	if n := drainReviewQueue(t, d); n != 1 {
		t.Fatalf("drained %d queued passes, want 1", n)
	}
	if fr.callCount() != 1 {
		t.Fatalf("the worker must run the queued pass exactly once, got %d", fr.callCount())
	}
	if got := findSession(t, d.sessions.Snapshot(), s.ID); got.ReviewedPRs["coderabbit-cli"] != 7 {
		t.Errorf("ReviewedPRs[cli] = %d, want 7 after the worker ran the pass", got.ReviewedPRs["coderabbit-cli"])
	}
}

// A session/kind already queued is not queued twice by the next cycle.
func TestQueueReviewPassDedupsWhileInFlight(t *testing.T) {
	d := newTestDaemon(t, reviewTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeNative{})
	syncProviders(d)
	(&fakeReactSeams{}).install(d)
	(&fakeReview{findings: "AUTO"}).install(d)

	s := reactSess("FE-1", "review_pending", openPR(7, "MERGEABLE", "", "pass"))
	d.sessions.Upsert(s)

	d.queueReviewProviders(context.Background(), s)
	d.queueReviewProviders(context.Background(), s)
	if n := len(d.reviewCh); n != 1 {
		t.Fatalf("queue holds %d jobs, want 1 (a queued session/kind must not re-queue)", n)
	}
}

// drainReviewQueue runs every queued pass synchronously, as the review worker
// would, and returns how many it ran. Tests never start reviewLoop — a real
// goroutine would race their assertions.
func drainReviewQueue(t *testing.T, d *Daemon) int {
	t.Helper()
	ran := 0
	for {
		select {
		case job := <-d.reviewCh:
			d.runQueuedReview(context.Background(), job)
			ran++
		default:
			return ran
		}
	}
}

// --- full observe cycle wires the trigger -------------------------------------

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
	drainReviewQueue(t, d) // observe queues the pass; the worker runs it

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

// THE REGRESSION: claude-code ends a turn (Stop hook → AtPrompt + AtPromptVerified)
// and THEN covers the pane with a modal setup dialog. handoffPromptProof used to
// short-circuit on the hook's AtPromptVerified without looking at the pane, so the
// findings were typed into the dialog, the gate was consumed and the stash was
// dropped — the daemon logged a delivery that reached nobody. A hook verdict is
// evidence about when it fired, not about now: every hand-off must see the pane.
func TestReviewNeverTypesIntoAModalDespiteHookVerifiedGate(t *testing.T) {
	d := newTestDaemon(t, reviewTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeNative{})
	syncProviders(d)
	seams := &fakeReactSeams{}
	seams.install(d)
	fr := &fakeReview{findings: "MODAL-SWALLOWED-FINDING"}
	fr.install(d)
	d.paneTail = func(context.Context, string, int) (string, error) { return paneModal, nil }

	s := reactSess("FE-1", "review_pending", openPR(7, "MERGEABLE", "", "pass"))
	s.AtPrompt = true         // the Stop hook fired…
	s.AtPromptVerified = true // …and marked its own verdict verified
	d.sessions.Upsert(s)

	d.runReviewProviders(context.Background(), s)

	if sends := seams.sendCalls(); len(sends) != 0 {
		t.Fatalf("nothing may be typed into a modal, got %+v", sends)
	}
	got, _ := d.sessions.Get(s.ID)
	if got.PendingHandoffs[string(kindCoderabbitCLI)] != "MODAL-SWALLOWED-FINDING" {
		t.Fatalf("the findings must be stashed for a later cycle, got %q",
			got.PendingHandoffs[string(kindCoderabbitCLI)])
	}
	if !got.AtPrompt {
		t.Error("a deferred hand-off must not consume the gate")
	}

	// Once the dialog is gone and the pane rests, the stash delivers.
	d.paneTail = func(context.Context, string, int) (string, error) { return paneWaiting, nil }
	d.flushReviewHandoffs(context.Background(), s.ID)
	sends := seams.sendCalls()
	if len(sends) != 1 || !strings.Contains(sends[0].text, "MODAL-SWALLOWED-FINDING") {
		t.Fatalf("the stash must deliver once the modal is dismissed, got %+v", sends)
	}
}

// The same rule on the flush path, and for the idle-notify parked worker: the
// widened gate admits it, the pane proof still has the last word.
func TestReviewFlushNeverTypesIntoAModal(t *testing.T) {
	d := newTestDaemon(t, reviewTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeNative{})
	syncProviders(d)
	seams := &fakeReactSeams{}
	seams.install(d)
	d.paneTail = func(context.Context, string, int) (string, error) { return paneModal, nil }

	s := reactSess("FE-1", "review_pending", openPR(7, "MERGEABLE", "", "pass"))
	s.PendingHandoffs = map[string]string{string(kindCoderabbitCLI): "STASHED"}
	d.sessions.Upsert(s)
	d.sessions.Update(s.ID, func(cur *session.Session) bool {
		cur.SetAgentState(state.AgentWaitingInput, "", time.Now())
		cur.InputReason = state.InputIdleNotify
		cur.AtPromptVerified = true
		return true
	})

	d.flushReviewHandoffs(context.Background(), s.ID)

	if sends := seams.sendCalls(); len(sends) != 0 {
		t.Fatalf("an idle-notify park over a modal must still defer, got %+v", sends)
	}
	if got, _ := d.sessions.Get(s.ID); got.PendingHandoffs[string(kindCoderabbitCLI)] != "STASHED" {
		t.Error("the stash must survive a deferred flush")
	}
}

// --- pluggable kinds (SUSHI-583) ---------------------------------------------

// passDesc builds a pass descriptor of any kind, so the tests below cover the
// kinds the daemon never names — proving dispatch is driven by the config-side
// family, not a hardcoded list.
func passDesc(k provKind) reviewProvider {
	return reviewProvider{
		Kind: k, Shape: shapePass, Enabled: true, OnPROpen: true,
		Transports: config.TransportSet{config.TransportLola}, Notify: true, SendToAgent: true, Handoff: handoffFull,
	}
}

// EVERY pass kind runs its own PR-open pass through the same guard and the same
// routing. A kind the daemon does not name by constant must work identically, or
// "add a review agent in config" is not actually enough.
func TestEveryPassKindRunsAndRoutes(t *testing.T) {
	for _, kind := range config.ReviewProviderPassKinds() {
		t.Run(kind, func(t *testing.T) {
			d := newTestDaemon(t, reviewTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeNative{})
			setProviders(d, passDesc(provKind(kind)))
			seams := &fakeReactSeams{}
			seams.install(d)
			fr := &fakeReview{findings: "one finding"}
			fr.installKind(d, provKind(kind))

			s := reactSess("FE-1", "review_pending", openPR(7, "MERGEABLE", "", "pass"))
			s.AtPrompt = true
			d.sessions.Upsert(s)
			d.runReviewProviders(context.Background(), s)

			if got := fr.callCount(); got != 1 {
				t.Fatalf("%s: pass ran %d times, want 1", kind, got)
			}
			cur, _ := d.sessions.Get(s.ID)
			if cur.ReviewedPRs[kind] != 7 {
				t.Errorf("%s: guard = %v, want PR 7 stamped under its own kind", kind, cur.ReviewedPRs)
			}
			// ...and the findings reached the worker, sanitized + gated as always.
			if len(sentTexts(seams)) != 1 {
				t.Errorf("%s: worker hand-off = %d sends, want 1", kind, len(sentTexts(seams)))
			}
		})
	}
}

// A guard is per KIND, so two agents reviewing the same PR do not suppress each
// other — and one that has already run does not stop the other.
func TestPerKindGuardsAreIndependentAcrossAgents(t *testing.T) {
	d := newTestDaemon(t, reviewTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeNative{})
	setProviders(d, passDesc(kindClaudeSession), passDesc(provKind("codex-session")))
	(&fakeReactSeams{}).install(d)
	claude := &fakeReview{findings: "claude finding"}
	claude.installKind(d, kindClaudeSession)
	codex := &fakeReview{findings: "codex finding"}
	codex.installKind(d, provKind("codex-session"))

	s := reactSess("FE-1", "review_pending", openPR(11, "MERGEABLE", "", "pass"))
	s.AtPrompt = true
	// claude already reviewed this PR; codex has not.
	s.ReviewedPRs = map[string]int{"claude-session": 11}
	d.sessions.Upsert(s)
	d.runReviewProviders(context.Background(), s)

	if got := claude.callCount(); got != 0 {
		t.Errorf("claude ran %d times, want 0 (its guard is stamped)", got)
	}
	if got := codex.callCount(); got != 1 {
		t.Errorf("codex ran %d times, want 1 (its own guard is clear)", got)
	}
}

// The headline use case: claude is over quota, so codex reviews instead — and
// the result routes under the PRIMARY's transports, not the fallback's.
func TestAgentFallsBackToAnotherAgentOnQuota(t *testing.T) {
	d := newTestDaemon(t, reviewTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeNative{})
	primary := passDesc(kindClaudeSession)
	primary.Fallback = []provKind{"codex-session"}
	setProviders(d, primary, passDesc(provKind("codex-session")))
	seams := &fakeReactSeams{}
	seams.install(d)

	claude := &fakeReview{err: reviewagent.ErrQuota}
	claude.installKind(d, kindClaudeSession)
	codex := &fakeReview{findings: "codex found something"}
	codex.installKind(d, provKind("codex-session"))

	s := reactSess("FE-1", "review_pending", openPR(3, "MERGEABLE", "", "pass"))
	s.AtPrompt = true
	d.sessions.Upsert(s)
	d.runReviewProviders(context.Background(), s)

	if claude.callCount() != 1 || codex.callCount() != 1 {
		t.Fatalf("claude=%d codex=%d, want each tried once", claude.callCount(), codex.callCount())
	}
	sent := sentTexts(seams)
	if len(sent) != 1 || !strings.Contains(sent[0], "codex found something") {
		t.Fatalf("worker got %v, want the fallback's findings", sent)
	}
	// The hand-off is labelled by the PRIMARY (whose transports routed it), so a
	// fallback result is never announced as coming from the wrong reviewer.
	if !strings.Contains(sent[0], "Claude review") {
		t.Errorf("hand-off preamble = %q, want the primary's label", sent[0])
	}
	// The guard is stamped on the PRIMARY kind only — the chain must not re-fire.
	cur, _ := d.sessions.Get(s.ID)
	if cur.ReviewedPRs["claude-session"] != 3 || cur.ReviewedPRs["codex-session"] != 0 {
		t.Errorf("guards = %v, want only the primary stamped", cur.ReviewedPRs)
	}
}

// A pass kind whose binary is unavailable is simply ABSENT from the seam map, and
// the chain advances rather than failing the review.
func TestUnavailableKindAdvancesTheChain(t *testing.T) {
	d := newTestDaemon(t, reviewTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeNative{})
	primary := passDesc(provKind("opencode-session"))
	primary.Fallback = []provKind{"claude-session"}
	setProviders(d, primary, passDesc(kindClaudeSession))
	seams := &fakeReactSeams{}
	seams.install(d)
	// opencode has no seam at all (binary missing); claude does.
	claude := &fakeReview{findings: "claude reviewed it"}
	claude.installKind(d, kindClaudeSession)

	s := reactSess("FE-1", "review_pending", openPR(4, "MERGEABLE", "", "pass"))
	s.AtPrompt = true
	d.sessions.Upsert(s)
	d.runReviewProviders(context.Background(), s)

	if claude.callCount() != 1 {
		t.Fatalf("claude ran %d times, want 1 via the fallback", claude.callCount())
	}
	if sent := sentTexts(seams); len(sent) != 1 {
		t.Errorf("worker hand-off = %v, want the fallback's findings", sent)
	}
}

// Labels are per PROVIDER: each agent's findings say which agent produced them,
// and a generic bot-watch names the bot it watches rather than CodeRabbit.
func TestLabelsNameTheActualReviewer(t *testing.T) {
	for _, tc := range []struct {
		desc      reviewProvider
		wantTitle string
		wantWho   string
	}{
		{passDesc(kindClaudeSession), config.ClaudeReviewNotifyTitle, "Claude"},
		{passDesc(provKind("codex-session")), config.CodexReviewNotifyTitle, "Codex"},
		{passDesc(provKind("opencode-session")), config.OpenCodeReviewNotifyTitle, "opencode"},
		{passDesc(kindCoderabbitCLI), config.ReviewNotifyTitle, "CodeRabbit"},
		{passDesc(provKind("custom-cli")), config.CustomCLIReviewNotifyTitle, "the review tool"},
		{watchDesc(), config.CodeRabbitNotifyTitle, config.DefaultCodeRabbitAuthor},
	} {
		lbl := labelsFor(tc.desc)
		if lbl.notifyTitle != tc.wantTitle {
			t.Errorf("%s: notify title = %q, want %q", tc.desc.Kind, lbl.notifyTitle, tc.wantTitle)
		}
		if lbl.who != tc.wantWho {
			t.Errorf("%s: who = %q, want %q", tc.desc.Kind, lbl.who, tc.wantWho)
		}
	}
	// A generic watch is named by its configured author.
	bot := watchDesc()
	bot.Kind, bot.Author = provKind("bot-watch"), "greptile-apps"
	if lbl := labelsFor(bot); lbl.who != "greptile-apps" || lbl.notifyTitle != "greptile-apps review" {
		t.Errorf("bot-watch labels = %+v, want the configured author", lbl)
	}
}

// The author reaches a human-facing label and the worker's pane, so it is
// sanitized to login characters and clipped — it is operator config, but it is
// still interpolated into text lola generates.
func TestBotDisplayNameSanitizes(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"coderabbitai", "coderabbitai"},
		{"coderabbitai[bot]", "coderabbitai[bot]"},
		{"greptile-apps", "greptile-apps"},
		{"bad name; rm -rf /", "badnamerm-rf"},
		{"", "Bot"},
		{"   ", "Bot"},
		{strings.Repeat("x", 100), strings.Repeat("x", 39)},
	} {
		if got := botDisplayName(tc.in); got != tc.want {
			t.Errorf("botDisplayName(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// A generic watch hands the worker a pointer naming ITS bot: telling an agent
// "CodeRabbit posted feedback" when Greptile did points it at comments that do
// not exist.
func TestWatchPointerNamesTheBot(t *testing.T) {
	s := reactSess("FE-1", "review_pending", openPR(42, "MERGEABLE", "", "pass"))
	if got := watchAgentPointer(s, watchDesc()); !strings.Contains(got, "CodeRabbit") || !strings.Contains(got, "#42") {
		t.Errorf("coderabbit pointer = %q", got)
	}
	bot := watchDesc()
	bot.Kind, bot.Author = provKind("bot-watch"), "greptile-apps"
	got := watchAgentPointer(s, bot)
	if !strings.Contains(got, "greptile-apps") || strings.Contains(got, "CodeRabbit") {
		t.Errorf("bot-watch pointer = %q, want it to name greptile-apps", got)
	}
	if !strings.Contains(got, "#42") || !strings.Contains(got, "gh pr view 42") {
		t.Errorf("bot-watch pointer = %q, want the PR number in both places", got)
	}
}

// The @-mention defuse generalizes with the catalog: a bot that reads its own
// mention as a command must not be triggered by lola's posted findings, whichever
// bot it is. CodeRabbit stays defused unconditionally.
func TestNeutralizeCoversConfiguredWatchBots(t *testing.T) {
	d := newTestDaemon(t, reviewTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeNative{})
	bot := watchDesc()
	bot.Kind, bot.Author = provKind("bot-watch"), "greptile-apps"
	setProviders(d, bot)

	body := d.neutralizeWatchedBots("see @greptile-apps and @coderabbitai about @Greptile-Apps")
	for _, live := range []string{"@greptile-apps", "@coderabbitai", "@Greptile-Apps"} {
		if strings.Contains(body, live) {
			t.Errorf("%q survived undefused in %q", live, body)
		}
	}
	// Defusing is invisible: the text still reads the same to a human.
	if strings.ReplaceAll(body, "​", "") != "see @greptile-apps and @coderabbitai about @Greptile-Apps" {
		t.Errorf("neutralize changed more than the zero-width spaces: %q", body)
	}
	// A DISABLED watch is not defused for — only what lola actually watches.
	bot.Enabled = false
	setProviders(d, bot)
	if got := d.neutralizeWatchedBots("@greptile-apps"); strings.Contains(got, "​") {
		t.Errorf("a disabled watch must not be defused for, got %q", got)
	}
}

// The unavailable-provider warning names each missing kind and the binary it
// needs, so an operator who enabled codex-session without codex installed is told
// which tool to install rather than being met with silence.
func TestUnavailableWarnNamesEveryMissingKind(t *testing.T) {
	d := newTestDaemon(t, reviewTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeNative{})
	setProviders(d, passDesc(provKind("codex-session")), passDesc(kindCoderabbitCLI), watchDesc())
	d.mu.Lock()
	warn := d.reviewUnavailableWarnLocked()
	d.mu.Unlock()

	for _, want := range []string{"codex-session", "codex", "coderabbit-cli", "coderabbit"} {
		if !strings.Contains(warn, want) {
			t.Errorf("warning %q does not name %q", warn, want)
		}
	}
	// A watch needs no binary, so it is never reported missing.
	if strings.Contains(warn, "coderabbit-watch") {
		t.Errorf("warning must not name the watch: %q", warn)
	}
}

// sentTexts is the send-keys payloads a fake seam set recorded, in order.
func sentTexts(f *fakeReactSeams) []string {
	var out []string
	for _, c := range f.sendCalls() {
		out = append(out, c.text)
	}
	return out
}

// Validation rejects a generic kind that names nothing, but Validate is NOT
// fatal at startup — it only holds polls — so a hand-edited config plus a
// restart reaches the runtime. There BOTH empty values fall back to CodeRabbit
// (an empty `command` resolves to the coderabbit binary, an empty watch `author`
// to "coderabbitai"), so a provider the operator pointed at another vendor would
// silently run CodeRabbit instead. It must fail CLOSED and say so.
func TestUnconfiguredGenericKindsFailClosed(t *testing.T) {
	cfg := nativeTestConfig(nativePoll("p1"))
	cli, _ := config.NewReviewProvider("custom-cli")
	cli.Enabled = true // no command
	watch, _ := config.NewReviewProvider("bot-watch")
	watch.Enabled = true // no author
	ok, _ := config.NewReviewProvider("coderabbit-cli")
	ok.Enabled = true
	cfg.ReviewProviders = []config.ReviewProvider{cli, watch, ok}

	d := newTestDaemon(t, cfg, &linear.Fake{}, &fakeNative{})
	syncProviders(d)

	for _, kind := range []provKind{"custom-cli", "bot-watch"} {
		p, found := d.providerByKind(kind)
		if !found {
			t.Fatalf("%s: descriptor missing", kind)
		}
		if p.Enabled {
			t.Errorf("%s: must be disabled when it names nothing", kind)
		}
		if p.Unconfigured == "" {
			t.Errorf("%s: must record WHY it was disabled", kind)
		}
		if d.passSeam(kind) != nil {
			t.Errorf("%s: must have no exec seam", kind)
		}
	}
	// A properly-configured provider beside them is untouched.
	if p, _ := d.providerByKind(kindCoderabbitCLI); !p.Enabled || p.Unconfigured != "" {
		t.Errorf("coderabbit-cli = %+v, want it left enabled", p)
	}
	// ...and the operator is told, by kind and by reason.
	d.mu.Lock()
	warn := d.reviewUnavailableWarnLocked()
	d.mu.Unlock()
	for _, want := range []string{"custom-cli", "no command configured", "bot-watch", "no author configured"} {
		if !strings.Contains(warn, want) {
			t.Errorf("warning %q does not carry %q", warn, want)
		}
	}
}

// A disabled generic kind names nothing on purpose and must not be reported.
func TestDisabledGenericKindIsNotReported(t *testing.T) {
	cfg := nativeTestConfig(nativePoll("p1"))
	cli, _ := config.NewReviewProvider("custom-cli") // enabled=false, no command
	cfg.ReviewProviders = []config.ReviewProvider{cli}

	d := newTestDaemon(t, cfg, &linear.Fake{}, &fakeNative{})
	syncProviders(d)
	if p, _ := d.providerByKind("custom-cli"); p.Unconfigured != "" {
		t.Errorf("a disabled provider must not be reported: %q", p.Unconfigured)
	}
	d.mu.Lock()
	warn := d.reviewUnavailableWarnLocked()
	d.mu.Unlock()
	if warn != "" {
		t.Errorf("warning = %q, want none", warn)
	}
}

// The unavailable warning must NAME the binary that could not be resolved —
// a custom-cli's own command, an agent's binary, and coderabbit's login hint.
func TestUnavailableWarnNamesTheBinary(t *testing.T) {
	cfg := nativeTestConfig(nativePoll("p1"))
	custom, _ := config.NewReviewProvider("custom-cli")
	custom.Enabled, custom.Command = true, "lola-nonexistent-review-tool --plain"
	codex, _ := config.NewReviewProvider("codex-session")
	codex.Enabled = true
	cr, _ := config.NewReviewProvider("coderabbit-cli")
	cr.Enabled = true
	cfg.ReviewProviders = []config.ReviewProvider{custom, codex, cr}

	d := newTestDaemon(t, cfg, &linear.Fake{}, &fakeNative{})
	syncProviders(d)
	d.mu.Lock()
	warn := d.reviewUnavailableWarnLocked()
	d.mu.Unlock()

	if !strings.Contains(warn, "lola-nonexistent-review-tool not on PATH") {
		t.Errorf("warning %q must name the custom-cli's own command", warn)
	}
	// The two whose binaries this machine may or may not have are only asserted
	// when they are actually missing, so the test is not host-dependent.
	if strings.Contains(warn, "codex-session") && !strings.Contains(warn, "codex not on PATH") {
		t.Errorf("warning %q must name codex's binary", warn)
	}
	if strings.Contains(warn, "coderabbit-cli") && !strings.Contains(warn, "coderabbit auth login") {
		t.Errorf("warning %q must keep coderabbit's actionable hint", warn)
	}
}

// An UNKNOWN kind is the third way a provider can name nothing: every family
// predicate is false for one, so it fell through to the cli branch and — with no
// command of its own — resolved to the coderabbit binary. A typo'd kind sending
// its PR to CodeRabbit is the same silent-wrong-vendor failure as an empty
// custom-cli, so it must fail closed the same way.
func TestUnknownKindFailsClosed(t *testing.T) {
	cfg := nativeTestConfig(nativePoll("p1"))
	// Built by hand: NewReviewProvider refuses an unknown kind, which is exactly
	// why this can only arrive from a hand-edited config.toml.
	unknown, _ := config.NewReviewProvider("coderabbit-cli")
	unknown.SetKind("greptile-cli")
	unknown.Enabled, unknown.Command = true, ""
	cfg.ReviewProviders = []config.ReviewProvider{unknown}

	d := newTestDaemon(t, cfg, &linear.Fake{}, &fakeNative{})
	syncProviders(d)

	p, ok := d.providerByKind("greptile-cli")
	if !ok {
		t.Fatal("descriptor missing")
	}
	if p.Enabled {
		t.Error("an unknown kind must be disabled, not run as coderabbit")
	}
	if !strings.Contains(p.Unconfigured, "unknown provider kind") {
		t.Errorf("Unconfigured = %q, want it to say the kind is unknown", p.Unconfigured)
	}
	if d.passSeam("greptile-cli") != nil {
		t.Error("an unknown kind must have no exec seam")
	}
	// It never runs, so a session with an open PR is left entirely alone.
	seams := &fakeReactSeams{}
	seams.install(d)
	s := reactSess("FE-1", "review_pending", openPR(9, "MERGEABLE", "", "pass"))
	s.AtPrompt = true
	d.sessions.Upsert(s)
	d.runReviewProviders(context.Background(), s)
	if cur, _ := d.sessions.Get(s.ID); len(cur.ReviewedPRs) != 0 {
		t.Errorf("guards = %v, want none stamped", cur.ReviewedPRs)
	}
	if len(sentTexts(seams)) != 0 {
		t.Errorf("worker got %v, want nothing", sentTexts(seams))
	}
}

// A FORCED review (`lola review` / the app's Review button) must post to the PR
// AGAIN, even though the github sink already settled that PR for this kind on
// the first review. Without releasing the settle guard the second pass ran, the
// findings reached the worker and Linear, and the PR silently got nothing.
func TestHandleReviewForcedRepostsToGitHub(t *testing.T) {
	d := newTestDaemon(t, reviewTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeNative{})
	cli := cliDesc()
	cli.Transports = config.TransportSet{config.TransportLola, config.TransportGitHub}
	setProviders(d, cli)
	(&fakeReactSeams{}).install(d)
	fr := &fakeReview{findings: "FORCED-FINDING"}
	fr.install(d)
	fp := &fakePostPR{}
	fp.install(d)

	// PR #7 already reviewed AND already posted (both one-shots settled).
	s := reactSess("FE-1", "review_pending", openPR(7, "MERGEABLE", "", "pass"))
	s.AtPrompt = true
	s.ReviewedPRs = map[string]int{"coderabbit-cli": 7}
	s.PostedGitHubPRs = map[string]int{"coderabbit-cli": 7}
	d.sessions.Upsert(s)

	if _, err := d.handleReview(context.Background(), s.ID); err != nil {
		t.Fatalf("handleReview: %v", err)
	}
	if fr.callCount() != 1 {
		t.Fatalf("forced review must run the pass, got %d execs", fr.callCount())
	}
	if fp.count() != 1 {
		t.Fatalf("a forced review must re-post to the PR, got %d posts", fp.count())
	}
	got, _ := d.sessions.Get(s.ID)
	if got.PostedGitHubPRs["coderabbit-cli"] != 7 {
		t.Errorf("the successful re-post must re-settle the guard, got %d", got.PostedGitHubPRs["coderabbit-cli"])
	}
}

// The release is scoped to the PR the force names: a settle guard pointing at a
// DIFFERENT PR is left alone.
func TestUnstampGithubSettledOnlyClearsMatchingPR(t *testing.T) {
	d := newTestDaemon(t, reviewTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeNative{})
	s := reactSess("FE-1", "review_pending", openPR(9, "MERGEABLE", "", "pass"))
	s.PostedGitHubPRs = map[string]int{"coderabbit-cli": 9}
	d.sessions.Upsert(s)

	d.unstampGithubSettled(s.ID, kindCoderabbitCLI, 7)
	got, _ := d.sessions.Get(s.ID)
	if got.PostedGitHubPRs["coderabbit-cli"] != 9 {
		t.Errorf("a guard for another PR must survive, got %d", got.PostedGitHubPRs["coderabbit-cli"])
	}

	d.unstampGithubSettled(s.ID, kindCoderabbitCLI, 9)
	got, _ = d.sessions.Get(s.ID)
	if _, ok := got.PostedGitHubPRs["coderabbit-cli"]; ok {
		t.Errorf("a matching guard must be released, got %v", got.PostedGitHubPRs)
	}
}
