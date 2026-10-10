// Package usage estimates what lola's agents SPEND, from the coding agent's own
// transcript files, and keeps the per-day ledger the dispatch budget reads.
//
// Why transcripts: the cost a user complains about ("$15 of tokens in a single
// evening") is spent by the claude processes lola starts, and every one of them
// already writes its token usage to a JSONL transcript under
// ~/.claude/projects/<slug(cwd)>/. lola never sees an API response itself, so
// the transcript is the only place the numbers exist. Claude Code keys that
// directory by the WORKING DIRECTORY, which is what makes per-session
// attribution free: a session's worktree is unique to it, so every claude that
// ran there — the worker, a resumed worker, its subagents, and the review
// passes lola runs IN the worktree — lands in the one directory ScanDir sums.
//
// # What is read, and what is not
//
// ONLY the numeric `message.usage` fields, the `message.model` id (used for
// nothing but the price lookup), the message/request ids (dedup) and the record
// timestamp (day bucketing). Never text, never a tool's input or output. The
// file is model output plus tool output — attacker-influenceable — so nothing
// read here is rendered, logged or executed; only sums leave this package.
//
// # An ESTIMATE, by construction
//
// The price table below is list price per model family. A subscription user
// pays nothing per token, a negotiated rate differs, and claude-code does not
// write the cost it computed. The figures are therefore labelled estimates in
// every UI, and a model this table does not know is priced by family keyword
// (or at zero) rather than failing the scan: tokens are still counted.
//
// # Only claude writes these files
//
// Codex and opencode keep differently shaped logs outside the worktree slug, so
// a session running them reports no spend (an absent figure, not $0). That gap
// is documented rather than guessed at.
//
// Stdlib-only leaf: no lola imports, so the daemon, the TUI and the desktop app
// can all use it.
package usage

import (
	"strings"
)

// Totals is a token + cost sum. Zero is "nothing spent / nothing known".
type Totals struct {
	Input      int64   `json:"input,omitempty"`
	Output     int64   `json:"output,omitempty"`
	CacheRead  int64   `json:"cache_read,omitempty"`
	CacheWrite int64   `json:"cache_write,omitempty"`
	CostUSD    float64 `json:"cost_usd,omitempty"`
}

// Add accumulates o into t.
func (t *Totals) Add(o Totals) {
	t.Input += o.Input
	t.Output += o.Output
	t.CacheRead += o.CacheRead
	t.CacheWrite += o.CacheWrite
	t.CostUSD += o.CostUSD
}

// Tokens is every token billed, cache traffic included.
func (t Totals) Tokens() int64 { return t.Input + t.Output + t.CacheRead + t.CacheWrite }

// IsZero reports whether nothing was recorded.
func (t Totals) IsZero() bool { return t == Totals{} }

// price is USD per MILLION tokens. Cache writes are a fixed multiple of input
// (1.25× for a 5-minute entry, 2× for an hour-long one), so only the read rate
// is listed: it is not a fixed ratio across generations.
type price struct {
	in, out, cacheRead float64
}

// prices maps a model-id PREFIX to its list price. Longest prefix wins, so a
// dated or suffixed id ("claude-opus-4-5-20251101", "claude-sonnet-4-6[1m]")
// still resolves. List prices as of 2026-10; keep newest first per family only
// for readability — lookup does not depend on order.
var prices = map[string]price{
	"claude-fable-5-1":  {10, 50, 0.25},
	"claude-fable-5":    {10, 50, 1.00},
	"claude-mythos-5-1": {10, 50, 0.25},
	"claude-mythos-5":   {10, 50, 1.00},
	"claude-opus-5-5":   {4, 20, 0.20},
	"claude-opus-5":     {5, 25, 0.50},
	"claude-opus-4-8":   {5, 25, 0.50},
	"claude-opus-4-7":   {5, 25, 0.50},
	"claude-opus-4-6":   {5, 25, 0.50},
	"claude-opus-4-5":   {5, 25, 0.50},
	"claude-opus-4-1":   {15, 75, 1.50},
	"claude-opus-4":     {15, 75, 1.50},
	"claude-sonnet-5-5": {2, 10, 0.20},
	"claude-sonnet-5":   {2, 10, 0.20},
	"claude-sonnet-4":   {3, 15, 0.30},
	"claude-3-7-sonnet": {3, 15, 0.30},
	"claude-3-5-sonnet": {3, 15, 0.30},
	"claude-haiku-5-5":  {0.10, 0.50, 0.01},
	"claude-haiku-4-5":  {1, 5, 0.10},
	"claude-3-5-haiku":  {0.80, 4, 0.08},
}

// familyPrices is the fallback for an id no prefix matches — a model released
// after this table was written. Priced at the most recent known member of the
// family, so a new model shows a plausible figure instead of $0.
var familyPrices = []struct {
	keyword string
	p       price
}{
	{"fable", price{10, 50, 0.25}},
	{"mythos", price{10, 50, 0.25}},
	{"opus", price{4, 20, 0.20}},
	{"sonnet", price{2, 10, 0.20}},
	{"haiku", price{0.10, 0.50, 0.01}},
}

func priceFor(model string) (price, bool) {
	m := strings.ToLower(strings.TrimSpace(model))
	if m == "" {
		return price{}, false
	}
	best, bestLen := price{}, 0
	for prefix, p := range prices {
		if len(prefix) > bestLen && strings.HasPrefix(m, prefix) {
			best, bestLen = p, len(prefix)
		}
	}
	if bestLen > 0 {
		return best, true
	}
	for _, f := range familyPrices {
		if strings.Contains(m, f.keyword) {
			return f.p, true
		}
	}
	return price{}, false
}

// Cost prices one message's usage at model's list price, every cache write at
// the 5-minute rate. An unknown model (claude-code's "<synthetic>" placeholder
// included) costs 0.
func Cost(model string, t Totals) float64 { return cost(model, t, 0, false) }

// cost is Cost with the two modifiers a transcript can carry: hourWrite of the
// CacheWrite tokens were written with the 1-hour TTL (2× input instead of
// 1.25×), and fast-mode requests are billed at twice the standard rate.
func cost(model string, t Totals, hourWrite int64, fast bool) float64 {
	p, ok := priceFor(model)
	if !ok {
		return 0
	}
	const mtok = 1e6
	c := (float64(t.Input)*p.in +
		float64(t.Output)*p.out +
		float64(t.CacheWrite-hourWrite)*p.in*1.25 +
		float64(hourWrite)*p.in*2 +
		float64(t.CacheRead)*p.cacheRead) / mtok
	if fast {
		c *= 2
	}
	return c
}
