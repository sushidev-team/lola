package reviewclaude

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

// claudeCapture records what the claude seam received on the last Review call.
type claudeCapture struct {
	bin         string
	model       string
	instruction string
	dir         string
	stdin       string
	timeout     time.Duration
	calls       int
}

// stubClaude installs a runClaude replacement that records its args and returns
// (out, err), restoring the real seam on cleanup so no test ever runs claude.
func stubClaude(t *testing.T, out string, err error) *claudeCapture {
	t.Helper()
	orig := runClaude
	t.Cleanup(func() { runClaude = orig })
	c := &claudeCapture{}
	runClaude = func(_ context.Context, bin, model, instruction, dir, stdin string, timeout time.Duration) (string, error) {
		c.calls++
		c.bin, c.model, c.instruction, c.dir, c.stdin, c.timeout = bin, model, instruction, dir, stdin, timeout
		return out, err
	}
	return c
}

// stubGitDiff installs a runGitDiff replacement returning a canned diff, so the
// real git is never run and tests can prove that exact diff reaches claude on
// stdin. It records the dir/base it was asked for.
func stubGitDiff(t *testing.T, diff string, err error) *struct {
	dir, base string
	calls     int
} {
	t.Helper()
	orig := runGitDiff
	t.Cleanup(func() { runGitDiff = orig })
	rec := &struct {
		dir, base string
		calls     int
	}{}
	runGitDiff = func(_ context.Context, dir, base string) (string, error) {
		rec.calls++
		rec.dir, rec.base = dir, base
		return diff, err
	}
	return rec
}

func TestReviewBuildsArgvAndPipesDiffInWorktree(t *testing.T) {
	// A diff that, if ever executed as a command, would be catastrophic — proving
	// it is handed to claude as stdin DATA, never as argv.
	const diff = "diff --git a/x b/x\n+; rm -rf / #dangerous\n"
	gd := stubGitDiff(t, diff, nil)
	cap := stubClaude(t, "some findings", nil)

	if _, err := (&Client{}).Review(context.Background(), "/work/tree", "main"); err != nil {
		t.Fatalf("Review: %v", err)
	}
	if cap.calls != 1 {
		t.Fatalf("claude calls = %d, want exactly 1 (no retries)", cap.calls)
	}
	if gd.calls != 1 {
		t.Fatalf("git diff calls = %d, want exactly 1", gd.calls)
	}
	// git diff was asked for the worktree + base branch.
	if gd.dir != "/work/tree" || gd.base != "main" {
		t.Errorf("git diff (dir,base) = (%q,%q), want (/work/tree, main)", gd.dir, gd.base)
	}
	// The claude command runs in the worktree...
	if cap.dir != "/work/tree" {
		t.Errorf("claude dir = %q, want the worktree /work/tree", cap.dir)
	}
	// ...and the diff is on stdin, verbatim (untrusted, not executed).
	if cap.stdin != diff {
		t.Errorf("claude stdin = %q, want the diff %q", cap.stdin, diff)
	}
	// The diff must NOT leak into the argv (the -p instruction is our own text).
	if strings.Contains(cap.instruction, "rm -rf") {
		t.Errorf("diff leaked into the -p argv: %q", cap.instruction)
	}
	if cap.instruction != reviewInstruction {
		t.Errorf("instruction = %q, want the fixed reviewInstruction", cap.instruction)
	}
	if cap.bin != defaultBin {
		t.Errorf("bin = %q, want default %q", cap.bin, defaultBin)
	}
	if cap.model != "" {
		t.Errorf("model = %q, want empty (claude default)", cap.model)
	}
	if cap.timeout != defaultTimeout {
		t.Errorf("timeout = %s, want default %s", cap.timeout, defaultTimeout)
	}
}

func TestReviewHonorsConfiguredBinModelTimeout(t *testing.T) {
	stubGitDiff(t, "some diff", nil)
	cap := stubClaude(t, "ok", nil)
	cl := &Client{Bin: "/opt/claude", Model: "claude-opus", Timeout: 42 * time.Second}
	if _, err := cl.Review(context.Background(), "/wt", "develop"); err != nil {
		t.Fatalf("Review: %v", err)
	}
	if cap.bin != "/opt/claude" {
		t.Errorf("bin = %q, want /opt/claude", cap.bin)
	}
	if cap.model != "claude-opus" {
		t.Errorf("model = %q, want claude-opus", cap.model)
	}
	if cap.timeout != 42*time.Second {
		t.Errorf("timeout = %s, want 42s", cap.timeout)
	}
}

