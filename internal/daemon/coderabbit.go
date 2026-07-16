package daemon

// coderabbit.go wires the [coderabbit] PR-COMMENT WATCH into the observer. It is
// the POLL half of the CodeRabbit story (the [review] buddy in review.go is the
// local-CLI half): every observer cycle, for a native session with an OPEN PR, it
// polls the PR for comments/reviews left by the CodeRabbit GitHub app (or any
// bot, via [coderabbit].author) and routes each NEW one — newer than the
// session's LastCodeRabbitAt watermark — to the human (notify), the worker agent
// (sanitized + idle-gated), and/or a Linear comment.
//
// It shares the review buddy's invariants:
//
//   - OPT-IN + ZERO REGRESSION. A no-op unless [coderabbit].enabled; then it adds
//     exactly one read-only `gh pr view` to the cycle, bounded like every other
//     reaction exec.
//   - FIRE-ONCE + SURVIVES DOWNTIME. The watermark is advanced BEFORE routing, so
//     a routing failure never re-fires the same comment, and a comment left while
//     the daemon was stopped is reconciled on the next cycle (poll, not push).
//   - UNTRUSTED OUTPUT. A PR comment is attacker-authorable, so its text is fit
//     ONLY for a notify body / Linear comment shown to a human, OR — for the
//     worker hand-off — text that FIRST passes sanitizeAgentText AND the AtPrompt
//     idle-gate. Never a command, never an unsanitized send-keys payload.

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/notify"
	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/session"
)

// coderabbitWatch polls native session s's open PR for new CodeRabbit feedback
// and routes it. It is a no-op when the watch is off, the PR is not open, the
// session has no repo, or nothing is newer than the watermark. Called once per
// session per observer cycle (from observeNative), with s re-read for fresh
// PR / AtPrompt facts.
func (d *Daemon) coderabbitWatch(ctx context.Context, s session.Session) {
	d.mu.Lock()
	fetch := d.coderabbitComments
	cc := d.cfg.CodeRabbit
	notifier := d.notifier
	d.mu.Unlock()

	if fetch == nil || !cc.Enabled {
		return
	}
	if s.Source != "native" || s.Repo == "" || !isReviewablePROpen(s.PR) {
		return
	}

	cctx, cancel := context.WithTimeout(ctx, reactExecTimeout)
	text, latest, err := fetch(cctx, s.Repo, s.PR.Number, s.LastCodeRabbitAt, cc.Author)
	cancel()
	if err != nil {
		d.logf("", "coderabbit: watch for %s (PR #%d) failed: %v", s.ID, s.PR.Number, err)
		return
	}

	// Advance the watermark FIRST — before any routing — so a failed notify /
	// send never re-surfaces the same comment, and a crash between here and the
	// hand-off cannot double-fire. A deferred worker hand-off carries the text on
	// PendingCodeRabbit, so advancing now loses nothing.
	if latest.After(s.LastCodeRabbitAt) {
		d.sessions.Update(s.ID, func(cur *session.Session) bool {
			if !latest.After(cur.LastCodeRabbitAt) {
				return false
			}
			cur.LastCodeRabbitAt = latest
			return true
		})
		d.coderabbitSave()
	}

	if strings.TrimSpace(text) == "" {
		return // watermark moved (or not); nothing new to route
	}
	d.routeCodeRabbit(ctx, s, cc, text, notifier)
}

// routeCodeRabbit surfaces the (UNTRUSTED) comment text at up to three sinks,
// mirroring routeReviewFindings:
//
//   - Notify (opt-out): an Action notify with a short HEAD, so the human (and the
//     attention/pane layer) sees CodeRabbit spoke.
//   - SendToAgent: hand the FULL text to the worker — sanitized and idle-gated
//     (deferred, never dropped, when the agent is mid-turn).
//   - CommentOnLinear: mirror the text onto the Linear issue (P4 comment path).
func (d *Daemon) routeCodeRabbit(ctx context.Context, s session.Session, cc config.CodeRabbitConfig, text string, notifier notify.Notifier) {
	if notifier == nil {
		notifier = notify.New(notify.NotifyConfig{})
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}

	if cc.Notify {
		// UNTRUSTED sink #1: a notify body is display-only text (never a command).
		notifier.Notify(ctx, notify.Note{
			Title:    config.CodeRabbitNotifyTitle,
			Body:     reviewHead(text, reviewNotifyHeadBytes),
			Priority: notify.Action,
			URL:      prURL(s),
		})
	}
	// Log which sinks actually fired — with every sink off (e.g. notify=false,
	// send_to_agent=false) the watch is inert, and a bare "surfaced" line would
	// wrongly suggest it reached a human/agent.
	if !cc.Notify && !cc.SendToAgent && !cc.CommentOnLinear {
		d.logf("", "coderabbit: %s new PR feedback but ALL sinks are off (notify/send_to_agent/comment_on_linear) — nothing routed", s.ID)
	} else {
		d.logf("", "coderabbit: %s new PR feedback routed (notify=%v send_to_agent=%v linear=%v)", s.ID, cc.Notify, cc.SendToAgent, cc.CommentOnLinear)
	}

	// sink #2: the worker agent. It gets a SHORT, single-line POINTER built from
	// the PR number — never the raw comment text: a one-line prompt submits
	// reliably via send-keys (a large multi-line paste can leave the text unsent),
	// carries no attacker-authored markdown into the pane, and makes the agent
	// fetch the current, full, actionable review itself. Still idle-gated.
	if cc.SendToAgent {
		d.sendCodeRabbitToAgent(ctx, s, coderabbitAgentPointer(s))
	}

	// UNTRUSTED sink #3: a Linear comment (an API payload shown to a human, so no
	// control-byte sanitization is needed — see renderWriteback).
	if cc.CommentOnLinear {
		d.commentCodeRabbitOnLinear(ctx, s, text)
	}
}

