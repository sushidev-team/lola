package tmux

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// pasteTmux is a stand-in that logs its argv, saves whatever it reads on stdin for
// load-buffer, answers the bracketed-paste flag, and fails the subcommand named by fail.
func pasteTmux(t *testing.T, flag, fail string) (bin, argsLog, stdin string) {
	t.Helper()
	dir := t.TempDir()
	bin = filepath.Join(dir, "tmux")
	argsLog = filepath.Join(dir, "args.log")
	stdin = filepath.Join(dir, "stdin")
	script := fmt.Sprintf(`#!/bin/sh
echo "$@" >> %q
case "$3" in
  load-buffer) cat > %q ;;
  display-message) echo %q ;;
esac
case "$3" in %s) echo nope >&2; exit 1 ;; esac
exit 0
`, argsLog, stdin, flag, fail)
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin, argsLog, stdin
}

// bufferName pulls the generated buffer name out of the load-buffer call.
func bufferName(t *testing.T, args string) string {
	t.Helper()
	for _, line := range strings.Split(args, "\n") {
		if f := strings.Fields(line); len(f) > 4 && f[2] == "load-buffer" {
			return f[4]
		}
	}
	t.Fatalf("no load-buffer in:\n%s", args)
	return ""
}

// The 800-byte line is where the damage starts on the far side, so both sides of it are
// pinned, and the text must never reach argv once it is a paste.
func TestSendKeysPastesOver800(t *testing.T) {
	bin, argsLog, _ := pasteTmux(t, "1", "none")
	at := strings.Repeat("x", 800)
	if err := (&Client{Bin: bin}).SendKeys(context.Background(), "main", at); err != nil {
		t.Fatal(err)
	}
	if args := loggedArgs(t, argsLog); !strings.Contains(args, "send-keys -t =main: -l -- "+at) || strings.Contains(args, "paste-buffer") {
		t.Errorf("800 bytes must still be typed:\n%s", args)
	}

	bin, argsLog, stdin := pasteTmux(t, "1", "none")
	over := "first line\n" + strings.Repeat("y", 790)
	started := time.Now()
	if err := (&Client{Bin: bin}).SendKeys(context.Background(), "main", over); err != nil {
		t.Fatal(err)
	}
	args := loggedArgs(t, argsLog)
	if strings.Contains(args, "yyyy") {
		t.Errorf("a pasted message reached argv:\n%s", args)
	}
	buf := bufferName(t, args)
	if !strings.HasPrefix(buf, "lola-send-") {
		t.Errorf("buffer = %q", buf)
	}
	want := "-L lola copy-mode -q -t =main:\n" +
		"-L lola load-buffer -b " + buf + " -\n" +
		"-L lola display-message -p -t =main: #{bracket_paste_flag}\n" +
		"-L lola paste-buffer -p -d -b " + buf + " -t =main:\n" +
		"-L lola send-keys -t =main: Enter"
	if args != want {
		t.Errorf("invoked:\n%s\nwant:\n%s", args, want)
	}
	raw, err := os.ReadFile(stdin)
	if err != nil || string(raw) != over {
		t.Errorf("stdin = %q, %v", raw, err)
	}
	if time.Since(started) < pasteSettleDelay {
		t.Error("Enter must wait for the paste to settle")
	}
}

// Outside bracketed paste a pasted CR is Enter, so each line would submit by itself. The
// flag being anything but a plain 1 keeps LF, including an older tmux that prints nothing.
func TestSendKeysKeepsLFWithoutBracketedPaste(t *testing.T) {
	for _, flag := range []string{"0", ""} {
		bin, argsLog, _ := pasteTmux(t, flag, "none")
		if err := (&Client{Bin: bin}).SendKeys(context.Background(), "main", strings.Repeat("z", 801)); err != nil {
			t.Fatal(err)
		}
		args := loggedArgs(t, argsLog)
		if want := "paste-buffer -p -r -d -b " + bufferName(t, args); !strings.Contains(args, want) {
			t.Errorf("flag %q: want %q in:\n%s", flag, want, args)
		}
	}
	bin, argsLog, _ := pasteTmux(t, "1", "display-message")
	if err := (&Client{Bin: bin}).SendKeys(context.Background(), "main", strings.Repeat("z", 801)); err != nil {
		t.Fatal(err)
	}
	if args := loggedArgs(t, argsLog); !strings.Contains(args, "paste-buffer -p -r -d") {
		t.Errorf("an unanswered flag must keep LF:\n%s", args)
	}
}

