package runtime

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/hook"
	"github.com/sushidev-team/lola/internal/linear"
	"github.com/sushidev-team/lola/internal/session"
	"github.com/sushidev-team/lola/internal/tmux"
	"github.com/sushidev-team/lola/internal/worktree"
)

// scriptBin installs a shell script standing in for a binary (git, tmux): it
// appends its argv (one line per invocation) to the returned log, then runs
// the caller-supplied `case "$*" in` bodies; unmatched invocations succeed
// silently. Pattern mirrors internal/ao/client_test.go fakeBin and the
// worktree/tmux test fakes; no real git/tmux/claude is ever run.
func scriptBin(t *testing.T, name, cases string) (bin, argsLog string) {
	t.Helper()
	dir := t.TempDir()
	bin = filepath.Join(dir, name)
	argsLog = filepath.Join(dir, "args.log")
	script := "#!/bin/sh\n" +
		"echo \"$@\" >> \"" + argsLog + "\"\n" +
		"case \"$*\" in\n" + cases + "\nesac\nexit 0\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin, argsLog
}

// loggedArgs returns the argv log, one invocation per line, or "" when the
// fake binary was never invoked.
func loggedArgs(t *testing.T, argsLog string) string {
	t.Helper()
	b, err := os.ReadFile(argsLog)
	if errors.Is(err, os.ErrNotExist) {
		return ""
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimRight(string(b), "\n")
}

// fixture is one fully wired Native against fake git/tmux binaries. The fake
// git's `worktree add` actually creates the worktree directory containing a
// `.git` gitdir-pointer file (real `git worktree add` behavior), pointing at
// gitDir, which carries a commondir file back to commonDir — so the
// info/exclude resolution walks the same chain as against real git.
type fixture struct {
	n         *Native
	p         config.Project
	root      string // worktree root
	repo      string // project main checkout stand-in
	commonDir string // shared .git dir; info/exclude lands here
	gitLog    string
	tmuxLog   string
}

// newFixture builds the fixture. gitCases/tmuxCases are extra `case` bodies
// prepended before the defaults (first match wins in sh case... esac since
// each body exits).
func newFixture(t *testing.T, gitCases, tmuxCases string) *fixture {
	t.Helper()
	t.Setenv("LOLA_HOME", t.TempDir())
	root, repo := t.TempDir(), t.TempDir()

	meta := t.TempDir()
	commonDir := filepath.Join(meta, "gitmeta")
	gitDir := filepath.Join(commonDir, "worktrees", "wt")
	if err := os.MkdirAll(gitDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gitDir, "commondir"), []byte("../..\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	addCase := `*"worktree add"*)
  mkdir -p "$7"
  printf 'gitdir: %s\n' "` + gitDir + `" > "$7/.git"
  exit 0
  ;;
*"rev-parse --verify --quiet refs/heads/"*)
  exit 1
  ;;`
	gitBin, gitLog := scriptBin(t, "git", gitCases+"\n"+addCase)
	tmuxBin, tmuxLog := scriptBin(t, "tmux", tmuxCases)

	p := config.Project{Name: "nori", Path: repo, Repo: "owner/nori", DefaultBranch: "main"}
	n := &Native{
		Cfg:       &config.Config{Projects: []config.Project{p}},
		WT:        &worktree.Manager{GitBin: gitBin, Root: root},
		Tmux:      &tmux.Client{Bin: tmuxBin},
		LolaBin:   "/usr/local/bin/lola",
		Home:      os.Getenv("LOLA_HOME"),
		ClaudeBin: "/usr/local/bin/claude",
	}
	return &fixture{n: n, p: p, root: root, repo: repo, commonDir: commonDir, gitLog: gitLog, tmuxLog: tmuxLog}
}

func issueENG42() linear.Issue {
	return linear.Issue{ID: "uuid-42", Identifier: "ENG-42", Title: "Fix login flow"}
}

func TestSpawnHappyPathFullSequence(t *testing.T) {
	f := newFixture(t, "", "")
	ctx := context.Background()

	got, err := f.n.Spawn(ctx, f.p, issueENG42())
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	id := "lola-nori-eng-42"
	dir := filepath.Join(f.root, "nori", id)

	// Returned session for the store.
	want := session.Session{
		ID: id, Source: "native", Project: "nori", Issue: "ENG-42",
		IssueUUID: "uuid-42", Branch: "lola/eng-42", Repo: "owner/nori",
		TmuxName: id, Status: StatusWorking,
	}
	if got != want {
		t.Errorf("session = %+v\nwant      %+v", got, want)
	}

	// Full git sequence: branch-collision probe (attempt 1 is free), then
	// create off origin/main on the derived branch.
	wantGit := strings.Join([]string{
		"-C " + f.repo + " rev-parse --verify --quiet refs/heads/lola/eng-42",
		"-C " + f.repo + " rev-parse --verify --quiet refs/remotes/origin/main",
		"-C " + f.repo + " worktree add -b lola/eng-42 " + dir + " origin/main",
	}, "\n")
	if gotGit := loggedArgs(t, f.gitLog); gotGit != wantGit {
		t.Errorf("git calls:\n%s\nwant:\n%s", gotGit, wantGit)
	}

	// prompt.md: full briefing incl. identifier, title, Linear-fetch note,
	// branch, PR target, never-merge rule.
	prompt, err := os.ReadFile(filepath.Join(dir, ".lola", "prompt.md"))
	if err != nil {
		t.Fatalf("prompt.md: %v", err)
	}
	for _, must := range []string{
		"ENG-42", "Fix login flow",
		"description and all comments — from Linear",
		"`lola/eng-42`",
		"pull request against `main`",
		"Never merge",
	} {
		if !strings.Contains(string(prompt), must) {
			t.Errorf("prompt.md missing %q:\n%s", must, prompt)
		}
	}

	// settings.json: exactly the hook wiring for LolaBin.
	settings, err := os.ReadFile(filepath.Join(dir, ".lola", "settings.json"))
	if err != nil {
		t.Fatalf("settings.json: %v", err)
	}
	if wantS := string(hook.SettingsJSON("/usr/local/bin/lola")); string(settings) != wantS {
		t.Errorf("settings.json = %s\nwant %s", settings, wantS)
	}

	// .lola/ is excluded via the COMMON git dir's info/exclude (resolved
	// through the gitdir pointer + commondir), never the repo's .gitignore.
	exclude, err := os.ReadFile(filepath.Join(f.commonDir, "info", "exclude"))
	if err != nil {
		t.Fatalf("info/exclude: %v", err)
	}
	if string(exclude) != ".lola/\n" {
		t.Errorf("info/exclude = %q, want %q", exclude, ".lola/\n")
	}

	// tmux: one new-session, detached, named id, cwd = worktree, running a
	// single shell command that exports LOLA_SESSION and starts claude with
	// the generated settings and the short read-the-prompt argv.
	wantTmux := "new-session -d -s " + id + " -c " + dir +
		" env LOLA_SESSION=" + id + " /usr/local/bin/claude --settings .lola/settings.json" +
		" 'You are lola session " + id + ". Read .lola/prompt.md in the current directory first; it contains your task briefing.'"
	if gotTmux := loggedArgs(t, f.tmuxLog); gotTmux != wantTmux {
		t.Errorf("tmux calls:\n%s\nwant:\n%s", gotTmux, wantTmux)
	}
}

func TestSpawnUsesLinearBranchName(t *testing.T) {
	f := newFixture(t, "", "")
	issue := issueENG42()
	issue.BranchName = "feat/eng-42-login"

	got, err := f.n.Spawn(context.Background(), f.p, issue)
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if got.Branch != "feat/eng-42-login" {
		t.Errorf("Branch = %q, want Linear's branch name", got.Branch)
	}
	if !strings.Contains(loggedArgs(t, f.gitLog), "worktree add -b feat/eng-42-login ") {
		t.Errorf("git calls:\n%s\nwant worktree add on Linear's branch", loggedArgs(t, f.gitLog))
	}
}

// A dead session's worktree is deliberately kept for inspection while
// reconcile re-queues its issue; the respawn must not collide with it forever
// (PLAN P2.9: branch `lola/<issue>-<n>`) — both session ID and branch get a
// retry suffix and the kept worktree stays untouched.
func TestSpawnSuffixesIDAndBranchWhenPreviousWorktreeKept(t *testing.T) {
	f := newFixture(t, "", "")
	kept := filepath.Join(f.root, "nori", "lola-nori-eng-42")
	if err := os.MkdirAll(kept, 0o755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(kept, "wip.go")
	if err := os.WriteFile(marker, []byte("package wip"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := f.n.Spawn(context.Background(), f.p, issueENG42())
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if got.ID != "lola-nori-eng-42-r2" || got.Branch != "lola/eng-42-r2" || got.TmuxName != "lola-nori-eng-42-r2" {
		t.Errorf("session = %+v, want retry-suffixed ID/branch/tmux (-r2)", got)
	}
	dir := filepath.Join(f.root, "nori", "lola-nori-eng-42-r2")
	if !strings.Contains(loggedArgs(t, f.gitLog), "worktree add -b lola/eng-42-r2 "+dir) {
		t.Errorf("git calls:\n%s\nwant worktree add on the suffixed branch/dir", loggedArgs(t, f.gitLog))
	}
	if !strings.Contains(loggedArgs(t, f.tmuxLog), "new-session -d -s lola-nori-eng-42-r2") {
		t.Errorf("tmux calls:\n%s\nwant new-session under the suffixed name", loggedArgs(t, f.tmuxLog))
	}
	// The kept worktree was never touched.
	if _, statErr := os.Stat(marker); statErr != nil {
		t.Errorf("kept worktree must stay untouched: %v", statErr)
	}
}

// A branch surviving a manual `git worktree remove` must not wedge respawns
// either: the collision probe skips to the next suffix.
func TestSpawnSuffixesWhenBranchSurvives(t *testing.T) {
	f := newFixture(t, `*"rev-parse --verify --quiet refs/heads/lola/eng-42")
  exit 0
  ;;`, "")

	got, err := f.n.Spawn(context.Background(), f.p, issueENG42())
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if got.ID != "lola-nori-eng-42-r2" || got.Branch != "lola/eng-42-r2" {
		t.Errorf("session = %+v, want -r2 suffix (base branch survived)", got)
	}
}

func TestSpawnGivesUpAfterMaxAttempts(t *testing.T) {
	// Every branch probe reports the branch as existing: no slot is ever free.
	f := newFixture(t, `*"rev-parse --verify --quiet refs/heads/"*)
  exit 0
  ;;`, "")

	_, err := f.n.Spawn(context.Background(), f.p, issueENG42())
	if err == nil {
		t.Fatal("Spawn: want error when no session slot is free, got nil")
	}
	if !strings.Contains(err.Error(), "no free session slot") {
		t.Errorf("error %q must name the exhausted slot search", err)
	}
	if strings.Contains(loggedArgs(t, f.gitLog), "worktree add") {
		t.Errorf("no worktree may be created when no slot is free; git calls:\n%s", loggedArgs(t, f.gitLog))
	}
}

func TestSpawnPrepareFailureRollsBackCleanWorktree(t *testing.T) {
	f := newFixture(t, "", "")
	f.p.PostCreate = []string{"echo boom >&2; exit 3"}

	_, err := f.n.Spawn(context.Background(), f.p, issueENG42())
	if err == nil {
		t.Fatal("Spawn: want error from failing post_create, got nil")
	}
	if !strings.Contains(err.Error(), "boom") || !strings.Contains(err.Error(), "worktree rolled back") {
		t.Errorf("error %q must carry the cause and state the rollback outcome", err)
	}
	// No tmux session was ever created or touched.
	if gotTmux := loggedArgs(t, f.tmuxLog); gotTmux != "" {
		t.Errorf("tmux must never run on pre-launch failure; got:\n%s", gotTmux)
	}
	// Rollback removed worktree AND the freshly created branch (force=false:
	// the dirty check ran first).
	dir := filepath.Join(f.root, "nori", "lola-nori-eng-42")
	gitCalls := loggedArgs(t, f.gitLog)
	for _, must := range []string{
		"-C " + dir + " status --porcelain",
		"-C " + f.repo + " worktree remove " + dir,
		"-C " + f.repo + " branch -D lola/eng-42",
	} {
		if !strings.Contains(gitCalls, must) {
			t.Errorf("git calls missing %q:\n%s", must, gitCalls)
		}
	}
}

func TestSpawnRollbackKeepsDirtyWorktree(t *testing.T) {
	f := newFixture(t, `*"status --porcelain"*)
  echo " M main.go"
  exit 0
  ;;`, "")
	f.p.PostCreate = []string{"exit 3"}

	_, err := f.n.Spawn(context.Background(), f.p, issueENG42())
	if err == nil {
		t.Fatal("Spawn: want error, got nil")
	}
	dir := filepath.Join(f.root, "nori", "lola-nori-eng-42")
	if !strings.Contains(err.Error(), "worktree kept at "+dir) {
		t.Errorf("error %q must say the dirty worktree was kept and where", err)
	}
	if !strings.Contains(err.Error(), worktree.ErrDirty.Error()) {
		t.Errorf("error %q must carry the dirty reason", err)
	}
	if strings.Contains(loggedArgs(t, f.gitLog), "worktree remove") {
		t.Errorf("a dirty worktree must never be removed; git calls:\n%s", loggedArgs(t, f.gitLog))
	}
	if _, statErr := os.Stat(dir); statErr != nil {
		t.Errorf("dirty worktree dir must stay on disk: %v", statErr)
	}
}

func TestSpawnTmuxFailureRollsBackWithoutSession(t *testing.T) {
	// new-session fails and the session never came up (has-session says no):
	// rollback must not attempt kill-session and must remove the worktree.
	f := newFixture(t, "", `*"new-session"*) exit 1 ;;
