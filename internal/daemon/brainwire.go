package daemon

// brainwire.go wires the PLAN P5.25 orchestrator BRAIN into the daemon: two
// bounded, OPT-IN, headless `claude -p` SUMMARIES that augment the EXISTING
// notify + Linear-comment paths at two decision points and nothing else.
//
//   - escalationSummary — at the CI-exhausted escalation (reactCIFailed): a
//     "why is this session blocked / what next" summary that becomes the
//     Urgent notify body AND the P4 blocked-comment detail.
//   - approvedSummary — at approved+green (reactApproved): a "what does this PR
//     change / what risk" summary that becomes the Action notify body.
//
// Three invariants dominate this file (they are the whole point of the feature):
//
//   - OPT-IN + ZERO REGRESSION. brainSummarize is nil unless [brain].enabled is
//     true AND claude resolves; every summary helper returns "" then, and the
//     call site keeps its generic template. A per-summary toggle
//     (SummarizeEscalation / SummarizeApproved) can turn either off with the
//     brain still on.
//   - BOUNDED + FIRE-ONCE + GRACEFUL. Each summary is ONE claude call, wrapped
//     in the brain timeout AND in a SINGLE per-cycle budget shared across every
//     session (observeNative's brainCycleCtx), so a slow/hung claude can never
//     stall reactions to later sessions or delay graceful shutdown beyond that
//     one bound — the budget derives from the shutdown-cancellable root, so
//     cancellation aborts the read-only claude exec. It fires at most once per
//     transition because it hangs off the existing P3/P4 one-shot guards
//     (Escalated / approved LastReactedStatus). Any error/timeout/cancel returns
//     "" → generic fallback.
//   - UNTRUSTED OUTPUT. The gathered context (pane tail, CI logs, PR diff) is
//     attacker-influenceable, so a summary is untrusted text fit ONLY for a
//     notify body or a Linear comment shown to a human. It is NEVER handed to
//     tmux send-keys — that would be prompt-injection into the control path.

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/sushidev-team/lola/internal/brain"
	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/session"
)

// escalationPaneLines is how much of the stuck agent's tmux pane the escalation
// summary feeds claude — enough to see the last exchange, bounded so a chatty
// pane can never dominate the (already size-capped) context.
const escalationPaneLines = 40

// buildBrain constructs the brain summarizer for bc, or nil when [brain] is
// disabled OR enabled-but-claude-is-unavailable. A nil result is the daemon's
// "use the generic template" signal, so a missing claude degrades gracefully to
// exactly the pre-P5 behavior rather than erroring per cycle.
func buildBrain(bc config.BrainConfig) *brain.Client {
	if !bc.Enabled {
		return nil
	}
	cl := &brain.Client{Model: bc.Model}
	if bc.TimeoutSeconds > 0 {
		cl.Timeout = time.Duration(bc.TimeoutSeconds) * time.Second
	}
	if !cl.Available() {
		return nil // enabled but claude not on PATH: caller logs once, falls back
	}
	return cl
}

// setBrainLocked (re)builds the brain client and its exec seam from bc. Caller
// holds d.mu. A nil client leaves brainSummarize nil, which every summary helper
// treats as "brain off → generic template". Called from Run and handleReload so
// enabling/disabling the brain (or changing model/timeout) takes effect live.
func (d *Daemon) setBrainLocked(bc config.BrainConfig) {
	d.brain = buildBrain(bc)
	if d.brain == nil {
		d.brainSummarize = nil
		return
	}
	d.brainSummarize = d.brain.Summarize
}

// brainTimeout is the wall-clock bound applied to a single summary call,
// independent of the seam's own bound — so the observer is protected even if a
// seam forgot to self-bound. Mirrors config.DefaultBrainTimeoutSeconds.
func brainTimeout(bc config.BrainConfig) time.Duration {
	if bc.TimeoutSeconds > 0 {
		return time.Duration(bc.TimeoutSeconds) * time.Second
	}
	return config.DefaultBrainTimeoutSeconds * time.Second
}

// setBrainCycleCtx installs (or clears, with nil) the current observe cycle's
// shared brain budget context — see the Daemon.brainCycleCtx field.
func (d *Daemon) setBrainCycleCtx(ctx context.Context) {
	d.brainMu.Lock()
	d.brainCycleCtx = ctx
	d.brainMu.Unlock()
}

// brainContext returns the observe cycle's shared, shutdown-cancellable brain
// budget context when one is active, else fallback. The whole summary path
// (context gathering + the one claude call) runs under it so a hung claude is
// bounded ONCE per cycle, not once per session, and is aborted at shutdown.
// fallback (the caller's ctx) is used when react runs outside an observe cycle,
// e.g. called directly in tests — where fallback still bounds each call via the
// per-call brainTimeout wrap below.
func (d *Daemon) brainContext(fallback context.Context) context.Context {
	d.brainMu.Lock()
	c := d.brainCycleCtx
	d.brainMu.Unlock()
	if c == nil {
		return fallback
	}
	return c
}

