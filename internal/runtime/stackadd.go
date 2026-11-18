package runtime

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/session"
)

// ADDING AN APP TO A RUNNING STACK SESSION. The issue's labels pick a stack
// session's apps at spawn; a missing routing label leaves the agent without a
// repository it needs, and all it can do is report that. AddApps is the
// human's answer: check further members out into the session folder, on the
// session's branch, each prepared exactly as the spawn prepares one.
//
// A SANDBOXED session's container has the mount list of its launch: the new
// app's git store is not in it, and the session folder is writable by the
// agent. So for a sandbox the agent and its container are stopped BEFORE the
// worktree exists (post_create then runs on a tree the agent never touched,
// as at spawn) and the agent is relaunched afterwards, resuming its
// conversation, on a mount list that includes the new app. A host session is
// left running; the daemon tells the agent what changed.

// AddAppsResult is what AddApps did. Relaunched is true when the session's
// agent was stopped and started again (sandbox) — also on a failed add, so
// the caller records the relaunch either way.
type AddAppsResult struct {
	Apps       []session.App
	Relaunched bool
	// Session is the relaunched record (Relaunched only).
	Session session.Session
}

// AddApps checks the named members of s's stack out into its session folder.
// All or nothing: on a failure the apps this call created are removed again.
// It refuses a name that is not a member, one already checked out, and a
// folder of that name that already exists (the agent may have made it).
func (n *Native) AddApps(ctx context.Context, s session.Session, names []string) (AddAppsResult, error) {
	var res AddAppsResult
	fail := func(format string, args ...any) (AddAppsResult, error) {
		return res, fmt.Errorf("runtime: add apps to %s: "+format, append([]any{s.ID}, args...)...)
	}
	if !s.Stack {
		return fail("not a stack session")
	}
	if s.GoalManaged() {
		return fail("a goal worker's repositories are set by its plan")
	}
	p := n.Cfg.ProjectByName(s.Project)
	if p == nil || !p.IsStack() {
		return fail("stack %s is no longer configured", s.Project)
	}
	if s.Branch == "" {
		return fail("the session's branch is unknown (its record was rebuilt from disk)")
	}
	dir := n.sessionDir(s)
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		return fail("session folder %s is gone", dir)
	}

	members := n.Cfg.StackMembers(p.Name)
	var add []config.Project
	for _, m := range members { // member order, whatever order they were named in
		if !slices.ContainsFunc(names, func(name string) bool { return strings.TrimSpace(name) == m.Name }) {
			continue
		}
		if slices.ContainsFunc(s.Apps, func(a session.App) bool { return a.Name == m.Name }) {
			return fail("%s is already checked out", m.Name)
		}
		if m.Path == "" {
			return fail("%s has no path configured", m.Name)
		}
		if _, err := os.Lstat(StackAppDir(dir, m.Name)); !errors.Is(err, fs.ErrNotExist) {
			return fail("%s already exists in the session folder — move it away first", m.Name)
		}
		add = append(add, m)
	}
	for _, name := range names {
		if name = strings.TrimSpace(name); name != "" && !slices.ContainsFunc(members, func(m config.Project) bool { return m.Name == name }) {
			return fail("%s is not a member of stack %s", name, p.Name)
		}
	}
	if len(add) == 0 {
		return fail("no app named")
	}

	id := s.TmuxName
	if id == "" {
		id = s.ID
	}
	sandboxed := s.Sandbox != "" || n.sandboxProject(s.Project) != nil
	relaunch := false
	if sandboxed {
		// Before anything is created: nothing of the agent's may be running
		// while a host command (post_create) runs in a tree it can reach.
		if n.Tmux.Has(ctx, id) {
			if err := n.Tmux.KillSessionTree(ctx, id); err != nil {
				return fail("stop the agent: %w", err)
			}
			relaunch = true
		}
		if err := n.removeContainer(ctx, s.ID); err != nil {
			if relaunch {
				res.Session, res.Relaunched = n.relaunchAfterAdd(ctx, s)
			}
			return fail("remove the sandbox container: %w", err)
		}
	}

	created, err := n.createApps(ctx, *p, add, dir, s.ID, s.Issue, s.Branch)
	if err == nil {
		err = n.rewriteStackBriefing(dir, members, append(slices.Clone(s.Apps), created...))
	}
	if err != nil {
		for _, a := range created {
			// Created a moment ago, by this call: nothing in it is anyone's work.
			if rbErr := n.removeApp(ctx, s, a, sandboxed, true); rbErr != nil {
				n.logf("session %s: rolling back app %s failed (kept at %s): %v", s.ID, a.Name, a.Worktree, rbErr)
			}
		}
		if relaunch {
			res.Session, res.Relaunched = n.relaunchAfterAdd(ctx, s)
		}
		return fail("%w", err)
	}
	for _, a := range created {
		n.recordBaseline(ctx, a.Worktree, s.ID)
	}
	res.Apps = created
	if relaunch {
		next := s
		next.Apps = append(slices.Clone(s.Apps), created...)
		res.Session, res.Relaunched = n.relaunchAfterAdd(ctx, next)
		if !res.Relaunched {
			// The apps are in; only the relaunch failed. The session reads dead
			// and Revive brings it back on the new mount list.
			n.logf("session %s: apps added, but the agent did not come back — revive it", s.ID)
		}
	}
	return res, nil
}

// relaunchAfterAdd starts a sandboxed session's agent again (resuming its
// conversation when it has one). ok is false when the launch failed: the pane
// is down and the session reads dead until it is revived.
func (n *Native) relaunchAfterAdd(ctx context.Context, s session.Session) (session.Session, bool) {
	revived, err := n.revive(ctx, s, false)
	if err != nil {
		n.logf("session %s: relaunch after adding apps failed: %v", s.ID, err)
		return s, false
	}
	return revived, true
}

// rewriteStackBriefing brings prompt.md's stack section up to date with apps.
func (n *Native) rewriteStackBriefing(dir string, members []config.Project, apps []session.App) error {
	path := filepath.Join(dir, lolaDir, "prompt.md")
	body, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil // nothing briefs this session (a record rebuilt from disk)
	}
	if err != nil {
		return fmt.Errorf("read prompt.md: %w", err)
	}
	order := map[string]int{}
	for i, m := range members {
		order[m.Name] = i
	}
	sorted := slices.Clone(apps)
	slices.SortStableFunc(sorted, func(a, b session.App) int { return order[a.Name] - order[b.Name] })
	if err := os.WriteFile(path, replaceStackBriefing(body, members, sorted), 0o600); err != nil {
		return fmt.Errorf("write prompt.md: %w", err)
	}
	return nil
}
