// Cockpit layout primitives (TUI redesign): hand-rolled bordered panels and
// the horizontal/vertical joiners that compose them into btop/lazygit-style
// regions. Deliberately NOT lipgloss.Border — we keep the same ANSI-clip
// discipline the live pane preview relies on (truncateANSI / padTo) and full
// control over exact width AND height. bubbletea's alt-screen repaint counts
// rendered lines, so every panel here is clipped/padded to an exact box and
// never wraps: a wrapped row would desync the repaint and smear the frame (the
// same hazard previewLine guards for captured pane rows).
package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

var (
	boxBorder   = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	boxBorderHi = lipgloss.NewStyle().Foreground(lipgloss.Color("39")) // cyan focus
	boxTitle    = lipgloss.NewStyle().Bold(true)
	boxTitleHi  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("39"))
)

// box renders a titled, rounded-border panel of exact OUTER width w and OUTER
// height h (both including the border). body lines are each clipped to the
// inner width; the stack is clipped or blank-padded to the inner height so the
// returned slice is always exactly h lines, every line exactly w columns.
// focused draws the cyan border + title. A too-small w/h is floored so the box
// is always well-formed rather than panicking.
func box(title string, body []string, w, h int, focused bool) []string {
	if w < 4 {
		w = 4
	}
	if h < 2 {
		h = 2
	}
	innerW, innerH := w-2, h-2
	bs, ts := boxBorder, boxTitle
	if focused {
		bs, ts = boxBorderHi, boxTitleHi
	}

	out := make([]string, 0, h)
	out = append(out, boxTop(title, innerW, bs, ts))
	left, right := bs.Render("│"), bs.Render("│")
	for i := 0; i < innerH; i++ {
		content := ""
		if i < len(body) {
			content = body[i]
		}
		out = append(out, left+padTo(content, innerW)+right)
	}
	out = append(out, bs.Render("└"+strings.Repeat("─", innerW)+"┘"))
	return out
}

// boxTop builds the top border with the title cut into the line, lazygit-style:
//
//	┌─ Title ───────────┐
//
// The span between the corners is exactly innerW columns: "─ " (2) + title +
// " " (1) + fill dashes. When the title cannot fit it is dropped for a plain
// rule so the border never overflows its width.
func boxTop(title string, innerW int, bs, ts lipgloss.Style) string {
	tw := lipgloss.Width(title)
	if title == "" || tw+3 > innerW {
		return bs.Render("┌" + strings.Repeat("─", innerW) + "┐")
	}
	fill := innerW - tw - 3
	return bs.Render("┌─ ") + ts.Render(title) + bs.Render(" "+strings.Repeat("─", fill)+"┐")
}

// joinCols places rendered columns side by side with a gap of spaces between
// them. Each column is a slice of lines already padded to a fixed width (box
// does this); shorter columns are blank-filled to the tallest so the rows stay
// aligned. The result height is the tallest column.
func joinCols(gap int, cols ...[]string) []string {
	h := 0
	widths := make([]int, len(cols))
	for i, c := range cols {
		if len(c) > h {
			h = len(c)
		}
		if len(c) > 0 {
			widths[i] = lipgloss.Width(c[0])
		}
	}
	sep := strings.Repeat(" ", gap)
	out := make([]string, h)
	for r := 0; r < h; r++ {
		parts := make([]string, len(cols))
		for i, c := range cols {
			if r < len(c) {
				parts[i] = c[r]
			} else {
				parts[i] = strings.Repeat(" ", widths[i])
			}
		}
		out[r] = strings.Join(parts, sep)
	}
	return out
}

// stackRows concatenates rendered blocks vertically (top to bottom). It is a
// thin readability wrapper over append so callers read as a layout.
func stackRows(blocks ...[]string) []string {
	var out []string
	for _, b := range blocks {
		out = append(out, b...)
	}
	return out
}

// fitHeight forces a block to exactly h lines: extra lines are dropped, missing
// lines are appended as blanks of the block's width. Used to hold a composed
// region to its height budget so the overall frame line count is deterministic.
func fitHeight(lines []string, h int) []string {
	if len(lines) == h {
		return lines
	}
	if len(lines) > h {
		return lines[:h]
	}
	w := 0
	if len(lines) > 0 {
		w = lipgloss.Width(lines[0])
	}
	pad := strings.Repeat(" ", w)
	for len(lines) < h {
		lines = append(lines, pad)
	}
	return lines
}

// highlightRow paints a full-width selection background (256-color `bg`) behind
// an already-styled row and pads it to w columns. Inner style resets (from
// status pills / colored cells) would punch a hole in the background, so the bg
// SGR is re-applied after every reset. The row is ANSI-clipped to w first so a
// selected wide row can't wrap and desync the repaint.
func highlightRow(s string, w int, bg string) string {
	if w > 0 && lipgloss.Width(s) > w {
		s = truncateANSI(s, w)
	}
	sgr := "\x1b[48;5;" + bg + "m"
	out := sgr + strings.ReplaceAll(s, "\x1b[0m", "\x1b[0m"+sgr)
	if w > 0 {
		if pad := w - lipgloss.Width(s); pad > 0 {
			out += strings.Repeat(" ", pad)
		}
	}
	return out + "\x1b[0m"
}

// placeModal centers a modal box (its own bordered block) over a dimmed
// background frame, returning the composited lines. The background lines are
// faint-rendered; the modal overwrites the center rows in place. Both inputs
// must already be width/height-exact (box + fitHeight); the modal must be no
// larger than the background.
func placeModal(bg []string, modal []string, width int) []string {
	out := make([]string, len(bg))
	// Dim every background row once; the modal rows overwrite their slots below.
	for i, ln := range bg {
		out[i] = faintText.Render(stripANSI(ln))
	}
	mh := len(modal)
	if mh == 0 || mh > len(bg) {
		return out
	}
	mw := lipgloss.Width(modal[0])
	top := (len(bg) - mh) / 2
	left := (width - mw) / 2
	if left < 0 {
		left = 0
	}
	pad := strings.Repeat(" ", left)
	for i, mln := range modal {
		out[top+i] = pad + mln
	}
	return out
}

// stripANSI removes SGR/OSC escape sequences from s, leaving the visible runes.
// Used to flatten a background frame before dimming it so the modal backdrop
// reads as one uniform faint layer rather than a clash of the panels' own
// colors. Reuses truncateANSI's escape-skipping logic with an unbounded width.
func stripANSI(s string) string {
	var b strings.Builder
	rs := []rune(s)
	for i := 0; i < len(rs); i++ {
		if rs[i] == 0x1b {
			j := i + 1
			if j < len(rs) {
				switch rs[j] {
				case '[':
					j++
					for j < len(rs) && (rs[j] < 0x40 || rs[j] > 0x7e) {
						j++
					}
				case ']':
					j++
					for j < len(rs) && rs[j] != 0x07 && rs[j] != 0x1b {
						j++
					}
					if j+1 < len(rs) && rs[j] == 0x1b && rs[j+1] == '\\' {
						j++
					}
				}
			}
			if j >= len(rs) {
				j = len(rs) - 1
			}
			i = j
			continue
		}
		b.WriteRune(rs[i])
	}
	return b.String()
}
