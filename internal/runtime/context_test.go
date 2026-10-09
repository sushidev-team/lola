package runtime

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sushidev-team/lola/internal/session"
)

// fakeCheckpoints records the launcher's checkpoint calls.
type fakeCheckpoints struct {
	applied  []string // dir + " " + tree
	pruned   []string // dir + " " + session
	applyErr error
}

func (f *fakeCheckpoints) Apply(_ context.Context, dir, tree string) error {
	f.applied = append(f.applied, dir+" "+tree)
	return f.applyErr
}

func (f *fakeCheckpoints) Prune(_ context.Context, dir, s string) error {
	f.pruned = append(f.pruned, dir+" "+s)
	return nil
}

func TestContextKey(t *testing.T) {
	for _, tc := range []struct {
		s    session.Session
		want string
	}{
		{session.Session{ID: "lola-p-eng-1-r2", Issue: "ENG-1"}, "eng-1"},
		{session.Session{ID: "lola-p-open-x"}, "lola-p-open-x"},
		{session.Session{ID: "fork", Issue: "ENG-1", ContextKey: "eng-9"}, "eng-9"},
	} {
		if got := ContextKey(tc.s); got != tc.want {
			t.Errorf("ContextKey(%+v) = %q, want %q", tc.s, got, tc.want)
		}
	}
}

// A spawn links the issue's context folder into .lola/context and briefs it;
// a re-spawn of the SAME issue (the "-r2" attempt) gets the same folder and is
// told what the first session left there.
func TestSpawnSharesContextFolderAcrossRespawns(t *testing.T) {
	f := newFixture(t, "", "")
	ck := &fakeCheckpoints{}
	f.n.Checkpoints = ck
	ctx := context.Background()

	first, err := f.n.Spawn(ctx, f.p, issueENG42(), "")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(f.n.Home, "context", "nori", "eng-42")
	link := filepath.Join(first.Worktree, ".lola", "context")
	if got, err := os.Readlink(link); err != nil || got != want {
		t.Fatalf("link = %q, %v; want -> %s", got, err, want)
	}
	prompt := readFile(t, filepath.Join(first.Worktree, ".lola", "prompt.md"))
	if !strings.Contains(prompt, "## Shared context folder") || !strings.Contains(prompt, "It is empty") {
		t.Errorf("first briefing lacks the empty-folder section:\n%s", prompt)
	}
	if len(ck.pruned) != 1 || ck.pruned[0] != first.Worktree+" "+first.ID {
		t.Errorf("fresh spawn must prune stale checkpoint refs for its id, pruned = %v", ck.pruned)
	}

	// The first session writes notes through the link.
	if err := os.WriteFile(filepath.Join(link, "notes.md"), []byte("tried X, dead end\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	second, err := f.n.Spawn(ctx, f.p, issueENG42(), "")
	if err != nil {
		t.Fatal(err)
	}
	if second.ID == first.ID || second.ContextKey != "eng-42" {
		t.Fatalf("re-spawn = %s (key %q); want a new attempt on the same key", second.ID, second.ContextKey)
	}
	notes := readFile(t, filepath.Join(second.Worktree, ".lola", "context", "notes.md"))
	if notes != "tried X, dead end\n" {
		t.Errorf("re-spawned session does not see the notes: %q", notes)
	}
	prompt = readFile(t, filepath.Join(second.Worktree, ".lola", "prompt.md"))
	if !strings.Contains(prompt, "A previous session left these") || !strings.Contains(prompt, ".lola/context/notes.md") {
		t.Errorf("re-spawn briefing does not name the notes:\n%s", prompt)
	}
}

func TestContextEntriesSkipsUnsafeNamesAndCaps(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"a.md", "b`x.md", "sub"} {
		p := filepath.Join(dir, n)
		if n == "sub" {
			if err := os.Mkdir(p, 0o700); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if err := os.WriteFile(p, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	got := strings.Join(contextEntries(dir), ",")
	if got != "a.md,sub/" {
		t.Errorf("entries = %q", got)
	}
	for i := range contextListMax + 3 {
		if err := os.WriteFile(filepath.Join(dir, "n"+strings.Repeat("x", i)), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if e := contextEntries(dir); len(e) != contextListMax+1 || !strings.HasPrefix(e[contextListMax], "… and ") {
		t.Errorf("uncapped listing: %d entries, last %q", len(e), e[len(e)-1])
	}
}

func TestForkAgentLaysCheckpointAndInheritsContext(t *testing.T) {
	f := newFixture(t, "", "")
	ck := &fakeCheckpoints{}
	f.n.Checkpoints = ck
	spec := ForkSpec{
		SessionID: "lola-nori-open-lola-eng-42-fork-3", Branch: "lola/eng-42-fork-3",
		Head: "abc123", Tree: "tree456", ContextKey: "eng-42", Prompt: "# Fork\n",
	}
	got, err := f.n.ForkAgent(context.Background(), f.p, spec)
	if err != nil {
		t.Fatal(err)
	}
	if got.Kind != session.KindManual || !got.OwnsBranch() || got.ContextKey != "eng-42" || got.Branch != spec.Branch {
		t.Errorf("fork session = %+v", got)
	}
	if len(ck.applied) != 1 || ck.applied[0] != got.Worktree+" tree456" {
		t.Errorf("applied = %v", ck.applied)
	}
	if !strings.Contains(loggedArgs(t, f.gitLog), "worktree add -b lola/eng-42-fork-3 "+got.Worktree+" abc123") {
		t.Errorf("fork must branch at the checkpoint's HEAD; git calls:\n%s", loggedArgs(t, f.gitLog))
	}
	if l, err := os.Readlink(filepath.Join(got.Worktree, ".lola", "context")); err != nil || !strings.HasSuffix(l, filepath.Join("context", "nori", "eng-42")) {
		t.Errorf("fork does not share the parent's context folder: %q %v", l, err)
	}
	if p := readFile(t, filepath.Join(got.Worktree, ".lola", "prompt.md")); !strings.HasPrefix(p, "# Fork\n") {
		t.Errorf("prompt.md = %q", p)
	}
}

func TestForkAgentRefusesWithoutCheckpointsAndRollsBackOnApplyFailure(t *testing.T) {
	f := newFixture(t, "", "")
	spec := ForkSpec{SessionID: "lola-nori-open-x-fork-1", Branch: "x-fork-1", Head: "h", Tree: "t"}
	if _, err := f.n.ForkAgent(context.Background(), f.p, spec); err == nil {
		t.Error("ForkAgent without a checkpoint store must refuse")
	}
	f2 := newFixture(t, "", "")
	f2.n.Checkpoints = &fakeCheckpoints{applyErr: errors.New("boom")}
	if _, err := f2.n.ForkAgent(context.Background(), f2.p, spec); err == nil || !strings.Contains(err.Error(), "rolled back") {
		t.Errorf("apply failure: err = %v", err)
	}
	if strings.Contains(loggedArgs(t, f2.tmuxLog), "new-session") {
		t.Error("an agent was launched on a fork whose checkpoint never landed")
	}
}
