package daemon

// Tests for the PLAN P5.25 brain wiring (brainwire.go + the escalation/approved
// call sites): the OPT-IN, bounded claude summary augments the EXISTING notify +
// Linear-comment paths only.
//
// Every seam is a hermetic fake — no claude, gh, tmux, or network. fakeBrain
// stands in for brain.Client.Summarize (via the d.brainSummarize seam) and the
// paneTail / prDiff context-gathering seams, so each summary call and its
// gathered context is observable, and no test ever runs real claude.

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/linear"
	"github.com/sushidev-team/lola/internal/notify"
)

// brainCall records one Summarize invocation.
type brainCall struct{ instruction, contextText string }

// fakeBrain installs the brain exec seam plus the paneTail/prDiff context seams.
type fakeBrain struct {
	mu    sync.Mutex
	calls []brainCall
	out   string
	err   error
}

// install wires the recording brain seam and canned context seams onto d and
// sets the [brain] toggle config. A disabled bc (SummarizeEscalation/Approved
// false) proves the gate prevents any Summarize call even with a seam present.
func (fb *fakeBrain) install(d *Daemon, bc config.BrainConfig) {
	d.cfg.Brain = bc
	d.brainSummarize = func(_ context.Context, instruction, contextText string) (string, error) {
		fb.mu.Lock()
		defer fb.mu.Unlock()
		fb.calls = append(fb.calls, brainCall{instruction, contextText})
		return fb.out, fb.err
	}
	d.paneTail = func(_ context.Context, _ string, _ int) (string, error) { return "PANE-TAIL-XYZ", nil }
	d.prDiff = func(_ context.Context, _ string, _ int) (string, error) { return "DIFF-CONTENT-ABC", nil }
}

func (fb *fakeBrain) callsFor(instruction string) []brainCall {
	fb.mu.Lock()
	defer fb.mu.Unlock()
	var out []brainCall
	for _, c := range fb.calls {
		if c.instruction == instruction {
			out = append(out, c)
		}
	}
	return out
}

// brainOn is the fully-enabled [brain] config (both summaries on).
func brainOn() config.BrainConfig {
	return config.BrainConfig{Enabled: true, SummarizeEscalation: true, SummarizeApproved: true, TimeoutSeconds: 120}
}

// escalationHarness wires a daemon that escalates FE-9 on its first ci_failed
// observe cycle, with the blocked Linear write-back configured. Returns the
// daemon, the linear fake, the react seams (notifier + failing-checks), and the
// session's issue UUID.
func escalationHarness(t *testing.T) (*Daemon, *linear.Fake, *fakeReactSeams, string) {
	t.Helper()
	p := labelPoll("p1")
	p.BlockedLabelID = "lbl-blocked"
	p.CommentOnBlocked = true
	cfg := reactTestConfig(p)
	cfg.Reactions.CIFailed.Retries = 0 // escalate on the first ci_failed
	fake := &linear.Fake{LabelIDsByIssue: map[string][]string{"uuid-fe-9": {"lbl-x"}}}
	nat := &fakeNative{}
	d := newTestDaemon(t, cfg, fake, nat)
	seams := &fakeReactSeams{failing: "FAILING-LOG-XYZ"}
	seams.install(d)
	(&fakeObsSeams{pr: openPR(9, "MERGEABLE", "", "fail")}).install(d) // ci_failed
	s := wbObserveSess(d, "FE-9", "p1")
	nat.alive = map[string]bool{s.ID: true}
	return d, fake, seams, s.IssueUUID
}

// --- escalation: summary → Urgent notify body + blocked comment, once ---------

