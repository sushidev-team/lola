package tui

import (
	"strings"
	"testing"

	"github.com/sushidev-team/lola/internal/protocol"
)

func TestUsageRendering(t *testing.T) {
	if got := costCell(protocol.SessionInfo{}); got != "-" {
		t.Errorf("unknown cost = %q, want - (absent is unknown, not $0)", got)
	}
	si := protocol.SessionInfo{Usage: &protocol.UsageInfo{TotalUSD: 18.333, TodayUSD: 2.5, Tokens: 46_700_420}}
	if got := costCell(si); got != "~$18.33" {
		t.Errorf("cost cell = %q", got)
	}
	if line := usageDetailLine(si); !strings.Contains(line, "~$18.33 total") || !strings.Contains(line, "46.7M tokens") || !strings.Contains(line, "estimate") {
		t.Errorf("detail line = %q", line)
	}
	if fmtUSD(250.4) != "$250" || fmtTokens(950) != "950" || fmtTokens(12_300) != "12.3k" {
		t.Errorf("formatters: %s %s %s", fmtUSD(250.4), fmtTokens(950), fmtTokens(12_300))
	}
}

func TestSpendVital(t *testing.T) {
	if spendVital(&protocol.StatusData{}) != "" {
		t.Error("an older daemon (no usage) renders nothing")
	}
	st := &protocol.StatusData{Usage: &protocol.UsageStatus{TodayUSD: 60, BudgetUSD: 50,
		Load: &protocol.LoadInfo{Busy: "machine busy"}}}
	got := spendVital(st)
	for _, want := range []string{"today ~$60.00 / $50.00", "budget reached", "load busy"} {
		if !strings.Contains(got, want) {
			t.Errorf("vital = %q, want %q", got, want)
		}
	}
	sum := spendSummary(&protocol.UsageStatus{Day: "2026-10-09", TodayUSD: 3,
		Projects: []protocol.ProjectSpend{{Name: "p", TodayUSD: 3, BudgetUSD: 5}},
		Load:     &protocol.LoadInfo{Load1: -1, CPUs: 8, FreeMemPercent: 40}})
	for _, want := range []string{"spend 2026-10-09: ~$3.00", "no limit", "p: ~$3.00 / $5.00", "load: unknown · memory 40% free"} {
		if !strings.Contains(sum, want) {
			t.Errorf("summary = %q, want %q", sum, want)
		}
	}
}
