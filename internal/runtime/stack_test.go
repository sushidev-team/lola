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

// These run REAL git in temp repos: what is under test is how several
// repositories' worktrees share one session folder and tear down, which a
// scripted git cannot model honestly. tmux is still faked — no agent runs.

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

type stackFixture struct {
	n             *Native
	p             config.Project // the stack
	root          string
	web, api, doc string // member repos: labelled, labelled, always
	tmuxCalled    string
}

// newStackFixture is stack "nori" with three members: nori-web (label
// apps/web), nori-api (label lbl-api) and nori-docs (no labels: always).
func newStackFixture(t *testing.T) *stackFixture {
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
	web, api, doc := initRepo(t), initRepo(t), initRepo(t)
	root := t.TempDir()
	tmuxBin, tmuxLog := scriptBin(t, "tmux", "")
	stack := config.Project{Name: "nori", Kind: config.ProjectKindStack}
	n := &Native{
		Cfg: &config.Config{Projects: []config.Project{
			stack,
			{Name: "nori-web", Stack: "nori", Path: web, Repo: "acme/web", DefaultBranch: "main", Labels: []string{"apps/web"},
				PostCreate: []string{`printf '%s' "$LOLA_SESSION_SLUG" > slug`}},
			{Name: "nori-api", Stack: "nori", Path: api, Repo: "acme/api", DefaultBranch: "main", Labels: []string{"lbl-api"}},
			{Name: "nori-docs", Stack: "nori", Path: doc, Repo: "acme/docs", DefaultBranch: "main"},
		}},
		WT:      &worktree.Manager{Root: root},
		Tmux:    &tmux.Client{Bin: tmuxBin},
		LolaBin: "/usr/local/bin/lola",
		Home:    os.Getenv("LOLA_HOME"),
	}
	return &stackFixture{n: n, p: stack, root: root, web: web, api: api, doc: doc, tmuxCalled: tmuxLog}
}

// An issue labelled for the website only (by group/name).
func webIssue() (is struct{ ids, names, groups []string }) {
	return struct{ ids, names, groups []string }{[]string{"lbl-web", "lbl-bug"}, []string{"Web", "bug"}, []string{"Apps", ""}}
}

func spawnWeb(t *testing.T, f *stackFixture) (string, []string) {
	t.Helper()
	is := issueENG42()
	l := webIssue()
	is.LabelIDs, is.LabelNames, is.LabelGroups = l.ids, l.names, l.groups
	s, err := f.n.Spawn(context.Background(), f.p, is, "")
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	var apps []string
	for _, a := range s.Apps {
		apps = append(apps, a.Name)
	}
	return s.ID, apps
}

func TestStackSpawnChecksOutOnlyTheSelectedApps(t *testing.T) {
	f := newStackFixture(t)
	is := issueENG42()
	l := webIssue()
	is.LabelIDs, is.LabelNames, is.LabelGroups = l.ids, l.names, l.groups
	s, err := f.n.Spawn(context.Background(), f.p, is, "")
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	dir := filepath.Join(f.root, "nori", "lola-nori-eng-42")
	if !s.Stack || s.Worktree != dir || s.Repo != "" || s.Branch != "lola/eng-42" {
		t.Fatalf("session = %+v", s)
	}
	var got []string
	for _, a := range s.Apps {
		got = append(got, a.Name)
		if a.Worktree != filepath.Join(dir, a.Name) || a.Branch != "lola/eng-42" || a.Base != "main" {
			t.Errorf("app record = %+v", a)
		}
		if head := realGit(t, a.Worktree, "rev-parse", "--abbrev-ref", "HEAD"); head != "lola/eng-42" {
			t.Errorf("%s on %q", a.Name, head)
		}
		// Each app's .lola is its own (dev tabs source its env) and invisible
		// to its repository.
		if st := realGit(t, a.Worktree, "status", "--porcelain"); a.Name != "nori-web" && st != "" {
			t.Errorf("%s dirty after spawn:\n%s", a.Name, st)
		}
	}
	if strings.Join(got, ",") != "nori-web,nori-docs" {
		t.Fatalf("apps = %v, want nori-web,nori-docs (api not selected)", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "nori-api")); !os.IsNotExist(err) {
		t.Fatalf("unselected app checked out: %v", err)
	}
	if out := realGit(t, f.api, "branch", "--list", "lola/eng-42"); out != "" {
		t.Errorf("unselected app got the session branch: %q", out)
	}
	// The folder is not a repository; the agent starts there.
	if _, err := os.Stat(filepath.Join(dir, ".git")); !os.IsNotExist(err) {
		t.Error("the session folder must not be a git repository")
	}
	if !strings.Contains(loggedArgs(t, f.tmuxCalled), "-c "+dir) {
		t.Errorf("agent not started in the session folder:\n%s", loggedArgs(t, f.tmuxCalled))
	}
	// post_create ran in the app, with the app's OWN slug.
	if slug, _ := os.ReadFile(filepath.Join(dir, "nori-web", "slug")); string(slug) != "lola-nori-eng-42-nori-web" {
		t.Errorf("post_create slug = %q", slug)
	}
	prompt, _ := os.ReadFile(filepath.Join(dir, ".lola", "prompt.md"))
	for _, must := range []string{"## Repositories", "This directory is NOT a git repository", "`nori-web/` — acme/web, branch `lola/eng-42`, base `main`",
		"`nori-docs/`", "This stack also has `nori-api`, NOT checked out", "Link the pull requests to each other"} {
		if !strings.Contains(string(prompt), must) {
			t.Errorf("prompt.md missing %q:\n%s", must, prompt)
		}
	}
	if err := f.n.Kill(context.Background(), s, true, false); err != nil {
		// nori-web holds the post_create output (untracked "slug") — dirty.
		if !errors.Is(err, worktree.ErrDirty) {
			t.Fatalf("Kill: %v", err)
		}
	}
}

