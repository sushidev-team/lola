package tmux

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeTmux installs a shell script standing in for the tmux binary: it
// appends its argv (one line per invocation) to <dir>/args.log, emits the
// canned stdout/stderr, and exits with code. Pattern mirrors
// internal/ao/client_test.go fakeBin; no real tmux is ever run.
func fakeTmux(t *testing.T, stdout, stderr string, code int) (bin, argsLog string) {
	t.Helper()
	dir := t.TempDir()
	bin = filepath.Join(dir, "tmux")
	argsLog = filepath.Join(dir, "args.log")
	var b strings.Builder
	b.WriteString("#!/bin/sh\n")
	b.WriteString("echo \"$@\" >> " + argsLog + "\n")
	if stdout != "" {
		b.WriteString("cat <<'EOF'\n" + stdout + "\nEOF\n")
	}
	if stderr != "" {
		b.WriteString("cat <<'EOF' >&2\n" + stderr + "\nEOF\n")
	}
	fmt.Fprintf(&b, "exit %d\n", code)
	if err := os.WriteFile(bin, []byte(b.String()), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin, argsLog
}

func loggedArgs(t *testing.T, argsLog string) string {
	t.Helper()
	b, err := os.ReadFile(argsLog)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimRight(string(b), "\n")
}

func TestListSessionsParsesFormatLines(t *testing.T) {
	fixture := "main\t1720000000\t1\nlola-NORI-12-1\t1720003600\t0"
	bin, argsLog := fakeTmux(t, fixture, "", 0)
	c := &Client{Bin: bin}

	got, err := c.ListSessions(context.Background())
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	want := []Session{
		{Name: "main", Created: time.Unix(1720000000, 0), Attached: true},
		{Name: "lola-NORI-12-1", Created: time.Unix(1720003600, 0), Attached: false},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d sessions, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if !got[i].Created.Equal(want[i].Created) || got[i].Name != want[i].Name || got[i].Attached != want[i].Attached {
			t.Errorf("session[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
	wantArgs := "ls -F #{session_name}\t#{session_created}\t#{session_attached}"
	if args := loggedArgs(t, argsLog); args != wantArgs {
		t.Errorf("invoked %q, want %q", args, wantArgs)
	}
}

func TestListSessionsNoServerIsEmptyNotError(t *testing.T) {
	bin, _ := fakeTmux(t, "", "no server running on /private/tmp/tmux-501/default", 1)
	c := &Client{Bin: bin}

	got, err := c.ListSessions(context.Background())
	if err != nil {
		t.Fatalf("ListSessions with no server: want nil error, got %v", err)
	}
	if got == nil || len(got) != 0 {
		t.Errorf("want empty non-nil slice, got %#v", got)
	}
}

func TestListSessionsOtherFailureWrapsStderr(t *testing.T) {
	bin, _ := fakeTmux(t, "", "error connecting to /private/tmp/tmux-501/default (Permission denied)", 1)
	c := &Client{Bin: bin}

	_, err := c.ListSessions(context.Background())
	if err == nil {
		t.Fatal("want error for non-no-server failure, got nil")
	}
	if !strings.Contains(err.Error(), "Permission denied") {
		t.Errorf("error %q does not wrap stderr", err)
	}
}

func TestHasUsesExactMatchTarget(t *testing.T) {
	bin, argsLog := fakeTmux(t, "", "", 0)
	c := &Client{Bin: bin}

	if !c.Has(context.Background(), "lola-NORI-12-1") {
		t.Error("Has: want true on exit 0")
	}
	if args := loggedArgs(t, argsLog); args != "has-session -t =lola-NORI-12-1" {
		t.Errorf("invoked %q, want exact-match target =lola-NORI-12-1", args)
	}
}

func TestHasMissingSession(t *testing.T) {
	bin, _ := fakeTmux(t, "", "can't find session: =nope", 1)
	c := &Client{Bin: bin}

	if c.Has(context.Background(), "nope") {
		t.Error("Has: want false on exit 1")
	}
}

func TestCapturePaneArgsAndOutput(t *testing.T) {
	fixture := "\x1b[32m$ make test\x1b[0m\nok"
	bin, argsLog := fakeTmux(t, fixture, "", 0)
	c := &Client{Bin: bin}

	out, err := c.CapturePane(context.Background(), "main", 200)
	if err != nil {
		t.Fatalf("CapturePane: %v", err)
	}
	if out != fixture+"\n" {
		t.Errorf("CapturePane output %q, want rendered screen incl. ANSI %q", out, fixture+"\n")
	}
	if args := loggedArgs(t, argsLog); args != "capture-pane -p -e -t =main -S -200" {
		t.Errorf("invoked %q, want capture-pane -p -e -t =main -S -200", args)
	}
}

func TestSendKeysLiteralThenEnter(t *testing.T) {
	bin, argsLog := fakeTmux(t, "", "", 0)
	c := &Client{Bin: bin}

	if err := c.SendKeys(context.Background(), "main", "fix the CI failure"); err != nil {
		t.Fatalf("SendKeys: %v", err)
	}
	want := "send-keys -t =main -l fix the CI failure\nsend-keys -t =main Enter"
	if args := loggedArgs(t, argsLog); args != want {
		t.Errorf("invoked:\n%s\nwant literal text then Enter:\n%s", args, want)
	}
}

func TestSendKeysErrorWrapsStderr(t *testing.T) {
	bin, _ := fakeTmux(t, "", "can't find session: =gone", 1)
	c := &Client{Bin: bin}

	err := c.SendKeys(context.Background(), "gone", "hello")
	if err == nil || !strings.Contains(err.Error(), "can't find session") {
		t.Errorf("want error wrapping stderr, got %v", err)
	}
}

func TestNewSessionArgs(t *testing.T) {
	bin, argsLog := fakeTmux(t, "", "", 0)
	c := &Client{Bin: bin}

	if err := c.NewSession(context.Background(), "lola-NORI-12-1", "/work/nori", "claude"); err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	want := "new-session -d -s lola-NORI-12-1 -c /work/nori claude"
	if args := loggedArgs(t, argsLog); args != want {
		t.Errorf("invoked %q, want %q", args, want)
	}
}

func TestNewSessionOmitsEmptyCommand(t *testing.T) {
	bin, argsLog := fakeTmux(t, "", "", 0)
	c := &Client{Bin: bin}

	if err := c.NewSession(context.Background(), "s1", "/work", ""); err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	want := "new-session -d -s s1 -c /work"
	if args := loggedArgs(t, argsLog); args != want {
		t.Errorf("invoked %q, want default-shell form %q", args, want)
	}
}

func TestKillSessionArgs(t *testing.T) {
	bin, argsLog := fakeTmux(t, "", "", 0)
	c := &Client{Bin: bin}

	if err := c.KillSession(context.Background(), "lola-NORI-12-1"); err != nil {
		t.Fatalf("KillSession: %v", err)
	}
	if args := loggedArgs(t, argsLog); args != "kill-session -t =lola-NORI-12-1" {
		t.Errorf("invoked %q, want exact-match kill-session target", args)
	}
}

func TestKillSessionMissingIsError(t *testing.T) {
	bin, _ := fakeTmux(t, "", "can't find session: =gone", 1)
	c := &Client{Bin: bin}

	err := c.KillSession(context.Background(), "gone")
	if err == nil || !strings.Contains(err.Error(), "can't find session") {
		t.Errorf("want error wrapping stderr for missing session, got %v", err)
	}
}

func TestAttachArgs(t *testing.T) {
	c := &Client{Bin: "tmux"}
	got := c.AttachArgs("main")
	want := []string{"tmux", "attach-session", "-t", "=main"}
	if len(got) != len(want) {
		t.Fatalf("AttachArgs = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("AttachArgs = %v, want %v", got, want)
		}
	}

	abs := &Client{Bin: "/opt/homebrew/bin/tmux"}
	if args := abs.AttachArgs("main"); args[0] != "/opt/homebrew/bin/tmux" {
		t.Errorf("AttachArgs[0] = %q, want configured absolute bin", args[0])
	}
}

func TestAvailable(t *testing.T) {
	bin, _ := fakeTmux(t, "", "", 0)

	if !(&Client{Bin: bin}).Available() {
		t.Error("Available: want true for existing executable (absolute path)")
	}
	if (&Client{Bin: filepath.Join(t.TempDir(), "missing")}).Available() {
		t.Error("Available: want false for missing binary")
	}

	// A bare name is resolved via PATH.
	t.Setenv("PATH", filepath.Dir(bin))
	if !(&Client{Bin: "tmux"}).Available() {
		t.Error("Available: want true for bare name resolved via PATH")
	}
}
