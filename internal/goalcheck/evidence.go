package goalcheck

import (
	"errors"
	"fmt"
	"path/filepath"

	"github.com/sushidev-team/lola/internal/goalwork"
)

// Evidence re-reads persisted results without running anything. Every required
// check must describe the exact accepted command, image, policy, operation and
// full repository combination. A caller cannot substitute a passed check from
// another attempt or omit an unaffected member of a stack.
func (r *Runner) Evidence(goal, operation, policy string, manifest map[string]goalwork.Revision, specs []Spec, refs map[string]string) ([]goalwork.Check, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.Workspaces == nil || len(specs) == 0 || len(refs) != len(specs) {
		return nil, errors.New("all accepted checks require durable evidence")
	}
	seen := map[string]bool{}
	var out []goalwork.Check
	for _, spec := range specs {
		if err := spec.Validate(); err != nil {
			return nil, err
		}
		if seen[spec.ID] {
			return nil, errors.New("duplicate acceptance check")
		}
		seen[spec.ID] = true
		id, ok := refs[spec.ID]
		if !ok {
			return nil, fmt.Errorf("missing check evidence: %s", spec.ID)
		}
		dir, err := r.Workspaces.ValidationDirectory(goal, id)
		if err != nil {
			return nil, err
		}
		path := filepath.Join(dir, "check.json")
		receipt, err := readReceipt(path)
		if err != nil {
			return nil, err
		}
		want := Receipt{ID: id, Goal: goal, Operation: operation, Policy: policy, Spec: spec, Manifest: manifest}
		if !sameInputs(receipt, want) || receipt.State != "pass" || !receipt.Stopped || receipt.StartedAt.IsZero() || receipt.FinishedAt.Before(receipt.StartedAt) {
			return nil, fmt.Errorf("check %s is incomplete or describes different verification inputs", spec.ID)
		}
		if err := r.validateEvidence(dir, receipt); err != nil {
			return nil, err
		}
		out = append(out, goalwork.Check{ID: spec.ID, State: "pass", Artifact: path})
	}
	return out, nil
}

func (r *Runner) validateEvidence(dir string, receipt Receipt) error {
	if len(receipt.Artifacts) != len(receipt.Spec.Artifacts) {
		return errors.New("check receipt has missing artifact evidence")
	}
	for i, a := range receipt.Artifacts {
		spec := receipt.Spec.Artifacts[i]
		if a.ID != spec.ID || a.Kind != spec.Kind {
			return errors.New("check artifact identity changed")
		}
	}
	return validateArtifacts(dir, receipt.Artifacts)
}
