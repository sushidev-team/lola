package daemon

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/sushidev-team/lola/internal/linear"
	"github.com/sushidev-team/lola/internal/session"
)

// Canned tmux pane tails the activity classifier reads unambiguously (mirrors
// internal/attention's own fixtures): a live status line, a resting input box,
// and plain scrolled output with neither cue.
const (
	paneWorking = "✻ Cerebrating… (esc to interrupt · 4s)\n"
	paneWaiting = "╭──────────────────────────────────────────────╮\n" +
		"│ >                                              │\n" +
		"╰──────────────────────────────────────────────╯\n" +
		"  ? for shortcuts\n"
	paneUnknown = "Compiling module foo...\nok  \tgithub.com/foo/bar\t0.123s\nAll tests passed.\n"
	// A resting input box with an ANSWERABLE question above it — the classifier
	// reads it as waiting and attention.Parse extracts the question, so it is
	// positive evidence the agent is blocked on a human.
	paneWaitingQuestion = "⏺ PR is up.\n" +
		"╭────────────────────────────────────────────────────────╮\n" +
		"│ Do you want to proceed?                                  │\n" +
		"│ ❯ 1. Yes                                                 │\n" +
		"│   2. No                                                  │\n" +
		"╰────────────────────────────────────────────────────────╯\n"
)

// paneDaemon builds a native-only test daemon with one seeded session, a PR seam
// that reports no PR (so the pre-PR pane reconcile is in play), and a paneTail
// seam returning a fixed canned pane. The session is alive unless told otherwise.
func paneDaemon(t *testing.T, seed session.Session, alive bool, pane string) *Daemon {
	t.Helper()
	d := newTestDaemon(t, nativeTestConfig(nativePoll("p1")), &linear.Fake{},
		&fakeNative{alive: map[string]bool{seed.ID: alive}})
	(&fakeObsSeams{}).install(d) // pr nil → prForBranch returns (nil, nil): no PR
	d.paneTail = func(ctx context.Context, tmuxName string, lines int) (string, error) {
		return pane, nil
	}
	d.sessions.Upsert(seed)
	return d
}

func getSess(t *testing.T, d *Daemon, id string) session.Session {
	t.Helper()
	s, ok := d.sessions.Get(id)
	if !ok {
		t.Fatalf("session %s vanished from store", id)
	}
	return s
}

// THE BUG FIX: a session the hooks left as "working" whose live pane shows the
// resting input box (a human is awaited) must surface as needs_input within one
// observe cycle, with AtPrompt cleared.
func TestObservePaneWaitingDowngradesFalseWorking(t *testing.T) {
	seed := nativeSess("FE-1", "working")
	seed.LastActivityAt = time.Now() // even a fresh heartbeat loses to a definite wait cue
	d := paneDaemon(t, seed, true, paneWaiting)

	d.observe(context.Background())

	got := getSess(t, d, seed.ID)
	if got.Status != "needs_input" {
		t.Fatalf("status = %q, want needs_input (waiting pane must beat a stale working)", got.Status)
	}
	if got.AtPrompt {
		t.Fatalf("AtPrompt = true, want false for a pane-derived needs_input")
	}
}

// The observer classifies the pane against the SESSION's coding-agent kind
// (Session.Agent). An explicit "claude" must behave identically to the legacy
// empty Agent (both resolve to the Claude cue set) — proof the kind is threaded
// through to attention.Classify/Parse without changing the Claude path.
func TestObservePaneClassifiesAgainstSessionAgent(t *testing.T) {
	seed := nativeSess("FE-1", "working")
	seed.Agent = "claude" // explicit; must match the empty-Agent behavior
	seed.LastActivityAt = time.Now()
	d := paneDaemon(t, seed, true, paneWaiting)

	d.observe(context.Background())

	got := getSess(t, d, seed.ID)
	if got.Status != "needs_input" {
		t.Fatalf("status = %q, want needs_input (a claude-agent session must classify its waiting pane just like the legacy default)", got.Status)
	}
	if got.Agent != "claude" {
		t.Fatalf("Agent = %q, want it preserved as claude across the observe cycle", got.Agent)
	}
}

