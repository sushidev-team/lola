package usage

import (
	"math"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// line builds one assistant record in the shape claude-code writes, trimmed to
// the fields this package decodes (plus a text block, which must be ignored).
func line(id, model, ts string, in, out, cw, cr int64) string {
	return `{"type":"assistant","timestamp":"` + ts + `","message":{"id":"` + id + `","model":"` + model +
		`","content":[{"type":"text","text":"usage is not this"}],"usage":{"input_tokens":` + itoa(in) +
		`,"output_tokens":` + itoa(out) + `,"cache_creation_input_tokens":` + itoa(cw) +
		`,"cache_read_input_tokens":` + itoa(cr) + `}}}`
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

func writeLines(t *testing.T, path string, lines ...string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	for _, l := range lines {
		if _, err := f.WriteString(l + "\n"); err != nil {
			t.Fatal(err)
		}
	}
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestCostUsesLongestPrefixAndFamilyFallback(t *testing.T) {
	m := Totals{Input: 1_000_000}
	cases := map[string]float64{
		"claude-opus-5-5":          4,
		"claude-opus-5":            5,
		"claude-opus-4-5-20251101": 5,  // dated id → its prefix
		"claude-opus-4-1-20250805": 15, // NOT claude-opus-4's sibling 4-5
		"claude-sonnet-4-6[1m]":    3,
		"claude-haiku-5-5":         0.10,
		"claude-opus-9-9":          4, // unknown → family fallback
		"<synthetic>":              0,
		"":                         0,
	}
	for model, want := range cases {
		if got := Cost(model, m); !near(got, want) {
			t.Errorf("Cost(%q) = %v, want %v", model, got, want)
		}
	}
}

func TestCostPricesCacheAndOutput(t *testing.T) {
	// sonnet-5-5: in 2, out 10, read 0.20; 5m write = 2.5
	got := Cost("claude-sonnet-5-5", Totals{Output: 1e6, CacheWrite: 1e6, CacheRead: 1e6})
	if !near(got, 10+2.5+0.20) {
		t.Fatalf("cost = %v", got)
	}
	// An hour-long cache write is 2× input, and fast mode doubles everything.
	if got := cost("claude-sonnet-5-5", Totals{CacheWrite: 1e6}, 1e6, true); !near(got, 8) {
		t.Fatalf("1h fast cost = %v, want 8", got)
	}
}

func TestScanDirSumsDedupsAndBucketsByDay(t *testing.T) {
	dir := t.TempDir()
	d1 := time.Date(2026, 10, 8, 12, 0, 0, 0, time.Local)
	d2 := d1.AddDate(0, 0, 1)
	ts1, ts2 := d1.UTC().Format(time.RFC3339Nano), d2.UTC().Format(time.RFC3339Nano)
	a := line("m1", "claude-sonnet-5-5", ts1, 100, 1_000_000, 0, 0)
	writeLines(t, filepath.Join(dir, "s1.jsonl"),
		`{"type":"user","message":{"role":"user","content":"hi"}}`,
		a,
		a, // second content block of the same message: same usage, counted once
		line("m2", "claude-sonnet-5-5", ts2, 0, 0, 0, 1_000_000),
		`{"type":"assistant","message":{"id":"bad"`, // truncated mid-write — skipped
	)
	// A subagent transcript one level down, and a resumed session that copied
	// m1 into a new file: m1 still counts once.
	writeLines(t, filepath.Join(dir, "s1", "subagents", "agent-x.jsonl"), line("m3", "claude-haiku-5-5", ts2, 1_000_000, 0, 0, 0))
	writeLines(t, filepath.Join(dir, "s2.jsonl"), a)

	s := NewScanner()
	days, err := s.ScanDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	k1, k2 := d1.Format(DayFormat), d2.Format(DayFormat)
	if got := days[k1]; got.Output != 1_000_000 || got.Input != 100 || !near(got.CostUSD, 10+100*2/1e6) {
		t.Errorf("day1 = %+v", got)
	}
	if got := days[k2]; got.CacheRead != 1_000_000 || got.Input != 1_000_000 || !near(got.CostUSD, 0.20+0.10) {
		t.Errorf("day2 = %+v", got)
	}

	// Incremental: an append is read, an unchanged file is not double counted.
	writeLines(t, filepath.Join(dir, "s1.jsonl"), line("m4", "claude-sonnet-5-5", ts2, 0, 1_000_000, 0, 0))
	days, _ = s.ScanDir(dir)
	if got := days[k2].Output; got != 1_000_000 {
		t.Errorf("after append day2 output = %d", got)
	}
	again, _ := s.ScanDir(dir)
	if Sum(again) != Sum(days) {
		t.Errorf("rescan changed totals: %+v vs %+v", Sum(again), Sum(days))
	}
}

func TestScanDirMissingIsEmpty(t *testing.T) {
	days, err := NewScanner().ScanDir(filepath.Join(t.TempDir(), "nope"))
	if err != nil || len(days) != 0 {
		t.Fatalf("missing dir = %v, %v", days, err)
	}
}

func TestScanDirShrunkFileStartsOver(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "s.jsonl")
	ts := time.Now().UTC().Format(time.RFC3339Nano)
	writeLines(t, p, line("a", "claude-sonnet-5-5", ts, 0, 10, 0, 0), line("b", "claude-sonnet-5-5", ts, 0, 10, 0, 0))
	s := NewScanner()
	s.ScanDir(dir)
	if err := os.WriteFile(p, []byte(line("c", "claude-sonnet-5-5", ts, 0, 5, 0, 0)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s.ScanDir(dir)            // detects the shrink and resets
	days, _ := s.ScanDir(dir) // re-reads from scratch
	if got := Sum(days).Output; got != 5 {
		t.Fatalf("output after rewrite = %d, want 5", got)
	}
}

func TestSlugDir(t *testing.T) {
	got := SlugDir("/r", "/Users/x/.lola/worktrees/p/p-1")
	if want := "/r/-Users-x--lola-worktrees-p-p-1"; got != want {
		t.Fatalf("SlugDir = %q, want %q", got, want)
	}
	if SlugDir("", "/a") != "" || SlugDir("/r", "") != "" {
		t.Fatal("empty input must answer empty")
	}
}

func TestLedgerSetIsIdempotentAndSums(t *testing.T) {
	l := &Ledger{}
	if !l.Set("2026-10-09", "s1", "p", Totals{CostUSD: 1}) {
		t.Fatal("first set must change")
	}
	if l.Set("2026-10-09", "s1", "p", Totals{CostUSD: 1}) {
		t.Fatal("same value must not change")
	}
	l.Set("2026-10-09", "s1", "p", Totals{CostUSD: 2}) // replaces, never adds
	l.Set("2026-10-09", "s2", "q", Totals{CostUSD: 3})
	l.Set("2026-10-09", "helpers", "", Totals{CostUSD: 0.5})
	all, by := l.Day("2026-10-09")
	if !near(all.CostUSD, 5.5) || !near(by["p"].CostUSD, 2) || !near(by["q"].CostUSD, 3) || len(by) != 2 {
		t.Fatalf("day = %+v %+v", all, by)
	}

	path := filepath.Join(t.TempDir(), "state", "usage.json")
	if err := l.Save(path); err != nil {
		t.Fatal(err)
	}
	back, err := LoadLedger(path)
	if err != nil {
		t.Fatal(err)
	}
	if a, _ := back.Day("2026-10-09"); !near(a.CostUSD, 5.5) {
		t.Fatalf("round trip = %+v", a)
	}
}

func TestLedgerPrune(t *testing.T) {
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.Local)
	l := &Ledger{}
	l.Set("2026-08-01", "s", "", Totals{CostUSD: 1})
	l.Set("2026-10-01", "s", "", Totals{CostUSD: 1})
	if !l.Prune(now, KeepDays) {
		t.Fatal("old day must be pruned")
	}
	if _, ok := l.Days["2026-10-01"]; !ok || len(l.Days) != 1 {
		t.Fatalf("days = %v", l.Days)
	}
}
