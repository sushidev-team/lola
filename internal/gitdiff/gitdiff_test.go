package gitdiff

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const twoFiles = `diff --git a/a.go b/a.go
index 1111111..2222222 100644
--- a/a.go
+++ b/a.go
@@ -1,3 +1,4 @@
 package a
-var x = 1
+var x = 2
+var y = 3
 // end
diff --git a/old.txt b/new.txt
similarity index 90%
rename from old.txt
rename to new.txt
index 3333333..4444444 100644
--- a/old.txt
+++ b/new.txt
@@ -5,2 +5,2 @@ func f() {
 keep
-gone
+here
diff --git a/gone.txt b/gone.txt
deleted file mode 100644
index 5555555..0000000
--- a/gone.txt
+++ /dev/null
@@ -1 +0,0 @@
-bye
diff --git a/img.png b/img.png
new file mode 100644
index 0000000..6666666
Binary files /dev/null and b/img.png differ
`

func TestSplitPatch(t *testing.T) {
	files := SplitPatch(twoFiles)
	if len(files) != 4 {
		t.Fatalf("got %d files, want 4: %+v", len(files), files)
	}
	a := files[0]
	if a.Path != "a.go" || a.Status != "modified" || a.Additions != 2 || a.Deletions != 1 || a.OldPath != "" {
		t.Errorf("a.go = %+v", a)
	}
	if !strings.HasPrefix(a.Patch, "@@ -1,3 +1,4 @@\n") || strings.Contains(a.Patch, "+++") {
		t.Errorf("a.go patch must start at the hunk header, got %q", a.Patch)
	}
	r := files[1]
	if r.Path != "new.txt" || r.OldPath != "old.txt" || r.Status != "renamed" || r.Additions != 1 || r.Deletions != 1 {
		t.Errorf("rename = %+v", r)
	}
	d := files[2]
	if d.Path != "gone.txt" || d.Status != "deleted" || d.Deletions != 1 || d.OldPath != "" {
		t.Errorf("deletion = %+v", d)
	}
	b := files[3]
	if b.Path != "img.png" || b.Status != "added" || !b.Binary || b.Patch != "" {
		t.Errorf("binary = %+v", b)
	}
}

