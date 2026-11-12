package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/sushidev-team/lola/internal/brain"
	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/projectgit"
)

// DraftCommitMessage is an explicit human action. It prepares editable text;
// it never stages files, creates commits or holds the mutation lock during AI work.
func (s *ProjectGitService) DraftCommitMessage(project, worktree, draft string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	dir, _, err := projectGitTarget(ctx, project, worktree)
	if err != nil {
		return "", err
	}
	client := projectgit.Client{}
	input, err := client.PrepareCommitDraft(ctx, dir, draft)
	if err != nil {
		return "", err
	}
	run := s.draftCommit
	if run == nil {
		home, err := config.Home()
		if err != nil {
			return "", err
		}
		helperDir := filepath.Join(home, "helpers")
		if err = os.MkdirAll(helperDir, 0700); err != nil {
			return "", err
		}
		model := brain.Client{Model: "haiku", Dir: helperDir, Timeout: 30 * time.Second}
		run = model.Summarize
	}
	output, err := run(ctx, projectgit.CommitDraftInstruction, input.Text)
	if errors.Is(err, brain.ErrNotFound) {
		return "", errors.New("install and sign in to Claude Code to draft commit messages")
	}
	if err != nil {
		return "", err
	}
	message, err := projectgit.ParseCommitDraft(output)
	if err != nil {
		return "", err
	}
	current, _, err := projectGitTarget(ctx, project, worktree)
	if err != nil {
		return "", err
	}
	fingerprint, err := client.IndexFingerprint(ctx, current)
	if err != nil {
		return "", err
	}
	if current != dir || fingerprint != input.Fingerprint {
		return "", errors.New("staged changes changed while AI was drafting; generate a new message")
	}
	return message, nil
}
