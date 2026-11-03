package runtime

import (
	"path/filepath"
	"strings"

	"github.com/sushidev-team/lola/internal/agent"
)

// trustAgentDir records Claude Code's workspace trust for dir right before a
// claude agent launches in it, so the pane does not open on the "do you trust
// this folder?" dialog (internal/claudetrust). It is called on EVERY launch
// path — spawn, PR/manual agent, stack, revive, switch-agent — because each
// one starts a fresh claude in a folder lola created.
//
// Skipped when: the seam is unset (tests), the agent is not claude, the
// operator turned it off ([defaults].trust_worktrees = false), the session is
// SANDBOXED (its claude reads the container's own config, never the host's),
// or dir is not inside lola's worktree root — lola only vouches for folders it
// made. Best-effort: a failure logs and the launch goes ahead; the cost is the
// dialog the user would have seen anyway.
func (n *Native) trustAgentDir(project, id, dir string, kind agent.Kind) {
	if n.TrustWorkspace == nil || kind != agent.Claude {
		return
	}
	if n.Cfg != nil && !n.Cfg.TrustClaudeWorktrees() {
		return
	}
	if n.sandboxProject(project) != nil || !n.underWorktreeRoot(dir) {
		return
	}
	if err := n.TrustWorkspace(dir); err != nil {
		n.logf("session %s: could not pre-accept Claude Code workspace trust (the agent may stop on the trust dialog): %v", id, err)
	}
}

// untrustAgentDir drops the trust entry for a worktree that is gone, so
// Claude Code's config does not collect one entry per session forever. It
// runs regardless of agent kind or the trust_worktrees setting — an entry
// written before either changed must still be cleaned up — and is a cheap
// no-op (one read, no write) when there is nothing to remove.
func (n *Native) untrustAgentDir(id, dir string) {
	if n.UntrustWorkspace == nil || !n.underWorktreeRoot(dir) {
		return
	}
	if err := n.UntrustWorkspace(dir); err != nil {
		n.logf("session %s: could not remove Claude Code workspace trust for %s: %v", id, dir, err)
	}
}

// underWorktreeRoot reports whether dir lies strictly inside WT.Root. An
// unset root vouches for nothing.
func (n *Native) underWorktreeRoot(dir string) bool {
	if n.WT == nil || n.WT.Root == "" || dir == "" {
		return false
	}
	rel, err := filepath.Rel(filepath.Clean(n.WT.Root), filepath.Clean(dir))
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return false
	}
	return true
}
