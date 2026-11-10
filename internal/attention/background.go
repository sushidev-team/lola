package attention

import (
	"regexp"
	"strings"

	"github.com/sushidev-team/lola/internal/agent"
)

var backgroundRunningRe = regexp.MustCompile(`^\s*⎿\s+Running(?:…|\.\.\.)\s*\([0-9hms .]+\)\s*$`)

// readBackground recognizes the foreground Bash notice captured in fresh-5..7.
// It is an optional action while working, never a question or an idle proof.
// Require the live, empty composer and the final tool block: an old hint in the
// transcript, a draft, or a dialog must never offer a control key.
func readBackground(visible string, c Cursor, k agent.Kind) (Prompt, bool) {
	if k != agent.Claude || !MessageComposerEmptyAt(visible, c, k) {
		return Prompt{}, false
	}
	rows := promptRows(visible)
	hint := -1
	for i, row := range rows {
		if strings.TrimSpace(row) == "(ctrl+b to run in background)" {
			if hint >= 0 { // Ctrl+B can affect multiple tasks; don't name just one.
				return Prompt{}, false
			}
			hint = i
		}
	}
	if hint < 2 || hint >= c.Y-1 || !backgroundRunningRe.MatchString(rows[hint-1]) {
		return Prompt{}, false
	}
	// Between the tool and the composer there may only be its live status line.
	live := false
	for _, row := range rows[hint+1 : c.Y-1] {
		if strings.TrimSpace(row) == "" {
			continue
		}
		if live || !hasLiveWorkingCue(row, k) {
			return Prompt{}, false
		}
		live = true
	}
	if !live {
		return Prompt{}, false
	}
	start := hint - 2
	for start > 0 && strings.TrimSpace(rows[start-1]) != "" {
		start--
	}
	lines := trimBlank(rows[start : hint-1])
	if len(lines) == 0 || len(lines) > maxBlockLines {
		return Prompt{}, false
	}
	lines = append([]string(nil), lines...)
	// The activity glyph and elapsed clocks redraw. The command text pins the
	// action; the handler requires a fresh running notice again before pressing.
	lines[0] = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(lines[0]), "⏺"))
	return Prompt{
		Kind: PromptBackground, Hash: hashRows(append([]string{PromptBackground}, lines...)), Lines: lines,
		Choices: []Choice{{Key: "background", Label: "Move to background"}},
	}, true
}
