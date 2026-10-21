package daemon

import (
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/goal"
	"github.com/sushidev-team/lola/internal/linear"
	"github.com/sushidev-team/lola/internal/scm"
	"github.com/sushidev-team/lola/internal/session"
)

func adoptedGoalFixture(t *testing.T) (*Daemon, *linear.Fake, goal.Draft, linear.IssueDetail) {
	t.Helper()
	d, f, draft := goalFixture(t)
	id := "12ab5678-1234-4234-9234-123456789abc"
	draft.Tasks[0].ExistingIssueID = id
	draft.Tasks[0].Description = "Existing API work owned by the release team."
	existing := linear.IssueDetail{ID: id, Identifier: "NOR-42", Title: draft.Tasks[0].Title, Description: draft.Tasks[0].Description, TeamID: "team-1", ProjectID: "project-1", ParentID: "existing-parent", MilestoneID: "release-milestone", LabelIDs: draft.Tasks[0].LabelIDs, StateType: "unstarted"}
	f.Details = append(f.Details, existing)
	return d, f, draft, existing
}

func TestGoalAdoptionPreservesIssueAndRecoversPublication(t *testing.T) {
	d, f, draft, existing := adoptedGoalFixture(t)
	d.lin = lostGoalResponse{f}
	g, err := d.createGoal(context.Background(), draft)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Created) != 0 || len(f.GoalRelations) != 0 || !d.goals.OwnsIssue(existing.ID) || g.Tasks[0].Adoption.Membership.Confirmed {
		t.Fatal("adoption was not reserved before external writes")
	}
	if err := d.claimOrdinaryIssue(existing.ID, existing.Identifier, existing.Description, existing.IssueRelations); err == nil {
		t.Fatal("reserved adoption dispatched normally")
	}
	for range 2 {
		g, err = d.publishGoal(context.Background(), g.ID)
		if err != nil {
			t.Fatal(err)
		}
		d.goals, err = goal.Open(filepath.Join(d.home, "state", "goals.json"))
		if err != nil {
			t.Fatal(err)
		}
	}
	if g.State != "planned" || !g.Tasks[0].Adoption.Membership.Confirmed || len(f.Created) != 2 || len(f.GoalRelations) != 2 {
		t.Fatal("adoption duplicated issue or relation", len(f.Created), len(f.GoalRelations))
	}
	if !reflect.DeepEqual(f.Details[0], existing) {
		t.Fatal("adoption rewrote the existing issue", f.Details[0])
	}
	if !strings.Contains(g.ParentDescription(), "https://linear.app/issue/"+existing.ID) || !strings.Contains(g.ParentDescription(), draft.Tasks[0].Acceptance[0]) {
		t.Fatal("accepted adopted criteria absent from Linear goal brief")
	}
	if err := d.goalBindingsCurrent(context.Background(), g); err != nil {
		t.Fatal(err)
	}
	if _, err := d.createGoal(context.Background(), draft); err == nil {
		t.Fatal("same issue adopted into two goals")
	}
	// A different Lola home has no local reservations, but the relation to the
	// goal parent is enough to exclude the issue from ordinary execution.
	other, _, _ := goalFixture(t)
	detail, err := f.IssueDetail(context.Background(), existing.ID)
	if err != nil {
		t.Fatal(err)
	}
	if other.goalHold(detail.ID, detail.Description, detail.IssueRelations) == "" {
		t.Fatal("foreign Lola home missed adopted ownership")
	}
	if _, err := other.createGoal(context.Background(), draft); err == nil {
		t.Fatal("fixture unexpectedly found foreign issue")
	}
	other.lin = f
	if _, err := other.createGoal(context.Background(), draft); err == nil {
		t.Fatal("adopted an issue already related to a foreign goal")
	}
}

func TestGoalAdoptionDriftHoldsWithoutOverwriting(t *testing.T) {
	for _, mode := range []string{"title", "description", "labels", "parent", "milestone", "team", "project", "archive", "cancel", "missing", "membership"} {
		t.Run(mode, func(t *testing.T) {
			d, f, draft, _ := adoptedGoalFixture(t)
			g, err := d.createGoal(context.Background(), draft)
			if err != nil {
				t.Fatal(err)
			}
			g, err = d.publishGoal(context.Background(), g.ID)
			if err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "title":
				f.Details[0].Title = "Human change"
			case "description":
				f.Details[0].Description += "\nNew requirement"
			case "labels":
				f.Details[0].LabelIDs = []string{"label-web"}
			case "parent":
				f.Details[0].ParentID = "another-parent"
			case "milestone":
				f.Details[0].MilestoneID = "next-release"
			case "team":
				f.Details[0].TeamID = "other-team"
			case "project":
				f.Details[0].ProjectID = "other-project"
			case "archive":
				f.Details[0].ArchivedAt = "2026-10-21"
			case "cancel":
				f.Details[0].StateType = "canceled"
			case "missing":
				f.Details = f.Details[1:]
			case "membership":
				f.GoalRelations = f.GoalRelations[1:]
			}
			if err := d.goalBindingsCurrent(context.Background(), g); err == nil {
				t.Fatal("execution ignored adoption drift")
			}
			if _, err := d.publishGoal(context.Background(), g.ID); err == nil {
				t.Fatal("republishing ignored adoption drift")
			}
			if len(f.Created) != 2 {
				t.Fatal("recreated an adopted issue")
			}
			for _, call := range f.Calls {
				if call.Method == "SetIssueState" || call.Method == "SetLabels" {
					t.Fatal("adoption mutated existing issue")
				}
			}
		})
	}
}

