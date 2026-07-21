// Package review is Lola's bounded, event-triggered QA pass over the CodeRabbit
// CLI (PLAN P9). It is the coding agent's QA BUDDY: on PR-open (opt-in) it runs
// exactly one `coderabbit review` against a worktree and hands the plain-text
// findings back to the worker agent and the human. It is NOT a persistent
// second agent — one invocation, then done.
//
// Hard contract — read before wiring this anywhere:
//
//   - EVENT-TRIGGERED + BOUNDED. Each review is a single `coderabbit review`
//     invocation with a hard context.WithTimeout (default 300s) and a
//     size-capped stdout (~16KB, head-clipped with a truncation marker). No
//     loops, no retries: on any error or timeout the caller skips the pass
//     gracefully (see the Err* sentinels), falling back to no QA rather than
//     blocking. Reuse the caller's one-shot guards so a review fires at most
//     once per PR-open transition.
//   - UNTRUSTED OUTPUT. The findings embed attacker-influenceable diff content,
//     so the returned string is untrusted text fit for a notification or a
//     Linear comment shown to a human. Before it is ever typed into the worker
//     agent it MUST pass the P3 sanitizeAgentText control-char stripper and the
//     AtPrompt idle-gate — it is never a command and never an unsanitized
//     send-keys payload.
//   - NO SECRETS. Auth is inherited, never managed here: the child coderabbit
//     runs with the daemon's environment (its own `coderabbit auth login`
//     session), so this package never reads, sets, or logs a credential. When
//     coderabbit is not authenticated the pass returns ErrAuth with a clear
//     "run: coderabbit auth login" hint. Any surfaced stderr is scrubbed
//     through redactSecrets so a nonzero-exit error can never carry a key.
package review

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	// defaultBin is the CodeRabbit executable resolved via PATH when
	// Client.Bin is empty. launchd contexts should set an absolute path.
	defaultBin = "coderabbit"
	// defaultTimeout bounds a single Review call when Client.Timeout == 0. A
	// real CodeRabbit pass takes a while; 300s is a generous hard ceiling.
	defaultTimeout = 300 * time.Second
	// maxOutputBytes is the soft cap on the findings we return. Output past
	// this is head-clipped (the head is kept) and terminated with truncMarker
	// so a runaway review can never blow up a notification or Linear comment.
	maxOutputBytes = 16 * 1024
	// maxCaptureBytes is the hard ceiling the real seam buffers from stdout. It
	// sits above maxOutputBytes so Review can detect overflow and apply the
	// head-clip marker; anything past this ceiling is discarded, keeping memory
	// bounded no matter how chatty coderabbit is.
	maxCaptureBytes = maxOutputBytes + 4*1024
	// maxStderrBytes bounds retained stderr surfaced (redacted) in errors.
	maxStderrBytes = 4 * 1024
	// truncMarker terminates a head-clipped review so the human can see the
	// findings were cut.
	truncMarker = "\n…[truncated]"
)

// defaultArgs is the CodeRabbit argv (minus --base) used when Client.Args is
// nil: a plain-text review of all finding types. It is never mutated — Review
// copies before appending --base.
var defaultArgs = []string{"review", "--plain", "--type", "all"}

// Distinct, testable error classes. Callers key on these to skip the QA pass
// gracefully (any of them means "no review this transition").
var (
	// ErrNotFound: the coderabbit binary was not found on PATH.
	ErrNotFound = errors.New("review: coderabbit not found on PATH")
	// ErrTimeout: the review hit its hard deadline and was killed.
	ErrTimeout = errors.New("review: coderabbit review timed out")
	// ErrAuth: coderabbit ran but reported an auth/login problem. The message
	// is the actionable hint and carries no stderr, so it can never leak a key.
	ErrAuth = errors.New("coderabbit not authenticated (run: coderabbit auth login)")
	// ErrExit: coderabbit exited nonzero for some other reason; the wrapped
	// message surfaces redacted stderr.
	ErrExit = errors.New("review: coderabbit exited nonzero")
	// ErrQuota: coderabbit is out of reviews / rate-limited / over quota. Unlike
	// the other sentinels this can arrive on a CLEAN exit (a limit line printed
	// to stdout with exit 0), so classification scans the stdout head too. It is
	// the one class that drives fallback: the caller advances to the next
	// provider in the chain rather than skipping QA outright.
	ErrQuota = errors.New("review: coderabbit over quota / rate-limited")
)

