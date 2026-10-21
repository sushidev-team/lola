package goal

import (
	"math"
	"path/filepath"
	"testing"
	"time"

	"github.com/sushidev-team/lola/internal/usage"
)

func TestGoalUsageSurvivesRestartWithoutDoubleCounting(t *testing.T) {
	g := executable(t)
	now := time.Now().UTC()
	if err := g.ReserveRun("worker1", "reviewer", "backend", now); err != nil {
		t.Fatal(err)
	}
	o := usage.Observation{Agent: "claude", Status: "partial", CostStatus: "estimate", Reason: "reported", At: now, Days: map[string]usage.Totals{now.Format(usage.DayFormat): {Input: 100, Output: 20, CostUSD: 0.5}}}
	for i := 0; i < 2; i++ {
		if err := g.ObserveUsage("worker1", o); err != nil {
			t.Fatal(err)
		}
	}
	if g.Execution.ObservedTokens != 120 || g.Execution.ObservedSpendUSD != 0.5 {
		t.Fatal("rescan double counted")
	}
	path := filepath.Join(t.TempDir(), "goals.json")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	g, err = store.Save(g)
	if err != nil {
		t.Fatal(err)
	}
	store, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	g, _ = store.Get(g.ID)
	o.At = now.Add(time.Minute)
	o.Status = "missing"
	o.Days = nil
	o.CostStatus = "unavailable"
	if err := g.ObserveUsage("worker1", o); err != nil {
		t.Fatal(err)
	}
	if g.Execution.ObservedTokens != 120 || g.Execution.ObservedSpendUSD != 0.5 || g.UsageCoverage().Missing != 1 {
		t.Fatal("missing logs erased spend or hid uncertainty")
	}
	g, err = store.Save(g)
	if err != nil {
		t.Fatal(err)
	}
	lower := copyGoal(g)
	lower.Execution.ObservedTokens = 0
	if _, err := store.Save(lower); err == nil {
		t.Fatal("aggregate usage could decrease")
	}
	lower = copyGoal(g)
	delete(lower.Execution.Runs, "worker1")
	if _, err := store.Save(lower); err == nil {
		t.Fatal("reserved usage could disappear")
	}
	lower = copyGoal(g)
	lower.Execution.Runs["worker1"].Usage.Days = map[string]usage.Totals{}
	if _, err := store.Save(lower); err == nil {
		t.Fatal("daily counters could disappear")
	}
}

func TestGoalUsageInvalidObservationsDoNotMutateBudget(t *testing.T) {
	for _, mode := range []string{"negative", "overflow", "nan", "stale", "unreserved", "bad-date", "exact-claim"} {
		t.Run(mode, func(t *testing.T) {
			g := executable(t)
			now := time.Now().UTC()
			if err := g.ReserveRun("r1", "reviewer", "backend", now); err != nil {
				t.Fatal(err)
			}
			day := now.Format(usage.DayFormat)
			id := "r1"
			o := usage.Observation{Agent: "codex", Status: "partial", CostStatus: "unavailable", At: now, Days: map[string]usage.Totals{day: {Input: 100}}}
			switch mode {
			case "negative":
				o.Days[day] = usage.Totals{Input: -1}
			case "overflow":
				o.Days[day] = usage.Totals{Input: math.MaxInt64}
			case "nan":
				o.Days[day] = usage.Totals{CostUSD: math.NaN()}
			case "stale":
				o.At = now.Add(-time.Minute)
			case "unreserved":
				id = "r2"
			case "bad-date":
				o.Days = map[string]usage.Totals{"not-a-day": {Input: 100}}
			case "exact-claim":
				o.Status = "exact"
			}
			if err := g.ObserveUsage(id, o); err == nil {
				t.Fatal("invalid usage accepted")
			}
			if g.Execution.ObservedTokens != 0 || g.Execution.Runs["r1"].Usage != nil {
				t.Fatal("invalid usage mutated goal")
			}
		})
	}
}

func TestGoalUsageBudgetGatesFurtherRuns(t *testing.T) {
	g := executable(t)
	now := time.Now().UTC()
	if err := g.ReserveRun("r1", "reviewer", "backend", now); err != nil {
		t.Fatal(err)
	}
	if err := g.ObserveUsage("r1", usage.Observation{Agent: "codex", Status: "partial", CostStatus: "unavailable", At: now, Days: map[string]usage.Totals{now.Format(usage.DayFormat): {Input: g.Execution.Policy.Limits.Tokens}}}); err != nil {
		t.Fatal(err)
	}
	if !g.UsageExceeded() {
		t.Fatal("usage did not exhaust goal budget")
	}
	if err := g.ReserveRun("r2", "coordinator", "replan", now); err == nil {
		t.Fatal("exhausted goal reserved another paid call")
	}
	if got := g.UsageCoverage(); got.Runs != 1 || got.Partial != 1 || got.CostUnavailable != 1 {
		t.Fatalf("coverage hid unknown cost: %+v", got)
	}
}

func TestGoalFractionalUsageHasStablePersistence(t *testing.T) {
	g := executable(t)
	now := time.Now().UTC()
	for _, id := range []string{"r1", "r2", "r3"} {
		if err := g.ReserveRun(id, "reviewer", id, now); err != nil {
			t.Fatal(err)
		}
		o := usage.Observation{Agent: "claude", Status: "partial", CostStatus: "estimate", At: now, Days: map[string]usage.Totals{}}
		for i, cost := range []float64{0.1, 0.2, 0.3, 10.0, 0.00001} {
			o.Days[now.AddDate(0, 0, -i).Format(usage.DayFormat)] = usage.Totals{Input: 100, CostUSD: cost}
		}
		if err := g.ObserveUsage(id, o); err != nil {
			t.Fatal(err)
		}
	}
	store, err := Open(filepath.Join(t.TempDir(), "goals.json"))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 30; i++ {
		g, err = store.Save(g)
		if err != nil {
			t.Fatal(err)
		}
	}
}
