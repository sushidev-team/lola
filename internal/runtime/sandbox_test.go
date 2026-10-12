package runtime

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sushidev-team/lola/internal/agent"
	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/session"
	"github.com/sushidev-team/lola/internal/usage"
)

func sandboxFixture(t *testing.T) *fixture {
	t.Helper()
	f := newFixture(t, "", "")
	f.p.Sandbox = config.SandboxDocker
	f.p.SandboxPorts = []string{"3000", "8080:80"}
	f.p.LinkedRepos = []config.LinkedRepo{{Name: "api", Path: "/src/api"}}
	f.n.Cfg.Projects[0] = f.p
	f.n.Cfg.Sandbox = config.SandboxConfig{Image: "acme/agent:1", LolaBin: "/opt/lola-linux", PassEnv: []string{"LOLA_TEST_PASS", "LOLA_TEST_UNSET"}}
	f.n.HookToken = func(id string) string { return "tok-" + id }
	return f
}

func TestSandboxedLaunchRunsTheAgentInDocker(t *testing.T) {
	f := sandboxFixture(t)
	id := "lola-nori-eng-42"
	dir := filepath.Join(f.root, "nori", id)
	if full := f.n.launchLine(id, agent.Claude, defaultLaunchPrompt(id), "", false); !strings.Contains(full, "docker") {
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
		`lola_gcd_0=$(git -C ` + shQuote(dir) + ` rev-parse --path-format=absolute --git-common-dir`,
		`lola_gcd_1=$(git -C ` + shQuote(filepath.Join(dir, ".lola", "repos", "api")) + ` rev-parse`,
		`${lola_gcd_0:+"$lola_gcd_0/config:$lola_gcd_0/config:ro"}`,
		`${lola_gcd_0:+"$lola_gcd_0/hooks:$lola_gcd_0/hooks:ro"}`,
		`${lola_gcd_1:+"$lola_gcd_1:$lola_gcd_1"}`,
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
	if plain := f.n.launchLine(id, agent.Claude, defaultLaunchPrompt(id), "", false); strings.Contains(plain, "docker") {
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
	f.p.LinkedRepos = nil
	f.n.Cfg.Projects[0] = f.p
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
