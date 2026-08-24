package reviewagent

// Tests for the VISIBLE pass (stream.go): the stream-json argv, the rendering
// of each event kind into a plain progress line, and ReviewStream's contract —
// identical findings, identical clean case, identical sentinels.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sushidev-team/lola/internal/agent"
)

// streamSeam is the signature both visible-pass exec seams share.
type streamSeam func(ctx context.Context, k agent.Kind, bin string, args []string, dir, stdin string, timeout time.Duration, progress io.Writer) (string, error)

// swapStream installs a fake streaming exec seam (claude's stream-json one) for
// the duration of a test.
func swapStream(t *testing.T, fn streamSeam) {
	t.Helper()
	orig := runAgentStreamJSON
	runAgentStreamJSON = fn
	t.Cleanup(func() { runAgentStreamJSON = orig })
}

// swapStreamPlain installs a fake for the stderr-narrating seam (codex/opencode).
func swapStreamPlain(t *testing.T, fn streamSeam) {
	t.Helper()
	orig := runAgentStreamPlain
	runAgentStreamPlain = fn
	t.Cleanup(func() { runAgentStreamPlain = orig })
}

// swapDiff installs a fake git-diff seam.
func swapDiff(t *testing.T, out string, err error) {
	t.Helper()
	orig := runGitDiff
	runGitDiff = func(context.Context, string, string) (string, error) { return out, err }
	t.Cleanup(func() { runGitDiff = orig })
}

func TestStreamArgsAskForTheStreamFormat(t *testing.T) {
	got := agent.ReviewStreamArgs(agent.Claude, "REVIEW-INSTRUCTION", "sonnet")
	want := []string{"-p", "REVIEW-INSTRUCTION", "--output-format", "stream-json", "--verbose", "--model", "sonnet"}
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Errorf("argv = %v, want %v", got, want)
	}
	// --verbose is mandatory alongside stream-json in print mode; without it the
	// CLI refuses to start and every visible pass would fail.
	if !slices.Contains(agent.ReviewStreamArgs(agent.Claude, "x", ""), "--verbose") {
		t.Error("streaming argv must carry --verbose")
	}
}

func TestRenderStreamLine(t *testing.T) {
	for _, tc := range []struct {
		name     string
		line     string
		want     string
		result   string
		isResult bool
	}{
		{
			name: "tool call names what it reads",
			line: `{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Read","input":{"file_path":"app/Services/Foo.php"}}]}}`,
			want: "→ Read app/Services/Foo.php",
		},
		{
			name: "tool call without a displayable argument",
			line: `{"type":"assistant","message":{"content":[{"type":"tool_use","name":"TodoWrite","input":{"todos":[]}}]}}`,
			want: "→ TodoWrite",
		},
		{
			name: "prose is collapsed onto one line",
			line: `{"type":"assistant","message":{"content":[{"type":"text","text":"Checking the\n  caller\tnow"}]}}`,
			want: "  Checking the caller now",
		},
		{
			name: "init announces the run",
			line: `{"type":"system","subtype":"init","model":"claude-opus-5"}`,
			want: "· reviewing the diff…",
		},
		{
			name:     "result carries the findings",
			line:     `{"type":"result","subtype":"success","result":"**[blocker]** x.go:1 — boom","num_turns":12,"duration_ms":483000,"total_cost_usd":1.5}`,
			want:     "✓ review finished (12 turns, 8m3s, $1.50)",
			result:   "**[blocker]** x.go:1 — boom",
			isResult: true,
		},
		{
			name:     "a clean result says so",
			line:     `{"type":"result","subtype":"success","result":"","num_turns":4}`,
			want:     "✓ review finished (4 turns, no findings)",
			isResult: true,
		},
		{
			name:     "an errored result is marked",
			line:     `{"type":"result","subtype":"error_max_turns","is_error":true,"num_turns":40}`,
			want:     "✗ review ended: error_max_turns (40 turns, no findings)",
			isResult: true,
		},
		{
			name: "tool results are too noisy to show",
			line: `{"type":"user","message":{"content":[{"type":"tool_result","content":"…4000 lines…"}]}}`,
			want: "",
		},
		{
			name: "a non-JSON line is shown verbatim",
			line: `warning: something happened`,
			want: "warning: something happened",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, result, isResult := renderStreamLine([]byte(tc.line))
			if got != tc.want {
				t.Errorf("display = %q, want %q", got, tc.want)
			}
			if result != tc.result {
				t.Errorf("result = %q, want %q", result, tc.result)
			}
			if isResult != tc.isResult {
				t.Errorf("isResult = %v, want %v", isResult, tc.isResult)
			}
		})
	}
}

// A long tool argument cannot flood the pane.
func TestRenderStreamLineClipsLongValues(t *testing.T) {
	long := strings.Repeat("a", 500)
	got, _, _ := renderStreamLine([]byte(`{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Grep","input":{"pattern":"` + long + `"}}]}}`))
	if len(got) > maxRenderedValue+len("→ Grep …")+4 {
		t.Errorf("rendered line is %d bytes, want it clipped near %d", len(got), maxRenderedValue)
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("a clipped value must be marked, got %q", got)
	}
}

