package runtime

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sushidev-team/lola/internal/session"
)

// A best-of attempt gets its own "-b<k>" ID and branch, the model on the launch
// line, the group on the record, and the draft-PR briefing in prompt.md.
func TestSpawnWithBestOfAttempt(t *testing.T) {
	f := newFixture(t, "", "")
	got, err := f.n.SpawnWith(context.Background(), f.p, issueENG42(), SpawnOpts{
		Agent: "claude", Model: "opus", Group: "eng-42-x1", Attempt: 2, Of: 3,
	})
	if err != nil {
		t.Fatalf("SpawnWith: %v", err)
	}
	if got.ID != "lola-nori-eng-42-b2" || got.Branch != "lola/eng-42-b2" {
		t.Errorf("ID/branch = %q/%q, want the -b2 sibling", got.ID, got.Branch)
	}
	if got.BestOf != "eng-42-x1" || got.Attempt != 2 || got.Attempts != 3 || got.Model != "opus" {
		t.Errorf("record = %+v, want group/attempt/model stamped", got)
	}
	if !strings.Contains(loggedArgs(t, f.tmuxLog), "claude --model opus --settings") {
		t.Errorf("launch line must pin the model:\n%s", loggedArgs(t, f.tmuxLog))
	}
	brief, err := os.ReadFile(filepath.Join(f.root, "nori", got.ID, lolaDir, "prompt.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"attempt 2 of 3", "gh pr create --draft", "[best-of 2/3]"} {
		if !strings.Contains(string(brief), want) {
			t.Errorf("prompt.md missing %q:\n%s", want, brief)
		}
	}
}

// A plain spawn stays exactly as before: no suffix, no model flag, no group.
func TestSpawnWithoutBestOfIsUnchanged(t *testing.T) {
	f := newFixture(t, "", "")
	got, err := f.n.SpawnWith(context.Background(), f.p, issueENG42(), SpawnOpts{Model: "--bad"})
	if err != nil {
		t.Fatalf("SpawnWith: %v", err)
	}
	if got.ID != "lola-nori-eng-42" || got.BestOf != "" || got.Attempt != 0 || got.Model != "" {
		t.Errorf("record = %+v, want a plain spawn (invalid model dropped)", got)
	}
	if strings.Contains(loggedArgs(t, f.tmuxLog), "--model") {
		t.Errorf("no model flag expected:\n%s", loggedArgs(t, f.tmuxLog))
	}
}

// Revive relaunches the recorded model.
func TestReviveKeepsModel(t *testing.T) {
	f := newFixture(t, "", "")
	t.Setenv("HOME", t.TempDir())
	id := "lola-nori-eng-42-b1"
	if err := os.MkdirAll(filepath.Join(f.root, "nori", id, lolaDir), 0o700); err != nil {
		t.Fatal(err)
	}
	s := session.Session{ID: id, Source: "native", Project: "nori", Issue: "ENG-42", Agent: "claude", Model: "sonnet"}
	if _, err := f.n.Revive(context.Background(), s); err != nil {
		t.Fatalf("Revive: %v", err)
	}
	if !strings.Contains(loggedArgs(t, f.tmuxLog), "claude --model sonnet --settings") {
		t.Errorf("revive must relaunch the recorded model:\n%s", loggedArgs(t, f.tmuxLog))
	}
}
