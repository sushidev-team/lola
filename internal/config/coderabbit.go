package config

// The [coderabbit] table configures the PR-COMMENT WATCH — distinct from the
// [review] table. [review] execs the CodeRabbit CLI locally against a worktree;
// [coderabbit] instead POLLS the session's GitHub PR (via gh, on the observer's
// 30s cadence) for comments/reviews left by the CodeRabbit GitHub app (or any
// reviewer bot, via `author`) and routes each NEW one to the human (notify),
// optionally the worker agent (sanitized + idle-gated, like [review]), and
// optionally a Linear comment.
//
// Poll — not push — on purpose: lola is a local socket daemon with no HTTP
// ingress, and it can be stopped (laptop asleep, restart) exactly when a webhook
// would fire. A watermark (session.LastCodeRabbitAt) makes the poll fire-once per
// comment and survive any downtime — the next cycle reconciles current state.
//
// The findings are UNTRUSTED text (an attacker can author a PR comment), so they
// are sanitized before ever reaching a pane and are never run as a command — the
// same discipline as [review].
//
// The table is entirely optional and defaults to DISABLED, so an absent
// [coderabbit] table means Enabled=false with zero behavior change: lola never
// polls for comments.

// DefaultCodeRabbitAuthor is the login SUBSTRING matched against each comment /
// review author when [coderabbit].author is unset. The CodeRabbit GitHub app
// posts as "coderabbitai[bot]", which contains this.
const DefaultCodeRabbitAuthor = "coderabbitai"

// Hand-off / notification strings for the comment watch. Both are plain strings
// (no template eval) so nothing in a comment body can inject a directive.
const (
	// CodeRabbitAgentPointerFmt is the SINGLE-LINE instruction handed to the worker
	// agent when CodeRabbit leaves new PR comments. It deliberately does NOT embed
	// the comment text: the fetched body is large, untrusted, and low-signal (a
	// walkthrough summary, not the actionable inline review), and a multi-line
	// send-keys payload submits unreliably. A one-line prompt submits cleanly, and
	// the agent pulls the current, full, actionable review itself. The PR number
	// fills both %d (the reference and the gh command).
	CodeRabbitAgentPointerFmt = "CodeRabbit posted new review feedback on PR #%d. Read it (run: gh pr view %d --comments, and check the PR's review comments), address the actionable items, then commit and push."
	// CodeRabbitNotifyTitle titles the human-facing comment notification/comment.
	CodeRabbitNotifyTitle = "CodeRabbit commented"
)

// CodeRabbitConfig is the [coderabbit] table.
//
//   - Enabled gates the whole feature; false (the default, and the value for an
//     absent table) means lola never polls PR comments — zero behavior change.
//   - Author is the login substring matched against comment/review authors;
//     empty defaults to DefaultCodeRabbitAuthor (the CodeRabbit app).
//   - Notify surfaces each new comment to the human; defaults ON when Enabled.
//   - SendToAgent relays each new comment to the worker via the P3 send-keys
//     gate; defaults ON when Enabled.
//   - CommentOnLinear also mirrors the comment onto the Linear issue; defaults
//     OFF regardless of Enabled.
type CodeRabbitConfig struct {
	Enabled         bool   `toml:"enabled"`
	Author          string `toml:"author"`
	Notify          bool   `toml:"notify"`
	SendToAgent     bool   `toml:"send_to_agent"`
	CommentOnLinear bool   `toml:"comment_on_linear"`
}

// --- on-disk mirror --------------------------------------------------------
//
// Like [review], the [coderabbit] table uses a pointer-per-field mirror so load
// can tell an ABSENT key (nil → take the default) from an explicit zero the
// operator wants preserved. The whole table is a nil pointer when unconfigured,
// so a fresh Config persists no [coderabbit] table and reloads to the disabled
// default.

type fileCodeRabbitConfig struct {
	Enabled         *bool   `toml:"enabled,omitempty"`
	Author          *string `toml:"author,omitempty"`
	Notify          *bool   `toml:"notify,omitempty"`
	SendToAgent     *bool   `toml:"send_to_agent,omitempty"`
	CommentOnLinear *bool   `toml:"comment_on_linear,omitempty"`
}

// resolveCodeRabbit materializes the [coderabbit] table. A nil (absent) mirror
// yields the zero CodeRabbitConfig — disabled, everything off — so a config with
// no [coderabbit] table behaves exactly as before. A present table overlays each
// explicitly-set field onto the defaults: author defaults to
// DefaultCodeRabbitAuthor, and notify / send_to_agent default to Enabled unless
// the operator set them, while comment_on_linear defaults OFF regardless.
func resolveCodeRabbit(fr *fileCodeRabbitConfig) CodeRabbitConfig {
	if fr == nil {
		return CodeRabbitConfig{}
	}
	r := CodeRabbitConfig{Author: DefaultCodeRabbitAuthor}
	if fr.Enabled != nil {
		r.Enabled = *fr.Enabled
	}
	if fr.Author != nil && *fr.Author != "" {
		r.Author = *fr.Author
	}
	// notify and send_to_agent default ON when the watch is enabled; an explicit
	// value wins (so send_to_agent = false with enabled = true is honored).
	r.Notify = r.Enabled
	r.SendToAgent = r.Enabled
	if fr.Notify != nil {
		r.Notify = *fr.Notify
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

// coderabbitFile builds the on-disk mirror for Save. A zero (unconfigured) table
// returns nil so [coderabbit] is omitted entirely; otherwise every field is
// written explicitly so the round-trip is exact and an operator's explicit
// false/"" survives.
func coderabbitFile(r CodeRabbitConfig) *fileCodeRabbitConfig {
	if r == (CodeRabbitConfig{}) {
		return nil
	}
	return &fileCodeRabbitConfig{
		Enabled:         &r.Enabled,
		Author:          &r.Author,
		Notify:          &r.Notify,
		SendToAgent:     &r.SendToAgent,
		CommentOnLinear: &r.CommentOnLinear,
	}
}
