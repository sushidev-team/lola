package daemon

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sushidev-team/lola/internal/linear"
	"github.com/sushidev-team/lola/internal/session"
	"github.com/sushidev-team/lola/internal/state"
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
	// paneModal is claude-code's auto-mode setup overlay: the turn has ended, the
	// pane is a keypress-driven form, and the "❯" marks the focused ROW rather
	// than a composer. attention.Classify reads it as ActivityBlocked.
	paneModal = "  ⏺ Pushed the branch and opened the PR.\n" +
		"▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔▔\n" +
		"   Set up auto mode for your environment?\n" +
		"\n" +
		"     How you use Claude here    ◀ Mixed ▶\n" +
		"   ❯ Also scan shell history    [✔]\n" +
		"\n" +
		"     Continue\n" +
		"\n" +
		"   ←/→ to change usage · Enter to continue · Esc to cancel\n"
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
// resting input box must stop claiming work within one observe cycle — a
// definite wait cue beats even a fresh heartbeat.
//
// The DESTINATION changed with the flap fix. A resting composer with nothing to
// answer is IDLE, not needs_input: the old rule escalated it unconditionally
// while no PR existed, which made AgentIdle unreachable for a session's whole
// pre-PR life. AtPrompt is OPENED, because a resting composer is exactly the
// state "stop" asserts — observed directly instead of reported by a hook.
func TestObservePaneWaitingDowngradesFalseWorking(t *testing.T) {
	seed := nativeSess("FE-1", "working")
	seed.LastActivityAt = time.Now() // even a fresh heartbeat loses to a definite wait cue
	d := paneDaemon(t, seed, true, paneWaiting)

	d.observe(context.Background())

	got := getSess(t, d, seed.ID)
	if got.AgentState != state.AgentIdle {
		t.Fatalf("AgentState = %q, want idle (a resting composer with no question is not a question)", got.AgentState)
	}
	if got.Status != "idle" {
		t.Fatalf("status = %q, want idle", got.Status)
	}
	if !got.AtPrompt || !got.AtPromptVerified {
		t.Fatalf("AtPrompt/verified = %v/%v, want true/true — a resting composer is live proof the gate is open",
			got.AtPrompt, got.AtPromptVerified)
	}
}

// The pre-PR rule that produced the other half of the measured needs_input
// population, stated on its own: with NO PR open and NO answerable question,
// a resting composer used to read `hasQuestion || cur.Delivery ==
// state.DeliveryNone` and escalate to needs_input every single cycle. The
// delivery axis is a fact about the PR, never evidence about the agent.
func TestObservePaneRestingComposerPrePRIsIdleNotNeedsInput(t *testing.T) {
	seed := nativeSess("FE-1", "working")
	d := paneDaemon(t, seed, true, paneWaiting) // paneDaemon's PR seam reports no PR

	d.observe(context.Background())

	got := getSess(t, d, seed.ID)
	if got.Delivery != state.DeliveryNone {
		t.Fatalf("precondition: delivery = %q, want none (no PR)", got.Delivery)
	}
	if got.AgentState != state.AgentIdle || got.InputReason != "" {
		t.Fatalf("axis = %q/%q, want idle with no input reason", got.AgentState, got.InputReason)
	}
}

// A resting composer WITH an answerable question is the real thing and still
// parks on waiting_input with the gate closed — that is the signal the whole
// change exists to keep meaningful.
func TestObservePaneWaitingWithQuestionStillNeedsInput(t *testing.T) {
	seed := nativeSess("FE-1", "working")
	d := paneDaemon(t, seed, true, paneWaitingQuestion)

	d.observe(context.Background())

	got := getSess(t, d, seed.ID)
	if got.AgentState != state.AgentWaitingInput || got.InputReason != state.InputQuestion {
		t.Fatalf("axis = %q/%q, want waiting_input/question", got.AgentState, got.InputReason)
	}
	if got.AtPrompt {
		t.Error("AtPrompt = true, want false: a pending question must not open the send-keys gate")
	}
}

