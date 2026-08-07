package reviewclaude

// Tests for the VISIBLE pass (stream.go): the stream-json argv, the rendering
// of each event kind into a plain progress line, and ReviewStream's contract —
// identical findings, identical clean case, identical sentinels.

import (
	"bytes"
	"context"
	"errors"
	"io"
	"slices"
	"strings"
	"testing"
	"time"
)

// swapStream installs a fake streaming exec seam for the duration of a test.
func swapStream(t *testing.T, fn func(ctx context.Context, bin, model, instruction, dir, stdin string, timeout time.Duration, progress io.Writer) (string, error)) {
	t.Helper()
	orig := runClaudeStream
	runClaudeStream = fn
	t.Cleanup(func() { runClaudeStream = orig })
}

// swapDiff installs a fake git-diff seam.
func swapDiff(t *testing.T, out string, err error) {
	t.Helper()
	orig := runGitDiff
	runGitDiff = func(context.Context, string, string) (string, error) { return out, err }
	t.Cleanup(func() { runGitDiff = orig })
}

func TestStreamArgsAskForTheStreamFormat(t *testing.T) {
	got := buildStreamArgs("sonnet", "REVIEW-INSTRUCTION")
	want := []string{"-p", "REVIEW-INSTRUCTION", "--output-format", "stream-json", "--verbose", "--model", "sonnet"}
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Errorf("argv = %v, want %v", got, want)
	}
	// --verbose is mandatory alongside stream-json in print mode; without it the
	// CLI refuses to start and every visible pass would fail.
	if !slices.Contains(buildStreamArgs("", "x"), "--verbose") {
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
	swapStream(t, func(_ context.Context, _, _, _, _, stdin string, _ time.Duration, progress io.Writer) (string, error) {
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
	swapStream(t, func(context.Context, string, string, string, string, string, time.Duration, io.Writer) (string, error) {
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
	swapStream(t, func(context.Context, string, string, string, string, string, time.Duration, io.Writer) (string, error) {
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
	swapStream(t, func(_ context.Context, _, _, _, _, _ string, _ time.Duration, progress io.Writer) (string, error) {
		progress.Write([]byte("ignored"))
		return "OK", nil
	})
	got, err := (&Client{}).ReviewStream(context.Background(), t.TempDir(), "main", nil)
	if err != nil || got != "OK" {
		t.Fatalf("ReviewStream = (%q, %v), want OK/nil", got, err)
	}
}
