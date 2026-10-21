package goal

import (
	"errors"
	"fmt"
	"maps"
	"math"
	"slices"
)

func (g Goal) validateExecution() error {
	e := g.Execution
	if e == nil {
		if !slices.Contains([]string{"draft", "provisioning", "planned"}, g.State) {
			return errors.New("execution state has no authorization record")
		}
		return nil
	}
	if !slices.Contains([]string{"preparing", "running", "verifying", "ready", "accepted", "paused", "blocked", "failed", "canceled"}, g.State) {
		return errors.New("invalid goal execution state")
	}
	digest, err := e.Policy.Digest()
	if err != nil {
		return err
	}
	if digest != e.PolicyDigest {
		return errors.New("execution policy digest mismatch")
	}
	if e.StartedAt.IsZero() || !e.Deadline.After(e.StartedAt) || e.Tasks == nil || e.Attempts == nil || e.Runs == nil || e.Integrations == nil {
		return errors.New("execution journal is incomplete")
	}
	if e.Initialized {
		if err := validateManifest(e.Policy, e.Manifest); err != nil {
			return err
		}
	} else {
		allowedRuns := 0
		if g.PlanningID != "" {
			allowedRuns = 1 // the accepted proposal predates workspace preparation
		}
		if len(e.Manifest) != 0 || len(e.Attempts) != 0 || len(e.Runs) != allowedRuns || !slices.Contains([]string{"preparing", "paused", "blocked", "failed", "canceled"}, g.State) {
			return errors.New("uninitialized execution has work or active state")
		}
	}
	if g.PlanningID != "" {
		r, ok := e.Runs[g.PlanningID]
		if !ok || r.Kind != "coordinator" || r.Subject != g.ID {
			return errors.New("accepted proposal is missing from the agent-run budget")
		}
	}
	if len(e.Tasks) != len(g.Tasks) || len(g.Tasks) > e.Policy.Limits.MaxTasks || len(e.Runs) > e.Policy.Limits.AgentRuns {
		return errors.New("execution journal exceeds accepted task/run limits")
	}
	if e.ObservedTokens < 0 || e.ObservedSpendUSD < 0 || math.IsNaN(e.ObservedSpendUSD) || math.IsInf(e.ObservedSpendUSD, 0) {
		return errors.New("invalid execution usage")
	}
	owned := map[string]bool{}
	for _, task := range g.Tasks {
		if _, err := TaskScopes(e.Policy, task); err != nil {
			return err
		}
		p, ok := e.Tasks[task.ID]
		if !ok || !slices.Contains([]string{"pending", "active", "integrated", "blocked", "superseded"}, p.State) || len(p.AttemptIDs) > e.Policy.Limits.AttemptsPerTask {
			return errors.New("invalid task execution state")
		}
		for _, id := range p.AttemptIDs {
			a, ok := e.Attempts[id]
			if !ok || a.TaskID != task.ID || owned[id] {
				return errors.New("attempt ownership mismatch")
			}
			owned[id] = true
		}
		if p.State == "integrated" {
			i, ok := e.Integrations[p.IntegrationID]
			if !ok || i.State != "complete" || e.Attempts[i.AttemptID].TaskID != task.ID {
				return errors.New("integrated task has no complete receipt")
			}
		}
	}
	if len(owned) != len(e.Attempts) {
		return errors.New("attempt not owned by any task")
	}
	sessions := map[string]bool{}
	for id, a := range e.Attempts {
		if _, err := g.AttemptScopes(a); err != nil {
			return err
		}
		if id != a.ID || id == "" || a.SessionID == "" || sessions[a.SessionID] || !slices.Contains([]string{"reserved", "running", "submitted", "verifying", "integrated", "failed", "stopped"}, a.State) || a.ReservedAt.IsZero() || !a.Deadline.After(a.ReservedAt) || a.Deadline.After(e.Deadline) {
			return errors.New("invalid attempt record")
		}
		sessions[a.SessionID] = true
		if err := validateManifest(e.Policy, a.Base); err != nil {
			return err
		}
		if _, err := taskMembers(e.Policy, Task{TaskDraft: TaskDraft{Apps: a.Members}}); err != nil {
			return err
		}
		run, ok := e.Runs[id]
		if !ok || run.Kind != "worker" || run.Subject != a.TaskID {
			return errors.New("attempt has no reserved agent budget")
		}
	}
	var runTokens int64
	var runSpend float64
	for _, id := range slices.Sorted(maps.Keys(e.Runs)) {
		r := e.Runs[id]
		if r.Usage != nil {
			if err := validateUsage(*r.Usage); err != nil {
				return err
			}
			totals := usageTotals(r.Usage.Days)
			if r.Tokens < totals.Tokens() || r.SpendUSD < totals.CostUSD || r.Usage.At.Before(r.ReservedAt) || e.UsageUpdatedAt.Before(r.Usage.At) {
				return errors.New("usage summary differs from observed counters")
			}
		}
		if id != r.ID || id == "" || r.Subject == "" || r.ReservedAt.IsZero() || !slices.Contains([]string{"coordinator", "worker", "reviewer"}, r.Kind) || r.Tokens < 0 || r.Tokens > 1e15 || r.SpendUSD > 1e12 || r.SpendUSD < 0 || math.IsNaN(r.SpendUSD) || math.IsInf(r.SpendUSD, 0) {
			return errors.New("invalid agent-run record")
		}
		runTokens += r.Tokens
		runSpend += r.SpendUSD
	}
	if e.ObservedTokens < runTokens || e.ObservedSpendUSD < runSpend {
		return errors.New("goal totals omit observed run usage")
	}
	if err := g.validateReplans(); err != nil {
		return err
	}
	if err := g.validateRefreshes(); err != nil {
		return err
	}
	for id, i := range e.Integrations {
		if id != i.ID || id == "" || i.CreatedAt.IsZero() || !slices.Contains([]string{"reserved", "complete", "failed"}, i.State) {
			return errors.New("invalid integration record")
		}
		a, ok := e.Attempts[i.AttemptID]
		if !ok {
			return errors.New("integration has no attempt")
		}
		if err := validateManifest(e.Policy, i.Base); err != nil {
			return err
		}
		if i.State == "complete" {
			if i.Receipt == "" || i.CompletedAt.IsZero() || a.State != "integrated" {
				return errors.New("complete integration lacks receipt or attempt acknowledgment")
			}
			if err := validateManifest(e.Policy, i.Manifest); err != nil {
				return err
			}
		}
	}
	if id := e.PendingIntegration; id != "" {
		i, ok := e.Integrations[id]
		if !ok || i.State != "reserved" {
			return fmt.Errorf("pending integration %s is not reserved", id)
		}
	}
	if f := e.Final; f != nil {
		if f.ID == "" || f.CreatedAt.IsZero() || !maps.Equal(f.Manifest, e.Manifest) || !maps.Equal(f.Targets, e.Targets) || e.PendingIntegration != "" || e.PendingReplan != "" || e.PendingRefresh != "" {
			return errors.New("invalid final verification record")
		}
		for id := range e.Tasks {
			if !g.TaskSatisfied(id) {
				return errors.New("final verification has unintegrated tasks")
			}
		}
		if (f.Receipt == "") != f.CompletedAt.IsZero() {
			return errors.New("final verification completion lacks evidence")
		}
	}
	if len(e.FinalHistory) > 0 && e.Final == nil {
		return errors.New("final retry history lacks current verification")
	}
	if len(e.FinalHistory)+1 > e.Policy.Limits.ReviewRounds {
		return errors.New("final verification round budget exceeded")
	}
	finalIDs := map[string]bool{}
	if e.Final != nil {
		finalIDs[e.Final.ID] = true
	}
	for _, f := range e.FinalHistory {
		if f.ID == "" || finalIDs[f.ID] || f.CreatedAt.IsZero() || f.Receipt != "" || !f.CompletedAt.IsZero() || !maps.Equal(f.Manifest, e.Manifest) || !maps.Equal(f.Targets, e.Targets) {
			return errors.New("invalid final verification history")
		}
		finalIDs[f.ID] = true
	}
	if (g.State == "ready" || g.State == "accepted") && (e.Final == nil || e.Final.Receipt == "") {
		return errors.New("ready goal has no final verification")
	}
	if e.Delivery != nil {
		all, err := g.validateDelivery(*e.Delivery)
		if err != nil {
			return err
		}
		if e.Delivery.State == "merged" && !all {
			return errors.New("delivery marked merged without all target observations")
		}
	}
	if e.DeliveryEvidenceURL != "" {
		if err := ValidateEvidenceURL(e.DeliveryEvidenceURL); err != nil {
			return err
		}
	}
	return nil
}
