package brain

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

// capture records what the seam received on the last Summarize call.
type capture struct {
	bin, model, instruction, stdin string
	timeout                        time.Duration
	calls                          int
}

// stubSeam installs a runClaude replacement that records its args and returns
// (out, err). It restores the real seam on cleanup, so no test ever runs
// claude.
func stubSeam(t *testing.T, out string, err error) *capture {
	t.Helper()
	orig := runClaude
	t.Cleanup(func() { runClaude = orig })
	c := &capture{}
	runClaude = func(_ context.Context, bin, model, instruction, stdin string, timeout time.Duration) (string, error) {
		c.calls++
		c.bin, c.model, c.instruction, c.stdin, c.timeout = bin, model, instruction, stdin, timeout
		return out, err
	}
	return c
}

func TestSummarizePassesInstructionOnArgAndContextOnStdin(t *testing.T) {
	cap := stubSeam(t, "ok", nil)
	cl := &Client{}
	const instruction = "Summarize the escalation in 5 lines"
	const ctxText = "PANE TAIL: agent is stuck waiting for input"

	if _, err := cl.Summarize(context.Background(), instruction, ctxText); err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	if cap.calls != 1 {
		t.Fatalf("calls = %d, want exactly 1 (no retries)", cap.calls)
	}
	if cap.instruction != instruction {
		t.Errorf("instruction = %q, want %q", cap.instruction, instruction)
	}
	if cap.stdin != ctxText {
		t.Errorf("stdin = %q, want the context %q", cap.stdin, ctxText)
	}
	// The context must never leak onto argv (instruction / model).
	if strings.Contains(cap.instruction, ctxText) {
		t.Error("context leaked into the -p instruction (argv)")
	}
	if cap.bin != defaultBin {
		t.Errorf("bin = %q, want default %q", cap.bin, defaultBin)
	}
	if cap.timeout != defaultTimeout {
		t.Errorf("timeout = %s, want default %s", cap.timeout, defaultTimeout)
	}
	if cap.model != "" {
		t.Errorf("model = %q, want empty by default", cap.model)
	}
}

func TestSummarizeHonorsConfiguredBinModelTimeout(t *testing.T) {
	cap := stubSeam(t, "ok", nil)
	cl := &Client{Bin: "/opt/claude", Model: "claude-opus-4-8", Timeout: 7 * time.Second}
	if _, err := cl.Summarize(context.Background(), "go", "ctx"); err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	if cap.bin != "/opt/claude" {
		t.Errorf("bin = %q, want /opt/claude", cap.bin)
	}
	if cap.model != "claude-opus-4-8" {
		t.Errorf("model = %q, want claude-opus-4-8", cap.model)
	}
	if cap.timeout != 7*time.Second {
		t.Errorf("timeout = %s, want 7s", cap.timeout)
	}
}

func TestSummarizeTrimsOutput(t *testing.T) {
	stubSeam(t, "  \n line one\nline two \n\n", nil)
	got, err := (&Client{}).Summarize(context.Background(), "i", "c")
	if err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	if want := "line one\nline two"; got != want {
		t.Fatalf("output = %q, want trimmed %q", got, want)
	}
}

func TestSummarizeCapsOversizedContext(t *testing.T) {
	cap := stubSeam(t, "ok", nil)
	big := strings.Repeat("A", maxContextBytes*2)
	if _, err := (&Client{}).Summarize(context.Background(), "i", big); err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	if len(cap.stdin) > maxContextBytes {
		t.Fatalf("stdin len = %d, want <= %d", len(cap.stdin), maxContextBytes)
	}
	if !strings.HasSuffix(cap.stdin, truncMarker) {
		t.Fatalf("truncated stdin missing marker %q: ...%q", truncMarker, tail(cap.stdin, 20))
	}
	if !strings.HasPrefix(cap.stdin, "AAAA") {
		t.Fatalf("truncated stdin should keep the original prefix, got %q...", cap.stdin[:8])
	}
}

func TestSummarizeDoesNotCapSmallContext(t *testing.T) {
	cap := stubSeam(t, "ok", nil)
	const small = "just a little context"
	if _, err := (&Client{}).Summarize(context.Background(), "i", small); err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	if cap.stdin != small {
		t.Fatalf("stdin = %q, want unchanged %q", cap.stdin, small)
	}
}

