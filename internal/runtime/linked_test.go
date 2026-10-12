package runtime

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/scm"
	"github.com/sushidev-team/lola/internal/session"
	"github.com/sushidev-team/lola/internal/tmux"
	"github.com/sushidev-team/lola/internal/worktree"
)

// These run REAL git in temp repos: what is under test is how two
// repositories' worktrees nest and tear down, which a scripted git cannot
// model honestly. tmux is still faked — no agent is ever started.

func realGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func initRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	realGit(t, dir, "init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "README"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	realGit(t, dir, "add", "README")
	realGit(t, dir, "commit", "-q", "-m", "init")
	return dir
}

type linkedFixture struct {
	n          *Native
	p          config.Project
	root       string
	web, api   string
	tmuxCalled string
}

func newLinkedFixture(t *testing.T) *linkedFixture {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	t.Setenv("LOLA_HOME", t.TempDir())
	t.Setenv("GIT_AUTHOR_NAME", "t")
	t.Setenv("GIT_AUTHOR_EMAIL", "t@example.com")
	t.Setenv("GIT_COMMITTER_NAME", "t")
	t.Setenv("GIT_COMMITTER_EMAIL", "t@example.com")
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	web, api := initRepo(t), initRepo(t)
	root := t.TempDir()
	tmuxBin, tmuxLog := scriptBin(t, "tmux", "")
	p := config.Project{
		Name: "web", Path: web, Repo: "acme/web", DefaultBranch: "main",
		LinkedRepos: []config.LinkedRepo{{Name: "api", Path: api, Repo: "acme/api"}},
	}
	n := &Native{
		Cfg:     &config.Config{Projects: []config.Project{p}},
		WT:      &worktree.Manager{Root: root},
		Tmux:    &tmux.Client{Bin: tmuxBin},
		LolaBin: "/usr/local/bin/lola",
		Home:    os.Getenv("LOLA_HOME"),
	}
	return &linkedFixture{n: n, p: p, root: root, web: web, api: api, tmuxCalled: tmuxLog}
}

func TestSpawnChecksLinkedReposOutOnTheSessionBranch(t *testing.T) {
	f := newLinkedFixture(t)
	ctx := context.Background()
	s, err := f.n.Spawn(ctx, f.p, issueENG42(), "")
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	dir := filepath.Join(f.root, "web", s.ID)
	wantWT := filepath.Join(dir, ".lola", "repos", "api")
	if len(s.Linked) != 1 {
		t.Fatalf("linked = %+v", s.Linked)
	}
	l := s.Linked[0]
	if l.Name != "api" || l.Repo != "acme/api" || l.Path != f.api || l.Worktree != wantWT || l.Branch != "lola/eng-42" {
		t.Fatalf("linked record = %+v", l)
	}
	if got := realGit(t, wantWT, "rev-parse", "--abbrev-ref", "HEAD"); got != "lola/eng-42" {
		t.Fatalf("linked worktree on %q", got)
	}
	// The nested checkout is invisible to the primary repo (it lives under the
	// excluded .lola/), so the primary is clean and its teardown not blocked.
	if st := realGit(t, dir, "status", "--porcelain"); st != "" {
		t.Fatalf("primary worktree dirty from the linked checkout:\n%s", st)
	}
	prompt, _ := os.ReadFile(filepath.Join(dir, ".lola", "prompt.md"))
	for _, must := range []string{"## Linked repositories", "`.lola/repos/api` — acme/api, branch `lola/eng-42`, base `main`", "Link the pull requests to each other"} {
		if !strings.Contains(string(prompt), must) {
			t.Errorf("prompt.md missing %q:\n%s", must, prompt)
		}
	}

	// A DIRTY linked checkout blocks the whole teardown: the primary is kept
	// too, because removing it would delete the linked one unchecked.
	if err := os.WriteFile(filepath.Join(wantWT, "wip.txt"), []byte("wip"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := f.n.Kill(ctx, s, true, false); !errors.Is(err, worktree.ErrDirty) {
		t.Fatalf("Kill with dirty linked worktree = %v, want ErrDirty", err)
	}
	for _, d := range []string{dir, wantWT} {
		if _, err := os.Stat(d); err != nil {
			t.Fatalf("%s removed despite dirty linked worktree: %v", d, err)
		}
	}

	// Clean again: both go, and the session branch is deleted in BOTH repos.
	if err := os.Remove(filepath.Join(wantWT, "wip.txt")); err != nil {
		t.Fatal(err)
	}
	if err := f.n.Kill(ctx, s, true, false); err != nil {
		t.Fatalf("Kill: %v", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("primary worktree still there: %v", err)
	}
	for _, repo := range []string{f.web, f.api} {
		if out := realGit(t, repo, "branch", "--list", "lola/eng-42"); out != "" {
			t.Errorf("branch left in %s: %q", repo, out)
		}
	}
}

func TestSpawnPicksARetrySlotWhenOnlyTheLinkedBranchIsTaken(t *testing.T) {
	f := newLinkedFixture(t)
	realGit(t, f.api, "branch", "lola/eng-42")
	s, err := f.n.Spawn(context.Background(), f.p, issueENG42(), "")
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if s.ID != "lola-web-eng-42-r2" || s.Branch != "lola/eng-42-r2" || s.Linked[0].Branch != "lola/eng-42-r2" {
		t.Fatalf("spawned %s on %s / linked %s", s.ID, s.Branch, s.Linked[0].Branch)
	}
}

func TestSpawnRollsBackWhenALinkedRepoFails(t *testing.T) {
	f := newLinkedFixture(t)
	broken := t.TempDir() // not a git repository
	f.p.LinkedRepos = append(f.p.LinkedRepos, config.LinkedRepo{Name: "broken", Path: broken})
	f.n.Cfg.Projects[0] = f.p
	_, err := f.n.Spawn(context.Background(), f.p, issueENG42(), "")
	if err == nil || !strings.Contains(err.Error(), "linked repo broken") {
		t.Fatalf("Spawn = %v, want a linked-repo failure", err)
	}
	if _, err := os.Stat(filepath.Join(f.root, "web", "lola-web-eng-42")); !os.IsNotExist(err) {
		t.Fatalf("primary worktree not rolled back: %v", err)
	}
	for _, repo := range []string{f.web, f.api} {
		if out := realGit(t, repo, "branch", "--list", "lola/eng-42"); out != "" {
			t.Errorf("branch left in %s after rollback: %q", repo, out)
		}
	}
	if loggedArgs(t, f.tmuxCalled) != "" {
		t.Errorf("tmux touched on a failed spawn: %s", loggedArgs(t, f.tmuxCalled))
	}
}

func TestLinkedPRsSettled(t *testing.T) {
	s := session.Session{Linked: []session.LinkedRepo{{Name: "api"}}}
	if !s.LinkedPRsSettled() {
		t.Fatal("no PR yet must not hold teardown")
	}
	s.Linked[0].PR = openPR()
	if s.LinkedPRsSettled() {
		t.Fatal("an open linked PR must hold teardown")
	}
	s.Linked[0].PR.State = "MERGED"
	if !s.LinkedPRsSettled() {
		t.Fatal("a merged linked PR must not hold teardown")
	}
}

func openPR() *scm.PR { return &scm.PR{Number: 7, State: "OPEN"} }
