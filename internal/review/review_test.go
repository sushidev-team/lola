package review

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// capture records what the seam received on the last Review call.
type capture struct {
	bin     string
	args    []string
	dir     string
	timeout time.Duration
	calls   int
}

// stubSeam installs a runReview replacement that records its args and returns
// (out, err). It restores the real seam on cleanup, so no test ever runs
// coderabbit.
func stubSeam(t *testing.T, out string, err error) *capture {
	t.Helper()
	orig := runReview
	t.Cleanup(func() { runReview = orig })
	c := &capture{}
	runReview = func(_ context.Context, bin string, args []string, dir string, timeout time.Duration) (string, error) {
		c.calls++
		c.bin, c.args, c.dir, c.timeout = bin, args, dir, timeout
		return out, err
	}
	return c
}

func TestReviewBuildsArgvWithBaseAndRunsInWorktree(t *testing.T) {
	cap := stubSeam(t, "some findings", nil)
	if _, err := (&Client{}).Review(context.Background(), "/work/tree", "main"); err != nil {
		t.Fatalf("Review: %v", err)
	}
	if cap.calls != 1 {
		t.Fatalf("calls = %d, want exactly 1 (no retries)", cap.calls)
	}
	want := []string{"review", "--plain", "--type", "all", "--base", "main"}
	if !equal(cap.args, want) {
		t.Fatalf("args = %v, want %v", cap.args, want)
	}
	if cap.dir != "/work/tree" {
		t.Errorf("dir = %q, want the worktree /work/tree", cap.dir)
	}
	if cap.bin != defaultBin {
		t.Errorf("bin = %q, want default %q", cap.bin, defaultBin)
	}
	if cap.timeout != defaultTimeout {
		t.Errorf("timeout = %s, want default %s", cap.timeout, defaultTimeout)
	}
}

func TestReviewHonorsConfiguredBinArgsTimeout(t *testing.T) {
	cap := stubSeam(t, "ok", nil)
	cl := &Client{Bin: "/opt/cr", Args: []string{"review", "--agent"}, Timeout: 42 * time.Second}
	if _, err := cl.Review(context.Background(), "/wt", "develop"); err != nil {
		t.Fatalf("Review: %v", err)
	}
	if cap.bin != "/opt/cr" {
		t.Errorf("bin = %q, want /opt/cr", cap.bin)
	}
	if want := []string{"review", "--agent", "--base", "develop"}; !equal(cap.args, want) {
		t.Errorf("args = %v, want %v", cap.args, want)
	}
	if cap.timeout != 42*time.Second {
		t.Errorf("timeout = %s, want 42s", cap.timeout)
	}
}

// Review must never mutate defaultArgs or a caller-supplied Args slice when it
// appends --base.
func TestReviewDoesNotMutateArgs(t *testing.T) {
	stubSeam(t, "", nil)

	// Default path: defaultArgs stays four elements across calls.
	if _, err := (&Client{}).Review(context.Background(), "/wt", "main"); err != nil {
		t.Fatalf("Review: %v", err)
	}
	if len(defaultArgs) != 4 {
		t.Fatalf("defaultArgs mutated: len = %d, want 4 (%v)", len(defaultArgs), defaultArgs)
	}

	// Caller slice with spare capacity: --base must not clobber it.
	userArgs := make([]string, 2, 8)
	userArgs[0], userArgs[1] = "review", "--plain"
	cl := &Client{Args: userArgs}
	if _, err := cl.Review(context.Background(), "/wt", "main"); err != nil {
		t.Fatalf("Review: %v", err)
	}
	if len(userArgs) != 2 || userArgs[0] != "review" || userArgs[1] != "--plain" {
		t.Fatalf("caller Args slice mutated: %v", userArgs)
	}
}

func TestReviewTrimsOutput(t *testing.T) {
	stubSeam(t, "  \n finding one\nfinding two \n\n", nil)
	got, err := (&Client{}).Review(context.Background(), "/wt", "main")
	if err != nil {
		t.Fatalf("Review: %v", err)
	}
	if want := "finding one\nfinding two"; got != want {
		t.Fatalf("output = %q, want trimmed %q", got, want)
	}
}

func TestReviewCleanReviewReturnsEmpty(t *testing.T) {
	for _, out := range []string{"", "   \n\t  \n"} {
		stubSeam(t, out, nil)
		got, err := (&Client{}).Review(context.Background(), "/wt", "main")
		if err != nil {
			t.Fatalf("clean review err = %v, want nil", err)
		}
		if got != "" {
			t.Fatalf("clean review output = %q, want empty string", got)
		}
	}
}

