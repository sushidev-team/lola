package attention

import (
	"strings"

	"github.com/sushidev-team/lola/internal/agent"
)

// EmptyComposer is stricter than ActivityWaiting: an idle agent may still have
// a human's draft in its editor. Only a complete, visibly empty input frame is
// evidence automation may use it. Unknown layouts, suggestions, attachments,
// and partially captured/multiline drafts fail closed. Never extract a draft
// from these rendered cells to clear and restore it: cells are not editor state.
func EmptyComposer(pane string, kind agent.Kind) bool {
	if Classify(pane, kind) != ActivityWaiting {
		return false
	}
	lines := strings.Split(lastLines(stripANSI(pane), maxScreenLines), "\n")
	// Use the LAST caret, so a historical empty prompt cannot authorize a send
	// into the current filled one. Both surrounding rules must be visible.
	for i := len(lines) - 1; i >= 0; i-- {
		line := composerLine(lines[i])
		caret := plainCaretRe.MatchString(line) || (claudeCues(kind) && claudeCaretRe.MatchString(line))
		if !caret {
			continue
		}
		if line != ">" && line != "❯" {
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
