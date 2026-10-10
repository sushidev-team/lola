package daemon

// overlap.go flags sessions of the same project that are editing the same
// files, BEFORE either PR conflicts. Merging parallel agent work is where
// conflicts are born, and the merge_conflict reaction only fires once GitHub
// has already declared one — by then both agents have built on top of the
// collision. Knowing early lets a human re-scope one of them, or merge the
// first and let the second build on it.
//
// Rules that hold it together:
//
//   - LOCAL git only (gitdiff.ChangedFiles: merge-base + `diff --name-status`
//     + `ls-files --others`), so it can run every observe cycle without a
//     network call. Uncommitted and untracked work counts — an agent mid-turn
//     has most of its work there, and that is exactly when warning is useful.
//   - DERIVED, never remembered: Session.Overlaps is recomputed from the
//     worktrees each cycle, so a file reverted or a session merged drops out
//     on its own.
//   - DISPLAY-ONLY. A chip and an attention hint in the clients; nothing in
//     dispatch, reactions, the merge queue or send-keys reads it. A file list
//     is evidence for a human, not a reason for lola to act.
//   - FAILS OPEN toward silence: a worktree git cannot read contributes no
//     files (and so no flag) rather than a guess, and logs once per distinct
//     error instead of every 30s.

import (
	"context"
	"os"
	"slices"

	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/session"
	"github.com/sushidev-team/lola/internal/state"
)

// overlapMaxFiles caps how many shared paths travel per pair; More counts the
// rest. A lockfile-plus-generated-code collision can share hundreds, and the
// clients only ever show a handful.
const overlapMaxFiles = 20

// overlapCandidate reports whether a session's worktree takes part: a native
// session with a worktree whose PR is not finished. A merged or closed PR's
// work is either on the default branch already (and so in every other
// session's merge-base soon) or abandoned — flagging it would be noise.
func overlapCandidate(s session.Session) bool {
	if s.Source != "native" || s.Worktree == "" {
		return false
	}
	switch s.Delivery {
	case state.DeliveryMerged, state.DeliveryClosed:
		return false
	}
	return true
}

// reconcileOverlaps recomputes Session.Overlaps for every session. Called once
// per observe cycle; ctx is the observer's unbounded context, so every git run
// carves its own observeExecTimeout.
func (d *Daemon) reconcileOverlaps(ctx context.Context) {
	if d.changedFiles == nil {
		return
	}
	d.mu.Lock()
	baseByProject := make(map[string]string, len(d.cfg.Projects))
	for _, p := range d.cfg.Projects {
		base := p.DefaultBranch
		if base == "" {
			base = config.DefaultBranchName
		}
		baseByProject[p.Name] = base
	}
	d.mu.Unlock()

	snap := d.sessions.Snapshot()
	byProject := map[string][]session.Session{}
	for _, s := range snap {
		if _, ok := baseByProject[s.Project]; ok && overlapCandidate(s) {
			byProject[s.Project] = append(byProject[s.Project], s)
		}
	}

	want := map[string][]session.Overlap{}
	for project, group := range byProject {
		if len(group) < 2 {
			continue // nothing to collide with: spend no git run at all
		}
		files := make([]map[string]bool, len(group))
		for i, s := range group {
			files[i] = d.sessionChangedFiles(ctx, s, baseByProject[project])
		}
		for i := range group {
			for j := i + 1; j < len(group); j++ {
				shared := sharedFiles(files[i], files[j])
				if len(shared) == 0 {
					continue
				}
				want[group[i].ID] = append(want[group[i].ID], newOverlap(group[j], shared))
				want[group[j].ID] = append(want[group[j].ID], newOverlap(group[i], shared))
			}
		}
	}

	for _, s := range snap {
		next := want[s.ID]
		if overlapsEqual(s.Overlaps, next) {
			continue
		}
		d.sessions.Update(s.ID, func(cur *session.Session) bool {
			if overlapsEqual(cur.Overlaps, next) {
				return false
			}
			cur.Overlaps = next
			return true
		})
	}
	for id := range d.overlapWarned {
		if _, ok := d.sessions.Get(id); !ok {
			delete(d.overlapWarned, id)
		}
	}
}

// sessionChangedFiles reads one worktree's changed paths as a set; nil when it
// cannot be read (missing directory, no merge-base, git failure).
func (d *Daemon) sessionChangedFiles(ctx context.Context, s session.Session, base string) map[string]bool {
	if _, err := os.Stat(s.Worktree); err != nil {
		return nil // the checkout is gone (teardown in progress): nothing to compare, nothing to log
	}
	cctx, cancel := context.WithTimeout(ctx, observeExecTimeout)
	paths, err := d.changedFiles(cctx, s.Worktree, base)
	cancel()
	if err != nil {
		if msg := err.Error(); d.overlapWarned[s.ID] != msg {
			d.overlapWarned[s.ID] = msg
			d.logf("", "overlap: changed files for %s unavailable (not compared this cycle): %v", s.ID, err)
		}
		return nil
	}
	delete(d.overlapWarned, s.ID)
	set := make(map[string]bool, len(paths))
	for _, p := range paths {
		set[p] = true
	}
	return set
}

// sharedFiles is the sorted intersection of two path sets.
func sharedFiles(a, b map[string]bool) []string {
	if len(a) > len(b) {
		a, b = b, a
	}
	var out []string
	for p := range a {
		if b[p] {
			out = append(out, p)
		}
	}
	slices.Sort(out)
	return out
}

func newOverlap(other session.Session, shared []string) session.Overlap {
	o := session.Overlap{Session: other.ID, Issue: other.Issue, Files: shared}
	if len(shared) > overlapMaxFiles {
		o.Files, o.More = shared[:overlapMaxFiles], len(shared)-overlapMaxFiles
	}
	return o
}

func overlapsEqual(a, b []session.Overlap) bool {
	return slices.EqualFunc(a, b, func(x, y session.Overlap) bool {
		return x.Session == y.Session && x.Issue == y.Issue && x.More == y.More && slices.Equal(x.Files, y.Files)
	})
}
