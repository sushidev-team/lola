package agent

import "strings"

// Caps says what lola has MEASURED for a kind of agent, and what is not measured is not
// offered. Claude's come from a spike on Claude Code 2.1.318. Codex's come from panes of
// codex-cli 0.167.2, checked in under internal/attention/testdata/codex. Opencode stays
// off until a real pane of it has been captured and checked in: with Send false its text
// still queues and flushes through the automation gate, and the session opens on its
// terminal.
//
//   - Feed: lola can read its log into a conversation (caps_feed.go).
//   - Send: a press-now send, on the cursor proof.
//   - Stop: Esc is known to end a turn and nothing else.
//   - Cards: its prompts can be read and answered from a card.
//   - Cursor: the composer proof can use the cursor.
//   - Paste: a long send may go as one bracketed paste with its Enter 300 ms later. A
//     sandboxed Claude Code keeps the paste too: docker -it was not measured, but the program
//     reading the bytes is the measured one, and typing over 1 KB at it is known to damage text.
//   - PasteOnly: every send goes as a paste, whatever its size. Codex reads fast keystrokes
//     as a paste burst and takes the Enter after them as a line break: 3.5 KB typed and
//     Enter 600 ms later left the whole message sitting in the composer as a draft. The same
//     text as one bracketed paste, and a 33 byte one, both submitted on an Enter 300 ms later.
//
// Codex has no Cards: with --approve-for-me its approvals go to its own reviewer, and no
// question card of it was captured. Its lists share the composer's caret, so they are told
// apart and never typed into (internal/attention/codex.go).
//
// Nor has the question of whether a leading space neutralizes "/" on claude been measured,
// which is why a widget fill beginning with "/" must be refused rather than spaced.
func Caps(k Kind) struct{ Feed, Send, Stop, Cards, Cursor, Paste, PasteOnly bool } {
	c := struct{ Feed, Send, Stop, Cards, Cursor, Paste, PasteOnly bool }{Feed: feedCap(k)}
	switch Parse(string(k)) {
	case Claude:
		c.Send, c.Stop, c.Cards, c.Cursor, c.Paste = true, true, true, true, true
	case Codex:
		c.Send, c.Stop, c.Cursor, c.Paste, c.PasteOnly = true, true, true, true, true
	}
	return c
}

// ComposerText is text as it must be put into k's composer so that Enter submits it.
//
// Codex opens a picker while the cursor rests on a word holding "@" (files and plugins)
// or "$" (skills), and Enter then inserts the picker's first row instead of submitting:
// "ignore @READ" became "ignore @Spreadsheets" and stayed in the composer. A space after
// the word closes the picker, measured with typed text and with a paste. The model sees
// one trailing space.
func ComposerText(k Kind, text string) string {
	if Parse(string(k)) != Codex {
		return text
	}
	last := text[strings.LastIndexAny(text, " \t\n")+1:]
	if strings.ContainsAny(last, "@$") {
		return text + " "
	}
	return text
}
