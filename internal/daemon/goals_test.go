package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/goal"
	"github.com/sushidev-team/lola/internal/linear"
	"github.com/sushidev-team/lola/internal/protocol"
)

func goalFixture(t *testing.T) (*Daemon, *linear.Fake, goal.Draft) {
	t.Helper()
	p := labelPoll("p1")
	p.ProjectID = "project-1"
	f := &linear.Fake{ProjectsByTeam: map[string][]linear.Project{"team-1": {{ID: "project-1", Name: "Nori"}}},
		LabelsByTeam: map[string][]linear.Label{"team-1": {{ID: "label-api", Name: "api"}, {ID: "label-web", Name: "web"}}}}
	d := newTestDaemon(t, testConfig(p), f, &fakeNative{})
	draft := goal.Draft{Project: "p1", Title: "Invitations", Outcome: "Invite a colleague", Acceptance: []string{"Recipient can accept"}, MilestoneName: "Invitations",
		Tasks: []goal.TaskDraft{{Title: "API", Acceptance: []string{"Expired links fail"}, LabelIDs: []string{"label-api"}},
			{Title: "UI", Acceptance: []string{"Form works with keyboard"}, LabelIDs: []string{"label-web"}, BlockedBy: []int{1}}}}
	return d, f, draft
}

func TestGoalPublishRetryAndRestart(t *testing.T) {
	d, f, draft := goalFixture(t)
	g, err := d.createGoal(context.Background(), draft)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Created) != 0 || !d.goals.OwnsIssue(g.Parent.ID) {
		t.Fatal("create must only reserve locally")
	}
	f.Errs = map[string]error{"CreateGoalRelation": errors.New("network unavailable")}
	if _, err := d.publishGoal(context.Background(), g.ID); err == nil {
		t.Fatal("partial dependency failure not reported")
	}
	if len(f.Created) != 3 || len(f.Milestones) != 1 {
		t.Fatalf("partial artifacts: issues=%d milestones=%d", len(f.Created), len(f.Milestones))
	}
	stored, _ := d.goals.Get(g.ID)
	if stored.State != "provisioning" || stored.SyncError == "" {
		t.Fatalf("partial state: %+v", stored)
	}
	// Reopen exactly the persisted store; a retry cannot issue fresh IDs.
	d.goals, err = goal.Open(filepath.Join(d.home, "state", "goals.json"))
	if err != nil {
		t.Fatal(err)
	}
	delete(f.Errs, "CreateGoalRelation")
	for range 2 {
		out, err := d.publishGoal(context.Background(), g.ID)
		if err != nil {
			t.Fatal(err)
		}
		if out.State != "planned" || out.SyncError != "" {
			t.Fatalf("not planned: %+v", out)
		}
	}
	if len(f.Created) != 3 || len(f.Milestones) != 1 || len(f.GoalRelations) != 1 {
		t.Fatal("retry duplicated Linear artifacts")
	}
	for _, in := range f.Created {
		if !goal.Marked(in.Description) || in.ProjectID != "project-1" || in.MilestoneID != g.Milestone.ID {
			t.Fatalf("missing creation-time ownership: %+v", in)
		}
	}
	if f.Created[1].LabelIDs[0] != "label-api" || f.Created[2].LabelIDs[0] != "label-web" {
		t.Fatal("task routing labels contaminated")
	}
}

// The server committed the request, but its response was lost. Every kind of
// create must recover from a read by the reserved UUID, not duplicate the write.
type lostGoalResponse struct{ *linear.Fake }

func (f lostGoalResponse) CreateIssue(ctx context.Context, in linear.IssueCreate) (string, string, error) {
	_, _, err := f.Fake.CreateIssue(ctx, in)
	if err != nil {
		return "", "", err
	}
	return "", "", errors.New("lost issue response")
}
func (f lostGoalResponse) CreateGoalMilestone(ctx context.Context, in linear.GoalMilestone) error {
	if err := f.Fake.CreateGoalMilestone(ctx, in); err != nil {
		return err
	}
	return errors.New("lost milestone response")
}
func (f lostGoalResponse) CreateGoalRelation(ctx context.Context, in linear.GoalRelation) error {
	if err := f.Fake.CreateGoalRelation(ctx, in); err != nil {
		return err
	}
	return errors.New("lost relation response")
}

