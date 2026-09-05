// Package agent describes the pluggable coding agent that lola spawns per
// Linear issue. lola can drive three interchangeable agents inside the
// per-session tmux pane — Claude Code (the default and legacy behavior),
// OpenAI's Codex CLI, and sst/opencode — each launched with its own argv and
// wired back to the daemon through its own lifecycle-callback mechanism.
//
// The same three agents also drive lola's one-shot REVIEW pass (the
// `claude-session` / `codex-session` / `opencode-session` review providers), so
// this package additionally owns the review argv — a very different, read-only
// posture from the unattended worker launch. See ReviewArgs.
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
//   - Codex:    --approve-for-me <prompt>
//     Automatic approval review keeps the workspace-write sandbox and routes
//     escalation requests (including Git writes and network) to Codex's reviewer.
//     Unlike `never`, it can approve work beyond the sandbox. Denied actions
//     remain denied. The runtime adds a per-process `notify` override.
//   - OpenCode: --prompt <prompt> --auto
//     `--prompt` seeds the first turn; `--auto` auto-approves every permission
//     that is not explicitly denied so it runs unattended. Callbacks come from
//     the in-process plugin (OpenCodePluginJS).
//
// Unknown kinds are treated as Claude.
func LaunchArgs(k Kind, promptArg string) []string {
	switch k {
	case Codex:
		return []string{"--approve-for-me", promptArg}
	case OpenCode:
		return []string{"--prompt", promptArg, "--auto"}
	default:
		return []string{"--settings", ".lola/settings.json", promptArg}
	}
}

// LaunchArgsResume is LaunchArgs for REVIVING a session whose agent already ran
// once in this worktree. Claude and opencode resume their most recent
// conversation (no prompt — the saved transcript/session already carries the
// task context), so a revived pane picks up where the dead one left off instead
// of restarting from scratch:
//
//   - Claude:   --settings .lola/settings.json --continue
//   - OpenCode: --continue --auto
//     `--continue` resumes the last session of the current directory (each lola
//     session owns its worktree, so that is the right conversation); `--auto`
//     keeps the revived session unattended, matching the fresh launch's
//     contract. No `--prompt`: combining it with `--continue` is broken
//     upstream (anomalyco/opencode#8850), and the resumed transcript already
//     carries the task context.
//
// Codex uses `resume --last --approve-for-me`, retaining cwd filtering so a
// shared CODEX_HOME never resumes another worktree's conversation. The caller
// decides WHETHER to resume (an agent that died before recording anything has
// nothing to continue); this only shapes the argv once that decision is made.
func LaunchArgsResume(k Kind, promptArg string) []string {
	switch k {
	case Claude:
		return []string{"--settings", ".lola/settings.json", "--continue"}
	case OpenCode:
		return []string{"--continue", "--auto"}
	case Codex:
		return []string{"resume", "--last", "--approve-for-me"}
	default:
		return LaunchArgs(k, promptArg)
	}
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
// contains spaces or shell metacharacters. Every command redirects stdin from
// /dev/null: Bun's `$` inherits the pane TTY as stdin, and `lola hook` must
// never sit reading it (a TTY never EOFs, and the blocked read eats the
// keystrokes the agent TUI is waiting for). Bun's shell implements sh-style
// redirections, so the redirect is part of the command string while lolaBin
// stays interpolated + escaped.
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
		"// stdin is redirected from /dev/null because Bun's $ inherits the pane TTY",
		"// and `lola hook` must never sit reading it.",
		"const lolaBin = " + string(bin) + ";",
		"",
		"export const LolaHook = async ({ $ }) => ({",
		"  event: async ({ event }) => {",
		"    const t = event.type;",
		"    if (t === \"session.idle\") await $`${lolaBin} hook stop < /dev/null`.quiet().nothrow();",
		"    else if (t === \"permission.asked\") await $`${lolaBin} hook notification < /dev/null`.quiet().nothrow();",
		"    else if (t === \"tool.execute.after\") await $`${lolaBin} hook tool_use < /dev/null`.quiet().nothrow();",
		"  },",
		"});",
		"",
	}
	return []byte(strings.Join(lines, "\n"))
}

// ParseCodexNotify maps a Codex `notify` JSON payload to a normalized lola hook
// event plus its useful payload fields. Codex uses hyphenated field names.
// The mapping:
//
//	type "agent-turn-complete" -> event "stop"
//	type "approval-requested"  -> event "notification"
//	any other / missing type   -> event ""  (the caller skips these)
//
// message is the "last-assistant-message" field (may be empty), notifyType the
// raw type — the caller forwards both so the daemon can display what codex
// said instead of discarding it. Malformed or non-object JSON yields
// ("", "", ""), so a garbage payload is silently ignored by the caller.
func ParseCodexNotify(jsonArg string) (event, message, notifyType string) {
	var p struct {
		Type    string `json:"type"`
		LastMsg string `json:"last-assistant-message"`
	}
	if err := json.Unmarshal([]byte(jsonArg), &p); err != nil {
		return "", "", ""
	}
	switch p.Type {
	case "agent-turn-complete":
		event = "stop"
	case "approval-requested":
		event = "notification"
	default:
		return "", "", ""
	}
	return event, p.LastMsg, p.Type
}

