package worktree

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/gitrepo"
)

// DeleteMergedRemoteBranch removes an owned branch only at the merged PR's
// exact head. The lease protects a concurrent push; an already-deleted branch
// is success (GitHub may have automatically deleted it on merge).
func (m *Manager) DeleteMergedRemoteBranch(ctx context.Context, p config.Project, branch, headSHA string) error {
	if branch == "" || branch == p.DefaultBranch || p.Path == "" || p.Repo == "" {
		return errors.New("worktree: cannot identify an owned remote branch")
	}
	if _, err := hex.DecodeString(headSHA); err != nil || len(headSHA) != 40 {
		return errors.New("worktree: missing or invalid merged head SHA")
	}
	ref := "refs/heads/" + branch
	if _, _, err := m.git(ctx, "check-ref-format", ref); err != nil {
		return err
	}
	// Check the PUSH destination, including multiple push URLs. Never delete
	// from a fork or another repository just because it is named origin.
	out, _, err := m.git(ctx, "-C", p.Path, "remote", "get-url", "--push", "--all", "origin")
	if err != nil {
		return err
	}
	urls := strings.Fields(out)
	if len(urls) != 1 || !strings.EqualFold(gitrepo.ParseRemoteURL(urls[0]), p.Repo) {
		return errors.New("worktree: origin push destination does not match the merged PR repository")
	}
	out, _, err = m.git(ctx, "-C", p.Path, "ls-remote", "--exit-code", "--refs", urls[0], ref)
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && ee.ExitCode() == 2 {
			m.pruneMergedRemoteRef(ctx, p.Path, branch, headSHA)
			return nil // no matching ref; authentication/transport errors are not success
		}
		return err
	}
	if fields := strings.Fields(out); len(fields) != 2 || fields[0] != headSHA || fields[1] != ref {
		return fmt.Errorf("worktree: remote branch %s no longer matches the merged PR head", branch)
	}
	// Use the verified URL so a changed origin configuration cannot redirect
	// the deletion. No hooks: cleanup must not run the project's pre-push hook.
	_, _, err = m.git(ctx, "-C", p.Path, "push", "--no-verify",
		"--force-with-lease="+ref+":"+headSHA, "--", urls[0], ":"+ref)
	if err == nil {
		m.pruneMergedRemoteRef(ctx, p.Path, branch, headSHA)
	}
	return err
}

func (m *Manager) pruneMergedRemoteRef(ctx context.Context, dir, branch, headSHA string) {
	// Pushing by URL does not update origin's cached ref. Best-effort, and
	// compare-and-delete so a concurrent fetch of a new head is preserved.
	_, _, _ = m.git(ctx, "-C", dir, "update-ref", "--no-deref", "-d", "refs/remotes/origin/"+branch, headSHA)
}
