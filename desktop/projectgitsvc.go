package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/gitdiff"
	"github.com/sushidev-team/lola/internal/gitguard"
	"github.com/sushidev-team/lola/internal/projectgit"
	"github.com/sushidev-team/lola/internal/session"
)

// ProjectGitService is the human repository interface, not an agent command
// channel. Repository identity comes from config; a caller cannot supply an
// arbitrary directory. Mutations are serialized so rapid clicks cannot race.
type ProjectGitService struct {
	pullRequests projectgit.PullRequestClient
	draftCommit  func(context.Context, string, string) (string, error)
	mu           sync.Mutex
	identities   projectgit.PersistentIdentityClient
}

func projectGitWithin(path, root string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && (rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))))
}

// The desktop has a separate process from the daemon, so its gitguard registry
// is not authoritative. Read persisted isolation records before any Git call.
func projectGitBlocker() (func(string) bool, error) {
	return projectGitPathBlocker(false)
}

func projectGitPathBlocker(removing bool) (func(string) bool, error) {
	home, err := config.Home()
	if err != nil {
		return nil, err
	}
	home = canonicalPath(home)
	var sessions []session.Session
	raw, err := os.ReadFile(filepath.Join(home, "state", "sessions.json"))
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	if err == nil {
		if err = json.Unmarshal(raw, &sessions); err != nil {
			return nil, errors.New("cannot verify session isolation; session state is unreadable")
		}
	}
	return func(path string) bool {
		path = canonicalPath(path)
		if removing && projectGitWithin(path, home) {
			return true
		}
		if gitguard.Pinned(path) {
			return true
		}
		// Goal workspaces are always isolated; they are not human working copies.
		if projectGitWithin(path, filepath.Join(home, "goals")) {
			return true
		}
		knownHost := false
		for _, s := range sessions {
			if s.Worktree == "" {
				continue
			}
			root := canonicalPath(s.Worktree)
			if removing && (projectGitWithin(path, root) || projectGitWithin(root, path)) {
				return true
			}
			if projectGitWithin(path, root) {
				if s.Sandbox != "" && s.Sandbox != "none" {
					return true
				}
				knownHost = true
			}
		}
		// A forgotten lola checkout is not proof it was never sandboxed.
		return projectGitWithin(path, filepath.Join(home, "worktrees")) && !knownHost
	}, nil
}

