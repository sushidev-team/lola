package runtime

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sushidev-team/lola/internal/agent"
	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/gitguard"
	"github.com/sushidev-team/lola/internal/session"
	"github.com/sushidev-team/lola/internal/tmux"
)

func TestAgentTabUsesSeparateProviderHistory(t *testing.T) {
	f := newFixture(t, "", "")
	f.p.RequirePlan = true
	f.p.Sandbox = config.SandboxDocker
	f.n.Cfg.Projects = []config.Project{f.p}
	for _, kind := range []string{"claude", "codex"} {
		owner := session.Session{ID: "original", Worktree: f.repo, Agent: kind}
		tab, err := f.n.OpenAgentTab(t.Context(), f.p, "lola-local-"+kind, owner)
		if err != nil {
			t.Fatal(err)
		}
		artifacts, _ := localArtifacts(tab.ID)
		if tab.AgentTabParent != owner.ID || tab.AgentCWD != filepath.Join(artifacts, "work") || tab.Worktree != f.repo || tab.OwnsBranch() || !tab.PlanGate.Blocks() {
			t.Fatal(tab)
		}
		raw, err := os.ReadFile(filepath.Join(artifacts, "launch"))
		if err != nil || !strings.Contains(string(raw), "--add-dir "+shQuote(f.repo)) {
			t.Errorf("missing shared checkout in %s launch: %v", kind, err)
		}
		if !strings.Contains(loggedArgs(t, f.tmuxLog), tab.AgentCWD) {
			t.Fatalf("tmux never started in %s", tab.AgentCWD)
		}
	}
}

// Use the actual tmux start metadata of a pre-change host launch. The stand-in
// provider records which checkout the second tab was given and never calls an AI.
func TestAgentTabInheritsLiveHostCheckout(t *testing.T) {
	if os.Getenv("LOLA_TMUX_IT") == "" {
		t.Skip("set LOLA_TMUX_IT=1 to use a local tmux server")
	}
	f := newFixture(t, "", "")
	dir, err := filepath.EvalSymlinks(f.repo)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	socket := fmt.Sprintf("lola-tab-host-test-%d", os.Getpid())
	f.n.Tmux = &tmux.Client{SocketName: socket}
	t.Cleanup(func() { _ = exec.Command("tmux", "-L", socket, "kill-server").Run() })
	f.n.ClaudeBin = filepath.Join(t.TempDir(), "provider")
	shim := `#!/bin/sh
if [ "$1" = "--add-dir" ]; then printf '%s' "$LOLA_SESSION" > "$2/second-tab"; fi
exec sleep 60
`
	if err := os.WriteFile(f.n.ClaudeBin, []byte(shim), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, ".lola"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".lola", "env"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	owner := session.Session{ID: "owner", TmuxName: "owner", Kind: session.KindPR, Project: f.p.Name, Worktree: dir, Agent: "claude", Source: "native"}
	if err := f.n.Tmux.NewSession(ctx, owner.ID, dir, f.n.launchCommand(f.p.Name, owner.ID, agent.Claude, false)); err != nil {
		t.Fatal(err)
	}
	f.p.Sandbox = config.SandboxDocker
	f.n.Cfg.Projects[0] = f.p
	if !f.n.HostAgentRunning(ctx, owner) {
		t.Fatal("older live host launch was not recognized")
	}
	tab, err := f.n.OpenAgentTab(ctx, f.p, "lola-local-host-tab", owner)
	if err != nil {
		t.Fatal(err)
	}
	for ctx.Err() == nil {
		marker, _ := os.ReadFile(filepath.Join(dir, "second-tab"))
		if string(marker) == tab.ID && f.n.Tmux.Has(ctx, owner.ID) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("second host agent did not reach the same checkout while the original was running")
}

// Exercise the generated Docker mounts in tmux, using git as a stand-in for an agent.
func TestAgentTabSharesDockerCheckout(t *testing.T) {
	if os.Getenv("LOLA_SANDBOX_IT") == "" {
		t.Skip("set LOLA_SANDBOX_IT=1 to use a local Docker engine")
	}
	f := newStackFixture(t)
	image := os.Getenv("LOLA_SANDBOX_IT_IMAGE")
	if image == "" {
		image = "alpine/git:latest"
	}
	p := config.Project{Name: "web", Path: f.web, DefaultBranch: "main", Sandbox: config.SandboxDocker}
	f.n.Cfg = &config.Config{Projects: []config.Project{p}, Sandbox: config.SandboxConfig{Image: image, LolaBin: fakeLinuxLola(t), PassEnv: []string{"LOLA_TEST_DUMMY"}}}
	hostAuthFile = func(agent.Kind) string { return "" }
	t.Cleanup(func() { hostAuthFile = agent.HostAuthFile })
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	dir, err := f.n.WT.Create(ctx, p, "owner", "shared-branch")
	if err != nil {
		t.Fatal(err)
	}
	dir, err = filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	pin, err := f.n.pinSandbox(p.Name, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer gitguard.Clear(dir)
	if err := os.MkdirAll(filepath.Join(dir, ".lola"), 0700); err != nil {
		t.Fatal(err)
	}
	owner := session.Session{ID: "owner", Project: p.Name, Worktree: dir, Agent: "codex", GitPin: pin, Sandbox: p.Sandbox}
	p.Sandbox = "" // Existing Docker sessions retain their runtime after a config change.
	f.n.Cfg.Projects[0] = p
	tab, err := f.n.OpenAgentTab(ctx, p, fmt.Sprintf("lola-local-tab-it-%d", os.Getpid()), owner)
	if err != nil {
		t.Fatal(err)
	}
	artifacts, _ := localArtifacts(tab.ID)
	line, err := f.n.agentTabContainer(ctx, tab, artifacts, `printf 'shared\n' > shared.txt && git -c safe.directory="$PWD" add shared.txt && git -c safe.directory="$PWD" branch --show-current > branch.txt`)
	if err != nil {
		t.Fatal(err)
	}
	socket := fmt.Sprintf("lola-tab-test-%d", os.Getpid())
	term := &tmux.Client{SocketName: socket}
	t.Cleanup(func() { _ = exec.Command("tmux", "-L", socket, "kill-server").Run() })
	output := filepath.Join(artifacts, "output")
	if err := term.NewSession(ctx, tab.ID, artifacts, "sh -c "+shQuote(line)+" > "+shQuote(output)+" 2>&1"); err != nil {
		t.Fatal(err)
	}
	for term.Has(ctx, tab.ID) && ctx.Err() == nil {
		time.Sleep(100 * time.Millisecond)
	}
	branch, _ := os.ReadFile(filepath.Join(dir, "branch.txt"))
	if strings.TrimSpace(string(branch)) != "shared-branch" || realGit(t, dir, "diff", "--cached", "--name-only") != "shared.txt" {
		logs, _ := os.ReadFile(output)
		t.Fatalf("shared checkout or index missing: %s", logs)
	}
}
