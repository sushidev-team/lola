package agent

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestValid(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"claude", true},
		{"codex", true},
		{"opencode", true},
		{"", false},       // empty means "inherit", not valid on its own
		{"Claude", false}, // strict: no case folding
		{"CODEX", false},
		{" codex", false}, // strict: no trimming
		{"claude ", false},
		{"cursor", false},
		{"gpt", false},
	}
	for _, c := range cases {
		if got := Valid(c.in); got != c.want {
			t.Errorf("Valid(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestParse(t *testing.T) {
	cases := []struct {
		in   string
		want Kind
	}{
		{"claude", Claude},
		{"codex", Codex},
		{"opencode", OpenCode},
		{"", Claude},       // empty -> default
		{"bogus", Claude},  // unknown -> default
		{"CLAUDE", Claude}, // lenient: case folded
		{"Codex", Codex},   // lenient: case folded
		{"OpenCode", OpenCode},
		{"  codex  ", Codex}, // lenient: trimmed
		{"\topencode\n", OpenCode},
	}
	for _, c := range cases {
		if got := Parse(c.in); got != c.want {
			t.Errorf("Parse(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestKinds(t *testing.T) {
	want := []Kind{Claude, Codex, OpenCode}
	if !reflect.DeepEqual(Kinds, want) {
		t.Errorf("Kinds = %v, want %v", Kinds, want)
	}
	// Every listed kind must be Valid and round-trip through Parse/String.
	for _, k := range Kinds {
		if !Valid(k.String()) {
			t.Errorf("Kinds member %q is not Valid", k)
		}
		if Parse(k.String()) != k {
			t.Errorf("Parse(%q) did not round-trip to %q", k.String(), k)
		}
	}
}

func TestString(t *testing.T) {
	cases := map[Kind]string{
		Claude:   "claude",
		Codex:    "codex",
		OpenCode: "opencode",
	}
	for k, want := range cases {
		if got := k.String(); got != want {
			t.Errorf("%v.String() = %q, want %q", k, got, want)
		}
	}
}

func TestBinary(t *testing.T) {
	cases := []struct {
		k    Kind
		want string
	}{
		{Claude, "claude"},
		{Codex, "codex"},
		{OpenCode, "opencode"},
		{Kind("bogus"), "claude"}, // unknown falls back to claude
		{Kind(""), "claude"},
	}
	for _, c := range cases {
		if got := c.k.Binary(); got != c.want {
			t.Errorf("Kind(%q).Binary() = %q, want %q", c.k, got, c.want)
		}
	}
}

func TestLaunchArgs(t *testing.T) {
	const prompt = "do the thing"
	cases := []struct {
		k    Kind
		want []string
	}{
		{Claude, []string{"--settings", ".lola/settings.json", prompt}},
		{Codex, []string{"--approve-for-me", prompt}},
		{OpenCode, []string{"--prompt", prompt, "--auto"}},
		// Unknown kinds are treated as Claude.
		{Kind("bogus"), []string{"--settings", ".lola/settings.json", prompt}},
		{Kind(""), []string{"--settings", ".lola/settings.json", prompt}},
	}
	for _, c := range cases {
		got := LaunchArgs(c.k, prompt)
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("LaunchArgs(%q, prompt) = %v, want %v", c.k, got, c.want)
		}
	}
}

func TestLaunchArgsResume(t *testing.T) {
	// Claude resumes its prior conversation via --continue and drops the
	// positional prompt (the saved transcript carries the context).
	got := LaunchArgsResume(Claude, "PROMPT")
	want := []string{"--settings", ".lola/settings.json", "--continue"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("LaunchArgsResume(claude) = %v, want %v", got, want)
	}
	if reflect.DeepEqual(got, LaunchArgs(Claude, "PROMPT")) {
		t.Error("claude resume must differ from a fresh launch (add --continue)")
	}
	// OpenCode resumes its prior session via --continue --auto, with no
	// --prompt: --continue + --prompt is broken upstream
	// (anomalyco/opencode#8850), and the resumed session already carries the
	// task context. --auto keeps the revived session unattended.
	oc := LaunchArgsResume(OpenCode, "PROMPT")
	if want := []string{"--continue", "--auto"}; !reflect.DeepEqual(oc, want) {
		t.Errorf("LaunchArgsResume(opencode) = %v, want %v", oc, want)
	}
	if reflect.DeepEqual(oc, LaunchArgs(OpenCode, "PROMPT")) {
		t.Error("opencode resume must differ from a fresh launch (drop --prompt, add --continue)")
	}
	for _, arg := range oc {
		if arg == "--prompt" {
			t.Error("opencode resume must not pass --prompt (broken with --continue upstream)")
		}
	}
	// --last keeps cwd filtering; --all could select another worktree's task.
	if got := LaunchArgsResume(Codex, "PROMPT"); !reflect.DeepEqual(got, []string{"resume", "--last", "--approve-for-me"}) {
		t.Errorf("LaunchArgsResume(codex) = %v, want cwd-filtered auto-review resume", got)
	}
}

func TestCodexAutoReviewOnlyForWorkers(t *testing.T) {
	for _, args := range [][]string{LaunchArgs(Codex, "task"), LaunchArgsResume(Codex, "task")} {
		if !slices.Contains(args, "--approve-for-me") {
			t.Fatalf("worker must automatically review escalations: %v", args)
		}
		for _, forbidden := range []string{"never", "--yolo", "--dangerously-bypass-approvals-and-sandbox", "danger-full-access", "--all"} {
			if slices.Contains(args, forbidden) {
				t.Errorf("worker disables review or isolation with %q: %v", forbidden, args)
			}
		}
	}
	for _, args := range [][]string{ReviewArgs(Codex, "review", ""), ReviewStreamArgs(Codex, "review", "")} {
		if slices.Contains(args, "--approve-for-me") || !slices.Contains(args, "read-only") {
			t.Errorf("review must retain its read-only posture: %v", args)
		}
	}
}

func TestLaunchArgsPromptIsLastForPositionalAgents(t *testing.T) {
	// Claude and Codex take the prompt positionally: it must be the final argv
	// element. OpenCode passes it as the value of --prompt (asserted above).
	for _, k := range []Kind{Claude, Codex} {
		args := LaunchArgs(k, "P")
		if args[len(args)-1] != "P" {
			t.Errorf("Kind(%q): prompt not last argv element: %v", k, args)
		}
	}
}

func TestOpenCodePluginJS(t *testing.T) {
	const bin = "/usr/local/bin/lola"
	body := string(OpenCodePluginJS(bin))

	// The binary is embedded as a JSON/JS string literal const.
	if !strings.Contains(body, `const lolaBin = "/usr/local/bin/lola";`) {
		t.Errorf("plugin missing lolaBin const:\n%s", body)
	}
	// It must export the plugin factory and interpolate lolaBin via Bun's $.
	if !strings.Contains(body, "export const LolaHook") {
		t.Errorf("plugin does not export LolaHook:\n%s", body)
	}
	// Every event mapping must be present, keyed off event.type and using
	// .quiet().nothrow() so a failing hook can't break the agent's turn. Each
	// command also redirects stdin from /dev/null: Bun's $ inherits the pane
	// TTY as stdin, and `lola hook` must never sit reading a TTY (it never
	// EOFs, and the blocked read eats the agent TUI's keystrokes).
	wants := []string{
		"session.idle",
		"${lolaBin} hook stop < /dev/null`.quiet().nothrow()",
		"permission.asked",
		"${lolaBin} hook notification < /dev/null`.quiet().nothrow()",
		"tool.execute.after",
		"${lolaBin} hook tool_use < /dev/null`.quiet().nothrow()",
	}
	for _, w := range wants {
		if !strings.Contains(body, w) {
			t.Errorf("plugin missing %q:\n%s", w, body)
		}
	}
	// The raw path is never spliced into the shell command directly (only the
	// escaped const is), so an injection-carrying path can't reach the shell.
	if strings.Contains(body, bin+" hook stop") {
		t.Errorf("plugin splices raw path into the command instead of the escaped const:\n%s", body)
	}
}

func TestOpenCodePluginJSEscapesUnsafePath(t *testing.T) {
	// A path with a double quote and backslash must be JSON-escaped so the
	// generated JS stays valid; the const value must decode back to the input.
	const bin = `/Users/me/a"b\c/lola`
	body := string(OpenCodePluginJS(bin))

	const marker = "const lolaBin = "
	i := strings.Index(body, marker)
	if i < 0 {
		t.Fatalf("no lolaBin const:\n%s", body)
	}
	rest := body[i+len(marker):]
	semi := strings.Index(rest, ";")
	if semi < 0 {
		t.Fatalf("lolaBin const not terminated:\n%s", body)
	}
	literal := rest[:semi]
	var decoded string
	if err := json.Unmarshal([]byte(literal), &decoded); err != nil {
		t.Fatalf("lolaBin literal %q is not a valid JSON/JS string: %v", literal, err)
	}
	if decoded != bin {
		t.Errorf("decoded lolaBin = %q, want %q", decoded, bin)
	}
}

func TestParseCodexNotify(t *testing.T) {
	cases := []struct {
		name      string
		in        string
		wantEvent string
		wantMsg   string
		wantType  string
	}{
		{
			name:      "turn complete with message",
			in:        `{"type":"agent-turn-complete","last-assistant-message":"all done"}`,
			wantEvent: "stop",
			wantMsg:   "all done",
			wantType:  "agent-turn-complete",
		},
		{
			name:      "turn complete without message",
			in:        `{"type":"agent-turn-complete"}`,
			wantEvent: "stop",
			wantType:  "agent-turn-complete",
		},
		{
			name:      "turn complete empty message",
			in:        `{"type":"agent-turn-complete","last-assistant-message":""}`,
			wantEvent: "stop",
			wantType:  "agent-turn-complete",
		},
		{
			name:      "approval requested",
			in:        `{"type":"approval-requested","last-assistant-message":"run rm?"}`,
			wantEvent: "notification",
			wantMsg:   "run rm?",
			wantType:  "approval-requested",
		},
		{
			name:      "approval requested no message",
			in:        `{"type":"approval-requested"}`,
			wantEvent: "notification",
			wantType:  "approval-requested",
		},
		{
			name:      "extra fields ignored",
			in:        `{"type":"agent-turn-complete","last-assistant-message":"hi","turn-id":7,"extra":true}`,
			wantEvent: "stop",
			wantMsg:   "hi",
			wantType:  "agent-turn-complete",
		},
		// Unknown / missing / garbage -> skipped by the caller.
		{name: "unknown type", in: `{"type":"agent-message"}`},
		{name: "missing type", in: `{"last-assistant-message":"hi"}`},
		{name: "empty object", in: `{}`},
		{name: "null", in: `null`},
		{name: "empty string", in: ``},
		{name: "malformed json", in: `{"type":`},
		{name: "not an object", in: `123`},
		{name: "json array", in: `["agent-turn-complete"]`},
		{name: "whitespace", in: `   `},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			gotEvent, gotMsg, gotType := ParseCodexNotify(c.in)
			if gotEvent != c.wantEvent || gotMsg != c.wantMsg || gotType != c.wantType {
				t.Errorf("ParseCodexNotify(%q) = (%q, %q, %q), want (%q, %q, %q)",
					c.in, gotEvent, gotMsg, gotType, c.wantEvent, c.wantMsg, c.wantType)
			}
		})
	}
}

// The review argv is the pluggable half of the review system: which agent runs
// is config, so every kind must produce a runnable, READ-ONLY one-shot argv with
// the prompt where that CLI expects it and the diff nowhere near it.
func TestReviewArgs(t *testing.T) {
	const instr = "REVIEW-INSTRUCTION"
	for _, tc := range []struct {
		kind Kind
		want []string
	}{
		{Claude, []string{"-p", instr, "--output-format", "text"}},
		{Codex, []string{"exec", "--sandbox", "read-only", instr}},
		{OpenCode, []string{"run", instr}},
	} {
		got := ReviewArgs(tc.kind, instr, "")
		if !slices.Equal(got, tc.want) {
			t.Errorf("ReviewArgs(%s) = %v, want %v", tc.kind, got, tc.want)
		}
	}
	// An unknown kind reviews with claude rather than producing no argv at all.
	if got := ReviewArgs(Kind("nope"), instr, ""); !slices.Equal(got, ReviewArgs(Claude, instr, "")) {
		t.Errorf("ReviewArgs(unknown) = %v, want claude's argv", got)
	}
}

// codex and opencode take the prompt POSITIONALLY and append piped stdin to it,
// so the instruction must be the LAST element — a flag after it would be read as
// part of the prompt (or eat the prompt as its own value).
func TestReviewArgsKeepThePromptLast(t *testing.T) {
	for _, k := range []Kind{Codex, OpenCode} {
		for _, model := range []string{"", "some-model"} {
			args := ReviewArgs(k, "INSTR", model)
			if args[len(args)-1] != "INSTR" {
				t.Errorf("ReviewArgs(%s, model=%q) = %v, want the prompt last", k, model, args)
			}
		}
	}
}

// A review READS; it must never be able to write. Each agent's most restrictive
// non-interactive posture is the guard, and it is the exact opposite of the
// unattended worker launch — so assert the worker's write-enabling flags are
// absent from every review argv.
func TestReviewArgsAreReadOnly(t *testing.T) {
	forbidden := map[Kind][]string{
		Claude:   {"--dangerously-skip-permissions", "--permission-mode"},
		Codex:    {"workspace-write", "danger-full-access", "--yolo", "--dangerously-bypass-approvals-and-sandbox"},
		OpenCode: {"--auto"},
	}
	for _, k := range Kinds {
		for _, build := range []func() []string{
			func() []string { return ReviewArgs(k, "i", "m") },
			func() []string { return ReviewStreamArgs(k, "i", "m") },
		} {
			args := build()
			for _, bad := range forbidden[k] {
				if slices.Contains(args, bad) {
					t.Errorf("%s review argv must not carry %q: %v", k, bad, args)
				}
			}
		}
	}
	// Codex has no "ask a human" mode in exec, so the sandbox IS the guard.
	if !slices.Contains(ReviewArgs(Codex, "i", ""), "read-only") {
		t.Error("codex review must run under --sandbox read-only")
	}
}

// Only claude needs a different argv to narrate; the other two already do it on
// stderr, and the two predicates are what route the visible pass.
func TestReviewStreamShapes(t *testing.T) {
	if !ReviewStreamsJSON(Claude) || ReviewProgressOnStderr(Claude) {
		t.Error("claude streams a stdout event feed, and says nothing on stderr")
	}
	for _, k := range []Kind{Codex, OpenCode} {
		if ReviewStreamsJSON(k) || !ReviewProgressOnStderr(k) {
			t.Errorf("%s narrates on stderr and emits no stream-json feed", k)
		}
		// ...so its streaming argv must be its plain one, unchanged.
		if !slices.Equal(ReviewStreamArgs(k, "i", "m"), ReviewArgs(k, "i", "m")) {
			t.Errorf("%s streaming argv must equal its plain argv", k)
		}
	}
	if want := []string{"-p", "i", "--output-format", "stream-json", "--verbose", "--model", "m"}; !slices.Equal(ReviewStreamArgs(Claude, "i", "m"), want) {
		t.Errorf("claude stream argv = %v, want %v", ReviewStreamArgs(Claude, "i", "m"), want)
	}
}

// The model flag is the agent's own, and an empty model leaves the agent's
// configured default in place rather than passing an empty value.
func TestReviewArgsModel(t *testing.T) {
	for _, k := range Kinds {
		args := ReviewArgs(k, "i", "")
		if slices.Contains(args, "--model") {
			t.Errorf("%s: empty model must not pass --model: %v", k, args)
		}
		args = ReviewArgs(k, "i", "the-model")
		i := slices.Index(args, "--model")
		if i < 0 || i+1 >= len(args) || args[i+1] != "the-model" {
			t.Errorf("%s: --model the-model missing from %v", k, args)
		}
	}
}

// All four review helpers must normalize a Kind IDENTICALLY. A caller that has
// not pre-normalized must not get claude's argv from one and codex's renderer
// from another — that mismatch shows up as an empty review, not as an error.
func TestReviewHelpersNormalizeAlike(t *testing.T) {
	for _, raw := range []Kind{"Codex", " codex ", "OPENCODE", "opencode", "nope", ""} {
		want := Parse(string(raw))
		if got := ReviewArgs(raw, "i", ""); !slices.Equal(got, ReviewArgs(want, "i", "")) {
			t.Errorf("ReviewArgs(%q) = %v, want %s's argv", raw, got, want)
		}
		if got := ReviewStreamArgs(raw, "i", ""); !slices.Equal(got, ReviewStreamArgs(want, "i", "")) {
			t.Errorf("ReviewStreamArgs(%q) = %v, want %s's argv", raw, got, want)
		}
		if ReviewStreamsJSON(raw) != ReviewStreamsJSON(want) || ReviewProgressOnStderr(raw) != ReviewProgressOnStderr(want) {
			t.Errorf("%q: the stream predicates disagree with %s", raw, want)
		}
		// ...and the argv and the renderer must agree with each other.
		if ReviewStreamsJSON(raw) != slices.Contains(ReviewStreamArgs(raw, "i", ""), "stream-json") {
			t.Errorf("%q: ReviewStreamsJSON disagrees with the streaming argv", raw)
		}
	}
}
