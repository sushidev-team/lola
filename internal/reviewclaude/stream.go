package reviewclaude

// stream.go is the VISIBLE variant of the review pass: the same one-shot
// `claude -p` over the same instruction and the same diff-on-stdin, but asked
// for `--output-format stream-json --verbose` so the run can be WATCHED while
// it happens.
//
// The plain pass (`--output-format text`) prints nothing at all until claude is
// finished, which for a real PR means a blank pane for ten minutes or more —
// useless as a progress display. The stream format emits one JSON object per
// event instead, and this file renders those events into plain lines a human
// can read at a glance ("→ Read app/Foo.php"). The rendered lines go to the
// caller's writer (in production: the review tmux pane's stdout); the FINDINGS
// still come back as the function's return value, taken from the terminal
// `result` event, so every downstream sink is byte-identical to the plain pass.
//
// The events are attacker-influenceable like everything else here (they quote
// the diff and the files it touches). They are rendered for a human to LOOK at
// and are never executed, never re-fed to an agent, and the findings still pass
// the caller's sanitize + idle gate before they reach a worker's pane.

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"
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

// ReviewStream runs the review like Review, but streams progress: it renders
// each stream-json event as a plain line to progress (nil discards them) and
// returns the trimmed, size-capped findings from the terminal result event. A
// clean review still returns ("", nil), and the failure classes are the same
// sentinels, so a caller can swap between Review and ReviewStream freely.
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
	out, err := runClaudeStream(ctx, c.bin(), c.Model, reviewInstruction, worktreeDir,
		capText(diff, maxDiffBytes), c.timeout(), progress)
	if err != nil {
		return "", err
	}
	return capText(strings.TrimSpace(out), maxOutputBytes), nil
}

// runClaudeStream is the streaming exec seam (the sibling of runClaude). Tests
// override it to assert argv/stdin/timeout without running claude. The real
// implementation applies the hard timeout, runs in worktreeDir, streams the
// diff on stdin, renders every event line to progress as it arrives, and
// classifies failures into the same Err* sentinels.
var runClaudeStream = func(ctx context.Context, bin, model, instruction, dir, stdin string, timeout time.Duration, progress io.Writer) (string, error) {
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(cctx, bin, buildStreamArgs(model, instruction)...)
	cmd.Dir = dir
	cmd.Stdin = strings.NewReader(stdin)
	stderr := &cappedBuffer{cap: maxStderrBytes}
	cmd.Stderr = stderr
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		return "", fmt.Errorf("reviewclaude: claude run failed: %s", redactSecrets(err.Error()))
	}
	if err := cmd.Start(); err != nil {
		// Start reports "executable not found" here, where Run would have
		// reported it below — classify it the same way.
		return "", classifyRunErr(err, cctx.Err(), stderr.String(), "", timeout)
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
	if e := classifyRunErr(err, cctx.Err(), stderr.String(), result, timeout); e != nil {
		return "", e
	}
	if scanErr != nil {
		return "", fmt.Errorf("reviewclaude: reading the review stream failed: %s", redactSecrets(scanErr.Error()))
	}
	return result, nil
}

// buildStreamArgs assembles the streaming argv. `--verbose` is REQUIRED by the
// CLI alongside `--output-format stream-json` in print mode; the diff is never
// here (it goes on stdin).
func buildStreamArgs(model, instruction string) []string {
	args := []string{"-p", instruction, "--output-format", "stream-json", "--verbose"}
	if model != "" {
		args = append(args, "--model", model)
	}
	return args
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
