// Package attention turns a raw tmux pane capture into an answerable question.
//
// It exists for the "session is needs_input" case (P7): an agent has stopped
// and is provably waiting at its own input prompt, and a human wants to answer
// in place without attaching. The only signal available is the rendered pane
// text (ANSI and all), so this package is deliberately a HEURISTIC parser, not
// a protocol: it inspects the trailing block of the pane and GUESSES whether it
// is an enumerated menu, a yes/no gate, or a free-form question.
//
// Honesty about the limits (each heuristic documents its own below):
//
//   - It reads rendered text, so it can be fooled by output that merely LOOKS
//     like a prompt — a code listing that happens to enumerate "1. ", "2. ", a
//     log line ending in "?".
//   - It only ever looks at the trailing non-empty block, so a question that
//     has scrolled above later output is invisible to it (returns ok=false).
//   - Choice KEYS are guessed from the visible enumerator; the keypress the
//     agent's line editor actually accepts is the ground truth. For claude-code
//     numbered selects and (y/n) gates the two coincide, which is the point.
//   - Parse returns ok=false whenever nothing question-shaped is discernible.
//     Callers MUST treat false as "cannot answer inline" and never fabricate a
//     prompt or auto-send. The human always initiates the answer.
package attention

import (
	"regexp"
	"strings"
	"unicode"
)

// Question is the parsed, answerable prompt. Exactly one of Choices / FreeForm
// is meaningful: a non-empty Choices means the human picks a Key; FreeForm true
// means the human types a line. Prompt is the human-readable question text
// extracted from around the options; Raw is the cleaned trailing block kept for
// display so a caller can show the operator what we parsed it from.
type Question struct {
	Prompt   string
	Choices  []Choice
	FreeForm bool
	Raw      string
}

// Choice is one selectable option. Key is what gets sent to the agent (e.g.
// "1", "2", "y"); Label is the human-readable text shown next to it.
type Choice struct {
	Key   string
	Label string
}

const (
	// maxInput bounds how much pane text Parse will even look at. A capture is
	// normally a few KB, but a caller may hand us unbounded scrollback; we only
	// need the tail, so anything earlier is dropped.
	maxInput = 64 * 1024
	// maxBlockLines caps the trailing block so a border-free wall of text can't
	// make us scan a whole screen. Real prompts are a handful of lines.
	maxBlockLines = 64
)

// ansiEscapeRe matches the ANSI escape sequences (CSI and OSC) a terminal
// capture routinely emits. Kept identical to internal/daemon's copy on purpose:
// this is a leaf package and must not import daemon just to share one regexp.
var ansiEscapeRe = regexp.MustCompile("\x1b\\[[0-9;?:<>=]*[ -/]*[@-~]|\x1b\\][^\x07\x1b]*(?:\x07|\x1b\\\\)")

// cursorRe strips a leading selection cursor / bullet ("❯ 1. Yes" → "1. Yes").
// claude-code renders the highlighted option with a "❯"; "> " and common
// bullets are tolerated too so plain-terminal menus parse as well.
var cursorRe = regexp.MustCompile(`^[❯>➤→*•‣]+[ \t]*`)

// enumRe matches one enumerated menu item: a 1–3 digit number or single letter,
// then "." or ")", then whitespace, then a non-empty label. The mandatory space
// after the punctuation keeps "1.5" and "e.g." from matching.
var enumRe = regexp.MustCompile(`^(\d{1,3}|[A-Za-z])[.)][ \t]+(\S.*)$`)

// yesNoRe matches a bracketed yes/no marker: (y/n), [y/n], (yes/no), (Y/n)…
// Only bracketed forms are accepted so bare "and/or" / "read/write" in output
// cannot masquerade as a gate.
var yesNoRe = regexp.MustCompile(`(?i)[\[(]\s*y(?:es)?\s*/\s*n(?:o)?\s*[\])]`)

// promptIndicatorRe matches a bare input-prompt line (just ">" or "❯", possibly
// with a cursor "_"), the kind a free-form prompt leaves the caret on.
var promptIndicatorRe = regexp.MustCompile(`^[>❯][ \t]*_?[ \t]*$`)

