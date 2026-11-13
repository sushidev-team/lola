package goal

import (
	"errors"
	"reflect"
	"slices"
	"time"
)

// WorkflowTransition projects a verified lifecycle fact onto a configured
// Linear state. It is never evidence that a task was executed or delivered.
type WorkflowTransition struct {
	TaskID      string    `json:"task_id"`
	Stage       string    `json:"stage"`
	From        string    `json:"from"`
	To          string    `json:"to"`
	ReservedAt  time.Time `json:"reserved_at"`
	SentAt      time.Time `json:"sent_at,omitempty"`
	ConfirmedAt time.Time `json:"confirmed_at,omitempty"`
}

func WorkflowRank(stage string) int {
	switch stage {
	case "started":
		return 1
	case "integrated":
		return 2
	case "delivered":
		return 3
	}
	return 0
}
func (g Goal) WorkflowStage(taskID string) string {
	e := g.Execution
	if e == nil || g.SyncError != "" || g.PendingBrief != "" || !slices.Contains([]string{"running", "verifying", "ready", "accepted"}, g.State) {
		return ""
	}
	p, ok := e.Tasks[taskID]
	if !ok || len(p.ReplacedBy) > 0 || p.State == "superseded" {
		return ""
	}
	if p.State == "integrated" {
		if g.State == "accepted" || g.Delivered() {
			return "delivered"
		}
		return "integrated"
	}
	for _, id := range p.AttemptIDs {
		if a := e.Attempts[id]; a.State == "running" || a.State == "submitted" || a.State == "verifying" {
			return "started"
		}
	}
	return ""
}
func (g Goal) LastWorkflow(taskID string) (WorkflowTransition, bool) {
	for i := len(g.Workflow) - 1; i >= 0; i-- {
		if g.Workflow[i].TaskID == taskID {
			return g.Workflow[i], true
		}
	}
	return WorkflowTransition{}, false
}
func (g *Goal) ReserveWorkflow(taskID, stage, from, to string, now time.Time) error {
	if g.WorkflowStage(taskID) != stage || from == "" || to == "" || now.IsZero() {
		return errors.New("workflow transition lacks a current lifecycle fact or states")
	}
	if last, ok := g.LastWorkflow(taskID); ok {
		if last.ConfirmedAt.IsZero() || WorkflowRank(last.Stage) >= WorkflowRank(stage) {
			return errors.New("workflow transition is pending or already recorded")
		}
	}
	g.Workflow = append(g.Workflow, WorkflowTransition{TaskID: taskID, Stage: stage, From: from, To: to, ReservedAt: now})
	return nil
}
func (g Goal) validateWorkflow() error {
	if len(g.Workflow) > 3*len(g.Tasks) || len(g.WorkflowError) > 2000 {
		return errors.New("workflow history exceeds bounds")
	}
	tasks := map[string]bool{}
	for _, t := range g.Tasks {
		tasks[t.ID] = true
	}
	last := map[string]WorkflowTransition{}
	for _, w := range g.Workflow {
		if g.Execution == nil || !tasks[w.TaskID] || WorkflowRank(w.Stage) == 0 || w.From == "" || w.To == "" || w.ReservedAt.IsZero() || (!w.SentAt.IsZero() && w.SentAt.Before(w.ReservedAt)) || (!w.ConfirmedAt.IsZero() && w.ConfirmedAt.Before(w.ReservedAt)) {
			return errors.New("invalid workflow history")
		}
		if prior, ok := last[w.TaskID]; ok && (prior.ConfirmedAt.IsZero() || WorkflowRank(prior.Stage) >= WorkflowRank(w.Stage)) {
			return errors.New("workflow phases regressed or abandoned an uncertain write")
		}
		last[w.TaskID] = w
	}
	return nil
}
func preserveWorkflow(old, g Goal) error {
	if len(g.Workflow) < len(old.Workflow) || len(g.Workflow) > len(old.Workflow)+1 {
		return errors.New("workflow history must be retained")
	}
	if len(g.Workflow) > len(old.Workflow) {
		w := g.Workflow[len(old.Workflow)]
		if old.WorkflowStage(w.TaskID) != w.Stage {
			return errors.New("workflow intent lacks an authoritative lifecycle fact")
		}
	}
	for i, a := range old.Workflow {
		b := g.Workflow[i]
		if a.TaskID != b.TaskID || a.Stage != b.Stage || a.From != b.From || a.To != b.To || a.ReservedAt != b.ReservedAt || (!a.SentAt.IsZero() && a.SentAt != b.SentAt) || (!a.ConfirmedAt.IsZero() && !reflect.DeepEqual(a, b)) {
			return errors.New("workflow intent or confirmed history changed")
		}
	}
	return nil
}
