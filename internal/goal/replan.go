package goal

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"time"
)

type Replacement struct {
	TaskDraft
	Covers []int `json:"covers"` // indexes in the original task acceptance, never model-authored criteria
}
type Replan struct {
	ID          string            `json:"id"`
	TaskID      string            `json:"task_id"`
	AttemptID   string            `json:"attempt_id"`
	Base        map[string]string `json:"base"`
	State       string            `json:"state"` // reserved, provisioning, complete, failed
	Children    []string          `json:"children,omitempty"`
	Coverage    [][]int           `json:"coverage,omitempty"`
	CreatedAt   time.Time         `json:"created_at"`
	CompletedAt time.Time         `json:"completed_at,omitempty"`
}

func (g Goal) ExecutionReplan() (Replan, bool) {
	if g.Execution == nil {
		return Replan{}, false
	}
	r, ok := g.Execution.Replans[g.Execution.PendingReplan]
	return r, ok && r.State == "provisioning"
}

// ReplanTask selects only conclusive, independently rejected work. A vanished
// agent, unknown check, permission question or canceled task is not this signal.
func (g Goal) ReplanTask() string {
	e := g.Execution
	if e == nil || !e.Policy.Replan || e.PendingReplan != "" || e.PendingIntegration != "" || !e.StopWorkersAt.IsZero() || len(g.Tasks)+2 > e.Policy.Limits.MaxTasks || len(e.Runs) >= e.Policy.Limits.AgentRuns {
		return ""
	}
	for _, a := range e.Attempts {
		if activeAttempt(a.State) {
			return ""
		}
	}
	for _, task := range g.Tasks {
		p := e.Tasks[task.ID]
		if p.State != "pending" || len(p.AttemptIDs) == 0 {
			continue
		}
		last := p.AttemptIDs[len(p.AttemptIDs)-1]
		a := e.Attempts[last]
		if a.State != "failed" || a.Error == "" {
			continue
		}
		used := false
		for _, r := range e.Replans {
			if r.AttemptID == last {
				used = true
			}
		}
		if used {
			continue
		}
		for _, i := range e.Integrations {
			if i.AttemptID == last && i.State == "failed" {
				return task.ID
			}
		}
	}
	return ""
}

func (g *Goal) ReserveReplan(task, id string, now time.Time) error {
	if err := g.canAdvance(now); err != nil {
		return err
	}
	if id == "" || g.ReplanTask() != task {
		return errors.New("no eligible conclusive failure for bounded replanning")
	}
	e := g.Execution
	p := e.Tasks[task]
	if err := g.ReserveRun(id, "coordinator", task, now); err != nil {
		return err
	}
	if e.Replans == nil {
		e.Replans = map[string]Replan{}
	}
	e.Replans[id] = Replan{ID: id, TaskID: task, AttemptID: p.AttemptIDs[len(p.AttemptIDs)-1], Base: maps.Clone(e.Manifest), State: "reserved", CreatedAt: now}
	e.PendingReplan = id
	return nil
}

