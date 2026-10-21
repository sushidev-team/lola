package goal

import (
	"errors"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"time"

	"github.com/sushidev-team/lola/internal/goalpolicy"
	"github.com/sushidev-team/lola/internal/usage"
)

var commitID = regexp.MustCompile(`^(?:[a-f0-9]{40}|[a-f0-9]{64})$`)

type Authorization struct {
	ID       string            `json:"id"`
	Revision uint64            `json:"revision"`
	Policy   goalpolicy.Policy `json:"policy"`
}

type ControlRequest struct {
	ID       string `json:"id"`
	Revision uint64 `json:"revision"`
	Action   string `json:"action"`
	Reason   string `json:"reason,omitempty"`
}

// Prepared acknowledges the real starting revisions after private repository
// creation. No worker can reserve a run while initialization is incomplete.
func (g *Goal) Prepared(manifest map[string]string) error {
	if g.Execution == nil || g.Execution.Initialized || g.State != "preparing" {
		return errors.New("goal is not preparing its private repositories")
	}
	if err := validateManifest(g.Execution.Policy, manifest); err != nil {
		return err
	}
	g.Execution.Manifest = maps.Clone(manifest)
	g.Execution.Initialized = true
	g.State = "running"
	return nil
}

type Execution struct {
	Initialized         bool                    `json:"initialized"`
	Policy              goalpolicy.Policy       `json:"policy"`
	PolicyDigest        string                  `json:"policy_digest"`
	StartedAt           time.Time               `json:"started_at"`
	Deadline            time.Time               `json:"deadline"`
	Manifest            map[string]string       `json:"manifest"`
	Targets             map[string]string       `json:"targets,omitempty"` // current delivery bases after a verified refresh
	Refreshes           []Refresh               `json:"refreshes,omitempty"`
	PendingRefresh      string                  `json:"pending_refresh,omitempty"`
	Tasks               map[string]TaskProgress `json:"tasks"`
	Attempts            map[string]Attempt      `json:"attempts"`
	Runs                map[string]AgentRun     `json:"runs"`
	Integrations        map[string]Integration  `json:"integrations"`
	PendingIntegration  string                  `json:"pending_integration,omitempty"`
	Replans             map[string]Replan       `json:"replans,omitempty"`
	PendingReplan       string                  `json:"pending_replan,omitempty"`
	Final               *FinalVerification      `json:"final,omitempty"`
	FinalHistory        []FinalVerification     `json:"final_history,omitempty"`
	Delivery            *Delivery               `json:"delivery,omitempty"`
	DeliveryEvidenceURL string                  `json:"delivery_evidence_url,omitempty"`
	ObservedTokens      int64                   `json:"observed_tokens"`
	ObservedSpendUSD    float64                 `json:"observed_spend_usd"`
	UsageUpdatedAt      time.Time               `json:"usage_updated_at,omitempty"`
	StopWorkersAt       time.Time               `json:"stop_workers_at,omitempty"`
	Reason              string                  `json:"reason,omitempty"`
}

type TaskProgress struct {
	State         string   `json:"state"` // pending, active, integrated, blocked
	AttemptIDs    []string `json:"attempt_ids,omitempty"`
	IntegrationID string   `json:"integration_id,omitempty"`
	ReplacedBy    []string `json:"replaced_by,omitempty"`
}

type Attempt struct {
	ID         string                      `json:"id"`
	TaskID     string                      `json:"task_id"`
	SessionID  string                      `json:"session_id"`
	Members    []string                    `json:"members"`
	Scopes     map[string]goalpolicy.Scope `json:"scopes,omitempty"`
	Base       map[string]string           `json:"base"`
	State      string                      `json:"state"` // reserved, running, submitted, verifying, integrated, failed, stopped
	ReservedAt time.Time                   `json:"reserved_at"`
	Deadline   time.Time                   `json:"deadline"`
	FinishedAt time.Time                   `json:"finished_at,omitempty"`
	Error      string                      `json:"error,omitempty"`
	Heads      map[string]string           `json:"heads,omitempty"`
	Summary    string                      `json:"summary,omitempty"`
}

