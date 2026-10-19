package runtime

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sushidev-team/lola/internal/agent"
	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/gitguard"
	"github.com/sushidev-team/lola/internal/session"
	"github.com/sushidev-team/lola/internal/usage"
)

func sandboxFixture(t *testing.T) *fixture {
	t.Helper()
	f := newFixture(t, "", "")
	f.p.Sandbox = config.SandboxDocker
	f.p.SandboxPorts = []string{"3000", "8080:80"}
	f.n.Cfg.Projects[0] = f.p
	f.n.Cfg.Sandbox = config.SandboxConfig{Image: "acme/agent:1", LolaBin: "/opt/lola-linux", PassEnv: []string{"LOLA_TEST_PASS", "LOLA_TEST_UNSET"}}
	f.n.HookToken = func(id string) string { return "tok-" + id }
	return f
}

func TestSandboxedLaunchRunsTheAgentInDocker(t *testing.T) {
	f := sandboxFixture(t)
	id := "lola-nori-eng-42"
	dir := filepath.Join(f.root, "nori", id)
	if full := f.n.launchLine("nori", id, agent.Claude, defaultLaunchPrompt(id), "", false); !strings.Contains(full, "docker") {
		t.Fatalf("sandboxed launch does not run docker:\n%s", full)
	}
	inner := "set -a; . ./.lola/env; set +a; exec claude"
	line := f.n.sandboxLine(&f.p, id, agent.Claude, inner)

	for _, must := range []string{
		"docker rm -f " + id + " >/dev/null 2>&1; ",
		"exec docker run --rm -it --init --name " + id,
		"--label dev.sushi.lola.session=" + id,
		"--add-host host.docker.internal:host-gateway",
		"--security-opt no-new-privileges",
		"-w " + shQuote(dir) + " -v " + shQuote(dir+":"+dir),
		// git stores: rw, with config + hooks overlaid read-only.
		// …asked of the main checkouts, which the agent cannot rewrite
		`lola_gcd_0=$(git -C ` + shQuote(f.repo) + ` rev-parse --path-format=absolute --git-common-dir`,
		` && test -d "$lola_gcd_0" || { echo 'lola: cannot resolve the git directory of this project'`,
		`${lola_gcd_0:+"$lola_gcd_0/config:$lola_gcd_0/config:ro"}`,
		`${lola_gcd_0:+"$lola_gcd_0/hooks:$lola_gcd_0/hooks:ro"}`,
		`${lola_gcd_0:+"$lola_gcd_0:$lola_gcd_0"}`,
		// the Linux lola, read-only, at the host lola's path
		"-v /opt/lola-linux:/usr/local/bin/lola:ro",
		"-p 127.0.0.1:3000:3000", "-p 127.0.0.1:8080:80",
		// the inner line is the host launch, unchanged, after the prelude
		"--entrypoint sh acme/agent:1 -c " + shQuote(sandboxPrelude(dir, agent.Claude, usageSlug(dir))+inner),
	} {
		if !strings.Contains(line, must) {
			t.Errorf("launch line missing %q:\n%s", must, line)
		}
	}
	// Nothing else of the host is mounted: no $HOME, no ~/.lola.
	home, _ := os.UserHomeDir()
	if strings.Contains(line, "-v '"+home+":") || strings.Contains(line, os.Getenv("LOLA_HOME")+":") {
		t.Errorf("launch line mounts a home directory:\n%s", line)
	}

	// A project that is not sandboxed launches exactly as before.
	f.p.Sandbox = ""
	f.n.Cfg.Projects[0] = f.p
	if plain := f.n.launchLine("nori", id, agent.Claude, defaultLaunchPrompt(id), "", false); strings.Contains(plain, "docker") {
		t.Errorf("host launch mentions docker:\n%s", plain)
	}
}

func usageSlug(dir string) string { return usage.SlugDir(usage.ProjectsRoot(), dir) }

