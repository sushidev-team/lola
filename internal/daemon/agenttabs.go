package daemon

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/sushidev-team/lola/internal/agent"
	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/gitguard"
	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/runtime"
	"github.com/sushidev-team/lola/internal/session"
)

var errAgentTabsOpen = errors.New("close the other agent tabs using this checkout first")

func agentTabOffered(s session.Session, p *config.Project) bool {
	if p == nil || p.IsStack() || s.Source != "native" || s.Stack || s.GoalManaged() || s.BestOf != "" ||
		s.Location == runtime.LocationSSH || s.IsAgentless() || !agent.Caps(agent.Parse(s.Agent)).Feed {
		return false
	}
	if s.Sandbox == config.SandboxDocker {
		return s.GitPin != nil && s.GitPin.Valid()
	}
	return s.Sandbox == "" && s.GitPin == nil
}

// A blank field on a record recovered by adoption does not prove a host
// launch. Check the live owner before inheriting host access across a change
// of project default. Bound the tmux query during recovery and launch.
func confirmedHostAgent(ctx context.Context, nat NativeAPI, s session.Session) bool {
	if nat == nil {
		return false
	}
	probe, cancel := context.WithTimeout(ctx, observeExecTimeout)
	defer cancel()
	return nat.HostAgentRunning(probe, s)
}

func (d *Daemon) tabBinary(s session.Session) string {
	if s.Sandbox == config.SandboxDocker {
		d.mu.Lock()
		defer d.mu.Unlock()
		return d.cfg.Sandbox.DockerBin()
	}
	return agent.Parse(s.Agent).Binary()
}

func (d *Daemon) hasAgentTabs(parent string) bool {
	if parent == "" {
		return false
	}
	for _, s := range d.sessions.Snapshot() {
		if s.AgentTabParent == parent {
			return true
		}
	}
	return false
}

// A Docker relaunch rebuilds mounts. It requires exclusive access to the
// checkout while preparing them. Host agents may restart independently.
func (d *Daemon) agentTabsBlockRelaunch(s session.Session) bool {
	if !d.hasAgentTabs(s.ID) {
		return false
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	p := d.cfg.ProjectByName(s.Project)
	return s.Sandbox != "" || p == nil || p.Sandboxed()
}

// The request names a session, never a path. The owner's lifecycle lock also
// covers restore and removal so neither can race provisioning a new borrower.
func (d *Daemon) openAgentTab(ctx context.Context, parent string) (protocol.OpenData, error) {
	if parent == "" {
		return protocol.OpenData{}, errors.New("choose a session for the agent tab")
	}
	lock := d.ckptLock(parent)
	lock.Lock()
	defer lock.Unlock()
	owner, ok := d.sessions.Get(parent)
	if !ok || owner.AgentTabParent != "" {
		return protocol.OpenData{}, errors.New("the original session is no longer available")
	}
	d.mu.Lock()
	nat, health := d.native, d.runtimeHealth
	p := d.cfg.ProjectByName(owner.Project)
	offered := agentTabOffered(owner, p)
	var project config.Project
	if offered {
		project = *p
	}
	d.mu.Unlock()
	if !offered || nat == nil {
		return protocol.OpenData{}, errors.New("agent tabs require a supported checkout with a Conversation-capable agent")
	}
	worktree := sessionWorktree(d.home, owner)
	dir, err := filepath.EvalSymlinks(worktree)
	if err != nil {
		return protocol.OpenData{}, fmt.Errorf("agent tab checkout: %w", err)
	}
	if owner.Sandbox == config.SandboxDocker {
		at, pin, ok := gitguard.Lookup(worktree)
		if !ok || at != worktree || pin != *owner.GitPin || dir != filepath.Join(gitCanonical(d.home), "worktrees", owner.Project, owner.ID) {
			return protocol.OpenData{}, errors.New("the shared checkout has lost its sandbox pin")
		}
		if kind := agent.Parse(owner.Agent); !d.sandboxLoginReady(kind) {
			return protocol.OpenData{}, errors.New(sandboxLoginMissing(kind))
		}
		dir = worktree // Keep the exact spelling used by the pin registry.
	} else if project.Sandboxed() && !confirmedHostAgent(ctx, nat, owner) {
		return protocol.OpenData{}, errors.New("could not verify the original agent is running on the host")
	} else if !filepath.IsAbs(dir) || d.projectGitBlocked(dir) {
		return protocol.OpenData{}, errors.New("the checkout is isolated")
	}
	if err := health(d.tabBinary(owner)); err != nil {
		return protocol.OpenData{}, fmt.Errorf("runtime not ready: %w", err)
	}
	var suffix [12]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		return protocol.OpenData{}, err
	}
	id := "lola-local-" + hex.EncodeToString(suffix[:])
	cctx, cancel := context.WithTimeout(ctx, nativeSpawnTimeout)
	defer cancel()
	owner.Worktree = dir
	tab, err := nat.OpenAgentTab(cctx, project, id, owner)
	if err != nil {
		return protocol.OpenData{}, err
	}
	tab.Title = "Agent · " + owner.DisplayTitle()
	d.sessions.Upsert(tab)
	if err := d.sessions.Save(); err != nil {
		if stopErr := nat.Kill(cctx, tab, true, false); stopErr != nil {
			return protocol.OpenData{}, fmt.Errorf("persist agent tab: %w (could not stop it: %v)", err, stopErr)
		}
		d.sessions.Delete(id)
		return protocol.OpenData{}, err
	}
	d.recordSessionEvent("", tab)
	return protocol.OpenData{SessionID: id, Worktree: dir, Message: "Agent tab opened"}, nil
}
