// Package brain is Lola's bounded, opt-in, headless-claude summarizer (PLAN
// P5.25). It exists to turn one attacker-influenceable blob of context (a PR
// diff, failing CI logs, a tmux pane tail) into a short human-facing SUMMARY
// at a single decision point, and nothing else.
//
// Hard contract — read before wiring this anywhere:
//
//   - OPT-IN. Callers gate every use behind [brain].enabled (default false).
//     This package has no global state and changes no behavior on its own; a
//     disabled brain is simply never constructed.
//   - READ-ONLY + BOUNDED. Each summary is exactly one `claude -p` invocation
//     with a hard context.WithTimeout (default 120s), a size-capped context
//     (~12KB) on STDIN, and a bounded stdout read (~8KB). No loops, no
//     retries: on any error or timeout the caller falls back to its generic
//     template. Callers must reuse their P3/P4 one-shot guards so a summary
//     fires at most once per transition.
//   - UNTRUSTED OUTPUT. The context fed to claude is attacker-influenceable,
//     so the returned string is untrusted text fit only for a notification or
//     a Linear comment shown to a human. It must NEVER be fed back into the
//     worker agent (tmux send-keys) — that would be prompt-injection straight
//     into the control path.
//
// Auth is inherited, never managed here: the child claude runs with the
// daemon's environment (os.Environ), so it uses the user's ~/.claude session
// or ANTHROPIC_API_KEY from the daemon env. This package never reads, sets, or
// logs that key; surfaced stderr is claude's own diagnostic text.
package brain

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	// defaultBin is the claude executable resolved via PATH when Client.Bin
	// is empty. launchd contexts should set an absolute path.
	defaultBin = "claude"
	// defaultTimeout bounds a single Summarize call when Client.Timeout == 0.
	defaultTimeout = 120 * time.Second
	// maxContextBytes caps the context delivered on stdin. Oversized context
	// is truncated with truncMarker so a runaway diff/log can never blow the
	// prompt (or memory) up.
	maxContextBytes = 12 * 1024
	// maxOutputBytes bounds how much of claude's stdout we retain. A summary
	// is a handful of lines; anything past this is discarded, not buffered.
	maxOutputBytes = 8 * 1024
	// maxStderrBytes bounds retained stderr surfaced in error messages.
	maxStderrBytes = 4 * 1024
	// truncMarker terminates a truncated context so the human (and claude)
	// can see the input was cut.
	truncMarker = "\n…[truncated]"
)

// Distinct, testable error classes. Callers key on these to skip gracefully
// (any of them means "fall back to the generic template").
var (
	// ErrNotFound: the claude binary was not found on PATH.
	ErrNotFound = errors.New("brain: claude not found on PATH")
	// ErrTimeout: the invocation hit its hard deadline and was killed.
	ErrTimeout = errors.New("brain: summarize timed out")
	// ErrNonZeroExit: claude ran but exited nonzero.
	ErrNonZeroExit = errors.New("brain: claude exited nonzero")
)

// Client runs bounded headless claude summaries. The zero value is usable and
// resolves "claude" via PATH with a 120s timeout.
type Client struct {
	// Bin is the claude executable; empty resolves "claude" via PATH.
	Bin string
	// Model, when non-empty, is passed as `--model <m>`; empty lets claude
	// pick its configured default. This is the "field" in "set via a
	// field/param".
	Model string
	// Timeout bounds one Summarize call; 0 means defaultTimeout.
	Timeout time.Duration
}

func (c *Client) bin() string {
	if c.Bin != "" {
		return c.Bin
	}
	return defaultBin
}

func (c *Client) timeout() time.Duration {
	if c.Timeout > 0 {
		return c.Timeout
	}
	return defaultTimeout
}

// Available reports whether the configured claude binary resolves on PATH.
// Callers use it to decide up front whether to attempt a summary at all
// (doctor performs the richer version check).
func (c *Client) Available() bool {
	_, err := exec.LookPath(c.bin())
	return err == nil
}