func TestSandboxedEnvFileCarriesTheBridgeAndCredentials(t *testing.T) {
	f := sandboxFixture(t)
	t.Setenv("LOLA_TEST_PASS", "s3cr3t'x")
	os.Unsetenv("LOLA_TEST_UNSET")
	id := "lola-nori-eng-42"
	dir := filepath.Join(f.root, "nori", id)

	env := string(f.n.envFile(f.p, id, dir, agent.Codex))
	for _, must := range []string{
		"LOLA_SANDBOX=docker\n",
		"LOLA_HOOK_ADDR=host.docker.internal:7718\n",
		"LOLA_HOOK_TOKEN=tok-" + id + "\n",
		"CODEX_HOME=" + shQuote(filepath.Join(dir, ".lola", "codex")) + "\n",
		`LOLA_TEST_PASS='s3cr3t'\''x'` + "\n",
	} {
		if !strings.Contains(env, must) {
			t.Errorf("env missing %q:\n%s", must, env)
		}
	}
	if strings.Contains(env, "LOLA_TEST_UNSET") {
		t.Errorf("unset pass_env exported:\n%s", env)
	}
	if strings.Count(env, "CODEX_HOME=") != 1 {
		t.Errorf("host CODEX_HOME leaked into a sandboxed env:\n%s", env)
	}
	if claude := string(f.n.envFile(f.p, id, dir, agent.Claude)); !strings.Contains(claude, "CLAUDE_CONFIG_DIR="+shQuote(filepath.Join(dir, ".lola", "claude"))) {
		t.Errorf("claude env missing CLAUDE_CONFIG_DIR:\n%s", claude)
	}

	f.p.Sandbox = ""
	f.n.Cfg.Projects[0] = f.p
	if host := string(f.n.envFile(f.p, id, dir, agent.Claude)); strings.Contains(host, "LOLA_HOOK") || strings.Contains(host, "LOLA_TEST_PASS") {
		t.Errorf("host session got sandbox env:\n%s", host)
	}
}

func TestSpawnRecordsTheSandboxAndKillRemovesTheContainer(t *testing.T) {
	f := sandboxFixture(t)
	var removed []string
	f.n.RemoveContainer = func(_ context.Context, name string) error {
		removed = append(removed, name)
		return nil
	}
	s, err := f.n.Spawn(context.Background(), f.p, issueENG42(), "")
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if s.Sandbox != config.SandboxDocker {
		t.Fatalf("Sandbox = %q", s.Sandbox)
	}
	if err := f.n.Kill(context.Background(), s, false, false); err != nil {
		t.Fatalf("Kill: %v", err)
	}
	// Recorded sandbox wins even after the project stopped being sandboxed.
	f.p.Sandbox = ""
	f.n.Cfg.Projects[0] = f.p
	if err := f.n.Kill(context.Background(), s, false, false); err != nil {
		t.Fatalf("Kill: %v", err)
	}
	if strings.Join(removed, ",") != s.ID+","+s.ID {
		t.Fatalf("removed containers = %v", removed)
	}
	// A host session never touches docker.
	removed = nil
	if err := f.n.Kill(context.Background(), session.Session{ID: "lola-nori-eng-1", Project: "nori"}, false, false); err != nil {
		t.Fatal(err)
	}
	if len(removed) != 0 {
		t.Fatalf("host kill removed containers: %v", removed)
	}
}

func TestSandboxMountsOnlyTheSessionsOwnContextFolder(t *testing.T) {
	f := sandboxFixture(t)
	id := "lola-nori-eng-42"
	dir := filepath.Join(f.root, "nori", id)
	if err := os.MkdirAll(filepath.Join(dir, ".lola"), 0o700); err != nil {
		t.Fatal(err)
	}
	ctxDir := f.n.setupContext(dir, "nori", "eng-42")
	if ctxDir == "" {
		t.Fatal("context not set up")
	}
	target := ContextDir(f.n.Home, "nori", "eng-42")
	if line := f.n.sandboxLine(&f.p, id, agent.Claude, "x"); !strings.Contains(line, "-v "+shQuote(target+":"+target)) {
		t.Fatalf("context folder not mounted:\n%s", line)
	}
	// An agent re-points the link at somewhere it should not reach.
	link := filepath.Join(dir, ".lola", "context")
	home := t.TempDir()
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(home, link); err != nil {
		t.Fatal(err)
	}
	if line := f.n.sandboxLine(&f.p, id, agent.Claude, "x"); strings.Contains(line, home) {
		t.Fatalf("a re-pointed context link got mounted:\n%s", line)
	}
}

