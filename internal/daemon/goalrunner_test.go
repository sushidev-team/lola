package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/goal"
	"github.com/sushidev-team/lola/internal/goalpolicy"
	"github.com/sushidev-team/lola/internal/goalwork"
	"github.com/sushidev-team/lola/internal/linear"
	"github.com/sushidev-team/lola/internal/runtime"
	"github.com/sushidev-team/lola/internal/scm"
	"github.com/sushidev-team/lola/internal/session"
	"github.com/sushidev-team/lola/internal/state"
	"github.com/sushidev-team/lola/internal/tmux"
)

func goalExecutionFixture(t *testing.T, changes ...func(*goal.Draft, *linear.Fake)) (*Daemon, *linear.Fake, goal.Goal, goalpolicy.Policy, *int) {
	t.Helper()
	d, f, draft := goalFixture(t)
	d.goalVerify = func(context.Context, string, string) error { return nil }
	source := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-c", "core.hooksPath=/dev/null", "-c", "commit.gpgsign=false", "-c", "user.name=Test", "-c", "user.email=test@localhost", "-C", source}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("fixture git: %v %s", err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "-b", "main")
	if err := os.WriteFile(filepath.Join(source, "app.txt"), []byte("initial"), 0600); err != nil {
		t.Fatal(err)
	}
	git("add", "app.txt")
	git("commit", "-m", "initial")
	p := d.cfg.ProjectByName("p1")
	p.Path = source
	p.GoalMode = true
	p.Sandbox = config.SandboxDocker
	p.DefaultBranch = "main"
	d.cfg.Defaults.GlobalCap = 2
	for _, change := range changes {
		change(&draft, f)
	}
	g, err := d.createGoal(context.Background(), draft)
	if err != nil {
		t.Fatal(err)
	}
	g, err = d.publishGoal(context.Background(), g.ID)
	if err != nil {
		t.Fatal(err)
	}
	policy := goalpolicy.Policy{Members: []goalpolicy.Member{{ID: p.Name, Project: p.Name, Source: p.Path, Repo: p.Repo, Target: "main"}}, Checks: []goalpolicy.Check{{ID: "tests", Command: "true", Image: "sha256:" + strings.Repeat("a", 64), TimeoutSeconds: 30}}, Limits: goalpolicy.Limits{Concurrency: 2, MaxTasks: 10, AttemptsPerTask: 2, ReviewRounds: 2, AgentRuns: 10, RuntimeMinutes: 60, AttemptMinutes: 10, Tokens: 100000}}
	launched := new(int)
	live := map[string]bool{}
	d.listTmuxSessions = func(context.Context) ([]tmux.Session, error) {
		var out []tmux.Session
		for id, up := range live {
			if up {
				out = append(out, tmux.Session{Name: id})
			}
		}
		return out, nil
	}
	d.goalSpawn = func(_ context.Context, g goal.Goal, id string) (session.Session, error) {
		persisted, _ := d.goals.Get(g.ID)
		a := persisted.Execution.Attempts[id]
		if a.ID == "" || a.State != "reserved" || persisted.Execution.Runs[id].ID != id {
			t.Fatal("worker launched before reservation and budget were durable")
		}
		placeholder, ok := d.sessions.Get(a.SessionID)
		if !ok || !state.HoldsSlot(placeholder.AgentState, placeholder.Delivery) {
			t.Fatal("spawn was not counted against global capacity")
		}
		*launched++
		live[a.SessionID] = true
		m := &goalwork.Manager{Root: filepath.Join(d.home, "goals")}
		dir, err := m.LaunchDirectory(g.ID, id)
		if err != nil {
			return session.Session{}, err
		}
		root := filepath.Join(dir, "workspace")
		if err := os.MkdirAll(root, 0700); err != nil {
			return session.Session{}, err
		}
		s := session.Session{ID: a.SessionID, Source: "native", Kind: session.KindGoal, GoalID: g.ID, TaskID: a.TaskID, AttemptID: id, Project: g.Project, TmuxName: a.SessionID, Worktree: root, Sandbox: "docker"}
		s.SetAgentState(state.AgentWorking, "", time.Now())
		return s, nil
	}
	return d, f, g, policy, launched
}