// Decompose appends new identities and preserves the original contract and all
// attempts. Each child inherits the exact original criteria it claims to cover.
// Dependencies on the old task remain held until ALL replacements integrate.
func (g *Goal) Decompose(id string, children []Replacement, now time.Time) error {
	if err := g.canAdvance(now); err != nil {
		return err
	}
	e := g.Execution
	r, ok := e.Replans[id]
	if !ok || e.PendingReplan != id || r.State != "reserved" || !maps.Equal(r.Base, e.Manifest) || e.PendingIntegration != "" {
		return errors.New("replan no longer matches the reserved repository combination")
	}
	var parent Task
	for _, t := range g.Tasks {
		if t.ID == r.TaskID {
			parent = t
		}
	}
	if parent.ID == "" || e.Tasks[parent.ID].State != "pending" {
		return errors.New("replan task is not pending")
	}
	if len(children) < 2 || len(children) > 10 || len(g.Tasks)+len(children) > e.Policy.Limits.MaxTasks {
		return errors.New("decomposition requires 2–10 children within the total retained-task limit")
	}
	parentScopes, err := TaskScopes(e.Policy, parent)
	if err != nil {
		return err
	}
	parentApps, err := taskMembers(e.Policy, parent)
	if err != nil {
		return err
	}
	covered := map[int]bool{}
	var drafts []TaskDraft
	for _, c := range children {
		if c.ExistingIssueID != "" {
			return errors.New("autonomous replanning cannot adopt existing issues")
		}
	}
	for i, c := range children {
		if len(c.Paths) == 0 || len(c.Covers) == 0 || len(c.Covers) > len(parent.Acceptance) {
			return errors.New("replacement needs explicit ownership and original acceptance coverage")
		}
		if !sameStrings(c.Apps, parentApps) || !sameStrings(c.LabelIDs, parent.LabelIDs) {
			return errors.New("replacement cannot change the original repository or label routing")
		}
		scopes, err := TaskScopes(e.Policy, Task{TaskDraft: c.TaskDraft})
		if err != nil {
			return err
		}
		for member, scope := range scopes {
			allowed, ok := parentScopes[member]
			if !ok || !scope.Within(allowed) {
				return errors.New("replacement expands original task ownership")
			}
		}
		seen := map[int]bool{}
		for _, n := range c.Covers {
			if n < 1 || n > len(parent.Acceptance) || seen[n] {
				return errors.New("invalid replacement coverage")
			}
			seen[n] = true
			covered[n] = true
			if !slices.Contains(c.Acceptance, parent.Acceptance[n-1]) {
				c.Acceptance = append(slices.Clone(c.Acceptance), parent.Acceptance[n-1])
			}
		}
		// Relative child dependencies are checked before conversion to global
		// indexes. The parent's prerequisites are then inherited by every child.
		for _, dep := range c.BlockedBy {
			if dep < 1 || dep > i {
				return errors.New("replacement dependencies must point to earlier children")
			}
		}
		drafts = append(drafts, c.TaskDraft)
	}
	if len(covered) != len(parent.Acceptance) {
		return errors.New("decomposition omits original acceptance criteria")
	}
	if err := ValidateDraft(Draft{Project: g.Project, Title: g.Title, Outcome: g.Outcome, Acceptance: g.Acceptance, NonGoals: g.NonGoals, Tasks: drafts}); err != nil {
		return err
	}
	offset := len(g.Tasks)
	for i, draft := range drafts {
		deps := slices.Clone(parent.BlockedBy)
		for _, dep := range draft.BlockedBy {
			deps = append(deps, offset+dep)
		}
		slices.Sort(deps)
		draft.BlockedBy = slices.Compact(deps)
		child := Task{ID: NewID(), TaskDraft: draft, Issue: Binding{ID: NewID()}, DecomposedFrom: parent.ID}
		for range child.BlockedBy {
			child.Relations = append(child.Relations, Binding{ID: NewID()})
		}
		g.Tasks = append(g.Tasks, child)
		e.Tasks[child.ID] = TaskProgress{State: "pending"}
		r.Children = append(r.Children, child.ID)
		r.Coverage = append(r.Coverage, slices.Clone(children[i].Covers))
	}
	p := e.Tasks[parent.ID]
	p.State = "superseded"
	p.ReplacedBy = slices.Clone(r.Children)
	e.Tasks[parent.ID] = p
	r.State = "provisioning"
	e.Replans[id] = r
	return nil
}

func (g *Goal) CompleteReplan(id string, now time.Time) error {
	if err := g.canAdvance(now); err != nil {
		return err
	}
	e := g.Execution
	r, ok := e.Replans[id]
	if !ok || e.PendingReplan != id || r.State != "provisioning" {
		return errors.New("replan is not publishing its reserved children")
	}
	for _, child := range r.Children {
		found := false
		for _, t := range g.Tasks {
			if t.ID != child {
				continue
			}
			found = true
			if !t.Issue.Confirmed {
				return errors.New("replacement issue is unconfirmed")
			}
			for _, b := range t.Relations {
				if !b.Confirmed {
					return errors.New("replacement dependency is unconfirmed")
				}
			}
		}
		if !found {
			return errors.New("replacement task missing")
		}
	}
	r.State = "complete"
	r.CompletedAt = now
	e.Replans[id] = r
	e.PendingReplan = ""
	return nil
}

