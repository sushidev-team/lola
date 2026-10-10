package checkpoint

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

// repo builds a real repository with one commit and .lola/ excluded the way the
// runtime excludes it, and returns its dir plus a git helper.
func repo(t *testing.T) (string, func(args ...string) string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	// The package's own git runs inherit the process env; keep the user's config
	// (signing, hooks) out of them too.
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	t.Setenv("GIT_CONFIG_SYSTEM", "/dev/null")
	dir := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t",
			"GIT_COMMITTER_EMAIL=t@t")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "-q", "-b", "main")
	write(t, dir, "a.txt", "a1\n")
	write(t, dir, "b.txt", "b1\n")
	write(t, dir, ".gitignore", "ignored.txt\n")
	git("add", "-A")
	git("commit", "-q", "-m", "init")
	if err := os.WriteFile(filepath.Join(dir, ".git", "info", "exclude"), []byte(".lola/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	write(t, dir, ".lola/context/notes.md", "keep me\n")
	write(t, dir, "ignored.txt", "local\n")
	return dir, git
}

func write(t *testing.T, dir, name, body string) {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, dir, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		return "<missing>"
	}
	return string(b)
}

// TestSnapshotSeesARacyEdit pins the racy-git case behind a flaky checkpoint:
// a same-size edit whose stat matches the index entry, made in the same second
// the index was written. git re-hashes such an entry only because the index's
// mtime is not newer than the file's, so the temp index copy must keep that
// mtime. Timestamps are pinned and ctime is ignored, so this fails every time
// the copy gets a fresh mtime, not one run in ten.
func TestSnapshotSeesARacyEdit(t *testing.T) {
	dir, git := repo(t)
	git("config", "core.trustctime", "false")
	at := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	touch := func(name string) {
		t.Helper()
		if err := os.Chtimes(filepath.Join(dir, name), at, at); err != nil {
			t.Fatal(err)
		}
	}
	touch("a.txt")
	git("add", "a.txt") // the index entry records a.txt at `at`, size 3
	write(t, dir, "a.txt", "a2\n")
	touch("a.txt")
	touch(".git/index")

	tree, _, err := Git{}.Snapshot(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := git("cat-file", "-p", tree+":a.txt"); got != "a2" {
		t.Fatalf("snapshot a.txt = %q, want the edited %q", got, "a2")
	}
}

func TestRecordDedupesAndListsInOrder(t *testing.T) {
	dir, git := repo(t)
	ctx := context.Background()
	g := Git{}
	sess := "lola-p-eng-1"

	write(t, dir, "a.txt", "a2\n")
	c1, rec, err := g.Record(ctx, dir, sess, "turn 1")
	if err != nil || !rec || c1.Seq != 1 {
		t.Fatalf("first record = %+v, %v, %v", c1, rec, err)
	}
	// Nothing changed: no new ref.
	again, rec, err := g.Record(ctx, dir, sess, "turn 2")
	if err != nil || rec || again.Seq != 1 {
		t.Fatalf("unchanged record = %+v, %v, %v; want the existing #1", again, rec, err)
	}
	write(t, dir, "c.txt", "new\n") // untracked
	c2, rec, err := g.Record(ctx, dir, sess, "turn 2\nwith a newline")
	if err != nil || !rec || c2.Seq != 2 {
		t.Fatalf("second record = %+v, %v, %v", c2, rec, err)
	}

	list, err := g.List(ctx, dir, sess)
	if err != nil || len(list) != 2 || list[0].Seq != 1 || list[1].Seq != 2 {
		t.Fatalf("List = %+v, %v", list, err)
	}
	if list[1].Label != "turn 2 with a newline" {
		t.Errorf("label = %q", list[1].Label)
	}
	if list[1].Head != git("rev-parse", "HEAD") {
		t.Errorf("Head = %s, want HEAD", list[1].Head)
	}
	// The snapshot carries the untracked file, never .lola/ or an ignored file.
	files := strings.Fields(git("ls-tree", "-r", "--name-only", list[1].SHA))
	sort.Strings(files)
	if strings.Join(files, ",") != ".gitignore,a.txt,b.txt,c.txt" {
		t.Errorf("snapshot files = %v", files)
	}
	// The real index and the branch are untouched.
	if st := git("status", "--porcelain"); !strings.Contains(st, "?? c.txt") || !strings.Contains(st, "M a.txt") {
		t.Errorf("status after record = %q", st)
	}
	if git("rev-parse", "HEAD") != list[0].Head {
		t.Error("HEAD moved")
	}
}

func TestRestoreRevertsATurnAndIsUndoable(t *testing.T) {
	dir, git := repo(t)
	ctx := context.Background()
	g := Git{}
	sess := "s"

	write(t, dir, "a.txt", "a2\n")
	if _, _, err := g.Record(ctx, dir, sess, "turn 1"); err != nil {
		t.Fatal(err)
	}
	// The bad turn: edit, add, delete, and even commit.
	write(t, dir, "a.txt", "a3\n")
	write(t, dir, "new.txt", "n\n")
	os.Remove(filepath.Join(dir, "b.txt"))
	write(t, dir, "committed.txt", "c\n")
	git("add", "committed.txt")
	git("commit", "-q", "-m", "agent commit")
	head := git("rev-parse", "HEAD")

	safety, err := g.Restore(ctx, dir, sess, 1)
	if err != nil {
		t.Fatal(err)
	}
	if safety.Seq != 2 || safety.Label != "before restore to #1" {
		t.Errorf("safety = %+v", safety)
	}
	if read(t, dir, "a.txt") != "a2\n" || read(t, dir, "b.txt") != "b1\n" {
		t.Errorf("tracked files not restored: a=%q b=%q", read(t, dir, "a.txt"), read(t, dir, "b.txt"))
	}
	if read(t, dir, "new.txt") != "<missing>" || read(t, dir, "committed.txt") != "<missing>" {
		t.Error("files added after the checkpoint were not removed")
	}
	if read(t, dir, ".lola/context/notes.md") != "keep me\n" || read(t, dir, "ignored.txt") != "local\n" {
		t.Error("restore touched .lola/ or an ignored file")
	}
	if git("rev-parse", "HEAD") != head {
		t.Error("restore moved HEAD")
	}
	// The index is back to HEAD: the restored state is plain uncommitted work.
	if staged := git("diff", "--cached", "--name-only"); staged != "" {
		t.Errorf("index not reset, staged: %q", staged)
	}

	// Undo the restore.
	if _, err := g.Restore(ctx, dir, sess, safety.Seq); err != nil {
		t.Fatal(err)
	}
	if read(t, dir, "a.txt") != "a3\n" || read(t, dir, "new.txt") != "n\n" || read(t, dir, "b.txt") != "<missing>" {
		t.Error("restoring the safety checkpoint did not bring the bad turn back")
	}
}

func TestGetPruneAndErrors(t *testing.T) {
	dir, _ := repo(t)
	ctx := context.Background()
	g := Git{}
	for i, body := range []string{"x\n", "y\n"} {
		write(t, dir, "a.txt", body)
		if _, _, err := g.Record(ctx, dir, "s", "turn"); err != nil {
			t.Fatalf("record %d: %v", i, err)
		}
	}
	cp, prev, err := g.Get(ctx, dir, "s", 2)
	if err != nil || cp.Seq != 2 || prev.Seq != 1 {
		t.Fatalf("Get = %+v %+v %v", cp, prev, err)
	}
	if _, _, err := g.Get(ctx, dir, "s", 9); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing seq err = %v", err)
	}
	if _, _, err := g.Record(ctx, dir, "../evil", "x"); err == nil {
		t.Error("a session id that climbs out of the ref namespace was accepted")
	}
	// Another session's refs are a different namespace.
	if l, _ := g.List(ctx, dir, "s2"); len(l) != 0 {
		t.Errorf("s2 sees %d checkpoints", len(l))
	}
	if err := g.Prune(ctx, dir, "s"); err != nil {
		t.Fatal(err)
	}
	if l, _ := g.List(ctx, dir, "s"); len(l) != 0 {
		t.Errorf("after Prune: %d left", len(l))
	}
}

