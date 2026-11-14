package main

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"time"

	"github.com/sushidev-team/lola/internal/projectgit"
)

type AICommitProgress struct {
	ID        string `json:"id"`
	Phase     string `json:"phase"`
	Completed int    `json:"completed"`
	Total     int    `json:"total"`
	Subject   string `json:"subject"`
	Stopping  bool   `json:"stopping"`
	Done      bool   `json:"done"`
	Error     string `json:"error"`
}
type aiCommitJob struct {
	progress AICommitProgress
	cancel   context.CancelFunc
}

func (s *ProjectGitService) PrepareAICommit(project, worktree string) (projectgit.AICommitPreparation, error) {
	ctx, cancel := projectgit.Context()
	defer cancel()
	dir, _, err := projectGitTarget(ctx, project, worktree)
	if err != nil {
		return projectgit.AICommitPreparation{}, err
	}
	in, err := (projectgit.Client{}).PrepareAICommit(ctx, dir)
	return in.Preparation, err
}

// StartAICommit reserves the mutation lock BEFORE returning a job ID. Other
// mutations fail immediately instead of queuing surprising actions after it.
func (s *ProjectGitService) StartAICommit(project, worktree, token string, includeStaged bool) (AICommitProgress, error) {
	if !s.mu.TryLock() {
		return AICommitProgress{}, errors.New("another repository action is running; wait for it to finish")
	}
	started := false
	defer func() {
		if !started {
			s.mu.Unlock()
		}
	}()
	ctx, cancel := projectgit.Context()
	defer cancel()
	dir, _, err := projectGitTarget(ctx, project, worktree)
	if err != nil {
		return AICommitProgress{}, err
	}
	client := projectgit.Client{}
	in, err := client.PrepareAICommit(ctx, dir)
	if err != nil {
		return AICommitProgress{}, err
	}
	if token == "" || in.Preparation.Token != token {
		return AICommitProgress{}, errors.New("changes changed; review and start AI commit again")
	}
	if in.Preparation.Staged > 0 && !includeStaged {
		return AICommitProgress{}, errors.New("confirm including staged files before AI commit")
	}
	run, cancelRun := context.WithCancel(context.Background())
	job := &aiCommitJob{progress: AICommitProgress{ID: rand.Text(), Phase: "planning"}, cancel: cancelRun}
	s.aiMu.Lock()
	s.aiJob = job
	initial := job.progress
	s.aiMu.Unlock()
	started = true
	go func() {
		defer s.mu.Unlock()
		defer cancelRun()
		var runErr error
		defer func() {
			if recovered := recover(); recovered != nil {
				runErr = fmt.Errorf("AI commit stopped unexpectedly; inspect the remaining changes")
			}
			s.aiMu.Lock()
			defer s.aiMu.Unlock()
			job.progress.Done = true
			switch {
			case errors.Is(runErr, context.Canceled):
				job.progress.Phase = "cancelled"
			case runErr != nil:
				job.progress.Phase = "failed"
				job.progress.Error = runErr.Error()
			default:
				job.progress.Phase = "complete"
			}
		}()
		planCtx, planCancel := context.WithTimeout(run, 120*time.Second)
		output, err := s.runGitHelper(planCtx, project, projectgit.AICommitInstruction, in.Text, true)
		planCancel()
		if run.Err() != nil {
			runErr = run.Err()
			return
		}
		if err != nil {
			runErr = err
			return
		}
		groups, err := projectgit.ParseAICommit(output, in)
		if err != nil {
			runErr = err
			return
		}
		// Re-authorize the directory after the helper returns, before any Git write.
		check, checkCancel := projectgit.Context()
		current, _, err := projectGitTarget(check, project, worktree)
		checkCancel()
		if err != nil {
			runErr = err
			return
		}
		if current != dir {
			runErr = errors.New("checkout changed during AI planning")
			return
		}
		s.aiMu.Lock()
		job.progress.Phase = "committing"
		job.progress.Total = len(groups)
		s.aiMu.Unlock()
		runErr = client.CommitAIGroups(run, dir, in, groups, func(completed int, subject string) {
			s.aiMu.Lock()
			defer s.aiMu.Unlock()
			job.progress.Completed = completed
			job.progress.Subject = subject
		})
	}()
	return initial, nil
}
func (s *ProjectGitService) AICommitStatus(id string) (AICommitProgress, error) {
	s.aiMu.Lock()
	defer s.aiMu.Unlock()
	if s.aiJob == nil || s.aiJob.progress.ID != id {
		return AICommitProgress{}, errors.New("AI commit run is no longer available")
	}
	return s.aiJob.progress, nil
}
func (s *ProjectGitService) CancelAICommit(id string) error {
	s.aiMu.Lock()
	defer s.aiMu.Unlock()
	if s.aiJob == nil || s.aiJob.progress.ID != id {
		return errors.New("AI commit run is no longer available")
	}
	if !s.aiJob.progress.Done {
		s.aiJob.progress.Stopping = true
		s.aiJob.cancel()
	}
	return nil
}