// coderabbitAgentPointer builds the single-line instruction handed to the worker.
// It is derived only from the PR number (our own text — no untrusted content), so
// it submits cleanly and carries nothing attacker-authored into the pane.
func coderabbitAgentPointer(s session.Session) string {
	n := 0
	if s.PR != nil {
		n = s.PR.Number
	}
	return fmt.Sprintf(config.CodeRabbitAgentPointerFmt, n, n)
}

// sendCodeRabbitToAgent is the send-keys path for the comment hand-off. msg is the
// SINGLE-LINE pointer (coderabbitAgentPointer) — never the raw comment text. It
// enforces the SAME send-keys safety gate as the review buddy's sendReviewToAgent,
// keyed on the session's own PendingCodeRabbit field:
//
//   - Agent mid-turn (AtPrompt false) → DEFERRED: the pointer is stashed on
//     PendingCodeRabbit and flushPendingCodeRabbit delivers it on a later idle
//     cycle. Nothing is typed.
//   - Agent idle → AtPrompt is CONSUMED atomically (clearing PendingCodeRabbit)
//     in one Store.Update. Only if that consume wins is the pointer sent, so a
//     hook that flipped AtPrompt false meanwhile cancels it (re-stashed).
//
// The pointer is still passed through sanitizeAgentText (defence in depth; it is
// single-line and control-char-free, so this is a no-op in practice).
func (d *Daemon) sendCodeRabbitToAgent(ctx context.Context, s session.Session, msg string) {
	if s.TmuxName == "" {
		return
	}
	if !s.AtPrompt {
		d.deferCodeRabbitHandoff(s.ID, msg)
		d.logf("", "coderabbit: %s worker is mid-turn — deferring the comment hand-off", s.ID)
		return
	}

	msg = sanitizeAgentText(msg)

	var (
		sent     bool
		tmuxName string
	)
	d.sessions.Update(s.ID, func(cur *session.Session) bool {
		if !cur.AtPrompt {
			return false // a hook resumed the agent between the read above and here
		}
		cur.AtPrompt = false
		cur.PendingCodeRabbit = "" // consumed
		tmuxName = cur.TmuxName
		sent = true
		return true
	})
	if !sent {
		d.deferCodeRabbitHandoff(s.ID, msg)
		d.logf("", "coderabbit: %s worker no longer idle at prompt — deferring the hand-off", s.ID)
		return
	}

	sctx, cancel := context.WithTimeout(ctx, reactExecTimeout)
	defer cancel()
	if err := d.sendKeys(sctx, tmuxName, msg); err != nil {
		// Gate already consumed; do not roll back (that would re-fire and spam).
		d.logf("", "coderabbit: send-keys of comment to %s failed: %v", s.ID, err)
		return
	}
	d.coderabbitSave()
	d.logf("", "coderabbit: pointed the worker %s at its PR's new CodeRabbit feedback", s.ID)
}

// deferCodeRabbitHandoff stashes the (short, single-line) pointer for a later
// idle-cycle delivery, idempotently (a repeat stash of the same text is a no-op),
// and persists.
func (d *Daemon) deferCodeRabbitHandoff(id, msg string) {
	changed := false
	d.sessions.Update(id, func(cur *session.Session) bool {
		if cur.PendingCodeRabbit == msg {
			return false
		}
		cur.PendingCodeRabbit = msg
		changed = true
		return true
	})
	if changed {
		d.coderabbitSave()
	}
}

// flushPendingCodeRabbit delivers a comment hand-off deferred earlier (the worker
// was mid-turn) once the worker is idle at its prompt again. It re-reads the
// record itself and no-ops when nothing is pending or the worker is still busy
// (sendCodeRabbitToAgent just re-stashes then). Called every observer cycle.
func (d *Daemon) flushPendingCodeRabbit(ctx context.Context, id string) {
	s, ok := d.sessions.Get(id)
	if !ok || s.PendingCodeRabbit == "" || !s.AtPrompt {
		return
	}
	d.sendCodeRabbitToAgent(ctx, s, s.PendingCodeRabbit)
}

