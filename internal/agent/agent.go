// Package agent describes the pluggable coding agent that lola spawns per
// Linear issue. lola can drive three interchangeable agents inside the
// per-session tmux pane — Claude Code (the default and legacy behavior),
// OpenAI's Codex CLI, and sst/opencode — each launched with its own argv and
// wired back to the daemon through its own lifecycle-callback mechanism.
//
// This package is a pure leaf: it knows the argv, the binary name, and the
// shape of each agent's callback artifact, and nothing about config, sessions,
// hooks, the runtime, or pane classification. It imports only the standard
// library so every other package can depend on it without cycles.
package agent

import (
	"encoding/json"
	"strings"
)

// Kind identifies which coding agent drives a session's pane. Its string form
// is exactly the token accepted in config (`agent = "…"`) and persisted on a
// Session.
type Kind string

const (
	// Claude is the default and legacy agent (Anthropic's Claude Code). An
	// empty/unknown kind resolves to Claude so pre-existing sessions and a
	// blank config keep today's behavior byte-for-byte.
	Claude Kind = "claude"
	// Codex is OpenAI's Codex CLI, driven headless in its interactive TUI.
	Codex Kind = "codex"
	// OpenCode is sst/opencode, driven headless with an in-process plugin.
	OpenCode Kind = "opencode"
)

// Kinds is the canonical set of supported agents, in a stable order (used for
// enumeration/validation messages).
var Kinds = []Kind{Claude, Codex, OpenCode}

// Valid reports whether s is exactly one of the supported agent tokens. It is
// strict (no trimming, no case folding) to match the config enum validators —
// an empty string is NOT valid here; callers that treat empty as "inherit"
// check for "" before calling Valid.
func Valid(s string) bool {
	switch Kind(s) {
	case Claude, Codex, OpenCode:
		return true
	default:
		return false
	}
}

// Parse coerces s into a Kind. It is lenient — surrounding whitespace and
// letter case are ignored — and total: an empty or unrecognized value yields
// Claude, so there is always a usable agent to launch. (Contrast with Valid,
// which is the strict gate used for config validation.)
func Parse(s string) Kind {
	switch Kind(strings.ToLower(strings.TrimSpace(s))) {
	case Codex:
		return Codex
	case OpenCode:
		return OpenCode
	default:
		return Claude
	}
}

// String returns the agent's config token ("claude"|"codex"|"opencode").
func (k Kind) String() string { return string(k) }

// Binary returns the default executable name resolved via PATH for k. Unknown
// kinds fall back to Claude's binary. (The runtime may override the Claude
// binary via its own ClaudeBin setting; Binary reports only the default.)
func (k Kind) Binary() string {
	switch k {
	case Codex:
		return "codex"
	case OpenCode:
		return "opencode"
	default:
		return "claude"
	}
}

// LaunchArgs returns the argv that follows the binary name for kind k, seeding
// the first turn with promptArg. Each agent is configured to run UNATTENDED, so
// it works its issue without a human in the loop, mirroring how the Claude
// session already runs:
//
//   - Claude:   --settings .lola/settings.json <prompt>
//     Reads the per-session hook wiring from the lola-managed settings file
//     (hook.SettingsJSON) so the project's own settings stay untouched; the
//     prompt is positional.
//   - Codex:    --ask-for-approval never --sandbox workspace-write <prompt>
//     `never` approvals + `workspace-write` sandbox let Codex edit the worktree
//     and run commands without pausing for confirmation; the prompt is
//     positional. Callbacks come from the `notify` key in its config.toml
//     (CodexConfigTOML), not from these flags.
//   - OpenCode: --prompt <prompt> --auto
//     `--prompt` seeds the first turn; `--auto` auto-approves every permission
//     that is not explicitly denied so it runs unattended. Callbacks come from
//     the in-process plugin (OpenCodePluginJS).
//
// Unknown kinds are treated as Claude.
func LaunchArgs(k Kind, promptArg string) []string {
	switch k {
	case Codex:
		return []string{"--ask-for-approval", "never", "--sandbox", "workspace-write", promptArg}
	case OpenCode:
		return []string{"--prompt", promptArg, "--auto"}
	default:
		return []string{"--settings", ".lola/settings.json", promptArg}
	}
}

// LaunchArgsResume is LaunchArgs for REVIVING a session whose agent already ran
// once in this worktree. Claude resumes its most recent conversation via
// --continue (no positional prompt — the saved transcript already carries the
// task context), so a revived pane picks up where the dead one left off instead
// of restarting from scratch. Only Claude has a reliable headless resume, so
// codex and opencode fall back to a fresh launch identical to LaunchArgs; that
// still restarts their agent on the kept worktree, just without conversation
// continuity. The caller decides WHETHER to resume (a Claude session that died
// before writing any transcript has nothing to continue); this only shapes the
// argv once that decision is made.
func LaunchArgsResume(k Kind, promptArg string) []string {
	if k == Claude {
		return []string{"--settings", ".lola/settings.json", "--continue"}
	}
	return LaunchArgs(k, promptArg)
}

