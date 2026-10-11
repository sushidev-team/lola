package agent

import (
	"encoding/json"
	"regexp"
)

// modelRe is the shape a worker MODEL override may take. It reaches argv (and,
// for codex, a TOML value), so it is checked rather than trusted: no leading
// "-" (it would read as a flag), no whitespace, no quotes, nothing a shell or a
// TOML parser would give meaning to. It is wide enough for every real model
// name the three agents accept — `opus`, `claude-opus-4-1[1m]`, `gpt-5-codex`,
// `anthropic/claude-sonnet-4`, a Bedrock `us.anthropic.claude-…-v1:0`.
var modelRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/@\[\]-]{0,99}$`)

// ValidModel reports whether model is a usable worker model override. ""
// is NOT valid here — it means "the agent's own default" and callers check for
// it before calling.
func ValidModel(model string) bool { return modelRe.MatchString(model) }

// LaunchModelArgs returns the argv that pins the WORKER launch of k to model,
// to be PREPENDED to LaunchArgs / LaunchArgsResume (so it never lands after a
// positional prompt, and for codex it precedes the `resume` subcommand):
//
//   - Claude:   --model <m>
//   - Codex:    -c model="<m>"   (a config override, accepted by the fresh TUI
//     and by `resume` alike, unlike a subcommand-scoped flag)
//   - OpenCode: --model <provider/model>
//
// An empty or invalid model returns nil: the agent runs on its own default,
// exactly as before the override existed.
func LaunchModelArgs(k Kind, model string) []string {
	if model == "" || !ValidModel(model) {
		return nil
	}
	switch Parse(string(k)) {
	case Codex:
		v, _ := json.Marshal(model) // a TOML basic string for every modelRe value
		return []string{"-c", "model=" + string(v)}
	default:
		return []string{"--model", model}
	}
}