// A stack with require_plan spawns gated and briefed, exactly like a
// single-repo project: the stack path writes its own prompt.md, so it must not
// skip the gate.
func TestStackSpawnRequirePlanGatesAndBriefs(t *testing.T) {
	f := newStackFixture(t)
	f.p.RequirePlan = true
	is := issueENG42()
	l := webIssue()
	is.LabelIDs, is.LabelNames, is.LabelGroups = l.ids, l.names, l.groups
	s, err := f.n.Spawn(context.Background(), f.p, is, "")
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if s.PlanGate != session.PlanPlanning {
		t.Fatalf("PlanGate = %q, want planning", s.PlanGate)
	}
	prompt, _ := os.ReadFile(filepath.Join(s.Worktree, ".lola", "prompt.md"))
	if !strings.Contains(string(prompt), "## Plan approval") || !strings.Contains(string(prompt), "## Repositories") {
		t.Fatalf("prompt.md lacks the plan or stack section:\n%s", prompt)
	}
}

func TestStackSpawnRefusesAnIssueThatSelectsNoApp(t *testing.T) {
	f := newStackFixture(t)
	f.n.Cfg.Projects = f.n.Cfg.Projects[:3] // drop the always-included docs
	if _, err := f.n.Spawn(context.Background(), f.p, issueENG42(), ""); !errors.Is(err, ErrNoStackApp) {
		t.Fatalf("Spawn = %v, want ErrNoStackApp", err)
	}
	if _, err := os.Stat(filepath.Join(f.root, "nori")); !os.IsNotExist(err) {
		t.Errorf("a refused spawn left a folder behind: %v", err)
	}
}