func projectGitTarget(ctx context.Context, project, requested string) (string, func(string) bool, error) {
	cfg, _, err := loadConfig()
	if err != nil {
		return "", nil, err
	}
	p := cfg.ProjectByName(project)
	if p == nil {
		return "", nil, errors.New("project no longer exists")
	}
	if p.IsStack() {
		return "", nil, errors.New("choose a repository inside this stack")
	}
	if p.Path == "" {
		return "", nil, errors.New("configure this project's repository folder first")
	}
	root := canonicalPath(p.Path)
	blocked, err := projectGitBlocker()
	if err != nil {
		return "", nil, err
	}
	if blocked(root) {
		return "", nil, errors.New("sandbox worktrees must be managed through their session")
	}
	trees, err := (projectgit.Client{}).Worktrees(ctx, root)
	if err != nil {
		return "", nil, err
	}
	wanted := root
	if requested != "" {
		if !filepath.IsAbs(requested) {
			return "", nil, errors.New("invalid worktree path")
		}
		wanted = canonicalPath(requested)
	}
	for _, tree := range trees {
		if canonicalPath(tree.Path) != wanted {
			continue
		}
		if tree.Prunable {
			return "", nil, errors.New("this worktree is no longer available")
		}
		if blocked(wanted) {
			return "", nil, errors.New("sandbox worktrees must be managed through their session")
		}
		return wanted, blocked, nil
	}
	return "", nil, errors.New("worktree does not belong to this project; refresh the repository")
}
func (s *ProjectGitService) Snapshot(project, worktree, ref string, limit int) (projectgit.Snapshot, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	dir, blocked, err := projectGitTarget(ctx, project, worktree)
	if err != nil {
		return projectgit.Snapshot{}, err
	}
	result, err := (projectgit.Client{}).Snapshot(ctx, dir, ref, limit)
	if err != nil {
		return result, err
	}
	for i := range result.Worktrees {
		result.Worktrees[i].Blocked = blocked(result.Worktrees[i].Path)
	}
	return result, nil
}
func (s *ProjectGitService) Diff(project, worktree, from, to, area, path string) (gitdiff.Result, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	dir, _, err := projectGitTarget(ctx, project, worktree)
	if err != nil {
		return gitdiff.Result{}, err
	}
	return (projectgit.Client{}).Diff(ctx, dir, from, to, area, path)
}
func (s *ProjectGitService) mutate(project, worktree string, fn func(context.Context, string) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	ctx, cancel := projectgit.Context()
	defer cancel()
	dir, _, err := projectGitTarget(ctx, project, worktree)
	if err != nil {
		return err
	}
	if err = fn(ctx, dir); err != nil {
		return fmt.Errorf("repository action failed: %w", err)
	}
	return nil
}
func (s *ProjectGitService) Stage(project, worktree string, paths []string) ([]projectgit.File, error) {
	return s.changeIndex(project, worktree, paths, true)
}
func (s *ProjectGitService) Unstage(project, worktree string, paths []string) ([]projectgit.File, error) {
	return s.changeIndex(project, worktree, paths, false)
}
func (s *ProjectGitService) changeIndex(project, worktree string, paths []string, stage bool) ([]projectgit.File, error) {
	var files []projectgit.File
	err := s.mutate(project, worktree, func(ctx context.Context, dir string) error {
		var err error
		files, err = (projectgit.Client{}).ChangeIndexAndStatus(ctx, dir, paths, stage)
		return err
	})
	return files, err
}
func (s *ProjectGitService) PrepareDiscard(project, worktree string) (projectgit.DiscardPlan, error) {
	result := projectgit.DiscardPlan{}
	err := s.mutate(project, worktree, func(ctx context.Context, dir string) error {
		var err error
		result, err = (projectgit.Client{}).PrepareDiscard(ctx, dir)
		return err
	})
	return result, err
}
func (s *ProjectGitService) DiscardUnstaged(project, worktree, token string) error {
	return s.mutate(project, worktree, func(ctx context.Context, dir string) error {
		return (projectgit.Client{}).DiscardUnstaged(ctx, dir, token)
	})
}
func (s *ProjectGitService) Commit(project, worktree, message string) error {
	return s.mutate(project, worktree, func(ctx context.Context, dir string) error { return (projectgit.Client{}).Commit(ctx, dir, message) })
}
func (s *ProjectGitService) Amend(project, worktree, message, expectedHead string) error {
	return s.mutate(project, worktree, func(ctx context.Context, dir string) error {
		return (projectgit.Client{}).Amend(ctx, dir, message, expectedHead)
	})
}
func (s *ProjectGitService) AmendNeedsConfirmation(project, worktree, expectedHead string) (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	dir, _, err := projectGitTarget(ctx, project, worktree)
	if err != nil {
		return false, err
	}
	return (projectgit.Client{}).AmendNeedsConfirmation(ctx, dir, expectedHead)
}
func (s *ProjectGitService) LastCommitMessage(project, worktree, expectedHead string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	dir, _, err := projectGitTarget(ctx, project, worktree)
	if err != nil {
		return "", err
	}
	return (projectgit.Client{}).LastCommitMessage(ctx, dir, expectedHead)
}
func (s *ProjectGitService) Fetch(project, worktree string) error {
	return s.mutate(project, worktree, func(ctx context.Context, dir string) error { return (projectgit.Client{}).Sync(ctx, dir, "fetch") })
}
func (s *ProjectGitService) Pull(project, worktree string) error {
	return s.mutate(project, worktree, func(ctx context.Context, dir string) error { return (projectgit.Client{}).Sync(ctx, dir, "pull") })
}
func (s *ProjectGitService) Push(project, worktree string) error {
	return s.mutate(project, worktree, func(ctx context.Context, dir string) error { return (projectgit.Client{}).Sync(ctx, dir, "push") })
}

// CreateBranch creates at a pinned commit in the explicitly selected checkout.
func (s *ProjectGitService) CreateBranch(project, worktree, name, startSHA string, checkout bool) (projectgit.BranchResult, error) {
	result := projectgit.BranchResult{}
	err := s.mutate(project, worktree, func(ctx context.Context, dir string) error {
		result.Path = dir
		return (projectgit.Client{}).CreateBranch(ctx, dir, name, startSHA, checkout)
	})
	return result, err
}

// CreateWorktree creates a human checkout outside managed storage and existing
// checkouts. The existing parent is resolved before checking isolation boundaries.
func (s *ProjectGitService) CreateWorktree(project, worktree, source, sha, path, newBranch string) (projectgit.BranchResult, error) {
	result := projectgit.BranchResult{}
	err := s.mutate(project, worktree, func(ctx context.Context, dir string) error {
		destination, err := projectgit.NewWorktreePath(path)
		if err != nil {
			return err
		}
		blocked, err := projectGitBlocker()
		if err != nil {
			return err
		}
		home, err := config.Home()
		if err != nil {
			return err
		}
		if blocked(destination) || projectGitWithin(destination, canonicalPath(home)) {
			return errors.New("choose a folder outside Lola's managed storage")
		}
		trees, err := (projectgit.Client{}).Worktrees(ctx, dir)
		if err != nil {
			return err
		}
		for _, tree := range trees {
			if projectGitWithin(destination, canonicalPath(tree.Path)) {
				return errors.New("choose a folder outside existing checkouts")
			}
		}
		result.Path = destination
		return (projectgit.Client{}).CreateWorktree(ctx, dir, source, sha, destination, newBranch)
	})
	return result, err
}