*"has-session"*) exit 1 ;;`)

	_, err := f.n.Spawn(context.Background(), f.p, issueENG42())
	if err == nil {
		t.Fatal("Spawn: want error from tmux failure, got nil")
	}
	if !strings.Contains(err.Error(), "start tmux session") || !strings.Contains(err.Error(), "worktree rolled back") {
		t.Errorf("error %q must name the failing step and the rollback outcome", err)
	}
	tmuxCalls := loggedArgs(t, f.tmuxLog)
	if strings.Contains(tmuxCalls, "kill-session") {
		t.Errorf("must not kill a session that never came up; tmux calls:\n%s", tmuxCalls)
	}
	if !strings.Contains(loggedArgs(t, f.gitLog), "worktree remove") {
		t.Errorf("worktree must be rolled back; git calls:\n%s", loggedArgs(t, f.gitLog))
	}
}

func TestSpawnTmuxFailureKillsHalfCreatedSession(t *testing.T) {
	// new-session errors but the session exists (has-session succeeds):
	// rollback kills it, leaving no tmux session behind.
	f := newFixture(t, "", `*"new-session"*) exit 1 ;;`)

	_, err := f.n.Spawn(context.Background(), f.p, issueENG42())
	if err == nil {
		t.Fatal("Spawn: want error, got nil")
	}
	tmuxCalls := loggedArgs(t, f.tmuxLog)
	if !strings.Contains(tmuxCalls, "kill-session -t =lola-nori-eng-42") {
		t.Errorf("half-created tmux session must be killed; tmux calls:\n%s", tmuxCalls)
	}
}

func TestSpawnRejectsIssueWithoutIdentifier(t *testing.T) {
	f := newFixture(t, "", "")
	if _, err := f.n.Spawn(context.Background(), f.p, linear.Issue{ID: "uuid"}); err == nil {
		t.Fatal("Spawn without identifier: want error, got nil")
	}
	if loggedArgs(t, f.gitLog) != "" || loggedArgs(t, f.tmuxLog) != "" {
		t.Error("nothing may be executed for an invalid issue")
	}
}

func TestAdoptPairingMatrix(t *testing.T) {
	f := newFixture(t, "", "")
	// On disk + registered: eng-1 (tmux alive -> working), eng-2 (no tmux ->
	// dead). Tmux only: eng-9 (-> orphaned, reported but never killed) and a
	// non-lola session (ignored).
	base := filepath.Join(f.root, "nori")
	dir1 := filepath.Join(base, "lola-nori-eng-1")
	dir2 := filepath.Join(base, "lola-nori-eng-2")
	for _, d := range []string{dir1, dir2} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	porcelain := "worktree " + f.repo + "\nHEAD 1111\nbranch refs/heads/main\n\n" +
		"worktree " + dir1 + "\nHEAD 2222\nbranch refs/heads/lola/eng-1\n\n" +
		"worktree " + dir2 + "\nHEAD 3333\nbranch refs/heads/lola/eng-2"
	gitBin, _ := scriptBin(t, "git", `*"worktree list --porcelain"*)
  cat <<'LOLA_EOF'
