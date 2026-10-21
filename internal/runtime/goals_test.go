package runtime

import (
	"context"
	"encoding/json"
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
	"github.com/sushidev-team/lola/internal/session"
	"github.com/sushidev-team/lola/internal/tmux"
)

func goalFixture(t *testing.T) (*Native, goal.Goal, string) {
	t.Helper()
	home := t.TempDir()
	source := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-c", "core.hooksPath=/dev/null", "-c", "commit.gpgsign=false", "-c", "user.name=Test", "-c", "user.email=test@localhost", "-C", source}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git fixture: %v %s", err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "-b", "main")
	if err := os.WriteFile(filepath.Join(source, "app.txt"), []byte("initial"), 0600); err != nil {
		t.Fatal(err)
	}
	git("add", "app.txt")
	git("commit", "-m", "initial")
	g, err := goal.New(goal.Draft{Project: "api", Title: "Task", Outcome: "Improve app", Acceptance: []string{"Works"}, Tasks: []goal.TaskDraft{{Title: "Implement", Description: "Change app", Acceptance: []string{"Checked"}}}}, "team", "project")
	if err != nil {
		t.Fatal(err)
	}
	m := &goalwork.Manager{Root: filepath.Join(home, "goals")}
	repo, err := m.Initialize(context.Background(), g.ID, "api", source, "main")
	if err != nil {
		t.Fatal(err)
	}
	g.State = "planned"
	g.Parent.Confirmed = true
	g.Tasks[0].Issue.Confirmed = true
	policy := goalpolicy.Policy{Members: []goalpolicy.Member{{ID: "api", Project: "api", Source: source, Target: "main"}}, Checks: []goalpolicy.Check{{ID: "tests", Command: "true", Image: "sha256:" + strings.Repeat("a", 64), TimeoutSeconds: 60}}, Limits: goalpolicy.Limits{Concurrency: 1, MaxTasks: 5, AttemptsPerTask: 2, ReviewRounds: 2, AgentRuns: 10, RuntimeMinutes: 60, AttemptMinutes: 10, Tokens: 100000}}
	if err := g.BeginExecution(policy, map[string]string{"api": repo.Start}, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if _, err := g.ReserveAttempt(g.Tasks[0].ID, "attempt1", GoalSessionID(g.ID, "attempt1"), time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	bin, log := scriptBin(t, "tmux", "*has-session*) exit 1 ;;\n")
	lola := filepath.Join(home, "linux-lola")
	if err := os.WriteFile(lola, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	p := config.Project{Name: "api", Path: source, TeamID: "team", ProjectID: "project", GoalMode: true, Sandbox: config.SandboxDocker, PostCreate: []string{"MUST_NOT_RUN"}, Symlinks: []string{".env"}, Env: map[string]string{"SECRET": "MUST_NOT_COPY"}}
	n := &Native{Home: home, Cfg: &config.Config{Projects: []config.Project{p}, Sandbox: config.SandboxConfig{Image: "test-agent", LolaBin: lola, PassEnv: []string{"OPENAI_API_KEY", "GH_TOKEN", "LINEAR_API_KEY", "SSH_AUTH_SOCK", "CUSTOM_SECRET"}}}, Tmux: &tmux.Client{Bin: bin, Dir: home}, HookToken: func(string) string { return "session-hook-token" }}
	return n, g, log
}

func TestGoalLaunchUsesPrivateStoresAndCredentialAllowlist(t *testing.T) {
	n, g, log := goalFixture(t)
	t.Setenv("OPENAI_API_KEY", "allowed-model-credential")
	t.Setenv("GH_TOKEN", "forbidden-gh")
	t.Setenv("LINEAR_API_KEY", "forbidden-linear")
	t.Setenv("SSH_AUTH_SOCK", "forbidden-ssh")
	t.Setenv("CUSTOM_SECRET", "forbidden-custom")
	s, err := n.SpawnGoal(context.Background(), g, "attempt1")
	if err != nil {
		t.Fatal(err)
	}
	if !s.GoalManaged() || s.LinearBound() || s.OwnsBranch() {
		t.Fatal("goal session received ordinary delivery ownership")
	}
	dir := filepath.Dir(s.Worktree)
	env, err := os.ReadFile(filepath.Join(dir, "control", lolaDir, "env"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(env), "allowed-model-credential") || !strings.Contains(string(env), "session-hook-token") {
		t.Fatal("model or scoped hook credential missing")
	}
	for _, bad := range []string{"forbidden-", "MUST_NOT_COPY", "LINEAR_API_KEY=", "GH_TOKEN=", "SSH_AUTH_SOCK="} {
		if strings.Contains(string(env), bad) {
			t.Fatalf("unexpected worker credential: %s", bad)
		}
	}
	args := loggedArgs(t, log)
	for _, bad := range []string{g.Execution.Policy.Members[0].Source, "/repositories/", "MUST_NOT_RUN", "forbidden-gh"} {
		if strings.Contains(args, bad) {
			t.Fatalf("host/upstream scope leaked into launch: %s", bad)
		}
	}
	for _, want := range []string{"/attempts/", "store.git", ":ro", "--cap-drop", "--pull", "no-new-privileges"} {
		if !strings.Contains(args, want) {
			t.Fatalf("missing isolation boundary %s", want)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "launch.json")); err != nil {
		t.Fatal(err)
	}
	var receipt goalLaunch
	data, err := os.ReadFile(filepath.Join(dir, "launch.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &receipt); err != nil {
		t.Fatal(err)
	}
	for _, w := range receipt.Workspaces {
		pointer, err := os.ReadFile(filepath.Join(w.Directory, ".git"))
		if err != nil {
			t.Fatal(err)
		}
		if string(pointer) != "gitdir: "+w.GitDir+"\n" {
			t.Fatalf("Git pointer is not reachable at its mounted path: %q versus %q", pointer, w.GitDir)
		}
	}
	before := args
	again, err := n.SpawnGoal(context.Background(), g, "attempt1")
	if err != nil || again.ID != s.ID {
		t.Fatalf("recovery: %+v %v", again, err)
	}
	if loggedArgs(t, log) != before {
		t.Fatal("recovery relaunched a reserved attempt")
	}
}

func TestGoalStopAndOrdinaryLifecyclePreservePrivateWork(t *testing.T) {
	n, g, _ := goalFixture(t)
	s, err := n.SpawnGoal(context.Background(), g, "attempt1")
	if err != nil {
		t.Fatal(err)
	}
	stopped := false
	n.RemoveContainer = func(_ context.Context, id string) error {
		if id != s.ID {
			t.Fatal("stopped wrong container")
		}
		stopped = true
		return nil
	}
	if err := n.Kill(context.Background(), s, true, true); err != nil {
		t.Fatal(err)
	}
	if !stopped {
		t.Fatal("goal container was not stopped")
	}
	if _, err := os.Stat(s.Worktree); err != nil {
		t.Fatal("goal workspace deleted")
	}
	if _, err := n.Revive(context.Background(), s); err == nil {
		t.Fatal("ordinary revive bypassed goal budgets")
	}
	if _, err := n.SwitchAgent(context.Background(), s, "codex", "", ""); err == nil {
		t.Fatal("ordinary switch bypassed goal budgets")
	}
	bad := session.Session{ID: "ordinary", Kind: session.KindGoal}
	if n.StopGoal(context.Background(), bad) == nil {
		t.Fatal("unowned container stop accepted")
	}
}

func TestGoalReadOnlyMemberIsMountedReadOnlyAndBriefed(t *testing.T) {
	n, g, _ := goalFixture(t)
	task := &g.Tasks[0]
	task.Apps = []string{"api", "shared"}
	task.Paths = map[string][]string{"api": {"app.txt"}, "shared": {}}
	source := t.TempDir()
	cmd := exec.Command("git", "clone", "--no-hardlinks", g.Execution.Policy.Members[0].Source, source)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("context fixture: %v %s", err, out)
	}
	manager := &goalwork.Manager{Root: filepath.Join(n.Home, "goals")}
	shared, err := manager.Initialize(context.Background(), g.ID, "shared", source, "main")
	if err != nil {
		t.Fatal(err)
	}
	g.Execution.Policy.Members = append(g.Execution.Policy.Members, goalpolicy.Member{ID: "shared", Project: "shared", Source: source, Target: "main"})
	g.Execution.PolicyDigest, _ = g.Execution.Policy.Digest()
	g.Execution.Manifest["shared"] = shared.Start
	a := g.Execution.Attempts["attempt1"]
	a.Members = append(a.Members, "shared")
	a.Base["shared"] = shared.Start
	a.Scopes = map[string]goalpolicy.Scope{"api": {Paths: []string{"app.txt"}}, "shared": {}}
	g.Execution.Attempts[a.ID] = a
	if _, err := n.SpawnGoal(context.Background(), g, a.ID); err != nil {
		t.Fatal(err)
	}
	m := &goalwork.Manager{Root: filepath.Join(n.Home, "goals")}
	dir, err := m.LaunchDirectory(g.ID, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "launch.json"))
	if err != nil {
		t.Fatal(err)
	}
	var launch goalLaunch
	if err := json.Unmarshal(b, &launch); err != nil {
		t.Fatal(err)
	}
	for _, w := range launch.Workspaces {
		if w.Member != "shared" {
			continue
		}
		for _, p := range []string{w.Directory, w.GitDir} {
			if !strings.Contains(launch.Command, p+":"+p+":ro") {
				t.Fatalf("read-only member mounted writable: %s", p)
			}
		}
	}
	brief, err := os.ReadFile(filepath.Join(dir, "control", lolaDir, "prompt.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(brief), "Read-only context: do not change or submit") {
		t.Fatal("ownership omitted from worker brief")
	}
}