// Client runs bounded, one-shot CodeRabbit reviews. The zero value is usable
// and resolves "coderabbit" via PATH with the default argv and a 300s timeout.
type Client struct {
	// Bin is the coderabbit executable; empty resolves "coderabbit" via PATH.
	Bin string
	// Args is the review argv minus --base; nil means defaultArgs
	// (["review","--plain","--type","all"]). --base <baseBranch> is always
	// appended by Review.
	Args []string
	// Timeout bounds one Review call; 0 means defaultTimeout (300s).
	Timeout time.Duration
}

func (c *Client) bin() string {
	if c.Bin != "" {
		return c.Bin
	}
	return defaultBin
}

func (c *Client) args() []string {
	if c.Args != nil {
		return c.Args
	}
	return defaultArgs
}

func (c *Client) timeout() time.Duration {
	if c.Timeout > 0 {
		return c.Timeout
	}
	return defaultTimeout
}

// Available reports whether the configured coderabbit binary resolves on PATH.
// Callers use it to decide up front whether to attempt a review at all.
func (c *Client) Available() bool {
	_, err := exec.LookPath(c.bin())
	return err == nil
}

// Review runs `<bin> <args...> --base <baseBranch>` with the working directory
// set to worktreeDir and a hard timeout, returning coderabbit's trimmed,
// size-capped plain-text findings. It makes exactly one attempt and never
// retries. A clean review (exit 0, no findings) returns ("", nil); failures map
// to ErrNotFound / ErrTimeout / ErrAuth / ErrExit.
func (c *Client) Review(ctx context.Context, worktreeDir, baseBranch string) (string, error) {
	// Copy into a fresh backing array before appending so neither defaultArgs
	// nor a caller-supplied Client.Args slice is ever mutated.
	args := append(append([]string{}, c.args()...), "--base", baseBranch)
	out, err := runReview(ctx, c.bin(), args, worktreeDir, c.timeout())
	if err != nil {
		return "", err
	}
	return capOutput(strings.TrimSpace(out), maxOutputBytes), nil
}

// runReview is the exec seam. Tests override it to assert the bin, argv (incl.
// --base), working dir, and timeout WITHOUT running coderabbit. The real
// implementation applies the hard timeout, runs in worktreeDir, bounds the
// stdout it retains, and classifies failures into the Err* sentinels.
var runReview = func(ctx context.Context, bin string, args []string, dir string, timeout time.Duration) (string, error) {
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(cctx, bin, args...)
	cmd.Dir = dir // the review runs IN the worktree
	stdout := &cappedBuffer{cap: maxCaptureBytes}
	stderr := &cappedBuffer{cap: maxStderrBytes}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	// cmd.Env left nil: the child inherits the daemon env (coderabbit's own
	// auth session). This package never reads, sets, or logs that credential.

	err := cmd.Run()
	if e := classifyRunErr(err, cctx.Err(), stderr.String(), stdout.String(), timeout); e != nil {
		return "", e
	}
	return stdout.String(), nil
}

// classifyRunErr maps a raw exec result to a distinct sentinel. Deadline is
// checked first because a killed process surfaces as "signal: killed", not as a
// deadline error. Quota is checked next — over stderr, and over stdout ONLY when
// stdout is a short limit line rather than a real findings body (isStdoutQuota)
// — because coderabbit may print an "out of reviews" line to stdout and exit 0,
// so a quota signal must be caught even on a clean run (before the runErr==nil
// short-circuit) and must win over ErrAuth/ErrExit so the caller can fall
// through to a fallback provider. Gating the stdout scan on shortness stops a
// legitimate multi-KB review that merely mentions "rate limit"/"429" in its
// prose from self-classifying as ErrQuota and being discarded. On a plain
// nonzero exit, stderr is inspected for auth cues (→ ErrAuth, no stderr
// surfaced) and otherwise surfaced through redactSecrets so an error can never
// carry a key.
func classifyRunErr(runErr, ctxErr error, stderr, stdout string, timeout time.Duration) error {
	if errors.Is(ctxErr, context.DeadlineExceeded) {
		return fmt.Errorf("%w after %s", ErrTimeout, timeout)
	}
	if looksLikeQuotaError(stderr) || isStdoutQuota(stdout) {
		return ErrQuota // actionable class only; never echoes stdout/stderr
	}
	if runErr == nil {
		return nil
	}
	if errors.Is(runErr, exec.ErrNotFound) {
		return fmt.Errorf("%w: %v", ErrNotFound, runErr)
	}
	var ee *exec.ExitError
	if errors.As(runErr, &ee) {
		trimmed := strings.TrimSpace(stderr)
		if looksLikeAuthError(trimmed) {
			return ErrAuth // actionable hint only; never echoes stderr
		}
		if clean := redactSecrets(trimmed); clean != "" {
			return fmt.Errorf("%w (%s): %s", ErrExit, exitStatus(ee), clean)
		}
		return fmt.Errorf("%w (%s)", ErrExit, exitStatus(ee))
	}
	// Generic (non-exit, non-deadline) failure: matched by no sentinel. Use %s,
	// not %w, and redact so a stray token in the message can never leak.
	return fmt.Errorf("review: coderabbit run failed: %s", redactSecrets(runErr.Error()))
}

