package attention

import (
	"regexp"
	"strings"
)

// Codex's composer, as measured on codex-cli 0.167.2 (testdata/codex holds the panes).
//
// It draws no box. The composer is the last row that starts with "›", two rows above a
// footer of at most two lines, and the same caret marks three other things: every past
// message in the transcript ("› fix the build"), the focused row of a list ("› 1. Trust
// and continue"), and nothing at all in shell mode, where the caret is "!". So a "›" on
// screen proves nothing by itself, and each of those is told apart below.
//
// Empty is read at the cursor, like Claude Code's: the cursor is shown, in the column
// after the caret, on that row, and the row holds only the dim placeholder ("Ask Codex
// to do anything"). Typing replaces the placeholder and moves the cursor, spaces
// included, and every screen that is not the composer hides the cursor.

var (
	// codexOptionRe is the focused row of a list: the update, trust and question screens.
	codexOptionRe = regexp.MustCompile(`^›\s+\d{1,2}\.\s`)
	// codexScreenHintRe is a footer that names keys for a screen, not for the composer:
	// a list ("enter continue · esc back"), the update prompt ("esc skip") and the
	// transcript browser that two Escs open.
	codexScreenHintRe = regexp.MustCompile(`(?i)\besc (?:back|skip)\b|\benter continue\b|Browsing transcript`)
)

// codexFooterRows is how many lines codex draws under its composer: the model and
// directory, then the key hints.
const codexFooterRows = 2

// codexRows is a capture as plain rows, right-trimmed, with the blank ones at the bottom
// gone. A row keeps its index, so a cursor's Y still names it.
func codexRows(text string) []string {
	rows := strings.Split(stripANSI(text), "\n")
	for i, r := range rows {
		rows[i] = strings.TrimRight(r, " \t\r")
	}
	for len(rows) > 0 && rows[len(rows)-1] == "" {
		rows = rows[:len(rows)-1]
	}
	return rows
}

// codexComposer finds the composer's row, or -1 when the screen is not showing one that
// can be told from everything else a "›" starts.
func codexComposer(rows []string) int {
	at := -1
	for i := len(rows) - 1; i >= 0; i-- {
		if strings.HasPrefix(rows[i], "›") {
			at = i
			break
		}
	}
	if at < 0 || codexOptionRe.MatchString(rows[at]) {
		return -1
	}
	// A draft's further lines follow its first, then one blank row, then the footer. A
	// past message is followed by the rest of the conversation instead.
	rest := at + 1
	for rest < len(rows) && rows[rest] != "" {
		rest++
	}
	footer := 0
	for _, r := range rows[rest:] {
		if r == "" {
			continue
		}
		if footer++; footer > codexFooterRows || codexScreenHintRe.MatchString(r) {
			return -1
		}
	}
	if footer == 0 {
		// No blank row was found, so what followed the caret was the footer itself.
		for _, r := range rows[at+1:] {
			if codexScreenHintRe.MatchString(r) {
				return -1
			}
		}
		if len(rows)-at-1 > codexFooterRows {
			return -1
		}
	}
	return at
}

// codexWaiting says a codex pane shows its composer. Classify asks it after the live
// working cue, so a turn in flight never reaches here.
func codexWaiting(screen string) bool {
	return codexComposer(codexRows(screen)) >= 0
}

// codexComposerEmptyAt is the cursor's half of the proof: see the note at the top.
func codexComposerEmptyAt(visible string, c Cursor) bool {
	if !c.Visible || c.X != composerCaretX {
		return false
	}
	raw := strings.Split(visible, "\n")
	rows := codexRows(visible)
	if c.Y < 0 || c.Y >= len(rows) || codexComposer(rows) != c.Y {
		return false
	}
	if rows[c.Y] != "›" && !suggestionOnly(raw[c.Y]) {
		return false
	}
	return c.Y+1 >= len(rows) || rows[c.Y+1] == ""
}
