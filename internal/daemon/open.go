package daemon

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/sushidev-team/lola/internal/agent"
	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/runtime"
	"github.com/sushidev-team/lola/internal/session"
)

// handleOpen manually checks out a branch or PR of a project into a throwaway
// DETACHED worktree with a plain interactive shell (cmd=open) — no coding agent,
// no Linear issue — so a human can run and test it. It is the human-triggered
// counterpart to the Linear-driven dispatch path: it resolves the target to a
// git fetch ref, health-gates git+tmux (the coding agent is irrelevant here),
// opens the session via the native runtime, and records it in the store as a
// Manual session so the observer keeps it out of the reaction/write-back/review
// control loop. Teardown reuses cmd=kill unchanged.
//
// The session it creates carries no in-flight/seen/label dispatch state — it was
// never a Linear match — so nothing needs unwinding on failure, and it never
// competes for a dispatch slot (its "shell" status is not slot-occupying).
func (d *Daemon) handleOpen(ctx context.Context, project, ref string) (protocol.OpenData, error) {
	project = strings.TrimSpace(project)
	ref = strings.TrimSpace(ref)
	if project == "" || ref == "" {
		return protocol.OpenData{}, errors.New("open: project and branch/PR required")
	}

	d.mu.Lock()
	nat := d.native
	p := d.cfg.ProjectByName(project)
	health := d.runtimeHealth
	home := d.home
	d.mu.Unlock()
	if p == nil {
		return protocol.OpenData{}, fmt.Errorf("unknown project %q", project)
	}
	if nat == nil {
		return protocol.OpenData{}, errors.New("native runtime unavailable")
	}
	// A manual shell needs git + tmux only (no coding agent), so gate on "git" —
	// checkRuntimeHealth verifies tmux and git regardless of the binary argument.
	if err := health("git"); err != nil {
		return protocol.OpenData{}, fmt.Errorf("runtime not ready: %w", err)
	}

	fetchRef, branch := resolveOpenTarget(ref)
	id := runtime.ManualSessionID(p.Name, branch)
	if _, ok := d.sessions.Get(id); ok {
		return protocol.OpenData{}, fmt.Errorf("%s is already open in %s (session %s) — kill it first", branch, p.Name, id)
	}

	// nativeSpawnTimeout bounds the whole open: fetch + worktree add + the
	// project's post_create commands can run for a while (same reasoning as Spawn).
	cctx, cancel := context.WithTimeout(ctx, nativeSpawnTimeout)
	sess, err := nat.Open(cctx, *p, id, fetchRef, branch)
	cancel()
	if err != nil {
		return protocol.OpenData{}, err
	}
	sess.Repo = p.Repo
	d.sessions.Upsert(sess)
	// Record the birth in the activity feed (Upsert has no transition callback).
	d.recordSessionEvent("", sess)
	if serr := d.sessions.Save(); serr != nil {
		d.logf("", "open: persist sessions after manual open of %s: %v", branch, serr)
	}

	dir := filepath.Join(home, "worktrees", p.Name, id)
	msg := fmt.Sprintf("opened %s (%s) at %s — attach in the TUI, or: tmux -L lola attach -t %s", branch, p.Name, dir, id)
	d.logf("", "open: %s", msg)
	return protocol.OpenData{SessionID: id, Worktree: dir, Branch: branch, Message: msg}, nil
}

// handleOpenManual creates a NEW branch in a fresh worktree with a plain shell
// (cmd=openManual) — the "just open a new worktree/branch manually" path. Like
// handleOpen it needs git + tmux only (no coding agent), records the session as
// an agent-less manual-kind shell (kept out of the control loop), and reuses
// cmd=kill for teardown. Unlike handleOpen the branch is lola-owned, so teardown
// deletes it.
func (d *Daemon) handleOpenManual(ctx context.Context, a protocol.OpenManualArgs) (protocol.OpenData, error) {
	project := strings.TrimSpace(a.Project)
	branch := strings.TrimSpace(a.Branch)
	base := strings.TrimSpace(a.Base)
	if project == "" || branch == "" {
		return protocol.OpenData{}, errors.New("openManual: project and branch required")
	}

	d.mu.Lock()
	nat := d.native
	p := d.cfg.ProjectByName(project)
	health := d.runtimeHealth
	home := d.home
	agentBin := agent.Parse(d.cfg.AgentForProject(project)).Binary()
	d.mu.Unlock()
	if p == nil {
		return protocol.OpenData{}, fmt.Errorf("unknown project %q", project)
	}
	if nat == nil {
		return protocol.OpenData{}, errors.New("native runtime unavailable")
	}
	// A shell needs git + tmux only; an agent additionally needs its binary.
	gate := "git"
	if a.Agent {
		gate = agentBin
	}
	if err := health(gate); err != nil {
		return protocol.OpenData{}, fmt.Errorf("runtime not ready: %w", err)
	}

	id := runtime.ManualSessionID(p.Name, branch)
	if _, ok := d.sessions.Get(id); ok {
		return protocol.OpenData{}, fmt.Errorf("%s is already open in %s (session %s) — kill it first", branch, p.Name, id)
	}

	cctx, cancel := context.WithTimeout(ctx, nativeSpawnTimeout)
	var sess session.Session
	var err error
	if a.Agent {
		sess, err = nat.OpenManualAgent(cctx, *p, id, branch, base, a.Prompt)
	} else {
		sess, err = nat.OpenManual(cctx, *p, id, branch, base)
	}
	cancel()
	if err != nil {
		return protocol.OpenData{}, err
	}
	sess.Repo = p.Repo
	d.sessions.Upsert(sess)
	d.recordSessionEvent("", sess)
	if serr := d.sessions.Save(); serr != nil {
		d.logf("", "openManual: persist sessions after manual worktree %s: %v", branch, serr)
	}

	dir := filepath.Join(home, "worktrees", p.Name, id)
	kindWord := "shell"
	if a.Agent {
		kindWord = "agent"
	}
	msg := fmt.Sprintf("created %s (%s) with an %s at %s — attach in the TUI, or: tmux -L lola attach -t %s", branch, p.Name, kindWord, dir, id)
	d.logf("", "openManual: %s", msg)
	return protocol.OpenData{SessionID: id, Worktree: dir, Branch: branch, Message: msg}, nil
}

