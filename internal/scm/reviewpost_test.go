package scm

// Tests for the gh WRITE path (reviewpost.go): the `github` transport's
// PostPRComment and the memoized AuthedLogin. A dedicated fake gh captures BOTH
// argv (args.log) and STDIN (stdin.log) so we can prove the untrusted body
// travels on stdin, never as a process argument.

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// fakeGhStdin installs a gh stand-in that logs its argv to <dir>/args.log,
// slurps stdin to <dir>/stdin.log, emits `stderr` on fd 2, and exits `code`.
func fakeGhStdin(t *testing.T, code int, stderr string) (bin, argsLog, stdinLog string) {
	t.Helper()
	dir := t.TempDir()
	bin = filepath.Join(dir, "gh")
	argsLog = filepath.Join(dir, "args.log")
	stdinLog = filepath.Join(dir, "stdin.log")
	errFile := filepath.Join(dir, "stderr")
	if err := os.WriteFile(errFile, []byte(stderr), 0o644); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\n" +
		"echo \"$@\" >> \"" + argsLog + "\"\n" +
		"cat > \"" + stdinLog + "\"\n" +
		"cat \"" + errFile + "\" >&2\n" +
		"exit " + strconv.Itoa(code) + "\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin, argsLog, stdinLog
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func TestPostPRCommentArgvAndStdin(t *testing.T) {
	bin, argsLog, stdinLog := fakeGhStdin(t, 0, "")
	c := &Client{GhBin: bin}

	body := "Review findings\n- issue one\n- issue two"
	if err := c.PostPRComment(context.Background(), "acme/nori", 42, body); err != nil {
		t.Fatalf("PostPRComment: %v", err)
	}

	// Exact argv: a plain COMMENT (never `pr review`), body sourced from stdin.
	args := loggedArgs(t, argsLog)
	if want := "pr comment 42 --repo acme/nori --body-file -"; args != want {
		t.Errorf("argv = %q, want %q", args, want)
	}
	if strings.Contains(args, "review") {
		t.Errorf("must use `pr comment`, never `pr review`: %q", args)
	}
	// Body arrived on STDIN, verbatim.
	if got := readFile(t, stdinLog); got != body {
		t.Errorf("stdin = %q, want %q", got, body)
	}
	// The body text must NOT appear in argv (no injection / ARG_MAX surface).
	if strings.Contains(args, "issue one") {
		t.Errorf("body leaked into argv: %q", args)
	}
}

func TestPostPRCommentEmptyBodySkips(t *testing.T) {
	bin, argsLog, _ := fakeGhStdin(t, 0, "")
	c := &Client{GhBin: bin}

	for _, body := range []string{"", "   \n\t "} {
		if err := c.PostPRComment(context.Background(), "acme/nori", 42, body); err != nil {
			t.Fatalf("empty body must be a no-op, got %v", err)
		}
	}
	// No exec: gh was never invoked, so args.log does not exist.
	if fileExists(argsLog) {
		t.Errorf("empty body must NOT exec gh; args.log = %q", readFile(t, argsLog))
	}
}

func TestPostPRCommentGhErrorIsDistinct(t *testing.T) {
	bin, _, _ := fakeGhStdin(t, 1, "gh: GraphQL: Resource not accessible by integration")
	c := &Client{GhBin: bin}

	err := c.PostPRComment(context.Background(), "acme/nori", 42, "some findings")
	if err == nil {
		t.Fatal("a gh failure must surface as an error")
	}
	// The wrapped error names the command and carries gh's stderr so the caller
	// can classify permanent vs transient.
	if !strings.Contains(err.Error(), "pr comment 42") ||
		!strings.Contains(err.Error(), "Resource not accessible") {
		t.Errorf("error should surface the command + gh stderr: %v", err)
	}
}

func TestPostPRCommentSecretStaysOutOfArgvAndError(t *testing.T) {
	// The body carries a credential-shaped token. Because it rides stdin, it must
	// never reach argv (args.log) nor the surfaced error — gh reads it from stdin
	// and does not echo it, so ghError (stderr-only) can't leak it.
	secret := "ANTHROPIC_API_KEY=sk-ant-supersecretvalue123456"
	bin, argsLog, stdinLog := fakeGhStdin(t, 1, "gh: permission denied")
	c := &Client{GhBin: bin}

	err := c.PostPRComment(context.Background(), "acme/nori", 7, "leaked? "+secret)
	if err == nil {
		t.Fatal("expected an error from the failing gh")
	}
	if strings.Contains(err.Error(), "supersecretvalue") {
		t.Errorf("secret leaked into the error: %v", err)
	}
	if strings.Contains(readFile(t, argsLog), "supersecretvalue") {
		t.Errorf("secret leaked into argv/log")
	}
	// It did land on stdin (that's where it is meant to go).
	if !strings.Contains(readFile(t, stdinLog), "supersecretvalue") {
		t.Errorf("body should have been delivered on stdin")
	}
}

func TestPostPRCommentSizeBound(t *testing.T) {
	huge := strings.Repeat("x", postCommentMaxBytes*2)
	bin, _, stdinLog := fakeGhStdin(t, 0, "")
	c := &Client{GhBin: bin}

	if err := c.PostPRComment(context.Background(), "acme/nori", 42, huge); err != nil {
		t.Fatalf("PostPRComment: %v", err)
	}
	if got := readFile(t, stdinLog); len(got) > postCommentMaxBytes {
		t.Errorf("posted body = %d bytes, want <= %d", len(got), postCommentMaxBytes)
	}
}

func TestAuthedLoginMemoized(t *testing.T) {
	// gh api user is invoked at most once, even across repeated calls.
	bin, argsLog := fakeGh(t, "octolola", 0)
	c := &Client{GhBin: bin}

	for range 3 {
		login, err := c.AuthedLogin(context.Background())
		if err != nil {
			t.Fatalf("AuthedLogin: %v", err)
		}
		if login != "octolola" {
			t.Errorf("login = %q, want %q", login, "octolola")
		}
	}
	args := loggedArgs(t, argsLog)
	if args != "api user --jq .login" {
		t.Errorf("argv = %q, want %q", args, "api user --jq .login")
	}
	if strings.Count(readFile(t, argsLog), "\n") != 1 {
		t.Errorf("gh api user must exec exactly once, got:\n%s", readFile(t, argsLog))
	}
}

func TestAuthedLoginErrorMemoized(t *testing.T) {
	// A failing resolution is cached (returned as an error) so it is NOT retried
	// per-cycle — the caller treats it as fail-open (skip the filter).
	bin, argsLog := fakeGh(t, "", 4)
	c := &Client{GhBin: bin}

	for range 2 {
		if _, err := c.AuthedLogin(context.Background()); err == nil {
			t.Fatal("expected an error from failing gh api user")
		}
	}
	if strings.Count(readFile(t, argsLog), "\n") != 1 {
		t.Errorf("failed AuthedLogin must still exec only once, got:\n%s", readFile(t, argsLog))
	}
}