func TestGoalAuthorizationRunsThroughDurablePreparationAndReservation(t *testing.T) {
	d, _, g, p, launched := goalExecutionFixture(t)
	if _, err := d.authorizeGoal(context.Background(), goal.Authorization{ID: g.ID, Revision: g.Revision - 1, Policy: p}); err == nil {
		t.Fatal("stale goal authorized")
	}
	authorized, err := d.authorizeGoal(context.Background(), goal.Authorization{ID: g.ID, Revision: g.Revision, Policy: p})
	if err != nil {
		t.Fatal(err)
	}
	if authorized.State != "preparing" || authorized.Execution.Initialized || *launched != 0 {
		t.Fatal("authorization launched before durable preparation")
	}
	if err := d.runGoal(context.Background(), g.ID); err != nil {
		t.Fatal(err)
	}
	g, _ = d.goals.Get(g.ID)
	if *launched != 1 || !g.Execution.Initialized || len(g.Execution.Attempts) != 1 {
		t.Fatalf("execution did not start one eligible task: %+v", g.Execution)
	}
	if err := d.runGoal(context.Background(), g.ID); err != nil {
		t.Fatal(err)
	}
	if *launched != 1 {
		t.Fatal("dependency or existing attempt dispatched again")
	}
	// Reopen the actual journal. The live attempt remains owned after restart.
	d.goals, err = goal.Open(filepath.Join(d.home, "state", "goals.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := d.runGoal(context.Background(), g.ID); err != nil || *launched != 1 {
		t.Fatalf("restart duplicated work: %v", err)
	}
	g, _ = d.goals.Get(g.ID)
	if _, err := d.controlGoal(context.Background(), goal.ControlRequest{ID: g.ID, Revision: g.Revision, Action: "pause"}); err != nil {
		t.Fatal(err)
	}
	if err := d.runGoal(context.Background(), g.ID); err != nil {
		t.Fatal(err)
	}
	if *launched != 1 {
		t.Fatal("paused goal dispatched")
	}
}

func TestGoalScopeDriftHoldsAndResumeRequiresCurrentBindings(t *testing.T) {
	d, f, g, p, launched := goalExecutionFixture(t)
	g, err := d.authorizeGoal(context.Background(), goal.Authorization{ID: g.ID, Revision: g.Revision, Policy: p})
	if err != nil {
		t.Fatal(err)
	}
	f.Errs = map[string]error{"LookupGoalIssue": errors.New("Linear unavailable")}
	if err := d.runGoal(context.Background(), g.ID); err == nil {
		t.Fatal("Linear failure did not hold execution")
	}
	g, _ = d.goals.Get(g.ID)
	if g.State != "blocked" || *launched != 0 {
		t.Fatal("unknown scope launched a worker")
	}
	if _, err := d.controlGoal(context.Background(), goal.ControlRequest{ID: g.ID, Revision: g.Revision, Action: "resume"}); err == nil {
		t.Fatal("resume ignored missing authority")
	}
	f.Errs = nil
	g, err = d.controlGoal(context.Background(), goal.ControlRequest{ID: g.ID, Revision: g.Revision, Action: "resume"})
	if err != nil {
		t.Fatal(err)
	}
	if g.State != "preparing" || g.SyncError != "" {
		t.Fatal("resume lost incomplete initialization")
	}
	if err := d.runGoal(context.Background(), g.ID); err != nil || *launched != 1 {
		t.Fatalf("recovered execution: %v", err)
	}
}

func TestGoalSubmissionNeverReleasesDependencyWithoutVerification(t *testing.T) {
	d, _, g, p, launched := goalExecutionFixture(t)
	g, err := d.authorizeGoal(context.Background(), goal.Authorization{ID: g.ID, Revision: g.Revision, Policy: p})
	if err != nil {
		t.Fatal(err)
	}
	if err := d.runGoal(context.Background(), g.ID); err != nil {
		t.Fatal(err)
	}
	g, _ = d.goals.Get(g.ID)
	var a goal.Attempt
	for _, v := range g.Execution.Attempts {
		a = v
	}
	s, _ := d.sessions.Get(a.SessionID)
	b, _ := json.Marshal(goalwork.Submission{Goal: g.ID, Task: a.TaskID, Attempt: a.ID, Heads: map[string]string{"p1": strings.Repeat("b", 40)}, Summary: "worker says tests passed"})
	if err := os.WriteFile(filepath.Join(s.Worktree, "submission.json"), b, 0600); err != nil {
		t.Fatal(err)
	}
	if err := d.runGoal(context.Background(), g.ID); err != nil {
		t.Fatal(err)
	}
	g, _ = d.goals.Get(g.ID)
	if g.Execution.Attempts[a.ID].State != "verifying" || g.Execution.Tasks[a.TaskID].State == "integrated" || *launched != 1 {
		t.Fatal("unverified submission unlocked dependent work")
	}
	if g.Execution.Manifest["p1"] == strings.Repeat("b", 40) {
		t.Fatal("worker claim advanced the authoritative revision")
	}
}

func TestGoalSessionsStayOutsideOrdinaryGitAndMergePaths(t *testing.T) {
	d, _, _, _, _ := goalExecutionFixture(t)
	s := session.Session{ID: runtime.GoalSessionID("g", "a"), Source: "native", Kind: session.KindGoal, GoalID: "g", AttemptID: "a", Project: "p1", TmuxName: "goal", Worktree: t.TempDir(), Branch: "goal/g", PR: &scm.PR{State: "OPEN", ReviewDecision: "APPROVED", ChecksState: "pass"}}
	s.SetAgentState(state.AgentWorking, "", time.Now())
	d.sessions.Upsert(s)
	if queueMember(s) || overlapCandidate(s) || s.LinearBound() || s.OwnsBranch() {
		t.Fatal("goal session entered ordinary delivery")
	}
	manual := s
	manual.ID = "ordinary-manual"
	manual.Kind = session.KindManual
	manual.GoalID = ""
	manual.Branch = "goal/final-delivery"
	if queueMember(manual) {
		t.Fatal("opening a final goal branch manually bypassed human-only delivery")
	}
	if _, err := d.handleDiff(context.Background(), s.ID); err == nil {
		t.Fatal("host diff read worker metadata")
	}
	if _, err := d.checkpointSession(s.ID); err == nil {
		t.Fatal("host checkpoint read worker metadata")
	}
	if _, err := d.handleDev(context.Background(), s.ID, true); err == nil {
		t.Fatal("host dev command ran for goal")
	}
	d.prForBranch = func(context.Context, string, string) (*scm.PR, error) {
		t.Fatal("goal observer queried PR")
		return nil, nil
	}
	d.paneTail = func(context.Context, string, int) (string, error) { return "", nil }
	if !d.observeGoalAgent(context.Background(), s, false, time.Time{}) {
		t.Fatal("goal liveness did not update")
	}
}
