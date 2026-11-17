package attention

import (
	"strconv"
	"strings"
	"unicode"

	"github.com/sushidev-team/lola/internal/agent"
)

// EmptyComposer is stricter than ActivityWaiting: an idle agent may still have
// a human's draft in its editor. Only a complete, visibly empty input frame is
// evidence automation may use it. Unknown layouts, attachments, and partially
// captured/multiline drafts fail closed. Never extract a draft from these
// rendered cells to clear and restore it: cells are not editor state.
//
// One kind of text on the caret line still counts as empty: Claude Code's
// PROMPT SUGGESTION, the ghost text it shows after most turns ("❯ check CI
// and fix if it fails"). It is drawn entirely in SGR 2 (dim), while anything a
// human typed is drawn at normal intensity, so a caret line whose every
// visible cell is dim holds no draft (suggestionOnly). Typing replaces a
// suggestion rather than accepting it (Tab accepts), so lola's literal text
// lands on an empty editor. Failing closed on suggestions used to park every
// conflict fix and review hand-off behind ghost text nobody wrote.
func EmptyComposer(pane string, kind agent.Kind) bool {
	if Classify(pane, kind) != ActivityWaiting {
		return false
	}
	// Strip per line so the raw line (SGR intact, for suggestionOnly) and the
	// plain one share an index. stripANSI never removes a newline.
	raw := strings.Split(lastLines(pane, maxScreenLines), "\n")
	lines := make([]string, len(raw))
	for i, r := range raw {
		lines[i] = stripANSI(r)
	}
	// Use the LAST caret, so a historical empty prompt cannot authorize a send
	// into the current filled one. Both surrounding rules must be visible.
	for i := len(lines) - 1; i >= 0; i-- {
		line := composerLine(lines[i])
		caret := plainCaretRe.MatchString(line) || (claudeCues(kind) && claudeCaretRe.MatchString(line))
		if !caret {
			continue
		}
		if line != ">" && line != "❯" && !(claudeCues(kind) && suggestionOnly(raw[i])) {
			return false
		}
		return emptyToRule(lines, i-1, -1) && emptyToRule(lines, i+1, 1)
	}
	return false
}

func emptyToRule(lines []string, start, step int) bool {
	for i := start; i >= 0 && i < len(lines); i += step {
		line := strings.TrimSpace(lines[i])
		if composerRule(line) {
			if step > 0 {
				// A historical empty frame above unrelated output is not the
				// current composer. Only known editor footers may follow it.
				for _, footer := range lines[i+1:] {
					footer = strings.TrimSpace(footer)
					if footer != "" && footer != "? for shortcuts" && !strings.HasPrefix(footer, "⏵⏵ ") {
						return false
					}
				}
			}
			return true
		}
		// Remove only the frame's vertical edges, not arbitrary punctuation in
		// a draft (cleanLine intentionally accepts more for status heuristics).
		if composerLine(line) != "" {
			return false
		}
	}
	return false
}

func composerLine(line string) string {
	line = strings.TrimSpace(line)
	line = strings.TrimPrefix(strings.TrimPrefix(line, "│"), "║")
	line = strings.TrimSuffix(strings.TrimSuffix(line, "│"), "║")
	return strings.TrimSpace(line)
}

func composerRule(line string) bool {
	count := 0
	for _, r := range line {
		switch r {
		case '─', '━', '═':
			count++
		case '╭', '╮', '╰', '╯', '┌', '┐', '└', '┘', '╔', '╗', '╚', '╝':
		default:
			return false
		}
	}
	return count >= 3
}

// suggestionOnly reports whether a raw (SGR-intact) caret line holds nothing
// but dim text after its "❯"/">" caret: Claude Code's prompt suggestion. Every
// visible, non-space cell after the caret must be rendered while SGR 2 is in
// effect, and at least one must exist. Any normal-intensity character (a
// human's draft, or a draft typed over a suggestion) fails it. So does a
// caret line that does not start with the caret.
func suggestionOnly(raw string) bool {
	dim, caretSeen, sawDim := false, false, false
	for len(raw) > 0 {
		if loc := ansiEscapeRe.FindStringIndex(raw); loc != nil && loc[0] == 0 {
			seq := raw[:loc[1]]
			raw = raw[loc[1]:]
			if strings.HasPrefix(seq, "\x1b[") && strings.HasSuffix(seq, "m") {
				dim = applySGR(seq[2:len(seq)-1], dim)
			}
			continue
		}
		next := len(raw)
		if loc := ansiEscapeRe.FindStringIndex(raw); loc != nil {
			next = loc[0]
		}
		for _, r := range raw[:next] {
			if unicode.IsSpace(r) || unicode.Is(unicode.Zs, r) || r < 0x20 {
				continue
			}
			if !caretSeen {
				if r != '❯' && r != '>' {
					return false
				}
				caretSeen = true
				continue
			}
			if !dim {
				return false
			}
			sawDim = true
		}
		raw = raw[next:]
	}
	return caretSeen && sawDim
}

// applySGR folds one SGR parameter list into the dim state: 2 sets it, 0 / an
// empty list / 22 clear it. Extended colours (38/48/58 ;5;n or ;2;r;g;b) are
// skipped whole, so the "2" in a truecolour selector is never read as dim.
func applySGR(params string, dim bool) bool {
	if params == "" {
		return false
	}
	ps := strings.Split(strings.ReplaceAll(params, ":", ";"), ";")
	for i := 0; i < len(ps); i++ {
		n, err := strconv.Atoi(ps[i])
		if ps[i] == "" {
			n, err = 0, nil
		}
		if err != nil {
			continue
		}
		switch n {
		case 0, 22:
			dim = false
		case 2:
			dim = true
		case 38, 48, 58:
			if i+1 < len(ps) {
				switch ps[i+1] {
				case "5":
					i += 2
				case "2":
					i += 4
				}
			}
		}
	}
	return dim
}
