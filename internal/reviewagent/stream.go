package reviewagent

// stream.go is the VISIBLE variant of the review pass: the same one-shot agent
// invocation over the same instruction and the same diff-on-stdin, but arranged
// so the run can be WATCHED while it happens.
//
// The agents divide into two shapes, which is why there are two seams:
//
//   - CLAUDE narrates nothing. A plain `claude -p --output-format text` prints
//     absolutely nothing until it finishes, which for a real PR means a blank
//     pane for ten minutes or more — useless as a progress display. So the
//     visible pass asks for `--output-format stream-json --verbose`, which emits
//     one JSON object per event, and this file renders those events into plain
//     lines a human can read at a glance ("→ Read app/Foo.php"). The FINDINGS
//     still come back from the terminal `result` event, so every downstream sink
//     is byte-identical to the plain pass.
//   - CODEX and OPENCODE already narrate: they write their progress to STDERR
//     and only the answer to stdout. Their argv is therefore unchanged and the
//     visible pass simply TEES both streams to the pane, capturing stdout as the
//     findings exactly as the plain pass does.
//
// Either way the pane text is attacker-influenceable like everything else here
// (it quotes the diff and the files it touches). It is rendered for a human to
// LOOK at and is never executed, never re-fed to an agent, and the findings
// still pass the caller's sanitize + idle gate before they reach a worker's pane.

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/sushidev-team/lola/internal/agent"
)

const (
	// maxStreamLineBytes bounds ONE stream-json line. Claude Code emits whole
	// tool results as single lines, so the scanner needs a generous ceiling; a
	// line past it ends the scan with an error rather than growing unbounded.
	maxStreamLineBytes = 4 * 1024 * 1024
	// maxRenderedValue bounds a rendered tool argument (a file path, a pattern,
	// a command) so one absurd argument cannot flood the pane.
	maxRenderedValue = 100
	// maxRenderedText bounds a rendered assistant text block for the same reason.
	// The full text is never needed on screen — the findings arrive intact in
	// the result event.
	maxRenderedText = 400
)

// ReviewStream runs the review like Review, but streams progress to progress
// (nil discards it) and returns the same trimmed, size-capped findings. A clean
// review still returns ("", nil), and the failure classes are the same
// sentinels, so a caller can swap between Review and ReviewStream freely — and
// so can it swap agents: which of the two streaming shapes runs is decided here,
// from the agent alone.
func (c *Client) ReviewStream(ctx context.Context, worktreeDir, baseBranch string, progress io.Writer) (string, error) {
	if progress == nil {
		progress = io.Discard
	}
	diff, err := runGitDiff(ctx, worktreeDir, baseBranch)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(diff) == "" {
		fmt.Fprintln(progress, "· nothing to review: the branch has no changes against "+baseBranch)
		return "", nil
	}
	k := c.kind()
	args := agent.ReviewStreamArgs(k, reviewInstruction, c.Model)
	stdin := capText(diff, maxDiffBytes)
	fmt.Fprintf(progress, "· %s reviewing %s onto %s\n", c.bin(), worktreeDir, baseBranch)

	var out string
	if agent.ReviewStreamsJSON(k) {
		out, err = runAgentStreamJSON(ctx, k, c.bin(), args, worktreeDir, stdin, c.timeout(), progress)
	} else {
		out, err = runAgentStreamPlain(ctx, k, c.bin(), args, worktreeDir, stdin, c.timeout(), progress)
	}
	if err != nil {
		return "", err
	}
	return capText(strings.TrimSpace(out), maxOutputBytes), nil
}

