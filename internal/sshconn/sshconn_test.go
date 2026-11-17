package sshconn

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func fakeSSH(t *testing.T, script string) Client {
	t.Helper()
	p := filepath.Join(t.TempDir(), "ssh")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+script+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return Client{Bin: p}
}

func TestCommandArgsFailClosedAndOneRemoteArgument(t *testing.T) {
	remote := RemoteCommand("/var/log", "ls; echo 'x'")
	a := CommandArgs(Target{Sock: "/s", Alias: "web", User: "ro"}, remote)
	for _, want := range []string{"ControlMaster=no", "BatchMode=yes", "ProxyCommand=false", "RequestTTY=no", "ClearAllForwardings=yes"} {
		if !slices.Contains(a, want) {
			t.Errorf("argv misses %s: %v", want, a)
		}
	}
	if a[len(a)-1] != remote || a[len(a)-2] != "web" || a[len(a)-3] != "-T" {
		t.Fatalf("alias then one remote argument expected, got %v", a[len(a)-3:])
	}
	if !strings.Contains(strings.Join(a, " "), "-l ro") {
		t.Fatalf("user missing: %v", a)
	}
}

func TestRemoteCommandQuotesCwdAndBody(t *testing.T) {
	got := RemoteCommand("/tmp/a b'c", "echo hi")
	out, err := exec.Command("sh", "-c", "printf %s "+ShQuote(got)).Output()
	if err != nil || string(out) != got {
		t.Fatalf("quote round trip: %q %v", out, err)
	}
	if !strings.HasPrefix(got, "sh -c ") {
		t.Fatalf("not an explicit sh: %s", got)
	}
	dir := t.TempDir()
	sub := filepath.Join(dir, "a b'c")
	if err := os.Mkdir(sub, 0o700); err != nil {
		t.Fatal(err)
	}
	out, err = exec.Command("sh", "-c", RemoteCommand(sub, "pwd")).Output()
	if err != nil {
		t.Fatal(err)
	}
	if resolved, _ := filepath.EvalSymlinks(sub); strings.TrimSpace(string(out)) != resolved && strings.TrimSpace(string(out)) != sub {
		t.Fatalf("pwd = %q", out)
	}
	// A hostile cwd stays a cd argument.
	out, _ = exec.Command("sh", "-c", RemoteCommand("/nonexistent; echo pwned", "echo ran")).Output()
	if len(out) != 0 {
		t.Fatalf("cwd escaped: %s", out)
	}
}

func TestSocketPathShortAndVerified(t *testing.T) {
	home := t.TempDir()
	// macOS temp dirs are long; force the normal branch with a short home if we can.
	short, err := os.MkdirTemp("/tmp", "lolaz")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(short) })
	p, err := socketPath(short, "c0123456789ab", filepath.Join(home, "fb"))
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(p) != filepath.Join(short, "ssh") {
		t.Fatalf("path = %s", p)
	}
	fi, err := os.Stat(filepath.Dir(p))
	if err != nil || fi.Mode().Perm() != 0o700 {
		t.Fatalf("dir mode: %v %v", fi.Mode(), err)
	}
}

func TestSocketPathFallsBackForLongHomeOrPercent(t *testing.T) {
	short, err := os.MkdirTemp("/tmp", "lolaz")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(short) })
	fb := filepath.Join(short, "fb")
	for _, home := range []string{"/x/" + strings.Repeat("h", 100), "/tmp/a%hb"} {
		p, err := socketPath(home, "c0123456789ab", fb)
		if err != nil {
			t.Fatal(err)
		}
		if filepath.Dir(p) != fb {
			t.Fatalf("home %s: path = %s", home, p)
		}
		if len(p) > MaxSocketPath {
			t.Fatalf("too long: %s", p)
		}
	}
}

func TestSocketPathRefusesUnverifiedFallback(t *testing.T) {
	short, err := os.MkdirTemp("/tmp", "lolaz")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(short) })
	long := "/x/" + strings.Repeat("h", 100)
	open := filepath.Join(short, "open")
	if err := os.Mkdir(open, 0o777); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(open, 0o777); err != nil {
		t.Fatal(err)
	}
	if _, err := socketPath(long, "c0123456789ab", open); err == nil {
		t.Fatal("a world-writable fallback was accepted")
	}
	link := filepath.Join(short, "link")
	safe := filepath.Join(short, "safe")
	if err := os.Mkdir(safe, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(safe, link); err != nil {
		t.Fatal(err)
	}
	if _, err := socketPath(long, "c0123456789ab", link); err == nil {
		t.Fatal("a symlink fallback was accepted")
	}
	if _, err := socketPath(short, "../evil", open); err == nil {
		t.Fatal("a path-shaped connection id was accepted")
	}
}

