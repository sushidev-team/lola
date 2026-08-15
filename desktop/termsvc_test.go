package main

import (
	"github.com/sushidev-team/lola/internal/lolaenv"
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
	// The shell tab must source .lola/env like the agent pane does, or a
	// project command typed here silently runs without [[project]].env.
	if !strings.Contains(log, lolaenv.ShellCommand) {
		t.Errorf("expected the shell to export .lola/env, log:\n%s", log)
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

// The review pane a visible review pass opens is listed as a tab too — LAST, so
// a review starting or ending never renumbers the shell tabs beside it. Another
// session's review pane is not this session's.
func TestShellsIncludesTheReviewPaneLast(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "tmux")
	script := "#!/bin/sh\n" +
		"for a in \"$@\"; do case \"$a\" in list-sessions) " +
		"printf '%s\\n' 'NORI-1' 'NORI-1-review' 'NORI-1-shell-2' 'NORI-2-review' 'NORI-1-shell-1'; exit 0;; esac; done\n" +
		"exit 0\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	svc := &TermService{tmuxBin: bin, streams: map[string]*ptyStream{}}

	got := svc.Shells("NORI-1")
	want := []string{"NORI-1-shell-1", "NORI-1-shell-2", "NORI-1-review"}
	if len(got) != len(want) {
		t.Fatalf("Shells = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Shells = %v, want %v", got, want)
		}
	}
}

// A review pane may be closed like any other tab; the marker guard still refuses
// an agent session name, which is what keeps a stray call from killing an agent.
func TestCloseShellAcceptsAReviewPane(t *testing.T) {
	bin, logPath := fakeTmux(t, false)
	svc := &TermService{tmuxBin: bin, streams: map[string]*ptyStream{}}
	if err := svc.CloseShell("NORI-1-review"); err != nil {
		t.Fatalf("CloseShell on a review pane: %v", err)
	}
	if !strings.Contains(tmuxLog(t, logPath), "kill-session -t =NORI-1-review") {
		t.Error("CloseShell must kill the review pane's tmux session")
	}
	if err := svc.CloseShell("NORI-1"); err == nil {
		t.Error("CloseShell must still refuse an agent session name")
	}
}

// Dev tabs ("<id>-dev-N", the project's dev_commands started by the daemon) are
// listed FIRST — they belong to the project rather than to this session, so they
// hold their place while shells come and go — and another session's dev tab is
// not this session's, including one whose id merely starts with the same text.
func TestShellsListsDevTabsFirst(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "tmux")
	script := "#!/bin/sh\n" +
		"for a in \"$@\"; do case \"$a\" in list-sessions) " +
		"printf '%s\\n' 'NORI-1' 'NORI-1-shell-1' 'NORI-1-dev-2' 'NORI-1-review' 'NORI-1-dev-1' 'NORI-10-dev-1'; exit 0;; esac; done\n" +
		"exit 0\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	svc := &TermService{tmuxBin: bin, streams: map[string]*ptyStream{}}

	got := svc.Shells("NORI-1")
	want := []string{"NORI-1-dev-1", "NORI-1-dev-2", "NORI-1-shell-1", "NORI-1-review"}
	if len(got) != len(want) {
		t.Fatalf("Shells = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("Shells[%d] = %q, want %q (full: %v)", i, got[i], want[i], got)
		}
	}
}

// The app closes a dev tab through the daemon (which stops the whole set), but
// CloseShell must still ACCEPT the name: a stale tab left by a killed session is
// reaped through the same path as a shell.
func TestCloseShellAcceptsADevTab(t *testing.T) {
	bin, logPath := fakeTmux(t, false)
	svc := &TermService{tmuxBin: bin, streams: map[string]*ptyStream{}}
	if err := svc.CloseShell("NORI-1-dev-1"); err != nil {
		t.Fatalf("CloseShell on a dev tab: %v", err)
	}
	if !strings.Contains(tmuxLog(t, logPath), "kill-session -t =NORI-1-dev-1") {
		t.Errorf("expected kill-session, log:\n%s", tmuxLog(t, logPath))
	}
}