// A genuinely working pane keeps the session working AND stamps LastActivityAt —
// positive evidence the anti-false-working guard later relies on.
func TestObservePaneWorkingStampsActivity(t *testing.T) {
	seed := nativeSess("FE-1", "working")
	seed.LastActivityAt = time.Time{} // no prior evidence
	d := paneDaemon(t, seed, true, paneWorking)

	before := time.Now()
	d.observe(context.Background())

	got := getSess(t, d, seed.ID)
	if got.Status != "working" {
		t.Fatalf("status = %q, want working", got.Status)
	}
	if got.LastActivityAt.Before(before) {
		t.Fatalf("LastActivityAt = %v, want stamped at/after %v (a working pane is positive evidence)", got.LastActivityAt, before)
	}
}

// An agent that RESUMED: a stored needs_input whose pane now shows a live working
// cue goes back to working (positive proof of work beats the stale wait state).
func TestObservePaneWorkingResumesNeedsInput(t *testing.T) {
	seed := nativeSess("FE-1", "needs_input")
	d := paneDaemon(t, seed, true, paneWorking)

	d.observe(context.Background())

	got := getSess(t, d, seed.ID)
	if got.Status != "working" {
		t.Fatalf("status = %q, want working (a working pane must resume a needs_input)", got.Status)
	}
}

// An Unknown pane must NOT change the status: a very recent hook (working from a
// tool_use/user_prompt within the activity window) wins over an ambiguous pane.
func TestObservePaneUnknownKeepsRecentWorking(t *testing.T) {
	seed := nativeSess("FE-1", "working")
	seed.LastActivityAt = time.Now().Add(-5 * time.Second) // fresh heartbeat
	d := paneDaemon(t, seed, true, paneUnknown)

	d.observe(context.Background())

	got := getSess(t, d, seed.ID)
	if got.Status != "working" {
		t.Fatalf("status = %q, want working (recent activity + Unknown pane keeps working)", got.Status)
	}
}

// Anti-false-working guard: a "working" with no positive activity for longer than
// staleWorkingThreshold, that an Unknown pane cannot confirm, must stop asserting
// work — here it falls back to idle (no question visible).
func TestObservePaneUnknownStaleDowngradesFalseWorking(t *testing.T) {
	seed := nativeSess("FE-1", "working")
	seed.LastActivityAt = time.Now().Add(-2 * staleWorkingThreshold) // long stale
	d := paneDaemon(t, seed, true, paneUnknown)

	d.observe(context.Background())

	got := getSess(t, d, seed.ID)
	if got.Status != "idle" {
		t.Fatalf("status = %q, want idle (a stale unconfirmable working must not keep claiming work)", got.Status)
	}
	if got.AtPrompt {
		t.Fatalf("AtPrompt = true, want false after downgrading a false working")
	}
}

// A hook-set needs_input must survive an Unknown pane untouched (never upgraded to
// working, never flipped to idle) — the pane only reinforces, never clobbers it.
func TestObservePaneUnknownDoesNotClobberNeedsInput(t *testing.T) {
	seed := nativeSess("FE-1", "needs_input")
	d := paneDaemon(t, seed, true, paneUnknown)

	d.observe(context.Background())

	got := getSess(t, d, seed.ID)
	if got.Status != "needs_input" {
		t.Fatalf("status = %q, want needs_input preserved under an Unknown pane", got.Status)
	}
}

