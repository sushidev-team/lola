package config

import "strings"

// The [review] table configures the P9 "QA buddy": an EVENT-TRIGGERED CodeRabbit
// review pass, NOT a persistent second agent. When enabled, lola execs the
// CodeRabbit CLI against a session's branch the first time that session opens a
// PR, then hands the findings back to the worker (through the same P3 send-keys
// safety gate as reactions) and, optionally, to a human via notify + a Linear
// comment. The findings are UNTRUSTED text (they embed diff content), so they
// are sanitized before ever reaching a pane and are never run as a command.
//
// The table is entirely optional and defaults to DISABLED, so an absent [review]
// table means Enabled=false with zero behavior change: lola never execs the
// review CLI. The daemon owns the exec (timeout, one-shot-per-PR guard, graceful
// skip when coderabbit is missing/unauthed); this package only holds the schema,
// defaults, and static validation.

// DefaultReviewTimeoutSeconds is the wall-clock cap on a single CodeRabbit review
// pass when [review].timeout_seconds is unset (the table present but the key
// omitted). A CodeRabbit review can take a while, so the default is generous; the
// daemon aborts the pass after this many seconds and skips the review.
const DefaultReviewTimeoutSeconds = 300

// Default hand-off strings for a completed review. ReviewToAgentPreamble is a
// plain instruction prepended to the (sanitized) findings before they are typed
// into the worker; ReviewNotifyTitle titles the human notification. Both are
// plain strings — no template eval — so nothing in the findings can inject a
// directive.
const (
	// ReviewToAgentPreamble prefixes the findings sent to the worker agent.
	ReviewToAgentPreamble = "A CodeRabbit review of your PR found the following. Address the actionable items, commit, and push. Ignore anything already handled or out of scope:\n"
	// ReviewNotifyTitle titles the human-facing review notification/comment.
	ReviewNotifyTitle = "CodeRabbit review"
)

// ReviewConfig is the [review] table.
//
//   - Enabled gates the whole feature; false (the default, and the value for an
//     absent table) means lola never execs coderabbit — zero behavior change.
//   - Command optionally overrides the coderabbit argv as a space-split string
//     (e.g. "coderabbit review --plain --type all"); empty uses the runner's
//     built-in default. Split with CommandArgs.
//   - OnPROpen runs the pass automatically when a session first opens a PR;
//     defaults to ON when Enabled.
//   - SendToAgent feeds the findings back to the worker via the P3 send-keys
//     gate; defaults to ON when Enabled.
//   - CommentOnLinear also posts the findings as a Linear comment; defaults OFF
//     regardless of Enabled.
//   - TimeoutSeconds bounds each pass (see DefaultReviewTimeoutSeconds); must be
//     >= 0.
type ReviewConfig struct {
	Enabled         bool   `toml:"enabled"`
	Command         string `toml:"command"`
	OnPROpen        bool   `toml:"on_pr_open"`
	SendToAgent     bool   `toml:"send_to_agent"`
	CommentOnLinear bool   `toml:"comment_on_linear"`
	TimeoutSeconds  int    `toml:"timeout_seconds"`
}

// CommandArgs splits the Command override into an argv on whitespace. It returns
// nil for an empty/whitespace-only Command, which the runner reads as "use the
// built-in default coderabbit invocation".
func (r ReviewConfig) CommandArgs() []string {
	fields := strings.Fields(r.Command)
	if len(fields) == 0 {
		return nil
	}
	return fields
}

// --- on-disk mirror --------------------------------------------------------
//
// Like [reactions]/[notify]/[brain], the [review] table uses a pointer-per-field
// mirror so load can tell an ABSENT key (nil → take the default) from an explicit
// zero (enabled=false, on_pr_open=false, timeout_seconds=0) the operator wants
// preserved. The whole table is a nil pointer when unconfigured, so a fresh
// Config persists no [review] table and reloads to the disabled default.

type fileReviewConfig struct {
	Enabled         *bool   `toml:"enabled,omitempty"`
	Command         *string `toml:"command,omitempty"`
	OnPROpen        *bool   `toml:"on_pr_open,omitempty"`
	SendToAgent     *bool   `toml:"send_to_agent,omitempty"`
	CommentOnLinear *bool   `toml:"comment_on_linear,omitempty"`
	TimeoutSeconds  *int    `toml:"timeout_seconds,omitempty"`
}

// resolveReview materializes the [review] table. A nil (absent) mirror yields the
// zero ReviewConfig — disabled, everything off, timeout unused — so a config with
// no [review] table behaves exactly as before. A present table overlays each
// explicitly-set field onto the defaults: timeout_seconds defaults to
// DefaultReviewTimeoutSeconds, and on_pr_open / send_to_agent default to Enabled
// unless the operator set them (so `enabled = true` alone runs the pass on PR
// open and feeds the worker), while comment_on_linear defaults OFF regardless.
func resolveReview(fr *fileReviewConfig) ReviewConfig {
	if fr == nil {
		return ReviewConfig{}
	}
	r := ReviewConfig{TimeoutSeconds: DefaultReviewTimeoutSeconds}
	if fr.Enabled != nil {
		r.Enabled = *fr.Enabled
	}
	if fr.Command != nil {
		r.Command = *fr.Command
	}
	if fr.TimeoutSeconds != nil {
		r.TimeoutSeconds = *fr.TimeoutSeconds
	}
	// on_pr_open and send_to_agent default ON when review is enabled; an explicit
	// value wins (so send_to_agent = false with enabled = true is honored).
	r.OnPROpen = r.Enabled
	r.SendToAgent = r.Enabled
	if fr.OnPROpen != nil {
		r.OnPROpen = *fr.OnPROpen
	}
	if fr.SendToAgent != nil {
		r.SendToAgent = *fr.SendToAgent
	}
	// comment_on_linear defaults OFF (does not follow Enabled).
	if fr.CommentOnLinear != nil {
		r.CommentOnLinear = *fr.CommentOnLinear
	}
	return r
}

// reviewFile builds the on-disk mirror for Save. A zero (unconfigured) table
// returns nil so [review] is omitted entirely; otherwise every field is written
// explicitly so the round-trip is exact and an operator's explicit false/0/""
// survives.
func reviewFile(r ReviewConfig) *fileReviewConfig {
	if r == (ReviewConfig{}) {
		return nil
	}
	return &fileReviewConfig{
		Enabled:         &r.Enabled,
		Command:         &r.Command,
		OnPROpen:        &r.OnPROpen,
		SendToAgent:     &r.SendToAgent,
		CommentOnLinear: &r.CommentOnLinear,
		TimeoutSeconds:  &r.TimeoutSeconds,
	}
}
