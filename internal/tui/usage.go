package tui

import (
	"fmt"
	"strings"

	"github.com/sushidev-team/lola/internal/protocol"
)

// Spend rendering (internal/usage on the daemon side). Every figure is an
// ESTIMATE at list price, so each one carries a "~" — a subscription user pays
// nothing per token, and a bare "$18.33" would read as a bill.

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

// costCell is the sessions table's COST cell: the session's whole spend, "-"
// when nothing is known (no transcript yet, or an agent lola cannot read).
func costCell(si protocol.SessionInfo) string {
	if si.Usage == nil {
		return "-"
	}
	return "~" + fmtUSD(si.Usage.TotalUSD)
}

// anyUsage reports whether a COST column has anything to show, so a fleet
// running only codex/opencode keeps the columns it had.
func anyUsage(list []protocol.SessionInfo) bool {
	for _, si := range list {
		if si.Usage != nil {
			return true
		}
	}
	return false
}

// usageDetailLine is the detail panel's cost line, "" when nothing is known.
func usageDetailLine(si protocol.SessionInfo) string {
	u := si.Usage
	if u == nil {
		return ""
	}
	return fmt.Sprintf("cost:     ~%s total · ~%s today · %s tokens (estimate)",
		fmtUSD(u.TotalUSD), fmtUSD(u.TodayUSD), fmtTokens(u.Tokens))
}

// spendVital is the vitals-bar segment for today's spend and the load hold:
// "today ~$12.34 / $50", orange from 80% of the budget, red once reached, and
// a red "load busy" while [load] holds dispatch. "" on an older daemon.
func spendVital(st *protocol.StatusData) string {
	if st == nil || st.Usage == nil {
		return ""
	}
	u := st.Usage
	text := "today ~" + fmtUSD(u.TodayUSD)
	if u.BudgetUSD > 0 {
		text += " / " + fmtUSD(u.BudgetUSD)
	}
	switch {
	case u.BudgetUSD > 0 && u.TodayUSD >= u.BudgetUSD:
		text = badText.Render(text + " budget reached")
	case u.BudgetUSD > 0 && u.TodayUSD >= 0.8*u.BudgetUSD:
		text = statusOrange.Render(text)
	}
	if u.Load != nil && u.Load.Busy != "" {
		text += " " + badText.Render("load busy")
	}
	return text
}

// spendSummary is `lola status`'s spend block: today's total against the
// global limit, each project with spend or a limit, and the load sample.
func spendSummary(u *protocol.UsageStatus) string {
	if u == nil {
		return ""
	}
	var b strings.Builder
	budget := "no limit"
	if u.BudgetUSD > 0 {
		budget = "limit " + fmtUSD(u.BudgetUSD)
	}
	fmt.Fprintf(&b, "spend %s: ~%s (%s tokens, estimate) — %s\n", u.Day, fmtUSD(u.TodayUSD), fmtTokens(u.Tokens), budget)
	for _, p := range u.Projects {
		line := fmt.Sprintf("  %s: ~%s", p.Name, fmtUSD(p.TodayUSD))
		if p.BudgetUSD > 0 {
			line += " / " + fmtUSD(p.BudgetUSD)
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