// No flapping: two identical cycles must not bounce the status. A waiting pane
// downgrades once to needs_input and STAYS there; a working pane stays working.
func TestObservePaneDoesNotFlapAcrossCycles(t *testing.T) {
	t.Run("waiting stays needs_input", func(t *testing.T) {
		seed := nativeSess("FE-1", "working")
		d := paneDaemon(t, seed, true, paneWaiting)
		d.observe(context.Background())
		d.observe(context.Background())
		if got := getSess(t, d, seed.ID); got.Status != "needs_input" {
			t.Fatalf("status after two waiting cycles = %q, want a stable needs_input", got.Status)
		}
	})
	t.Run("working stays working", func(t *testing.T) {
		seed := nativeSess("FE-1", "working")
		d := paneDaemon(t, seed, true, paneWorking)
		d.observe(context.Background())
		d.observe(context.Background())
		if got := getSess(t, d, seed.ID); got.Status != "working" {
			t.Fatalf("status after two working cycles = %q, want a stable working", got.Status)
		}
	})
}

// Finding 2: a capture-pane FAILURE on an alive session must be treated as an
// Unknown pane, not skipped — otherwise the anti-false-working staleness guard
// never runs and a hook-stuck "working" that the pane cannot confirm stays
// working forever. Here a long-stale working with an unreadable pane downgrades.
func TestObservePaneCaptureFailureRunsStalenessGuard(t *testing.T) {
	seed := nativeSess("FE-1", "working")
	seed.LastActivityAt = time.Now().Add(-2 * staleWorkingThreshold) // long stale
	d := paneDaemon(t, seed, true, "")
	d.paneTail = func(ctx context.Context, tmuxName string, lines int) (string, error) {
		return "", errors.New("capture-pane: no server running")
	}

	d.observe(context.Background())

	got := getSess(t, d, seed.ID)
	if got.Status != "idle" {
		t.Fatalf("status = %q, want idle (an unreadable pane must not keep a stale working trusted)", got.Status)
	}
}

// Finding 3: an agent that asks a plain-text question and waits AFTER opening a
// PR (no reliable hook) must still surface as needs_input within one cycle — the
// PR-derived status must not mask it. A definite waiting pane WITH a question is
// the positive evidence.
func TestObservePanePostPRWaitingQuestionSurfacesNeedsInput(t *testing.T) {
	seed := nativeSess("FE-1", "working")
	d := newTestDaemon(t, nativeTestConfig(nativePoll("p1")), &linear.Fake{},
		&fakeNative{alive: map[string]bool{seed.ID: true}})
	(&fakeObsSeams{pr: openPR(7, "MERGEABLE", "", "")}).install(d) // open PR exists
	d.paneTail = func(ctx context.Context, tmuxName string, lines int) (string, error) {
		return paneWaitingQuestion, nil
	}
	d.sessions.Upsert(seed)

	d.observe(context.Background())

	got := getSess(t, d, seed.ID)
	if got.Status != "needs_input" {
		t.Fatalf("status = %q, want needs_input (a post-PR waiting question must surface, not be masked by the PR status)", got.Status)
	}
	if got.AtPrompt {
		t.Fatalf("AtPrompt = true, want false for a pane-derived needs_input")
	}
}

// Finding 3 guard: a post-PR pane with NO answerable question (routine idling at
// the prompt) must NOT be escalated — it keeps its PR-derived status.
func TestObservePanePostPRIdleKeepsPRStatus(t *testing.T) {
	seed := nativeSess("FE-1", "working")
	d := newTestDaemon(t, nativeTestConfig(nativePoll("p1")), &linear.Fake{},
		&fakeNative{alive: map[string]bool{seed.ID: true}})
	// Approved + green derives to "approved"; a bare resting box (no question)
	// must not flip that to needs_input.
	(&fakeObsSeams{pr: openPR(7, "MERGEABLE", "APPROVED", "pass")}).install(d)
	d.paneTail = func(ctx context.Context, tmuxName string, lines int) (string, error) {
		return paneWaiting, nil
	}
	d.sessions.Upsert(seed)

	d.observe(context.Background())

	got := getSess(t, d, seed.ID)
	if got.Status == "needs_input" {
		t.Fatalf("status = needs_input, want the PR-derived status (a question-less idle box must not escalate post-PR)")
	}
}
