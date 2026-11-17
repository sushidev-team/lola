package agent

// Caps says what lola has MEASURED for a kind of agent, and what is not measured is not
// offered. Claude's come from a spike on Claude Code 2.1.318. Codex and opencode stay off
// until a real pane of each has been captured and checked in: with Send false their text
// still queues and flushes through the automation gate, and the session opens on its
// terminal.
//
//   - Feed: lola can read its log into a conversation (caps_feed.go).
//   - Send: a press-now send, on the cursor proof.
//   - Stop: Esc is known to end a turn and nothing else.
//   - Cards: its prompts can be read and answered from a card.
//   - Cursor: the composer proof can use the cursor.
//   - Paste: a long send may go as one bracketed paste with its Enter 300 ms later. That
//     timing was measured on Claude Code only, so another agent keeps plain send-keys. A
//     sandboxed Claude Code keeps the paste too: docker -it was not measured, but the program
//     reading the bytes is the measured one, and typing over 1 KB at it is known to damage text.
//
// The measurements that would turn codex and opencode on have not been made: a live pane
// of each at rest, mid-turn and with a draft for Send and Cards, Esc for Stop, and the paste
// transport. Nor has the question of whether a leading space neutralizes "/" on claude,
// which is why a widget fill beginning with "/" must be refused rather than spaced.
func Caps(k Kind) struct{ Feed, Send, Stop, Cards, Cursor, Paste bool } {
	c := struct{ Feed, Send, Stop, Cards, Cursor, Paste bool }{Feed: feedCap(k)}
	if Parse(string(k)) == Claude {
		c.Send, c.Stop, c.Cards, c.Cursor, c.Paste = true, true, true, true, true
	}
	return c
}