// runAgentStreamJSON is claude's streaming exec seam (the sibling of runAgent).
// Tests override it to assert argv/stdin/timeout without running claude. The
// real implementation applies the hard timeout, runs in dir, streams the diff on
// stdin, renders every stream-json event line to progress as it arrives, and
// classifies failures into the same Err* sentinels.
var runAgentStreamJSON = func(ctx context.Context, k agent.Kind, bin string, args []string, dir, stdin string, timeout time.Duration, progress io.Writer) (string, error) {
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(cctx, bin, args...)
	cmd.Dir = dir
	cmd.Stdin = strings.NewReader(stdin)
	stderr := &tailBuffer{cap: maxStderrBytes}
	cmd.Stderr = stderr
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		return "", fmt.Errorf("reviewagent: %s run failed: %s", k.Binary(), redactSecrets(err.Error()))
	}
	if err := cmd.Start(); err != nil {
		// Start reports "executable not found" here, where Run would have
		// reported it below — classify it the same way.
		return "", classifyRunErr(k, err, cctx.Err(), stderr.String(), "", timeout)
	}

	// Render as the events arrive; the pane is a live display, so nothing is
	// buffered until the end. The result text is kept for the return value.
	var result string
	sc := bufio.NewScanner(pipe)
	sc.Buffer(make([]byte, 0, 64*1024), maxStreamLineBytes)
	for sc.Scan() {
		line, res, isResult := renderStreamLine(sc.Bytes())
		if line != "" {
			fmt.Fprintln(progress, line)
		}
		if isResult {
			result = res
		}
	}
	scanErr := sc.Err()

	err = cmd.Wait()
	if e := classifyRunErr(k, err, cctx.Err(), stderr.String(), result, timeout); e != nil {
		return "", e
	}
	if scanErr != nil {
		return "", fmt.Errorf("reviewagent: reading the review stream failed: %s", redactSecrets(scanErr.Error()))
	}
	return result, nil
}

// runAgentStreamPlain is the streaming exec seam for the agents that narrate on
// STDERR (codex, opencode). It is runAgent with two writers teed onto the pane:
// stdout, which carries the findings and is still captured under the SAME cap,
// and stderr, which carries the progress a human watches and is still buffered
// (capped) for error classification. Teeing never widens a cap — the pane gets a
// copy, the buffers decide what lola keeps.
//
// Both streams reach the child as PIPES, never a TTY, which is what makes codex
// and opencode put their answer on stdout at all (each suppresses that copy when
// it detects an interactive terminal).
var runAgentStreamPlain = func(ctx context.Context, k agent.Kind, bin string, args []string, dir, stdin string, timeout time.Duration, progress io.Writer) (string, error) {
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(cctx, bin, args...)
	cmd.Dir = dir
	cmd.Stdin = strings.NewReader(stdin)
	stdout := &cappedBuffer{cap: maxCaptureBytes}
	stderr := &tailBuffer{cap: maxStderrBytes}
	// ONE lock over the pane. os/exec reuses a single copier goroutine only when
	// Stdout and Stderr are the same interface value (interfaceEqual); two
	// distinct MultiWriters are not, so exec runs a goroutine per stream and both
	// would write to progress concurrently — and these agents write to both
	// streams at once. Unsynchronized that is interleaved output at best and, for
	// a progress writer that is not itself thread-safe, a data race.
	pane := &lockedWriter{w: progress}
	cmd.Stdout = io.MultiWriter(stdout, pane)
	cmd.Stderr = io.MultiWriter(stderr, pane)

	err := cmd.Run()
	if e := classifyRunErr(k, err, cctx.Err(), stderr.String(), stdout.String(), timeout); e != nil {
		return "", e
	}
	return stdout.String(), nil
}

// streamEvent is the SUBSET of a stream-json event this renderer needs. Unknown
// types and unknown fields are ignored, so a newer CLI that adds events (or
// fields) renders fewer lines rather than failing.
type streamEvent struct {
	Type    string `json:"type"`
	Subtype string `json:"subtype"`
	Message struct {
		Content []struct {
			Type  string         `json:"type"`
			Text  string         `json:"text"`
			Name  string         `json:"name"`
			Input map[string]any `json:"input"`
		} `json:"content"`
	} `json:"message"`
	Result     string  `json:"result"`
	IsError    bool    `json:"is_error"`
	NumTurns   int     `json:"num_turns"`
	DurationMS int     `json:"duration_ms"`
	TotalCost  float64 `json:"total_cost_usd"`
}