// CodexConfigTOML returns the body of a Codex `config.toml` (written under a
// per-session CODEX_HOME) that routes Codex's lifecycle notifications back to
// the lola daemon. Codex runs the `notify` program with a single JSON payload
// appended as the last argv element:
//
//	<lolaBin> hook codex-notify '<jsonPayload>'
//
// The `notify` array is emitted as the very first line: in TOML a top-level key
// must precede any [table] header, and this file has none, but keeping it first
// makes that invariant obvious and lets callers assert it cheaply.
func CodexConfigTOML(lolaBin string) []byte {
	// json.Marshal of a []string is valid TOML: an array of double-quoted
	// basic strings with the same escape rules, so the path is safely quoted.
	arr, _ := json.Marshal([]string{lolaBin, "hook", "codex-notify"})

	var b strings.Builder
	b.WriteString("notify = ")
	b.Write(arr)
	b.WriteByte('\n')
	b.WriteByte('\n')
	b.WriteString("# Written by lola. `notify` routes Codex turn-complete/approval\n")
	b.WriteString("# events back to the daemon; it must stay a top-level key above any\n")
	b.WriteString("# [table]. Codex invokes it with the event JSON as the last argument:\n")
	b.WriteString("#   ")
	b.WriteString(lolaBin)
	b.WriteString(" hook codex-notify '<jsonPayload>'\n")
	return []byte(b.String())
}

// OpenCodePluginJS returns the body of an OpenCode plugin (written to
// .opencode/plugins/lola-hook.js and auto-loaded by opencode) that bridges
// OpenCode's in-process lifecycle events to `lola hook`:
//
//	session.idle        -> <lolaBin> hook stop
//	permission.asked    -> <lolaBin> hook notification
//	tool.execute.after  -> <lolaBin> hook tool_use
//
// Each command is fired via Bun's shell ($), .quiet() to swallow output and
// .nothrow() so a failing hook never breaks the agent's turn. LOLA_SESSION is
// inherited from the pane environment, so the daemon identifies the session
// without any argument. The binary path is interpolated as a Bun `$` string,
// which Bun escapes automatically — the launch stays safe even when lolaBin
// contains spaces or shell metacharacters.
func OpenCodePluginJS(lolaBin string) []byte {
	// A JSON string literal is also a valid JS string literal; this quotes and
	// escapes lolaBin for embedding as `const lolaBin = "…";`.
	bin, _ := json.Marshal(lolaBin)

	lines := []string{
		"// lola OpenCode hook plugin - written by lola, auto-loaded from .opencode/plugins/.",
		"// Bridges OpenCode lifecycle events back to the lola daemon via `lola hook`.",
		"// LOLA_SESSION is inherited from the pane environment so the daemon",
		"// identifies the session. Bun's $ escapes the interpolated binary path,",
		"// so the launch stays safe even when the path contains spaces or metacharacters.",
		"const lolaBin = " + string(bin) + ";",
		"",
		"export const LolaHook = async ({ $ }) => ({",
		"  event: async ({ event }) => {",
		"    const t = event.type;",
		"    if (t === \"session.idle\") await $`${lolaBin} hook stop`.quiet().nothrow();",
		"    else if (t === \"permission.asked\") await $`${lolaBin} hook notification`.quiet().nothrow();",
		"    else if (t === \"tool.execute.after\") await $`${lolaBin} hook tool_use`.quiet().nothrow();",
		"  },",
		"});",
		"",
	}
	return []byte(strings.Join(lines, "\n"))
}

// ParseCodexNotify maps a Codex `notify` JSON payload to a normalized lola hook
// event and a human-facing detail string. Codex uses hyphenated field names.
// The mapping:
//
//	type "agent-turn-complete" -> event "stop"
//	type "approval-requested"  -> event "notification"
//	any other / missing type   -> event ""  (the caller skips these)
//
// detail is the "last-assistant-message" field when present, otherwise the raw
// type. Malformed or non-object JSON yields ("", ""), so a garbage payload is
// silently ignored by the caller.
func ParseCodexNotify(jsonArg string) (event, detail string) {
	var p struct {
		Type    string `json:"type"`
		LastMsg string `json:"last-assistant-message"`
	}
	if err := json.Unmarshal([]byte(jsonArg), &p); err != nil {
		return "", ""
	}
	switch p.Type {
	case "agent-turn-complete":
		event = "stop"
	case "approval-requested":
		event = "notification"
	default:
		return "", ""
	}
	if p.LastMsg != "" {
		detail = p.LastMsg
	} else {
		detail = p.Type
	}
	return event, detail
}
