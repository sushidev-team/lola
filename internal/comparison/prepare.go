package comparison

import (
	"encoding/json"
	"errors"
	"fmt"
)

// Template is deliberately an unfilled plan, not synthetic evaluation evidence.
// Fill its task, bases, criteria and limits before Prepare will accept it.
func Template() Bundle {
	return Bundle{Version: Version, ID: "comparison-id", Question: "Does the selected lesson or reviewer reduce repeated corrections at the same budget?", Pairs: []Pair{{ID: "task-1-repeat-1", Task: "", Acceptance: []string{}, Repositories: []Repository{{Name: "", Base: ""}}, Limits: Limits{}, Intervention: ""}}, Runs: []Run{}}
}

// Prepare freezes each predeclared task/budget into two editable run records.
// Drafts cannot produce a report until real times, outcomes and evidence exist.
// It never overwrites existing observations or invents execution evidence.
func Prepare(plan Bundle) (Bundle, error) {
	if len(plan.Runs) > 0 {
		return Bundle{}, errors.New("prepare requires an unrun plan; retain existing records instead of replacing them")
	}
	if err := Validate(plan); err != nil {
		return Bundle{}, err
	}
	// Deep copy through JSON keeps the caller's plan immutable.
	raw, err := json.Marshal(plan)
	if err != nil {
		return Bundle{}, err
	}
	out, err := Decode(raw)
	if err != nil {
		return Bundle{}, err
	}
	unavailable := func(reason string) Measurement {
		return Measurement{Status: "unavailable", Reason: reason, Evidence: []string{}}
	}
	for i, p := range out.Pairs {
		for _, arm := range []string{"baseline", "treatment"} {
			metrics := map[string]Measurement{}
			for _, name := range Metrics {
				metrics[name] = unavailable("Not recorded; retain an explicit reason if measurement remains unavailable.")
			}
			out.Runs = append(out.Runs, Run{ID: fmt.Sprintf("pair-%d-%s", i+1, arm), PairID: p.ID, Arm: arm, PairSHA256: PairDigest(p), OutcomeEvidence: []string{}, Worker: Invocation{UnknownReason: "Record the worker invocation from retained logs; do not infer a resolved model from a provider alias."}, Lessons: []Lesson{}, Reviewers: []Reviewer{}, AgentCalls: unavailable("Run usage has not been recorded."), Tokens: unavailable("Run usage has not been recorded."), Metrics: metrics})
		}
	}
	raw, err = json.Marshal(out)
	if err != nil {
		return Bundle{}, err
	}
	if len(raw) > MaxBytes {
		return Bundle{}, errors.New("prepared records exceed 512 KiB; use smaller predeclared comparison cohorts")
	}
	return out, nil
}
