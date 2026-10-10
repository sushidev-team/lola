package daemon

// broadcast.go serves cmd=broadcast (SUSHI-629): ONE message from a human to
// every live coding agent of a project — "rebase on main", "stop touching X" —
// for the moment the context changes for all sessions at once.
//
// It is the diff feedback's queue (feedback.go) fanned out, and obeys the same
// send-keys invariant in CLAUDE.md:
//
//   - The text is queued on each session (Session.PendingBroadcast) FIRST, then
//     typed only through deliverQueued — handoffDeliverable AND paneWaitingNow,
//     sanitized immediately before the send, never run as a command.
//   - A session that is not provably resting at its prompt is DEFERRED, never
//     forced and never refused: the Stop hook or the observer cycle delivers it
//     the moment the agent comes back to its prompt (flushReviewHandoffs).
//   - The reply names which sessions got it now and which have it queued, so the
//     human sees the delivered/deferred split instead of a bare "ok".

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/session"
	"github.com/sushidev-team/lola/internal/state"
)

// broadcastMaxRunes bounds one broadcast. It is typed into every agent's pane,
// so it is kept to an instruction, not a document — and it is refused rather
// than clipped, because a silently truncated instruction is a different one.
const broadcastMaxRunes = 4000

// handleBroadcast serves cmd=broadcast: queue the message on every live agent
// session of the project, then try to deliver each right away.
func (d *Daemon) handleBroadcast(ctx context.Context, a protocol.BroadcastArgs) (protocol.BroadcastData, error) {
	project := strings.TrimSpace(a.Project)
	if project == "" {
		return protocol.BroadcastData{}, errors.New("project required")
	}
	msg := strings.TrimSpace(a.Message)
	if msg == "" {
		return protocol.BroadcastData{}, errors.New("message is empty")
	}
	if n := utf8.RuneCountInString(msg); n > broadcastMaxRunes {
		return protocol.BroadcastData{}, fmt.Errorf("message too long (%d characters, max %d)", n, broadcastMaxRunes)
	}
	d.mu.Lock()
	p := d.cfg.ProjectByName(project)
	d.mu.Unlock()
	if p == nil {
		return protocol.BroadcastData{}, fmt.Errorf("unknown project %q", project)
	}

	var targets []string
	for _, s := range d.sessions.Snapshot() {
		if s.Project == p.Name && broadcastTarget(s) {
			targets = append(targets, s.ID)
		}
	}
	sort.Strings(targets)

	out := protocol.BroadcastData{Delivered: []string{}, Queued: []string{}}
	for _, id := range targets {
		var full, queued bool
		d.sessions.Update(id, func(cur *session.Session) bool {
			// Re-check under the lock: the agent may have died since the snapshot.
			if !broadcastTarget(*cur) {
				return false
			}
			next := msg
			if cur.PendingBroadcast != "" {
				next = cur.PendingBroadcast + "\n\n" + msg
			}
			if len(next) > feedbackMaxPending {
				full = true
				return false
			}
			cur.PendingBroadcast = next
			queued = true
			return true
		})
		switch {
		case full:
			out.Skipped = append(out.Skipped, id)
		case queued:
			out.Queued = append(out.Queued, id)
		}
	}
	if len(out.Queued) > 0 {
		if err := d.sessions.Save(); err != nil {
			d.logf("", "broadcast: persist sessions: %v", err)
		}
	}

	// Deliver what can be delivered now. Each send is gated per session, so a
	// busy agent costs one pane read and keeps its copy queued.
	pending := out.Queued
	out.Queued = []string{}
	for _, id := range pending {
		if d.deliverBroadcast(ctx, id) {
			out.Delivered = append(out.Delivered, id)
		} else {
			out.Queued = append(out.Queued, id)
		}
	}
	out.Message = broadcastSummary(out)
	d.logf("", "broadcast: %s — %s", p.Name, out.Message)
	return out, nil
}

// broadcastTarget reports whether a session is a LIVE coding agent a broadcast
// should reach: it has an agent, the agent process is alive, and its PR is not
// already merged or closed (that session is on its way to teardown, and an
// instruction about ongoing work is meaningless to it).
func broadcastTarget(s session.Session) bool {
	if s.IsAgentless() {
		return false
	}
	switch s.AgentState {
	case state.AgentDead, state.AgentExited, state.AgentShell, state.AgentOrphaned:
		return false
	}
	switch s.Delivery {
	case state.DeliveryMerged, state.DeliveryClosed:
		return false
	}
	return true
}

// deliverBroadcast types the session's queued broadcast(s) when the agent is
// provably resting at its prompt; see deliverQueued.
func (d *Daemon) deliverBroadcast(ctx context.Context, id string) bool {
	return d.deliverQueued(ctx, id, queuedText{
		name:  "broadcast",
		field: func(s *session.Session) *string { return &s.PendingBroadcast },
		wrap:  broadcastMessage,
		done:  "delivered the human's project broadcast to the agent",
	})
}

// broadcastMessage wraps the queued broadcast text in lola's own framing and
// sanitizes the result — the human's text reaches the pane only as data in it.
func broadcastMessage(text string) string {
	return sanitizeAgentText(
		"A human sent the message below to every coding agent working in this project. " +
			"Apply it to your current task; if it does not apply to your work, say so briefly " +
			"and carry on.\n\n" + text)
}

func broadcastSummary(b protocol.BroadcastData) string {
	total := len(b.Delivered) + len(b.Queued) + len(b.Skipped)
	if total == 0 {
		return "no live agents in this project"
	}
	msg := fmt.Sprintf("delivered to %d, deferred for %d (sent when idle)", len(b.Delivered), len(b.Queued))
	if len(b.Skipped) > 0 {
		msg += fmt.Sprintf(", skipped %d (too much undelivered text queued)", len(b.Skipped))
	}
	return msg
}
