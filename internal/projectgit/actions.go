package projectgit

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// BranchRequest names full refs and the exact tips the human reviewed.
// Merge integrates Source into Target; rebase replays Source onto Target.
type BranchRequest struct {
	Action       string `json:"action"`
	Source       string `json:"source"`
	Target       string `json:"target"`
	SourceSHA    string `json:"sourceSHA"`
	TargetSHA    string `json:"targetSHA"`
	Remote       string `json:"remote,omitempty"`
	RemoteBranch string `json:"remoteBranch,omitempty"`
	Mainline     int    `json:"mainline,omitempty"`
}
type BranchResult struct {
	Path string `json:"path"`
}

// Operation reports existing Git state, including operations started outside Lola.
func (c Client) Operation(ctx context.Context, dir string) (string, error) {
	for _, entry := range []struct{ path, kind string }{{"rebase-merge", "rebase"}, {"rebase-apply", "rebase"}, {"MERGE_HEAD", "merge"}, {"CHERRY_PICK_HEAD", "cherry-pick"}, {"REVERT_HEAD", "revert"}} {
		path, err := c.text(ctx, dir, "rev-parse", "--git-path", entry.path)
		if err != nil {
			return "", err
		}
		if !filepath.IsAbs(path) {
			path = filepath.Join(dir, path)
		}
		if _, err = os.Stat(path); err == nil {
			return entry.kind, nil
		} else if !os.IsNotExist(err) {
			return "", err
		}
	}
	return "", nil
}
func (c Client) requireIdle(ctx context.Context, dir string) error {
	op, err := c.Operation(ctx, dir)
	if err != nil {
		return err
	}
	if op != "" {
		return fmt.Errorf("finish or abort the %s in progress first", op)
	}
	return nil
}
func (c Client) requireClean(ctx context.Context, dir string) error {
	if err := c.requireIdle(ctx, dir); err != nil {
		return err
	}
	files, err := c.status(ctx, dir)
	if err != nil {
		return err
	}
	if len(files) > 0 {
		return errors.New("commit or stash this checkout's changes before switching or integrating branches")
	}
	return nil
}
func (c Client) branchRef(ctx context.Context, dir, ref, expected string, local bool) (string, error) {
	if !strings.HasPrefix(ref, "refs/heads/") && (local || !strings.HasPrefix(ref, "refs/remotes/")) {
		return "", errors.New("choose a local or remote branch")
	}
	if strings.HasPrefix(ref, "refs/heads/-") {
		return "", errors.New("branch names beginning with a dash cannot be used for checkout actions")
	}
	if _, err := c.git(ctx, dir, "", "check-ref-format", ref); err != nil {
		return "", errors.New("invalid branch reference")
	}
	sha, err := c.text(ctx, dir, "rev-parse", "--verify", ref+"^{commit}")
	if err != nil {
		return "", err
	}
	if !shaPattern.MatchString(expected) || sha != expected {
		return "", errors.New("branch changed since it was selected; refresh and try again")
	}
	return sha, nil
}
func (c Client) branchOwner(ctx context.Context, dir, ref string) (string, error) {
	trees, err := c.Worktrees(ctx, dir)
	if err != nil {
		return "", err
	}
	for _, tree := range trees {
		if "refs/heads/"+tree.Branch == ref {
			if tree.Prunable {
				return "", errors.New("branch belongs to an unavailable worktree")
			}
			return tree.Path, nil
		}
	}
	return "", nil
}

