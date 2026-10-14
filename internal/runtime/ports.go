package runtime

import (
	"context"
	"time"

	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/session"
)

// preRemoveTimeout bounds a session's WHOLE pre_remove hook (every command
// together). Teardown runs under the caller's own deadline (30s for `lola kill`
// and merged-cleanup), and the hook must leave room inside it for the git
// removal that follows — a hook that hangs costs its own cleanup, never the
// worktree's.
const preRemoveTimeout = 20 * time.Second

// sessionProject renders p for one session: it leases the session's port block
// when the project asks for one (port_range), then expands [[project]].env and
// adds lola's per-session variables (withSessionEnv). Every launch path calls
// it right after the worktree exists and before anything reads p.Env.
//
// The lease is IDEMPOTENT per session ID, so a revive or an agent switch that
// re-renders the env gets the same ports back. It is also BEST-EFFORT: an
// exhausted range or an unwritable registry logs and the session starts without
// LOLA_PORT — the agent's work matters more than its dev server, and dev.go
// refuses to start a multi-active dev stack for a session holding no lease
// rather than letting it fight over the default port.
func (n *Native) sessionProject(p config.Project, v EnvVars) config.Project {
	if lo, hi, cnt, ok := p.PortLease(); ok && n.Ports != nil {
		l, err := n.Ports.Acquire(v.Session, p.Name, lo, hi, cnt)
		if err != nil {
			n.logf("session %s: no port lease (dev stack will not start in parallel): %v", v.Session, err)
		} else {
			v.Ports = l.Ports
		}
	}
	return withSessionEnv(p, v)
}

// teardownProject is sessionProject for a session that already exists: the
// SAME env its post_create saw, from its recorded lease (never a new one —
// teardown must not allocate).
func (n *Native) teardownProject(p config.Project, s session.Session, dir string) config.Project {
	v := EnvVars{Session: s.ID, Issue: s.Issue, Branch: s.Branch, Project: p.Name, Worktree: dir}
	if l, ok := n.Ports.Get(s.ID); ok {
		v.Ports = l.Ports
	}
	return withSessionEnv(p, v)
}

// releasePorts frees id's lease (a rolled-back spawn, a removed worktree).
// Best-effort: a lease that cannot be released is reclaimed by the daemon's
// prune once the session is gone from its store.
func (n *Native) releasePorts(id string) {
	if n.Ports == nil {
		return
	}
	if err := n.Ports.Release(id); err != nil {
		n.logf("session %s: release port lease: %v", id, err)
	}
}

// runPreRemove runs the project's pre_remove hook in dir before the worktree is
// removed. Bounded by preRemoveTimeout and best-effort: every failure is logged
// and NONE stops the teardown — a broken hook must not keep a worktree, a
// branch and a lease alive forever (the caller would retry the whole cleanup
// on every cycle). It runs after the tmux sessions are down, so the dev servers
// the hook may have to clean up after are already stopped.
func (n *Native) runPreRemove(ctx context.Context, p config.Project, s session.Session, dir string) {
	if len(p.PreRemove) == 0 {
		return
	}
	hctx, cancel := context.WithTimeout(ctx, preRemoveTimeout)
	defer cancel()
	for _, err := range n.WT.Teardown(hctx, n.teardownProject(p, s, dir), dir) {
		n.logf("session %s: pre_remove: %v (teardown continues)", s.ID, err)
	}
}

// logf is Logf, nil-safe.
func (n *Native) logf(format string, args ...any) {
	if n.Logf != nil {
		n.Logf(format, args...)
	}
}