func TestGoalAdoptionRejectsUnavailableOrBusyIssues(t *testing.T) {
	for _, mode := range []string{"title", "description", "labels", "completed", "canceled", "inflight", "relations", "foreign"} {
		t.Run(mode, func(t *testing.T) {
			d, f, draft, existing := adoptedGoalFixture(t)
			switch mode {
			case "title":
				draft.Tasks[0].Title = "Unaccepted replacement"
			case "description":
				draft.Tasks[0].Description = "Unaccepted replacement"
			case "labels":
				draft.Tasks[0].LabelIDs = []string{"label-web"}
			case "completed", "canceled":
				f.Details[0].StateType = mode
			case "inflight":
				if err := d.claimOrdinaryIssue(existing.ID, existing.Identifier, "", linear.IssueRelations{}); err != nil {
					t.Fatal(err)
				}
			case "relations":
				f.Details[0].RelationsTruncated = true
			case "foreign":
				f.Details[0].RelatedIssues = []linear.RelatedIssue{{ID: "foreign", Description: goal.Marker("other", "parent")}}
			}
			if _, err := d.createGoal(context.Background(), draft); err == nil {
				t.Fatal("unsafe adoption accepted")
			}
			if len(d.goals.List()) != 0 || len(f.Created) != 0 {
				t.Fatal("failed adoption left partial reservation or writes")
			}
		})
	}
}

func TestGoalAdoptionSnapshotCannotLoseMembershipOrPlacement(t *testing.T) {
	d, _, draft, _ := adoptedGoalFixture(t)
	g, err := d.createGoal(context.Background(), draft)
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"drop", "parent", "milestone", "relation", "contract"} {
		x, _ := d.goals.Get(g.ID)
		switch mode {
		case "drop":
			x.Tasks[0].Adoption = nil
		case "parent":
			x.Tasks[0].Adoption.ParentID = "replaced"
		case "milestone":
			x.Tasks[0].Adoption.MilestoneID = "replaced"
		case "relation":
			x.Tasks[0].Adoption.Membership.ID = goal.NewID()
		case "contract":
			x.Tasks[0].Title = "rewritten"
		}
		if _, err := d.goals.Save(x); err == nil {
			t.Fatal("rewrote adoption", mode)
		}
	}
}

type adoptionReadBarrier struct {
	*linear.Fake
	id               string
	entered, release chan struct{}
}

func (f adoptionReadBarrier) LookupGoalIssue(ctx context.Context, id string) (*linear.IssueDetail, error) {
	if id == f.id {
		close(f.entered)
		<-f.release
	}
	return f.Fake.LookupGoalIssue(ctx, id)
}