func TestReviewCapsOversizedOutput(t *testing.T) {
	big := strings.Repeat("A", maxOutputBytes*2)
	stubSeam(t, big, nil)
	got, err := (&Client{}).Review(context.Background(), "/wt", "main")
	if err != nil {
		t.Fatalf("Review: %v", err)
	}
	if len(got) > maxOutputBytes {
		t.Fatalf("output len = %d, want <= %d", len(got), maxOutputBytes)
	}
	if !strings.HasSuffix(got, truncMarker) {
		t.Fatalf("capped output missing marker %q: ...%q", truncMarker, tail(got, 20))
	}
	if !strings.HasPrefix(got, "AAAA") {
		t.Fatalf("head-clip should keep the original head, got %q...", got[:8])
	}
}

func TestReviewPropagatesSeamSentinels(t *testing.T) {
	for _, sentinel := range []error{ErrNotFound, ErrTimeout, ErrAuth, ErrExit} {
		stubSeam(t, "", sentinel)
		got, err := (&Client{}).Review(context.Background(), "/wt", "main")
		if !errors.Is(err, sentinel) {
			t.Errorf("err = %v, want %v", err, sentinel)
		}
		if got != "" {
			t.Errorf("output = %q, want empty on error", got)
		}
	}
}

func TestClassifyRunErr(t *testing.T) {
	// Deadline wins even when the raw error is "signal: killed".
	if e := classifyRunErr(errors.New("signal: killed"), context.DeadlineExceeded, "", 3*time.Second); !errors.Is(e, ErrTimeout) {
		t.Errorf("deadline: got %v, want ErrTimeout", e)
	}
	// Not-found via a synthetic exec.Error.
	notFound := &exec.Error{Name: "coderabbit", Err: exec.ErrNotFound}
	if e := classifyRunErr(notFound, nil, "", time.Second); !errors.Is(e, ErrNotFound) {
		t.Errorf("not-found: got %v, want ErrNotFound", e)
	}
	// Auth cue in stderr → ErrAuth, and the actionable hint is surfaced.
	authErr := classifyRunErr(&exec.ExitError{}, nil, "Error: you are not authenticated, run: coderabbit auth login", time.Second)
	if !errors.Is(authErr, ErrAuth) {
		t.Errorf("auth: got %v, want ErrAuth", authErr)
	}
	if !strings.Contains(authErr.Error(), "coderabbit auth login") {
		t.Errorf("auth error should carry the login hint, got %q", authErr.Error())
	}
	// Other nonzero exit surfaces stderr (synthetic ExitError has nil
	// ProcessState — must not panic).
	exitErr := classifyRunErr(&exec.ExitError{}, nil, "  parse error on line 3  ", time.Second)
	if !errors.Is(exitErr, ErrExit) {
		t.Errorf("exit: got %v, want ErrExit", exitErr)
	}
	if !strings.Contains(exitErr.Error(), "parse error on line 3") {
		t.Errorf("exit error should surface stderr, got %q", exitErr.Error())
	}
	// Success and generic failure.
	if e := classifyRunErr(nil, nil, "", time.Second); e != nil {
		t.Errorf("nil run error should classify to nil, got %v", e)
	}
	generic := classifyRunErr(errors.New("weird"), nil, "", time.Second)
	if generic == nil || errors.Is(generic, ErrTimeout) || errors.Is(generic, ErrNotFound) || errors.Is(generic, ErrAuth) || errors.Is(generic, ErrExit) {
		t.Errorf("generic error misclassified: %v", generic)
	}
}

// The exit path must never surface a credential, even when coderabbit's stderr
// echoes one. Auth cues are classified before the exit path, so use a
// non-auth-looking secret shape here.
func TestExitErrorNeverLeaksAKey(t *testing.T) {
	const key = "sk-ant-api03-DEADBEEFdeadbeef0123456789ABCDEF"
	stderr := "fatal: request failed using " + key + " while contacting the server"
	e := classifyRunErr(&exec.ExitError{}, nil, stderr, time.Second)
	if !errors.Is(e, ErrExit) {
		t.Fatalf("got %v, want ErrExit", e)
	}
	if strings.Contains(e.Error(), key) {
		t.Fatalf("exit error leaked the key: %q", e.Error())
	}
	if !strings.Contains(e.Error(), "[redacted]") {
		t.Fatalf("expected the key to be redacted, got %q", e.Error())
	}
}