// commentCodeRabbitOnLinear mirrors the comment text onto the Linear issue via
// the P4 write-back client, best-effort. The body is bounded (scm caps it at
// ~4KB) and UNTRUSTED but display-only, so no control-byte sanitization is
// needed. Linear-unavailable / auth failures are logged (dropping the cached
// client on auth error) — never fatal, never blocking the lifecycle.
func (d *Daemon) commentCodeRabbitOnLinear(ctx context.Context, s session.Session, text string) {
	if s.IssueUUID == "" {
		return
	}
	api, err := d.ensureLinear()
	if err != nil {
		d.logf("", "coderabbit: linear unavailable for %s comment: %v", s.ID, err)
		return
	}
	cctx, cancel := context.WithTimeout(ctx, reactExecTimeout)
	defer cancel()
	body := config.CodeRabbitNotifyTitle + " on the PR:\n\n" + text
	if err := api.CreateComment(cctx, s.IssueUUID, body); err != nil {
		d.wbLinErr("coderabbit comment for "+issueLabel(s), err)
		return
	}
	d.logf("", "coderabbit: mirrored CodeRabbit feedback as a Linear comment on %s", issueLabel(s))
}

// handleCodeRabbit serves cmd=coderabbit (`lola coderabbit <session>`): it FORCES
// a PR-comment poll now for the named session, IGNORING the LastCodeRabbitAt
// watermark (it polls with a zero `since`, so the PR's CURRENT CodeRabbit
// feedback is re-surfaced and re-routed — the analog of `lola review` ignoring
// its once-per-PR guard). It routes the comments the same way the observer does
// and reports a short outcome for the CLI: skipped (watch off / no open PR),
// none found, found, or error. An unknown session is an error.
func (d *Daemon) handleCodeRabbit(ctx context.Context, sessionID string) (protocol.CodeRabbitData, error) {
	if sessionID == "" {
		return protocol.CodeRabbitData{}, fmt.Errorf("session id required")
	}
	s, ok := d.sessions.Get(sessionID)
	if !ok {
		return protocol.CodeRabbitData{}, fmt.Errorf("unknown session %s", sessionID)
	}

	d.mu.Lock()
	fetch := d.coderabbitComments
	cc := d.cfg.CodeRabbit
	notifier := d.notifier
	d.mu.Unlock()

	if fetch == nil || !cc.Enabled {
		return protocol.CodeRabbitData{
			Skipped: "not enabled",
			Message: "coderabbit check skipped: the [coderabbit] watch is not enabled",
		}, nil
	}
	if s.Source != "native" || s.Repo == "" || !isReviewablePROpen(s.PR) {
		return protocol.CodeRabbitData{
			Skipped: "no open PR",
			Message: "coderabbit check skipped: session has no open PR to poll",
		}, nil
	}

	// FORCE: poll from a zero watermark so the current feedback surfaces
	// regardless of what the observer has already routed (the manual analog of
	// `lola review` ignoring its guard).
	cctx, cancel := context.WithTimeout(ctx, reactExecTimeout)
	text, latest, err := fetch(cctx, s.Repo, s.PR.Number, time.Time{}, cc.Author)
	cancel()
	if err != nil {
		return protocol.CodeRabbitData{}, fmt.Errorf("coderabbit check failed: %w", err)
	}

	// Keep the watermark monotonic so the next observer cycle does not re-fire
	// what this forced poll just routed.
	if latest.After(s.LastCodeRabbitAt) {
		d.sessions.Update(s.ID, func(cur *session.Session) bool {
			if !latest.After(cur.LastCodeRabbitAt) {
				return false
			}
			cur.LastCodeRabbitAt = latest
			return true
		})
		d.coderabbitSave()
	}

	text = strings.TrimSpace(text)
	if text == "" {
		return protocol.CodeRabbitData{
			Ran:     true,
			Message: fmt.Sprintf("coderabbit check: no CodeRabbit comments on PR #%d", s.PR.Number),
		}, nil
	}

	d.routeCodeRabbit(ctx, s, cc, text, notifier)
	return protocol.CodeRabbitData{
		Ran:      true,
		Found:    true,
		Comments: text,
		Message:  "coderabbit check: routed CodeRabbit feedback to the configured sinks\n\n" + reviewHead(text, reviewNotifyHeadBytes),
	}, nil
}

// coderabbitSave persists the session store after a watch mutation, logging any
// failure. Watch state is best-effort durable — at worst a re-surface or re-send
// after a restart.
func (d *Daemon) coderabbitSave() {
	if err := d.sessions.Save(); err != nil {
		d.logf("", "coderabbit: persist sessions: %v", err)
	}
}
