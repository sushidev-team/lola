package goal

import (
	"maps"
	"slices"
	"time"

	"github.com/sushidev-team/lola/internal/goalpolicy"
)

// Overview is the cached presentation of a goal. It omits private paths,
// repository snapshots and raw artifacts; rendering it never calls an agent or
// external service. Actions are advisory: the daemon rechecks the revision and
// all runtime gates when a human submits one.
type Overview struct {
	ID                     string               `json:"id"`
	Revision               uint64               `json:"revision"`
	Project                string               `json:"project"`
	Title                  string               `json:"title"`
	State                  string               `json:"state"`
	Outcome                string               `json:"outcome"`
	Acceptance             []string             `json:"acceptance"`
	NonGoals               []string             `json:"nonGoals"`
	Parent                 Binding              `json:"parent"`
	Milestone              Binding              `json:"milestone"`
	MilestoneName          string               `json:"milestoneName"`
	SyncError              string               `json:"syncError"`
	Reason                 string               `json:"reason"`
	Actions                []string             `json:"actions"`
	Tasks                  []TaskView           `json:"tasks"`
	Members                []MemberView         `json:"members"`
	Authorized             bool                 `json:"authorized"`
	Stopping               bool                 `json:"stopping"`
	Verified               bool                 `json:"verified"`
	Deadline               time.Time            `json:"deadline"`
	UpdatedAt              time.Time            `json:"updatedAt"`
	Tokens                 int64                `json:"tokens"`
	TokenLimit             int64                `json:"tokenLimit"`
	SpendUSD               float64              `json:"spendUSD"`
	SpendLimitUSD          float64              `json:"spendLimitUSD"`
	RunLimit               int                  `json:"runLimit"`
	Coverage               UsageCoverage        `json:"coverage"`
	RequireHumanAcceptance bool                 `json:"requireHumanAcceptance,omitempty"`
	Delivered              bool                 `json:"delivered,omitempty"`
	HumanAcceptance        *HumanAcceptanceView `json:"humanAcceptance,omitempty"`
	Rollout                *goalpolicy.Rollout  `json:"rollout,omitempty"`
}

// HumanAcceptanceView omits local proof paths; the delivered heads and targets
// remain available through Members in the same revision-bound overview.
type HumanAcceptanceView struct {
	Revision   uint64 `json:"revision"`
	Evidence   string `json:"evidence"`
	AcceptedAt string `json:"acceptedAt"`
}
type TaskView struct {
	ID         string        `json:"id"`
	Title      string        `json:"title"`
	State      string        `json:"state"`
	Issue      Binding       `json:"issue"`
	Acceptance []string      `json:"acceptance"`
	Apps       []string      `json:"apps"`
	BlockedBy  []int         `json:"blockedBy"`
	ReplacedBy []string      `json:"replacedBy"`
	Attempts   []AttemptView `json:"attempts"`
}
type AttemptView struct {
	ID        string `json:"id"`
	SessionID string `json:"sessionId"`
	State     string `json:"state"`
	Error     string `json:"error"`
	Summary   string `json:"summary"`
}
type MemberView struct {
	ID            string `json:"id"`
	Repo          string `json:"repo"`
	Target        string `json:"target"`
	Commit        string `json:"commit"`
	PRURL         string `json:"prUrl"`
	PRNumber      int    `json:"prNumber"`
	DeliveryState string `json:"deliveryState"`
}

func (g Goal) Overview(now time.Time) Overview {
	out := Overview{ID: g.ID, Revision: g.Revision, Project: g.Project, Title: g.Title, State: g.State, Outcome: g.Outcome,
		Acceptance: slices.Clone(g.Acceptance), NonGoals: slices.Clone(g.NonGoals), Parent: g.Parent, Milestone: g.Milestone,
		MilestoneName: g.MilestoneName, SyncError: g.SyncError, UpdatedAt: g.UpdatedAt, Coverage: g.UsageCoverage(),
		Actions: []string{}, Tasks: []TaskView{}, Members: []MemberView{}}
	for _, task := range g.Tasks {
		t := TaskView{ID: task.ID, Title: task.Title, State: "planned", Issue: task.Issue, Acceptance: slices.Clone(task.Acceptance),
			Apps: slices.Clone(task.Apps), BlockedBy: slices.Clone(task.BlockedBy), Attempts: []AttemptView{}}
		if e := g.Execution; e != nil {
			progress := e.Tasks[task.ID]
			t.State = progress.State
			t.ReplacedBy = slices.Clone(progress.ReplacedBy)
			for _, id := range progress.AttemptIDs {
				a := e.Attempts[id]
				t.Attempts = append(t.Attempts, AttemptView{ID: a.ID, SessionID: a.SessionID, State: a.State, Error: a.Error, Summary: a.Summary})
			}
		}
		out.Tasks = append(out.Tasks, t)
	}
	e := g.Execution
	if e == nil {
		return out
	}
	out.Authorized = true
	out.RequireHumanAcceptance = e.Policy.RequireHumanAcceptance
	if r := e.Policy.Rollout; r != nil {
		out.Rollout = &goalpolicy.Rollout{Compatibility: slices.Clone(r.Compatibility), MergeOrder: slices.Clone(r.MergeOrder)}
	}
	out.Delivered = g.Delivered()
	if a := e.HumanAcceptance; a != nil {
		out.HumanAcceptance = &HumanAcceptanceView{Revision: a.Revision, Evidence: a.Evidence, AcceptedAt: a.AcceptedAt.UTC().Format(time.RFC3339)}
	}
	out.Stopping = !e.StopWorkersAt.IsZero()
	out.Verified = e.Final != nil && e.Final.Receipt != ""
	out.Reason = e.Reason
	out.Deadline = e.Deadline
	out.Tokens, out.TokenLimit = e.ObservedTokens, e.Policy.Limits.Tokens
	out.SpendUSD, out.SpendLimitUSD = e.ObservedSpendUSD, e.Policy.Limits.SpendUSD
	out.RunLimit = e.Policy.Limits.AgentRuns
	for _, member := range e.Policy.Members {
		v := MemberView{ID: member.ID, Repo: member.Repo, Target: member.Target, Commit: e.Manifest[member.ID]}
		if e.Delivery != nil {
			d := e.Delivery.Members[member.ID]
			v.PRURL, v.PRNumber, v.DeliveryState = d.URL, d.Number, d.State
		}
		out.Members = append(out.Members, v)
	}
	for _, action := range []string{"accept-delivery", "pause", "resume", "stop-workers", "cancel"} {
		if action == "pause" && g.State == "paused" || action == "stop-workers" && out.Stopping {
			continue
		}
		trial := g
		copied := *e
		copied.Tasks = maps.Clone(e.Tasks) // resume can change pending task state
		trial.Execution = &copied
		if trial.Control(action, "Action availability probe", now) == nil {
			out.Actions = append(out.Actions, action)
		}
	}
	return out
}