func TestApplyOnAFreshCheckout(t *testing.T) {
	dir, git := repo(t)
	ctx := context.Background()
	g := Git{}
	write(t, dir, "a.txt", "fork me\n")
	write(t, dir, "u.txt", "untracked\n")
	cp, _, err := g.Record(ctx, dir, "s", "turn 1")
	if err != nil {
		t.Fatal(err)
	}
	// A second worktree at the checkpoint's HEAD, as a fork creates it.
	other := filepath.Join(t.TempDir(), "fork")
	git("worktree", "add", "-q", "-b", "fork", other, cp.Head)
	if err := g.Apply(ctx, other, cp.Tree); err != nil {
		t.Fatal(err)
	}
	if read(t, other, "a.txt") != "fork me\n" || read(t, other, "u.txt") != "untracked\n" {
		t.Error("fork worktree does not hold the checkpoint state")
	}
}

func TestCleanLabel(t *testing.T) {
	if got := cleanLabel(" \x1b[31m turn\t1 "); got != "[31m turn 1" {
		t.Errorf("cleanLabel = %q", got)
	}
	if cleanLabel("") != "checkpoint" {
		t.Error("empty label")
	}
}

// An ignored file the restore would overwrite is never captured by the safety
// checkpoint, so the restore must refuse rather than destroy it.
func TestRestoreRefusesToClobberIgnoredFiles(t *testing.T) {
	dir, git := repo(t)
	ctx := context.Background()
	g := Git{}
	write(t, dir, "conf.txt", "v1\n")
	git("add", "conf.txt")
	git("commit", "-q", "-m", "track conf")
	if _, _, err := g.Record(ctx, dir, "s", "turn 1"); err != nil {
		t.Fatal(err)
	}
	// Later the file is untracked and ignored, and gets local-only contents.
	git("rm", "-q", "--cached", "conf.txt")
	write(t, dir, ".gitignore", "ignored.txt\nconf.txt\n")
	write(t, dir, "conf.txt", "precious local value\n")

	if _, err := g.Restore(ctx, dir, "s", 1); !errors.Is(err, ErrWouldClobber) || !strings.Contains(err.Error(), "conf.txt") {
		t.Fatalf("err = %v, want ErrWouldClobber naming conf.txt", err)
	}
	if read(t, dir, "conf.txt") != "precious local value\n" {
		t.Error("the ignored file was overwritten")
	}
	if read(t, dir, ".gitignore") != "ignored.txt\nconf.txt\n" {
		t.Error("a refused restore still moved files")
	}
}

