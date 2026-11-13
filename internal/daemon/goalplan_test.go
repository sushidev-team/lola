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
	"github.com/sushidev-team/lola/internal/goalplan"
	"github.com/sushidev-team/lola/internal/goalpolicy"
	"github.com/sushidev-team/lola/internal/linear"
	"github.com/sushidev-team/lola/internal/protocol"
)

func goalPlanFixture(t *testing.T) (*Daemon, *linear.Fake, goalplan.Request, *int) {
	t.Helper()
	d, f, _ := goalFixture(t)
	source := t.TempDir()
	goalFixtureGit(t, source, "init", "-b", "main")
	if err := os.WriteFile(filepath.Join(source, "README.md"), []byte("Existing invitation architecture\n"), 0600); err != nil {
		t.Fatal(err)
	}
	goalFixtureGit(t, source, "add", "README.md")
	goalFixtureGit(t, source, "commit", "-m", "source")
	p := d.cfg.ProjectByName("p1")
	p.Path = source
	p.GoalMode = true
	p.Sandbox = config.SandboxDocker
	p.DefaultBranch = "main"
	d.cfg.Sandbox.Image = "planner-image"
	d.cfg.Defaults.Agent = "codex"
	r := goalplan.Request{ID: goal.NewID(), Project: "p1", Title: "Invitations", Outcome: "An administrator can invite a colleague", Acceptance: []string{"Recipient accepts an unexpired invitation"}, NonGoals: []string{"Billing"}, ContextPaths: map[string][]string{"p1": {"README.md"}}}
	calls := new(int)
	d.goalPlanRun = func(_ context.Context, _ string, args []string, input []byte, _ []string) ([]byte, error) {
		if args[0] != "run" {
			return nil, nil
		}
		*calls++
		var in goalplan.Input
		if err := json.Unmarshal(input, &in); err != nil {
			t.Fatal(err)
		}
		reserved, ok := d.goalPlans.Get(in.Request.ID)
		if !ok || reserved.State != "running" || reserved.GoalID == "" || reserved.InputSHA != goalplan.Digest(in) {
			t.Fatal("model call preceded durable reservation")
		}
		out := goalplan.Output{Tasks: []goalplan.ProposedTask{{TaskDraft: goal.TaskDraft{Title: "Implement invitations", Description: "Reuse existing architecture and test acceptance", Acceptance: []string{"Recipient acceptance integration test passes"}, Apps: []string{"p1"}, LabelIDs: []string{"label-api"}, Paths: map[string][]string{"p1": {"src/invitations"}}}, Covers: []int{1}}}}
		body, _ := json.Marshal(out)
		return fixtureAgentEvents(args, body), nil
	}
	return d, f, r, calls
}