// --- one-shot review runs ----------------------------------------------------
//
// Beside the per-issue WORKER launch above, lola drives the same three agents in
// a second, very different mode: a single bounded, non-interactive REVIEW of a
// pull request (the `claude-session` / `codex-session` / `opencode-session`
// review providers). The review's prompt is lola's own fixed instruction on
// argv; the PR's unified diff is piped on STDIN, which all three CLIs accept:
//
//   - claude:   `-p <instruction>` reads piped stdin as additional context.
//   - codex:    `exec [PROMPT]` appends piped stdin to the prompt inside a
//     literal `<stdin>…</stdin>` block.
//   - opencode: `run [message..]` joins the positional message and piped stdin
//     with a newline.
//
// A review READS; it must never write. Each agent is therefore launched in its
// most restrictive non-interactive posture — the deliberate opposite of the
// unattended, auto-approving worker launch above:
//
//   - claude:   no `--permission-mode`, so headless defaults stand (reads are
//     allowed, an edit/exec that would need approval is denied).
//   - codex:    `--sandbox read-only` (`codex exec` hardcodes "never ask", so
//     the sandbox is the whole guard; `--ask-for-approval` is a TUI-only flag
//     and does not exist on `exec`).
//   - opencode: NO `--auto`. A non-interactive opencode denies the blocking
//     `question` permission, so anything that would ask is refused.
//
// Be precise about what that last one is worth. claude and codex are constrained
// by what lola puts on the argv, so their posture holds whatever the user's own
// config says. opencode has no read-only flag to pass, so its posture rests on
// ITS defaults: omitting `--auto` means lola never widens them, but a user who
// has explicitly allowed edits in their own opencode config gets a reviewer that
// could write to the worktree. That is the one gap in "a review never writes",
// and it is opencode's to close — do not paper over it by claiming otherwise.

// ReviewArgs returns the argv that follows the binary name for a one-shot
// review by agent k: lola's instruction as the prompt, plain text on stdout, and
// an optional model override. The diff is NEVER here — it goes on stdin (see the
// block above). Unknown kinds are treated as Claude.
func ReviewArgs(k Kind, instruction, model string) []string {
	// Parse, not a raw switch: all four review helpers must normalize IDENTICALLY
	// or a caller that has not pre-normalized gets claude's argv from one and
	// codex's renderer from another — an argv/renderer mismatch that shows up as
	// an empty review, not as an error.
	switch Parse(string(k)) {
	case Codex:
		return codexReviewArgs(instruction, model)
	case OpenCode:
		return openCodeReviewArgs(instruction, model)
	default:
		args := []string{"-p", instruction, "--output-format", "text"}
		return appendModel(args, "--model", model)
	}
}

// InterpretArgs uses the review posture for context-only status interpretation.
// The daemon may start outside a checkout, so Codex must not require a git repo.
func InterpretArgs(k Kind, instruction, model string) []string {
	args := ReviewArgs(k, instruction, model)
	if Parse(string(k)) == Codex {
		args = append(args[:len(args)-1], "--skip-git-repo-check", instruction)
	}
	return args
}

// ReviewStreamArgs is ReviewArgs for a VISIBLE review — one run in a tmux pane a
// human watches. Only Claude needs a different argv: a plain `claude -p` prints
// NOTHING until it finishes, so the visible pass asks for the stream-json event
// feed and the caller renders it (see ReviewStreamsJSON). Codex and opencode
// already narrate their progress on stderr, so their argv is unchanged and the
// caller tees stderr to the pane instead (see ReviewProgressOnStderr).
func ReviewStreamArgs(k Kind, instruction, model string) []string {
	if Parse(string(k)) != Claude {
		return ReviewArgs(k, instruction, model)
	}
	args := []string{"-p", instruction, "--output-format", "stream-json", "--verbose"}
	return appendModel(args, "--model", model)
}

// codexReviewArgs builds `codex exec --sandbox read-only [--model m]
// <instruction>`. The prompt is positional and MUST come last: piped stdin is
// appended to it by codex itself. With stdout piped (never a TTY under lola),
// `codex exec` prints ONLY the final assistant message there and every progress
// line on stderr, which is exactly the plain-findings contract Review wants.
func codexReviewArgs(instruction, model string) []string {
	args := []string{"exec", "--sandbox", "read-only"}
	args = appendModel(args, "--model", model)
	return append(args, instruction)
}

// openCodeReviewArgs builds `opencode run [--model provider/model]
// <instruction>`. The message is positional and MUST come last; piped stdin is
// joined onto it with a newline. With stdout piped, opencode writes the
// assistant text there and its progress on stderr. `--auto` is deliberately
// omitted — see the posture note above.
func openCodeReviewArgs(instruction, model string) []string {
	args := []string{"run"}
	args = appendModel(args, "--model", model)
	return append(args, instruction)
}

// appendModel appends `flag model` when model is non-empty, leaving the agent's
// own configured default in place otherwise.
func appendModel(args []string, flag, model string) []string {
	if model == "" {
		return args
	}
	return append(args, flag, model)
}

// ReviewStreamsJSON reports whether k's ReviewStreamArgs makes it emit a
// stream-json EVENT feed on stdout (which the caller must render to plain lines)
// rather than the plain text its non-streaming argv produces. True for Claude
// only.
func ReviewStreamsJSON(k Kind) bool { return Parse(string(k)) == Claude }

// ReviewProgressOnStderr reports whether k narrates a review's progress on
// STDERR, so a visible pass has to tee stderr to the pane to show anything at
// all. True for codex and opencode; Claude says nothing until its stream-json
// feed is asked for on stdout.
func ReviewProgressOnStderr(k Kind) bool { return Parse(string(k)) != Claude }