func TestGoalAdoptionSerializesAgainstOrdinaryClaim(t *testing.T) {
	d, f, draft, existing := adoptedGoalFixture(t)
	barrier := adoptionReadBarrier{Fake: f, id: existing.ID, entered: make(chan struct{}), release: make(chan struct{})}
	d.lin = barrier
	done := make(chan error, 1)
	go func() { _, err := d.createGoal(context.Background(), draft); done <- err }()
	select {
	case <-barrier.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("capture did not begin")
	}
	claimed := make(chan error, 1)
	go func() {
		claimed <- d.claimOrdinaryIssue(existing.ID, existing.Identifier, existing.Description, existing.IssueRelations)
	}()
	close(barrier.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := <-claimed; err == nil {
		t.Fatal("ordinary claim slipped through adoption")
	}
	if d.inflight.Has(existing.ID) {
		t.Fatal("held ordinary claim leaked in-flight state")
	}
}

func TestGoalAdoptionStopsLateNativeWriteback(t *testing.T) {
	d, f, draft, existing := adoptedGoalFixture(t)
	if _, err := d.createGoal(context.Background(), draft); err != nil {
		t.Fatal(err)
	}
	s := session.Session{ID: "old-native", Issue: existing.Identifier, IssueUUID: existing.ID, Source: "native"}
	p := config.Project{OnSpawnStateID: "started", CommentOnSpawn: true, BlockedLabelID: "blocked", CommentOnBlocked: true}
	before := len(f.CallLog())
	d.writeBackSpawn(context.Background(), f, p, linear.Issue{ID: existing.ID}, s)
	d.writeBackState(context.Background(), f, s, "done", true, "comment", "detail", "label", wbMerged)
	d.writeBackBlocked(context.Background(), f, p, s, "old failed attempt")
	if len(f.CallLog()) != before {
		t.Fatal("old native session wrote into newly adopted issue")
	}
}

func TestGoalAdoptionPreservesExistingDependencies(t *testing.T) {
	d, f, draft, first := adoptedGoalFixture(t)
	second := linear.IssueDetail{ID: goal.NewID(), Identifier: "NOR-43", Title: draft.Tasks[1].Title, Description: draft.Tasks[1].Description, TeamID: first.TeamID, ProjectID: first.ProjectID, ParentID: "another-parent", MilestoneID: "different-release", LabelIDs: draft.Tasks[1].LabelIDs, StateType: "unstarted"}
	draft.Tasks[1].ExistingIssueID = second.ID
	f.Details = append(f.Details, second)
	original := linear.GoalRelation{ID: goal.NewID(), IssueID: first.ID, RelatedIssueID: second.ID, Type: "blocks"}
	f.GoalRelations = append(f.GoalRelations, original)
	g, err := d.createGoal(context.Background(), draft)
	if err != nil {
		t.Fatal(err)
	}
	if g.Tasks[1].Relations[0].ID != original.ID || !g.Tasks[1].Relations[0].Confirmed {
		t.Fatal("existing dependency was not adopted")
	}
	g, err = d.publishGoal(context.Background(), g.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Created) != 1 || len(f.GoalRelations) != 3 || f.GoalRelations[0] != original {
		t.Fatal("existing graph was duplicated or replaced")
	}
	if err := d.goalBindingsCurrent(context.Background(), g); err != nil {
		t.Fatal(err)
	}
	f.Details[0].BlockingIssues = []linear.Blocker{{ID: "outside", StateType: "started"}}
	if err := d.goalBindingsCurrent(context.Background(), g); err == nil {
		t.Fatal("new unfinished external prerequisite was ignored")
	}
}

func TestGoalAdoptionRejectsUndeclaredPrerequisiteAndOpenPR(t *testing.T) {
	for _, mode := range []string{"prerequisite", "open-pr"} {
		t.Run(mode, func(t *testing.T) {
			d, f, draft, existing := adoptedGoalFixture(t)
			if mode == "prerequisite" {
				f.Details[0].BlockingIssues = []linear.Blocker{{ID: "external", StateType: "started"}}
			} else {
				s := nativeSess(existing.Identifier, "dead")
				s.IssueUUID = existing.ID
				s.PR = &scm.PR{State: "OPEN"}
				d.sessions.Upsert(s)
			}
			if _, err := d.createGoal(context.Background(), draft); err == nil {
				t.Fatal("adopted unresolved external work")
			}
		})
	}
}

func TestGoalAdoptionExecutesThroughVerifiedGoalController(t *testing.T) {
	var existing linear.IssueDetail
	d, f, g, p, launched := goalExecutionFixture(t, func(draft *goal.Draft, f *linear.Fake) {
		task := &draft.Tasks[0]
		task.ExistingIssueID = goal.NewID()
		existing = linear.IssueDetail{ID: task.ExistingIssueID, Identifier: "NOR-42", Title: task.Title, Description: task.Description, TeamID: "team-1", ProjectID: "project-1", ParentID: "release-parent", MilestoneID: "release-milestone", LabelIDs: task.LabelIDs, StateType: "unstarted"}
		f.Details = append(f.Details, existing)
	})
	var err error
	g, err = d.authorizeGoal(context.Background(), goal.Authorization{ID: g.ID, Revision: g.Revision, Policy: p})
	if err != nil {
		t.Fatal(err)
	}
	d.goalVerify = nil
	d.goalCheckRun = func(context.Context, string, []string) (string, error) { return "acceptance passed", nil }
	for range 2 {
		g = goalTick(t, d, g.ID)
		g = submitRunningGoalTask(t, d, g.ID)
		if g.Execution.PendingIntegration != "" || g.State == "blocked" {
			t.Fatalf("integration did not complete: %s %s", g.State, g.Execution.Reason)
		}
	}
	g = goalTick(t, d, g.ID)
	if g.State != "ready" || *launched != 2 || g.Execution.Tasks[g.Tasks[0].ID].State != "integrated" {
		t.Fatalf("adopted task did not verify: %s %s", g.State, g.Execution.Reason)
	}
	if !reflect.DeepEqual(f.Details[0], existing) {
		t.Fatal("goal execution rewrote adopted issue")
	}
}

func TestGoalAdoptionPreventsOldNativeReviveAndMerge(t *testing.T) {
	d, _, draft, existing := adoptedGoalFixture(t)
	if _, err := d.createGoal(context.Background(), draft); err != nil {
		t.Fatal(err)
	}
	s := nativeSess(existing.Identifier, "dead")
	s.IssueUUID = existing.ID
	d.sessions.Upsert(s)
	if _, err := d.handleRevive(context.Background(), s.ID); err == nil {
		t.Fatal("revived an adopted native issue")
	}
	s.PR = greenPR(1, sha(1))
	d.prForBranch = func(context.Context, string, string) (*scm.PR, error) {
		t.Fatal("adopted PR reached merge queue")
		return nil, nil
	}
	if got := d.queueMerge(context.Background(), queueEntry{s: s}, "squash", false, nil); got != mqBlocked {
		t.Fatal("adopted native PR can auto-merge")
	}
}
