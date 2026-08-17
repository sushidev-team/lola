package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sushidev-team/lola/internal/lolaenv"
	"github.com/sushidev-team/lola/internal/tmux"
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

// Scrolling drives tmux's COPY MODE rather than the terminal's mouse reporting:
// `tmux attach` runs on the alternate screen, so the history the user wants is
// tmux's, and reaching it this way works whether or not [tmux].mouse is on.
// `-e` is what makes the pane leave copy mode on its own at the bottom.
func TestScrollEntersCopyModeAndScrollsUp(t *testing.T) {
	bin, logPath := fakeTmux(t, false)
	svc := &TermService{tmuxBin: bin, streams: map[string]*ptyStream{}}
	if err := svc.Scroll("lola-nori-1", 3); err != nil {
		t.Fatalf("Scroll: %v", err)
	}
	// One invocation, two tmux commands separated by ";".
	want := "copy-mode -e -t =lola-nori-1: ; send-keys -X -N 3 -t =lola-nori-1: scroll-up"
	if got := tmuxLog(t, logPath); !strings.Contains(got, want) {
		t.Errorf("scroll log:\n%s\nwant %q", got, want)
	}
}

// A negative count scrolls back toward the live view — the same command with the
// sign carried into the verb, so the frontend never has to know tmux's spelling.
func TestScrollDownUsesScrollDown(t *testing.T) {
	bin, logPath := fakeTmux(t, false)
	svc := &TermService{tmuxBin: bin, streams: map[string]*ptyStream{}}
	if err := svc.Scroll("lola-nori-1", -2); err != nil {
		t.Fatalf("Scroll: %v", err)
	}
	if got := tmuxLog(t, logPath); !strings.Contains(got, "-N 2 -t =lola-nori-1: scroll-down") {
		t.Errorf("scroll log:\n%s\nwant a scroll-down of 2", got)
	}
}

// The line count comes from the webview, so it is bounded here; zero and an
// empty name never reach tmux at all.
func TestScrollBoundsItsInput(t *testing.T) {
	bin, logPath := fakeTmux(t, false)
	svc := &TermService{tmuxBin: bin, streams: map[string]*ptyStream{}}
	if err := svc.Scroll("lola-nori-1", 1<<30); err != nil {
		t.Fatalf("Scroll: %v", err)
	}
	if got := tmuxLog(t, logPath); !strings.Contains(got, "-N 500 ") {
		t.Errorf("scroll log:\n%s\nwant the count clamped to %d", got, tmux.MaxScrollLines)
	}
	if err := svc.Scroll("lola-nori-1", 0); err != nil {
		t.Fatalf("Scroll(0): %v", err)
	}
	if strings.Count(tmuxLog(t, logPath), "copy-mode") != 1 {
		t.Errorf("a zero scroll must not run tmux:\n%s", tmuxLog(t, logPath))
	}
	if err := svc.Scroll("", 3); err == nil {
		t.Error("empty session name must error")
	}
}

// Typing snaps back to the live view. A pane left in copy mode reads keys as
// copy-mode COMMANDS, so without this the first keystroke after a scroll would
// vanish instead of reaching the agent — and it costs one exec, only once per
// scroll (the flag is consumed).
func TestWriteLeavesCopyModeOnceAfterAScroll(t *testing.T) {
	bin, logPath := fakeTmux(t, false)
	s := &ptyStream{}
	svc := &TermService{tmuxBin: bin, streams: map[string]*ptyStream{"lola-nori-1": s}}
	if err := svc.Scroll("lola-nori-1", 5); err != nil {
		t.Fatalf("Scroll: %v", err)
	}
	if !s.scrolled {
		t.Fatal("Scroll must record that the pane is in copy mode")
	}
	// Write needs a real fd to write into; a pipe stands in for the PTY.
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()
	s.f = w
	go func() { io.Copy(io.Discard, r) }()

	if err := svc.Write("lola-nori-1", "hello"); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := svc.Write("lola-nori-1", " again"); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if got := strings.Count(tmuxLog(t, logPath), "copy-mode -q"); got != 1 {
		t.Errorf("copy-mode -q ran %d times, want exactly 1:\n%s", got, tmuxLog(t, logPath))
	}
}