type AgentRun struct {
	Usage      *usage.Observation `json:"usage,omitempty"`
	ID         string             `json:"id"`
	Kind       string             `json:"kind"` // coordinator, worker, reviewer
	Subject    string             `json:"subject"`
	ReservedAt time.Time          `json:"reserved_at"`
	FinishedAt time.Time          `json:"finished_at,omitempty"`
	Tokens     int64              `json:"tokens,omitempty"`
	SpendUSD   float64            `json:"spend_usd,omitempty"`
	Estimated  bool               `json:"estimated,omitempty"`
}

type Integration struct {
	ID          string            `json:"id"`
	AttemptID   string            `json:"attempt_id"`
	Base        map[string]string `json:"base"`
	Manifest    map[string]string `json:"manifest,omitempty"`
	Receipt     string            `json:"receipt,omitempty"`
	State       string            `json:"state"` // reserved, complete, failed
	CreatedAt   time.Time         `json:"created_at"`
	CompletedAt time.Time         `json:"completed_at,omitempty"`
}

// BeginExecution accepts an explicit policy only after Linear publication and
// private repository initialization. The caller persists the returned mutation
// before reserving or launching any agent. Policy changes require a separate
// accepted revision, never an edited Linear description.
func (g *Goal) BeginExecution(policy goalpolicy.Policy, manifest map[string]string, now time.Time) error {
	return g.beginExecution(policy, manifest, now, false)
}

func (g *Goal) BeginPreparation(policy goalpolicy.Policy, now time.Time) error {
	return g.beginExecution(policy, nil, now, true)
}

func (g *Goal) beginExecution(policy goalpolicy.Policy, manifest map[string]string, now time.Time, preparing bool) error {
	if err := g.validateAdoptions(); err != nil {
		return err
	}
	if g.State != "planned" || g.Execution != nil || (g.SyncError != "" || g.PendingBrief != "") || !g.Parent.Confirmed || (g.Milestone.ID != "" && !g.Milestone.Confirmed) {
		return errors.New("only a fully published, synchronized goal can be authorized")
	}
	digest, err := policy.Digest()
	if err != nil {
		return err
	}
	if len(g.Tasks) > policy.Limits.MaxTasks {
		return errors.New("goal has more tasks than its accepted limit")
	}
	if !preparing {
		if err := validateManifest(policy, manifest); err != nil {
			return err
		}
	}
	tasks := map[string]TaskProgress{}
	for _, task := range g.Tasks {
		if !task.Issue.Confirmed {
			return errors.New("task bindings are not fully published")
		}
		for _, relation := range task.Relations {
			if !relation.Confirmed {
				return errors.New("task dependencies are not fully published")
			}
		}
		if _, err := TaskScopes(policy, task); err != nil {
			return err
		}
		tasks[task.ID] = TaskProgress{State: "pending"}
	}
	if now.IsZero() {
		return errors.New("authorization time required")
	}
	g.Execution = &Execution{Initialized: !preparing, Policy: policy, PolicyDigest: digest, StartedAt: now, Deadline: now.Add(time.Duration(policy.Limits.RuntimeMinutes) * time.Minute), Manifest: maps.Clone(manifest), Tasks: tasks, Attempts: map[string]Attempt{}, Runs: map[string]AgentRun{}, Integrations: map[string]Integration{}}
	g.State = "running"
	if preparing {
		g.State = "preparing"
	}
	return nil
}

func (g Goal) canAdvance(now time.Time) error { return g.canOperate(now, false) }