func TestBrainEscalationSummaryFeedsNotifyAndComment(t *testing.T) {
	d, fake, seams, uuid := escalationHarness(t)
	fb := &fakeBrain{out: "BRAIN-SUMMARY-5-LINES"}
	fb.install(d, brainOn())

	d.observe(context.Background())

	// Summarize called exactly once, with the escalation instruction and a
	// context carrying the gathered facts: derived status, failing-check logs,
	// and the agent pane tail.
	calls := fb.callsFor(config.BrainEscalationInstruction)
	if len(calls) != 1 {
		t.Fatalf("escalation Summarize calls = %d, want 1", len(calls))
	}
	cx := calls[0].contextText
	for _, want := range []string{"ci_failed", "FAILING-LOG-XYZ", "PANE-TAIL-XYZ"} {
		if !strings.Contains(cx, want) {
			t.Errorf("gathered context missing %q; got:\n%s", want, cx)
		}
	}

	// The summary is the Urgent notify body (not the generic "handing off" text).
	urgent := seams.notesByPriority(notify.Urgent)
	if len(urgent) != 1 || urgent[0].Body != "BRAIN-SUMMARY-5-LINES" {
		t.Fatalf("Urgent notify body = %+v, want one body == the summary", urgent)
	}

	// ...and the blocked Linear comment detail.
	c := fake.CommentsByIssue[uuid]
	if len(c) != 1 || !strings.Contains(c[0], "BRAIN-SUMMARY-5-LINES") {
		t.Fatalf("blocked comment = %v, want one containing the summary", c)
	}

	// The untrusted summary must NEVER reach tmux send-keys.
	for _, sk := range seams.sendCalls() {
		if strings.Contains(sk.text, "BRAIN-SUMMARY") {
			t.Errorf("summary leaked into send-keys: %q", sk.text)
		}
	}

	// Fire once: a second identical cycle re-summarizes nothing.
	d.observe(context.Background())
	if n := len(fb.callsFor(config.BrainEscalationInstruction)); n != 1 {
		t.Errorf("escalation summary fired %d times, want once", n)
	}
	if c := len(fake.CommentsByIssue[uuid]); c != 1 {
		t.Errorf("blocked comment fired %d times, want once", c)
	}
}

// Brain disabled: the generic template is used and Summarize is NEVER called,
// even though a recording seam is installed (the toggle gate blocks it).
func TestBrainDisabledUsesGenericTemplate(t *testing.T) {
	d, fake, seams, uuid := escalationHarness(t)
	fb := &fakeBrain{out: "SHOULD-NOT-APPEAR"}
	fb.install(d, config.BrainConfig{}) // disabled: all toggles false

	d.observe(context.Background())

	if n := len(fb.calls); n != 0 {
		t.Fatalf("Summarize called %d times with the brain disabled, want 0", n)
	}
	urgent := seams.notesByPriority(notify.Urgent)
	if len(urgent) != 1 || !strings.Contains(urgent[0].Body, "handing off") {
		t.Fatalf("Urgent body = %+v, want the generic 'handing off' template", urgent)
	}
	c := fake.CommentsByIssue[uuid]
	if len(c) != 1 || !strings.Contains(c[0], "CI is still failing after automatic retries") {
		t.Fatalf("blocked comment = %v, want the generic reason", c)
	}
}

// Brain error: the summary is attempted once but fails, so both surfaces fall
// back to the generic template (zero regression).
func TestBrainEscalationErrorFallsBackToGeneric(t *testing.T) {
	d, fake, seams, uuid := escalationHarness(t)
	fb := &fakeBrain{err: errors.New("claude boom")}
	fb.install(d, brainOn())

	d.observe(context.Background())

	if n := len(fb.callsFor(config.BrainEscalationInstruction)); n != 1 {
		t.Fatalf("Summarize attempts = %d, want exactly 1 (no retries)", n)
	}
	urgent := seams.notesByPriority(notify.Urgent)
	if len(urgent) != 1 || !strings.Contains(urgent[0].Body, "handing off") {
		t.Fatalf("Urgent body = %+v, want the generic template on brain error", urgent)
	}
	c := fake.CommentsByIssue[uuid]
	if len(c) != 1 || !strings.Contains(c[0], "CI is still failing after automatic retries") {
		t.Fatalf("blocked comment = %v, want the generic reason on brain error", c)
	}
}

