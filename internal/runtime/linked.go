package runtime

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/gitguard"
	"github.com/sushidev-team/lola/internal/session"
)

// LINKED REPOSITORIES ([[project.linked_repo]]): one issue that spans several
// repositories. The session keeps ONE directory — its primary worktree — and
// every linked repository gets a worktree of its own INSIDE it, at
// .lola/repos/<name>, on the session's branch. Three things follow from that
// placement, and each is load-bearing:
//
//   - .lola/ is excluded from the primary repo's git, so the nested checkouts
//     never show up as untracked files there (and never block a clean teardown).
//   - A sandboxed session's container mounts the primary worktree, so the
//     linked worktrees are inside the sandbox without a mount of their own.
//   - Teardown must remove them FIRST and dirty-safe: removing the primary
//     worktree deletes the directory tree, and uncommitted work in a linked
//     checkout would go with it (git only dirty-checks the primary).

// LinkedWorktreeDir is where linked repository name lives inside the session
// worktree dir.
func LinkedWorktreeDir(dir, name string) string {
	return filepath.Join(dir, lolaDir, "repos", name)
}

// linkedProject is the worktree manager's view of a linked repository: the
// manager keys git commands by Path and keeps worktrees under Root/<Name>.
func linkedProject(project, path string) config.Project {
	return config.Project{Name: project, Path: path}
}

// linkedBranchTaken reports whether branch already exists in any of p's linked
// repositories — freeSessionSlot's collision check, so a retry suffix is
// chosen when an earlier attempt's linked branch was kept.
func (n *Native) linkedBranchTaken(ctx context.Context, p config.Project, branch string) (bool, error) {
	for _, l := range p.LinkedRepos {
		taken, err := n.WT.BranchExists(ctx, linkedProject(p.Name, l.Path), branch)
		if err != nil || taken {
			return taken, err
		}
	}
	return false, nil
}

// createLinked checks every linked repository of p out into the session
// worktree dir on branch. On a failure it returns the ones it DID create
// alongside the error: rolling them back is the caller's, because only the
// caller can decide to keep the primary worktree when that rollback refuses.
// A sandboxed project's linked worktrees are pinned like the primary one.
func (n *Native) createLinked(ctx context.Context, p config.Project, dir, branch string) ([]session.LinkedRepo, error) {
	var out []session.LinkedRepo
	for _, l := range p.LinkedRepos {
		wt := LinkedWorktreeDir(dir, l.Name)
		if err := n.WT.CreateLinked(ctx, l.Path, wt, branch, l.BaseBranch(p)); err != nil {
			return out, fmt.Errorf("linked repo %s: %w", l.Name, err)
		}
		rec := session.LinkedRepo{Name: l.Name, Repo: l.Repo, Path: l.Path, Worktree: wt, Branch: branch}
		out = append(out, rec)
		pin, err := n.pinSandbox(p.Name, wt)
		if err != nil {
			return out, fmt.Errorf("linked repo %s: pin sandbox git layout: %w", l.Name, err)
		}
		out[len(out)-1].GitPin = pin
	}
	return out, nil
}

// discoverLinked rebuilds a session's linked-worktree records from disk for
// adoption, when the store lost them: every .lola/repos/<name> that names one
// of the project's linked repos. Branch is left empty (it is not observable
// here), so a teardown of such a record keeps the branch; GitPin is nil, so a
// sandboxed one's host git is DENIED until a human looks (the daemon's pin
// sync). Unknown entries are not recorded — Kill's unaccountedLinked guard
// then keeps the whole worktree.
func discoverLinked(p config.Project, dir string) []session.LinkedRepo {
	var out []session.LinkedRepo
	for _, l := range p.LinkedRepos {
		wt := LinkedWorktreeDir(dir, l.Name)
		if fi, err := os.Stat(wt); err == nil && fi.IsDir() {
			out = append(out, session.LinkedRepo{Name: l.Name, Repo: l.Repo, Path: l.Path, Worktree: wt})
		}
	}
	return out
}

// unaccountedLinked lists what is left under dir/.lola/repos — called after
// the recorded linked worktrees were removed, so every entry is one no dirty
// check has seen.
func unaccountedLinked(dir string) []string {
	entries, err := os.ReadDir(filepath.Join(dir, lolaDir, "repos"))
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}

// removeLinked tears a session's linked worktrees down, each through the
// manager's dirty-safe Remove (so a checkout holding uncommitted work refuses
// with worktree.ErrDirty and is kept). A worktree already gone still has its
// branch deleted. ownsBranch=false keeps every branch (none is lola's). Every
// linked repo is attempted; the errors are joined.
func (n *Native) removeLinked(ctx context.Context, project string, linked []session.LinkedRepo, ownsBranch, force bool) error {
	var errs []error
	for _, l := range linked {
		if l.Path == "" || l.Worktree == "" {
			continue
		}
		lp := linkedProject(project, l.Path)
		branch := ""
		if ownsBranch {
			branch = l.Branch
		}
		if _, err := os.Stat(l.Worktree); errors.Is(err, fs.ErrNotExist) {
			if err := n.WT.DeleteBranch(ctx, lp, branch); err != nil {
				errs = append(errs, fmt.Errorf("linked repo %s: %w", l.Name, err))
			}
			continue
		}
		if err := n.WT.Remove(ctx, lp, l.Worktree, branch, force); err != nil {
			errs = append(errs, fmt.Errorf("linked repo %s: %w", l.Name, err))
			continue
		}
		gitguard.Clear(l.Worktree)
	}
	return errors.Join(errs...)
}

// withLinkedBriefing appends the linked-repositories section to a prompt.md
// body: where each checkout is, that it is already on the session's branch,
// and that each one with a change needs its own PR — cross-linked, so a
// reviewer of one finds the others.
func withLinkedBriefing(prompt []byte, p config.Project, linked []session.LinkedRepo) []byte {
	if len(linked) == 0 {
		return prompt
	}
	var b strings.Builder
	b.Write(prompt)
	b.WriteString("\n## Linked repositories\n\n")
	b.WriteString("This issue may span more than one repository. Besides this worktree, each repository below is checked out for you on the SAME branch:\n\n")
	bases := map[string]string{}
	for _, l := range p.LinkedRepos {
		bases[l.Name] = l.BaseBranch(p)
	}
	for _, l := range linked {
		repo := l.Repo
		if repo == "" {
			repo = "(no GitHub repo configured)"
		}
		fmt.Fprintf(&b, "- `%s/repos/%s` — %s, branch `%s`, base `%s`\n", lolaDir, l.Name, repo, l.Branch, bases[l.Name])
	}
	b.WriteString("\nMake each change in the repository it belongs to. For every repository you changed, commit there, push the branch and open a pull request against its base branch. Link the pull requests to each other in their descriptions (\"Part of …, see also <other PR URL>\"). Leave a repository you did not need untouched — no empty pull requests.\n")
	return []byte(b.String())
}