func TestParseLinesNumbersBothSides(t *testing.T) {
	got := ParseLines("@@ -10,3 +10,4 @@ ctx\n a\n-b\n+B\n+C\n c\n\\ No newline at end of file\n")
	want := []Line{
		{Kind: '@', Text: "@@ -10,3 +10,4 @@ ctx"},
		{Kind: ' ', OldLine: 10, NewLine: 10, Text: "a"},
		{Kind: '-', OldLine: 11, Text: "b"},
		{Kind: '+', NewLine: 11, Text: "B"},
		{Kind: '+', NewLine: 12, Text: "C"},
		{Kind: ' ', OldLine: 12, NewLine: 13, Text: "c"},
		{Kind: '\\', Text: "\\ No newline at end of file"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d lines, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("line %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestAddedPatch(t *testing.T) {
	p, n := addedPatch("one\ntwo")
	if n != 2 || p != "@@ -0,0 +1,2 @@\n+one\n+two\n\\ No newline at end of file\n" {
		t.Errorf("addedPatch = %d %q", n, p)
	}
	if p, n := addedPatch(""); p != "" || n != 0 {
		t.Errorf("empty file = %d %q", n, p)
	}
}

func TestDiffRejectsOptionLikeBase(t *testing.T) {
	if _, err := (Differ{}).Diff(context.Background(), t.TempDir(), "--output=/tmp/x"); err == nil {
		t.Fatal("a base starting with '-' must be refused before it reaches git argv")
	}
}

func TestDiffNoBase(t *testing.T) {
	d := Differ{run: func(context.Context, string, string, ...string) ([]byte, error) {
		return nil, errors.New("exit status 1")
	}}
	if _, err := d.Diff(context.Background(), "/wt", "main"); !errors.Is(err, ErrNoBase) {
		t.Fatalf("err = %v, want ErrNoBase", err)
	}
}

func TestDiffPrefersRemoteBaseAndCapsFiles(t *testing.T) {
	big := "@@ -0,0 +1 @@\n+" + strings.Repeat("x", MaxFilePatchBytes) + "\n"
	diff := "diff --git a/big b/big\n--- a/big\n+++ b/big\n" + big +
		"diff --git a/s b/s\n--- a/s\n+++ b/s\n@@ -1 +1 @@\n-a\n+b\n"
	var calls []string
	d := Differ{run: func(_ context.Context, _, _ string, args ...string) ([]byte, error) {
		calls = append(calls, strings.Join(args, " "))
		switch {
		case args[0] == "merge-base" && args[2] == "origin/main":
			return []byte("abc123\n"), nil
		case args[0] == "ls-files":
			return nil, nil
		case strings.Contains(strings.Join(args, " "), " diff "):
			return []byte(diff), nil
		}
		return nil, errors.New("unexpected")
	}}
	res, err := d.Diff(context.Background(), "/wt", "main")
	if err != nil {
		t.Fatal(err)
	}
	if res.Base != "origin/main" || res.MergeBase != "abc123" {
		t.Errorf("base = %q %q", res.Base, res.MergeBase)
	}
	if len(res.Files) != 2 || !res.Files[0].TooLarge || res.Files[0].Patch != "" || res.Files[1].Patch == "" {
		t.Errorf("files = %+v", res.Files)
	}
	if !strings.Contains(calls[1], "abc123 --") {
		t.Errorf("diff must run against the merge-base, got %q", calls[1])
	}
}

// TestDiffRealRepo runs the whole thing against a real repository: a commit on
// the branch, an uncommitted edit, an untracked file, and a commit main gained
// AFTER the fork, which must NOT appear (the merge-base is the point).
func TestDiffRealRepo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t",
			"GIT_COMMITTER_EMAIL=t@t", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	git("init", "-q", "-b", "main")
	write("a.txt", "one\ntwo\n")
	git("add", ".")
	git("commit", "-qm", "base")
	git("checkout", "-qb", "feature")
	write("a.txt", "one\nTWO\n")
	git("commit", "-qam", "feature work")
	git("checkout", "-q", "main")
	write("main-only.txt", "later\n")
	git("add", ".")
	git("commit", "-qm", "main moved on")
	git("checkout", "-q", "feature")
	write("a.txt", "one\nTWO\nthree\n") // uncommitted
	write("new.txt", "fresh\n")         // untracked

	res, err := Diff(context.Background(), dir, "main")
	if err != nil {
		t.Fatal(err)
	}
	if res.Base != "main" {
		t.Errorf("base = %q, want the local branch when there is no origin", res.Base)
	}
	byPath := map[string]File{}
	for _, f := range res.Files {
		byPath[f.Path] = f
	}
	if _, ok := byPath["main-only.txt"]; ok {
		t.Error("a commit main gained after the fork must not appear in the session's diff")
	}
	a := byPath["a.txt"]
	if a.Additions != 2 || a.Deletions != 1 || !strings.Contains(a.Patch, "+three") {
		t.Errorf("a.txt must carry committed AND uncommitted work: %+v", a)
	}
	n := byPath["new.txt"]
	if !n.Untracked || n.Status != "added" || n.Additions != 1 {
		t.Errorf("new.txt = %+v", n)
	}
}

func TestUntrackedOverflowIsDisclosed(t *testing.T) {
	dir := t.TempDir()
	var z strings.Builder
	for i := range MaxUntracked + 1 {
		name := "f" + strconv.Itoa(i)
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		z.WriteString(name + "\x00")
	}
	d := Differ{run: func(_ context.Context, _, _ string, args ...string) ([]byte, error) {
		switch args[0] {
		case "merge-base":
			return []byte("abc\n"), nil
		case "ls-files":
			return []byte(z.String()), nil
		}
		return nil, nil
	}}
	res, err := d.Diff(context.Background(), dir, "main")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Files) != MaxUntracked || !res.Truncated {
		t.Errorf("files=%d truncated=%v, want %d and true", len(res.Files), res.Truncated, MaxUntracked)
	}
}