// Parse strips ANSI from paneText, isolates the trailing non-empty block, and
// classifies it. It returns (Question, true) when a menu, yes/no gate, or
// free-form question is discernible, and (zero, false) otherwise.
func Parse(paneText string) (Question, bool) {
	if len(paneText) > maxInput {
		paneText = paneText[len(paneText)-maxInput:]
	}
	block := trailingBlock(stripANSI(paneText))
	if len(block) == 0 {
		return Question{}, false
	}
	raw := strings.Join(block, "\n")

	// (a) Enumerated menu — claude-code style select. Require >= 2 enumerated
	// lines so a lone "1. foo" in prose can't masquerade as a menu. Limit: two
	// or more enumerated lines in ordinary agent OUTPUT (a numbered list) would
	// still be misread as a menu; the trailing-block scope makes that rare but
	// not impossible.
	var choices []Choice
	firstItem := -1
	for i, ln := range block {
		m := enumRe.FindStringSubmatch(cursorRe.ReplaceAllString(ln, ""))
		if m == nil {
			continue
		}
		if firstItem < 0 {
			firstItem = i
		}
		choices = append(choices, Choice{Key: m[1], Label: strings.TrimSpace(m[2])})
	}
	if len(choices) >= 2 {
		return Question{Prompt: promptAbove(block, firstItem), Choices: choices, Raw: raw}, true
	}

	// (b) Yes/no gate. The marker may sit on the question line ("Overwrite? (y/n)")
	// or on its own; either way we strip the marker and fall back to the line
	// above for the prompt text.
	for i, ln := range block {
		if !yesNoRe.MatchString(ln) {
			continue
		}
		prompt := strings.TrimSpace(yesNoRe.ReplaceAllString(ln, ""))
		if prompt == "" {
			prompt = promptAbove(block, i)
		}
		return Question{
			Prompt:  prompt,
			Choices: []Choice{{Key: "y", Label: "Yes"}, {Key: "n", Label: "No"}},
			Raw:     raw,
		}, true
	}

	// (c) Free-form: a line ending in "?" is the strongest signal — take the
	// last such line as the prompt. Failing that, a trailing bare prompt
	// indicator (">") with a sentence-like line above it is treated as a
	// free-form ask. The sentence-like guard (looksLikePrompt) keeps a lone
	// shell "$ " / ">" from being reported as an answerable question.
	for i := len(block) - 1; i >= 0; i-- {
		if strings.HasSuffix(block[i], "?") {
			return Question{Prompt: block[i], FreeForm: true, Raw: raw}, true
		}
	}
	if promptIndicatorRe.MatchString(block[len(block)-1]) {
		if p := promptAbove(block, len(block)-1); looksLikePrompt(p) {
			return Question{Prompt: p, FreeForm: true, Raw: raw}, true
		}
	}
	return Question{}, false
}

// stripANSI removes ANSI escape sequences, then drops any remaining C0/C1
// control bytes (bare ESC, BEL, CR — CR is a submit vector and vanishes) while
// preserving TAB and LF, which carry layout the block scan relies on.
func stripANSI(s string) string {
	s = ansiEscapeRe.ReplaceAllString(s, "")
	return strings.Map(func(r rune) rune {
		if r == '\t' || r == '\n' {
			return r
		}
		if r < 0x20 || (r >= 0x7f && r <= 0x9f) {
			return -1
		}
		return r
	}, s)
}

// isFrame reports whether r is box-drawing or padding used to frame a prompt
// (claude-code renders its select inside a rounded box). A line made only of
// these is treated as blank so it delimits blocks instead of joining them.
func isFrame(r rune) bool {
	switch r {
	case '│', '|', '─', '━', '╭', '╮', '╰', '╯', '┌', '┐', '└', '┘',
		'├', '┤', '┬', '┴', '┼', '═', '║', '╔', '╗', '╚', '╝', ' ', '\t':
		return true
	}
	return false
}

// cleanLine trims trailing whitespace, collapses a pure box-border line to "",
// and strips a left/right vertical border plus its padding so "│ 1. Yes │"
// becomes "1. Yes". Leading indentation is intentionally dropped — none of the
// classifiers depend on it.
func cleanLine(s string) string {
	s = strings.TrimRight(s, " \t")
	if strings.TrimFunc(s, isFrame) == "" {
		return "" // blank line, or a pure box-border rule
	}
	s = strings.TrimLeft(s, "│| \t")
	s = strings.TrimRight(s, "│| \t")
	return s
}

// trailingBlock cleans every line, then returns the last contiguous run of
// non-empty (post-clean) lines — the trailing "paragraph". Trailing blanks and
// border rules are skipped first, and the run is capped at maxBlockLines.
func trailingBlock(s string) []string {
	lines := strings.Split(s, "\n")
	cleaned := make([]string, len(lines))
	for i, ln := range lines {
		cleaned[i] = cleanLine(ln)
	}
	end := len(cleaned)
	for end > 0 && cleaned[end-1] == "" {
		end--
	}
	start := end
	for start > 0 && cleaned[start-1] != "" {
		start--
	}
	block := cleaned[start:end]
	if len(block) > maxBlockLines {
		block = block[len(block)-maxBlockLines:]
	}
	return block
}

// promptAbove returns the nearest non-empty line above idx that is not itself an
// enumerated option (so a multi-choice menu doesn't pick an earlier option as
// its prompt). Returns "" when there is no such line.
func promptAbove(block []string, idx int) string {
	for i := idx - 1; i >= 0; i-- {
		if block[i] == "" {
			continue
		}
		if enumRe.MatchString(cursorRe.ReplaceAllString(block[i], "")) {
			continue
		}
		return block[i]
	}
	return ""
}

// looksLikePrompt is the guard for the bare-prompt-indicator free-form path: a
// real instruction has at least one space and at least one letter, which a lone
// shell prompt ("$", ">") does not.
func looksLikePrompt(s string) bool {
	if !strings.ContainsRune(s, ' ') {
		return false
	}
	return strings.IndexFunc(s, unicode.IsLetter) >= 0
}
