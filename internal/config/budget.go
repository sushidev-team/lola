package config

import (
	"errors"
	"fmt"
	"math"
)

// The [budget] and [load] tables hold DISPATCH, not sessions. Both are checked
// at the top of every tick, beside the runtime health gate, and share its
// discipline: a held tick skips, records why as the poll's LastError, and
// mutates nothing — no seen entry, no label, no in-flight claim, and never a
// live session. Spend that is already running is the user's to stop; lola only
// declines to start more of it.
//
// Both tables are optional and every zero value means "off", so an absent
// table is no behavior change and a fresh Config persists neither.

// BudgetConfig is the [budget] table.
//
//   - DailyTokens is the GLOBAL daily limit, across every project plus lola's
//     own helpers (brain, statusagent), in WEIGHTED tokens (internal/usage:
//     the agents' transcripts, each token weighted by its list-price ratio to
//     input — output 5×, cache write 1.25×, cache read 0.1×). Weighted because
//     most raw tokens are cheap cache reads; a limit in raw tokens would be
//     spent mostly by a long session re-reading its own context. A local
//     calendar day; the count resets at midnight. 0 = no global limit.
//     Per-project limits are [[project]].daily_budget_tokens.
//   - Notify sends one notification per limit per day when spend first reaches
//     it, so a held dispatch is not discovered only by reading a status line.
type BudgetConfig struct {
	DailyTokens int64 `toml:"daily_tokens,omitempty"`
	Notify      bool  `toml:"notify,omitempty"`
}

// LoadConfig is the [load] table — a machine-load hold on top of the slot cap,
// for the "ten parallel builds killed my laptop" case the cap cannot see (a
// slot is one agent, not one agent's `cargo build`).
//
//   - MaxLoadPerCPU holds dispatch while the 1-minute load average divided by
//     the CPU count is above it (1.0 = every core busy). 0 = off.
//   - MinFreeMemoryPercent holds dispatch while the system reports less free
//     memory than this, as a percentage (macOS kern.memorystatus_level, Linux
//     MemAvailable/MemTotal). 0 = off.
//
// A probe that cannot answer holds NOTHING: unlike the reconcile revert, the
// cost of a wrong "busy" here is a machine that silently never dispatches.
type LoadConfig struct {
	MaxLoadPerCPU        float64 `toml:"max_load_per_cpu,omitempty"`
	MinFreeMemoryPercent float64 `toml:"min_free_memory_percent,omitempty"`
}

// Enabled reports whether either load check is on.
func (l LoadConfig) Enabled() bool { return l.MaxLoadPerCPU > 0 || l.MinFreeMemoryPercent > 0 }

// ProjectBudget is name's daily limit (0 = none).
func (c *Config) ProjectBudget(name string) int64 {
	if p := c.ProjectByName(name); p != nil {
		return p.DailyBudgetTokens
	}
	return 0
}

func (c *Config) validateLimits() []error {
	var errs []error
	bad := func(v float64) bool { return v < 0 || math.IsNaN(v) || math.IsInf(v, 0) }
	if c.Budget.DailyTokens < 0 {
		errs = append(errs, errors.New("budget.daily_tokens must be >= 0 (0 = no limit)"))
	}
	if bad(c.Load.MaxLoadPerCPU) {
		errs = append(errs, errors.New("load.max_load_per_cpu must be >= 0 (0 = off)"))
	}
	if bad(c.Load.MinFreeMemoryPercent) || c.Load.MinFreeMemoryPercent >= 100 {
		errs = append(errs, errors.New("load.min_free_memory_percent must be in [0, 100) (0 = off)"))
	}
	for _, p := range c.Projects {
		if p.DailyBudgetTokens < 0 {
			errs = append(errs, fmt.Errorf("project %q: daily_budget_tokens must be >= 0 (0 = no limit)", p.Name))
		}
	}
	return errs
}

// tableOf reads an optional all-plain-fields table: absent is the zero value.
func tableOf[T any](p *T) T {
	if p == nil {
		var z T
		return z
	}
	return *p
}

// nonZero is tableOf's inverse for Save: the zero table is omitted from the file.
func nonZero[T comparable](v T) *T {
	var z T
	if v == z {
		return nil
	}
	return &v
}
