package usage

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
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
	if !l.Set("2026-10-09", "s1", Entry{Project: "p", Totals: Totals{CostUSD: 1}}) {
		t.Fatal("first set must change")
	}
	if l.Set("2026-10-09", "s1", Entry{Project: "p", Totals: Totals{CostUSD: 1}}) {
		t.Fatal("same value must not change")
	}
	l.Set("2026-10-09", "s1", Entry{Project: "p", Totals: Totals{CostUSD: 2}}) // replaces, never adds
	l.Set("2026-10-09", "s2", Entry{Project: "q", Totals: Totals{CostUSD: 3}})
	l.Set("2026-10-09", "helpers", Entry{Project: "", Totals: Totals{CostUSD: 0.5}})
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
	l.Set("2026-08-01", "s", Entry{Project: "", Totals: Totals{CostUSD: 1}})
	l.Set("2026-10-01", "s", Entry{Project: "", Totals: Totals{CostUSD: 1}})
	if !l.Prune(now, KeepDays) {
		t.Fatal("old day must be pruned")
	}
	if _, ok := l.Days["2026-10-01"]; !ok || len(l.Days) != 1 {
		t.Fatalf("days = %v", l.Days)
	}
}

func TestWeightedDiscountsCacheReads(t *testing.T) {
	if got := (Totals{Input: 100, Output: 10, CacheWrite: 40, CacheRead: 1000}).Weighted(); got != 100+50+50+100 {
		t.Fatalf("weighted = %d", got)
	}
}

func TestScanCountsActiveSlotsAndRecent(t *testing.T) {
	dir := t.TempDir()
	base := time.Date(2026, 10, 8, 12, 0, 0, 0, time.Local)
	at := func(m int) string { return base.Add(time.Duration(m) * time.Minute).UTC().Format(time.RFC3339Nano) }
	writeLines(t, filepath.Join(dir, "s.jsonl"),
		line("a", "claude-sonnet-5-5", at(1), 0, 100_000, 0, 0),
		line("b", "claude-sonnet-5-5", at(2), 0, 100_000, 0, 0), // same window
		line("c", "claude-sonnet-5-5", at(25), 0, 100_000, 0, 0),
		line("d", "claude-sonnet-5-5", at(55), 0, 100_000, 0, 0),
	)
	s := NewScanner()
	days, err := s.ScanDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := days[base.Format(DayFormat)].Slots; got != 3 {
		t.Fatalf("slots = %d, want 3 distinct windows", got)
	}
	if got := s.Recent(dir, base.Add(50*time.Minute)); got.Output != 100_000 {
		t.Fatalf("recent = %+v, want only the last record", got)
	}
}

func TestRankAmong(t *testing.T) {
	if r := RankAmong(12, nil, ClaudeScale); r.Of != 0 || r.Level != LevelHeavy {
		t.Fatalf("fallback rank = %+v, want heavy by threshold", r)
	}
	hist := []float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	cases := map[float64]int{0.5: LevelLight, 6.5: LevelNormal, 8.5: LevelHeavy, 50: LevelTop}
	for w, want := range cases {
		if r := RankAmong(w, hist, ClaudeScale); r.Level != want || r.Of != 10 {
			t.Errorf("RankAmong(%v) = %+v, want level %d", w, r, want)
		}
	}
}

func TestBurnThreshold(t *testing.T) {
	if got := BurnThreshold([]float64{1}, ClaudeScale); got != ClaudeScale.BurnFallback {
		t.Fatalf("thin history = %v", got)
	}
	rates := []float64{1, 1, 1, 1, 1, 1, 1, 1, 1, 1}
	if got := BurnThreshold(rates, ClaudeScale); got != ClaudeScale.BurnFloor {
		t.Fatalf("quiet history must clamp to the floor, got %v", got)
	}
	rates[9] = 40
	if got := BurnThreshold(rates, ClaudeScale); got != 40 {
		t.Fatalf("p90 = %v", got)
	}
	if HourlyRate(Totals{CostUSD: 10, Slots: 2}, ClaudeScale) != 0 {
		t.Fatal("too short a session must have no rate")
	}
	if got := HourlyRate(Totals{CostUSD: 10, Slots: 6}, ClaudeScale); !near(got, 10) {
		t.Fatalf("rate = %v, want $10 per active hour", got)
	}
}