// A record parked on waiting_input by the IDLE NUDGE — written by a pre-change
// daemon, or carried across a restart by adoption — must be able to LEAVE on the
// next pane read. Without this it is stuck on "Needs You" forever: nothing
// demotes waiting_input except a new turn, and a session nobody is looking at
// never takes one.
func TestObservePaneReleasesNudgeParkedWaitingInput(t *testing.T) {
	seed := nativeSess("FE-1", "needs_input")
	seed.AgentState = state.AgentWaitingInput
	seed.InputReason = state.InputIdleNotify
	seed.LastNotification = "Claude is waiting for your input"
	d := paneDaemon(t, seed, true, paneWaiting)

	d.observe(context.Background())

	got := getSess(t, d, seed.ID)
	if got.AgentState != state.AgentIdle {
		t.Fatalf("AgentState = %q, want idle — a nudge-parked record must be releasable by the pane", got.AgentState)
	}
	if !got.AtPrompt {
		t.Error("AtPrompt = false, want the gate reopened for the released session")
	}
}

// The same release applies to the empty InputReason the OLD pre-PR rule minted
// for every resting pane — that shape is the bulk of the records on disk.
func TestObservePaneReleasesLegacyReasonlessWaitingInput(t *testing.T) {
	seed := nativeSess("FE-1", "needs_input")
	seed.AgentState = state.AgentWaitingInput
	seed.InputReason = "" // what agentReconcile wrote before the flap fix
	d := paneDaemon(t, seed, true, paneWaiting)

	d.observe(context.Background())

	if got := getSess(t, d, seed.ID); got.AgentState != state.AgentIdle {
		t.Fatalf("AgentState = %q, want idle", got.AgentState)
	}
}

