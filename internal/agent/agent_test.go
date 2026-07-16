package agent

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
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
		{"", Claude},        // empty -> default
		{"bogus", Claude},   // unknown -> default
		{"CLAUDE", Claude},  // lenient: case folded
		{"Codex", Codex},    // lenient: case folded
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
		{Codex, []string{"--ask-for-approval", "never", "--sandbox", "workspace-write", prompt}},
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

func TestCodexConfigTOML(t *testing.T) {
	const bin = "/usr/local/bin/lola"
	body := string(CodexConfigTOML(bin))

	// The notify array must be the very first line.
	firstLine := body
	if i := strings.IndexByte(body, '\n'); i >= 0 {
		firstLine = body[:i]
	}
	if !strings.HasPrefix(firstLine, "notify = [") {
		t.Errorf("first line = %q, want it to start the notify array", firstLine)
	}

	// It must be valid TOML and decode notify as the expected argv, top-level.
	var decoded struct {
		Notify []string `toml:"notify"`
	}
	if _, err := toml.Decode(body, &decoded); err != nil {
		t.Fatalf("CodexConfigTOML is not valid TOML: %v\n%s", err, body)
	}
	want := []string{bin, "hook", "codex-notify"}
	if !reflect.DeepEqual(decoded.Notify, want) {
		t.Errorf("notify = %v, want %v", decoded.Notify, want)
	}

	// No [table] header may appear before notify (TOML requires top-level keys
	// first, and lola asserts it explicitly).
	notifyIdx := strings.Index(body, "notify")
	tableIdx := strings.Index(body, "\n[")
	if tableIdx >= 0 && tableIdx < notifyIdx {
		t.Errorf("a [table] header precedes notify:\n%s", body)
	}
}

func TestCodexConfigTOMLQuotesUnsafePath(t *testing.T) {
	const bin = `/Users/me/My "Tools"/lola`
	body := string(CodexConfigTOML(bin))

	var decoded struct {
		Notify []string `toml:"notify"`
	}
	if _, err := toml.Decode(body, &decoded); err != nil {
		t.Fatalf("CodexConfigTOML with unsafe path is not valid TOML: %v\n%s", err, body)
	}
	if len(decoded.Notify) != 3 || decoded.Notify[0] != bin {
		t.Errorf("notify[0] = %q, want %q (full: %v)", decoded.Notify[0], bin, decoded.Notify)
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
	// .quiet().nothrow() so a failing hook can't break the agent's turn.
	wants := []string{
		"session.idle",
		"${lolaBin} hook stop`.quiet().nothrow()",
		"permission.asked",
		"${lolaBin} hook notification`.quiet().nothrow()",
		"tool.execute.after",
		"${lolaBin} hook tool_use`.quiet().nothrow()",
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
		name       string
		in         string
		wantEvent  string
		wantDetail string
	}{
		{
			name:       "turn complete with message",
			in:         `{"type":"agent-turn-complete","last-assistant-message":"all done"}`,
			wantEvent:  "stop",
			wantDetail: "all done",
		},
		{
			name:       "turn complete without message falls back to type",
			in:         `{"type":"agent-turn-complete"}`,
			wantEvent:  "stop",
			wantDetail: "agent-turn-complete",
		},
		{
			name:       "turn complete empty message falls back to type",
			in:         `{"type":"agent-turn-complete","last-assistant-message":""}`,
			wantEvent:  "stop",
			wantDetail: "agent-turn-complete",
		},
		{
			name:       "approval requested",
			in:         `{"type":"approval-requested","last-assistant-message":"run rm?"}`,
			wantEvent:  "notification",
			wantDetail: "run rm?",
		},
		{
			name:       "approval requested no message",
			in:         `{"type":"approval-requested"}`,
			wantEvent:  "notification",
			wantDetail: "approval-requested",
		},
		{
			name:       "extra fields ignored",
			in:         `{"type":"agent-turn-complete","last-assistant-message":"hi","turn-id":7,"extra":true}`,
			wantEvent:  "stop",
			wantDetail: "hi",
		},
		// Unknown / missing / garbage -> skipped by the caller.
		{name: "unknown type", in: `{"type":"agent-message"}`, wantEvent: "", wantDetail: ""},
		{name: "missing type", in: `{"last-assistant-message":"hi"}`, wantEvent: "", wantDetail: ""},
		{name: "empty object", in: `{}`, wantEvent: "", wantDetail: ""},
		{name: "null", in: `null`, wantEvent: "", wantDetail: ""},
		{name: "empty string", in: ``, wantEvent: "", wantDetail: ""},
		{name: "malformed json", in: `{"type":`, wantEvent: "", wantDetail: ""},
		{name: "not an object", in: `123`, wantEvent: "", wantDetail: ""},
		{name: "json array", in: `["agent-turn-complete"]`, wantEvent: "", wantDetail: ""},
		{name: "whitespace", in: `   `, wantEvent: "", wantDetail: ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			gotEvent, gotDetail := ParseCodexNotify(c.in)
			if gotEvent != c.wantEvent || gotDetail != c.wantDetail {
				t.Errorf("ParseCodexNotify(%q) = (%q, %q), want (%q, %q)",
					c.in, gotEvent, gotDetail, c.wantEvent, c.wantDetail)
			}
		})
	}
}
