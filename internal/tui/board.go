package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/sushidev-team/lola/internal/protocol"
)

// The agent's self-report (`lola report …`, SessionInfo.Board), rendered three
// ways: a compact chip for the list's PLAN column and the board's cards, and a
// full block in the detail card. It is the agent's CLAIM about itself, so it
// always rides BESIDE the axes, never instead of them, and it fades once the
// report is old — agents forget to report, and a confident stale "testing" is
// worse than none.

// boardStaleAfter is when a report stops reading as current.
const boardStaleAfter = 15 * time.Minute

// boardStale reports whether the board's last report is old enough to fade.
func boardStale(b *protocol.BoardInfo) bool {
	return b.UpdatedAt.IsZero() || time.Since(b.UpdatedAt) > boardStaleAfter
}

// boardMeter draws a cells-wide progress bar from a 0–100 percentage.
func boardMeter(pct, cells int) string {
	pct = max(0, min(100, pct))
	full := (pct*cells + 50) / 100
	return strings.Repeat("▰", full) + strings.Repeat("▱", cells-full)
}

// boardChip is the one-cell summary of a board: the blocker when there is one
// (it is the only part a human must act on), else the meter plus done/total or
// the percentage. "" when the agent has reported nothing worth a chip.
func boardChip(si protocol.SessionInfo) string {
	b := si.Board
	if b == nil {
		return ""
	}
	if b.Blocked != "" {
		return warnText.Render("⏸ blocked")
	}
	if !b.HasProgress {
		if b.Phase != "" {
			return faintText.Render(b.Phase)
		}
		return ""
	}
	tail := fmt.Sprintf(" %d%%", b.Percent)
	if b.ProgressDerived {
		tail = fmt.Sprintf(" %d/%d", b.Done, b.Total)
	}
	style := goodText
	if boardStale(b) {
		style = faintText
	}
	return style.Render(boardMeter(b.Percent, 5)) + faintText.Render(tail)
}

// boardLines is the detail card's block: a header (phase, count, how old the
// report is), the bar, the plan, the blocker, the note and the checks. w bounds
// every line so nothing wraps (a wrapped row smears bubbletea's repaint).
func boardLines(si protocol.SessionInfo, w int) []string {
	b := si.Board
	if b == nil {
		return nil
	}
	w = max(w, 20)
	fade := boardStale(b)
	text := func(s string) string {
		if fade {
			return faintText.Render(s)
		}
		return s
	}
	var head []string
	if b.Phase != "" {
		head = append(head, b.Phase)
	}
	if b.Total > 0 {
		head = append(head, fmt.Sprintf("%d/%d done", b.Done, b.Total))
	}
	if b.UpdatedAgo != "" {
		head = append(head, "reported "+b.UpdatedAgo+" ago")
	}
	out := []string{"plan:     " + text(truncPlain(strings.Join(head, " · "), w-10))}
	if b.HasProgress {
		line := boardMeter(b.Percent, 12) + fmt.Sprintf(" %3d%%", b.Percent)
		if b.ProgressLabel != "" {
			line += " " + b.ProgressLabel
		}
		out = append(out, "  "+text(truncPlain(line, w-2)))
	}
	if b.Blocked != "" {
		out = append(out, warnText.Render(truncPlain("  ⏸ blocked: "+b.Blocked, w)))
	}
	for _, t := range b.Todos {
		var glyph string
		switch t.State {
		case "done":
			glyph = goodText.Render("✓")
		case "active":
			glyph = boxTitleHi.Render("▸")
		default:
			glyph = faintText.Render("·")
		}
		body := truncPlain(t.Text, w-4)
		if t.State == "done" {
			body = faintText.Render(body)
		} else {
			body = text(body)
		}
		out = append(out, "  "+glyph+" "+body)
	}
	if b.Note != "" {
		out = append(out, faintText.Render(truncPlain("  note: "+b.Note, w)))
	}
	if len(b.Checks) > 0 {
		parts := make([]string, 0, len(b.Checks))
		for _, c := range b.Checks {
			p := c.Name
			if c.Summary != "" {
				p += " " + c.Summary
			}
			switch c.State {
			case "pass":
				parts = append(parts, goodText.Render("✓ "+p))
			case "fail":
				parts = append(parts, badText.Render("✗ "+p))
			default:
				parts = append(parts, faintText.Render("… "+p))
			}
		}
		out = append(out, "  checks: "+strings.Join(parts, "  "))
	}
	return out
}
