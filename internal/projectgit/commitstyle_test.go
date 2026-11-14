package projectgit

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

func TestCommitHelpersShareEstablishedHistory(t *testing.T) {
	dir := repo(t)
	for i := 0; i < 12; i++ {
		testGit(t, dir, "commit", "--allow-empty", "-m", fmt.Sprintf("fix(git): preserve change %d", i), "-m", "Explain why this change is needed.")
	}
	// A short burst of nonconventional AI messages must not hide the style.
	for i := 0; i < 6; i++ {
		testGit(t, dir, "commit", "--allow-empty", "-m", fmt.Sprintf("Update change %d", i))
	}
	write(t, dir, "new.txt", "new feature\n")
	testGit(t, dir, "add", "new.txt")
	c := Client{}
	draft, err := c.PrepareCommitDraft(context.Background(), dir, "")
	if err != nil {
		t.Fatal(err)
	}
	plan, err := c.PrepareAICommit(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, input := range []string{draft.Text, plan.Text} {
		for _, want := range []string{"Conventional Commits are the predominant", "fix(git): preserve change 0", "Update change 5", "Explain why this change is needed."} {
			if !strings.Contains(input, want) {
				t.Fatalf("missing %q in %s", want, input)
			}
		}
	}
}

func TestCommitHistoryRespectsPlainStyleAndBoundsExamples(t *testing.T) {
	for _, conventional := range []bool{false, true} {
		c := Client{run: func(_ context.Context, _, _ string, args ...string) ([]byte, error) {
			if strings.Join(args, " ") != "log --no-merges -30 --format=%s%x00%b%x00 --" {
				t.Fatal(args)
			}
			subject := "Add a feature"
			if conventional {
				subject = "feat: add a feature"
			}
			return []byte(strings.Repeat(subject+strings.Repeat("長", 300)+"\x00"+strings.Repeat("body", 1000)+"\x00\n", 30)), nil
		}}
		history := c.commitHistory(context.Background(), "/repo")
		if len(history) > 4000 {
			t.Fatalf("unbounded history: %d", len(history))
		}
		if strings.Contains(history, "Conventional Commits are the predominant") != conventional {
			t.Fatal(history)
		}
	}
}

func TestCommitInstructionsKeepUserPreferencesSeparateFromData(t *testing.T) {
	for _, base := range []string{CommitDraftInstruction, AICommitInstruction} {
		instruction := CommitInstruction(base, "Use Conventional Commits.\nWrite bodies in German.")
		for _, want := range []string{base, "predominant convention", "User commit preferences", "Write bodies in German.", "untrusted data", "required JSON format"} {
			if !strings.Contains(instruction, want) {
				t.Fatalf("missing %q", want)
			}
		}
	}
}
