package goalwork

import (
	"context"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestStackIntegrationJournalRecoversPartialUpdates(t *testing.T) {
	m, api := fixture(t)
	ui, err := m.Initialize(context.Background(), api.Goal, "ui", api.Source, "main")
	if err != nil {
		t.Fatal(err)
	}
	a := worker(t, m, api, "t", "a", api.Start)
	u := worker(t, m, ui, "t", "a", ui.Start)
	ac := stage(t, m, a, "op1", api.Start, submit(t, m, a, "app.txt", "API\n"))
	uc := stage(t, m, u, "op1", ui.Start, submit(t, m, u, "app.txt", "UI\n"))
	v := proof(ac)
	v.Manifest["ui"] = uc.Commit
	base := map[string]string{"api": api.Start, "ui": ui.Start}
	// A stack acceptance command sees the proposed combination before either
	// authoritative branch advances.
	w, err := m.PrepareValidation(context.Background(), api.Goal, "check1", map[string]Revision{"api": {Commit: ac.Commit, Operation: "op1"}, "ui": {Commit: uc.Commit, Operation: "op1"}})
	if err != nil {
		t.Fatal(err)
	}
	for member, want := range map[string]string{"api": "API\n", "ui": "UI\n"} {
		b, err := os.ReadFile(filepath.Join(w.Directory, member, "app.txt"))
		if err != nil || string(b) != want {
			t.Fatalf("incorrect validation combination %s: %s %v", member, b, err)
		}
	}
	plain := &Manager{Root: m.Root}
	m.Run = func(ctx context.Context, dir string, args []string) (string, error) {
		if dir == ui.GitDir && slices.Contains(args, "update-ref") {
			return "", errors.New("simulated interruption before second ref update")
		}
		return plain.git(ctx, dir, args...)
	}
	partial, err := m.AdvanceManifest(context.Background(), api.Goal, "op1", base, []Candidate{ac, uc}, v, "policy1", []string{"tests"}, []string{"security"})
	if err == nil || partial.Complete || len(partial.Integrated) != 1 {
		t.Fatalf("partial update appeared complete: %+v %v", partial, err)
	}
	if tip, _ := m.Tip(context.Background(), api.Goal, "api"); tip != ac.Commit {
		t.Fatal("first ref was not integrated")
	}
	if tip, _ := m.Tip(context.Background(), api.Goal, "ui"); tip != ui.Start {
		t.Fatal("second ref unexpectedly advanced")
	}
	// Reopening the manager uses the durable intent and exactly the same proof.
	complete, err := plain.AdvanceManifest(context.Background(), api.Goal, "op1", base, []Candidate{ac, uc}, v, "policy1", []string{"tests"}, []string{"security"})
	if err != nil || !complete.Complete || len(complete.Integrated) != 2 || !maps.Equal(complete.Manifest, v.Manifest) {
		t.Fatalf("resume: %+v %v", complete, err)
	}
	if again, err := plain.AdvanceManifest(context.Background(), api.Goal, "op1", base, []Candidate{uc, ac}, v, "policy1", []string{"tests"}, []string{"security"}); err != nil || !again.Complete {
		t.Fatalf("idempotent complete: %+v %v", again, err)
	}
	if got := gitOK(t, plain, api.Source, "rev-parse", "main"); got != api.Start {
		t.Fatal("stack integration moved original main")
	}
}

func TestStackVerificationIncludesUnchangedMembers(t *testing.T) {
	m, r := fixture(t)
	ui, err := m.Initialize(context.Background(), r.Goal, "ui", r.Source, "main")
	if err != nil {
		t.Fatal(err)
	}
	w := worker(t, m, r, "t", "a", r.Start)
	c := stage(t, m, w, "op1", r.Start, submit(t, m, w, "app.txt", "API\n"))
	v := proof(c)
	base := map[string]string{"api": r.Start, "ui": ui.Start}
	if _, err := m.AdvanceManifest(context.Background(), r.Goal, "op1", base, []Candidate{c}, v, "policy1", []string{"tests"}, []string{"security"}); err == nil {
		t.Fatal("verification omitted unchanged member")
	}
	v.Manifest["ui"] = ui.Start
	result, err := m.AdvanceManifest(context.Background(), r.Goal, "op1", base, []Candidate{c}, v, "policy1", []string{"tests"}, []string{"security"})
	if err != nil || !result.Complete || len(result.Integrated) != 1 {
		t.Fatalf("unchanged member: %+v %v", result, err)
	}
	changed := v
	changed.Manifest = maps.Clone(v.Manifest)
	changed.Manifest["ui"] = strings.Repeat("a", 40)
	if _, err := m.AdvanceManifest(context.Background(), r.Goal, "op1", base, []Candidate{c}, changed, "policy1", []string{"tests"}, []string{"security"}); err == nil {
		t.Fatal("completed manifest accepted changed proof")
	}
}
