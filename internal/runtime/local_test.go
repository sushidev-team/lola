package runtime

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sushidev-team/lola/internal/session"
	"github.com/sushidev-team/lola/internal/state"
)

func TestLocalSessionsShareCheckoutWithoutOwningIt(t *testing.T) {
	f := newFixture(t, "", "*\"has-session\"*) exit 1 ;;")
	f.p.Env = map[string]string{"PROJECT_VALUE": "hello 'quoted'"}
	original := filepath.Join(f.repo, "keep.txt")
	if err := os.WriteFile(original, []byte("user work"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, kind := range []string{"claude", "codex", "opencode", "shell"} {
		id := "lola-local-" + kind
		s, err := f.n.OpenDirectory(ctx, f.p, id, f.repo, "main", kind, kind != "shell")
		if err != nil {
			t.Fatal(err)
		}
		if s.Kind != session.KindLocal || s.OwnsBranch() || s.Worktree != f.repo {
			t.Fatalf("unsafe ownership: %+v", s)
		}
		if kind == "shell" && s.AgentState != state.AgentShell {
			t.Fatalf("shell state: %s", s.AgentState)
		}
		art, _ := localArtifacts(id)
		env, err := os.ReadFile(filepath.Join(art, "env"))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(env), "PROJECT_VALUE=") {
			t.Fatal("lost project environment")
		}
		shellEnv, _ := os.ReadFile(filepath.Join(art, "shell-env"))
		if strings.Contains(string(shellEnv), "LOLA_SESSION=") {
			t.Fatal("shell tab inherited agent callback identity")
		}
		if err := f.n.Kill(ctx, s, true, true); err != nil {
			t.Fatal(err)
		}
		body, err := os.ReadFile(original)
		if err != nil || string(body) != "user work" {
			t.Fatalf("checkout altered: %q %v", body, err)
		}
	}
	entries, _ := os.ReadDir(f.repo)
	if len(entries) != 1 || entries[0].Name() != "keep.txt" {
		t.Fatalf("artifacts leaked into checkout: %v", entries)
	}
	if log := loggedArgs(t, f.gitLog); strings.Contains(log, "worktree") || strings.Contains(log, "branch -") {
		t.Fatalf("local launch/close mutated git: %s", log)
	}
	log := loggedArgs(t, f.tmuxLog)
	if strings.Contains(log, "./.lola/env") || strings.Contains(log, "--continue") {
		t.Fatalf("shared launch state used: %s", log)
	}
}

func TestLocalReviveStartsShellWithoutAgentOrCheckoutWrites(t *testing.T) {
	f := newFixture(t, "", "*\"has-session\"*) exit 1 ;;")
	s, err := f.n.OpenDirectory(context.Background(), f.p, "lola-local-revive", f.repo, "main", "claude", false)
	if err != nil {
		t.Fatal(err)
	}
	revived, err := f.n.Revive(context.Background(), s)
	if err != nil || revived.AgentState != state.AgentShell {
		t.Fatalf("revive = %+v, %v", revived, err)
	}
	if log := loggedArgs(t, f.tmuxLog); strings.Contains(log, "--settings") {
		t.Fatalf("shell revived as agent: %s", log)
	}
}

func TestLocalRejectsInvalidDirectoryBeforeLaunch(t *testing.T) {
	f := newFixture(t, "", "")
	_, err := f.n.OpenDirectory(context.Background(), f.p, "lola-local-test", filepath.Join(f.repo, "missing"), "main", "claude", false)
	if err == nil {
		t.Fatal("missing directory accepted")
	}
	if log := loggedArgs(t, f.tmuxLog); log != "" {
		t.Fatal(log)
	}
}
