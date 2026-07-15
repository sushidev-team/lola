package attention

// This file adds an ACTIVITY classifier on top of the same pane-capture text
// that Parse consumes. Where Parse asks "is there an answerable question?",
// Classify asks the coarser, load-bearing question the daemon needs to trust:
// "is this pane actively DOING something, or has the agent yielded the turn?"
//
// Why it exists: Claude Code's hooks do not reliably fire when an agent asks a
// plain-text question and waits — Notification fires for permission prompts and
// delayed-idle, not for every wait — so the last hook (a tool_use/user_prompt
// heartbeat) can leave a session stuck in "working" while a human is actually
// being waited on. The observer therefore corroborates the hook against the
// live pane each cycle. The invariant this classifier upholds:
//
//	NEVER report ActivityWorking without POSITIVE evidence of activity.
//	Ambiguity is ActivityUnknown (fall back to the hook), never Working.
//
// A false "working" is precisely the bug being fixed, so every working cue here
// is deliberately specific and each documents its own fragility. A false
// "waiting" is far cheaper (it just surfaces the session as needing input one
// cycle early), so the waiting cues are allowed to be a touch more liberal.

import (
	"regexp"
	"strings"
)

// Activity is a coarse read of what a pane is doing right now, derived purely
// from its rendered text.
type Activity int

const (
	// ActivityUnknown means neither a working nor a waiting cue was discernible
	// (blank pane, plain output scrolling past). It is the SAFE default: callers
	// must fall back to the hook-derived status and must never promote Unknown to
	// "working".
	ActivityUnknown Activity = iota
	// ActivityWorking means the pane shows a live activity indicator — a running
	// spinner, an elapsed-time status line, a streaming token counter, or the
	// literal "esc to interrupt" affordance. Only ever returned on a POSITIVE cue.
	ActivityWorking
	// ActivityWaiting means the pane shows an input prompt at rest — the bordered
	// input box or a bare caret — with NO active spinner, i.e. the agent has
	// yielded the turn and is waiting for a human, whether or not a question is
	// visible above the prompt.
	ActivityWaiting
)

// String renders the activity for logs and test failures.
func (a Activity) String() string {
	switch a {
	case ActivityWorking:
		return "working"
	case ActivityWaiting:
		return "waiting"
	default:
		return "unknown"
	}
}

// maxScreenLines bounds how many trailing lines Classify inspects for WAITING
// cues. The input box always sits at the very bottom of a claude-code pane, so a
// few dozen lines is plenty; scanning deeper into scrollback only invites false
// positives from a prompt that has since scrolled off.
const maxScreenLines = 80

// statusTailLines bounds how many trailing lines Classify inspects for WORKING
// cues — a MUCH tighter window than the waiting scan. claude-code's live status
// line (spinner / elapsed timer / token meter / "esc to interrupt") renders in
// the bottom cluster, immediately adjacent to the input box, so it always lands
// within the last handful of rows. Restricting the working scan to this tail is
// what stops a STALE cue that has scrolled up into ordinary scrollback — a
// braille progress frame left by `pnpm install`, the literal phrase "esc to
// interrupt" an agent printed while editing this repo — from being read as live
// activity. A stale working cue is precisely the sticky false-"working" bug;
// pinning working detection to the live tail keeps it from winning over a
// clearly-waiting input box below it. Fragility: assumes the status line stays
// in the bottom cluster; a build that floats it far above the box would need
// this widened, but the waiting scan below would still catch the resting prompt.
const statusTailLines = 10