// --- approved: PR diff summarized into the Action notify, once ----------------

func TestBrainApprovedSummarizesPRDiffIntoNotify(t *testing.T) {
	d := newTestDaemon(t, reactTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeNative{})
	seams := &fakeReactSeams{}
	seams.install(d)
	fb := &fakeBrain{out: "RISK-SUMMARY"}
	fb.install(d, brainOn())

	s := reactSess("FE-1", "approved", openPR(11, "MERGEABLE", "APPROVED", "pass"))
	s.AtPrompt = true
	d.sessions.Upsert(s)

	d.react(context.Background(), s)

	// The PR diff (not the escalation context) is summarized, exactly once.
	calls := fb.callsFor(config.BrainApprovedInstruction)
	if len(calls) != 1 || calls[0].contextText != "DIFF-CONTENT-ABC" {
		t.Fatalf("approved Summarize calls = %+v, want one over the PR diff", calls)
	}
	if len(fb.callsFor(config.BrainEscalationInstruction)) != 0 {
		t.Error("approved path must not run the escalation summary")
	}

	// The summary is the Action notify body.
	action := seams.notesByPriority(notify.Action)
	if len(action) != 1 || action[0].Body != "RISK-SUMMARY" {
		t.Fatalf("Action notify = %+v, want one body == the summary", action)
	}

	// approved never types into the agent, and the summary must never reach it.
	for _, sk := range seams.sendCalls() {
		if strings.Contains(sk.text, "RISK-SUMMARY") {
			t.Errorf("summary leaked into send-keys: %q", sk.text)
		}
	}

	// Fire once: a second identical cycle does not re-summarize or re-notify.
	got, _ := d.sessions.Get(s.ID)
	d.react(context.Background(), got)
	if n := len(fb.callsFor(config.BrainApprovedInstruction)); n != 1 {
		t.Errorf("approved summary fired %d times, want once", n)
	}
	if n := len(seams.notesByPriority(notify.Action)); n != 1 {
		t.Errorf("approved notify fired %d times, want once", n)
	}
}

// --- per-cycle brain budget: bound the serial observe loop --------------------

// Every escalation summary in ONE observe cycle must share a SINGLE brain budget
// (one brainTimeout for the whole cycle, not per session) so a hung `claude -p`
// cannot serialize into N×timeout of stalled reactions to later sessions. The
// proof is that every Summarize call in the cycle sees the SAME context deadline.
func TestBrainEscalationSummariesShareOnePerCycleBudget(t *testing.T) {
	cfg := reactTestConfig(nativePoll("p1"))
	cfg.Reactions.CIFailed.Retries = 0 // escalate on the first ci_failed
	nat := &fakeNative{alive: map[string]bool{}}
	d := newTestDaemon(t, cfg, &linear.Fake{}, nat)
	seams := &fakeReactSeams{failing: "FAIL"}
	seams.install(d)
	(&fakeObsSeams{pr: openPR(9, "MERGEABLE", "", "fail")}).install(d) // ci_failed for every branch
	d.cfg.Brain = brainOn()
	d.paneTail = func(_ context.Context, _ string, _ int) (string, error) { return "PANE", nil }

	var (
		mu        sync.Mutex
		deadlines []time.Time
	)
	d.brainSummarize = func(ctx context.Context, _, _ string) (string, error) {
		dl, ok := ctx.Deadline()
		if !ok {
			t.Error("brain ctx carries no deadline — the per-cycle budget was not applied")
		}
		mu.Lock()
		deadlines = append(deadlines, dl)
		mu.Unlock()
		return "SUMMARY", nil
	}

	// Three native sessions that all derive ci_failed and escalate this cycle.
	for _, ident := range []string{"FE-1", "FE-2", "FE-3"} {
		s := nativeSess(ident, "working")
		s.IssueUUID = "uuid-" + strings.ToLower(ident)
		d.sessions.Upsert(s)
		nat.alive[s.ID] = true
	}

	d.observe(context.Background())

	mu.Lock()
	defer mu.Unlock()
	if len(deadlines) != 3 {
		t.Fatalf("escalation Summarize calls = %d, want 3 (one per escalating session)", len(deadlines))
	}
	for i, dl := range deadlines {
		if !dl.Equal(deadlines[0]) {
			t.Fatalf("Summarize call %d deadline %v != call 0 deadline %v — budget not shared across the cycle", i, dl, deadlines[0])
		}
	}
}