func (g Goal) canOperate(now time.Time, verifying bool) error {
	e := g.Execution
	if e == nil || !e.Initialized || (g.State != "running" && !(verifying && g.State == "verifying")) || (g.SyncError != "" || g.PendingBrief != "") {
		return errors.New("goal is not authorized and synchronized for new work")
	}
	if now.IsZero() || now.Before(e.StartedAt) {
		return errors.New("execution timestamp precedes authorization")
	}
	if digest, err := e.Policy.Digest(); err != nil || digest != e.PolicyDigest {
		return errors.New("accepted execution policy changed")
	}
	if !now.Before(e.Deadline) {
		return errors.New("goal runtime budget reached")
	}
	if e.ObservedTokens >= e.Policy.Limits.Tokens || (e.Policy.Limits.SpendUSD > 0 && e.ObservedSpendUSD >= e.Policy.Limits.SpendUSD) {
		return errors.New("goal observed usage budget reached")
	}
	return nil
}

func (g *Goal) ReserveRun(id, kind, subject string, now time.Time) error {
	if err := g.canOperate(now, kind != "worker"); err != nil {
		return err
	}
	e := g.Execution
	if id == "" || subject == "" || !slices.Contains([]string{"coordinator", "worker", "reviewer"}, kind) {
		return errors.New("agent run identity, kind and subject required")
	}
	if _, exists := e.Runs[id]; exists {
		return errors.New("agent run already reserved")
	}
	if len(e.Runs) >= e.Policy.Limits.AgentRuns {
		return errors.New("goal agent-run budget reached")
	}
	e.Runs[id] = AgentRun{ID: id, Kind: kind, Subject: subject, ReservedAt: now}
	return nil
}

func activeAttempt(state string) bool {
	return slices.Contains([]string{"reserved", "running", "submitted", "verifying"}, state)
}

func (g *Goal) ReserveAttempt(taskID, id, sessionID string, now time.Time) (Attempt, error) {
	if err := g.canAdvance(now); err != nil {
		return Attempt{}, err
	}
	e := g.Execution
	if e.PendingIntegration != "" || e.PendingReplan != "" || e.PendingRefresh != "" || !e.StopWorkersAt.IsZero() {
		return Attempt{}, errors.New("goal integration or stop-workers barrier holds dispatch")
	}
	if id == "" || sessionID == "" {
		return Attempt{}, errors.New("attempt and session identities required")
	}
	if _, exists := e.Attempts[id]; exists {
		return Attempt{}, errors.New("attempt identity already reserved")
	}
	progress, exists := e.Tasks[taskID]
	if !exists || progress.State != "pending" {
		return Attempt{}, errors.New("task is not pending")
	}
	if len(progress.AttemptIDs) >= e.Policy.Limits.AttemptsPerTask {
		return Attempt{}, errors.New("task attempt budget reached")
	}
	live := 0
	for _, a := range e.Attempts {
		if a.SessionID == sessionID {
			return Attempt{}, errors.New("session identity already belongs to an attempt")
		}
		if activeAttempt(a.State) {
			live++
		}
	}
	if live >= e.Policy.Limits.Concurrency {
		return Attempt{}, errors.New("goal concurrency budget reached")
	}
	var task Task
	for _, t := range g.Tasks {
		if t.ID == taskID {
			task = t
			break
		}
	}
	for _, dep := range task.BlockedBy {
		if dep < 1 || dep > len(g.Tasks) || !g.TaskSatisfied(g.Tasks[dep-1].ID) {
			return Attempt{}, errors.New("task dependencies have no complete integration receipt")
		}
	}
	members, err := taskMembers(e.Policy, task)
	if err != nil {
		return Attempt{}, err
	}
	scopes, err := TaskScopes(e.Policy, task)
	if err != nil {
		return Attempt{}, err
	}
	for _, other := range e.Attempts {
		if !activeAttempt(other.State) {
			continue
		}
		owned, err := g.AttemptScopes(other)
		if err != nil {
			return Attempt{}, err
		}
		if scopesOverlap(scopes, owned) {
			return Attempt{}, errors.New("task write ownership overlaps an active attempt")
		}
	}
	if err := g.ReserveRun(id, "worker", taskID, now); err != nil {
		return Attempt{}, err
	}
	deadline := now.Add(time.Duration(e.Policy.Limits.AttemptMinutes) * time.Minute)
	if e.Deadline.Before(deadline) {
		deadline = e.Deadline
	}
	a := Attempt{ID: id, TaskID: taskID, SessionID: sessionID, Members: members, Scopes: scopes, Base: maps.Clone(e.Manifest), State: "reserved", ReservedAt: now, Deadline: deadline}
	e.Attempts[id] = a
	progress.State = "active"
	progress.AttemptIDs = append(progress.AttemptIDs, id)
	e.Tasks[taskID] = progress
	return a, nil
}