var (
	// -----------------------------------------------------------------------
	// WORKING cues. Each is INDEPENDENTLY sufficient. On claude-code's status
	// line they co-occur (spinner + elapsed timer + token meter + "esc to
	// interrupt" all on one line), so the classifier degrades gracefully if any
	// single rendering detail changes. Listed most-robust first.
	// -----------------------------------------------------------------------

	// escInterruptRe — the "esc to interrupt" affordance claude-code prints for
	// the whole duration a turn is streaming. This is the single most reliable
	// working cue: the exact phrase does not occur in ordinary tool output.
	// Fragility: the wording is claude-code's own; a reworded build ("press esc
	// to stop") would slip past — which is why the other cues exist.
	escInterruptRe = regexp.MustCompile(`(?i)esc to interrupt`)

	// tokenCounterRe — the LIVE token meter, e.g. "↓ 4.5k tokens" / "↑ 512 tokens".
	// The leading up/down streaming arrow is mandatory on purpose: it is what
	// distinguishes an in-flight counter from a STATIC context read-out such as
	// "context left: 12k tokens", which appears next to an idle prompt and must
	// NOT be mistaken for activity. Fragility: the arrow glyph set and the word
	// "tokens" are claude-code specifics.
	tokenCounterRe = regexp.MustCompile(`(?i)[↓↑↕⇡⇣⬆⬇]\s*\d[\d.,]*\s*[km]?\s*tokens\b`)

	// gerundTimerRe — a status word ending in an ellipsis immediately followed by
	// a parenthesised elapsed time, e.g. "Deciphering… (2m 6s)". The ellipsis is
	// the disambiguator: it keeps incidental "(1.23s)" durations in test/log
	// output from matching. The time body is anchored to whole-second form right
	// after "(", so "(1.23s)" (which starts with a fraction) cannot match either.
	// Fragility: assumes the trailing ellipsis and a wall-clock-shaped timer; the
	// esc/token cues cover the same status line when the ellipsis is absent.
	gerundTimerRe = regexp.MustCompile(`(?:…|\.\.\.)\s*\(\s*(?:\d+h\s*)?(?:\d+m\s*)?\d+s\b`)

	// brailleSpinnerRe — a Braille Patterns glyph (U+2800–U+28FF) at the START of
	// a line (after optional whitespace). CLI spinners animate through these
	// frames as the leading glyph of a status line, so a line that BEGINS with one
	// is a working cue. Anchoring to line-lead (like spinnerStatusRe) — rather than
	// matching a glyph anywhere — keeps a braille glyph embedded mid-line in
	// ordinary output or prose from tripping it. Combined with the statusTailLines
	// window in hasWorkingCue, a stale spinner frame that has scrolled up out of
	// the live status cluster no longer masquerades as activity. Fragility: a
	// build whose spinner frame is not the line's first glyph would slip past —
	// which is why the esc/token/gerund cues also cover the same status line.
	brailleSpinnerRe = regexp.MustCompile(`(?m)^\s*[\x{2800}-\x{28FF}]`)

	// spinnerStatusRe — a status line of the shape "<spinner glyph> <Word>…", the
	// circle/star spinner claude-code cycles through ("✳ Simmering…", "◐ Working…").
	// Requiring a leading glyph AND a following word that ends in an ellipsis is
	// what keeps a plain bullet ("● item", "○ todo") — which never ends in "…" —
	// from matching. Fragility: the glyph set and the trailing ellipsis are
	// claude-code rendering details.
	spinnerStatusRe = regexp.MustCompile(`(?m)^\s*[○◐◓◑◒◔◕◖◗◜◝◞◟✢✳✶✷✸✺✻✽]\s*\p{L}[\p{L}\x{2019}'-]*…`)

	// -----------------------------------------------------------------------
	// WAITING cues.
	// -----------------------------------------------------------------------

	// boxBorderRe — a rounded/square box-drawing corner, i.e. the frame of
	// claude-code's input box. A frame on screen lets a bare ">" caret be trusted
	// as an input prompt rather than a shell prompt, a quoted diff, or markdown.
	boxBorderRe = regexp.MustCompile(`[╭╮╰╯┌┐└┘]`)

	// claudeCaretRe — the "❯" input caret (optionally followed by editable text).
	// Distinctive enough to trust on its own: starship/claude use it, plain tool
	// output does not.
	claudeCaretRe = regexp.MustCompile(`^❯(\s|$)`)

	// plainCaretRe — a ">" input caret. Trusted only when a box frame is ALSO on
	// screen, because ">" alone is ambiguous (shell prompt, quoted line, blockquote).
	plainCaretRe = regexp.MustCompile(`^>(\s|$)`)
)

// Classify strips ANSI from paneText, restricts itself to the last rendered
// screen, and returns:
//
//   - ActivityWorking when a positive activity cue is present (spinner, elapsed
//     timer, streaming token counter, or "esc to interrupt");
//   - ActivityWaiting when an input prompt is present with NO such cue (the
//     bordered box, a "❯" caret, a boxed ">" caret, or an answerable question
//     per Parse);
//   - ActivityUnknown otherwise.
//
// Working is checked first and wins WHEN a live cue is in the status tail: a
// genuinely working pane still renders its input box below the status line, and
// the live spinner is the ground truth. But the working scan is pinned to the
// last statusTailLines rows (the live status cluster), so a cue that has since
// scrolled up into scrollback does NOT win over the resting input box below it —
// that stale-cue-beats-waiting case is the sticky false-"working" bug. Input
// size is bounded to the tail (maxInput); working scans the last
// statusTailLines lines, waiting the last maxScreenLines lines.
func Classify(paneText string) Activity {
	if len(paneText) > maxInput {
		paneText = paneText[len(paneText)-maxInput:]
	}
	clean := stripANSI(paneText)
	screen := lastLines(clean, maxScreenLines)
	tail := lastLines(clean, statusTailLines)

	if hasWorkingCue(tail) {
		return ActivityWorking
	}
	if hasWaitingCue(paneText, screen) {
		return ActivityWaiting
	}
	return ActivityUnknown
}

// hasWorkingCue reports whether the (ANSI-stripped) live status tail shows any
// live activity indicator. Any single cue suffices; see each regexp for its cue
// and fragility. Callers MUST pass only the status tail (last statusTailLines),
// not the full screen — scanning deeper reintroduces the scrollback false
// positive this window exists to prevent.
func hasWorkingCue(tail string) bool {
	return escInterruptRe.MatchString(tail) ||
		tokenCounterRe.MatchString(tail) ||
		gerundTimerRe.MatchString(tail) ||
		brailleSpinnerRe.MatchString(tail) ||
		spinnerStatusRe.MatchString(tail)
}

// hasWaitingCue reports whether the pane shows an input prompt at rest. It scans
// every cleaned line of the last screen (not just the trailing block) so a caret
// still counts even when a hint line like "? for shortcuts" renders below the
// box. A "❯" caret is trusted alone; a ">" caret only when a box frame is also
// present. Parse is reused as a final corroborator: any answerable question at
// the prompt is, by definition, the agent waiting.
func hasWaitingCue(paneText, screen string) bool {
	boxed := boxBorderRe.MatchString(screen)
	for _, ln := range strings.Split(screen, "\n") {
		ln = cleanLine(ln)
		if ln == "" {
			continue
		}
		if claudeCaretRe.MatchString(ln) {
			return true
		}
		if boxed && plainCaretRe.MatchString(ln) {
			return true
		}
	}
	if _, ok := Parse(paneText); ok {
		return true
	}
	return false
}

// lastLines returns the trailing n lines of s (all of s when it has fewer). It
// is how Classify pins every cue to the last rendered screen: the status line
// and the input box always sit at the bottom, and ignoring older scrollback
// avoids matching a spinner or prompt that has since scrolled away.
func lastLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
