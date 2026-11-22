package runtime

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/sushidev-team/lola/internal/agent"
	"github.com/sushidev-team/lola/internal/gitguard"
	"github.com/sushidev-team/lola/internal/session"
	"github.com/sushidev-team/lola/internal/usage"
)

// HostAgentRunning disambiguates an older record's empty sandbox field.
// The native host launcher sources the session env first. Container launchers
// start Docker on the host and source that env only inside the container.
func (n *Native) HostAgentRunning(ctx context.Context, s session.Session) bool {
	if s.Source != "native" || s.ID == "" || (s.TmuxName != "" && s.TmuxName != s.ID) || s.IsAgentless() || s.Sandbox != "" || s.GitPin != nil || s.Stack || s.GoalManaged() {
		return false
	}
	cwd, line, live := n.Tmux.LiveLaunch(ctx, s.ID)
	if !live || cwd == "" || filepath.Clean(cwd) != filepath.Clean(n.sessionDir(s)) {
		return false
	}
	if s.Kind == session.KindLocal {
		// Borrowed project directories have only ever supported a host launch.
		if s.Location != "main" && s.Location != "checkout" {
			return false
		}
		art, err := localArtifacts(s.ID)
		return err == nil && line == "exec sh "+shQuote(filepath.Join(art, "launch"))
	}
	return strings.HasPrefix(line, "exec sh -c 'set -a; . ./.lola/env; set +a; ")
}

// A second container shares the checkout and Git store. Its config, callbacks,
// launch files and transcript belong to the tab. In particular, no new host
// bind source may come from *inside* either tree a running agent can modify.
func (n *Native) agentTabContainer(ctx context.Context, s session.Session, artifacts, inner string) (string, error) {
	p := n.Cfg.ProjectByName(s.Project)
	at, pin, pinned := gitguard.Lookup(s.Worktree)
	if p == nil || s.GitPin == nil || !pinned || at != s.Worktree || pin != *s.GitPin || !pin.Valid() ||
		!strings.HasPrefix(pin.GitDir, filepath.Join(pin.CommonDir, "worktrees")+string(filepath.Separator)) {
		return "", errors.New("agent tab requires the original pinned Docker worktree")
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	root, err := os.OpenRoot(pin.CommonDir)
	if err != nil {
		return "", err
	}
	defer root.Close()
	// Use private read-only copies as mount sources. The Git store belongs to
	// the running agents, so a path inside it must never resolve a host mount.
	for _, cfg := range []struct{ source, dest string }{
		{"config", "git-config"},
		{filepath.Join(strings.TrimPrefix(pin.GitDir, pin.CommonDir+string(filepath.Separator)), "config.worktree"), "worktree-config"},
	} {
		data, err := readTabGitConfig(root, cfg.source)
		if err != nil && !(cfg.dest == "worktree-config" && os.IsNotExist(err)) {
			return "", fmt.Errorf("shared checkout configuration: %w", err)
		}
		if err := os.WriteFile(filepath.Join(artifacts, cfg.dest), data, 0600); err != nil {
			return "", err
		}
	}
	mask := filepath.Join(artifacts, "empty")
	if err := os.MkdirAll(mask, 0700); err != nil {
		return "", err
	}
	ptr := filepath.Join(artifacts, "git-pointer")
	if err := os.WriteFile(ptr, []byte("gitdir: "+pin.GitDir+"\n"), 0600); err != nil {
		return "", err
	}

	kind := agent.Parse(s.Agent)
	state := n.sandboxStateRoot(s.ID)
	if state == "" || n.sandboxLolaPath() == "" {
		return "", errors.New("agent tab sandbox is unavailable")
	}
	args := []string{n.Cfg.Sandbox.DockerBin(), "run", "--rm", "-it", "--init", "--name", s.ID,
		"--label", sandboxLabel + "=" + s.ID, "--add-host", sandboxHost + ":host-gateway",
		"--security-opt", "no-new-privileges", "-w", s.AgentCWD}
	bind := func(source, dest string, readOnly bool) {
		spec := source + ":" + dest
		if readOnly {
			spec += ":ro"
		}
		args = append(args, "-v", spec)
	}
	// Mount the checkout at the tab's own cwd: both providers work at the
	// repository root, while a provider looking up history by cwd sees only this tab.
	bind(s.Worktree, s.AgentCWD, false)
	bind(mask, filepath.Join(s.AgentCWD, lolaDir), true)
	bind(ptr, filepath.Join(s.AgentCWD, ".git"), true)
	bind(pin.CommonDir, pin.CommonDir, false)
	bind(filepath.Join(artifacts, "git-config"), filepath.Join(pin.CommonDir, "config"), true)
	bind(mask, filepath.Join(pin.CommonDir, "hooks"), true)
	bind(filepath.Join(artifacts, "worktree-config"), filepath.Join(pin.GitDir, "config.worktree"), true)
	bind(state, state, false)
	bind(filepath.Join(artifacts, "env"), filepath.Join(artifacts, "env"), true)
	bind(n.sandboxLolaPath(), n.lolaBin(), true)
	transcript := ""
	if kind == agent.Claude {
		bind(filepath.Join(artifacts, "settings.json"), filepath.Join(artifacts, "settings.json"), true)
		transcript = usage.SlugDir(usage.ProjectsRoot(), s.AgentCWD)
		if transcript == "" {
			return "", errors.New("agent tab transcript directory unavailable")
		}
		if err := os.MkdirAll(transcript, 0700); err != nil {
			return "", err
		}
		bind(transcript, transcript, false)
	}
	for _, rel := range p.Symlinks {
		if filepath.IsLocal(rel) {
			target := filepath.Join(p.Path, rel)
			if _, err := os.Stat(target); err == nil {
				bind(target, target, true)
			}
		}
	}
	args = append(args, "--entrypoint", "sh", n.sandboxImageOf(p), "-c",
		sandboxPrelude(s.AgentCWD, n.agentStateDir(s.ID, s.AgentCWD, kind), kind, transcript)+inner)
	for i := range args {
		args[i] = shQuote(args[i])
	}
	docker := shQuote(n.Cfg.Sandbox.DockerBin())
	return docker + " rm -f " + shQuote(s.ID) + " >/dev/null 2>&1; exec " + strings.Join(args, " "), nil
}

func readTabGitConfig(root *os.Root, name string) ([]byte, error) {
	info, err := root.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("expected a regular Git config file")
	}
	file, err := root.OpenFile(name, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return nil, errors.New("Git config changed while opening it")
	}
	const maxConfig = 1 << 20
	data, err := io.ReadAll(io.LimitReader(file, maxConfig+1))
	if len(data) > maxConfig {
		return nil, errors.New("Git config exceeds 1 MiB")
	}
	return data, err
}