func TestTargetRefusesOptionShapedAlias(t *testing.T) {
	for _, tg := range []Target{{Alias: "-oProxyCommand=x"}, {Alias: "a b"}, {Alias: "user@host"}, {Alias: "ok", User: "-x"}} {
		if tg.Valid() == nil {
			t.Errorf("%+v accepted", tg)
		}
	}
	if _, err := (Client{Bin: "/nonexistent"}).Run(context.Background(), Target{Alias: "-oX"}, "x", nil, nil); err == nil {
		t.Fatal("Run accepted an option-shaped alias")
	}
}

func TestRunRealExitAndSeparateStreams(t *testing.T) {
	c := fakeSSH(t, `echo out; echo err >&2; exit 7`)
	var o, e bytes.Buffer
	code, err := c.Run(context.Background(), Target{Sock: "/s", Alias: "a"}, "x", &o, &e)
	if err != nil || code != 7 || o.String() != "out\n" || e.String() != "err\n" {
		t.Fatalf("code=%d err=%v o=%q e=%q", code, err, o.String(), e.String())
	}
}

func TestRunCancelKillsWholeGroup(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "child")
	c := fakeSSH(t, `(sleep 30; touch `+marker+`) & wait`)
	ctx, cancel := context.WithCancelCause(context.Background())
	stop := errors.New("stopped")
	done := make(chan struct{})
	var err error
	go func() {
		_, err = c.Run(ctx, Target{Sock: "/s", Alias: "a"}, "x", &bytes.Buffer{}, &bytes.Buffer{})
		close(done)
	}()
	time.Sleep(200 * time.Millisecond)
	cancel(stop)
	select {
	case <-done:
	case <-time.After(6 * time.Second):
		t.Fatal("Run did not return after cancel")
	}
	if !errors.Is(err, stop) {
		t.Fatalf("cause = %v", err)
	}
}

func TestCheckStates(t *testing.T) {
	up := fakeSSH(t, `exit 0`)
	if err := up.Check(context.Background(), Target{Sock: "/s", Alias: "a"}); err != nil {
		t.Fatal(err)
	}
	down := fakeSSH(t, `exit 255`)
	if err := down.Check(context.Background(), Target{Sock: "/s", Alias: "a"}); err == nil || errors.Is(err, ErrUndetermined) {
		t.Fatalf("down reads %v", err)
	}
}

func TestResolveParsesG(t *testing.T) {
	c := fakeSSH(t, `printf 'user deploy\nhostname 10.0.0.1\nport 22\nproxyjump none\n'`)
	r, err := c.Resolve(context.Background(), "web", "")
	if err != nil || r.User != "deploy" || r.Hostname != "10.0.0.1" || r.Port != "22" || r.ProxyJump != "" {
		t.Fatalf("%+v %v", r, err)
	}
}

// A failed cd ends the shell, whatever the command's list looks like: an approval
// that named a directory must not run in the login directory instead.
func TestRemoteCommandFailedCdRunsNothing(t *testing.T) {
	for _, body := range []string{"echo A; echo B-ran", "false || echo B-ran", "echo A & echo B-ran\nwait", "echo A\necho B-ran"} {
		cmd := exec.Command("sh", "-c", RemoteCommand("/nonexistent-lola-x", body))
		out, err := cmd.Output()
		if strings.Contains(string(out), "ran") || strings.Contains(string(out), "A") {
			t.Errorf("%q ran after a failed cd: %q", body, out)
		}
		var ee *exec.ExitError
		if !errors.As(err, &ee) || ee.ExitCode() != 1 {
			t.Errorf("%q: exit = %v, want 1", body, err)
		}
	}
	// And a good cd still applies to every statement.
	dir := t.TempDir()
	out, err := exec.Command("sh", "-c", RemoteCommand(dir, "true; pwd")).Output()
	want, _ := filepath.EvalSymlinks(dir)
	if err != nil || (strings.TrimSpace(string(out)) != dir && strings.TrimSpace(string(out)) != want) {
		t.Fatalf("pwd = %q %v", out, err)
	}
}