func TestGoalProposalAcceptanceIsLocalDurableAndBudgeted(t *testing.T) {
	d, f, request, calls := goalPlanFixture(t)
	ctx := context.Background()
	r, err := d.proposeGoal(ctx, request)
	if err != nil || r.State != "proposed" {
		t.Fatalf("proposal: %+v %v", r, err)
	}
	if *calls != 1 || len(d.goals.List()) != 0 || len(f.Created) != 0 {
		t.Fatal("proposal created work or called model twice")
	}
	if r.Input.Members[0].Snapshot.Files[0].Content != "Existing invitation architecture\n" {
		t.Fatal("source context missing")
	}
	if _, err := d.acceptGoalPlan(ctx, goalPlanArgs{r.ID, r.Revision - 1}); err == nil {
		t.Fatal("stale proposal accepted")
	}
	g, err := d.acceptGoalPlan(ctx, goalPlanArgs{r.ID, r.Revision})
	if err != nil {
		t.Fatal(err)
	}
	if g.Execution != nil || g.State != "draft" || g.ID != r.GoalID || g.PlanningID != r.ID || g.Outcome != request.Outcome || len(f.Created) != 0 {
		t.Fatal("acceptance exceeded local goal authority")
	}
	// Simulate a crash after goal persistence but before proposal acknowledgement.
	body, _ := json.Marshal(struct {
		Version int                        `json:"version"`
		Records map[string]goalplan.Record `json:"records"`
	}{1, map[string]goalplan.Record{r.ID: r}})
	file := filepath.Join(d.home, "state", "goal-plans.json")
	if err := os.WriteFile(file, body, 0600); err != nil {
		t.Fatal(err)
	}
	d.goalPlans, err = goalplan.Open(file)
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		recovered, err := d.acceptGoalPlan(ctx, goalPlanArgs{r.ID, r.Revision})
		if err != nil || recovered.ID != g.ID {
			t.Fatal("acceptance recovery failed", err)
		}
	}
	if len(d.goals.List()) != 1 || *calls != 1 {
		t.Fatal("recovery duplicated work")
	}
	retried, err := d.proposeGoal(ctx, request)
	if err != nil || retried.State != "accepted" || *calls != 1 {
		t.Fatal("paid call repeated after lost response", err)
	}
	g, err = d.publishGoal(ctx, g.ID)
	if err != nil {
		t.Fatal(err)
	}
	p := d.cfg.ProjectByName("p1")
	policy := goalpolicy.Policy{Members: []goalpolicy.Member{{ID: p.Name, Project: p.Name, Source: p.Path, Repo: p.Repo, Target: "main"}}, Checks: []goalpolicy.Check{{ID: "tests", Command: "true", Image: "sha256:" + strings.Repeat("a", 64), TimeoutSeconds: 30}}, Limits: goalpolicy.Limits{Concurrency: 1, MaxTasks: 10, AttemptsPerTask: 2, ReviewRounds: 2, AgentRuns: 5, RuntimeMinutes: 60, AttemptMinutes: 10, Tokens: 10000}}
	g, err = d.authorizeGoal(ctx, goal.Authorization{ID: g.ID, Revision: g.Revision, Policy: policy})
	if err != nil {
		t.Fatal(err)
	}
	if len(g.Execution.Runs) != 1 || g.Execution.Runs[r.ID].Kind != "coordinator" || !g.Execution.Runs[r.ID].ReservedAt.Equal(r.StartedAt) {
		t.Fatal("accepted planning cost not reserved")
	}
	// Commands return durable data through the normal socket handler.
	args, _ := json.Marshal(protocol.GoalArgs{ID: r.ID})
	if response := d.handle(ctx, protocol.Request{Cmd: "goalProposal", Args: args}); response.Error != "" {
		t.Fatal(response.Error)
	}
}

func TestGoalProposalRejectsSourceOrRoutingDrift(t *testing.T) {
	for _, mode := range []string{"source", "labels", "config"} {
		t.Run(mode, func(t *testing.T) {
			d, f, request, _ := goalPlanFixture(t)
			r, err := d.proposeGoal(context.Background(), request)
			if err != nil {
				t.Fatal(err)
			}
			p := d.cfg.ProjectByName("p1")
			switch mode {
			case "source":
				if err := os.WriteFile(filepath.Join(p.Path, "README.md"), []byte("new committed architecture"), 0600); err != nil {
					t.Fatal(err)
				}
				goalFixtureGit(t, p.Path, "add", "README.md")
				goalFixtureGit(t, p.Path, "commit", "-m", "changed")
			case "labels":
				f.LabelsByTeam[p.TeamID] = []linear.Label{{ID: "other", Name: "other"}}
			case "config":
				p.Repo = "different/repository"
			}
			if _, err := d.acceptGoalPlan(context.Background(), goalPlanArgs{r.ID, r.Revision}); err == nil {
				t.Fatal("stale proposal accepted")
			}
			if len(d.goals.List()) != 0 || len(f.Created) != 0 {
				t.Fatal("rejected proposal created work")
			}
		})
	}
}

