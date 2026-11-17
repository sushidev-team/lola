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
	b.WriteString("\x1b[?25l\x1b[0m\x1b[2J\x1b[H")
	if p.AltScreen {
		// The host's agent runs on the alternate screen; stay on it so output
		// that follows addresses the same grid.
		b.WriteString("\x1b[?1049h\x1b[2J\x1b[H")
	}
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