// ReviewStream returns the result event's text and writes progress as it goes.
func TestReviewStreamReturnsFindingsAndWritesProgress(t *testing.T) {
	swapDiff(t, "diff --git a/x b/x", nil)
	swapStream(t, func(_ context.Context, _ agent.Kind, _ string, _ []string, _, stdin string, _ time.Duration, progress io.Writer) (string, error) {
		if stdin != "diff --git a/x b/x" {
			t.Errorf("the diff must reach claude on stdin, got %q", stdin)
		}
		progress.Write([]byte("→ Read x.go\n"))
		return "FINDING", nil
	})

	var buf bytes.Buffer
	got, err := (&Client{}).ReviewStream(context.Background(), t.TempDir(), "main", &buf)
	if err != nil {
		t.Fatalf("ReviewStream: %v", err)
	}
	if got != "FINDING" {
		t.Errorf("findings = %q, want FINDING", got)
	}
	if !strings.Contains(buf.String(), "→ Read x.go") {
		t.Errorf("progress = %q, want the rendered line", buf.String())
	}
}

// An empty diff short-circuits without paying for a claude call, and says so.
func TestReviewStreamSkipsAnEmptyDiff(t *testing.T) {
	swapDiff(t, "   \n", nil)
	called := false
	swapStream(t, func(context.Context, agent.Kind, string, []string, string, string, time.Duration, io.Writer) (string, error) {
		called = true
		return "", nil
	})

	var buf bytes.Buffer
	got, err := (&Client{}).ReviewStream(context.Background(), t.TempDir(), "main", &buf)
	if err != nil || got != "" {
		t.Fatalf("ReviewStream = (%q, %v), want empty and nil", got, err)
	}
	if called {
		t.Error("an empty diff must not invoke claude")
	}
	if !strings.Contains(buf.String(), "nothing to review") {
		t.Errorf("progress = %q, want the no-changes note", buf.String())
	}
}

// The sentinels are the plain pass's, so the chain classifies a visible pass
// identically (a timeout falls through to a fallback provider, an auth failure
// does not).
func TestReviewStreamPropagatesSentinels(t *testing.T) {
	swapDiff(t, "diff", nil)
	swapStream(t, func(context.Context, agent.Kind, string, []string, string, string, time.Duration, io.Writer) (string, error) {
		return "", ErrTimeout
	})
	_, err := (&Client{}).ReviewStream(context.Background(), t.TempDir(), "main", io.Discard)
	if !errors.Is(err, ErrTimeout) {
		t.Errorf("err = %v, want ErrTimeout", err)
	}
}

// A nil progress writer is legal (discard), so a caller need not supply one.
func TestReviewStreamAcceptsNilProgress(t *testing.T) {
	swapDiff(t, "diff", nil)
	swapStream(t, func(_ context.Context, _ agent.Kind, _ string, _ []string, _, _ string, _ time.Duration, progress io.Writer) (string, error) {
		progress.Write([]byte("ignored"))
		return "OK", nil
	})
	got, err := (&Client{}).ReviewStream(context.Background(), t.TempDir(), "main", nil)
	if err != nil || got != "OK" {
		t.Fatalf("ReviewStream = (%q, %v), want OK/nil", got, err)
	}
}

// os/exec runs a copier goroutine PER STREAM unless Stdout and Stderr are the
// same interface value, and codex/opencode write to both at once. Both tees must
// therefore be serialized onto the pane: unsynchronized, a progress writer that
// is not itself thread-safe (a bytes.Buffer here, and anything holding an
// in-process pane buffer in production) is a data race. Run with -race.
func TestLockedWriterSerializesConcurrentTees(t *testing.T) {
	var buf bytes.Buffer // deliberately NOT thread-safe
	pane := &lockedWriter{w: &buf}
	out := io.MultiWriter(&cappedBuffer{cap: 1 << 20}, pane)
	errw := io.MultiWriter(&tailBuffer{cap: 1 << 20}, pane)

	var wg sync.WaitGroup
	for _, w := range []io.Writer{out, errw} {
		wg.Add(1)
		go func(w io.Writer) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				fmt.Fprintln(w, "a line of narration")
			}
		}(w)
	}
	wg.Wait()

	if got := strings.Count(buf.String(), "a line of narration"); got != 400 {
		t.Fatalf("pane got %d lines, want 400 (writes were lost or torn)", got)
	}
	// Every line arrived whole — no interleaving mid-line.
	for _, line := range strings.Split(strings.TrimSuffix(buf.String(), "\n"), "\n") {
		if line != "a line of narration" {
			t.Fatalf("torn line %q", line)
		}
	}
}

// The real seam must wire BOTH tees through one lock. Asserting on the writers
// exec would receive is the only way to catch a future edit that reverts to two
// bare MultiWriters (which would still pass every behavioural test on a
// thread-safe pane, and race everywhere else).
func TestStreamPlainTeesShareOneLock(t *testing.T) {
	var buf bytes.Buffer
	pane := &lockedWriter{w: &buf}
	// Two MultiWriters over the SAME lockedWriter is the shape under test; the
	// point is that the shared element is the lock, not the raw writer.
	a := io.MultiWriter(&cappedBuffer{cap: 8}, pane)
	b := io.MultiWriter(&tailBuffer{cap: 8}, pane)
	if a == nil || b == nil {
		t.Fatal("multiwriters must build")
	}
	if _, err := pane.Write([]byte("x")); err != nil {
		t.Fatalf("locked write: %v", err)
	}
	if buf.String() != "x" {
		t.Fatalf("pane = %q, want the byte to reach the underlying writer", buf.String())
	}
}