// A shell tab can be the session that starts a COLD tmux server (its agent pane
// died and took the server with it), so it applies lola's pane-history default
// the same way the CLI does — before the create, and again afterwards when there
// was no server to set it on yet.
func TestShellAppliesTheScrollDefault(t *testing.T) {
	t.Setenv("LOLA_HOME", t.TempDir()) // no config.toml -> lola's own default
	bin, logPath := fakeTmux(t, false)
	svc := &TermService{tmuxBin: bin, streams: map[string]*ptyStream{}}
	if _, err := svc.Shell("NORI-1-shell-1", t.TempDir()); err != nil {
		t.Fatalf("Shell: %v", err)
	}
	log := tmuxLog(t, logPath)
	opt := strings.Index(log, "set-option -g history-limit 10000")
	create := strings.Index(log, "new-session")
	if opt < 0 || create < 0 || opt > create {
		t.Errorf("history-limit must be set before the create:\n%s", log)
	}
	// Mouse mode comes from [tmux].mouse in both states, exactly as the CLI writes
	// it, so the two surfaces cannot leave the server in different modes.
	if !strings.Contains(log, "set-option -g mouse off") {
		t.Errorf("mouse must be written from config, not left to ~/.tmux.conf:\n%s", log)
	}
	// The server answered, so there is nothing to retry.
	if got := strings.Count(log, "history-limit"); got != 1 {
		t.Errorf("history-limit set %d times on a live server, want 1:\n%s", got, log)
	}
}

func TestShellRetriesTheScrollDefaultOnAColdServer(t *testing.T) {
	t.Setenv("LOLA_HOME", t.TempDir())
	dir := t.TempDir()
	bin := filepath.Join(dir, "tmux")
	logPath := filepath.Join(dir, "args.log")
	started := filepath.Join(dir, "started")
	// Stands in for tmux: has-session fails (so Shell creates), and set-option
	// fails until a session exists.
	script := "#!/bin/sh\necho \"$@\" >> " + logPath + "\n" +
		"case \"$3\" in\n" +
		"  has-session) exit 1 ;;\n" +
		"  set-option) [ -f " + started + " ] || exit 1 ;;\n" +
		"  new-session) touch " + started + " ;;\n" +
		"esac\nexit 0\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	svc := &TermService{tmuxBin: bin, streams: map[string]*ptyStream{}}
	if _, err := svc.Shell("NORI-1-shell-1", t.TempDir()); err != nil {
		t.Fatalf("Shell: %v", err)
	}
	if got := strings.Count(tmuxLog(t, logPath), "history-limit"); got != 2 {
		t.Errorf("history-limit set %d times on a cold server, want 2:\n%s", got, tmuxLog(t, logPath))
	}
}

// The keystroke-cancels-copy-mode handshake has to survive a keystroke that
// arrives WHILE the scroll is still running: Wails dispatches each webview call
// on its own goroutine, so without a lock held across the copy-mode exec the
// Write would consume the flag before the pane was in copy mode — and nothing
// would ever cancel it again.
func TestWriteWaitsForAnInFlightScroll(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "tmux")
	logPath := filepath.Join(dir, "args.log")
	// Entering copy mode is slow; cancelling is not.
	script := "#!/bin/sh\ncase \"$3\" in copy-mode) case \"$4\" in -e) sleep 1;; esac;; esac\n" +
		"echo \"$@\" >> " + logPath + "\nexit 0\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()
	go func() { io.Copy(io.Discard, r) }()

	s := &ptyStream{f: w}
	svc := &TermService{tmuxBin: bin, streams: map[string]*ptyStream{"lola-nori-1": s}}
	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := svc.Scroll("lola-nori-1", 5); err != nil {
			t.Errorf("Scroll: %v", err)
		}
	}()
	time.Sleep(100 * time.Millisecond) // land the keystroke mid-scroll
	if err := svc.Write("lola-nori-1", "x"); err != nil {
		t.Fatalf("Write: %v", err)
	}
	<-done

	log := tmuxLog(t, logPath)
	enter := strings.Index(log, "copy-mode -e")
	leave := strings.Index(log, "copy-mode -q")
	if enter < 0 || leave < 0 || enter > leave {
		t.Errorf("the keystroke must leave copy mode AFTER the scroll entered it:\n%s", log)
	}
}
