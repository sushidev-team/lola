package comparison

import (
	"encoding/json"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"
)

func measured(v float64) Measurement {
	return Measurement{Value: &v, Status: "observed", Evidence: []string{"fixture://retained-observation"}}
}
func fixture() Bundle {
	p := Pair{ID: "dialog-repeat-1", Task: "Preserve focus after dismissing a dialog", Acceptance: []string{"Escape returns focus to the original trigger."}, Repositories: []Repository{{Name: "example/ui", Base: strings.Repeat("a", 40)}}, Limits: Limits{AgentCalls: 8, RuntimeSeconds: 1800, Tokens: 100000}, Intervention: "One reviewed dialog-focus lesson in the treatment arm only."}
	b := Bundle{Version: Version, ID: "synthetic-regression-fixture", Question: "Fixture data only; not evidence of product improvement.", Pairs: []Pair{p}}
	for _, arm := range []string{"baseline", "treatment"} {
		r := Run{ID: arm + "-run", PairID: p.ID, Arm: arm, PairSHA256: PairDigest(p), Outcome: "accepted", OutcomeEvidence: []string{"fixture://acceptance"}, StartedAt: time.Date(2026, 10, 22, 10, 0, 0, 0, time.UTC), EndedAt: time.Date(2026, 10, 22, 10, 10, 0, 0, time.UTC), Worker: Invocation{Agent: "codex", RequestedModel: "fixture-model", ResolvedModel: "fixture-resolved", PromptSHA256: strings.Repeat("b", 64), Source: "fixture://worker-prompt"}, AgentCalls: measured(4), Tokens: measured(10000), Metrics: map[string]Measurement{}}
		for _, name := range Metrics {
			r.Metrics[name] = measured(0)
		}
		r.Metrics["human_intervention_seconds"] = measured(120)
		r.Metrics["cost_usd"] = measured(1)
		r.Metrics["repeat_corrections"] = measured(2)
		if arm == "treatment" {
			r.Metrics["repeat_corrections"] = measured(0)
			r.Metrics["human_intervention_seconds"] = measured(30)
			r.Metrics["cost_usd"] = measured(2)
			r.Lessons = []Lesson{{ID: "dialog-focus", Version: 2, SHA256: strings.Repeat("c", 64), Source: "fixture://lesson-v2"}}
		}
		b.Runs = append(b.Runs, r)
	}
	return b
}
func metric(r Report, name string) MetricResult {
	for _, m := range r.Metrics {
		if m.Name == name {
			return m
		}
	}
	panic(name)
}
func TestPairedReportIsReproducibleAndRetainsOutcomeDenominators(t *testing.T) {
	b := fixture()
	before, _ := json.Marshal(b)
	r, err := Generate(b)
	if err != nil {
		t.Fatal(err)
	}
	r2, err := Generate(b)
	if err != nil || !reflect.DeepEqual(r, r2) {
		t.Fatal("nondeterministic", err)
	}
	after, _ := json.Marshal(b)
	if string(before) != string(after) {
		t.Fatal("source mutated")
	}
	if r.PlannedPairs != 1 || r.MatchedBudgetPairs != 1 || r.Baseline.Accepted != 1 || r.Treatment.Accepted != 1 {
		t.Fatal(r)
	}
	if *metric(r, "repeat_corrections").Difference != -2 || *metric(r, "human_intervention_seconds").Difference != -90 || *r.Treatment.CostPerAccepted != 2 {
		t.Fatal(r)
	}
	if !strings.Contains(r.Markdown, r.BundleSHA256) || !strings.Contains(r.Markdown, "do not establish causation") {
		t.Fatal("missing provenance/limits")
	}
	// Costs include every run in matched pairs, including rejected work. Two
	// accepted treatment tasks cost 2+2; one rejected baseline still costs 1.
	p := b.Pairs[0]
	p.ID = "dialog-repeat-2"
	b.Pairs = append(b.Pairs, p)
	for _, old := range append([]Run{}, b.Runs...) {
		old.ID += "-2"
		old.PairID = p.ID
		old.PairSHA256 = PairDigest(p)
		if old.Arm == "baseline" {
			old.Outcome = "rejected"
		}
		b.Runs = append(b.Runs, old)
	}
	r, err = Generate(b)
	if err != nil {
		t.Fatal(err)
	}
	if r.Baseline.Accepted != 1 || r.Baseline.Rejected != 1 || *r.Baseline.CostPerAccepted != 2 || *r.Treatment.CostPerAccepted != 2 {
		t.Fatal("failed run costs omitted", r)
	}
}
func TestUnavailableNeverBecomesZeroOrAnImprovement(t *testing.T) {
	b := fixture()
	b.Runs[1].Metrics["human_intervention_seconds"] = Measurement{Status: "unavailable", Reason: "Operator time was not tracked."}
	b.Runs[1].Metrics["cost_usd"] = Measurement{Status: "unavailable", Reason: "Provider does not expose actual cost."}
	r, err := Generate(b)
	if err != nil {
		t.Fatal(err)
	}
	m := metric(r, "human_intervention_seconds")
	if m.Treatment.Total != nil || m.Difference != nil || m.Treatment.Unavailable != 1 || r.Treatment.CostPerAccepted != nil {
		t.Fatal("unknown converted to zero", r)
	}
	b.Runs[1].Metrics["cost_usd"] = measured(0)
	r, _ = Generate(b)
	if r.Treatment.CostPerAccepted == nil || *r.Treatment.CostPerAccepted != 0 {
		t.Fatal("measured zero lost")
	}
	b.Runs[1].Outcome = "incomplete"
	r, _ = Generate(b)
	if r.Treatment.Accepted != 0 || r.Treatment.Incomplete != 1 || r.Treatment.CostPerAccepted != nil {
		t.Fatal("zero denominator hidden")
	}
}
func TestMissingPairsAndBudgetOverrunsStayVisible(t *testing.T) {
	b := fixture()
	p := b.Pairs[0]
	p.ID = "missing-both"
	b.Pairs = append(b.Pairs, p)
	b.Runs = b.Runs[:1]
	r, err := Generate(b)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Pairs) != 2 || r.MatchedBudgetPairs != 0 || r.Baseline.Runs != 0 || len(r.Pairs[0].Limitations) != 1 || len(r.Pairs[1].Limitations) != 2 {
		t.Fatal(r)
	}
	for _, kind := range []string{"calls", "tokens", "runtime", "unknown", "estimated", "spend"} {
		t.Run(kind, func(t *testing.T) {
			b := fixture()
			switch kind {
			case "calls":
				b.Runs[1].AgentCalls = measured(9)
			case "tokens":
				b.Runs[1].Tokens = measured(100001)
			case "runtime":
				b.Runs[1].EndedAt = b.Runs[1].StartedAt.Add(31 * time.Minute)
			case "unknown":
				b.Runs[1].Tokens = Measurement{Status: "unavailable", Reason: "Log unavailable"}
			case "estimated":
				b.Runs[1].Tokens.Status = "estimated"
			case "spend":
				b.Pairs[0].Limits.SpendUSD = 1.5
				for i := range b.Runs {
					b.Runs[i].PairSHA256 = PairDigest(b.Pairs[0])
				}
			}
			r, err := Generate(b)
			if err != nil {
				t.Fatal(err)
			}
			if r.MatchedBudgetPairs != 0 || r.Pairs[0].MatchedBudget || len(r.Pairs[0].Limitations) == 0 {
				t.Fatal("budget problem hidden", r)
			}
		})
	}
}
func TestProvenanceAndAssessmentQualityRemainExplicit(t *testing.T) {
	b := fixture()
	b.Runs[1].Worker.RequestedModel = "different-model"
	b.Runs[1].Worker.ResolvedModel = ""
	b.Runs[1].Worker.PromptSHA256 = strings.Repeat("d", 64)
	b.Runs[1].Worker.UnknownReason = "Provider alias; resolved model not exposed."
	cost := b.Runs[1].Metrics["cost_usd"]
	cost.Status = "estimated"
	b.Runs[1].Metrics["cost_usd"] = cost
	useful := b.Runs[1].Metrics["useful_findings"]
	useful.Status = "reported"
	b.Runs[1].Metrics["useful_findings"] = useful
	r, err := Generate(b)
	if err != nil {
		t.Fatal(err)
	}
	warnings := strings.Join(r.Pairs[0].Limitations, " ")
	for _, s := range []string{"model/agent differs", "prompt differs", "incomplete"} {
		if !strings.Contains(warnings, s) {
			t.Fatal(warnings)
		}
	}
	if metric(r, "cost_usd").Treatment.Estimated != 1 || metric(r, "useful_findings").Treatment.Reported != 1 {
		t.Fatal(r)
	}
	b = fixture()
	b.Runs[1].Lessons = []Lesson{}
	r, _ = Generate(b)
	if !strings.Contains(strings.Join(r.Pairs[0].Limitations, " "), "no recorded lesson/reviewer difference") {
		t.Fatal("nil vs empty invented intervention")
	}
}
func TestRunContractsAndStrictValidation(t *testing.T) {
	tests := map[string]func(*Bundle){
		"version": func(b *Bundle) { b.Version++ }, "duplicate-pair": func(b *Bundle) { b.Pairs = append(b.Pairs, b.Pairs[0]) }, "duplicate-slot": func(b *Bundle) { r := b.Runs[0]; r.ID = "different"; b.Runs = append(b.Runs, r) }, "foreign-pair": func(b *Bundle) { b.Runs[0].PairID = "foreign" }, "budget-changed": func(b *Bundle) { b.Pairs[0].Limits.AgentCalls++ }, "base-changed": func(b *Bundle) { b.Pairs[0].Repositories[0].Base = strings.Repeat("f", 40) }, "criteria-changed": func(b *Bundle) { b.Pairs[0].Acceptance[0] = "New criterion" }, "negative": func(b *Bundle) { b.Runs[0].Tokens = measured(-1) }, "fraction-count": func(b *Bundle) { b.Runs[0].AgentCalls = measured(1.5) }, "nan": func(b *Bundle) { b.Runs[0].Metrics["cost_usd"] = measured(math.NaN()) }, "unknown-zero": func(b *Bundle) {
			b.Runs[0].Metrics["cost_usd"] = Measurement{Value: new(float64), Status: "unavailable", Reason: "Unknown"}
		}, "missing-metric": func(b *Bundle) { delete(b.Runs[0].Metrics, "cost_usd") }, "no-evidence": func(b *Bundle) { m := measured(1); m.Evidence = nil; b.Runs[0].Metrics["regressions_caught"] = m }, "unknown-model-no-reason": func(b *Bundle) { b.Runs[0].Worker.ResolvedModel = "" }, "lesson-version": func(b *Bundle) { b.Runs[1].Lessons[0].Version = 0 }, "invalid-interval": func(b *Bundle) { b.Runs[0].EndedAt = b.Runs[0].StartedAt.Add(-time.Second) },
	}
	for name, change := range tests {
		t.Run(name, func(t *testing.T) {
			b := fixture()
			change(&b)
			if _, err := Generate(b); err == nil {
				t.Fatal("invalid record accepted")
			}
		})
	}
	raw, _ := json.Marshal(fixture())
	for _, bad := range [][]byte{append(raw, []byte(` {}`)...), []byte(strings.Replace(string(raw), `"version":1`, `"version":1,"invented":true`, 1)), []byte(strings.Repeat(" ", MaxBytes+1))} {
		if _, err := Decode(bad); err == nil {
			t.Fatal("invalid input accepted")
		}
	}
}
func TestPrepareNeverInventsResultsAndDoesNotOverwriteRuns(t *testing.T) {
	if _, err := Prepare(Template()); err == nil {
		t.Fatal("unfilled plan accepted")
	}
	plan := fixture()
	if _, err := Prepare(plan); err == nil {
		t.Fatal("replaced recorded observations")
	}
	plan.Runs = nil
	before, _ := json.Marshal(plan)
	draft, err := Prepare(plan)
	if err != nil {
		t.Fatal(err)
	}
	if len(draft.Runs) != 2 || draft.Runs[0].PairSHA256 != PairDigest(plan.Pairs[0]) || draft.Runs[1].Arm != "treatment" {
		t.Fatal(draft)
	}
	if _, err := Generate(draft); err == nil {
		t.Fatal("unfilled run drafts became evidence")
	}
	if draft.Runs[0].Metrics["cost_usd"].Value != nil {
		t.Fatal("invented zero")
	}
	after, _ := json.Marshal(plan)
	if string(before) != string(after) {
		t.Fatal("plan mutated")
	}
}