`+porcelain+`
LOLA_EOF
  exit 0
  ;;`)
	tmuxBin, tmuxLog := scriptBin(t, "tmux", `"ls -F"*)
  cat <<'LOLA_EOF'
lola-nori-eng-1	1720000000	0
lola-nori-eng-9	1720000001	0
main	1720000002	1
LOLA_EOF
  exit 0
  ;;`)
	f.n.WT.GitBin = gitBin
	f.n.Tmux.Bin = tmuxBin

	got, err := f.n.Adopt(context.Background())
	if err != nil {
		t.Fatalf("Adopt: %v", err)
	}
	want := []session.Session{
		{ID: "lola-nori-eng-1", Source: "native", Project: "nori", Issue: "ENG-1", Repo: "owner/nori", TmuxName: "lola-nori-eng-1", Status: StatusWorking},
		{ID: "lola-nori-eng-2", Source: "native", Project: "nori", Issue: "ENG-2", Repo: "owner/nori", TmuxName: "lola-nori-eng-2", Status: StatusDead},
		{ID: "lola-nori-eng-9", Source: "native", Project: "nori", Issue: "ENG-9", TmuxName: "lola-nori-eng-9", Status: StatusOrphaned},
	}
	if len(got) != len(want) {
		t.Fatalf("Adopt = %+v\nwant %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("adopt[%d] = %+v\nwant       %+v", i, got[i], want[i])
		}
	}
	// Pure observation: the orphaned session must not be killed.
	if strings.Contains(loggedArgs(t, tmuxLog), "kill-session") {
		t.Errorf("Adopt must never kill; tmux calls:\n%s", loggedArgs(t, tmuxLog))
	}
}

func TestAdoptNoServerNoWorktreesIsEmpty(t *testing.T) {
	f := newFixture(t, "", `"ls -F"*)
  echo "no server running on /tmp/tmux-501/default" >&2
  exit 1
  ;;`)
	got, err := f.n.Adopt(context.Background())
	if err != nil {
		t.Fatalf("Adopt: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("Adopt = %+v, want empty", got)
	}
}

func killFixtureSession() session.Session {
	return session.Session{
		ID: "lola-nori-eng-42", Source: "native", Project: "nori",
		Issue: "ENG-42", Branch: "lola/eng-42", TmuxName: "lola-nori-eng-42",
	}
}

func TestKillRemovesCleanWorktreeAndBranch(t *testing.T) {
	f := newFixture(t, "", "")
	dir := filepath.Join(f.root, "nori", "lola-nori-eng-42")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := f.n.Kill(context.Background(), killFixtureSession(), true); err != nil {
		t.Fatalf("Kill: %v", err)
	}
	wantTmux := "has-session -t =lola-nori-eng-42\nkill-session -t =lola-nori-eng-42"
	if got := loggedArgs(t, f.tmuxLog); got != wantTmux {
		t.Errorf("tmux calls:\n%s\nwant:\n%s", got, wantTmux)
	}
	wantGit := strings.Join([]string{
		"-C " + dir + " status --porcelain",
		"-C " + f.repo + " worktree remove " + dir,
		"-C " + f.repo + " branch -D lola/eng-42",
	}, "\n")
	if got := loggedArgs(t, f.gitLog); got != wantGit {
		t.Errorf("git calls:\n%s\nwant force=false removal:\n%s", got, wantGit)
	}
}

func TestKillDirtyWorktreePropagatesErrDirtyAndKeepsDir(t *testing.T) {
	f := newFixture(t, `*"status --porcelain"*)
  echo " M main.go"
  exit 0
  ;;`, "")
	dir := filepath.Join(f.root, "nori", "lola-nori-eng-42")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	err := f.n.Kill(context.Background(), killFixtureSession(), true)
	if !errors.Is(err, worktree.ErrDirty) {
		t.Fatalf("Kill dirty: want ErrDirty, got %v", err)
	}
	if strings.Contains(loggedArgs(t, f.gitLog), "worktree remove") {
		t.Errorf("dirty worktree must never be removed; git calls:\n%s", loggedArgs(t, f.gitLog))
	}
	if _, statErr := os.Stat(dir); statErr != nil {
		t.Errorf("dirty worktree dir must stay on disk: %v", statErr)
	}
	// The tmux session is still killed: only worktree removal refuses.
	if !strings.Contains(loggedArgs(t, f.tmuxLog), "kill-session") {
		t.Errorf("tmux session should be killed even when the worktree is dirty")
	}
}

func TestKillAbsentTmuxSessionIsNotAnError(t *testing.T) {
	f := newFixture(t, "", `*"has-session"*) exit 1 ;;`)

	if err := f.n.Kill(context.Background(), killFixtureSession(), false); err != nil {
		t.Fatalf("Kill absent session: %v", err)
	}
	if strings.Contains(loggedArgs(t, f.tmuxLog), "kill-session") {
		t.Error("kill-session must not run for an absent session")
	}
	if got := loggedArgs(t, f.gitLog); got != "" {
		t.Errorf("removeWorktree=false must never touch git; got:\n%s", got)
	}
}

func TestKillMissingWorktreeDirIsNoop(t *testing.T) {
	f := newFixture(t, "", `*"has-session"*) exit 1 ;;`)
	// removeWorktree=true, but the dir never existed: nothing to remove.
	if err := f.n.Kill(context.Background(), killFixtureSession(), true); err != nil {
		t.Fatalf("Kill with missing worktree dir: %v", err)
	}
	if got := loggedArgs(t, f.gitLog); got != "" {
		t.Errorf("git must not run for a missing worktree dir; got:\n%s", got)
	}
}

func TestKillUnknownProjectErrors(t *testing.T) {
	f := newFixture(t, "", "")
	s := killFixtureSession()
	s.Project = "ghost"
	if err := f.n.Kill(context.Background(), s, true); err == nil {
		t.Fatal("Kill with unknown project: want error, got nil")
	}
}

func TestAlive(t *testing.T) {
	f := newFixture(t, "", "")
	if !f.n.Alive(context.Background(), killFixtureSession()) {
		t.Error("Alive: want true when has-session succeeds")
	}
	f2 := newFixture(t, "", `*"has-session"*) exit 1 ;;`)
	if f2.n.Alive(context.Background(), killFixtureSession()) {
		t.Error("Alive: want false when has-session fails")
	}
	if got := loggedArgs(t, f.tmuxLog); got != "has-session -t =lola-nori-eng-42" {
		t.Errorf("tmux calls %q, want exact-match has-session", got)
	}
}

func TestSessionID(t *testing.T) {
	if got := SessionID("nori", "ENG-42"); got != "lola-nori-eng-42" {
		t.Errorf("SessionID = %q, want lola-nori-eng-42", got)
	}
}

func TestExcludeLolaDirPlainGitDir(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := excludeLolaDir(dir); err != nil {
		t.Fatalf("excludeLolaDir: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dir, ".git", "info", "exclude"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != ".lola/\n" {
		t.Errorf("exclude = %q, want %q", got, ".lola/\n")
	}
}

func TestExcludeLolaDirAppendsAndIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	infoDir := filepath.Join(dir, ".git", "info")
	if err := os.MkdirAll(infoDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Existing content without trailing newline must be preserved intact.
	if err := os.WriteFile(filepath.Join(infoDir, "exclude"), []byte("*.tmp"), 0o644); err != nil {
		t.Fatal(err)
	}
	for range 2 { // second run must not duplicate the line
		if err := excludeLolaDir(dir); err != nil {
			t.Fatalf("excludeLolaDir: %v", err)
		}
	}
	got, err := os.ReadFile(filepath.Join(infoDir, "exclude"))
	if err != nil {
		t.Fatal(err)
	}
	if want := "*.tmp\n.lola/\n"; string(got) != want {
		t.Errorf("exclude = %q, want %q", got, want)
	}
}

func TestExcludeLolaDirResolvesWorktreePointer(t *testing.T) {
	// Linked-worktree layout: <dir>/.git is a file pointing at the
	// per-worktree git dir, whose commondir file points at the shared one.
	dir, meta := t.TempDir(), t.TempDir()
	common := filepath.Join(meta, "repo.git")
	gitDir := filepath.Join(common, "worktrees", "wt")
	if err := os.MkdirAll(gitDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gitDir, "commondir"), []byte("../..\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".git"), []byte("gitdir: "+gitDir+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := excludeLolaDir(dir); err != nil {
		t.Fatalf("excludeLolaDir: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(common, "info", "exclude"))
	if err != nil {
		t.Fatalf("common info/exclude: %v", err)
	}
	if string(got) != ".lola/\n" {
		t.Errorf("exclude = %q, want %q", got, ".lola/\n")
	}
	// The per-worktree dir must not grow its own (ignored-by-git) exclude.
	if _, err := os.Stat(filepath.Join(gitDir, "info", "exclude")); err == nil {
		t.Error("exclude must land in the common dir, not the per-worktree dir")
	}
}

func TestLaunchCommandQuoting(t *testing.T) {
	n := &Native{} // ClaudeBin empty -> "claude" from PATH
	got := n.launchCommand(config.Project{}, "lola-nori-eng-42")
	want := "env LOLA_SESSION=lola-nori-eng-42 claude --settings .lola/settings.json " +
		"'You are lola session lola-nori-eng-42. Read .lola/prompt.md in the current directory first; it contains your task briefing.'"
	if got != want {
		t.Errorf("launchCommand:\n%s\nwant:\n%s", got, want)
	}

	n.ClaudeBin = "/odd path/claude's bin"
	got = n.launchCommand(config.Project{}, "lola-nori-eng-42")
	if !strings.Contains(got, `'/odd path/claude'\''s bin'`) {
		t.Errorf("launchCommand must single-quote unsafe binary paths:\n%s", got)
	}
}

