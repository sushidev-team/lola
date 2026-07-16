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

	"github.com/sushidev-team/lola/internal/agent"
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

	// codexWorkingRe — Codex's live status line: the verb "Working" immediately
	// followed by an elapsed timer ("47s", "4m 07s", or the parenthesised
	// "(1s • esc to interrupt)"). Codex prints this while a turn streams, the way
	// claude-code prints its spinner+timer, so it is the codex analogue of the
	// claude working cues and is gated to k==Codex. The "esc to interrupt" variant
	// is already caught by the SHARED escInterruptRe above; this cue adds the
	// bare-elapsed forms. Requiring the timer to sit right after "Working" (only
	// whitespace / an opening paren between) keeps incidental prose like
	// "Working on it, took 5s" from matching. Fragility: the verb wording and the
	// timer shape are codex specifics; a resting composer (a waiting cue) still
	// wins because hasWorkingCue is consulted only once no resting prompt is found.
	codexWorkingRe = regexp.MustCompile(`(?i)\bWorking\b\s*\(?\s*(?:\d+h\s*)?(?:\d+m\s*)?\d+s\b`)

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

	// shellPromptRe — a "$ " shell prompt at line start. Its presence on screen
	// means a bare ">" caret is most likely a subprocess/shell prompt rather than
	// claude-code's own input line, so the box-free empty-caret trust below is
	// withheld (keeps the lone-shell-prompt pane classified Unknown).
	shellPromptRe = regexp.MustCompile(`(?m)^\s*\$\s`)

	// openCodeMetaRe — opencode's post-turn metadata line, which begins with a
	// "▣" glyph and summarises the finished turn (tokens, cost). It renders only
	// AFTER opencode has yielded, so a line that STARTS with it corroborates a
	// waiting pane. Gated to k==OpenCode and used only on the waiting side, where
	// a false positive is cheap (it just surfaces needs_input a cycle early). The
	// leading anchor keeps a "▣" embedded mid-line in ordinary output from tripping
	// it.
	openCodeMetaRe = regexp.MustCompile(`(?m)^\s*▣`)
)

// claudeCues reports whether kind k uses claude-code's caret and question-parse
// heuristics. Claude does; codex and opencode do not (their carets/questions are
// covered by the SHARED cues instead). Any unknown or legacy kind ("" resolves
// to claude) does, so pre-existing sessions keep today's claude behavior.
func claudeCues(k agent.Kind) bool {
	switch k {
	case agent.Codex, agent.OpenCode:
		return false
	default:
		return true
	}
}

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
//
// k selects which agent's cue set applies. SHARED cues (esc-to-interrupt, the
// braille spinner, the bordered input box) fire for every kind; claude-code's
// own cues (the "❯" caret, arrow token meter, gerund timer, circle/star spinner,
// and the question-parse corroborator) fire only for k==Claude; codex adds a
// "Working"+elapsed-timer cue; opencode leans on the shared set plus a "▣"
// post-turn waiting corroborator. k==Claude (and any legacy/unknown kind, which
// resolves to claude) behaves byte-identically to before this parameter existed.
func Classify(paneText string, k agent.Kind) Activity {
	if len(paneText) > maxInput {
		paneText = paneText[len(paneText)-maxInput:]
	}
	clean := stripANSI(paneText)
	screen := lastLines(clean, maxScreenLines)
	tail := lastLines(clean, statusTailLines)

	// "esc to interrupt" is the ONE unambiguous LIVE cue and is SHARED by every
	// agent (claude, codex and opencode all print it while a turn streams). It
	// always wins.
	if escInterruptRe.MatchString(tail) {
		return ActivityWorking
	}
	// A resting input prompt (bordered box, a caret, or an answerable question)
	// means the agent has yielded. It beats the WEAKER working cues below, because
	// a COMPLETED status line can leave a frozen token counter / elapsed timer /
	// spinner frame on screen right next to the resting prompt — reading that as
	// live activity is exactly the sticky false-"working" bug. A genuinely
	// streaming turn shows "esc to interrupt" (handled above) and does not rest an
	// empty caret, so nothing live is lost here.
	if hasWaitingCue(paneText, screen, k) {
		return ActivityWaiting
	}
	// No resting prompt: the remaining cues (token counter, elapsed timer, spinner
	// frame, codex "Working" timer) are trusted as live activity.
	if hasWorkingCue(tail, k) {
		return ActivityWorking
	}
	return ActivityUnknown
}