func TestGoalProposalUsesActualStackRouting(t *testing.T) {
	d, _, r, calls := goalPlanFixture(t)
	p := d.cfg.ProjectByName("p1")
	source := p.Path
	p.Kind = config.ProjectKindStack
	p.Path = ""
	d.cfg.Projects = append(d.cfg.Projects, config.Project{Name: "api", Stack: "p1", Path: source, DefaultBranch: "main", Labels: []string{"api"}}, config.Project{Name: "shared", Stack: "p1", Path: source, DefaultBranch: "main"})
	r.ContextPaths = map[string][]string{"api": {"README.md"}}
	base := d.goalPlanRun
	d.goalPlanRun = func(ctx context.Context, bin string, args []string, input []byte, env []string) ([]byte, error) {
		b, err := base(ctx, bin, args, input, env)
		if err != nil || args[0] != "run" {
			return b, err
		}
		var out goalplan.Output
		_ = json.Unmarshal(fixtureAgentText(args, b), &out)
		out.Tasks[0].Apps = []string{"api"}
		out.Tasks[0].Paths = map[string][]string{"api": {"src/invitations"}}
		body, _ := json.Marshal(out)
		return fixtureAgentEvents(args, body), nil
	}
	failed, err := d.proposeGoal(context.Background(), r)
	if err != nil || failed.State != "failed" || !strings.Contains(failed.Error, "including always-included") {
		t.Fatalf("routing was not enforced: %+v %v", failed, err)
	}
	if _, err := d.proposeGoal(context.Background(), r); err != nil || *calls != 1 {
		t.Fatal("invalid output automatically retried")
	}
	r.ID = goal.NewID()
	d.goalPlanRun = func(ctx context.Context, bin string, args []string, input []byte, env []string) ([]byte, error) {
		b, err := base(ctx, bin, args, input, env)
		if err != nil || args[0] != "run" {
			return b, err
		}
		var out goalplan.Output
		_ = json.Unmarshal(fixtureAgentText(args, b), &out)
		out.Tasks[0].Apps = []string{"api", "shared"}
		out.Tasks[0].Paths = map[string][]string{"api": {"src/invitations"}, "shared": {}}
		body, _ := json.Marshal(out)
		return fixtureAgentEvents(args, body), nil
	}
	good, err := d.proposeGoal(context.Background(), r)
	if err != nil || good.State != "proposed" {
		t.Fatalf("valid stack rejected: %+v %v", good, err)
	}
	if _, err := d.acceptGoalPlan(context.Background(), goalPlanArgs{good.ID, good.Revision}); err != nil {
		t.Fatal(err)
	}
}

func TestGoalProposalOneCallAndInterruptedRecovery(t *testing.T) {
	d, _, r, calls := goalPlanFixture(t)
	entered, release := make(chan struct{}), make(chan struct{})
	base := d.goalPlanRun
	d.goalPlanRun = func(ctx context.Context, bin string, args []string, input []byte, env []string) ([]byte, error) {
		if args[0] == "run" {
			close(entered)
			<-release
		}
		return base(ctx, bin, args, input, env)
	}
	done := make(chan error, 1)
	go func() { _, err := d.proposeGoal(context.Background(), r); done <- err }()
	<-entered
	if running, err := d.proposeGoal(context.Background(), r); err != nil || running.State != "running" {
		t.Fatal("same ID did not return existing call", err)
	}
	other := r
	other.ID = goal.NewID()
	if _, err := d.proposeGoal(context.Background(), other); err == nil {
		t.Fatal("parallel proposal bypassed coordinator cap")
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if *calls != 1 {
		t.Fatal("duplicate call")
	}
	// A reserved call with no durable result must never be rerun on restart.
	in := goalplan.Input{Request: other}
	if err := in.Request.Normalize(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := d.goalPlans.Reserve(in, "codex", "image"); err != nil {
		t.Fatal(err)
	}
	var err error
	d.goalPlans, err = goalplan.Open(filepath.Join(d.home, "state", "goal-plans.json"))
	if err != nil {
		t.Fatal(err)
	}
	d.goalPlanRun = func(context.Context, string, []string, []byte, []string) ([]byte, error) {
		return nil, errors.New("must not run")
	}
	interrupted, err := d.proposeGoal(context.Background(), other)
	if err != nil || interrupted.State != "interrupted" {
		t.Fatal("restart reran a reserved call", err)
	}
}

func TestGoalProposalsReadOnly(t *testing.T) {
	d, f, request, calls := goalPlanFixture(t)
	ctx := context.Background()
	r, err := d.proposeGoal(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	for _, project := range []string{"p1", "other"} {
		args, _ := json.Marshal(map[string]string{"project": project})
		response := d.handle(ctx, protocol.Request{Cmd: "goalProposals", Args: args})
		if response.Error != "" {
			t.Fatal(response.Error)
		}
		var got []goalplan.Summary
		if err := json.Unmarshal(response.Data, &got); err != nil {
			t.Fatal(err)
		}
		if project == "p1" && (len(got) != 1 || got[0].ID != r.ID) {
			t.Fatalf("missing saved proposal: %+v", got)
		}
		if project == "other" && len(got) != 0 {
			t.Fatal("cross-project proposal")
		}
	}
	if *calls != 1 || len(d.goals.List()) != 0 || len(f.Created) != 0 {
		t.Fatal("picker triggered work")
	}
}
