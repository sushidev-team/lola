package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func aiService(t *testing.T) (*ProjectGitService, string) {
	t.Helper()
	root := projectGitRepo(t)
	writeTestConfig(t, minimalConfig+fmt.Sprintf("\n[[project]]\nname = \"demo\"\npath = %q\n", root))
	if err := os.WriteFile(filepath.Join(root, "new.txt"), []byte("new feature"), 0600); err != nil {
		t.Fatal(err)
	}
	return &ProjectGitService{}, root
}
func aiWait(t *testing.T, s *ProjectGitService, id string) AICommitProgress {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		p, err := s.AICommitStatus(id)
		if err != nil {
			t.Fatal(err)
		}
		if p.Done {
			return p
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("AI job did not stop")
	return AICommitProgress{}
}
func TestProjectGitAICommitRequiresFreshStagedConsent(t *testing.T) {
	s, root := aiService(t)
	if _, err := s.Stage("demo", "", []string{"new.txt"}); err != nil {
		t.Fatal(err)
	}
	p, err := s.PrepareAICommit("demo", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.StartAICommit("demo", "", p.Token, false); err == nil || !strings.Contains(err.Error(), "confirm") {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(root, "new.txt"), []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = s.StartAICommit("demo", "", p.Token, true); err == nil || !strings.Contains(err.Error(), "changed") {
		t.Fatal(err)
	}
}
func TestProjectGitAICommitCancellationLocksUntilAcknowledged(t *testing.T) {
	s, _ := aiService(t)
	entered, release := make(chan struct{}), make(chan struct{})
	s.draftCommit = func(ctx context.Context, _, _ string) (string, error) {
		close(entered)
		<-ctx.Done()
		<-release
		return "", ctx.Err()
	}
	p, err := s.PrepareAICommit("demo", "")
	if err != nil {
		t.Fatal(err)
	}
	job, err := s.StartAICommit("demo", "", p.Token, false)
	if err != nil {
		t.Fatal(err)
	}
	<-entered
	if _, err = s.Stage("demo", "", []string{"new.txt"}); err == nil {
		t.Fatal("mutated while planning")
	}
	if err = s.CancelAICommit(job.ID); err != nil {
		t.Fatal(err)
	}
	status, _ := s.AICommitStatus(job.ID)
	if !status.Stopping || status.Done {
		t.Fatal(status)
	}
	if _, err = s.Stage("demo", "", []string{"new.txt"}); err == nil {
		t.Fatal("released lock before worker exited")
	}
	close(release)
	status = aiWait(t, s, job.ID)
	if status.Phase != "cancelled" || status.Completed != 0 {
		t.Fatal(status)
	}
}
func TestProjectGitAICommitCompletesAndReportsInvalidPlans(t *testing.T) {
	for _, valid := range []bool{false, true} {
		t.Run(fmt.Sprint(valid), func(t *testing.T) {
			s, _ := aiService(t)
			s.draftCommit = func(ctx context.Context, instruction, input string) (string, error) {
				if !strings.Contains(input, "new feature") {
					return "", fmt.Errorf("missing file contents")
				}
				if valid {
					return `{"groups":[{"subject":"Add feature","paths":["new.txt"]}]}`, nil
				}
				return `{"groups":[{"subject":"Unsafe","paths":["outside"]}]}`, nil
			}
			p, err := s.PrepareAICommit("demo", "")
			if err != nil {
				t.Fatal(err)
			}
			job, err := s.StartAICommit("demo", "", p.Token, false)
			if err != nil {
				t.Fatal(err)
			}
			status := aiWait(t, s, job.ID)
			if valid && (status.Phase != "complete" || status.Completed != 1) {
				t.Fatal(status)
			}
			if !valid && (status.Phase != "failed" || status.Completed != 0 || status.Error == "") {
				t.Fatal(status)
			}
		})
	}
}