// A hung brain summary must be abortable at shutdown: because the summary is
// read-only, its bounded context descends from the shutdown-cancellable root
// (not the observe cycle's shielded WithoutCancel ctx), so cancellation aborts
// the in-flight `claude -p` instead of delaying graceful shutdown up to the
// brain timeout. The cycle then falls back to the generic escalation template.
func TestBrainSummaryAbortsOnShutdown(t *testing.T) {
	cfg := reactTestConfig(nativePoll("p1"))
	cfg.Reactions.CIFailed.Retries = 0
	nat := &fakeNative{alive: map[string]bool{}}
	d := newTestDaemon(t, cfg, &linear.Fake{}, nat)
	seams := &fakeReactSeams{failing: "FAIL"}
	seams.install(d)
	(&fakeObsSeams{pr: openPR(9, "MERGEABLE", "", "fail")}).install(d)
	d.cfg.Brain = brainOn()
	d.paneTail = func(_ context.Context, _ string, _ int) (string, error) { return "PANE", nil }

	shutCtx, shutCancel := context.WithCancel(context.Background())
	d.shutdownCtx = shutCtx

	entered := make(chan struct{})
	d.brainSummarize = func(ctx context.Context, _, _ string) (string, error) {
		close(entered)
		<-ctx.Done() // block until shutdown cancels the brain budget
		return "", ctx.Err()
	}

	s := nativeSess("FE-1", "working")
	s.IssueUUID = "uuid-fe-1"
	d.sessions.Upsert(s)
	nat.alive[s.ID] = true

	done := make(chan struct{})
	go func() {
		d.observe(context.WithoutCancel(shutCtx)) // mirror safeObserve's shielded observe ctx
		close(done)
	}()

	<-entered
	shutCancel() // shutdown must abort the in-flight (read-only) claude summary

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("observe did not return after shutdown cancelled the brain summary — brain call is not abortable")
	}

	urgent := seams.notesByPriority(notify.Urgent)
	if len(urgent) != 1 || !strings.Contains(urgent[0].Body, "handing off") {
		t.Fatalf("Urgent body = %+v, want the generic template after the shutdown abort", urgent)
	}
}

// Approved with the SummarizeApproved toggle off: the generic body is used and
// no PR diff is fetched or summarized.
func TestBrainApprovedToggleOffUsesGeneric(t *testing.T) {
	d := newTestDaemon(t, reactTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeNative{})
	seams := &fakeReactSeams{}
	seams.install(d)
	fb := &fakeBrain{out: "SHOULD-NOT-APPEAR"}
	bc := brainOn()
	bc.SummarizeApproved = false
	fb.install(d, bc)

	s := reactSess("FE-1", "approved", openPR(11, "MERGEABLE", "APPROVED", "pass"))
	s.AtPrompt = true
	d.sessions.Upsert(s)

	d.react(context.Background(), s)

	if n := len(fb.callsFor(config.BrainApprovedInstruction)); n != 0 {
		t.Fatalf("approved Summarize calls = %d with the toggle off, want 0", n)
	}
	action := seams.notesByPriority(notify.Action)
	if len(action) != 1 || !strings.Contains(action[0].Body, "ready to merge") {
		t.Fatalf("Action notify = %+v, want the generic template", action)
	}
}
