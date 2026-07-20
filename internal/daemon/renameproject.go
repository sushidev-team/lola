package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/protocol"
)

// handleRenameProject changes a [[project]]'s NAME — its identity, not its
// display Label. A label is free text any client rewrites with an ordinary
// config save; the name is a path segment (~/.lola/worktrees/<name>/,
// ~/.lola/state/<name>.seen) and a prefix of every session ID (and therefore of
// every tmux session name), so renaming it means migrating runtime state and
// only the daemon may do it.
//
// It is deliberately IDLE-ONLY. Migrating a live session would mean moving its
// worktree directory (which deregisters it from git — see worktree.Manager's
// header), repairing that registration, renaming its tmux session, and
// rewriting its stored ID, worktree path and tmux name, with a partial-failure
// state at every step. Refusing instead keeps the operation to two atomic
// renames, and the human loses nothing but the wait: sessions are short-lived.
//
// Ordering mirrors dispatch's fail-closed discipline: every check that can
// refuse runs BEFORE anything is written, and the config is saved before the
// runtime state moves, so a crash in the middle leaves a config the next reload
// can still make sense of (a project whose seen file has simply gone missing
// re-dispatches; a seen file with no project is inert).
func (d *Daemon) handleRenameProject(ctx context.Context, raw json.RawMessage) (protocol.RenameProjectData, error) {
	var args protocol.RenameProjectArgs
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &args); err != nil {
			return protocol.RenameProjectData{}, fmt.Errorf("bad renameProject args: %w", err)
		}
	}
	from, to := args.From, args.To
	switch {
	case from == "":
		return protocol.RenameProjectData{}, errors.New("renameProject: from is required")
	case to == "":
		return protocol.RenameProjectData{}, errors.New("renameProject: to is required")
	case from == to:
		return protocol.RenameProjectData{From: from, To: to, Message: "name unchanged"}, nil
	case !config.IsSlug(to):
		// The client is expected to have slugified already; refusing rather than
		// silently rewriting keeps the name the human confirmed the one they get.
		return protocol.RenameProjectData{}, fmt.Errorf(
			"renameProject: %q is not a valid project id (lowercase letters, digits, . _ -)", to)
	}

	// Hold BOTH names' tick mutexes for the whole operation: the old one so a
	// tick cannot be reading/writing its seen file mid-rename, the new one so a
	// tick started under the new name (post-reload) cannot race the same file.
	// Order by name so two concurrent renames cannot deadlock.
	first, second := from, to
	if second < first {
		first, second = second, first
	}
	mu1, mu2 := d.tickMutex(first), d.tickMutex(second)
	mu1.Lock()
	defer mu1.Unlock()
	mu2.Lock()
	defer mu2.Unlock()

	// Rebase on the on-disk config, exactly as the TUI form does: another client
	// may have saved since this request was composed.
	path, err := config.DefaultPath()
	if err != nil {
		return protocol.RenameProjectData{}, err
	}
	nc, err := config.Load(path)
	if err != nil {
		return protocol.RenameProjectData{}, err
	}
	idx := -1
	for i := range nc.Projects {
		switch nc.Projects[i].Name {
		case from:
			idx = i
		case to:
			return protocol.RenameProjectData{}, fmt.Errorf("renameProject: a project named %q already exists", to)
		}
	}
	if idx < 0 {
		return protocol.RenameProjectData{}, fmt.Errorf("renameProject: project %q not found", from)
	}

	// Refuse while anything still carries the old name. The session store is the
	// authority (the same snapshot Budget counts), not a client's stale view.
	var blockers []string
	for _, s := range d.sessions.Snapshot() {
		if s.Project == from {
			blockers = append(blockers, s.ID)
		}
	}
	sort.Strings(blockers)
	if len(blockers) > 0 {
		return protocol.RenameProjectData{From: from, To: to, Blockers: blockers}, fmt.Errorf(
			"renameProject: %q still has %d session%s (%s); finish or kill them first — "+
				"their worktree paths and tmux names embed the project name",
			from, len(blockers), plural(len(blockers)), blockers[0])
	}

	// A leftover worktree directory means state the store no longer knows about
	// (a kill that half-failed, a manual mkdir). Renaming around it would strand
	// it under a name nothing resolves, so fail closed and let a human look.
	wtOld := filepath.Join(d.home, "worktrees", from)
	entries, err := os.ReadDir(wtOld)
	switch {
	case err != nil && !os.IsNotExist(err):
		return protocol.RenameProjectData{}, fmt.Errorf("renameProject: inspect %s: %w", wtOld, err)
	case len(entries) > 0:
		return protocol.RenameProjectData{}, fmt.Errorf(
			"renameProject: %s still holds %d worktree%s; remove them first",
			wtOld, len(entries), plural(len(entries)))
	}

	// --- past every refusal: start writing -------------------------------
	nc.Projects[idx].Name = to
	if err := nc.Validate(); err != nil {
		return protocol.RenameProjectData{}, fmt.Errorf("renameProject: config invalid after rename: %w", err)
	}
	if err := nc.Save(path); err != nil {
		return protocol.RenameProjectData{}, fmt.Errorf("renameProject: save config: %w", err)
	}

	// Move the seen guard so already-dispatched issues stay deduped. A failure
	// here is logged, not returned: the config rename has landed and is the part
	// that matters — a missing seen file only costs some re-dispatch.
	if err := d.seen.rename(from, to); err != nil {
		d.logf(to, "renameProject: carry over seen state from %q: %v (issues may re-dispatch once)", from, err)
	}
	// The now-empty worktrees/<from>/ dir is inert; drop it so a later project
	// reusing the name does not inherit a stale one. Best-effort by design.
	if err := os.Remove(wtOld); err != nil && !os.IsNotExist(err) {
		d.logf(to, "renameProject: remove empty %s: %v", wtOld, err)
	}

	if err := d.handleReload(ctx); err != nil {
		// Config on disk is already renamed, so this is recoverable by a manual
		// reload — say so rather than implying the rename failed.
		return protocol.RenameProjectData{From: from, To: to}, fmt.Errorf(
			"renameProject: renamed to %q on disk but reload failed: %w", to, err)
	}
	msg := fmt.Sprintf("project %q renamed to %q", from, to)
	d.logf(to, "renameProject: %s", msg)
	return protocol.RenameProjectData{From: from, To: to, Message: msg}, nil
}

// plural returns the "s" tail for a count.
func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
