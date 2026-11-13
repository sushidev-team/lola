package runtime

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sushidev-team/lola/internal/session"
	"github.com/sushidev-team/lola/internal/state"
)

func TestLocalAIProfilePinsModelAndEffort(t *testing.T) {
	for _, kind := range []string{"claude", "codex", "opencode"} {
		t.Run(kind, func(t *testing.T) {
			f := newFixture(t, "", "*\"has-session\"*) exit 1 ;;")
			s, err := f.n.OpenDirectoryProfile(context.Background(), f.p, "lola-local-profile", f.repo, "checkout", kind, true, "Resolve conflicts", "provider/model", "high")
			if err != nil {
				t.Fatal(err)
			}
			if s.Model != "provider/model" || s.ModelEffort != "high" || s.OwnsBranch() {
				t.Fatalf("profile or ownership lost: %+v", s)
			}
			art, _ := localArtifacts(s.ID)
			raw, err := os.ReadFile(filepath.Join(art, "launch"))
			if err != nil {
				t.Fatal(err)
			}
			log := string(raw)
			if !strings.Contains(log, "provider/model") || !strings.Contains(log, "high") {
				t.Fatalf("launch omitted model/effort: %s", log)
			}
		})
	}
}

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

// A pasted brief full of apostrophes costs 13 bytes each on the tmux command line. Past about
// 16 KB tmux refuses the command and the session never opens.
func TestLocalLongBriefStaysOffTheTmuxCommandLine(t *testing.T) {
	f := newFixture(t, "", "*\"has-session\"*) exit 1 ;;")
	out := filepath.Join(t.TempDir(), "argv")
	stub := filepath.Join(t.TempDir(), "claude")
	if err := os.WriteFile(stub, []byte("#!/bin/sh\nfor a in \"$@\"; do printf '%s\\n' \"$a\"; done > "+out+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	f.n.ClaudeBin = stub
	id := "lola-local-brief"
	art, err := localArtifacts(id)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(art, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(art, "env"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	brief := strings.Repeat("it's ", 1400)
	s := session.Session{ID: id, Agent: "claude", Kind: session.KindLocal, Worktree: f.repo}
	if _, err := f.n.startLocal(context.Background(), s, false, brief); err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(loggedArgs(t, f.tmuxLog), "\n") {
		if strings.Contains(line, "new-session") && len(line) > 4096 {
			t.Fatalf("new-session is %d bytes", len(line))
		}
	}
	script := filepath.Join(art, "launch")
	if fi, err := os.Stat(script); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("launch file: %v %v", fi, err)
	}
	if b, err := exec.Command("sh", script).CombinedOutput(); err != nil {
		t.Fatalf("run it: %v: %s", err, b)
	}
	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), brief) {
		t.Errorf("the brief did not reach the agent whole (%d bytes)", len(got))
	}
	if _, err := os.Stat(script); !os.IsNotExist(err) {
		t.Errorf("the launch file holding the brief was left behind: %v", err)
	}
}
