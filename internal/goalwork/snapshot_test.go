package goalwork

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestSnapshotUsesCommittedBlobsAndExcludesPrivateFiles(t *testing.T) {
	m, r := fixture(t)
	put(t, filepath.Join(r.Source, "README.md"), "committed architecture\n")
	put(t, filepath.Join(r.Source, ".env"), "private credential\n")
	put(t, filepath.Join(r.Source, "binary"), "\x00\xff")
	if err := os.Symlink(".env", filepath.Join(r.Source, "link")); err != nil {
		t.Fatal(err)
	}
	gitOK(t, m, r.Source, "add", ".")
	gitOK(t, m, r.Source, "commit", "-m", "context")
	commit := gitOK(t, m, r.Source, "rev-parse", "main")
	put(t, filepath.Join(r.Source, "README.md"), "uncommitted changes never disclosed")
	s, err := m.Snapshot(context.Background(), "api", r.Source, "main", nil)
	if err != nil {
		t.Fatal(err)
	}
	if s.Commit != commit || len(s.Files) != 1 || s.Files[0].Content != "committed architecture\n" || s.Files[0].SHA256 == "" {
		t.Fatalf("wrong source: %+v", s)
	}
	if slices.Contains(s.Tree, ".env") || slices.Contains(s.Tree, "link") {
		t.Fatal("private or symlink context leaked")
	}
	for _, p := range []string{".env", "link", "binary", "../README.md", "absent", "README.md\n", ".git/config"} {
		if _, err := m.Snapshot(context.Background(), "api", r.Source, "main", []string{p}); err == nil {
			t.Fatalf("accepted %q", p)
		}
	}
	if _, err := m.Snapshot(context.Background(), "api", r.Source, "main", []string{"README.md", "README.md"}); err == nil {
		t.Fatal("duplicate context accepted")
	}
	put(t, filepath.Join(r.Source, "large.txt"), strings.Repeat("a", 16385))
	gitOK(t, m, r.Source, "add", "large.txt")
	gitOK(t, m, r.Source, "commit", "-m", "large")
	if _, err := m.Snapshot(context.Background(), "api", r.Source, "main", []string{"large.txt"}); err == nil {
		t.Fatal("oversized blob accepted")
	}
	if got, _ := os.ReadFile(filepath.Join(r.Source, "README.md")); string(got) != "uncommitted changes never disclosed" {
		t.Fatal("working tree changed")
	}
}

func TestSnapshotDoesNotExecuteTextconvOrWorkingTreeFilter(t *testing.T) {
	m, r := fixture(t)
	put(t, filepath.Join(r.Source, "README.md"), "source\n")
	put(t, filepath.Join(r.Source, ".gitattributes"), "README.md diff=hostile\n")
	gitOK(t, m, r.Source, "add", ".")
	gitOK(t, m, r.Source, "commit", "-m", "context")
	gitOK(t, m, r.Source, "config", "diff.hostile.textconv", "false")
	if _, err := m.Snapshot(context.Background(), "api", r.Source, "main", []string{"README.md"}); err != nil {
		t.Fatal("snapshot invoked working tree filter:", err)
	}
}
