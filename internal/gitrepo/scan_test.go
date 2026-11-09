package gitrepo

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Scan lists only the immediate subdirectories that are the ROOT of their own
// checkout: a plain folder, a hidden one and a folder inside the parent's own
// repository are not apps.
func TestScanListsNestedCheckouts(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	parent := t.TempDir()
	initRepo := func(dir, remote string) {
		t.Helper()
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		for _, args := range [][]string{{"init", "-b", "main"}, {"remote", "add", "origin", remote}} {
			cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("git %v: %v\n%s", args, err, out)
			}
		}
	}
	initRepo(filepath.Join(parent, "web"), "git@github.com:acme/web.git")
	initRepo(filepath.Join(parent, "api"), "https://github.com/acme/api.git")
	initRepo(filepath.Join(parent, ".hidden"), "git@github.com:acme/hidden.git")
	if err := os.MkdirAll(filepath.Join(parent, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(parent, "README"), nil, 0o644); err != nil {
		t.Fatal(err)
	}

	got := Scan(context.Background(), parent)
	if len(got) != 2 {
		t.Fatalf("Scan = %+v, want api and web", got)
	}
	if got[0].Root != filepath.Join(parent, "api") || got[0].Repo != "acme/api" {
		t.Errorf("first = %+v", got[0])
	}
	if got[1].Root != filepath.Join(parent, "web") || got[1].Repo != "acme/web" {
		t.Errorf("second = %+v", got[1])
	}

	// A monorepo's subfolders resolve to the monorepo root: never apps.
	initRepo(filepath.Join(parent, "mono"), "git@github.com:acme/mono.git")
	if err := os.MkdirAll(filepath.Join(parent, "mono", "pkg"), 0o755); err != nil {
		t.Fatal(err)
	}
	if apps := Scan(context.Background(), filepath.Join(parent, "mono")); len(apps) != 0 {
		t.Fatalf("monorepo subfolders offered as apps: %+v", apps)
	}
	if apps := Scan(context.Background(), filepath.Join(parent, "missing")); apps != nil {
		t.Fatalf("missing dir = %+v", apps)
	}
}