// hasWorkingCue reports whether the (ANSI-stripped) live status tail shows any
// live activity indicator for agent kind k. Any single cue suffices; see each
// regexp for its cue and fragility. Callers MUST pass only the status tail (last
// statusTailLines), not the full screen — scanning deeper reintroduces the
// scrollback false positive this window exists to prevent.
//
// The SHARED cues (esc-to-interrupt, braille spinner) fire for every kind.
// Beyond them each kind adds its own: claude the token meter / gerund timer /
// circle-star spinner; codex the "Working"+elapsed-timer status line; opencode
// nothing (the shared set already covers its braille+esc status line). For
// k==Claude the union is exactly the historical cue set, so behavior is
// byte-identical; any legacy/unknown kind resolves to the claude branch.
func hasWorkingCue(tail string, k agent.Kind) bool {
	// SHARED across every agent.
	if escInterruptRe.MatchString(tail) || brailleSpinnerRe.MatchString(tail) {
		return true
	}
	switch k {
	case agent.Codex:
		return codexWorkingRe.MatchString(tail)
	case agent.OpenCode:
		return false
	default:
		// Claude (and legacy/unknown kinds).
		return tokenCounterRe.MatchString(tail) ||
			gerundTimerRe.MatchString(tail) ||
			spinnerStatusRe.MatchString(tail)
	}
}

// hasWaitingCue reports whether the pane shows an input prompt at rest for agent
// kind k. It scans every cleaned line of the last screen (not just the trailing
// block) so a caret still counts even when a hint line like "? for shortcuts"
// renders below the box.
//
// The SHARED cue is the bordered input box: a ">" caret trusted BECAUSE a box
// frame is on screen — every agent frames its resting composer this way. The
// claude-only cues (gated via claudeCues) are the "❯" caret trusted alone, the
// box-free bare caret, and the Parse question corroborator; codex and opencode
// do not render these, so extending them would only add false positives.
// OpenCode additionally treats its "▣" post-turn metadata line as a corroborator.
// For k==Claude the reachable set is exactly the historical one, so behavior is
// byte-identical.
func hasWaitingCue(paneText, screen string, k agent.Kind) bool {
	boxed := boxBorderRe.MatchString(screen)
	// A "$ " shell prompt anywhere on screen means a bare ">" is most likely a
	// subprocess/shell prompt, so the box-free caret trust is withheld (a lone
	// shell prompt with no box stays Unknown).
	shell := shellPromptRe.MatchString(screen)
	claude := claudeCues(k)
	for _, ln := range strings.Split(screen, "\n") {
		ln = cleanLine(ln)
		if ln == "" {
			continue
		}
		// SHARED: a ">" caret inside a rendered input box. The box frame is what
		// lets a bare ">" be trusted as a composer rather than a shell prompt or a
		// quoted line, and claude/codex/opencode all draw one when waiting.
		if boxed && plainCaretRe.MatchString(ln) {
			return true
		}
		if !claude {
			continue
		}
		// CLAUDE-ONLY carets below.
		if claudeCaretRe.MatchString(ln) {
			return true
		}
		// A bare, EMPTY caret ("> " / "❯") with NO box: claude-code resting at its
		// input prompt after a turn that scrolled its box off the capture (or that
		// renders a minimal prompt). promptIndicatorRe matches only an empty caret
		// line, so a markdown blockquote ("> quoted text") cannot trip it; the
		// shell guard covers the ambiguous "$"/">" shell case.
		if !shell && promptIndicatorRe.MatchString(ln) {
			return true
		}
	}
	// OPENCODE: a post-turn "▣" metadata line means the turn finished and the
	// agent is back at its composer. Waiting-side corroborator only.
	if k == agent.OpenCode && openCodeMetaRe.MatchString(screen) {
		return true
	}
	// CLAUDE-ONLY: any answerable question at the prompt is, by definition, the
	// agent waiting. Parse self-gates to claude (returns false for other kinds),
	// so this call is a no-op for codex/opencode and byte-identical for claude.
	if _, ok := Parse(paneText, k); ok {
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
