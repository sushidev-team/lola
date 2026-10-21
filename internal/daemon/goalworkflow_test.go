package daemon

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sushidev-team/lola/internal/goal"
	"github.com/sushidev-team/lola/internal/linear"
)

func configureGoalWorkflow(d *Daemon, f *linear.Fake, g goal.Goal) {
	p := d.cfg.ProjectByName(g.Project)
	p.OnSpawnStateID = "working"
	p.OnPRStateID = "review"
	p.OnMergedStateID = "done"
	f.StatesByTeam = map[string][]linear.State{g.TeamID: {{ID: "todo", Type: "unstarted"}, {ID: "working", Type: "started"}, {ID: "review", Type: "started"}, {ID: "done", Type: "completed"}, {ID: "human", Type: "started"}}}
	for i := range f.Details {
		f.Details[i].StateID = "todo"
		f.Details[i].StateType = "unstarted"
	}
}
func stateWriteCount(f *linear.Fake) int {
	n := 0
	for _, c := range f.Calls {
		if c.Method == "SetIssueState" {
			n++
		}
	}
	return n
}
func TestGoalWorkflowIntegrationWaitsForDelivery(t *testing.T) {
	d, g := readyPublicationFixture(t)
	f := d.lin.(*linear.Fake)
	configureGoalWorkflow(d, f, g)
	ctx := context.Background()
	now := time.Now().UTC()
	if err := d.syncGoalWorkflow(ctx, g.ID, now); err != nil {
		t.Fatal(err)
	}
	for _, task := range g.Tasks {
		if f.StateByIssue[task.Issue.ID] != "review" {
			t.Fatal("integrated task not left in review")
		}
	}
	if err := d.syncGoalWorkflow(ctx, g.ID, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	g, _ = d.goals.Get(g.ID)
	if g.State != "ready" || len(g.Workflow) != len(g.Tasks) {
		t.Fatal("state projection changed goal delivery")
	}
	if stateWriteCount(f) != len(g.Tasks) {
		t.Fatal("confirmation repeated a state write")
	}
	member := g.Execution.Policy.Members[0]
	delivery := goal.Delivery{Operation: g.Execution.Final.ID, State: "merged", CheckedAt: now.Add(2 * time.Second), Members: map[string]goal.DeliveryMember{member.ID: {Repo: member.Repo, Target: member.Target, Head: g.Execution.Manifest[member.ID], State: "merged", Number: 1, URL: "https://github.com/acme/widgets/pull/1"}}}
	if err := g.ObserveDelivery(delivery); err != nil {
		t.Fatal(err)
	}
	var err error
	g, err = d.goals.Save(g)
	if err != nil {
		t.Fatal(err)
	}
	if err = d.syncGoalWorkflow(ctx, g.ID, now.Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}
	for _, task := range g.Tasks {
		if f.StateByIssue[task.Issue.ID] != "done" {
			t.Fatal("delivered task not completed")
		}
	}
	if err = d.syncGoalWorkflow(ctx, g.ID, now.Add(4*time.Second)); err != nil {
		t.Fatal(err)
	}
	if stateWriteCount(f) != 2*len(g.Tasks) {
		t.Fatal("repeated delivery observation wrote again")
	}
}

type lostWorkflowWrite struct {
	*linear.Fake
	before func()
}

func (f lostWorkflowWrite) SetIssueState(ctx context.Context, issue, state string) error {
	if f.before != nil {
		f.before()
	}
	if err := f.Fake.SetIssueState(ctx, issue, state); err != nil {
		return err
	}
	return errors.New("response lost")
}
func TestGoalWorkflowRecoversLostWriteAndPreservesHumanChanges(t *testing.T) {
	d, f, g, p, _ := goalExecutionFixture(t)
	configureGoalWorkflow(d, f, g)
	ctx := context.Background()
	var err error
	g, err = d.authorizeGoal(ctx, goal.Authorization{ID: g.ID, Revision: g.Revision, Policy: p})
	if err != nil {
		t.Fatal(err)
	}
	if err = d.runGoal(ctx, g.ID); err != nil {
		t.Fatal(err)
	}
	d.lin = lostWorkflowWrite{Fake: f, before: func() {
		stored, _ := d.goals.Get(g.ID)
		w, ok := stored.LastWorkflow(g.Tasks[0].ID)
		if !ok || w.SentAt.IsZero() {
			t.Fatal("remote mutation preceded durable intent")
		}
	}}
	now := time.Now().UTC()
	if err = d.syncGoalWorkflow(ctx, g.ID, now); err == nil {
		t.Fatal("lost response not reported")
	}
	stored, _ := d.goals.Get(g.ID)
	if len(stored.Workflow) != 1 || stored.Workflow[0].SentAt.IsZero() {
		t.Fatal("send intent missing")
	}
	d.goals, err = goal.Open(filepath.Join(d.home, "state", "goals.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err = d.syncGoalWorkflow(ctx, g.ID, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	stored, _ = d.goals.Get(g.ID)
	if stored.Workflow[0].ConfirmedAt.IsZero() || stateWriteCount(f) != 1 {
		t.Fatal("lost response caused another write")
	}
	f.StateByIssue[g.Tasks[0].Issue.ID] = "human"
	if err = d.syncGoalWorkflow(ctx, g.ID, now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	if f.StateByIssue[g.Tasks[0].Issue.ID] != "human" || stateWriteCount(f) != 1 {
		t.Fatal("overwrote human status")
	}
}
func TestGoalWorkflowHoldsUnsafeMappingAndAmbiguousSend(t *testing.T) {
	for _, mode := range []string{"terminal-mapping", "foreign-state", "unknown-send", "edited-contract"} {
		t.Run(mode, func(t *testing.T) {
			d, g := readyPublicationFixture(t)
			f := d.lin.(*linear.Fake)
			configureGoalWorkflow(d, f, g)
			ctx := context.Background()
			switch mode {
			case "terminal-mapping":
				d.cfg.ProjectByName(g.Project).OnPRStateID = "done"
			case "foreign-state":
				d.cfg.ProjectByName(g.Project).OnPRStateID = "foreign"
			case "unknown-send":
				f.Errs = map[string]error{"SetIssueState": errors.New("network failure")}
			case "edited-contract":
				for i := range f.Details {
					if f.Details[i].ID == g.Tasks[0].Issue.ID {
						f.Details[i].Title = "Human changed scope"
					}
				}
			}
			now := time.Now().UTC()
			if err := d.syncGoalWorkflow(ctx, g.ID, now); err == nil {
				t.Fatal("unsafe projection succeeded")
			}
			writes := stateWriteCount(f)
			if mode == "terminal-mapping" || mode == "foreign-state" {
				if writes != 0 {
					t.Fatal("unsafe mapping wrote state")
				}
			}
			if mode == "edited-contract" {
				if f.StateByIssue[g.Tasks[0].Issue.ID] != "" || f.StateByIssue[g.Tasks[1].Issue.ID] != "review" {
					t.Fatal("changed issue overwritten or unrelated issue starved")
				}
			}
			if mode == "unknown-send" {
				f.Errs = nil
				if err := d.syncGoalWorkflow(ctx, g.ID, now.Add(time.Second)); err == nil || !strings.Contains(err.Error(), "unconfirmed") {
					t.Fatal("ambiguous send retried", err)
				}
				if stateWriteCount(f) != writes {
					t.Fatal("retried a possibly committed state write")
				}
			}
		})
	}
}
func TestGoalPrematureLinearCompletionDoesNotAuthorizeWork(t *testing.T) {
	d, f, g, p, launched := goalExecutionFixture(t)
	configureGoalWorkflow(d, f, g)
	f.StateByIssue = map[string]string{g.Tasks[0].Issue.ID: "done"}
	if _, err := d.authorizeGoal(context.Background(), goal.Authorization{ID: g.ID, Revision: g.Revision, Policy: p}); err == nil || !strings.Contains(err.Error(), "before verified integration") {
		t.Fatal("premature Done accepted", err)
	}
	if *launched != 0 {
		t.Fatal("human Done unlocked an unverified task")
	}
}

type changingWorkflowRead struct {
	*linear.Fake
	issue string
	reads int
}

func (f *changingWorkflowRead) LookupGoalIssue(ctx context.Context, id string) (*linear.IssueDetail, error) {
	if id == f.issue {
		f.reads++
		if f.reads == 2 {
			f.StateByIssue = map[string]string{id: "human"}
		}
	}
	return f.Fake.LookupGoalIssue(ctx, id)
}
func TestGoalWorkflowRechecksHumanStateBeforeWrite(t *testing.T) {
	d, g := readyPublicationFixture(t)
	f := d.lin.(*linear.Fake)
	configureGoalWorkflow(d, f, g)
	remote := &changingWorkflowRead{Fake: f, issue: g.Tasks[0].Issue.ID}
	d.lin = remote
	if err := d.syncGoalTaskWorkflow(context.Background(), g.ID, g.Tasks[0].ID, time.Now().UTC()); err == nil || !strings.Contains(err.Error(), "immediately before") {
		t.Fatal("late human state change not held", err)
	}
	if stateWriteCount(f) != 0 || f.StateByIssue[remote.issue] != "human" {
		t.Fatal("late human state overwritten")
	}
	stored, _ := d.goals.Get(g.ID)
	w, ok := stored.LastWorkflow(g.Tasks[0].ID)
	if !ok || !w.SentAt.IsZero() {
		t.Fatal("unsent reservation lost or marked sent")
	}
}
