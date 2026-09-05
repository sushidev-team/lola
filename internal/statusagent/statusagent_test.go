package statusagent

import (
	"context"
	"errors"
	"github.com/sushidev-team/lola/internal/agent"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The exec seam receives the right argv shape: instruction on -p, context on
// STDIN (never argv), model only when set, the configured timeout.
func TestInterpretArgvAndStdin(t *testing.T) {
	orig := runAgent
	t.Cleanup(func() { runAgent = orig })

	var gotBin, gotModel, gotInstr, gotStdin string
	var gotTimeout time.Duration
	runAgent = func(ctx context.Context, kind agent.Kind, bin, model, instruction, stdin string, timeout time.Duration) (string, error) {
		gotBin, gotModel, gotInstr, gotStdin, gotTimeout = bin, model, instruction, stdin, timeout
		return `{"agent_state":"working","headline":"x","waiting_on":"","confidence":0.9}`, nil
	}

	c := &Client{Model: "sonnet", Timeout: 42 * time.Second}
	out, err := c.Interpret(context.Background(), "PANE TEXT")
	if err != nil {
		t.Fatalf("Interpret: %v", err)
	}
	if out == "" {
		t.Fatal("empty output")
	}
	if gotBin != "claude" || gotModel != "sonnet" || gotTimeout != 42*time.Second {
		t.Errorf("bin=%q model=%q timeout=%v", gotBin, gotModel, gotTimeout)
	}
	if gotInstr != Instruction {
		t.Errorf("instruction not the package const")
	}
	if gotStdin != "PANE TEXT" {
		t.Errorf("stdin = %q, want the context", gotStdin)
	}
	if strings.Contains(gotInstr, "PANE TEXT") {
		t.Error("context leaked onto argv")
	}
}

func TestInterpretCapsContext(t *testing.T) {
	orig := runAgent
	t.Cleanup(func() { runAgent = orig })
	var gotStdin string
	runAgent = func(ctx context.Context, kind agent.Kind, bin, model, instruction, stdin string, timeout time.Duration) (string, error) {
		gotStdin = stdin
		return "{}", nil
	}
	huge := strings.Repeat("x", 64*1024)
	_, _ = (&Client{}).Interpret(context.Background(), huge)
	if len(gotStdin) > maxContextBytes {
		t.Fatalf("stdin len = %d, want <= %d", len(gotStdin), maxContextBytes)
	}
	if !strings.HasSuffix(gotStdin, truncMarker) {
		t.Error("truncated context must end with the marker")
	}
}

func TestClassifyRunErr(t *testing.T) {
	if err := classifyRunErr(nil, context.DeadlineExceeded, "", time.Second); !errors.Is(err, ErrTimeout) {
		t.Errorf("deadline → %v, want ErrTimeout", err)
	}
	if err := classifyRunErr(exec.ErrNotFound, nil, "", time.Second); !errors.Is(err, ErrNotFound) {
		t.Errorf("not found → %v, want ErrNotFound", err)
	}
	if err := classifyRunErr(&exec.ExitError{}, nil, "boom", time.Second); !errors.Is(err, ErrNonZeroExit) {
		t.Errorf("exit → %v, want ErrNonZeroExit", err)
	}
	if err := classifyRunErr(nil, nil, "", time.Second); err != nil {
		t.Errorf("clean run → %v, want nil", err)
	}
}

func TestParse(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		want    Interpretation
		wantErr bool
	}{
		{
			name: "clean object",
			in:   `{"agent_state":"working","headline":"running the test suite","waiting_on":"","confidence":0.8}`,
			want: Interpretation{AgentState: "working", Headline: "running the test suite", Confidence: 0.8},
		},
		{
			name: "fenced with prose",
			in:   "Sure! Here is the judgement:\n```json\n{\"agent_state\":\"stuck\",\"headline\":\"looping on a failing build\",\"waiting_on\":\"\",\"confidence\":0.6}\n```\nHope that helps.",
			want: Interpretation{AgentState: "stuck", Headline: "looping on a failing build", Confidence: 0.6},
		},
		{
			name: "first object wins",
			in:   `{"agent_state":"idle","headline":"turn finished","confidence":1} {"agent_state":"working","headline":"nope","confidence":1}`,
			want: Interpretation{AgentState: "idle", Headline: "turn finished", Confidence: 1},
		},
		{
			name: "brace inside a string does not truncate",
			in:   `{"agent_state":"working","headline":"editing main() { } body","confidence":0.7}`,
			want: Interpretation{AgentState: "working", Headline: "editing main() { } body", Confidence: 0.7},
		},
		{
			name: "waiting with reason",
			in:   `{"agent_state":"waiting_input","headline":"asking which auth flow to keep","waiting_on":"a choice between OAuth and sessions","confidence":0.9}`,
			want: Interpretation{AgentState: "waiting_input", Headline: "asking which auth flow to keep", WaitingOn: "a choice between OAuth and sessions", Confidence: 0.9},
		},
		{
			name: "unknown with empty headline is valid",
			in:   `{"agent_state":"unknown","headline":"","waiting_on":"","confidence":0}`,
			want: Interpretation{AgentState: "unknown"},
		},
		{
			name: "confidence clamped high",
			in:   `{"agent_state":"working","headline":"x","confidence":7}`,
			want: Interpretation{AgentState: "working", Headline: "x", Confidence: 1},
		},
		{
			name: "confidence clamped low",
			in:   `{"agent_state":"working","headline":"x","confidence":-2}`,
			want: Interpretation{AgentState: "working", Headline: "x", Confidence: 0},
		},
		{
			name: "newlines and ANSI stripped from headline",
			in:   "{\"agent_state\":\"working\",\"headline\":\"line one\\nline \\u001b[31mtwo\\u001b[0m\",\"confidence\":0.5}",
			want: Interpretation{AgentState: "working", Headline: "line one line two", Confidence: 0.5},
		},
		{name: "no json", in: "the agent seems busy", wantErr: true},
		{name: "unbalanced", in: `{"agent_state":"working"`, wantErr: true},
		{name: "unlisted state", in: `{"agent_state":"needs_input","headline":"x","confidence":1}`, wantErr: true},
		{name: "empty state", in: `{"headline":"x","confidence":1}`, wantErr: true},
		{name: "empty headline on known state", in: `{"agent_state":"working","headline":"  ","confidence":1}`, wantErr: true},
		{name: "empty input", in: "", wantErr: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := Parse(c.in)
			if c.wantErr {
				if err == nil {
					t.Fatalf("Parse(%q) = %+v, want error", c.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if got != c.want {
				t.Errorf("Parse = %+v, want %+v", got, c.want)
			}
		})
	}
}

func TestParseCapsRunes(t *testing.T) {
	long := strings.Repeat("héadline ", 40) // multibyte, > 120 runes
	in := `{"agent_state":"working","headline":"` + long + `","confidence":0.5}`
	got, err := Parse(in)
	if err != nil {
		t.Fatal(err)
	}
	if n := len([]rune(got.Headline)); n > maxHeadlineRunes {
		t.Fatalf("headline runes = %d, want <= %d", n, maxHeadlineRunes)
	}
	if !strings.HasSuffix(got.Headline, "…") {
		t.Error("capped headline must end with an ellipsis")
	}
}

func TestInterpretUsesSelectedProvider(t *testing.T) {
	original := runAgent
	t.Cleanup(func() { runAgent = original })
	for _, kind := range agent.Kinds {
		runAgent = func(ctx context.Context, got agent.Kind, bin, model, instruction, stdin string, timeout time.Duration) (string, error) {
			if got != kind || bin != kind.Binary() || model != "custom-model" || stdin != "pane data" {
				t.Fatalf("got %s %s %s %s", got, bin, model, stdin)
			}
			return "{}", nil
		}
		if _, err := (&Client{Agent: kind, Model: "custom-model"}).Interpret(context.Background(), "pane data"); err != nil {
			t.Fatal(err)
		}
	}
}

func TestProviderExecutableReceivesHeadlessArguments(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "fake-agent")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\"\ncat\n"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		kind   agent.Kind
		prefix string
	}{
		{agent.Claude, "-p\n"},
		{agent.Codex, "exec\n--sandbox\nread-only\n--model\ncustom-model\n"},
		{agent.OpenCode, "run\n--model\ncustom-model\n"},
	} {
		out, err := (&Client{Agent: tc.kind, Bin: bin, Model: "custom-model"}).Interpret(context.Background(), "OBSERVED DATA")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(out, tc.prefix) || !strings.Contains(out, Instruction) || !strings.HasSuffix(out, "OBSERVED DATA") {
			t.Fatalf("%s command/context mismatch: %s", tc.kind, out)
		}
	}
}