func (g Goal) TaskSatisfied(id string) bool { return g.taskSatisfied(id, map[string]bool{}) }
func (g Goal) taskSatisfied(id string, visiting map[string]bool) bool {
	if g.Execution == nil || visiting[id] {
		return false
	}
	p, ok := g.Execution.Tasks[id]
	if !ok {
		return false
	}
	if p.State == "integrated" {
		return true
	}
	if p.State != "superseded" || len(p.ReplacedBy) == 0 {
		return false
	}
	visiting[id] = true
	defer delete(visiting, id)
	for _, child := range p.ReplacedBy {
		if !g.taskSatisfied(child, visiting) {
			return false
		}
	}
	return true
}
func (g Goal) AllTasksSatisfied() bool {
	if g.Execution == nil || g.Execution.PendingReplan != "" {
		return false
	}
	for _, t := range g.Tasks {
		if !g.TaskSatisfied(t.ID) {
			return false
		}
	}
	return true
}
func sameStrings(a, b []string) bool {
	a = slices.Clone(a)
	b = slices.Clone(b)
	slices.Sort(a)
	slices.Sort(b)
	return slices.Equal(a, b)
}

func (g Goal) TaskContract(t Task) string {
	body := g.TaskDescription(t)
	seen := map[string]bool{t.ID: true}
	for t.DecomposedFrom != "" {
		id := t.DecomposedFrom
		if seen[id] {
			break
		}
		seen[id] = true
		found := false
		for _, parent := range g.Tasks {
			if parent.ID == id {
				body = "Original task contract (still required):\n" + g.TaskDescription(parent) + "\n" + body
				t = parent
				found = true
				break
			}
		}
		if !found {
			break
		}
	}
	return body
}

func (g *Goal) RetryReplan(id string, now time.Time) error {
	if g.Execution == nil || (g.State != "blocked" && g.State != "paused") {
		return errors.New("only a held coordinator run can be retried")
	}
	e := g.Execution
	old, ok := e.Replans[e.PendingReplan]
	if !ok || old.State != "reserved" {
		return errors.New("published replacement work must recover its existing replan")
	}
	copy := copyGoal(*g)
	copy.SyncError = ""
	copy.State = "running"
	copy.Execution.PendingReplan = ""
	copy.Execution.StopWorkersAt = time.Time{}
	r := copy.Execution.Replans[old.ID]
	r.State = "failed"
	r.CompletedAt = now
	copy.Execution.Replans[old.ID] = r
	// Explicit retry is a new charged run, not an automatic replay of an
	// ambiguous paid call. Retain the old evidence under its original identity.
	if err := copy.canAdvance(now); err != nil {
		return err
	}
	if err := copy.ReserveRun(id, "coordinator", old.TaskID, now); err != nil {
		return err
	}
	copy.Execution.Replans[id] = Replan{ID: id, TaskID: old.TaskID, AttemptID: old.AttemptID, Base: maps.Clone(e.Manifest), State: "reserved", CreatedAt: now}
	copy.Execution.PendingReplan = id
	*g = copy
	return nil
}

