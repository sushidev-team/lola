package runtime

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/sushidev-team/lola/internal/state"
)

func TestAdoptStackLeftovers(t *testing.T) {
	f := newStackFixture(t)
	for _, tc := range []struct {
		name, entry string
		live, want  bool
	}{
		{"empty", "", false, false},
		{"metadata", ".lola/prompt.md", false, false},
		{"live-metadata", ".lola/prompt.md", true, true},
		{"notes", "notes.txt", false, true},
		{"unknown-app", "former-app/work.txt", false, true},
		{"app", "nori-web/README", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			id := "lola-nori-" + tc.name
			dir := filepath.Join(f.root, f.p.Name, id)
			file := filepath.Join(dir, tc.entry)
			parent := dir
			if tc.entry != "" {
				parent = filepath.Dir(file)
			}
			if err := os.MkdirAll(parent, 0o755); err != nil {
				t.Fatal(err)
			}
			if tc.entry != "" {
				if err := os.WriteFile(file, []byte("keep"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			found := false
			for _, s := range f.n.adoptStack(f.p, map[string]bool{id: tc.live}) {
				if s.ID == id {
					found = true
				}
			}
			if found != tc.want {
				t.Fatalf("adopted = %v, want %v", found, tc.want)
			}
			if _, err := os.Stat(file); err != nil {
				t.Fatalf("adoption changed the leftover: %v", err)
			}
		})
	}
}

func TestAdoptStandaloneSessionsAfterProjectJoinsStack(t *testing.T) {
	f := newStackFixture(t)
	// Git records physical paths (/private/var on macOS), so use the same
	// root for the registered-worktree lookup.
	root, err := filepath.EvalSymlinks(f.root)
	if err != nil {
		t.Fatal(err)
	}
	f.root, f.n.WT.Root = root, root
	liveID, deadID := "lola-nori-web-eng-1", "lola-nori-web-eng-2"
	for _, id := range []string{liveID, deadID} {
		dir := filepath.Join(f.root, "nori-web", id)
		realGit(t, f.web, "worktree", "add", "-b", id, dir, "main")
	}
	bin, _ := scriptBin(t, "tmux", `*"ls -F"*)
  printf 'lola-nori-web-eng-1\t1720000000\t0\n'
  exit 0
  ;;`)
	f.n.Tmux.Bin = bin
	got, err := f.n.Adopt(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("adopted = %+v, want both standalone sessions", got)
	}
	for _, s := range got {
		want := state.AgentDead
		if s.ID == liveID {
			want = state.AgentWorking
		}
		if s.Stack || s.Project != "nori-web" || s.AgentState != want {
			t.Errorf("adopted = %+v, want standalone %s", s, want)
		}
	}
}