func TestBlocked(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "f", "x")
	write(t, dir, "d/g", "x")
	for p, want := range map[string]bool{"f": true, "f/sub": true, "d": true, "d/g": true, "d/new": false, "nope/x": false} {
		if got := blocked(dir, p); got != want {
			t.Errorf("blocked(%q) = %v, want %v", p, got, want)
		}
	}
}

// A restore that fails after the index was pointed at the safety snapshot puts
// the index back on HEAD — even when the request's context is already gone —
// so the agent is not left with every file staged.
func TestRestoreFailureResetsTheIndex(t *testing.T) {
	dir, git := repo(t)
	ctx := context.Background()
	if _, _, err := (Git{}).Record(ctx, dir, "s", "start"); err != nil {
		t.Fatal(err)
	}
	write(t, dir, "new.txt", "turn\n")
	rctx, cancel := context.WithCancel(ctx)
	g := Git{run: func(c context.Context, bin, d string, env []string, args ...string) ([]byte, error) {
		if len(args) > 1 && args[0] == "read-tree" && args[1] == "--reset" {
			cancel() // the request goes away mid-restore
			return nil, errors.New("boom")
		}
		return runGit(c, bin, d, env, args...)
	}}
	if _, err := g.Restore(rctx, dir, "s", 1); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("err = %v", err)
	}
	if staged := git("diff", "--cached", "--name-only"); staged != "" {
		t.Errorf("index left staged after a failed restore: %q", staged)
	}
	if got := git("status", "--porcelain", "--", "new.txt"); got != "?? new.txt" {
		t.Errorf("new.txt = %q, want untracked again", got)
	}
}
