package daemon

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/sushidev-team/lola/internal/gitguard"
	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/session"
	"github.com/sushidev-team/lola/internal/sshstore"
)

// handleRemoveEndedWorktree removes only a retained checkout identified by an
// ended record. It never resurrects a session or infers ownership from a name.
// Branches stay: history does not retain enough information to claim ownership.
func (d *Daemon) handleRemoveEndedWorktree(ctx context.Context, id, path string, force bool) (protocol.KillData, error) {
	refuse := func(message string) (protocol.KillData, error) {
		return protocol.KillData{}, errors.New(message)
	}
	if !sshstore.ValidSession(id) || !filepath.IsAbs(path) {
		return refuse("an ended session and its absolute worktree path are required")
	}
	d.mu.Lock()
	home, nat := d.home, d.native
	d.mu.Unlock()
	ended := sshstore.ReadEnded(home, id)
	if ended == nil || ended.ID != id || ended.KeptWorktree != path {
		return refuse("the retained worktree changed; refresh and try again")
	}
	d.mu.Lock()
	p := d.cfg.ProjectByName(ended.Project)
	if p == nil || p.IsStack() {
		d.mu.Unlock()
		return refuse("the original standalone project is required to remove this worktree")
	}
	project := *p
	d.mu.Unlock()
	if !strings.HasPrefix(id, "lola-"+project.Name+"-") {
		return refuse("this is not a standalone managed worktree")
	}
	if filepath.Clean(path) != filepath.Join(home, "worktrees", project.Name, id) {
		return refuse("the retained worktree no longer matches its runtime path")
	}
	if nat == nil {
		return refuse("native runtime unavailable")
	}
	root, err := filepath.EvalSymlinks(filepath.Join(home, "worktrees"))
	if err != nil {
		return protocol.KillData{}, err
	}
	want := filepath.Join(root, project.Name, id)
	actual, err := filepath.EvalSymlinks(path)
	if err != nil || actual != want || filepath.Dir(filepath.Dir(want)) != root {
		return refuse("the retained worktree is outside its original managed folder or has been replaced by a link")
	}
	// A stale history entry cannot authorize removal of a newly adopted session,
	// borrowed checkout, or stack member. Check overlaps in both directions.
	for _, s := range d.sessions.Snapshot() {
		paths := []string{s.Worktree, sessionWorktree(home, s)}
		for _, app := range s.Apps {
			paths = append(paths, app.Worktree)
		}
		for _, other := range paths {
			if other == "" {
				continue
			}
			if resolved, err := filepath.EvalSymlinks(other); err == nil {
				other = resolved
			}
			if s.ID == id || endedPathOverlaps(actual, other) {
				return refuse("this worktree belongs to a current session; refresh and remove it through that session")
			}
		}
		if s.ID == id {
			return refuse("this session is current again; refresh and try again")
		}
	}
	ctx, cancel := context.WithTimeout(ctx, killExecTimeout)
	defer cancel()
	names, err := d.livePaneNames(ctx)
	if err != nil {
		return protocol.KillData{}, fmt.Errorf("cannot verify that the session ended: %w", err)
	}
	for _, name := range names {
		if name == id || strings.HasPrefix(name, id+"-") || strings.HasPrefix(name, id+"~") {
			return refuse("this session still has terminals; close them before removing its retained worktree")
		}
	}
	// Never re-capture a pin from an agent-writable .git pointer. New history
	// preserves isolation; older history requires a surviving pin or explicit
	// consent to discarding files whose cleanliness cannot be verified.
	sandbox := "unknown"
	knownHost := false
	if isolation := ended.Isolation; isolation != nil {
		sandbox = isolation.Sandbox
		if sandbox == "" && isolation.GitPin != nil {
			sandbox = "unknown" // a pin outlives a move back to a host agent
		}
		knownHost = sandbox == "" && isolation.GitPin == nil
		if isolation.GitPin != nil && isolation.GitPin.Valid() {
			gitguard.Set(path, *isolation.GitPin)
		}
	}
	pinnedPath, pin, pinned := gitguard.Lookup(path)
	if !knownHost && (!pinned || pinnedPath != filepath.Clean(path) || !pin.Valid()) {
		if !force {
			return refuse("retained worktree changes cannot be verified safely; explicit discard confirmation is required")
		}
		gitguard.Deny(path)
	}
	// Treat unknown isolation as sandboxed: no pre_remove host commands, and
	// remove any surviving container through the normal runtime teardown.
	s := session.Session{ID: id, Project: project.Name, Worktree: path, TmuxName: id, Sandbox: sandbox, Manual: true}
	d.sshCloseSession(ctx, id)
	if err := nat.Kill(ctx, s, true, force); err != nil {
		return protocol.KillData{}, err
	}
	if st, err := d.sshStore(id, false); err == nil && st != nil {
		if err := st.Update(func(state *sshstore.State) error {
			if state.Ended != nil && state.Ended.KeptWorktree == path {
				state.Ended.KeptWorktree = ""
			}
			return nil
		}); err != nil {
			d.logf(project.Name, "retained worktree removed; history update failed: %v", err)
		}
	}
	d.talk.forgetEnded()
	d.logf(project.Name, "retained worktree %s removed (force=%t); local branches kept", id, force)
	return protocol.KillData{Removed: true, Worktree: path, Message: "Retained worktree removed; local branches kept"}, nil
}

func endedPathOverlaps(a, b string) bool {
	within := func(path, root string) bool {
		rel, err := filepath.Rel(root, path)
		return err == nil && (rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))))
	}
	return within(a, b) || within(b, a)
}
