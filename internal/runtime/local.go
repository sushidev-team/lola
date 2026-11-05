package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sushidev-team/lola/internal/agent"
	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/hook"
	"github.com/sushidev-team/lola/internal/session"
	"github.com/sushidev-team/lola/internal/state"
)

func localArtifacts(id string) (string, error) {
	if !strings.HasPrefix(id, "lola-local-") || filepath.Base(id) != id {
		return "", errors.New("invalid local session id")
	}
	home, err := config.Home()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "local-sessions", id), nil
}

// OpenDirectory attaches a session to a borrowed directory. All Lola artifacts
// live in a per-session directory outside it; even failed launches never alter
// the checkout or run its worktree setup/cleanup hooks.
func (n *Native) OpenDirectory(ctx context.Context, p config.Project, id, dir, location, kindName string, useAgent bool) (result session.Session, launchErr error) {
	if !strings.HasPrefix(id, "lola-local-") || filepath.Base(id) != id {
		return session.Session{}, errors.New("invalid local session id")
	}
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		return session.Session{}, fmt.Errorf("session directory unavailable: %s", dir)
	}
	if useAgent && n.sandboxProject(p.Name) != nil {
		return session.Session{}, errors.New("sandboxed agents require an isolated worktree; choose New worktree")
	}
	kind := agent.Parse(kindName)
	artifacts, err := localArtifacts(id)
	if err != nil {
		return session.Session{}, err
	}
	if err := os.MkdirAll(artifacts, 0700); err != nil {
		return session.Session{}, err
	}
	defer func() {
		if launchErr != nil {
			_ = os.RemoveAll(artifacts)
		}
	}()
	p = withSessionEnv(p, EnvVars{Session: id, Project: p.Name, Worktree: dir})
	env := manualEnvFile(p)
	if err := os.WriteFile(filepath.Join(artifacts, "shell-env"), env, 0600); err != nil {
		return session.Session{}, err
	}
	if useAgent {
		// Reserved values follow project env so another session cannot inherit
		// a stale callback target. No Linear credential is needed here.
		env = append(env, []byte("LOLA_SESSION="+shQuote(id)+"\n")...)
		if kind == agent.Codex {
			env = append(env, []byte("CODEX_HOME="+shQuote(userCodexHome())+"\n")...)
		}
		if kind == agent.Claude {
			if err := os.WriteFile(filepath.Join(artifacts, "settings.json"), hook.SettingsJSON(n.lolaBin()), 0600); err != nil {
				return session.Session{}, err
			}
		}
		if kind == agent.OpenCode {
			plugins := filepath.Join(artifacts, "plugins")
			if err := os.MkdirAll(plugins, 0700); err != nil {
				return session.Session{}, err
			}
			if err := os.WriteFile(filepath.Join(plugins, "lola-hook.js"), agent.OpenCodePluginJS(n.lolaBin()), 0600); err != nil {
				return session.Session{}, err
			}
			env = append(env, []byte("OPENCODE_CONFIG_DIR="+shQuote(artifacts)+"\n")...)
		}
	}
	if err := os.WriteFile(filepath.Join(artifacts, "env"), env, 0600); err != nil {
		return session.Session{}, err
	}
	s := session.Session{ID: id, Source: "native", Kind: session.KindLocal, Location: location, Project: p.Name, Worktree: dir, TmuxName: id, Agentless: !useAgent, Agent: string(kind), Title: "Shell · " + filepath.Base(dir)}
	if useAgent {
		s.Title = kind.Binary() + " · " + filepath.Base(dir)
	}
	return n.startLocal(ctx, s)
}

// RemoveLocalArtifacts forgets only Lola's private launch files. It never
// signals a process or touches the borrowed working directory.
func RemoveLocalArtifacts(id string) error {
	dir, err := localArtifacts(id)
	if err != nil {
		return err
	}
	return os.RemoveAll(dir)
}

func (n *Native) startLocal(ctx context.Context, s session.Session) (session.Session, error) {
	artifacts, err := localArtifacts(s.ID)
	if err != nil {
		return session.Session{}, err
	}
	posix := "set -a; . " + shQuote(filepath.Join(artifacts, "env")) + " || exit; set +a; exec "
	if s.IsAgentless() {
		posix += `"${SHELL:-/bin/sh}" -l`
	} else {
		kind := agent.Parse(s.Agent)
		// No cwd-based --continue: two agents can share this directory, so that
		// would resume an unrelated conversation. Reopen starts a fresh agent.
		args := agent.LaunchArgs(kind, "You are Lola session "+s.ID+". You are in the project's main checkout, shared with other sessions. Wait for the user's task.")
		for i, arg := range args {
			if arg == ".lola/settings.json" {
				args[i] = filepath.Join(artifacts, "settings.json")
			}
		}
		if kind == agent.Codex {
			notify, _ := json.Marshal([]string{n.lolaBin(), "hook", "codex-notify"})
			args = append([]string{"-c", "notify=" + string(notify)}, args...)
		}
		bin := kind.Binary()
		if kind == agent.Claude && n.ClaudeBin != "" {
			bin = n.ClaudeBin
		}
		posix += shQuote(bin)
		for _, arg := range args {
			posix += " " + shQuote(arg)
		}
	}
	if err := n.Tmux.NewSession(ctx, s.ID, s.Worktree, "exec sh -c "+shQuote(posix)); err != nil {
		if n.Tmux.Has(ctx, s.ID) {
			_ = n.Tmux.KillSession(ctx, s.ID)
		}
		return session.Session{}, err
	}
	_ = n.Tmux.ConfigureSession(ctx, s.ID, n.Cfg.SessionChrome(s.DisplayTitle()))
	a := state.AgentStarting
	if s.IsAgentless() {
		a = state.AgentShell
	}
	s.SetAgentState(a, "", time.Now())
	return s, nil
}

func (n *Native) reviveLocal(ctx context.Context, s session.Session) (session.Session, error) {
	if !s.IsAgentless() && n.sandboxProject(s.Project) != nil {
		return session.Session{}, errors.New("sandboxed agents require an isolated worktree; create a new worktree session")
	}
	if fi, err := os.Stat(s.Worktree); err != nil || !fi.IsDir() {
		return session.Session{}, errors.New("session directory is no longer available")
	}
	artifacts, err := localArtifacts(s.ID)
	if err != nil {
		return session.Session{}, err
	}
	if _, err := os.Stat(filepath.Join(artifacts, "env")); err != nil {
		return session.Session{}, errors.New("session launch settings are missing; create a new session")
	}
	return n.startLocal(ctx, s)
}