// handleOpenPr opens a PR's head branch as a TRACKING worktree with the coding
// agent (cmd=openPr) — the "agent on PR" upgrade that can push back. It refuses
// a fork PR (no push-back to a fork), full-health-gates the project's agent, and
// records a pr-kind agent session (which counts toward liveCounted but is never
// Linear-bound). The upstream branch is never deleted on teardown.
func (d *Daemon) handleOpenPr(ctx context.Context, a protocol.OpenPrArgs) (protocol.OpenData, error) {
	project := strings.TrimSpace(a.Project)
	branch := strings.TrimSpace(a.Branch)
	if project == "" || branch == "" {
		return protocol.OpenData{}, errors.New("openPr: project and branch required")
	}
	if a.IsFork {
		return protocol.OpenData{}, errors.New("openPr: fork PR — open it detached (run/test) instead; push-back to a fork is not supported")
	}

	d.mu.Lock()
	nat := d.native
	p := d.cfg.ProjectByName(project)
	health := d.runtimeHealth
	home := d.home
	agentBin := agent.Parse(d.cfg.AgentForProject(project)).Binary()
	d.mu.Unlock()
	if p == nil {
		return protocol.OpenData{}, fmt.Errorf("unknown project %q", project)
	}
	if nat == nil {
		return protocol.OpenData{}, errors.New("native runtime unavailable")
	}
	if err := health(agentBin); err != nil {
		return protocol.OpenData{}, fmt.Errorf("runtime not ready: %w", err)
	}

	id := runtime.ManualSessionID(p.Name, branch)
	if _, ok := d.sessions.Get(id); ok {
		return protocol.OpenData{}, fmt.Errorf("%s is already open in %s (session %s) — kill it first", branch, p.Name, id)
	}

	prompt := fmt.Sprintf("You are on the branch %q of an existing pull request in %s. Review the current state, address any outstanding review feedback or CI failures, and push your changes back to this branch.", branch, p.Repo)

	cctx, cancel := context.WithTimeout(ctx, nativeSpawnTimeout)
	sess, err := nat.OpenPRAgent(cctx, *p, id, branch, prompt)
	cancel()
	if err != nil {
		return protocol.OpenData{}, err
	}
	sess.Repo = p.Repo
	d.sessions.Upsert(sess)
	d.recordSessionEvent("", sess)
	if serr := d.sessions.Save(); serr != nil {
		d.logf("", "openPr: persist sessions after PR-agent open of %s: %v", branch, serr)
	}

	dir := filepath.Join(home, "worktrees", p.Name, id)
	msg := fmt.Sprintf("opened PR branch %s (%s) with an agent at %s — attach in the TUI, or: tmux -L lola attach -t %s", branch, p.Name, dir, id)
	d.logf("", "openPr: %s", msg)
	return protocol.OpenData{SessionID: id, Worktree: dir, Branch: branch, Message: msg}, nil
}

// resolveOpenTarget maps a user-supplied open target to a git fetch ref and a
// human-readable branch label. A purely numeric target is a PR number, fetched
// via refs/pull/<n>/head (which works across forks and needs no local branch);
// anything else is treated as a branch name fetched from origin as-is.
func resolveOpenTarget(target string) (fetchRef, branch string) {
	if n, err := strconv.Atoi(target); err == nil && n > 0 {
		return fmt.Sprintf("pull/%d/head", n), fmt.Sprintf("pr-%d", n)
	}
	return target, target
}