// The buffer holds the whole message, and tmux keeps it until the server exits unless
// something removes it. A failed paste must also not press Enter on whatever is there.
func TestSendKeysDeletesBufferOnFailure(t *testing.T) {
	for _, fail := range []string{"paste-buffer", "load-buffer"} {
		t.Run(fail, func(t *testing.T) {
			bin, argsLog, _ := pasteTmux(t, "1", fail)
			text := strings.Repeat("s", 900)
			err := (&Client{Bin: bin}).SendKeys(context.Background(), "main", text)
			if err == nil {
				t.Fatal("a failed paste must be an error")
			}
			if strings.Contains(err.Error(), "sss") {
				t.Errorf("the message leaked into the error: %v", err)
			}
			args := loggedArgs(t, argsLog)
			if !strings.Contains(args, "delete-buffer -b "+bufferName(t, args)) {
				t.Errorf("buffer left behind:\n%s", args)
			}
			if strings.Contains(args, "Enter") {
				t.Errorf("Enter after a failed paste:\n%s", args)
			}
		})
	}
}

// A context that ended is exactly when the paste fails, and the delete must still run.
func TestSendKeysDeletesBufferWhenTheContextEnded(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "tmux")
	argsLog := filepath.Join(dir, "args.log")
	marker := filepath.Join(dir, "cancel")
	script := fmt.Sprintf(`#!/bin/sh
echo "$@" >> %q
case "$3" in
  load-buffer) cat > /dev/null ;;
  paste-buffer) : > %q; exit 1 ;;
esac
exit 0
`, argsLog, marker)
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// The context ends the moment the paste fails. The marker file is the only signal the
	// stand-in can give, so poll it.
	go func() {
		for {
			if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
				cancel()
				return
			}
			time.Sleep(time.Millisecond)
		}
	}()
	if err := (&Client{Bin: bin}).SendKeys(ctx, "main", strings.Repeat("c", 900)); err == nil {
		t.Fatal("want an error")
	}
	if args := loggedArgs(t, argsLog); !strings.Contains(args, "delete-buffer") {
		t.Errorf("no delete:\n%s", args)
	}
}

// SendKey exists for a choice on a card: one press, no Enter, and copy mode left first
// because a digit sent to a scrolled-back pane is a copy-mode command.
func TestSendKeyIsOneKeyNoEnter(t *testing.T) {
	bin, argsLog := fakeTmux(t, "", "", 0)
	if err := (&Client{Bin: bin}).SendKey(context.Background(), "main", "3"); err != nil {
		t.Fatal(err)
	}
	args := loggedArgs(t, argsLog)
	lines := strings.Split(args, "\n")
	if len(lines) != 2 || !strings.HasPrefix(lines[0], "-L lola copy-mode") || lines[1] != "-L lola send-keys -t =main: -l 3" {
		t.Errorf("invoked:\n%s", args)
	}
	if strings.Contains(args, "Enter") {
		t.Errorf("a choice must not press Enter:\n%s", args)
	}
}

// tmux reads a payload starting with - as flags and a trailing ; as its own separator. Both
// are ordinary things for a person to write, so both are pinned against a real tmux on a
// private socket.
func TestSendKeysLiteralAgainstRealTmux(t *testing.T) {
	bin, err := exec.LookPath("tmux")
	if err != nil {
		t.Skip("no tmux")
	}
	dir := t.TempDir()
	out := filepath.Join(dir, "out")
	socket := fmt.Sprintf("lola-literal-test-%d", os.Getpid())
	ctx := context.Background()
	c := &Client{Bin: bin, SocketName: socket}
	defer func() { _ = exec.Command(bin, "-L", socket, "kill-server").Run() }()
	if err := exec.Command(bin, "-f", "/dev/null", "-L", socket, "new-session", "-d", "-s", "t", "-x", "200", "-y", "20",
		"stty -echo -icanon min 1 -icrnl; cat > "+out).Run(); err != nil {
		t.Skipf("no private tmux server: %v", err)
	}

	cases := []struct{ say, want string }{
		{"- item one", "- item one"},
		{"-foo", "-foo"},
		{"--", "--"},
		{"ends with semi;", "ends with semi;"},
		{";", ";"},
		{`keeps its slash\;`, `keeps its slash\;`},
		{"plain", "plain"},
	}
	var want strings.Builder
	for _, tc := range cases {
		if err := c.SendKeys(ctx, "t", tc.say); err != nil {
			t.Errorf("SendKeys(%q): %v", tc.say, err)
		}
		want.WriteString(tc.want + "\r")
	}
	if err := c.SendText(ctx, "t", "-from send text;", false); err != nil {
		t.Errorf("SendText: %v", err)
	}
	want.WriteString("\x15-from send text;") // C-u, then the text
	deadline := time.Now().Add(3 * time.Second)
	var got []byte
	for time.Now().Before(deadline) {
		got, _ = os.ReadFile(out)
		if string(got) == want.String() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Errorf("the pane received %q, want %q", got, want.String())
}
