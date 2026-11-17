package goal

import (
	"errors"
	"maps"
	"math"
	"slices"
	"time"

	"github.com/sushidev-team/lola/internal/goalpolicy"
	"github.com/sushidev-team/lola/internal/usage"
)

// ObserveUsage replaces an absolute run observation, retaining high-water
// counters when logs rotate, disappear, or are rewritten. Repeated scans never
// add the same usage twice. Partial/missing describes coverage, not completion.
func (g *Goal) ObserveUsage(id string, observation usage.Observation) error {
	if g.Execution == nil {
		return errors.New("goal has no execution")
	}
	e := g.Execution
	run, ok := e.Runs[id]
	if !ok {
		return errors.New("usage run is not reserved")
	}
	if err := validateUsage(observation); err != nil {
		return err
	}
	if observation.At.Before(run.ReservedAt) || (run.Usage != nil && observation.At.Before(run.Usage.At)) {
		return errors.New("usage observation is stale")
	}
	days := map[string]usage.Totals{}
	if run.Usage != nil {
		for day, t := range run.Usage.Days {
			days[day] = t
		}
	}
	for day, t := range observation.Days {
		old := days[day]
		days[day] = usage.Totals{Input: max(old.Input, t.Input), Output: max(old.Output, t.Output), CacheRead: max(old.CacheRead, t.CacheRead), CacheWrite: max(old.CacheWrite, t.CacheWrite), CostUSD: max(old.CostUSD, t.CostUSD), Slots: max(old.Slots, t.Slots)}
	}
	observation.Days = days
	if err := validateUsage(observation); err != nil {
		return err
	}
	total := usageTotals(days)
	run.Tokens = max(run.Tokens, total.Tokens())
	run.SpendUSD = max(run.SpendUSD, total.CostUSD)
	run.Estimated = true
	run.Usage = &observation
	e.Runs[id] = run
	var tokens int64
	var spend float64
	for _, id := range slices.Sorted(maps.Keys(e.Runs)) {
		r := e.Runs[id]
		tokens += r.Tokens
		spend += r.SpendUSD
	}
	e.ObservedTokens = max(e.ObservedTokens, tokens)
	e.ObservedSpendUSD = max(e.ObservedSpendUSD, spend)
	if observation.At.After(e.UsageUpdatedAt) {
		e.UsageUpdatedAt = observation.At
	}
	return nil
}

func validateUsage(o usage.Observation) error {
	if o.At.IsZero() || (o.Agent != "claude" && o.Agent != "codex" && o.Agent != "opencode") || (o.Status != "partial" && o.Status != "missing") || (o.CostStatus != "estimate" && o.CostStatus != "unavailable") || len(o.Reason) > 500 || len(o.Days) > 366 {
		return errors.New("invalid usage observation")
	}
	for day, t := range o.Days {
		if _, err := time.Parse(usage.DayFormat, day); err != nil || !usage.ValidTotals(t) {
			return errors.New("invalid daily usage counters")
		}
	}
	total := usageTotals(o.Days)
	if total.Tokens() < 0 || total.Tokens() > 1e15 || total.CostUSD > 1e12 || math.IsNaN(total.CostUSD) || math.IsInf(total.CostUSD, 0) {
		return errors.New("usage totals exceed bounds")
	}
	return nil
}

// ValidateUsageObservation is shared with the pre-authorization proposal store.
func ValidateUsageObservation(o usage.Observation) error { return validateUsage(o) }

func (g Goal) UsageExceeded() bool {
	e := g.Execution
	return e != nil && (e.ObservedTokens >= e.Policy.Limits.Tokens || (e.Policy.Limits.SpendUSD > 0 && e.ObservedSpendUSD >= e.Policy.Limits.SpendUSD))
}

// UsageCoverage gives callers an honest denominator including coordinator and
// specialist reservations that do not yet have persisted telemetry.
type UsageCoverage struct {
	Runs            int `json:"runs"`
	Missing         int `json:"missing"`
	Partial         int `json:"partial"`
	CostUnavailable int `json:"cost_unavailable"`
}

func (g Goal) UsageCoverage() (out UsageCoverage) {
	if g.Execution == nil {
		return out
	}
	for _, r := range g.Execution.Runs {
		out.Runs++
		if r.Usage == nil || r.Usage.Status == "missing" {
			out.Missing++
		} else {
			out.Partial++
		}
		if r.Usage == nil || r.Usage.CostStatus == "unavailable" {
			out.CostUnavailable++
		}
	}
	return out
}

func preserveUsage(old, next *Execution) error {
	if next.ObservedTokens < old.ObservedTokens || next.ObservedSpendUSD < old.ObservedSpendUSD {
		return errors.New("observed goal usage cannot decrease")
	}
	for id, r := range old.Runs {
		n, ok := next.Runs[id]
		if !ok || n.Kind != r.Kind || n.Subject != r.Subject || !n.ReservedAt.Equal(r.ReservedAt) || n.Tokens < r.Tokens || n.SpendUSD < r.SpendUSD {
			return errors.New("reserved run and observed usage must be retained")
		}
		if r.Usage == nil {
			continue
		}
		if n.Usage == nil || n.Usage.At.Before(r.Usage.At) || n.Usage.Agent != r.Usage.Agent {
			return errors.New("usage provenance cannot be replaced")
		}
		for day, t := range r.Usage.Days {
			u := n.Usage.Days[day]
			if u.Input < t.Input || u.Output < t.Output || u.CacheRead < t.CacheRead || u.CacheWrite < t.CacheWrite || u.CostUSD < t.CostUSD {
				return errors.New("daily usage cannot decrease")
			}
		}
	}
	return nil
}

// UsageReport exposes observations and their coverage without duplicating the
// goal contract, repository manifest, and execution history in a usage response.
type UsageReport struct {
	Goal             string              `json:"goal"`
	Limits           goalpolicy.Limits   `json:"limits"`
	ObservedTokens   int64               `json:"observed_tokens"`
	ObservedSpendUSD float64             `json:"observed_spend_usd"`
	UpdatedAt        time.Time           `json:"updated_at,omitempty"`
	Coverage         UsageCoverage       `json:"coverage"`
	Runs             map[string]AgentRun `json:"runs"`
}

func (g Goal) UsageReport() UsageReport {
	r := UsageReport{Goal: g.ID, Coverage: g.UsageCoverage()}
	if e := g.Execution; e != nil {
		r.Limits = e.Policy.Limits
		r.ObservedTokens = e.ObservedTokens
		r.ObservedSpendUSD = e.ObservedSpendUSD
		r.UpdatedAt = e.UsageUpdatedAt
		r.Runs = e.Runs
	}
	return r
}

// Sum in a fixed order because persisted dollar floors are compared on reload.
func usageTotals(days map[string]usage.Totals) (total usage.Totals) {
	for _, day := range slices.Sorted(maps.Keys(days)) {
		total.Add(days[day])
	}
	return total
}