// buildArgs must place the diff nowhere, force text output, and append --model
// only when set.
func TestBuildArgs(t *testing.T) {
	got := buildArgs("", reviewInstruction)
	want := []string{"-p", reviewInstruction, "--output-format", "text"}
	if !equal(got, want) {
		t.Fatalf("buildArgs(no model) = %v, want %v", got, want)
	}
	got = buildArgs("claude-sonnet", reviewInstruction)
	want = []string{"-p", reviewInstruction, "--output-format", "text", "--model", "claude-sonnet"}
	if !equal(got, want) {
		t.Fatalf("buildArgs(model) = %v, want %v", got, want)
	}
}

func TestReviewTrimsOutput(t *testing.T) {
	stubGitDiff(t, "diff", nil)
	stubClaude(t, "  \n finding one\nfinding two \n\n", nil)
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
		stubGitDiff(t, "diff", nil)
		stubClaude(t, out, nil)
		got, err := (&Client{}).Review(context.Background(), "/wt", "main")
		if err != nil {
			t.Fatalf("clean review err = %v, want nil", err)
		}
		if got != "" {
			t.Fatalf("clean review output = %q, want empty string", got)
		}
	}
}

// An empty diff (no changes against the base) must short-circuit to ("", nil)
// WITHOUT ever invoking claude — no diff, nothing to review, no paid call.
func TestReviewEmptyDiffSkipsClaude(t *testing.T) {
	for _, diff := range []string{"", "   \n\t  \n"} {
		stubGitDiff(t, diff, nil)
		cap := stubClaude(t, "should not be called", nil)
		got, err := (&Client{}).Review(context.Background(), "/wt", "main")
		if err != nil {
			t.Fatalf("empty-diff err = %v, want nil", err)
		}
		if got != "" {
			t.Fatalf("empty-diff output = %q, want empty", got)
		}
		if cap.calls != 0 {
			t.Fatalf("claude called %d times on an empty diff, want 0", cap.calls)
		}
	}
}

// A git-diff failure surfaces (redacted) and never reaches claude.
func TestReviewGitDiffErrorSkipsClaude(t *testing.T) {
	stubGitDiff(t, "", errors.New("not a git repository"))
	cap := stubClaude(t, "unused", nil)
	_, err := (&Client{}).Review(context.Background(), "/wt", "main")
	if err == nil {
		t.Fatal("expected a git-diff error, got nil")
	}
	if cap.calls != 0 {
		t.Fatalf("claude called %d times despite a git-diff failure, want 0", cap.calls)
	}
}

