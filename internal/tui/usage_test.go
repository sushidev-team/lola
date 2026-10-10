package tui

import (
	"strings"
	"testing"

	"github.com/sushidev-team/lola/internal/protocol"
)

func TestUsageRendering(t *testing.T) {
	if got := costCell(protocol.SessionInfo{}); got != "-" {
		t.Errorf("unknown usage = %q, want - (absent is unknown, not zero)", got)
	}
	si := protocol.SessionInfo{Usage: &protocol.UsageInfo{Tokens: 46_700_420, TodayTokens: 2_100_000, TotalUSD: 18.333,
		Level: 2, Percentile: 82, Of: 40}}
	if got := costCell(si); got != "46.7M ▅" {
		t.Errorf("tokens cell = %q", got)
	}
	line := usageDetailLine(si)
	for _, want := range []string{"46.7M", "2.1M today", "heavier than 82% of your last 40 sessions", "~$18.33 at list price"} {
		if !strings.Contains(line, want) {
			t.Errorf("detail line = %q, want %q", line, want)
		}
	}
	si.Usage.Burning, si.Usage.TokensPerHour = true, 12_300_000
	if got := costCell(si); !strings.Contains(got, flame) {
		t.Errorf("burning cell = %q, want the flame", got)
	}
	if got := usageDetailLine(si); !strings.Contains(got, "burning 12.3M tokens/h") {
		t.Errorf("burning detail = %q", got)
	}
	si.Usage = &protocol.UsageInfo{Tokens: 10, Level: 0}
	if got := usageDetailLine(si); !strings.Contains(got, "too little history") {
		t.Errorf("fallback detail = %q", got)
	}
	if fmtUSD(250.4) != "$250" || fmtTokens(950) != "950" || fmtTokens(12_300) != "12.3k" {
		t.Errorf("formatters: %s %s %s", fmtUSD(250.4), fmtTokens(950), fmtTokens(12_300))
	}
}

func TestSpendVital(t *testing.T) {
	if spendVital(&protocol.StatusData{}) != "" {
		t.Error("an older daemon (no usage) renders nothing")
	}
	st := &protocol.StatusData{Usage: &protocol.UsageStatus{Tokens: 46_700_000, Weighted: 210_000_000, BudgetTokens: 200_000_000,
		Load: &protocol.LoadInfo{Busy: "machine busy"}}}
	got := spendVital(st)
	for _, want := range []string{"today 46.7M", "105% of budget", "dispatch held", "load busy"} {
		if !strings.Contains(got, want) {
			t.Errorf("vital = %q, want %q", got, want)
		}
	}
	sum := spendSummary(&protocol.UsageStatus{Day: "2026-10-09", Tokens: 3_000_000, Weighted: 1_000_000, TodayUSD: 3,
		Projects: []protocol.ProjectSpend{{Name: "p", Tokens: 3_000_000, Weighted: 1_000_000, BudgetTokens: 4_000_000}},
		Load:     &protocol.LoadInfo{Load1: -1, CPUs: 8, FreeMemPercent: 40}})
	for _, want := range []string{"tokens 2026-10-09: 3.0M (1.0M weighted, ~$3.00 at list price) — no limit", "p: 3.0M (1.0M weighted) — 25% of 4.0M", "load: unknown · memory 40% free"} {
		if !strings.Contains(sum, want) {
			t.Errorf("summary = %q, want %q", sum, want)
		}
	}
}
