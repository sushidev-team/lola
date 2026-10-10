package tui

import (
	"fmt"
	"strings"

	"github.com/sushidev-team/lola/internal/protocol"
)

// Usage rendering (internal/usage on the daemon side). The figure shown is
// TOKENS: a subscription user pays nothing per token, so a dollar amount would
// read as a bill nobody is sent. The list-price estimate survives only as a
// "~$" aside in the detail line. How HEAVY a session is comes from the daemon's
// ranking against the user's own finished sessions, drawn as a 4-step glyph,
// plus a flame while it burns tokens faster than past sessions ever did.

// levelGlyphs are the size glyph's four steps (usage.LevelLight … LevelTop).
var levelGlyphs = [4]string{"▁", "▃", "▅", "█"}

const flame = "🔥"

// fmtUSD renders a dollar figure compactly: cents below $100, whole dollars above.
func fmtUSD(v float64) string {
	if v >= 100 {
		return fmt.Sprintf("$%.0f", v)
	}
	return fmt.Sprintf("$%.2f", v)
}

// fmtTokens renders a token count as 950, 12.3k, 4.5M.
func fmtTokens(n int64) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1e6)
	case n >= 1_000:
		return fmt.Sprintf("%.1fk", float64(n)/1e3)
	default:
		return fmt.Sprintf("%d", n)
	}
}

// levelGlyph is u's size glyph, orange for the top step.
func levelGlyph(u *protocol.UsageInfo) string {
	g := levelGlyphs[max(0, min(u.Level, len(levelGlyphs)-1))]
	if u.Level >= len(levelGlyphs)-1 {
		return statusOrange.Render(g)
	}
	return g
}

// costCell is the sessions table's TOKENS cell: "46.7M ▅" plus the flame while
// burning, "-" when nothing is known (no transcript yet, or an agent lola
// cannot read).
func costCell(si protocol.SessionInfo) string {
	u := si.Usage
	if u == nil {
		return "-"
	}
	cell := fmtTokens(u.Tokens) + " " + levelGlyph(u)
	if u.Burning {
		cell += flame
	}
	return cell
}

// anyUsage reports whether a TOKENS column has anything to show, so a fleet
// running only codex/opencode keeps the columns it had.
func anyUsage(list []protocol.SessionInfo) bool {
	for _, si := range list {
		if si.Usage != nil {
			return true
		}
	}
	return false
}

// rankText says in words what the glyph shows.
func rankText(u *protocol.UsageInfo) string {
	if u.Of == 0 {
		return [4]string{"light", "moderate", "heavy", "very heavy"}[max(0, min(u.Level, 3))] + " (too little history to compare yet)"
	}
	return fmt.Sprintf("heavier than %.0f%% of your last %d sessions", u.Percentile, u.Of)
}

// usageDetailLine is the detail panel's usage line, "" when nothing is known.
func usageDetailLine(si protocol.SessionInfo) string {
	u := si.Usage
	if u == nil {
		return ""
	}
	line := fmt.Sprintf("tokens:   %s %s (%s today) · %s · ~%s at list price",
		fmtTokens(u.Tokens), levelGlyph(u), fmtTokens(u.TodayTokens), rankText(u), fmtUSD(u.TotalUSD))
	if u.Burning {
		line += " · " + flame + " " + statusOrange.Render(fmt.Sprintf("burning %s tokens/h", fmtTokens(u.TokensPerHour)))
	}
	return line
}

// budgetPercent is used/budget as a whole percentage; -1 without a budget.
func budgetPercent(used, budget int64) int {
	if budget <= 0 {
		return -1
	}
	return int(100 * used / budget)
}

// spendVital is the vitals-bar segment for today's usage and the load hold:
// "today 46.7M", plus "· 38% of budget" when a [budget] limit is set — orange
// from 80%, red once reached — and a red "load busy" while [load] holds
// dispatch. The budget is a PERCENTAGE because it counts weighted tokens, a
// different number from the raw count beside it. "" on an older daemon.
func spendVital(st *protocol.StatusData) string {
	if st == nil || st.Usage == nil {
		return ""
	}
	u := st.Usage
	text := "today " + fmtTokens(u.Tokens)
	if pct := budgetPercent(u.Weighted, u.BudgetTokens); pct >= 0 {
		b := fmt.Sprintf("· %d%% of budget", pct)
		switch {
		case pct >= 100:
			b = badText.Render(b + " (dispatch held)")
		case pct >= 80:
			b = statusOrange.Render(b)
		}
		text += " " + b
	}
	if u.Load != nil && u.Load.Busy != "" {
		text += " " + badText.Render("load busy")
	}
	return text
}

// spendSummary is `lola status`'s usage block: today's tokens against the
// global limit, each project with usage or a limit, and the load sample.
func spendSummary(u *protocol.UsageStatus) string {
	if u == nil {
		return ""
	}
	var b strings.Builder
	budget := "no limit"
	if pct := budgetPercent(u.Weighted, u.BudgetTokens); pct >= 0 {
		budget = fmt.Sprintf("%d%% of the %s limit", pct, fmtTokens(u.BudgetTokens))
	}
	fmt.Fprintf(&b, "tokens %s: %s (%s weighted, ~%s at list price) — %s\n",
		u.Day, fmtTokens(u.Tokens), fmtTokens(u.Weighted), fmtUSD(u.TodayUSD), budget)
	for _, p := range u.Projects {
		line := fmt.Sprintf("  %s: %s (%s weighted)", p.Name, fmtTokens(p.Tokens), fmtTokens(p.Weighted))
		if pct := budgetPercent(p.Weighted, p.BudgetTokens); pct >= 0 {
			line += fmt.Sprintf(" — %d%% of %s", pct, fmtTokens(p.BudgetTokens))
		}
		b.WriteString(line + "\n")
	}
	if l := u.Load; l != nil {
		load := "unknown"
		if l.Load1 >= 0 {
			load = fmt.Sprintf("%.2f on %d CPUs", l.Load1, l.CPUs)
		}
		mem := "unknown"
		if l.FreeMemPercent >= 0 {
			mem = fmt.Sprintf("%.0f%% free", l.FreeMemPercent)
		}
		fmt.Fprintf(&b, "load: %s · memory %s", load, mem)
		if l.Busy != "" {
			b.WriteString(" — " + badText.Render("dispatch held"))
		}
		b.WriteString("\n")
	}
	return b.String()
}