func TestGoalLostResponsesRecover(t *testing.T) {
	d, f, draft := goalFixture(t)
	d.lin = lostGoalResponse{f}
	g, err := d.createGoal(context.Background(), draft)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.publishGoal(context.Background(), g.ID); err != nil {
		t.Fatal(err)
	}
	if len(f.Created) != 3 || len(f.Milestones) != 1 || len(f.GoalRelations) != 1 {
		t.Fatal("lost responses duplicated artifacts")
	}
}

func TestGoalConfirmedBindingDriftHolds(t *testing.T) {
	for _, change := range []string{"edit", "move", "cancel", "archive", "labels", "delete", "relation", "milestone"} {
		t.Run(change, func(t *testing.T) {
			d, f, draft := goalFixture(t)
			g, err := d.createGoal(context.Background(), draft)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := d.publishGoal(context.Background(), g.ID); err != nil {
				t.Fatal(err)
			}
			switch change {
			case "edit":
				f.Details[0].Description += "\nHuman correction"
			case "move":
				f.Details[1].ProjectID = "different"
			case "cancel":
				f.Details[1].StateType = "canceled"
			case "archive":
				f.Details[1].ArchivedAt = "now"
			case "labels":
				f.Details[1].LabelIDs = nil
			case "delete":
				f.Details = f.Details[1:]
			case "relation":
				f.GoalRelations = nil
			case "milestone":
				f.Milestones = nil
			}
			if _, err := d.publishGoal(context.Background(), g.ID); err == nil {
				t.Fatal("must hold on human change")
			}
			if len(f.Created) != 3 {
				t.Fatal("recreated deleted/changed work")
			}
			stored, _ := d.goals.Get(g.ID)
			if stored.SyncError == "" {
				t.Fatal("sync degradation not persisted")
			}
		})
	}
}

func TestGoalExistingMilestonePreserved(t *testing.T) {
	d, f, draft := goalFixture(t)
	draft.MilestoneName, draft.MilestoneID = "", "release"
	f.Milestones = []linear.GoalMilestone{{ID: "release", ProjectID: "project-1", Name: "First Public Release", Description: "Human release plan"}}
	g, err := d.createGoal(context.Background(), draft)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.publishGoal(context.Background(), g.ID); err != nil {
		t.Fatal(err)
	}
	if len(f.Milestones) != 1 || f.Milestones[0].Description != "Human release plan" {
		t.Fatal("existing milestone changed")
	}
}

func TestGoalStackRoutingIncludesUnconditionalMembers(t *testing.T) {
	d, _, draft := goalFixture(t)
	d.cfg.Projects[0].Kind = config.ProjectKindStack
	d.cfg.Projects = append(d.cfg.Projects,
		config.Project{Name: "api", Stack: "p1", Labels: []string{"api"}},
		config.Project{Name: "web", Stack: "p1", Labels: []string{"web"}},
		config.Project{Name: "shared", Stack: "p1"})
	draft.Tasks[0].Apps, draft.Tasks[1].Apps = []string{"api"}, []string{"web", "shared"}
	if _, err := d.createGoal(context.Background(), draft); err == nil || !strings.Contains(err.Error(), "always-included") {
		t.Fatalf("missing unconditional member: %v", err)
	}
	draft.Tasks[0].Apps = []string{"api", "shared"}
	if _, err := d.createGoal(context.Background(), draft); err != nil {
		t.Fatal(err)
	}
	draft.Tasks[1].LabelIDs = []string{"label-api"}
	if _, err := d.createGoal(context.Background(), draft); err == nil {
		t.Fatal("mismatched task scope allowed")
	}
}