// BranchAction resolves and authorizes the owner before writing in a linked checkout.
func (s *ProjectGitService) BranchAction(project, worktree string, request projectgit.BranchRequest) (projectgit.BranchResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ctx, cancel := projectgit.Context()
	defer cancel()
	dir, _, err := projectGitTarget(ctx, project, worktree)
	if err != nil {
		return projectgit.BranchResult{}, err
	}
	c := projectgit.Client{}
	if request.Action == "remove-worktree" || request.Action == "remove-worktree-branch" {
		root, _, err := projectGitTarget(ctx, project, "")
		if err != nil {
			return projectgit.BranchResult{}, err
		}
		target, _, err := projectGitTarget(ctx, project, request.Target)
		if err != nil {
			return projectgit.BranchResult{}, err
		}
		if projectGitWithin(root, target) {
			return projectgit.BranchResult{}, errors.New("the project's repository folder cannot be removed")
		}
		blocked, err := projectGitPathBlocker(true)
		if err != nil {
			return projectgit.BranchResult{}, err
		}
		if blocked(target) {
			return projectgit.BranchResult{}, errors.New("managed worktrees must be removed through their Lola session")
		}
		request.Target = target
		return projectgit.BranchResult{Path: root}, c.RemoveWorktree(ctx, root, request)
	}
	branch, err := c.ActionBranch(ctx, dir, request)
	if err != nil {
		return projectgit.BranchResult{}, err
	}
	if branch != "" {
		trees, err := c.Worktrees(ctx, dir)
		if err != nil {
			return projectgit.BranchResult{}, err
		}
		for _, tree := range trees {
			if "refs/heads/"+tree.Branch != branch {
				continue
			}
			dir, _, err = projectGitTarget(ctx, project, tree.Path)
			if err != nil {
				return projectgit.BranchResult{}, err
			}
			break
		}
	}
	result := projectgit.BranchResult{Path: dir}
	if err = c.BranchAction(ctx, dir, request); err != nil {
		return result, fmt.Errorf("repository action failed: %w", err)
	}
	return result, nil
}
func (s *ProjectGitService) Stashes(project, worktree string) ([]projectgit.Stash, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	dir, _, err := projectGitTarget(ctx, project, worktree)
	if err != nil {
		return nil, err
	}
	return (projectgit.Client{}).Stashes(ctx, dir)
}
func (s *ProjectGitService) StashPush(project, worktree, message, expectedHead string) error {
	return s.mutate(project, worktree, func(ctx context.Context, dir string) error {
		return (projectgit.Client{}).StashPush(ctx, dir, message, expectedHead)
	})
}
func (s *ProjectGitService) StashApply(project, worktree, sha, expectedHead string, pop bool) error {
	return s.mutate(project, worktree, func(ctx context.Context, dir string) error {
		return (projectgit.Client{}).StashApply(ctx, dir, sha, expectedHead, pop)
	})
}
func (s *ProjectGitService) StashDrop(project, worktree, sha string) error {
	return s.mutate(project, worktree, func(ctx context.Context, dir string) error { return (projectgit.Client{}).StashDrop(ctx, dir, sha) })
}

// CachedIdentities returns on-disk presentation metadata without network work.
// The frontend keeps these images visible while Identities refreshes them.
func (s *ProjectGitService) CachedIdentities(project string, shas []string) (projectgit.Identities, error) {
	return s.projectIdentities(project, shas, false)
}

// Identities refreshes each requested identity/avatar once per app lifetime.
func (s *ProjectGitService) Identities(project string, shas []string) (projectgit.Identities, error) {
	return s.projectIdentities(project, shas, true)
}
func (s *ProjectGitService) projectIdentities(project string, shas []string, refresh bool) (projectgit.Identities, error) {
	empty := projectgit.Identities{Authors: map[string]projectgit.AuthorIdentity{}, RemoteAvatars: map[string]string{}}
	if !projectgit.ValidIdentitySHAs(shas) {
		return empty, errors.New("choose at most 200 full commit hashes")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	dir, _, err := projectGitTarget(ctx, project, "")
	if err != nil {
		return empty, err
	}
	client := projectgit.Client{}
	remotes := client.Remotes(ctx, dir)
	cfg, _, err := loadConfig()
	if err != nil {
		return empty, err
	}
	p := cfg.ProjectByName(project)
	if p == nil {
		return empty, errors.New("project no longer exists")
	}
	repo := ""
	for _, remote := range remotes {
		if remote.Host != "github.com" || remote.Repo == "" {
			continue
		}
		if remote.Repo == p.Repo {
			repo = remote.Repo
			break
		}
		if repo == "" || remote.Name == "origin" {
			repo = remote.Repo
		}
	}
	home, err := config.Home()
	if err != nil {
		return empty, err
	}
	cacheDir := filepath.Join(home, "cache", "git-identities")
	emails := client.AuthorEmails(ctx, dir, shas)
	if refresh {
		return s.identities.Refresh(ctx, cacheDir, repo, shas, emails, remotes), nil
	}
	return s.identities.Cached(cacheDir, repo, shas, emails, remotes), nil
}