func TestLaunchCommandForwardsProjectEnv(t *testing.T) {
	n := &Native{}
	p := config.Project{Env: map[string]string{
		"B_VAR":   "plain",
		"APP_ENV": "local dev", // needs quoting
	}}
	got := n.launchCommand(p, "lola-nori-eng-42")
	// Sorted key order, each assignment quoted as a whole when needed, after
	// LOLA_SESSION and before the claude invocation.
	want := "env LOLA_SESSION=lola-nori-eng-42 'APP_ENV=local dev' B_VAR=plain claude --settings .lola/settings.json " +
		"'You are lola session lola-nori-eng-42. Read .lola/prompt.md in the current directory first; it contains your task briefing.'"
	if got != want {
		t.Errorf("launchCommand:\n%s\nwant:\n%s", got, want)
	}
}

func TestIssueFromSessionID(t *testing.T) {
	cases := []struct{ id, project, want string }{
		{"lola-nori-eng-42", "nori", "ENG-42"},
		{"lola-my-app-eng-7", "my-app", "ENG-7"},
		{"lola-nori-eng-42-r2", "nori", "ENG-42"},  // retry suffix stripped
		{"lola-nori-eng-42-r10", "nori", "ENG-42"}, // multi-digit attempt
		{"lola-nori-eng-42", "other", ""},
		{"lola-nori-eng-42", "", ""},
		{"unrelated", "nori", ""},
	}
	for _, c := range cases {
		if got := issueFromSessionID(c.id, c.project); got != c.want {
			t.Errorf("issueFromSessionID(%q, %q) = %q, want %q", c.id, c.project, got, c.want)
		}
	}
}