func TestSummarizePropagatesSeamErrors(t *testing.T) {
	for _, sentinel := range []error{ErrNotFound, ErrTimeout, ErrNonZeroExit} {
		stubSeam(t, "", sentinel)
		got, err := (&Client{}).Summarize(context.Background(), "i", "c")
		if !errors.Is(err, sentinel) {
			t.Errorf("err = %v, want %v", err, sentinel)
		}
		if got != "" {
			t.Errorf("output = %q, want empty on error", got)
		}
	}
}

func TestBuildArgs(t *testing.T) {
	// No model: -p <instruction> --output-format text, context absent.
	got := buildArgs("", "do the thing")
	want := []string{"-p", "do the thing", "--output-format", "text"}
	if !equal(got, want) {
		t.Fatalf("buildArgs() = %v, want %v", got, want)
	}
	if got[0] != "-p" || got[1] != "do the thing" {
		t.Fatalf("instruction is not the -p value: %v", got)
	}

	// With model: appends --model <m>.
	gotM := buildArgs("claude-x", "go")
	if !contains(gotM, "--model") || !contains(gotM, "claude-x") {
		t.Fatalf("buildArgs with model missing --model: %v", gotM)
	}
	// Context is never assembled into argv, so no arg equals a context blob.
	for _, a := range gotM {
		if strings.HasPrefix(a, "CONTEXT:") {
			t.Fatalf("context leaked into argv: %v", gotM)
		}
	}
}

func TestClassifyRunErr(t *testing.T) {
	// Deadline wins even when the raw error is "signal: killed".
	if e := classifyRunErr(errors.New("signal: killed"), context.DeadlineExceeded, "", 3*time.Second); !errors.Is(e, ErrTimeout) {
		t.Errorf("deadline: got %v, want ErrTimeout", e)
	}
	// Not-found via a synthetic exec.Error.
	notFound := &exec.Error{Name: "claude", Err: exec.ErrNotFound}
	if e := classifyRunErr(notFound, nil, "", time.Second); !errors.Is(e, ErrNotFound) {
		t.Errorf("not-found: got %v, want ErrNotFound", e)
	}
	// Nonzero exit surfaces stderr (a synthetic ExitError has nil
	// ProcessState — must not panic).
	exitErr := &exec.ExitError{}
	e := classifyRunErr(exitErr, nil, "  boom on stderr  ", time.Second)
	if !errors.Is(e, ErrNonZeroExit) {
		t.Errorf("exit: got %v, want ErrNonZeroExit", e)
	}
	if !strings.Contains(e.Error(), "boom on stderr") {
		t.Errorf("exit error should surface stderr, got %q", e.Error())
	}
	// Success and generic failure.
	if e := classifyRunErr(nil, nil, "", time.Second); e != nil {
		t.Errorf("nil run error should classify to nil, got %v", e)
	}
	generic := classifyRunErr(errors.New("weird"), nil, "", time.Second)
	if generic == nil || errors.Is(generic, ErrTimeout) || errors.Is(generic, ErrNotFound) || errors.Is(generic, ErrNonZeroExit) {
		t.Errorf("generic error misclassified: %v", generic)
	}
}

func TestCapContextRuneSafe(t *testing.T) {
	// "é" is 2 bytes; a byte-cut at maxContextBytes-len(marker) can land
	// mid-rune. The result must still be valid UTF-8.
	in := strings.Repeat("é", maxContextBytes) // 2*max bytes
	out := capContext(in, maxContextBytes)
	if !utf8.ValidString(out) {
		t.Fatal("capped context is not valid UTF-8 (cut mid-rune)")
	}
	if len(out) > maxContextBytes {
		t.Fatalf("capped len = %d, want <= %d", len(out), maxContextBytes)
	}
	if !strings.HasSuffix(out, truncMarker) {
		t.Fatal("capped context missing truncation marker")
	}
	body := strings.TrimSuffix(out, truncMarker)
	if !strings.HasPrefix(in, body) {
		t.Fatal("capped body is not a prefix of the original")
	}
}

func TestAvailable(t *testing.T) {
	// Missing binary → false.
	if (&Client{Bin: "lola-brain-nonexistent-binary-zzz"}).Available() {
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

// TestRealSeamNotFound exercises the REAL runClaude (seam not overridden) with
// a binary that cannot be found, proving the exec+classify path returns
// ErrNotFound without ever spawning claude.
func TestRealSeamNotFound(t *testing.T) {
	cl := &Client{Bin: "lola-brain-nonexistent-binary-zzz", Timeout: 2 * time.Second}
	_, err := cl.Summarize(context.Background(), "i", "c")
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

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}
