package daemon

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/sushidev-team/lola/internal/agent"
	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/runtime"
	"github.com/sushidev-team/lola/internal/session"
)

func validateSessionName(name string) error {
	if utf8.RuneCountInString(name) > 100 || strings.IndexFunc(name, unicode.IsControl) >= 0 {
		return errors.New("session name must be at most 100 characters without control characters")
	}
	return nil
}

// A PTY EOF can mean a detached client, not an exited shell. Retire only an
// ephemeral shell whose primary and auxiliary tmux sessions are ALL gone.
func (d *Daemon) handleCloseExitedTemporary(ctx context.Context, id string) (protocol.KillData, error) {
	s, ok := d.sessions.Get(id)
	if !ok {
		return protocol.KillData{Removed: true}, nil
	}
	if s.Kind != session.KindLocal || s.Location != "temporary" || !s.IsAgentless() {
		return protocol.KillData{}, errors.New("session is not a temporary shell")
	}
	d.mu.Lock()
	nat, list := d.native, d.listTmuxSessions
	d.mu.Unlock()
	if nat == nil || list == nil {
		return protocol.KillData{}, errors.New("cannot verify temporary shell liveness")
	}
	cctx, cancel := context.WithTimeout(ctx, observeExecTimeout)
	defer cancel()
	live, err := list(cctx)
	if err != nil {
		return protocol.KillData{}, err
	}
	parent := paneTarget(s)
	for _, pane := range live {
		if pane.Name == parent || strings.HasPrefix(pane.Name, parent+"-") {
			return protocol.KillData{Removed: false}, nil
		}
	}
	d.sessions.Delete(id)
	if err := d.sessions.Save(); err != nil {
		d.sessions.Upsert(s)
		return protocol.KillData{}, err
	}
	// Never call Kill from a PTY-exit notification: even if another tab appeared
	// after the listing, there is no process termination on this path.
	if err := runtime.RemoveLocalArtifacts(s.ID); err != nil {
		d.logf("", "temporary session %s: remove launch artifacts: %v", id, err)
	}
	return protocol.KillData{Removed: true}, nil
}

func (d *Daemon) handleRenameSession(id, name string) error {
	name = strings.TrimSpace(name)
	if err := validateSessionName(name); err != nil {
		return err
	}
	if _, ok := d.sessions.Get(id); !ok {
		return fmt.Errorf("unknown session %s", id)
	}
	d.sessions.Update(id, func(s *session.Session) bool { s.Name = name; return true })
	return d.sessions.Save()
}

// A borrowed directory never becomes a worktree owned by Lola. No checkout,
// setup hooks, branch mutations or implicit project configuration occur here.
func (d *Daemon) handleOpenDirectory(ctx context.Context, a protocol.OpenManualArgs) (protocol.OpenData, error) {
	if a.Location != "main" && a.Location != "temporary" {
		return protocol.OpenData{}, errors.New("unknown session location")
	}
	if err := validateSessionName(a.Name); err != nil {
		return protocol.OpenData{}, err
	}
	if a.Branch != "" || a.Base != "" {
		return protocol.OpenData{}, errors.New("branch options require a new worktree")
	}
	if a.Location == "temporary" && (a.Agent || a.Project != "") {
		return protocol.OpenData{}, errors.New("temporary sessions are project-free shells")
	}
	d.mu.Lock()
	nat, health := d.native, d.runtimeHealth
	var p config.Project
	configured := d.cfg.ProjectByName(a.Project)
	if configured != nil {
		p = *configured
	}
	kind := d.cfg.AgentForProject(a.Project)
	dir := d.cfg.Defaults.TemporaryDirectory
	d.mu.Unlock()
	if nat == nil {
		return protocol.OpenData{}, errors.New("native runtime unavailable")
	}
	if a.Location == "main" {
		if configured == nil || p.IsStack() || p.Path == "" {
			return protocol.OpenData{}, fmt.Errorf("unknown project %q", a.Project)
		}
		if a.Path != "" {
			return protocol.OpenData{}, errors.New("main checkout uses the configured project path")
		}
		dir = p.Path
	} else if a.Path != "" {
		dir = a.Path
	}
	if dir == "" {
		dir = "~"
	}
	if dir == "~" || strings.HasPrefix(dir, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return protocol.OpenData{}, err
		}
		dir = filepath.Join(home, strings.TrimPrefix(strings.TrimPrefix(dir, "~"), "/"))
	}
	if !filepath.IsAbs(dir) {
		return protocol.OpenData{}, errors.New("session directory must be an absolute path or start with ~/")
	}
	dir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return protocol.OpenData{}, fmt.Errorf("session directory: %w", err)
	}
	if a.AgentKind != "" {
		if !agent.Valid(a.AgentKind) {
			return protocol.OpenData{}, errors.New("unknown coding agent")
		}
		kind = a.AgentKind
	}
	bin := "tmux"
	if a.Agent {
		bin = agent.Parse(kind).Binary()
	}
	if err := health(bin); err != nil {
		return protocol.OpenData{}, fmt.Errorf("runtime not ready: %w", err)
	}
	var suffix [12]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		return protocol.OpenData{}, err
	}
	id := "lola-local-" + hex.EncodeToString(suffix[:])
	cctx, cancel := context.WithTimeout(ctx, nativeSpawnTimeout)
	defer cancel()
	s, err := nat.OpenDirectory(cctx, p, id, dir, a.Location, kind, a.Agent)
	if err != nil {
		return protocol.OpenData{}, err
	}
	s.Name = strings.TrimSpace(a.Name)
	d.sessions.Upsert(s)
	if err := d.sessions.Save(); err != nil {
		_ = nat.Kill(cctx, s, false, false)
		d.sessions.Delete(id)
		return protocol.OpenData{}, fmt.Errorf("persist session: %w", err)
	}
	d.recordSessionEvent("", s)
	return protocol.OpenData{SessionID: id, Worktree: dir, Message: "Session opened"}, nil
}