// The load-bearing half of the release rule: a pane read may let go of a park it
// could itself have made, but it must NEVER overrule positive evidence of a real
// block. A modal, a y/n approval and a usage-limit banner can all leave a
// composer looking at rest while nothing can proceed — demoting them to idle
// would hand every send-keys path a gate it must not have.
func TestObservePaneKeepsPositiveBlockReasons(t *testing.T) {
	for _, reason := range []state.InputReason{state.InputPermission, state.InputDialog, state.InputQuotaLimited} {
		t.Run(string(reason), func(t *testing.T) {
			seed := nativeSess("FE-1", "needs_input")
			seed.AgentState = state.AgentWaitingInput
			seed.InputReason = reason
			d := paneDaemon(t, seed, true, paneWaiting)

			d.observe(context.Background())

			got := getSess(t, d, seed.ID)
			if got.AgentState != state.AgentWaitingInput || got.InputReason != reason {
				t.Fatalf("axis = %q/%q, want it held at waiting_input/%s", got.AgentState, got.InputReason, reason)
			}
			if got.AtPrompt {
				t.Error("AtPrompt = true, want the send-keys gate to stay CLOSED on a real block")
			}
		})
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
	if got.Status != "idle" {
		t.Fatalf("status = %q, want idle (a claude-agent session must classify its waiting pane just like the legacy default)", got.Status)
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
	t.Run("waiting stays idle", func(t *testing.T) {
		seed := nativeSess("FE-1", "working")
		d := paneDaemon(t, seed, true, paneWaiting)
		d.observe(context.Background())
		d.observe(context.Background())
		if got := getSess(t, d, seed.ID); got.Status != "idle" {
			t.Fatalf("status after two waiting cycles = %q, want a stable idle", got.Status)
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

// A modal overlay is a keypress-driven form, not a composer: the observer parks
// the session on needs_input with InputDialog and CLOSES the send-keys gate a
// Stop hook opened moments earlier. Without this the dialog's focused "❯" row
// read as a resting prompt and every gate stayed wide open over it.
func TestObservePaneModalParksOnDialogAndClosesGate(t *testing.T) {
	seed := nativeSess("FE-1", "working")
	seed.LastActivityAt = time.Now() // a fresh heartbeat must not keep it "working"
	seed.AtPrompt = true             // the Stop hook that fired just before the dialog
	d := paneDaemon(t, seed, true, paneModal)

	d.observe(context.Background())

	got := getSess(t, d, seed.ID)
	if got.Status != "needs_input" {
		t.Fatalf("status = %q, want needs_input (a modal blocks the agent)", got.Status)
	}
	if got.InputReason != state.InputDialog {
		t.Errorf("InputReason = %q, want %q", got.InputReason, state.InputDialog)
	}
	if got.AtPrompt {
		t.Error("AtPrompt = true; a modal must close the send-keys gate")
	}
	if !got.AtPromptVerified {
		t.Error("AtPromptVerified = false; the live pane is current evidence about the gate")
	}
}

// The delivery axis does not soften it: post-PR a bare resting prompt settles to
// idle (routine post-PR idling), but a modal still escalates — nothing advances
// until a human presses a key, and the session holds a concurrency slot meanwhile.
func TestObservePaneModalEscalatesEvenPostPR(t *testing.T) {
	seed := nativeSess("FE-1", "review_pending")
	seed.Delivery = state.DeliveryReviewPending
	seed.LastActivityAt = time.Now()
	d := paneDaemon(t, seed, true, paneModal)

	d.observe(context.Background())

	got := getSess(t, d, seed.ID)
	if got.AgentState != state.AgentWaitingInput {
		t.Fatalf("AgentState = %q, want waiting_input", got.AgentState)
	}
	if got.Status != "needs_input" {
		t.Errorf("status = %q, want needs_input", got.Status)
	}
}

// --- the transcript corroborator --------------------------------------------
//
// attention.Classify is a MIRROR of claude-code's rendering, and every cue in it
// documents its own fragility; two rewordings have each cost a debugging session
// (CLAUDE.md). internal/agentlog reads the agent's OWN JSONL transcript instead,
// which is a fact about its conversation state rather than about how it paints a
// terminal. These tests pin where that second opinion is allowed to win — and,
// just as important, where it is not.

// Canned transcript records, trimmed to the fields internal/agentlog decodes.
// The bookkeeping shapes are verbatim from live files in ~/.claude/projects:
// real transcripts routinely END on one, which is why the reader scans backward
// rather than reading "the last line".
const (
	jsonlToolUse = `{"type":"assistant","isSidechain":false,"message":{"role":"assistant","stop_reason":"tool_use","content":[{"type":"tool_use","name":"Bash"}]}}`
	jsonlEndTurn = `{"type":"assistant","isSidechain":false,"message":{"role":"assistant","stop_reason":"end_turn","content":[{"type":"text","text":"Done."}]}}`
	jsonlNoise   = `{"type":"last-prompt","lastPrompt":"go on","leafUuid":"x"}`
)

// transcriptFile writes a transcript into the test's temp dir and stamps its
// mtime, so "how long has this file been quiet" is a test input rather than a
// race with the clock. It also resets the shared reader: the cache is a package
// var (see observer.go) and an assertion on Len must start from zero.
func transcriptFile(t *testing.T, age time.Duration, lines ...string) string {
	t.Helper()
	transcripts.Reset()
	p := filepath.Join(t.TempDir(), "transcript.jsonl")
	body := ""
	for _, l := range lines {
		body += l + "\n"
	}
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatalf("write transcript: %v", err)
	}
	mod := time.Now().Add(-age)
	if err := os.Chtimes(p, mod, mod); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
	return p
}

// THE HEADLINE CASE. claude-code draws its composer mid-turn too, so a resting
// caret with no recognized status line means only "no working cue matched" —
// exactly what a reworded status line produces, and exactly the shape that
// silently disabled the classifier twice before. The transcript still knows a
// tool is in flight, so the session stays working and the send-keys gate stays
// SHUT.
func TestObserveTranscriptBeatsARestingComposer(t *testing.T) {
	seed := nativeSess("FE-1", "working")
	seed.AtPrompt = true // a Stop hook from the PREVIOUS turn
	seed.TranscriptPath = transcriptFile(t, 2*time.Second, jsonlEndTurn, jsonlToolUse, jsonlNoise)
	d := paneDaemon(t, seed, true, paneWaiting)

	d.observe(context.Background())

	got := getSess(t, d, seed.ID)
	if got.AgentState != state.AgentWorking {
		t.Fatalf("AgentState = %q, want working — the transcript says a tool is in flight", got.AgentState)
	}
	if got.ActivitySource != state.SourceTranscript {
		t.Errorf("ActivitySource = %q, want %q so the next reader knows the pane was not the witness",
			got.ActivitySource, state.SourceTranscript)
	}
	if got.AtPrompt {
		t.Error("AtPrompt = true; a turn in flight must close the send-keys gate")
	}
}

// The other side of the same branch: when the transcript agrees that the turn
// ended, the resting composer settles to idle with the gate OPEN, byte-identical
// to the behavior before the transcript existed.
func TestObserveTranscriptQuietLeavesTheRestingComposerIdle(t *testing.T) {
	seed := nativeSess("FE-1", "working")
	seed.TranscriptPath = transcriptFile(t, 2*time.Second, jsonlToolUse, jsonlEndTurn, jsonlNoise)
	d := paneDaemon(t, seed, true, paneWaiting)

	d.observe(context.Background())

	got := getSess(t, d, seed.ID)
	if got.AgentState != state.AgentIdle {
		t.Fatalf("AgentState = %q, want idle", got.AgentState)
	}
	if !got.AtPrompt || !got.AtPromptVerified {
		t.Errorf("AtPrompt/verified = %v/%v, want true/true", got.AtPrompt, got.AtPromptVerified)
	}
}

// FAIL TOWARD THE STATUS QUO. Every way a transcript can be unusable must leave
// the pane-driven outcome exactly as it was — this is the whole safety claim of
// reading another program's file format.
func TestObserveUnusableTranscriptChangesNothing(t *testing.T) {
	cases := map[string]func(t *testing.T) string{
		"no path recorded": func(t *testing.T) string { transcripts.Reset(); return "" },
		"file is gone": func(t *testing.T) string {
			transcripts.Reset()
			return filepath.Join(t.TempDir(), "vanished.jsonl")
		},
		"malformed json": func(t *testing.T) string {
			return transcriptFile(t, time.Hour, `{"type":"assist`, `not json`)
		},
		"only bookkeeping records": func(t *testing.T) string {
			return transcriptFile(t, time.Hour, jsonlNoise, jsonlNoise)
		},
		"turn in flight but far too old": func(t *testing.T) string {
			return transcriptFile(t, 24*time.Hour, jsonlToolUse)
		},
	}
	for name, mk := range cases {
		t.Run(name, func(t *testing.T) {
			seed := nativeSess("FE-1", "working")
			seed.TranscriptPath = mk(t)
			d := paneDaemon(t, seed, true, paneWaiting)

			d.observe(context.Background())

			got := getSess(t, d, seed.ID)
			if got.AgentState != state.AgentIdle {
				t.Fatalf("AgentState = %q, want the unchanged pane-driven idle", got.AgentState)
			}
			if !got.AtPrompt {
				t.Error("AtPrompt = false, want the pane-driven gate still opened")
			}
		})
	}
}

// A MODAL is a fact about the screen with no transcript record behind it — the
// agent wrote its tool_use and then put a keypress-driven form over the pane.
// The screen wins, or a review hand-off gets typed into the dialog and vanishes.
func TestObserveModalBeatsAnInFlightTranscript(t *testing.T) {
	seed := nativeSess("FE-1", "working")
	seed.TranscriptPath = transcriptFile(t, 2*time.Second, jsonlToolUse)
	d := paneDaemon(t, seed, true, paneModal)

	d.observe(context.Background())

	got := getSess(t, d, seed.ID)
	if got.AgentState != state.AgentWaitingInput || got.InputReason != state.InputDialog {
		t.Fatalf("axis = %q/%q, want waiting_input/dialog — a modal outranks the transcript",
			got.AgentState, got.InputReason)
	}
	if got.AtPrompt {
		t.Error("AtPrompt = true; a modal must close the send-keys gate")
	}
}

// THE PERMISSION-PROMPT CASE, and the reason the transcript sits BELOW an
// answerable question. claude-code writes the assistant record carrying the
// tool_use and THEN asks for approval, so the file reads "a tool is in flight"
// for the whole time the agent sits waiting on a human. Only the screen witnesses
// that.
func TestObserveAnswerableQuestionBeatsAnInFlightTranscript(t *testing.T) {
	seed := nativeSess("FE-1", "working")
	seed.TranscriptPath = transcriptFile(t, 2*time.Second, jsonlToolUse)
	d := paneDaemon(t, seed, true, paneWaitingQuestion)

	d.observe(context.Background())

	got := getSess(t, d, seed.ID)
	if got.AgentState != state.AgentWaitingInput || got.InputReason != state.InputQuestion {
		t.Fatalf("axis = %q/%q, want waiting_input/question — a pending approval is an in-flight tool call in the file",
			got.AgentState, got.InputReason)
	}
	if got.AtPrompt {
		t.Error("AtPrompt = true; a pending question must not open the send-keys gate")
	}
}

// The same protection one layer down: a session already parked on positive block
// evidence must not be promoted out of it by the transcript, even when the pane
// has stopped rendering the question that put it there.
func TestObserveTranscriptNeverPromotesAPositiveBlock(t *testing.T) {
	for _, reason := range []state.InputReason{state.InputPermission, state.InputDialog, state.InputQuotaLimited} {
		t.Run(string(reason), func(t *testing.T) {
			seed := nativeSess("FE-1", "needs_input")
			seed.AgentState = state.AgentWaitingInput
			seed.InputReason = reason
			seed.TranscriptPath = transcriptFile(t, 2*time.Second, jsonlToolUse)
			d := paneDaemon(t, seed, true, paneWaiting)

			d.observe(context.Background())

			got := getSess(t, d, seed.ID)
			if got.AgentState != state.AgentWaitingInput || got.InputReason != reason {
				t.Fatalf("axis = %q/%q, want it held at waiting_input/%s", got.AgentState, got.InputReason, reason)
			}
			if got.AtPrompt {
				t.Error("AtPrompt = true, want the gate to stay CLOSED on a real block")
			}
		})
	}
}

// THE LONG-BUILD CASE. A dispatched tool writes nothing until it returns, so an
// agent 20 minutes into a test suite has no hook, no tmux activity and nothing
// recognizable on screen — and the anti-false-working guard used to give up on
// it after 45 seconds. The transcript still says the tool is out.
func TestObserveTranscriptSustainsWorkingUnderAnUnknownPane(t *testing.T) {
	seed := nativeSess("FE-1", "working")
	seed.LastActivityAt = time.Now().Add(-4 * staleWorkingThreshold) // long past the guard
	seed.TranscriptPath = transcriptFile(t, 10*time.Minute, jsonlToolUse)
	d := paneDaemon(t, seed, true, paneUnknown)

	before := time.Now()
	d.observe(context.Background())

	got := getSess(t, d, seed.ID)
	if got.AgentState != state.AgentWorking {
		t.Fatalf("AgentState = %q, want working held (a tool is still out)", got.AgentState)
	}
	if got.LastActivityAt.Before(before) {
		t.Fatalf("LastActivityAt = %v, want re-stamped at/after %v so the guard stands down next cycle too",
			got.LastActivityAt, before)
	}
	if got.ActivitySource != state.SourceTranscript {
		t.Errorf("ActivitySource = %q, want %q", got.ActivitySource, state.SourceTranscript)
	}
}

// ...and the mirror image: the agent's own file recording that the turn stopped
// is better evidence than waiting out a timer, so the downgrade happens NOW
// rather than after staleWorkingThreshold. It must NOT open the gate — the pane
// is unreadable, and nothing that cannot see the screen may open a send-keys
// gate.
func TestObserveTranscriptIdleDowngradesWithoutWaitingOutTheGuard(t *testing.T) {
	seed := nativeSess("FE-1", "working")
	seed.LastActivityAt = time.Now() // well inside the guard's window
	seed.AtPrompt = true
	seed.TranscriptPath = transcriptFile(t, 5*time.Second, jsonlToolUse, jsonlEndTurn)
	d := paneDaemon(t, seed, true, paneUnknown)

	d.observe(context.Background())

	got := getSess(t, d, seed.ID)
	if got.AgentState != state.AgentIdle {
		t.Fatalf("AgentState = %q, want idle (the transcript recorded the turn ending)", got.AgentState)
	}
	if got.AtPrompt {
		t.Error("AtPrompt = true; an unreadable pane must never open the send-keys gate")
	}
}

// The transcript may only SUSTAIN work already believed to be underway. Promoting
// a parked session while the pane says nothing would have no screen evidence
// behind it at all — and the permission-prompt shape above is exactly what it
// would get wrong.
func TestObserveTranscriptDoesNotPromoteUnderAnUnknownPane(t *testing.T) {
	seed := nativeSess("FE-1", "needs_input")
	seed.TranscriptPath = transcriptFile(t, 2*time.Second, jsonlToolUse)
	d := paneDaemon(t, seed, true, paneUnknown)

	d.observe(context.Background())

	if got := getSess(t, d, seed.ID); got.Status != "needs_input" {
		t.Fatalf("status = %q, want needs_input preserved under an Unknown pane", got.Status)
	}
}

// Only claude writes a transcript, and Session.TranscriptPath is populated
// exclusively from its hooks — so a codex/opencode session must not even stat a
// file. Proven against the reader's own bookkeeping: the codex run leaves no
// entry behind, while the identical claude run leaves exactly one.
func TestObserveNonClaudeSessionNeverReadsATranscript(t *testing.T) {
	path := transcriptFile(t, 2*time.Second, jsonlToolUse)

	for _, tc := range []struct {
		agent   string
		want    state.AgentState
		entries int
	}{
		{"codex", state.AgentIdle, 0},
		{"opencode", state.AgentIdle, 0},
		{"claude", state.AgentWorking, 1},
	} {
		t.Run(tc.agent, func(t *testing.T) {
			transcripts.Reset()
			seed := nativeSess("FE-1", "working")
			seed.Agent = tc.agent
			seed.TranscriptPath = path
			d := paneDaemon(t, seed, true, paneWaiting)

			d.observe(context.Background())

			if got := getSess(t, d, seed.ID); got.AgentState != tc.want {
				t.Fatalf("AgentState = %q, want %q", got.AgentState, tc.want)
			}
			if n := transcripts.Len(); n != tc.entries {
				t.Fatalf("reader tracked %d transcripts, want %d — %s must%s touch the file",
					n, tc.entries, tc.agent, map[bool]string{true: "", false: " never"}[tc.entries > 0])
			}
		})
	}
}

// A DEAD pane is terminal for the agent axis, so the transcript could not be used
// anyway — and a killed session's file would go on claiming a tool was in flight
// for workingClaimMaxAge. It must not be read at all.
func TestObserveDeadPaneNeverReadsATranscript(t *testing.T) {
	seed := nativeSess("FE-1", "working")
	seed.TranscriptPath = transcriptFile(t, 2*time.Second, jsonlToolUse)
	d := paneDaemon(t, seed, false, paneWaiting) // pane gone

	d.observe(context.Background())

	got := getSess(t, d, seed.ID)
	if got.AgentState != state.AgentDead {
		t.Fatalf("AgentState = %q, want dead — an in-flight transcript must not resurrect a gone pane", got.AgentState)
	}
	if n := transcripts.Len(); n != 0 {
		t.Fatalf("reader tracked %d transcripts, want 0 for a dead pane", n)
	}
}

// No flapping across cycles, with the transcript in play: a held tool_use stays
// working and a recorded end_turn stays idle, however many times the loop runs.
func TestObserveTranscriptDoesNotFlapAcrossCycles(t *testing.T) {
	t.Run("in flight stays working", func(t *testing.T) {
		seed := nativeSess("FE-1", "working")
		seed.TranscriptPath = transcriptFile(t, 2*time.Second, jsonlToolUse)
		d := paneDaemon(t, seed, true, paneWaiting)
		d.observe(context.Background())
		d.observe(context.Background())
		if got := getSess(t, d, seed.ID); got.Status != "working" {
			t.Fatalf("status after two cycles = %q, want a stable working", got.Status)
		}
	})
	t.Run("ended stays idle", func(t *testing.T) {
		seed := nativeSess("FE-1", "working")
		seed.TranscriptPath = transcriptFile(t, 2*time.Second, jsonlEndTurn)
		d := paneDaemon(t, seed, true, paneWaiting)
		d.observe(context.Background())
		d.observe(context.Background())
		if got := getSess(t, d, seed.ID); got.Status != "idle" {
			t.Fatalf("status after two cycles = %q, want a stable idle", got.Status)
		}
	})
}
