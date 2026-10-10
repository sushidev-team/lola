package tui

import (
	"fmt"
	"math"
	"strings"
	"time"

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
	kind := ""
	if u.Agent == "codex" {
		kind = "codex " // ranked against codex sessions only
	}
	return fmt.Sprintf("heavier than %.0f%% of your last %d %ssessions", u.Percentile, u.Of, kind)
}

// usageDetailLine is the detail panel's usage line, "" when nothing is known.
func usageDetailLine(si protocol.SessionInfo) string {
	u := si.Usage
	if u == nil {
		return ""
	}
	line := fmt.Sprintf("tokens:   %s %s (%s today) · %s",
		fmtTokens(u.Tokens), levelGlyph(u), fmtTokens(u.TodayTokens), rankText(u))
	if u.TotalUSD > 0 { // codex has no list price lola trusts
		line += " · ~" + fmtUSD(u.TotalUSD) + " at list price"
	}
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

// quotaPercent renders one limit window, orange from 80%, red from 95%.
func quotaPercent(w protocol.QuotaWindow) string {
	text := fmt.Sprintf("%s %.0f%%", w.Label, w.UsedPercent)
	switch {
	case w.UsedPercent >= 95:
		return badText.Render(text)
	case w.UsedPercent >= 80:
		return statusOrange.Render(text)
	}
	return text
}

// quotaBarCells is the width of a limit bar in the vitals bar.
const quotaBarCells = 5

// quotaBar is a limit window as a small progress bar: "5h ▰▰▱▱▱ 42%", the
// filled cells toned like quotaPercent.
func quotaBar(w protocol.QuotaWindow) string {
	filled := int(math.Round(math.Min(math.Max(w.UsedPercent, 0), 100) / 100 * quotaBarCells))
	if filled == 0 && w.UsedPercent > 0 {
		filled = 1 // any use shows: an empty bar reads as "nothing used"
	}
	on := strings.Repeat("▰", filled)
	switch {
	case w.UsedPercent >= 95:
		on = badText.Render(on)
	case w.UsedPercent >= 80:
		on = statusOrange.Render(on)
	}
	return fmt.Sprintf("%s %s%s %.0f%%", w.Label, on, faintText.Render(strings.Repeat("▱", quotaBarCells-filled)), w.UsedPercent)
}

// quotaVital is "claude 5h ▰▰▱▱▱ 42% 7d ▰▱▱▱▱ 18% · codex 7d ▰▱▱▱▱ 4%"; ""
// when no agent has reported its subscription limits.
func quotaVital(qs []protocol.QuotaInfo) string {
	parts := make([]string, 0, len(qs))
	for _, q := range qs {
		ws := make([]string, 0, len(q.Windows))
		for _, w := range q.Windows {
			ws = append(ws, quotaBar(w))
		}
		parts = append(parts, q.Agent+" "+strings.Join(ws, " "))
	}
	return strings.Join(parts, " · ")
}

// shortDuration renders a positive duration as 45m, 2h10m, 3d4h.
func shortDuration(d time.Duration) string {
	m := int(d.Round(time.Minute).Minutes())
	switch {
	case m < 60:
		return fmt.Sprintf("%dm", m)
	case m < 1440:
		return fmt.Sprintf("%dh%02dm", m/60, m%60)
	default:
		return fmt.Sprintf("%dd%dh", m/1440, (m%1440)/60)
	}
}

// spendVital is the vitals-bar segment for usage and the load hold. It leads
// with the agents' SUBSCRIPTION limits ("claude 5h 42% 7d 18% · codex 7d 4%")
// when any agent reported them — the number a subscriber budgets by — else
// "today 46.7M". Then "· 38% of budget" when a [budget] limit is set — orange
// from 80%, red once reached — and a red "load busy" while [load] holds
// dispatch. The budget is a PERCENTAGE because it counts weighted tokens, a
// different number from the raw count. "" on an older daemon.
func spendVital(st *protocol.StatusData) string {
	if st == nil || st.Usage == nil {
		return ""
	}
	u := st.Usage
	text := "today " + fmtTokens(u.Tokens)
	if q := quotaVital(u.Quotas); q != "" {
		text = q
	}
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
	now := time.Now()
	for _, q := range u.Quotas {
		head := q.Agent
		if q.Plan != "" {
			head += " (" + q.Plan + ")"
		}
		ws := make([]string, 0, len(q.Windows))
		for _, w := range q.Windows {
			ww := quotaPercent(w)
			if d := w.ResetsAt.Sub(now); !w.ResetsAt.IsZero() && d > 0 {
				ww += " (resets in " + shortDuration(d) + ")"
			}
			ws = append(ws, ww)
		}
		fmt.Fprintf(&b, "%s limits, as of %s ago: %s\n", head, shortDuration(max(now.Sub(q.At), 0)), strings.Join(ws, " · "))
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