// quotaProbeBytes bounds how much stdout is treated as a bare "limit line" quota
// probe. A genuine over-quota message is short (a one-liner like "out of reviews
// for this cycle"); a real findings body is many KB. Gating the stdout quota
// scan on this ceiling stops a legitimate review that merely DISCUSSES rate
// limits / 429 / "exceeded" in its prose from self-classifying as ErrQuota and
// being discarded (which would wrongly trip the fallback chain).
const quotaProbeBytes = 512

// isStdoutQuota reports whether stdout is a short, quota-signalling limit line
// rather than a substantial findings body. See quotaProbeBytes.
func isStdoutQuota(stdout string) bool {
	s := strings.TrimSpace(stdout)
	if len(s) > quotaProbeBytes {
		return false
	}
	return looksLikeQuotaError(s)
}

// looksLikeAuthError is a best-effort classifier: on a failed run, stderr that
// mentions auth/login/unauthenticated almost certainly means coderabbit needs
// `coderabbit auth login`.
func looksLikeAuthError(stderr string) bool {
	l := strings.ToLower(stderr)
	for _, kw := range []string{"unauthenticated", "unauthorized", "not logged in", "auth", "login", "credential"} {
		if strings.Contains(l, kw) {
			return true
		}
	}
	return false
}

// looksLikeQuotaError is a best-effort classifier: output (stderr OR the stdout
// head) that mentions an out-of-reviews / rate-limit / quota condition almost
// certainly means the provider cannot answer right now, so the caller should
// advance to a fallback provider rather than skip QA. The cues are conservative
// and case-folded; they are matched against provider output only (never the
// findings we hand a human), so a false positive merely triggers a fallback.
func looksLikeQuotaError(s string) bool {
	l := strings.ToLower(s)
	for _, kw := range []string{
		"out of reviews", "usage limit", "rate limit", "rate_limit", "quota",
		"429", "too many requests", "exceeded", "insufficient", "credit balance",
	} {
		if strings.Contains(l, kw) {
			return true
		}
	}
	return false
}

// exitStatus renders an ExitError's code, guarding a nil ProcessState (which a
// synthetic ExitError may carry) so classification never panics.
func exitStatus(ee *exec.ExitError) string {
	if ee.ProcessState == nil {
		return "exit status unknown"
	}
	return fmt.Sprintf("exit status %d", ee.ExitCode())
}

// Secret shapes scrubbed from any stderr we surface. This runs only on a failed
// run's stderr (never on the findings themselves), so it errs aggressively:
// safety over fidelity. It must never let a credential reach an error string.
var (
	// Provider-style API keys, e.g. sk-... / sk-ant-...
	reAPIKey = regexp.MustCompile(`(?i)sk-[a-z0-9_-]{10,}`)
	// Bearer tokens — keep the scheme, drop the credential.
	reBearer = regexp.MustCompile(`(?i)(bearer\s+)[a-z0-9._~+/=-]{8,}`)
	// KEY / TOKEN / SECRET / PASSWORD assignments — keep the name, drop value.
	reAssign = regexp.MustCompile(`(?i)([a-z0-9_]*(?:key|token|secret|password|passwd|pwd)[a-z0-9_]*\s*[=:]\s*)(\S+)`)
	// Generic long opaque tokens (>=32 base64/hex-ish chars).
	reLongToken = regexp.MustCompile(`(?i)\b[a-z0-9_-]{32,}\b`)
)

// redactSecrets replaces credential-shaped substrings with "[redacted]".
func redactSecrets(s string) string {
	if s == "" {
		return s
	}
	s = reAPIKey.ReplaceAllString(s, "[redacted]")
	s = reBearer.ReplaceAllString(s, "${1}[redacted]")
	s = reAssign.ReplaceAllString(s, "${1}[redacted]")
	s = reLongToken.ReplaceAllString(s, "[redacted]")
	return s
}

// capOutput head-clips s to at most max bytes, keeping the head and appending
// truncMarker (on a UTF-8 rune boundary) when it cuts. Short input is returned
// unchanged.
func capOutput(s string, max int) string {
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
// cut, so a head-clipped review never ends mid-rune.
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
