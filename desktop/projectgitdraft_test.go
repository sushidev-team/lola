package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestProjectGitAIDraftIsEditableTextAndRejectsChangedIndex(t *testing.T) {
	root := projectGitRepo(t)
	writeTestConfig(t, minimalConfig+fmt.Sprintf("\n[[project]]\nname = \"demo\"\npath = %q\n", root))
	path := filepath.Join(root, "new.txt")
	if err := os.WriteFile(path, []byte("staged feature"), 0600); err != nil {
		t.Fatal(err)
	}
	svc := &ProjectGitService{}
	if _, err := svc.Stage("demo", "", []string{"new.txt"}); err != nil {
		t.Fatal(err)
	}
	calls := 0
	svc.draftCommit = func(ctx context.Context, instruction, input string) (string, error) {
		calls++
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > 30*time.Second {
			t.Fatal("unbounded model call")
		}
		if !strings.Contains(input, "staged feature") || !strings.Contains(instruction, "untrusted data") {
			t.Fatal("missing bounded staged context")
		}
		return `{"subject":"feat: add staged feature","body":""}`, nil
	}
	message, err := svc.DraftCommitMessage("demo", "", "feat:")
	if err != nil || message != "feat: add staged feature" {
		t.Fatalf("%q %v", message, err)
	}
	if _, err = svc.DraftCommitMessage("missing", "", ""); err == nil || calls != 1 {
		t.Fatal("unknown project reached model")
	}
	svc.draftCommit = func(ctx context.Context, _, _ string) (string, error) {
		if err := os.WriteFile(path, []byte("new staged feature"), 0600); err != nil {
			t.Fatal(err)
		}
		// Index actions remain available during inference; stale results are refused.
		if _, err := svc.Stage("demo", "", []string{"new.txt"}); err != nil {
			t.Fatal(err)
		}
		return `{"subject":"feat: stale draft","body":""}`, nil
	}
	if _, err = svc.DraftCommitMessage("demo", "", ""); err == nil || !strings.Contains(err.Error(), "changed") {
		t.Fatalf("stale error=%v", err)
	}
	out, err := exec.Command("git", "-C", root, "log", "--format=%s").Output()
	if err != nil || strings.TrimSpace(string(out)) != "Initial" {
		t.Fatal("AI created a commit")
	}
}