func TestCodexScannerAttributesByCwdAndCountsDeltas(t *testing.T) {
	root := t.TempDir()
	prefix := filepath.Join(root, "worktrees")
	wt := filepath.Join(prefix, "p", "p-1")
	day := filepath.Join(root, "sessions", "2026", "10", "10")
	at := time.Date(2026, 10, 10, 10, 0, 0, 0, time.Local)
	ev := func(min int, in, cached, out int64) string {
		return `{"timestamp":"` + at.Add(time.Duration(min)*time.Minute).UTC().Format(time.RFC3339Nano) +
			`","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":` +
			itoa(in) + `,"cached_input_tokens":` + itoa(cached) + `,"output_tokens":` + itoa(out) + `}}}}`
	}
	log := filepath.Join(day, "rollout-a.jsonl")
	writeLines(t, log,
		`{"type":"session_meta","payload":{"cwd":"`+wt+`"}}`,
		ev(1, 1000, 600, 50),
		ev(2, 1000, 600, 50), // re-emitted: adds nothing
		`{"type":"event_msg","payload":{"type":"token_count","info":null}}`,
	)
	writeLines(t, filepath.Join(day, "rollout-b.jsonl"),
		`{"type":"session_meta","payload":{"cwd":"/home/me/own-project"}}`, ev(1, 5_000_000, 0, 0))

	c := NewCodexScanner(prefix)
	c.Scan(filepath.Join(root, "sessions"))
	got := Sum(c.For(filepath.Join(prefix, "p", "p-1")))
	if got.Input != 400 || got.CacheRead != 600 || got.Output != 50 || got.Slots != 1 {
		t.Fatalf("first scan = %+v", got)
	}
	// Appended later: only the delta counts, and the outside log never does.
	writeLines(t, log, ev(25, 3000, 2000, 150))
	c.Scan(filepath.Join(root, "sessions"))
	got = Sum(c.For(wt))
	if got.Input != 1000 || got.CacheRead != 2000 || got.Output != 150 || got.Slots != 2 {
		t.Fatalf("incremental = %+v", got)
	}
	if r := c.Recent(wt, at.Add(20*time.Minute)); r.Output != 100 {
		t.Fatalf("recent = %+v, want only the last delta", r)
	}
	if len(c.For(filepath.Join(prefix, "p", "p-2"))) != 0 {
		t.Fatal("another worktree must see nothing")
	}
}

func TestScaleForPicksPerAgent(t *testing.T) {
	if ScaleFor("codex").Weight(Totals{Output: 1}) != 5 || ScaleFor("").Weight(Totals{CostUSD: 2}) != 2 {
		t.Fatal("codex weighs tokens, claude weighs list price")
	}
}

func TestOpencodeScannerReplacesByIDAndFiltersByDir(t *testing.T) {
	db := filepath.Join(t.TempDir(), "opencode.db")
	writeLines(t, db, "") // only has to exist
	prefix := "/h/.lola/worktrees"
	at := time.Date(2026, 10, 10, 10, 0, 0, 0, time.Local)
	ms := func(m int) string { return itoa(at.Add(time.Duration(m) * time.Minute).UnixMilli()) }
	var queries []string
	reply := `[{"id":"a","dir":"/h/.lola/worktrees/p/p-1","created":` + ms(0) + `,"updated":` + ms(0) + `,"i":100,"o":10,"r":5,"cr":1000,"cw":0,"cost":0.5},
{"id":"x","dir":"/h/.lola/worktrees-not/p","created":` + ms(0) + `,"updated":` + ms(0) + `,"i":9999,"o":0,"r":0,"cr":0,"cw":0,"cost":0}]`
	o := NewOpencodeScanner(prefix)
	o.RunSQL = func(_ context.Context, _, q string) ([]byte, error) {
		queries = append(queries, q)
		return []byte(reply), nil
	}
	if err := o.Scan(context.Background(), db, at.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	got := Sum(o.For("/h/.lola/worktrees/p/p-1"))
	if got.Input != 100 || got.Output != 15 || got.CacheRead != 1000 || got.CostUSD != 0.5 || got.Slots != 1 {
		t.Fatalf("first = %+v", got)
	}
	if !strings.Contains(queries[0], "= '/h/.lola/worktrees/'") || !strings.Contains(queries[0], "time_updated > 0") {
		t.Fatalf("query must select lola's worktrees from the start:\n%s", queries[0])
	}
	// The same message re-read with final counts REPLACES, never adds; the
	// next query starts from the watermark minus the margin.
	reply = `[{"id":"a","dir":"/h/.lola/worktrees/p/p-1","created":` + ms(0) + `,"updated":` + ms(1) + `,"i":200,"o":20,"r":0,"cr":1000,"cw":0,"cost":1}]`
	_ = o.Scan(context.Background(), db, at.Add(time.Hour))
	if got := Sum(o.For("/h/.lola/worktrees/p/p-1")); got.Input != 200 || got.Output != 20 {
		t.Fatalf("replaced = %+v", got)
	}
	if want := "time_updated > " + itoa(at.Add(-2*time.Minute).UnixMilli()); !strings.Contains(queries[1], want) {
		t.Fatalf("second query lacks %q:\n%s", want, queries[1])
	}
	if err := o.Scan(context.Background(), filepath.Join(t.TempDir(), "none.db"), at); err != nil || len(queries) != 2 {
		t.Fatal("a missing database is not an error and runs no query")
	}
	if sqlString("it's") != "'it''s'" {
		t.Fatal("quote escaping")
	}
}