func TestGoalDispatchExclusionAllPollModes(t *testing.T) {
	for _, mode := range []string{"label", "state", "seen", "no-labels"} {
		t.Run(mode, func(t *testing.T) {
			d, f, draft := goalFixture(t)
			g, err := d.createGoal(context.Background(), draft)
			if err != nil {
				t.Fatal(err)
			}
			p := &d.cfg.Projects[0]
			p.DedupMode = mode
			if mode == "no-labels" {
				p.MatchLabels = nil
				p.DedupMode = "seen"
			}
			f.Issues = []linear.Issue{{ID: g.Parent.ID, Identifier: "GOAL-1", OpenChildren: 0},
				{ID: g.Tasks[0].Issue.ID, Identifier: "GOAL-2"},
				{ID: "unknown", Identifier: "GOAL-3", Description: goal.MarkerPrefix + "partial"},
				testIssue("FE-9", 1, "2024-01-01T00:00:00Z")}
			res, err := d.tick(context.Background(), p.Name, false)
			if err != nil {
				t.Fatal(err)
			}
			for _, id := range []string{"GOAL-1", "GOAL-2", "GOAL-3"} {
				m := findMatch(t, res, id)
				if m.Action != "skipped" || !strings.Contains(m.Reason, "managed by a goal") {
					t.Fatalf("%s escaped: %+v", id, m)
				}
			}
			if m := findMatch(t, res, "FE-9"); m.Action != "spawned" {
				t.Fatalf("ordinary dispatch changed: %+v", m)
			}
			seen, _ := d.seen.load(p.Name)
			if _, ok := seen[g.Parent.ID]; ok {
				t.Fatal("goal exclusion mutated seen")
			}
		})
	}
}

func TestGoalCorruptStoreHoldsDispatch(t *testing.T) {
	d, f, _ := goalFixture(t)
	path := filepath.Join(d.home, "state", "goals.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	d.goals, d.goalsErr = goal.Open(path)
	f.Issues = []linear.Issue{testIssue("FE-9", 1, "2024-01-01T00:00:00Z")}
	res, err := d.tick(context.Background(), "p1", false)
	if err != nil {
		t.Fatal(err)
	}
	if m := findMatch(t, res, "FE-9"); m.Action != "skipped" || !strings.Contains(m.Reason, "reservations unavailable") {
		t.Fatalf("unsafe dispatch: %+v", m)
	}
}

func TestGoalManualTicketCannotBypassMarker(t *testing.T) {
	d, f, _ := goalFixture(t)
	f.Details = []linear.IssueDetail{{ID: "unknown", Identifier: "FE-9", Description: goal.MarkerPrefix}}
	_, err := d.handleOpenTicket(context.Background(), protocol.OpenTicketArgs{Project: "p1", UUID: "unknown", Identifier: "FE-9"})
	if err == nil || !strings.Contains(err.Error(), "managed by a goal") {
		t.Fatalf("manual launch bypassed goal: %v", err)
	}
	if d.inflight.Has("unknown") {
		t.Fatal("held issue was claimed")
	}
}

func TestGoalPublishReviewed(t *testing.T) {
	d, f, draft := goalFixture(t)
	g, err := d.createGoal(context.Background(), draft)
	if err != nil {
		t.Fatal(err)
	}
	// A stale publication request must not even write a sync error onto the goal.
	before, _ := d.goals.Get(g.ID)
	if _, err = d.publishGoalAtRevision(context.Background(), g.ID, g.Revision+1); err == nil {
		t.Fatal("accepted stale publication")
	}
	after, _ := d.goals.Get(g.ID)
	if len(f.Created) != 0 || len(f.Milestones) != 0 || after.Revision != before.Revision || after.SyncError != "" {
		t.Fatal("stale request mutated state")
	}
	args, _ := json.Marshal(map[string]any{"id": g.ID, "revision": 0})
	if response := d.handle(context.Background(), protocol.Request{Cmd: "goalPublishReviewed", Args: args}); response.Error == "" {
		t.Fatal("accepted absent review revision")
	}
	args, _ = json.Marshal(map[string]any{"id": g.ID, "revision": g.Revision})
	response := d.handle(context.Background(), protocol.Request{Cmd: "goalPublishReviewed", Args: args})
	if response.Error != "" {
		t.Fatal(response.Error)
	}
	after, _ = d.goals.Get(g.ID)
	if len(f.Created) != 3 || len(f.Milestones) != 1 || after.State != "planned" || after.Execution != nil {
		t.Fatal("publication exceeded reviewed scope")
	}
}