// Summarize runs `<bin> -p <instruction> --output-format text` (plus
// `--model <Model>` when set), delivering contextText on STDIN — never on
// argv, because it can be large and secret-adjacent. contextText is capped to
// ~12KB before sending. It returns claude's trimmed stdout, or one of
// ErrNotFound / ErrTimeout / ErrNonZeroExit on failure. It makes exactly one
// attempt with a hard timeout; it never retries.
func (c *Client) Summarize(ctx context.Context, instruction, contextText string) (string, error) {
	out, err := runClaude(ctx, c.bin(), c.Model, instruction, capContext(contextText, maxContextBytes), c.timeout())
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// runClaude is the exec seam. Tests override it to assert the bin, model,
// instruction (the -p arg), stdin (the context — asserted to NOT be on argv),
// and timeout WITHOUT running claude. The real implementation applies the
// hard timeout, streams context on stdin, and bounds the stdout it retains.
//
// The `model` parameter is threaded through the seam deliberately: it is the
// only way an optional `--model` can reach the real argv, since this is a
// package-level var with no access to the Client value.
var runClaude = func(ctx context.Context, bin, model, instruction, stdin string, timeout time.Duration) (string, error) {
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(cctx, bin, buildArgs(model, instruction)...)
	cmd.Stdin = strings.NewReader(stdin) // context on stdin, never argv
	stdout := &cappedBuffer{cap: maxOutputBytes}
	stderr := &cappedBuffer{cap: maxStderrBytes}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	// cmd.Env left nil: the child inherits the daemon env (claude auth /
	// ANTHROPIC_API_KEY). This package never touches that key.

	err := cmd.Run()
	if e := classifyRunErr(err, cctx.Err(), stderr.String(), timeout); e != nil {
		return "", e
	}
	return stdout.String(), nil
}

// buildArgs assembles the claude argv. The instruction is the `-p` prompt;
// the context is NEVER here (it goes on stdin). Output is forced to text.
func buildArgs(model, instruction string) []string {
	args := []string{"-p", instruction, "--output-format", "text"}
	if model != "" {
		args = append(args, "--model", model)
	}
	return args
}

// classifyRunErr maps a raw exec result to a distinct sentinel. Deadline is
// checked first because a killed process surfaces as "signal: killed", not as
// a deadline error. Surfaced stderr is claude's own text; this package never
// puts a key in it.
func classifyRunErr(runErr, ctxErr error, stderr string, timeout time.Duration) error {
	if errors.Is(ctxErr, context.DeadlineExceeded) {
		return fmt.Errorf("%w after %s", ErrTimeout, timeout)
	}
	if runErr == nil {
		return nil
	}
	if errors.Is(runErr, exec.ErrNotFound) {
		return fmt.Errorf("%w: %v", ErrNotFound, runErr)
	}
	var ee *exec.ExitError
	if errors.As(runErr, &ee) {
		if s := strings.TrimSpace(stderr); s != "" {
			return fmt.Errorf("%w (%s): %s", ErrNonZeroExit, exitStatus(ee), s)
		}
		return fmt.Errorf("%w (%s)", ErrNonZeroExit, exitStatus(ee))
	}
	return fmt.Errorf("brain: claude run failed: %w", runErr)
}

// exitStatus renders an ExitError's code, guarding a nil ProcessState (which
// a synthetic ExitError may carry) so classification never panics.
func exitStatus(ee *exec.ExitError) string {
	if ee.ProcessState == nil {
		return "exit status unknown"
	}
	return fmt.Sprintf("exit status %d", ee.ExitCode())
}

// capContext truncates s to at most max bytes, appending truncMarker (on a
// UTF-8 rune boundary) when it cuts. Short input is returned unchanged.
func capContext(s string, max int) string {
	if len(s) <= max {
		return s
	}
	keep := max - len(truncMarker)
	if keep < 0 {
		keep = 0
	}
	return trimPartialRune(s[:keep]) + truncMarker
}

// trimPartialRune drops a trailing partial UTF-8 sequence left by a byte-slice
// cut, so the truncated context never ends mid-rune.
func trimPartialRune(s string) string {
	for len(s) > 0 {
		if r, size := utf8.DecodeLastRuneInString(s); r == utf8.RuneError && size <= 1 {
			s = s[:len(s)-1]
			continue
		}
		break
	}
	return s
}

// cappedBuffer accumulates at most cap bytes but keeps accepting (and
// discarding) the rest, so a chatty child never blocks on a full pipe while
// memory stays bounded.
type cappedBuffer struct {
	buf bytes.Buffer
	cap int
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	if room := b.cap - b.buf.Len(); room > 0 {
		if room < len(p) {
			b.buf.Write(p[:room])
		} else {
			b.buf.Write(p)
		}
	}
	return len(p), nil
}

func (b *cappedBuffer) String() string { return b.buf.String() }
