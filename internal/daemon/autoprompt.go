package daemon

import (
	"context"

	"github.com/sushidev-team/lola/internal/agent"
	"github.com/sushidev-team/lola/internal/attention"
	"github.com/sushidev-team/lola/internal/notify"
	"github.com/sushidev-team/lola/internal/session"
)

// lockAutomationPrompt holds off other daemon sends and checkpoint restores
// from proof through submit. In particular, never prove emptiness and then wait
// behind a restore: the user may have started typing by the time it finishes.
// A successful caller must release the lock and use typeToAgentLocked.
func (d *Daemon) lockAutomationPrompt(ctx context.Context, s session.Session) (func(), bool) {
	g := d.sendGate(s.ID)
	g.Lock()
	cur, ok := d.sessions.Get(s.ID)
	if !ok || !handoffDeliverable(cur) || !d.automationPromptReady(ctx, cur) {
		g.Unlock()
		return nil, false
	}
	return g.Unlock, true
}

// automationPromptReady checks human ownership separately from agent activity.
// Hooks describe a turn, not the draft or who currently owns the terminal.
// Run this AFTER slow content fetches and BEFORE consuming any pending message
// or retry guard. Failure leaves those untouched for the next observer pass.
func (d *Daemon) automationPromptReady(ctx context.Context, s session.Session) bool {
	cctx, cancel := context.WithTimeout(ctx, reactExecTimeout)
	defer cancel()
	if d.promptAttached == nil || d.paneTail == nil {
		return false
	}
	target := paneTarget(s)
	attached, err := d.promptAttached(cctx, target)
	if err != nil {
		d.automationDeferred(ctx, s, "Cannot verify whether the terminal is in use.")
		return false
	}
	if attached {
		d.automationDeferred(ctx, s, "Close or detach this session's live terminals to let lola send queued messages.")
		return false
	}
	pane, err := d.paneTail(cctx, target, observePaneLines)
	if err != nil {
		d.automationDeferred(ctx, s, "Cannot verify that the composer is empty.")
		return false
	}
	if attention.Classify(pane, agent.Parse(s.Agent)) != attention.ActivityWaiting {
		return false
	}
	if !attention.EmptyComposer(pane, agent.Parse(s.Agent)) {
		d.automationDeferred(ctx, s, "Waiting for an idle agent with an empty composer; any draft is left untouched.")
		return false
	}
	// A client could attach while the capture ran. Recheck ownership as the
	// final read before claiming the prompt. tmux is a terminal transport, not
	// an atomic editor API; this cannot promise isolation from external clients
	// that attach after the check, but never deliberately types into an owner.
	attached, err = d.promptAttached(cctx, target)
	if err != nil {
		d.automationDeferred(ctx, s, "Cannot verify whether the terminal is in use.")
		return false
	}
	if attached {
		d.automationDeferred(ctx, s, "Close or detach this session's live terminals to let lola send queued messages.")
		return false
	}
	d.sessions.Update(s.ID, func(cur *session.Session) bool {
		if cur.AutomationDeferredReason == "" {
			return false
		}
		cur.AutomationDeferredReason = ""
		return true
	})
	return true
}

// Explain a blocked delivery once per reason, rather than notifying on every
// observer pass. The pending payload stays in its existing durable queue.
func (d *Daemon) automationDeferred(ctx context.Context, s session.Session, reason string) {
	changed := false
	d.sessions.Update(s.ID, func(cur *session.Session) bool {
		if cur.AutomationDeferredReason == reason {
			return false
		}
		cur.AutomationDeferredReason = reason
		changed = true
		return true
	})
	if !changed {
		return
	}
	d.logf("", "automation: %s deferred — %s", s.ID, reason)
	d.mu.Lock()
	n := d.notifier
	d.mu.Unlock()
	if n != nil {
		n.Notify(ctx, notify.Note{Title: "Agent message waiting", Body: issueLabel(s) + ": " + reason, Priority: notify.Action, URL: prURL(s)})
	}
}