func TestStackKillIsDirtySafePerApp(t *testing.T) {
	f := newStackFixture(t)
	f.n.Cfg.Projects[1].PostCreate = nil
	ctx := context.Background()
	is := issueENG42()
	is.LabelIDs = []string{"lbl-api"}
	s, err := f.n.Spawn(ctx, f.p, is, "")
	if err != nil {
		t.Fatal(err)
	}
	dir := s.Worktree
	api := filepath.Join(dir, "nori-api")

	// A dirty app keeps EVERYTHING: the folder, and the clean app in it.
	if err := os.WriteFile(filepath.Join(api, "wip.txt"), []byte("wip"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := f.n.Kill(ctx, s, true, false); !errors.Is(err, worktree.ErrDirty) {
		t.Fatalf("Kill with a dirty app = %v, want ErrDirty", err)
	}
	if _, err := os.Stat(filepath.Join(api, "wip.txt")); err != nil {
		t.Fatalf("dirty app removed: %v", err)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("session folder removed while an app is dirty: %v", err)
	}
	if err := os.Remove(filepath.Join(api, "wip.txt")); err != nil {
		t.Fatal(err)
	}

	// Committed but in no PR: the branch is the only copy — kept.
	realGit(t, api, "commit", "-q", "--allow-empty", "-m", "work")
	if err := f.n.Kill(ctx, s, true, false); !errors.Is(err, worktree.ErrDirty) || !strings.Contains(err.Error(), "in no pull request") {
		t.Fatalf("Kill with unshipped commits = %v, want ErrDirty naming the commits", err)
	}
	if out := realGit(t, f.api, "branch", "--list", "lola/eng-42"); out == "" {
		t.Fatal("the branch holding unshipped commits was deleted")
	}
	// The same commits on a PR: shipped, so teardown proceeds.
	i := s.AppIndex("nori-api")
	s.Apps[i].PR = &scm.PR{Number: 3, State: "MERGED"}

	// Files the agent left in the folder itself are in no repository: kept.
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := f.n.Kill(ctx, s, true, false); !errors.Is(err, worktree.ErrDirty) || !strings.Contains(err.Error(), "notes.txt") {
		t.Fatalf("Kill with files in the folder = %v, want ErrDirty naming them", err)
	}
	if err := os.Remove(filepath.Join(dir, "notes.txt")); err != nil {
		t.Fatal(err)
	}
	if err := f.n.Kill(ctx, s, true, false); err != nil {
		t.Fatalf("Kill: %v", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("session folder still there: %v", err)
	}
	for _, repo := range []string{f.api, f.doc} {
		if out := realGit(t, repo, "branch", "--list", "lola/eng-42"); out != "" {
			t.Errorf("branch left in %s: %q", repo, out)
		}
	}
}

func TestStackSpawnPicksARetrySlotWhenAMemberBranchIsTaken(t *testing.T) {
	f := newStackFixture(t)
	realGit(t, f.api, "branch", "lola/eng-42") // an earlier attempt's, kept
	id, apps := spawnWeb(t, f)
	if id != "lola-nori-eng-42-r2" || strings.Join(apps, ",") != "nori-web,nori-docs" {
		t.Fatalf("spawned %s with %v", id, apps)
	}
}

func TestStackSpawnRollsBackWhenAnAppFails(t *testing.T) {
	f := newStackFixture(t)
	f.n.Cfg.Projects[1].PostCreate = nil   // a clean web worktree, so its rollback may remove it
	f.n.Cfg.Projects[3].Path = t.TempDir() // docs: not a git repository
	is := issueENG42()
	is.LabelIDs = []string{"lbl-web"}
	is.LabelNames, is.LabelGroups = []string{"web"}, []string{"apps"}
	_, err := f.n.Spawn(context.Background(), f.p, is, "")
	if err == nil || !strings.Contains(err.Error(), "app nori-docs") {
		t.Fatalf("Spawn = %v, want an app failure", err)
	}
	if _, err := os.Stat(filepath.Join(f.root, "nori", "lola-nori-eng-42")); !os.IsNotExist(err) {
		t.Fatalf("session folder not rolled back: %v", err)
	}
	if out := realGit(t, f.web, "branch", "--list", "lola/eng-42"); out != "" {
		t.Errorf("branch left in the web repo after rollback: %q", out)
	}
	if loggedArgs(t, f.tmuxCalled) != "" {
		t.Errorf("tmux touched on a failed spawn: %s", loggedArgs(t, f.tmuxCalled))
	}
}

func TestAdoptRebuildsAStackSessionFromItsFolder(t *testing.T) {
	f := newStackFixture(t)
	id, _ := spawnWeb(t, f)
	got, err := f.n.Adopt(context.Background())
	if err != nil {
		t.Fatalf("Adopt: %v", err)
	}
	var found bool
	for _, s := range got {
		if s.ID != id {
			continue
		}
		found = true
		var apps []string
		for _, a := range s.Apps {
			apps = append(apps, a.Name)
		}
		if !s.Stack || s.Project != "nori" || s.Issue != "ENG-42" || strings.Join(apps, ",") != "nori-web,nori-docs" {
			t.Errorf("adopted = %+v (apps %v)", s, apps)
		}
	}
	if !found {
		t.Fatalf("stack session %s not adopted: %+v", id, got)
	}
}

func TestStackRefusesCheckoutOnlyLaunches(t *testing.T) {
	f := newStackFixture(t)
	if _, err := f.n.OpenManual(context.Background(), f.p, "lola-nori-open-x", "x", "main"); err == nil || !strings.Contains(err.Error(), "is a stack") {
		t.Fatalf("OpenManual on a stack = %v", err)
	}
}
