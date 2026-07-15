package config

// The [brain] table configures the P5 "orchestrator brain": a single, bounded,
// OPT-IN `claude -p` call that produces a human-facing SUMMARY at two decision
// points (escalation, approved+green). It is deliberately outside the control
// loop — its output goes to the notifier and an optional Linear comment ONLY and
// is NEVER fed back to the worker agent (that context is attacker-influenceable,
// so the summary is untrusted text shown to a human, never an action).
//
// The table is entirely optional and defaults to DISABLED, so an absent [brain]
// table means Enabled=false with zero behavior change: lola keeps using its
// generic notify/comment templates. The daemon owns the exec (timeout, one-shot
// guards, graceful skip on any error); this package only holds the schema,
// defaults, and static validation.

// DefaultBrainTimeoutSeconds is the wall-clock cap on a single brain summary
// call when [brain].timeout_seconds is unset (the table present but the key
// omitted). The daemon aborts the `claude -p` call after this many seconds and
// falls back to the generic template.
const DefaultBrainTimeoutSeconds = 120

// Default brain instructions. These are instructions to claude (a system-style
// preamble), NOT user content: the attacker-influenceable context (PR diff, CI
// logs, pane tail) is appended by the daemon as the material to summarize. Both
// forbid code fences so the result drops cleanly into a notification or a Linear
// comment.
const (
	// BrainEscalationInstruction drives the escalation summary: why a stuck
	// session is blocked and the single most useful next step for a human.
	BrainEscalationInstruction = "You are a triage assistant. In 3-5 short lines summarize why this coding-agent session is blocked and the single most useful next step for a human. Be specific and terse. Do not include code fences."
	// BrainApprovedInstruction drives the approved+green summary: what the PR
	// changes and any risk a human should check before merging.
	BrainApprovedInstruction = "You are a code reviewer. In 3-5 short lines summarize what this PR changes and flag any risk a human should check before merging. Terse. No code fences."
)

// BrainConfig is the [brain] table.
//
//   - Enabled gates the whole feature; false (the default, and the value for an
//     absent table) means the daemon never execs claude and uses the generic
//     templates — zero behavior change.
//   - Model is passed to claude as --model when non-empty; "" uses claude's
//     default model.
//   - TimeoutSeconds bounds each call (see DefaultBrainTimeoutSeconds); must be
//     >= 0.
//   - SummarizeEscalation / SummarizeApproved select which of the two decision
//     points get a brain summary. Both default to ON when Enabled and the
//     operator leaves them unset, so enabling the brain enables both; either can
//     be set false explicitly to summarize only the other.
type BrainConfig struct {
	Enabled             bool   `toml:"enabled"`
	Model               string `toml:"model"`
	TimeoutSeconds      int    `toml:"timeout_seconds"`
	SummarizeEscalation bool   `toml:"summarize_escalation"`
	SummarizeApproved   bool   `toml:"summarize_approved"`
}

// --- on-disk mirror --------------------------------------------------------
//
// Like [reactions]/[notify], the [brain] table uses a pointer-per-field mirror
// so load can tell an ABSENT key (nil → take the default) from an explicit zero
// (enabled=false, summarize_escalation=false, timeout_seconds=0) the operator
// wants preserved. The whole table is a nil pointer when unconfigured, so a
// fresh Config persists no [brain] table and reloads to the disabled default.

type fileBrainConfig struct {
	Enabled             *bool   `toml:"enabled,omitempty"`
	Model               *string `toml:"model,omitempty"`
	TimeoutSeconds      *int    `toml:"timeout_seconds,omitempty"`
	SummarizeEscalation *bool   `toml:"summarize_escalation,omitempty"`
	SummarizeApproved   *bool   `toml:"summarize_approved,omitempty"`
}

// resolveBrain materializes the [brain] table. A nil (absent) mirror yields the
// zero BrainConfig — disabled, everything off, timeout unused — so a config with
// no [brain] table behaves exactly as before. A present table overlays each
// explicitly-set field onto the defaults: timeout_seconds defaults to
// DefaultBrainTimeoutSeconds, and the two summarizers default to Enabled unless
// the operator set them, so `enabled = true` alone turns both summaries on.
func resolveBrain(fb *fileBrainConfig) BrainConfig {
	if fb == nil {
		return BrainConfig{}
	}
	b := BrainConfig{TimeoutSeconds: DefaultBrainTimeoutSeconds}
	if fb.Enabled != nil {
		b.Enabled = *fb.Enabled
	}
	if fb.Model != nil {
		b.Model = *fb.Model
	}
	if fb.TimeoutSeconds != nil {
		b.TimeoutSeconds = *fb.TimeoutSeconds
	}
	// Summarizers default ON when the brain is enabled; an explicit value wins
	// (so summarize_escalation = false with enabled = true is honored).
	b.SummarizeEscalation = b.Enabled
	b.SummarizeApproved = b.Enabled
	if fb.SummarizeEscalation != nil {
		b.SummarizeEscalation = *fb.SummarizeEscalation
	}
	if fb.SummarizeApproved != nil {
		b.SummarizeApproved = *fb.SummarizeApproved
	}
	return b
}

// brainFile builds the on-disk mirror for Save. A zero (unconfigured) table
// returns nil so [brain] is omitted entirely; otherwise every field is written
// explicitly so the round-trip is exact and an operator's explicit
// false/0/"" survives.
func brainFile(b BrainConfig) *fileBrainConfig {
	if b == (BrainConfig{}) {
		return nil
	}
	return &fileBrainConfig{
		Enabled:             &b.Enabled,
		Model:               &b.Model,
		TimeoutSeconds:      &b.TimeoutSeconds,
		SummarizeEscalation: &b.SummarizeEscalation,
		SummarizeApproved:   &b.SummarizeApproved,
	}
}
