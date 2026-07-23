package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeTmux installs a stub tmux that appends its argv to <dir>/args.log. Unless
// `reuse` is set it exits 1 for `has-session` (so Shell takes the create path)
// and 0 otherwise; with `reuse` it exits 0 for everything, standing in for an
// already-running shell session. No real tmux is ever run. Mirrors the fake-bin
// helper in internal/tmux/client_test.go.
func fakeTmux(t *testing.T, reuse bool) (bin, logPath string) {
	t.Helper()
	dir := t.TempDir()
	bin = filepath.Join(dir, "tmux")
	logPath = filepath.Join(dir, "args.log")
	script := "#!/bin/sh\necho \"$@\" >> " + logPath + "\n"
	if !reuse {
		script += "for a in \"$@\"; do case \"$a\" in has-session) exit 1;; esac; done\n"
	}
	script += "exit 0\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin, logPath
}

func tmuxLog(t *testing.T, p string) string {
	t.Helper()
	b, _ := os.ReadFile(p)
	return string(b)
}

// Shell rejects a name lacking the "-shell" marker (so it can never create an
// agent session), an empty worktree, and a worktree that isn't a directory — so
// a stray call never spawns a rootless or misrooted shell.
func TestShellValidatesArgs(t *testing.T) {
	bin, _ := fakeTmux(t, false)
	svc := &TermService{tmuxBin: bin, streams: map[string]*ptyStream{}}
	if _, err := svc.Shell("NORI-1", t.TempDir()); err == nil {
		t.Error("name without the shell marker must error")
	}
	if _, err := svc.Shell("NORI-1-shell-1", ""); err == nil {
		t.Error("empty worktree must error")
	}
	if _, err := svc.Shell("NORI-1-shell-1", filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Error("nonexistent worktree must error")
	}
}

// Shell creates the named session rooted in the worktree, probing first so it can
// reuse one instead of spawning a duplicate.
func TestShellCreatesSession(t *testing.T) {
	bin, logPath := fakeTmux(t, false)
	svc := &TermService{tmuxBin: bin, streams: map[string]*ptyStream{}}
	wt := t.TempDir()

	name, err := svc.Shell("NORI-1-shell-2", wt)
	if err != nil {
		t.Fatalf("Shell: %v", err)
	}
	if name != "NORI-1-shell-2" {
		t.Fatalf("name = %q, want NORI-1-shell-2", name)
	}
	log := tmuxLog(t, logPath)
	if !strings.Contains(log, "has-session -t =NORI-1-shell-2") {
		t.Errorf("expected has-session probe, log:\n%s", log)
	}
	if !strings.Contains(log, "new-session -d -s NORI-1-shell-2 -c "+wt) {
		t.Errorf("expected new-session rooted in worktree, log:\n%s", log)
	}
}

// A live shell session is reused, not recreated, so re-opening the tab attaches
// rather than spawning a second shell.
func TestShellReusesExisting(t *testing.T) {
	bin, logPath := fakeTmux(t, true) // has-session succeeds
	svc := &TermService{tmuxBin: bin, streams: map[string]*ptyStream{}}
	if _, err := svc.Shell("NORI-1-shell-1", t.TempDir()); err != nil {
		t.Fatalf("Shell: %v", err)
	}
	if strings.Contains(tmuxLog(t, logPath), "new-session") {
		t.Error("existing session must be reused, not recreated")
	}
}

// Shells lists only a session's own "<id>-shell-N" sessions, sorted by index —
// excluding the agent, unrelated sessions, and a same-prefix sibling (NORI-10).
func TestShellsListsAndSorts(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "tmux")
	script := "#!/bin/sh\n" +
		"for a in \"$@\"; do case \"$a\" in list-sessions) " +
		"printf '%s\\n' 'NORI-1' 'NORI-1-shell-2' 'other' 'NORI-1-shell-1' 'NORI-10-shell-1'; exit 0;; esac; done\n" +
		"exit 0\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	svc := &TermService{tmuxBin: bin, streams: map[string]*ptyStream{}}

	got := svc.Shells("NORI-1")
	want := []string{"NORI-1-shell-1", "NORI-1-shell-2"} // sorted by index; NORI-10 excluded
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("Shells = %v, want %v", got, want)
	}
	if svc.Shells("") != nil {
		t.Error("empty session id must list nothing")
	}
}

// CloseShell kills the named session (idempotent) and refuses a name without the
// shell marker, so it can never kill an agent session.
func TestCloseShellKillsSession(t *testing.T) {
	bin, logPath := fakeTmux(t, false)
	svc := &TermService{tmuxBin: bin, streams: map[string]*ptyStream{}}
	if err := svc.CloseShell("NORI-1-shell-1"); err != nil {
		t.Fatalf("CloseShell: %v", err)
	}
	if !strings.Contains(tmuxLog(t, logPath), "kill-session -t =NORI-1-shell-1") {
		t.Errorf("expected kill-session, log:\n%s", tmuxLog(t, logPath))
	}
	if err := svc.CloseShell("NORI-1"); err == nil {
		t.Error("name without the shell marker must error")
	}
}