func (g *Goal) ReserveIntegration(id, attemptID string, now time.Time) (Integration, error) {
	if err := g.canAdvance(now); err != nil {
		return Integration{}, err
	}
	e := g.Execution
	a, ok := e.Attempts[attemptID]
	if !ok || a.State != "submitted" || id == "" || e.PendingIntegration != "" || e.PendingReplan != "" || e.PendingRefresh != "" {
		return Integration{}, errors.New("integration requires submitted output and an empty integration queue")
	}
	if _, exists := e.Integrations[id]; exists {
		return Integration{}, errors.New("integration identity reused")
	}
	i := Integration{ID: id, AttemptID: attemptID, Base: maps.Clone(e.Manifest), State: "reserved", CreatedAt: now}
	e.Integrations[id] = i
	e.PendingIntegration = id
	a.State = "verifying"
	e.Attempts[attemptID] = a
	return i, nil
}

// CompleteIntegration accepts only a daemon-verified complete manifest receipt.
// This method is deliberately absent from worker/socket APIs. The caller must
// verify the journal and check/profile evidence before making this mutation.
func (g *Goal) CompleteIntegration(id, receipt string, manifest map[string]string, now time.Time) error {
	if err := g.canAdvance(now); err != nil {
		return err
	}
	e := g.Execution
	i, ok := e.Integrations[id]
	if !ok || e.PendingIntegration != id || i.State != "reserved" || receipt == "" || !maps.Equal(e.Manifest, i.Base) {
		return errors.New("integration receipt does not match the pending base")
	}
	if err := validateManifest(e.Policy, manifest); err != nil {
		return err
	}
	a := e.Attempts[i.AttemptID]
	for member, sha := range manifest {
		if !slices.Contains(a.Members, member) && sha != i.Base[member] {
			return errors.New("task integration changed a repository outside its scope")
		}
	}
	i.Manifest = maps.Clone(manifest)
	i.State = "complete"
	i.Receipt = receipt
	i.CompletedAt = now
	e.Integrations[id] = i
	e.Manifest = maps.Clone(manifest)
	e.PendingIntegration = ""
	a.State = "integrated"
	a.FinishedAt = now
	e.Attempts[a.ID] = a
	p := e.Tasks[a.TaskID]
	p.State = "integrated"
	p.IntegrationID = id
	e.Tasks[a.TaskID] = p
	if g.AllTasksSatisfied() {
		g.State = "verifying"
	} // Final goal acceptance and human delivery are separate.
	return nil
}

func taskMembers(policy goalpolicy.Policy, t Task) ([]string, error) {
	members := slices.Clone(t.Apps)
	if len(members) == 0 && len(policy.Members) == 1 {
		members = []string{policy.Members[0].ID}
	}
	if len(members) == 0 {
		return nil, errors.New("stack task requires explicit member routing")
	}
	seen := map[string]bool{}
	for _, member := range members {
		if seen[member] || !slices.ContainsFunc(policy.Members, func(m goalpolicy.Member) bool { return m.ID == member }) {
			return nil, fmt.Errorf("task member %s is outside accepted scope or repeated", member)
		}
		seen[member] = true
	}
	return members, nil
}

func validateManifest(policy goalpolicy.Policy, manifest map[string]string) error {
	if len(manifest) != len(policy.Members) {
		return errors.New("execution manifest must contain every authorized repository")
	}
	for _, m := range policy.Members {
		if !commitID.MatchString(manifest[m.ID]) {
			return fmt.Errorf("exact starting commit required for member %s", m.ID)
		}
	}
	return nil
}