// ActionBranch identifies which local checkout a service must authorize before
// calling BranchAction. It does not itself authorize any path.
func (c Client) ActionBranch(ctx context.Context, dir string, r BranchRequest) (string, error) {
	switch r.Action {
	case "merge":
		return r.Target, nil
	case "rebase", "pull", "push", "publish":
		if strings.HasPrefix(r.Source, "refs/heads/") {
			return r.Source, nil
		}
		return "", nil
	case "checkout":
		if strings.HasPrefix(r.Source, "refs/heads/") {
			return r.Source, nil
		}
		remote, branch, err := c.remoteBranch(ctx, dir, r.Source)
		if err != nil {
			return "", err
		}
		if strings.HasPrefix(branch, "-") {
			return "", errors.New("remote branch name cannot be used as a local checkout")
		}
		local := "refs/heads/" + branch
		if _, err = c.text(ctx, dir, "show-ref", "--verify", "--hash", local); err == nil {
			upstream, err := c.text(ctx, dir, "for-each-ref", "--format=%(upstream)", local)
			if err != nil || upstream != "refs/remotes/"+remote+"/"+branch {
				return "", errors.New("a different local branch already has this name; choose it explicitly")
			}
			return local, nil
		}
		return local, nil
	default:
		return "", nil
	}
}
func (c Client) remoteBranch(ctx context.Context, dir, ref string) (string, string, error) {
	if !strings.HasPrefix(ref, "refs/remotes/") {
		return "", "", errors.New("choose a remote branch")
	}
	names, err := c.text(ctx, dir, "remote")
	if err != nil {
		return "", "", err
	}
	best := ""
	for _, name := range strings.Split(names, "\n") {
		if name != "" && strings.HasPrefix(ref, "refs/remotes/"+name+"/") && len(name) > len(best) {
			best = name
		}
	}
	if best == "" {
		return "", "", errors.New("remote no longer exists")
	}
	branch := strings.TrimPrefix(ref, "refs/remotes/"+best+"/")
	if branch == "HEAD" {
		return "", "", errors.New("choose the named remote branch instead of HEAD")
	}
	return best, branch, nil
}
func sameCheckout(a, b string) bool {
	if resolved, err := filepath.EvalSymlinks(a); err == nil {
		a = resolved
	}
	if resolved, err := filepath.EvalSymlinks(b); err == nil {
		b = resolved
	}
	return filepath.Clean(a) == filepath.Clean(b)
}
func (c Client) checkout(ctx context.Context, dir, ref string) error {
	owner, err := c.branchOwner(ctx, dir, ref)
	if err != nil {
		return err
	}
	if owner != "" && !sameCheckout(owner, dir) {
		return errors.New("branch is checked out elsewhere; open its worktree")
	}
	current, _ := c.text(ctx, dir, "symbolic-ref", "--quiet", "HEAD")
	if current == ref {
		return nil
	}
	if err = c.requireClean(ctx, dir); err != nil {
		return err
	}
	_, err = c.git(ctx, dir, "", "switch", "--", strings.TrimPrefix(ref, "refs/heads/"))
	return err
}
func (c Client) BranchAction(ctx context.Context, dir string, r BranchRequest) error {
	if r.Action == "continue" || r.Action == "abort" {
		op, err := c.Operation(ctx, dir)
		if err != nil {
			return err
		}
		if op != r.Source || (op != "merge" && op != "rebase" && op != "cherry-pick" && op != "revert") {
			return errors.New("no matching operation to continue or abort")
		}
		if err := c.requireHead(ctx, dir, r.SourceSHA); err != nil {
			return err
		}
		// The UI explicitly confirms abort; continue uses Git's prepared message,
		// while retaining normal signing and hooks without launching an editor.
		_, err = c.git(ctx, dir, "", "-c", "core.editor=true", op, "--"+r.Action)
		return err
	}
	if r.Action == "cherry-pick" || r.Action == "revert" {
		return c.commitAction(ctx, dir, r)
	}
	if r.Action == "fetch" {
		names, err := c.text(ctx, dir, "remote")
		if err != nil {
			return err
		}
		found := false
		for _, name := range strings.Split(names, "\n") {
			if name == r.Source && name != "" {
				found = true
			}
		}
		if !found {
			return errors.New("remote no longer exists")
		}
		_, err = c.git(ctx, dir, "", "fetch", "--", r.Source)
		return err
	}
	if _, err := c.branchRef(ctx, dir, r.Source, r.SourceSHA, r.Action == "rebase"); err != nil {
		return err
	}
	if err := c.requireIdle(ctx, dir); err != nil {
		return err
	}
	switch r.Action {
	case "publish":
		return c.publishBranch(ctx, dir, r)
	case "checkout":
		if strings.HasPrefix(r.Source, "refs/heads/") {
			return c.checkout(ctx, dir, r.Source)
		}
		local, err := c.ActionBranch(ctx, dir, r)
		if err != nil {
			return err
		}
		if _, err = c.text(ctx, dir, "show-ref", "--verify", "--hash", local); err == nil {
			return c.checkout(ctx, dir, local)
		}
		if err = c.requireClean(ctx, dir); err != nil {
			return err
		}
		_, err = c.git(ctx, dir, "", "switch", "--track", "-c", strings.TrimPrefix(local, "refs/heads/"), "--", strings.TrimPrefix(r.Source, "refs/remotes/"))
		return err
	case "pull":
		return c.pullBranch(ctx, dir, r)
	case "push":
		if !strings.HasPrefix(r.Source, "refs/heads/") {
			return errors.New("only local branches can be pushed")
		}
		remote, merge, err := c.upstream(ctx, dir, r.Source)
		if err != nil {
			return err
		}
		if remote == "." {
			return errors.New("this branch tracks a local branch, not a push remote")
		}
		_, err = c.git(ctx, dir, "", "push", "--", remote, r.Source+":"+merge)
		return err
	case "merge", "rebase":
		if r.Source == r.Target {
			return errors.New("choose two different branches")
		}
		if _, err := c.branchRef(ctx, dir, r.Target, r.TargetSHA, r.Action == "merge"); err != nil {
			return err
		}
		if err := c.requireClean(ctx, dir); err != nil {
			return err
		}
		branch := r.Target
		if r.Action == "rebase" {
			branch = r.Source
		}
		if err := c.checkout(ctx, dir, branch); err != nil {
			return err
		}
		// Verify both refs again after switching: external Git may have moved them.
		if _, err := c.branchRef(ctx, dir, r.Source, r.SourceSHA, r.Action == "rebase"); err != nil {
			return err
		}
		if _, err := c.branchRef(ctx, dir, r.Target, r.TargetSHA, r.Action == "merge"); err != nil {
			return err
		}
		var err error
		if r.Action == "merge" {
			_, err = c.git(ctx, dir, "", "merge", "--no-edit", "--", r.SourceSHA)
		} else {
			_, err = c.git(ctx, dir, "", "-c", "rebase.autoStash=false", "-c", "rebase.updateRefs=false", "rebase", "--no-autostash", "--", r.TargetSHA)
		}
		return err
	default:
		return errors.New("unknown branch action")
	}
}
func (c Client) upstream(ctx context.Context, dir, ref string) (string, string, error) {
	branch := strings.TrimPrefix(ref, "refs/heads/")
	remote, err := c.text(ctx, dir, "config", "--get", "branch."+branch+".remote")
	if err != nil || remote == "" {
		return "", "", errors.New("branch has no upstream; configure its tracking branch first")
	}
	merge, err := c.text(ctx, dir, "config", "--get", "branch."+branch+".merge")
	if err != nil || !strings.HasPrefix(merge, "refs/heads/") {
		return "", "", errors.New("branch has no upstream merge branch")
	}
	if _, err = c.git(ctx, dir, "", "check-ref-format", merge); err != nil {
		return "", "", err
	}
	return remote, merge, nil
}
func (c Client) pullBranch(ctx context.Context, dir string, r BranchRequest) error {
	if strings.HasPrefix(r.Source, "refs/remotes/") {
		remote, branch, err := c.remoteBranch(ctx, dir, r.Source)
		if err != nil {
			return err
		}
		_, err = c.git(ctx, dir, "", "fetch", "--", remote, "refs/heads/"+branch+":"+r.Source)
		return err
	}
	remote, merge, err := c.upstream(ctx, dir, r.Source)
	if err != nil {
		return err
	}
	owner, err := c.branchOwner(ctx, dir, r.Source)
	if err != nil {
		return err
	}
	if owner != "" && !sameCheckout(owner, dir) {
		return errors.New("branch is checked out elsewhere; open its worktree")
	}
	if owner != "" {
		if err = c.requireClean(ctx, dir); err != nil {
			return err
		}
	}
	// FETCH_HEAD is used only for the explicitly configured upstream. No forced
	// ref update and no --update-head-ok; the expected-old update is atomic.
	if _, err = c.git(ctx, dir, "", "fetch", "--", remote, merge); err != nil {
		return err
	}
	incoming, err := c.text(ctx, dir, "rev-parse", "--verify", "FETCH_HEAD^{commit}")
	if err != nil {
		return err
	}
	if _, err = c.branchRef(ctx, dir, r.Source, r.SourceSHA, true); err != nil {
		return err
	}
	if _, err = c.git(ctx, dir, "", "merge-base", "--is-ancestor", r.SourceSHA, incoming); err != nil {
		if _, aheadErr := c.git(ctx, dir, "", "merge-base", "--is-ancestor", incoming, r.SourceSHA); aheadErr == nil {
			return nil
		}
		return errors.New("branch has diverged; check it out and merge or rebase the upstream explicitly")
	}
	// Recheck ownership after network I/O; Git in another process can create a
	// checkout while fetch runs. Never move a ref underneath its working tree.
	now, err := c.branchOwner(ctx, dir, r.Source)
	if err != nil {
		return err
	}
	if now != owner {
		return errors.New("branch checkout changed while fetching; refresh and try again")
	}
	if owner != "" {
		current, err := c.text(ctx, dir, "symbolic-ref", "--quiet", "HEAD")
		if err != nil || current != r.Source {
			return errors.New("checkout changed while fetching; refresh and try again")
		}
		if err = c.requireClean(ctx, dir); err != nil {
			return err
		}
		_, err = c.git(ctx, dir, "", "merge", "--ff-only", "--no-edit", "--", incoming)
		return err
	}
	_, err = c.git(ctx, dir, "", "update-ref", "-m", "lola: fast-forward upstream", r.Source, incoming, r.SourceSHA)
	return err
}