// escalationSummary returns a brain summary of WHY session s is blocked and the
// most useful next step, or "" when the brain is off, this summary is toggled
// off, or any error/timeout occurs (the caller then keeps its generic template).
// The context it feeds claude — session/PR status, failing-check logs, the agent
// pane tail — is attacker-influenceable, so the returned string is UNTRUSTED:
// callers place it only in a notify body / Linear comment, never in send-keys.
func (d *Daemon) escalationSummary(ctx context.Context, s session.Session) string {
	d.mu.Lock()
	summarize := d.brainSummarize
	bc := d.cfg.Brain
	d.mu.Unlock()
	if summarize == nil || !bc.SummarizeEscalation {
		return ""
	}
	// Run the whole summary under the cycle's shared brain budget (nil → the
	// caller's ctx): a hung claude is capped for the WHOLE cycle so it can neither
	// stall reactions to later sessions nor delay graceful shutdown.
	pctx := d.brainContext(ctx)
	contextText := d.gatherEscalationContext(pctx, s)
	if strings.TrimSpace(contextText) == "" {
		return ""
	}
	// Bound the one claude call by the brain timeout too: belt-and-suspenders when
	// no cycle budget is active (tests), and a no-op when the budget deadline is
	// already the sooner one.
	bctx, cancel := context.WithTimeout(pctx, brainTimeout(bc))
	defer cancel()
	out, err := summarize(bctx, config.BrainEscalationInstruction, contextText)
	if err != nil {
		d.logf("", "brain: escalation summary for %s skipped, using generic template: %v", s.ID, err)
		return ""
	}
	return strings.TrimSpace(out)
}

// gatherEscalationContext assembles the size-bounded, read-only material the
// escalation summary hands claude: the derived status + PR facts, the failing-CI
// summary (reused from the reaction engine), and the tail of the agent's tmux
// pane. Every piece is best-effort — a missing piece is simply omitted.
func (d *Daemon) gatherEscalationContext(ctx context.Context, s session.Session) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Coding-agent session %s (issue %s) has been handed off after automatic recovery failed.\n", s.ID, issueLabel(s))
	fmt.Fprintf(&b, "Derived status: %s\n", s.Status)
	if s.PR != nil {
		fmt.Fprintf(&b, "PR #%d state=%s checks=%s review=%s mergeable=%s\n",
			s.PR.Number, s.PR.State, s.PR.ChecksState, s.PR.ReviewDecision, s.PR.Mergeable)
		if s.PR.URL != "" {
			fmt.Fprintf(&b, "%s\n", s.PR.URL)
		}
	}
	if fc := d.fetchFailingChecks(ctx, s); fc != "" {
		b.WriteString("\nFailing checks:\n")
		b.WriteString(fc)
		b.WriteString("\n")
	}
	if tail := d.fetchPaneTail(ctx, s, escalationPaneLines); tail != "" {
		b.WriteString("\nAgent pane (tail):\n")
		b.WriteString(tail)
	}
	return b.String()
}

// fetchPaneTail returns the last `lines` rows of the session's tmux pane, or ""
// on any error / no pane (best-effort, like the reaction-content fetchers).
func (d *Daemon) fetchPaneTail(ctx context.Context, s session.Session, lines int) string {
	if s.TmuxName == "" {
		return ""
	}
	cctx, cancel := context.WithTimeout(ctx, reactExecTimeout)
	defer cancel()
	out, err := d.paneTail(cctx, s.TmuxName, lines)
	if err != nil {
		d.logf("", "brain: capture pane for %s failed: %v", s.ID, err)
		return ""
	}
	return out
}

// approvedSummary returns a brain summary of what the approved PR changes and any
// merge risk, or "" when the brain is off, this summary is toggled off, the PR /
// repo is unknown, or any error/timeout occurs. The PR diff it summarizes is
// attacker-authored, so the result is UNTRUSTED: it is used only as the approved
// notify body, never handed to send-keys.
func (d *Daemon) approvedSummary(ctx context.Context, s session.Session) string {
	d.mu.Lock()
	summarize := d.brainSummarize
	bc := d.cfg.Brain
	d.mu.Unlock()
	if summarize == nil || !bc.SummarizeApproved || s.PR == nil || s.Repo == "" {
		return ""
	}
	// The diff fetch + the one claude call run under the cycle's shared brain
	// budget (nil → the caller's ctx) so a hung claude is capped once per cycle
	// and abortable at shutdown — see brainContext.
	pctx := d.brainContext(ctx)
	cctx, cancel := context.WithTimeout(pctx, reactExecTimeout)
	diff, err := d.prDiff(cctx, s.Repo, s.PR.Number)
	cancel()
	if err != nil {
		d.logf("", "brain: PR diff for %s failed, using generic template: %v", s.ID, err)
		return ""
	}
	if strings.TrimSpace(diff) == "" {
		return ""
	}
	bctx, cancel := context.WithTimeout(pctx, brainTimeout(bc))
	defer cancel()
	out, err := summarize(bctx, config.BrainApprovedInstruction, diff)
	if err != nil {
		d.logf("", "brain: approved summary for %s skipped, using generic template: %v", s.ID, err)
		return ""
	}
	return strings.TrimSpace(out)
}

// stashEscalationSummary hands the escalation summary computed at react's Urgent
// notify to the P4 blocked comment in the same observe cycle, so a single claude
// call feeds both surfaces. Only non-empty summaries are stashed.
func (d *Daemon) stashEscalationSummary(id, summary string) {
	if summary == "" {
		return
	}
	d.brainMu.Lock()
	if d.escSummaries == nil {
		d.escSummaries = map[string]string{}
	}
	d.escSummaries[id] = summary
	d.brainMu.Unlock()
}

// takeEscalationSummary returns and removes any stashed escalation summary for
// id ("" when none). Consuming it keeps the map from leaking when a poll
// configures no blocked write-back to receive it.
func (d *Daemon) takeEscalationSummary(id string) string {
	d.brainMu.Lock()
	defer d.brainMu.Unlock()
	s := d.escSummaries[id]
	delete(d.escSummaries, id)
	return s
}