// A sandboxed "web" and a host "web-eng" share the ID prefix of web's ENG-42
// session ("lola-web-eng-42"). The project is passed explicitly, so web's
// session runs in docker whatever the ID looks like.
func TestSandboxDoesNotHingeOnTheSessionIDPrefix(t *testing.T) {
	f := sandboxFixture(t)
	web := f.p
	web.Name = "web"
	hostProj := config.Project{Name: "web-eng", Path: t.TempDir(), DefaultBranch: "main"}
	f.n.Cfg.Projects = []config.Project{web, hostProj}
	id := SessionID("web", "ENG-42")
	if n := f.n.projectForSessionName(id); n != "web-eng" {
		t.Fatalf("precondition: the ID prefix is ambiguous (got %q)", n)
	}
	if line := f.n.launchLine("web", id, agent.Claude, "go", "", false); !strings.Contains(line, "docker") {
		t.Fatalf("sandboxed project launched on the host:\n%s", line)
	}
	if env := string(f.n.envFile(web, id, "/w", agent.Claude)); !strings.Contains(env, "LOLA_HOOK_ADDR") {
		t.Fatalf("sandboxed project got a host env:\n%s", env)
	}
	if line := f.n.launchLine("web-eng", id, agent.Claude, "go", "", false); strings.Contains(line, "docker") {
		t.Fatalf("host project launched in docker:\n%s", line)
	}
}

func TestSandboxedSpawnPinsItsGitLayoutBeforeTheAgentRuns(t *testing.T) {
	f := sandboxFixture(t)
	s, err := f.n.Spawn(context.Background(), f.p, issueENG42(), "")
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	gitDir := filepath.Join(f.commonDir, "worktrees", "wt")
	if s.GitPin == nil || s.GitPin.GitDir != gitDir || s.GitPin.CommonDir != f.commonDir {
		t.Fatalf("GitPin = %+v, want %s / %s", s.GitPin, gitDir, f.commonDir)
	}
	dir := filepath.Join(f.root, "nori", s.ID)
	if at, _, ok := gitguard.Lookup(dir); !ok || at != dir {
		t.Fatalf("worktree not registered with gitguard")
	}
	// The admin dir's config.worktree is overlaid read-only in the container.
	cw := filepath.Join(gitDir, "config.worktree")
	if line := f.n.sandboxLine(&f.p, s.ID, agent.Claude, "x"); !strings.Contains(line, "-v "+shQuote(cw+":"+cw+":ro")) {
		t.Fatalf("config.worktree not overlaid read-only:\n%s", line)
	}
	if ptr := filepath.Join(dir, ".git"); !strings.Contains(f.n.sandboxLine(&f.p, s.ID, agent.Claude, "x"), "-v "+shQuote(ptr+":"+ptr+":ro")) {
		t.Fatal(".git pointer not overlaid read-only")
	}
	if err := f.n.Kill(context.Background(), s, true, false); err != nil {
		t.Fatalf("Kill: %v", err)
	}
	if gitguard.Pinned(dir) {
		t.Fatal("pin outlived the worktree")
	}
}

// A sandboxed STACK session mounts its folder and the git store of each app it
// actually checked out — never an app the spawn's label selection left out —
// and has no checkout of its own whose store must resolve.
func TestSandboxedStackMountsOnlyItsCheckedOutApps(t *testing.T) {
	f := newFixture(t, "", "")
	stack := config.Project{Name: "nori", Kind: config.ProjectKindStack, Sandbox: config.SandboxDocker}
	api := config.Project{Name: "nori-api", Stack: "nori", Path: "/src/api", Symlinks: []string{".env"}}
	web := config.Project{Name: "nori-web", Stack: "nori", Path: "/src/web", Labels: []string{"apps/web"}}
	f.n.Cfg = &config.Config{
		Projects: []config.Project{stack, api, web},
		Sandbox:  config.SandboxConfig{Image: "acme/agent:1", LolaBin: "/opt/lola-linux"},
	}
	id := "lola-nori-eng-42"
	dir := filepath.Join(f.root, "nori", id)
	if err := os.MkdirAll(StackAppDir(dir, "nori-api"), 0o755); err != nil {
		t.Fatal(err)
	}
	line := f.n.sandboxLine(&stack, id, agent.Claude, "exec claude")
	for _, must := range []string{
		"-w " + shQuote(dir) + " -v " + shQuote(dir+":"+dir),
		`lola_gcd_0=$(git -C /src/api rev-parse --path-format=absolute --git-common-dir 2>/dev/null); `,
		`${lola_gcd_0:+"$lola_gcd_0/hooks:$lola_gcd_0/hooks:ro"}`,
	} {
		if !strings.Contains(line, must) {
			t.Errorf("stack launch line missing %q:\n%s", must, line)
		}
	}
	if strings.Contains(line, "/src/web") || strings.Contains(line, "lola_gcd_1") {
		t.Errorf("an app the session did not check out is mounted:\n%s", line)
	}
	if strings.Contains(line, "cannot resolve the git directory") {
		t.Errorf("a stack has no checkout of its own to require:\n%s", line)
	}
}