func (g Goal) validateReplans() error {
	e := g.Execution
	children := map[string]string{}
	for id, r := range e.Replans {
		if id == "" || r.ID != id || !e.Policy.Replan || r.CreatedAt.IsZero() || e.Runs[id].Kind != "coordinator" || e.Runs[id].Subject != r.TaskID || e.Attempts[r.AttemptID].TaskID != r.TaskID || e.Attempts[r.AttemptID].State != "failed" {
			return errors.New("invalid coordinator replan reservation")
		}
		if err := validateManifest(e.Policy, r.Base); err != nil {
			return err
		}
		if !slices.Contains([]string{"reserved", "provisioning", "complete", "failed"}, r.State) {
			return errors.New("invalid replan state")
		}
		if r.State == "reserved" || r.State == "failed" {
			if len(r.Children) > 0 {
				return errors.New("unapplied replan has children")
			}
			continue
		}
		if len(r.Children) < 2 || len(r.Children) != len(r.Coverage) || !slices.Equal(e.Tasks[r.TaskID].ReplacedBy, r.Children) {
			return errors.New("decomposition journal lost replacement ownership")
		}
		var parent Task
		parentIndex := -1
		for i, t := range g.Tasks {
			if t.ID == r.TaskID {
				parent = t
				parentIndex = i
			}
		}
		covered := map[int]bool{}
		parentScopes, err := TaskScopes(e.Policy, parent)
		if err != nil {
			return err
		}
		parentApps, err := taskMembers(e.Policy, parent)
		if err != nil {
			return err
		}
		for i, child := range r.Children {
			if children[child] != "" {
				return errors.New("replacement belongs to multiple replans")
			}
			children[child] = r.TaskID
			found := false
			for j, t := range g.Tasks {
				if t.ID != child {
					continue
				}
				found = true
				if j <= parentIndex || t.DecomposedFrom != parent.ID {
					return errors.New("replacement ancestry is invalid")
				}
				if !sameStrings(t.Apps, parentApps) || !sameStrings(t.LabelIDs, parent.LabelIDs) {
					return errors.New("replacement routing drift")
				}
				scopes, err := TaskScopes(e.Policy, t)
				if err != nil {
					return err
				}
				for id, scope := range scopes {
					if !scope.Within(parentScopes[id]) {
						return errors.New("replacement ownership exceeds original task")
					}
				}
				for _, dep := range parent.BlockedBy {
					if !slices.Contains(t.BlockedBy, dep) {
						return errors.New("replacement lost original prerequisite")
					}
				}
				for _, dep := range t.BlockedBy {
					if slices.Contains(parent.BlockedBy, dep) {
						continue
					}
					if dep < 1 || dep > len(g.Tasks) || !slices.Contains(r.Children[:i], g.Tasks[dep-1].ID) {
						return errors.New("replacement depends on unrelated or later work")
					}
				}
				if len(r.Coverage[i]) == 0 {
					return errors.New("replacement lacks coverage")
				}
				for _, n := range r.Coverage[i] {
					if n < 1 || n > len(parent.Acceptance) || !slices.Contains(t.Acceptance, parent.Acceptance[n-1]) {
						return errors.New("replacement weakened original criteria")
					}
					covered[n] = true
				}
			}
			if !found {
				return errors.New("replacement task missing")
			}
		}
		if len(covered) != len(parent.Acceptance) {
			return errors.New("replacement coverage incomplete")
		}
		if e.Tasks[parent.ID].State != "superseded" {
			return errors.New("decomposed original task must stay superseded")
		}
		if r.State == "complete" && r.CompletedAt.IsZero() {
			return errors.New("completed replan has no completion time")
		}
	}
	for _, t := range g.Tasks {
		p := e.Tasks[t.ID]
		if t.DecomposedFrom != children[t.ID] {
			return fmt.Errorf("task %s has unjournaled ancestry", t.ID)
		}
		if p.State == "superseded" {
			if len(p.ReplacedBy) == 0 {
				return errors.New("superseded task has no replacements")
			}
			for _, id := range p.ReplacedBy {
				if children[id] != t.ID {
					return errors.New("replacement ownership mismatch")
				}
			}
		} else if len(p.ReplacedBy) > 0 {
			return errors.New("ordinary task has replacements")
		}
	}
	if e.PendingReplan != "" {
		r, ok := e.Replans[e.PendingReplan]
		if !ok || (r.State != "reserved" && r.State != "provisioning") || e.PendingIntegration != "" {
			return errors.New("invalid pending replan barrier")
		}
	}
	return nil
}
