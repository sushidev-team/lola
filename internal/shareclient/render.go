package shareclient

import (
	"strconv"
	"strings"

	"github.com/sushidev-team/lola/internal/protocol"
)

// Render turns a resync into the bytes that paint it on an xterm of the same
// size: clear, each row, then the cursor. Rows carry their own SGR; rows are
// reset at their end so a colour never leaks into the next line. Trailing blank
// rows were trimmed by the host, so the rest of the screen stays cleared.
func Render(p protocol.ResyncPayload) []byte {
	var b strings.Builder
	// Select the buffer the host is on first, so the clear and the repaint land in it:
	// the alternate screen for an agent that runs on it, the normal one otherwise (a
	// viewer left on the alternate buffer by an earlier resync would send shell output
	// to the wrong one).
	if p.AltScreen {
		b.WriteString("\x1b[?1049h")
	} else {
		b.WriteString("\x1b[?1049l")
	}
	b.WriteString("\x1b[?25l\x1b[0m\x1b[2J\x1b[H")
	for i, ln := range p.Lines {
		if i > 0 {
			b.WriteString("\r\n")
		}
		b.WriteString(ln)
		b.WriteString("\x1b[0m")
	}
	b.WriteString("\x1b[")
	b.WriteString(strconv.Itoa(max(p.CursorY, 0) + 1))
	b.WriteByte(';')
	b.WriteString(strconv.Itoa(max(p.CursorX, 0) + 1))
	b.WriteByte('H')
	if !p.CursorHidden {
		b.WriteString("\x1b[?25h")
	}
	return []byte(b.String())
}