func TestRedactSecrets(t *testing.T) {
	cases := []struct {
		in     string
		secret string // must NOT appear in the output
	}{
		{"key sk-ant-abcdef0123456789 was rejected", "sk-ant-abcdef0123456789"},
		{"Authorization: Bearer eyJhbGciOiJI.payload.sig", "eyJhbGciOiJI.payload.sig"},
		{"ANTHROPIC_API_KEY=supersecretvalue123", "supersecretvalue123"},
		{"token: 0123456789abcdef0123456789abcdef0123", "0123456789abcdef0123456789abcdef0123"},
		{"opaque abcdefghijklmnopqrstuvwxyz0123456789 blob", "abcdefghijklmnopqrstuvwxyz0123456789"},
	}
	for _, tc := range cases {
		out := redactSecrets(tc.in)
		if strings.Contains(out, tc.secret) {
			t.Errorf("redactSecrets(%q) = %q, still contains secret %q", tc.in, out, tc.secret)
		}
		if !strings.Contains(out, "[redacted]") {
			t.Errorf("redactSecrets(%q) = %q, expected a [redacted] marker", tc.in, out)
		}
	}
	// Ordinary text is left intact.
	if got := redactSecrets("undefined variable `foo` at bar.go:10"); !strings.Contains(got, "undefined variable") {
		t.Errorf("redactSecrets mangled ordinary text: %q", got)
	}
}

func TestLooksLikeAuthError(t *testing.T) {
	for _, s := range []string{
		"you are not authenticated",
		"please run coderabbit auth login",
		"HTTP 401 Unauthorized",
		"not logged in",
		"invalid credential",
	} {
		if !looksLikeAuthError(s) {
			t.Errorf("looksLikeAuthError(%q) = false, want true", s)
		}
	}
	if looksLikeAuthError("syntax error near unexpected token") {
		t.Error("looksLikeAuthError matched a non-auth message")
	}
}

func TestCapOutputRuneSafe(t *testing.T) {
	// "é" is 2 bytes; a byte-cut at maxOutputBytes-len(marker) can land
	// mid-rune. The result must still be valid UTF-8.
	in := strings.Repeat("é", maxOutputBytes) // 2*max bytes
	out := capOutput(in, maxOutputBytes)
	if !utf8.ValidString(out) {
		t.Fatal("capped output is not valid UTF-8 (cut mid-rune)")
	}
	if len(out) > maxOutputBytes {
		t.Fatalf("capped len = %d, want <= %d", len(out), maxOutputBytes)
	}
	if !strings.HasSuffix(out, truncMarker) {
		t.Fatal("capped output missing truncation marker")
	}
	body := strings.TrimSuffix(out, truncMarker)
	if !strings.HasPrefix(in, body) {
		t.Fatal("capped body is not a prefix of the original (not head-clipped)")
	}
}

func TestCapOutputLeavesSmallUnchanged(t *testing.T) {
	const small = "one small finding"
	if got := capOutput(small, maxOutputBytes); got != small {
		t.Fatalf("capOutput mangled small input: %q", got)
	}
}

func TestAvailable(t *testing.T) {
	// Missing binary → false.
	if (&Client{Bin: "lola-review-nonexistent-binary-zzz"}).Available() {
		t.Error("Available() = true for a missing binary")
	}
	// A real, executable file (path form) → true.
	dir := t.TempDir()
	bin := filepath.Join(dir, "coderabbit")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if !(&Client{Bin: bin}).Available() {
		t.Errorf("Available() = false for executable %s", bin)
	}
}

// TestRealSeamNotFound exercises the REAL runReview (seam not overridden) with a
// binary that cannot be found, proving the exec+classify path returns
// ErrNotFound without ever spawning coderabbit.
func TestRealSeamNotFound(t *testing.T) {
	cl := &Client{Bin: "lola-review-nonexistent-binary-zzz", Timeout: 2 * time.Second}
	_, err := cl.Review(context.Background(), t.TempDir(), "main")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestCappedBuffer(t *testing.T) {
	b := &cappedBuffer{cap: 4}
	n, err := b.Write([]byte("abcdefgh"))
	if err != nil || n != 8 {
		t.Fatalf("Write = (%d, %v), want (8, nil) so the child never blocks", n, err)
	}
	if b.String() != "abcd" {
		t.Fatalf("retained = %q, want first 4 bytes %q", b.String(), "abcd")
	}
	// A second write past the cap is fully accepted but not retained.
	if n, _ := b.Write([]byte("ijkl")); n != 4 {
		t.Fatalf("second Write returned %d, want 4", n)
	}
	if b.String() != "abcd" {
		t.Fatalf("retained after overflow = %q, want %q", b.String(), "abcd")
	}
}

// --- helpers ---

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}