func TestReviewCapsOversizedOutput(t *testing.T) {
	stubGitDiff(t, "diff", nil)
	big := strings.Repeat("A", maxOutputBytes*2)
	stubClaude(t, big, nil)
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

// An oversized diff is head-clipped to maxDiffBytes before it is piped to claude
// on stdin, so a runaway diff can never blow the prompt (or memory) up.
func TestReviewCapsOversizedDiffOnStdin(t *testing.T) {
	stubGitDiff(t, strings.Repeat("D", maxDiffBytes*2), nil)
	cap := stubClaude(t, "ok", nil)
	if _, err := (&Client{}).Review(context.Background(), "/wt", "main"); err != nil {
		t.Fatalf("Review: %v", err)
	}
	if len(cap.stdin) > maxDiffBytes {
		t.Fatalf("stdin diff len = %d, want <= %d", len(cap.stdin), maxDiffBytes)
	}
	if !strings.HasSuffix(cap.stdin, truncMarker) {
		t.Fatalf("capped diff missing marker %q", truncMarker)
	}
}

func TestReviewPropagatesSeamSentinels(t *testing.T) {
	for _, sentinel := range []error{ErrNotFound, ErrTimeout, ErrAuth, ErrExit, ErrQuota} {
		stubGitDiff(t, "diff", nil)
		stubClaude(t, "", sentinel)
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
	if e := classifyRunErr(errors.New("signal: killed"), context.DeadlineExceeded, "", "", 3*time.Second); !errors.Is(e, ErrTimeout) {
		t.Errorf("deadline: got %v, want ErrTimeout", e)
	}
	// Not-found via a synthetic exec.Error.
	notFound := &exec.Error{Name: "claude", Err: exec.ErrNotFound}
	if e := classifyRunErr(notFound, nil, "", "", time.Second); !errors.Is(e, ErrNotFound) {
		t.Errorf("not-found: got %v, want ErrNotFound", e)
	}
	// Auth cue in stderr → ErrAuth.
	authErr := classifyRunErr(&exec.ExitError{}, nil, "Error: invalid api key provided", "", time.Second)
	if !errors.Is(authErr, ErrAuth) {
		t.Errorf("auth: got %v, want ErrAuth", authErr)
	}
	// Other nonzero exit surfaces stderr (synthetic ExitError has nil
	// ProcessState — must not panic).
	exitErr := classifyRunErr(&exec.ExitError{}, nil, "  parse error on line 3  ", "", time.Second)
	if !errors.Is(exitErr, ErrExit) {
		t.Errorf("exit: got %v, want ErrExit", exitErr)
	}
	if !strings.Contains(exitErr.Error(), "parse error on line 3") {
		t.Errorf("exit error should surface stderr, got %q", exitErr.Error())
	}
	// A plain nonzero exit with no quota cue must stay ErrExit, NOT ErrQuota.
	if errors.Is(exitErr, ErrQuota) {
		t.Errorf("plain exit misclassified as quota: %v", exitErr)
	}
	// Success and generic failure.
	if e := classifyRunErr(nil, nil, "", "", time.Second); e != nil {
		t.Errorf("nil run error should classify to nil, got %v", e)
	}
	generic := classifyRunErr(errors.New("weird"), nil, "", "", time.Second)
	if generic == nil || errors.Is(generic, ErrTimeout) || errors.Is(generic, ErrNotFound) || errors.Is(generic, ErrAuth) || errors.Is(generic, ErrExit) || errors.Is(generic, ErrQuota) {
		t.Errorf("generic error misclassified: %v", generic)
	}
}

// TestClassifyRunErrQuota covers the over-quota class, which — unlike every
// other sentinel — can arrive on a CLEAN exit (a limit line printed to stdout
// with exit 0). It must be detected on stderr AND on the stdout head, and it
// must win over ErrExit/ErrAuth so the caller can fall through to a fallback.
func TestClassifyRunErrQuota(t *testing.T) {
	// Quota cue in stderr on a nonzero exit → ErrQuota (not ErrExit).
	if e := classifyRunErr(&exec.ExitError{}, nil, "Error: usage limit reached, try again later", "", time.Second); !errors.Is(e, ErrQuota) {
		t.Errorf("stderr quota: got %v, want ErrQuota", e)
	}
	// Quota cue on the stdout head with a CLEAN exit (runErr == nil) → ErrQuota,
	// caught before the nil short-circuit.
	if e := classifyRunErr(nil, nil, "", "You have reached your usage limit for this period.", time.Second); !errors.Is(e, ErrQuota) {
		t.Errorf("stdout quota (exit 0): got %v, want ErrQuota", e)
	}
	// Quota beats auth: a message carrying both cues classifies as quota so the
	// chain falls through rather than skipping outright.
	if e := classifyRunErr(&exec.ExitError{}, nil, "HTTP 429 too many requests; not authenticated", "", time.Second); !errors.Is(e, ErrQuota) {
		t.Errorf("quota+auth: got %v, want ErrQuota", e)
	}
	// The error text never echoes the (attacker-influenceable) output.
	q := classifyRunErr(nil, nil, "", "quota exceeded: sk-ant-DEADBEEFdeadbeef0123456789", time.Second)
	if strings.Contains(q.Error(), "sk-ant-DEADBEEFdeadbeef0123456789") {
		t.Errorf("quota error leaked output: %q", q.Error())
	}
	// Regression: a REAL, multi-KB findings body that merely mentions quota cues
	// in its prose ("rate limit", "429", "exceeded") on a CLEAN exit must NOT be
	// misclassified ErrQuota — that would silently discard the review and trip the
	// fallback chain. The stdout scan is gated on shortness (isStdoutQuota).
	bigFindings := "The handler ignores the rate limit header and can return HTTP 429; the retry budget is exceeded under load. " +
		strings.Repeat("Additional review detail about input validation and error handling. ", 20)
	if len(bigFindings) <= quotaProbeBytes {
		t.Fatalf("test fixture too short (%d bytes) to exercise the shortness gate", len(bigFindings))
	}
	if e := classifyRunErr(nil, nil, "", bigFindings, time.Second); e != nil {
		t.Errorf("substantial findings mentioning quota words misclassified: got %v, want nil", e)
	}
}

func TestLooksLikeQuotaError(t *testing.T) {
	for _, s := range []string{
		"You are out of reviews",
		"monthly usage limit reached",
		"rate limit exceeded",
		"error: rate_limit",
		"quota exhausted",
		"HTTP 429",
		"429 Too Many Requests",
		"insufficient credits remaining",
		"your credit balance is too low",
	} {
		if !looksLikeQuotaError(s) {
			t.Errorf("looksLikeQuotaError(%q) = false, want true", s)
		}
	}
	// Ordinary review output / a plain exit error must NOT read as quota.
	for _, s := range []string{"", "parse error on line 3", "found 2 issues in main.go"} {
		if looksLikeQuotaError(s) {
			t.Errorf("looksLikeQuotaError(%q) = true, want false", s)
		}
	}
}

func TestLooksLikeAuthError(t *testing.T) {
	for _, s := range []string{
		"you are not authenticated",
		"invalid api key",
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

// The exit path must never surface a credential, even when claude's stderr
// echoes one. Auth cues are classified before the exit path, so use a
// non-auth-looking secret shape here.
func TestExitErrorNeverLeaksAKey(t *testing.T) {
	const key = "sk-ant-api03-DEADBEEFdeadbeef0123456789ABCDEF"
	stderr := "fatal: request failed using " + key + " while contacting the server"
	e := classifyRunErr(&exec.ExitError{}, nil, stderr, "", time.Second)
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
	if got := redactSecrets("undefined variable `foo` at bar.go:10"); !strings.Contains(got, "undefined variable") {
		t.Errorf("redactSecrets mangled ordinary text: %q", got)
	}
}

func TestCapTextRuneSafe(t *testing.T) {
	// "é" is 2 bytes; a byte-cut at max-len(marker) can land mid-rune. The result
	// must still be valid UTF-8.
	in := strings.Repeat("é", maxOutputBytes) // 2*max bytes
	out := capText(in, maxOutputBytes)
	if !utf8.ValidString(out) {
		t.Fatal("capped text is not valid UTF-8 (cut mid-rune)")
	}
	if len(out) > maxOutputBytes {
		t.Fatalf("capped len = %d, want <= %d", len(out), maxOutputBytes)
	}
	if !strings.HasSuffix(out, truncMarker) {
		t.Fatal("capped text missing truncation marker")
	}
	body := strings.TrimSuffix(out, truncMarker)
	if !strings.HasPrefix(in, body) {
		t.Fatal("capped body is not a prefix of the original (not head-clipped)")
	}
}

func TestCapTextLeavesSmallUnchanged(t *testing.T) {
	const small = "one small finding"
	if got := capText(small, maxOutputBytes); got != small {
		t.Fatalf("capText mangled small input: %q", got)
	}
}

func TestAvailable(t *testing.T) {
	// Missing binary → false.
	if (&Client{Bin: "lola-reviewclaude-nonexistent-binary-zzz"}).Available() {
		t.Error("Available() = true for a missing binary")
	}
	// A real, executable file (path form) → true.
	dir := t.TempDir()
	bin := filepath.Join(dir, "claude")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if !(&Client{Bin: bin}).Available() {
		t.Errorf("Available() = false for executable %s", bin)
	}
}

// TestRealClaudeSeamNotFound exercises the REAL runClaude (seam not overridden)
// with a binary that cannot be found, proving the exec+classify path returns
// ErrNotFound without ever spawning claude. The git-diff seam is stubbed so no
// real git runs.
func TestRealClaudeSeamNotFound(t *testing.T) {
	stubGitDiff(t, "some diff", nil)
	cl := &Client{Bin: "lola-reviewclaude-nonexistent-binary-zzz", Timeout: 2 * time.Second}
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