// renderStreamLine turns ONE stream-json line into a display line. It returns
// the rendered text ("" when the event is not worth showing), the result text,
// and whether this was the terminal result event. A line that is not valid JSON
// is passed through verbatim (claude may print a plain warning), bounded — the
// pane must show whatever really happened, not swallow it.
func renderStreamLine(line []byte) (display, result string, isResult bool) {
	trimmed := strings.TrimSpace(string(line))
	if trimmed == "" {
		return "", "", false
	}
	var ev streamEvent
	if err := json.Unmarshal([]byte(trimmed), &ev); err != nil {
		return clip(trimmed, maxRenderedText), "", false
	}
	switch ev.Type {
	case "system":
		if ev.Subtype == "init" {
			return "· reviewing the diff…", "", false
		}
		return "", "", false
	case "assistant":
		return renderAssistant(ev), "", false
	case "result":
		return renderResult(ev), ev.Result, true
	default:
		// "user" carries tool RESULTS (whole file contents): far too noisy for a
		// progress display, and the interesting half is the tool call above it.
		return "", "", false
	}
}

// renderAssistant renders one assistant turn: its tool calls as "→ Read path"
// lines and any prose as an indented line. A turn with several blocks renders
// one line per block.
func renderAssistant(ev streamEvent) string {
	var lines []string
	for _, b := range ev.Message.Content {
		switch b.Type {
		case "tool_use":
			if arg := toolArg(b.Input); arg != "" {
				lines = append(lines, "→ "+b.Name+" "+arg)
				continue
			}
			lines = append(lines, "→ "+b.Name)
		case "text":
			if t := strings.TrimSpace(b.Text); t != "" {
				lines = append(lines, "  "+clip(collapse(t), maxRenderedText))
			}
		}
	}
	return strings.Join(lines, "\n")
}

// renderResult renders the terminal event: how it ended, how long it took, and
// whether it found anything. The findings themselves are NOT rendered here —
// they are the return value, and the caller (the visible runner) prints them
// under a heading of its own.
func renderResult(ev streamEvent) string {
	head := "✓ review finished"
	if ev.IsError || ev.Subtype != "" && ev.Subtype != "success" {
		head = "✗ review ended: " + firstNonEmpty(ev.Subtype, "error")
	}
	parts := []string{head}
	if ev.NumTurns > 0 {
		parts = append(parts, fmt.Sprintf("%d turns", ev.NumTurns))
	}
	if ev.DurationMS > 0 {
		parts = append(parts, (time.Duration(ev.DurationMS) * time.Millisecond).Round(time.Second).String())
	}
	if ev.TotalCost > 0 {
		parts = append(parts, fmt.Sprintf("$%.2f", ev.TotalCost))
	}
	if strings.TrimSpace(ev.Result) == "" {
		parts = append(parts, "no findings")
	}
	return parts[0] + " (" + strings.Join(parts[1:], ", ") + ")"
}

// toolArgKeys is the ordered list of tool-input fields worth showing: the first
// present STRING field names what the call is about. Anything else renders as
// the bare tool name.
var toolArgKeys = []string{"file_path", "path", "pattern", "command", "url", "query", "description", "prompt"}

// toolArg picks the one displayable argument of a tool call.
func toolArg(in map[string]any) string {
	for _, k := range toolArgKeys {
		if v, ok := in[k].(string); ok {
			if s := strings.TrimSpace(collapse(v)); s != "" {
				return clip(s, maxRenderedValue)
			}
		}
	}
	return ""
}

// collapse folds every run of whitespace (newlines included) into one space, so
// a multi-line tool argument or paragraph can never break the one-line-per-event
// shape of the display.
func collapse(s string) string { return strings.Join(strings.Fields(s), " ") }

// clip bounds a rendered value on a rune boundary, marking a cut with an ellipsis.
func clip(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return trimPartialRune(s[:max]) + "…"
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// lockedWriter serializes concurrent writes onto one underlying writer. It
// exists for runAgentStreamPlain's two tees; nothing else needs it, because the
// other seams have a single writer each (runAgentStreamJSON renders from the
// caller's own goroutine, and internal/review tees stdout only).
type lockedWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}
