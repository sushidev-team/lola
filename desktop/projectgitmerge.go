package main

import "github.com/sushidev-team/lola/internal/projectgit"

func (s *ProjectGitService) CanFastForward(project, worktree string, request projectgit.BranchRequest) (bool, error) {
	ctx, cancel := projectgit.Context()
	defer cancel()
	dir, _, err := projectGitTarget(ctx, project, worktree)
	if err != nil {
		return false, err
	}
	c := projectgit.Client{}
	trees, err := c.Worktrees(ctx, dir)
	if err != nil {
		return false, err
	}
	// Read merge settings from the same checkout BranchAction will use.
	for _, tree := range trees {
		if "refs/heads/"+tree.Branch == request.Target {
			dir, _, err = projectGitTarget(ctx, project, tree.Path)
			if err != nil {
				return false, err
			}
			break
		}
	}
	return c.CanFastForward(ctx, dir, request)
}
