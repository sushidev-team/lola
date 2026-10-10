package daemon

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/linear"
	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/session"
	"github.com/sushidev-team/lola/internal/state"
)

// --- dispatch: dependency gating -------------------------------------------

func TestDependencyHold(t *testing.T) {
	merged := map[string]bool{"FE-9": true}
	cases := []struct {
		name string
		is   linear.Issue
		want string
	}{
		{"no deps", linear.Issue{}, ""},
		{"blocker done", linear.Issue{BlockedBy: []linear.Blocker{{Identifier: "FE-1", StateType: "completed"}}}, ""},
		{"blocker canceled", linear.Issue{BlockedBy: []linear.Blocker{{Identifier: "FE-1", StateType: "canceled"}}}, ""},
		{"blocker merged but not yet done", linear.Issue{BlockedBy: []linear.Blocker{{Identifier: "FE-9", StateType: "started"}}}, ""},
		{"blockers open", linear.Issue{BlockedBy: []linear.Blocker{
			{Identifier: "FE-1", StateType: "started"},
			{Identifier: "FE-2", StateType: "completed"},
			{Identifier: "FE-3", StateType: "unstarted"},
		}}, "blocked-by FE-1,FE-3"},
		{"open children", linear.Issue{OpenChildren: 2}, "sub-issues open (2)"},
	}
	for _, c := range cases {
		if got := dependencyHold(c.is, merged); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

func TestTickHoldsBlockedIssuesAndDispatchesTheRest(t *testing.T) {
	free := testIssue("FE-1", 1, "2024-01-01T00:00:00Z")
	blocked := testIssue("FE-2", 1, "2024-01-02T00:00:00Z")
	blocked.BlockedBy = []linear.Blocker{{ID: "uuid-FE-1", Identifier: "FE-1", StateType: "unstarted"}}
	unblocked := testIssue("FE-3", 1, "2024-01-03T00:00:00Z")
	unblocked.BlockedBy = []linear.Blocker{{ID: "uuid-FE-0", Identifier: "FE-0", StateType: "started"}}
	parent := testIssue("FE-4", 1, "2024-01-04T00:00:00Z")
	parent.OpenChildren = 1

	fake := &linear.Fake{Issues: []linear.Issue{free, blocked, unblocked, parent}}
	nat := &fakeNative{}
	d := newTestDaemon(t, testConfig(labelPoll("p1")), fake, nat)
	// FE-0's PR merged under lola, though Linear still says "started".
	d.sessions.Upsert(session.Session{ID: "p1-fe-0", Source: "native", Issue: "FE-0",
		AgentState: state.AgentExited, Delivery: state.DeliveryMerged})

	res, err := d.tick(context.Background(), "p1", false)
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	if m := findMatch(t, res, "FE-2"); m.Action != "skipped" || m.Reason != "blocked-by FE-1" {
		t.Errorf("FE-2 = %+v, want skipped/blocked-by FE-1", m)
	}
	if m := findMatch(t, res, "FE-4"); m.Action != "skipped" || !strings.HasPrefix(m.Reason, "sub-issues open") {
		t.Errorf("FE-4 = %+v, want skipped/sub-issues open", m)
	}
	for _, id := range []string{"FE-1", "FE-3"} {
		if m := findMatch(t, res, id); m.Action != "spawned" {
			t.Errorf("%s = %+v, want spawned", id, m)
		}
	}
	if got := len(nat.spawnCalls()); got != 2 {
		t.Errorf("spawns = %d, want 2", got)
	}
	// A held issue writes no seen entry: it must re-qualify once unblocked.
	seen, _ := d.seen.load("p1")
	if _, ok := seen[blocked.ID]; ok {
		t.Error("a blocked issue must not be marked seen")
	}
}

// --- planning pass -----------------------------------------------------------

func planParent() linear.IssueDetail {
	return linear.IssueDetail{
		ID: "uuid-FE-10", Identifier: "FE-10", Title: "Big thing", Description: "do it all",
		TeamID: "team-1", ProjectID: "lin-proj", CycleID: "cyc-old", StateID: "st-backlog",
		AssigneeID: "user-parent", LabelIDs: []string{"lbl-area", "lbl-trigger"},
	}
}

func TestHandlePlanReturnsValidatedProposal(t *testing.T) {
	fake := &linear.Fake{Details: []linear.IssueDetail{planParent()}}
	d := newTestDaemon(t, testConfig(labelPoll("p1")), fake, &fakeNative{})
	var gotStdin string
	d.planSummarize = func(_ context.Context, _, stdin string) (string, error) {
		gotStdin = stdin
		return "```json\n{\"steps\":[{\"title\":\"Schema\",\"description\":\"add table\"},{\"title\":\"API\",\"blockedBy\":[1,1]}]}\n```", nil
	}
	data, err := d.handlePlan(context.Background(), protocol.PlanArgs{Issue: "FE-10"})
	if err != nil {
		t.Fatalf("handlePlan: %v", err)
	}
	if data.Issue != "FE-10" || len(data.Steps) != 2 || !reflect.DeepEqual(data.Steps[1].BlockedBy, []int{1}) {
		t.Errorf("plan = %+v", data)
	}
	if !strings.Contains(gotStdin, "do it all") {
		t.Errorf("issue text must reach the planner on stdin, got %q", gotStdin)
	}
	if n := countCalls(fake.CallNames(), "CreateIssue"); n != 0 {
		t.Errorf("cmd=plan must create nothing, CreateIssue called %d times", n)
	}
}

func TestHandlePlanRefusesDecomposedIssue(t *testing.T) {
	parent := planParent()
	parent.Children = 3
	d := newTestDaemon(t, testConfig(labelPoll("p1")), &linear.Fake{Details: []linear.IssueDetail{parent}}, &fakeNative{})
	d.planSummarize = func(context.Context, string, string) (string, error) {
		t.Fatal("planner must not run for an already-decomposed issue")
		return "", nil
	}
	if _, err := d.handlePlan(context.Background(), protocol.PlanArgs{Issue: "FE-10"}); err == nil {
		t.Fatal("want an error for an issue that already has sub-issues")
	}
}

func applySteps() []protocol.PlanStep {
	return []protocol.PlanStep{
		{Title: "Schema", Description: "add table"},
		{Title: "API", BlockedBy: []int{1}},
		{Title: "UI", BlockedBy: []int{1, 2}},
	}
}

func TestHandlePlanApplyCreatesSubIssuesAndRelations(t *testing.T) {
	fake := &linear.Fake{Details: []linear.IssueDetail{planParent()}}
	d := newTestDaemon(t, testConfig(labelPoll("p1")), fake, &fakeNative{})

	res, err := d.handlePlanApply(context.Background(), protocol.PlanApplyArgs{Issue: "FE-10", Steps: applySteps()})
	if err != nil {
		t.Fatalf("handlePlanApply: %v", err)
	}
	if res.Project != "p1" || !reflect.DeepEqual(res.Created, []string{"NEW-1", "NEW-2", "NEW-3"}) {
		t.Errorf("result = %+v", res)
	}
	for i, in := range fake.Created {
		if in.ParentID != "uuid-FE-10" || in.TeamID != "team-1" {
			t.Errorf("step %d placement = %+v", i+1, in)
		}
	}
	if !strings.Contains(fake.Created[2].Description, "Step 3 of 3 of FE-10") {
		t.Errorf("description footer missing: %q", fake.Created[2].Description)
	}
	wantRel := [][2]string{
		{"created-uuid-1", "created-uuid-2"},
		{"created-uuid-1", "created-uuid-3"},
		{"created-uuid-2", "created-uuid-3"},
	}
	if !reflect.DeepEqual(fake.Relations, wantRel) {
		t.Errorf("relations = %v, want %v", fake.Relations, wantRel)
	}
	// No step may match the poll before its blockers are linked: each one is
	// linked right after creation, and a blocked step gets its trigger label
	// only after its links exist.
	wantCalls := []string{
		"IssueDetail",
		"CreateIssue",
		"CreateIssue", "CreateBlocksRelation", "SetIssueLabels",
		"CreateIssue", "CreateBlocksRelation", "CreateBlocksRelation", "SetIssueLabels",
		"SetIssueLabels", // the parent's trigger label, below
	}
	if got := fake.CallNames(); !reflect.DeepEqual(got, wantCalls) {
		t.Errorf("calls = %v, want %v", got, wantCalls)
	}
	if !slices.Contains(fake.Created[0].LabelIDs, "lbl-trigger") {
		t.Errorf("unblocked step 1 labels = %v, want the trigger label", fake.Created[0].LabelIDs)
	}
	for i := 1; i < 3; i++ {
		if slices.Contains(fake.Created[i].LabelIDs, "lbl-trigger") {
			t.Errorf("blocked step %d created with the trigger label: %v", i+1, fake.Created[i].LabelIDs)
		}
		id := fmt.Sprintf("created-uuid-%d", i+1)
		if got := fake.LabelIDsByIssue[id]; !slices.Contains(got, "lbl-trigger") {
			t.Errorf("blocked step %d labels after linking = %v, want the trigger label", i+1, got)
		}
	}
	// The parent's trigger label is dropped so it never dispatches itself.
	if got := fake.LabelIDsByIssue["uuid-FE-10"]; !reflect.DeepEqual(got, []string{"lbl-area"}) {
		t.Errorf("parent labels = %v, want [lbl-area]", got)
	}
}

func TestHandlePlanApplyRefusals(t *testing.T) {
	decomposed := planParent()
	decomposed.Children = 1
	other := labelPoll("p2")
	elsewhere := labelPoll("p3")
	elsewhere.TeamID = "team-2"

	cases := []struct {
		name    string
		cfg     *config.Config
		parent  linear.IssueDetail
		args    protocol.PlanApplyArgs
		wantErr string
	}{
		{"already decomposed", testConfig(labelPoll("p1")), decomposed,
			protocol.PlanApplyArgs{Issue: "FE-10", Steps: applySteps()}, "already has"},
		{"ambiguous project", testConfig(labelPoll("p1"), other), planParent(),
			protocol.PlanApplyArgs{Issue: "FE-10", Steps: applySteps()}, "pass --project"},
		{"project on another team", testConfig(labelPoll("p1"), elsewhere), planParent(),
			protocol.PlanApplyArgs{Issue: "FE-10", Project: "p3", Steps: applySteps()}, "different Linear team"},
		{"forward dependency", testConfig(labelPoll("p1")), planParent(),
			protocol.PlanApplyArgs{Issue: "FE-10", Steps: []protocol.PlanStep{{Title: "a", BlockedBy: []int{2}}, {Title: "b"}}}, "not an earlier step"},
	}
	for _, c := range cases {
		fake := &linear.Fake{Details: []linear.IssueDetail{c.parent}}
		d := newTestDaemon(t, c.cfg, fake, &fakeNative{})
		_, err := d.handlePlanApply(context.Background(), c.args)
		if err == nil || !strings.Contains(err.Error(), c.wantErr) {
			t.Errorf("%s: err = %v, want containing %q", c.name, err, c.wantErr)
		}
		if len(fake.Created) != 0 {
			t.Errorf("%s: created %d issue(s) on a refused apply", c.name, len(fake.Created))
		}
	}
}

func TestHandlePlanApplyReportsPartialCreation(t *testing.T) {
	fake := &linear.Fake{Details: []linear.IssueDetail{planParent()}}
	d := newTestDaemon(t, testConfig(labelPoll("p1")), fake, &fakeNative{})
	fake.Errs = map[string]error{"CreateBlocksRelation": errors.New("boom")}
	_, err := d.handlePlanApply(context.Background(), protocol.PlanApplyArgs{Issue: "FE-10", Steps: applySteps()})
	if err == nil || !strings.Contains(err.Error(), "already created: NEW-1, NEW-2)") {
		t.Errorf("err = %v, want it to name the issues already created", err)
	}
	// Creation stops at the first failed link, and the unlinked step never got
	// its trigger label, so it cannot dispatch unblocked.
	if len(fake.Created) != 2 {
		t.Errorf("created %d step(s), want 2", len(fake.Created))
	}
	if got := fake.LabelIDsByIssue["created-uuid-2"]; got != nil {
		t.Errorf("unlinked step 2 got labels %v", got)
	}
}

func TestSubIssuePlacementMatchesThePollFilter(t *testing.T) {
	parent := planParent()
	parent.LabelIDs = []string{"lbl-area", "lbl-sent", "lbl-blocked"}

	p := labelPoll("p1")
	p.ProjectID = "lin-proj-2"
	p.CycleMode = "active"
	p.AssigneeMode = "me"
	p.StateIDs = []string{"st-todo", "st-ready"}
	p.MatchLabels = []string{"lbl-trigger", "lbl-agent"}
	p.MatchMode = "all"
	p.BlockedLabelID = "lbl-blocked"

	in := subIssuePlacement(p, parent, "cyc-active", "user-me")
	want := linear.IssueCreate{
		TeamID: "team-1", ParentID: "uuid-FE-10", ProjectID: "lin-proj-2", CycleID: "cyc-active",
		AssigneeID: "user-me", StateID: "st-todo",
		LabelIDs: []string{"lbl-area", "lbl-trigger", "lbl-agent"},
	}
	if !reflect.DeepEqual(in, want) {
		t.Errorf("placement = %+v\nwant        %+v", in, want)
	}
	// The placed sub-issue satisfies the filter the poll queries with.
	f := linear.BuildIssueFilter(p, "cyc-active", "user-me")
	if f["cycle"].(map[string]any)["id"].(map[string]any)["eq"] != in.CycleID {
		t.Error("cycle does not satisfy the poll filter")
	}

	// Open filter fields fall back to the parent; a matching state is kept.
	open := labelPoll("p1")
	open.StateIDs = []string{"st-backlog"}
	in = subIssuePlacement(open, planParent(), "", "")
	if in.ProjectID != "lin-proj" || in.CycleID != "cyc-old" || in.AssigneeID != "user-parent" || in.StateID != "st-backlog" {
		t.Errorf("open-filter placement = %+v", in)
	}
}
