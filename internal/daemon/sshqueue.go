package daemon

// Text waiting for the agent. A human talks to an agent that is usually mid-turn,
// and an explicit send can use the agent's mid-turn composer after a cursor proof.
// Messages that cannot go immediately wait for the next agent-side reply (Inbox,
// printed by the CLI), or the resting prompt's automatic delivery gate.
//
// The store's queue is the source of truth, and Session.PendingSSH is the join of
// what is waiting, rewritten whenever that changes. "Delivered" means lola put the
// words in front of the agent. It cannot mean the agent read them, and a UI says so.

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/session"
	"github.com/sushidev-team/lola/internal/sshstore"
	"github.com/sushidev-team/lola/internal/state"
)

var errSSHQueueFull = errors.New("too much text is already waiting for the agent. Wait for it to pick that up")

// sshSyncPending rewrites the session's PendingSSH from the store. It is the one place that
// holds the prompt path: while QueueHeld it writes nothing to deliver, so deliverQueued and
// the slot in flushReviewHandoffs need no check of their own. An empty queue has nothing to
// hold, so it ends the hold.
func (d *Daemon) sshSyncPending(id string, store *sshstore.Store) {
	changed := false
	d.sessions.Update(id, func(cur *session.Session) bool {
		store.View(func(st *sshstore.State) {
			if len(st.Queue) == 0 && cur.QueueHeld {
				cur.QueueHeld = false
				changed = true
			}
			// Nothing waits, so nothing is deferred: a reason left set would be drawn beside
			// the next message and silence the notice for a real deferral with the same words.
			if len(st.Queue) == 0 && cur.AutomationDeferredReason != "" {
				cur.AutomationDeferredReason = ""
				changed = true
			}
			t := st.PendingText()
			if cur.QueueHeld {
				t = ""
			}
			if t != cur.PendingSSH {
				cur.PendingSSH = t
				changed = true
			}
		})
		return changed
	})
	if changed {
		if err := d.sessions.Save(); err != nil {
			d.logf("", "ssh: persist sessions: %v", err)
		}
	}
}

// sshReleaseHeld ends a Stop's hold on the human's queue: they spoke again.
func (d *Daemon) sshReleaseHeld(id string, store *sshstore.Store) {
	d.sessions.Update(id, func(cur *session.Session) bool {
		if !cur.QueueHeld {
			return false
		}
		cur.QueueHeld = false
		return true
	})
	d.sshSyncPending(id, store)
}

// sshInboxSays is the frame a human's unframed words get in a tool's output. At the prompt
// they go in as written, but here they arrive inside a command's reply and have to say whose
// they are. Text that is already framed keeps its own.
const sshInboxSays = "[lola] The human says:\n"

// sshInbox hands everything waiting to the agent's current reply and marks it
// delivered. A CLI that dies between the reply and printing loses it, which is why
// the wording everywhere is "delivered" and never "read". A slash command stays for the
// prompt, because in a tool's output it means nothing, and a held queue hands out nothing.
func (d *Daemon) sshInbox(id string) []string {
	store, err := d.sshStore(id, false)
	if err != nil || store == nil {
		return nil
	}
	if s, ok := d.sessions.Get(id); ok && s.QueueHeld {
		return nil
	}
	handOut := func(q sshstore.QueueEntry) bool { return !q.Claimed && !q.Command }
	now := d.ssh.now()
	has := false
	store.View(func(st *sshstore.State) {
		has = slices.ContainsFunc(st.Queue, handOut)
	})
	if !has {
		return nil
	}
	var entries []sshstore.QueueEntry
	if err := store.Update(func(st *sshstore.State) error {
		entries = st.Deliver(protocol.SSHViaReply, now, handOut)
		return nil
	}); err != nil {
		d.logf("", "ssh: inbox: %v", err)
	}
	d.sshSyncPending(id, store)
	out := make([]string, 0, len(entries))
	for _, q := range entries {
		t := q.Text
		if q.Human && !strings.HasPrefix(t, "[lola] ") {
			t = sshInboxSays + t
		}
		out = append(out, sanitizeAgentText(t))
	}
	return out
}

// deliverSSHQueue types the head batch at the agent's resting prompt, through the same gate
// as diff feedback. It is one of flushReviewHandoffs's one-per-pass slots.
func (d *Daemon) deliverSSHQueue(ctx context.Context, id string) bool {
	store, err := d.sshStore(id, false)
	if err != nil || store == nil {
		return false
	}
	return d.deliverSSH(ctx, id, store, queuedText{})
}

// deliverSSH is deliverSSHQueue with a caller's choice of lock. PendingSSH is the head batch,
// and what is typed is claimed by position, so what stays queued is exactly what was not.
func (d *Daemon) deliverSSH(ctx context.Context, id string, store *sshstore.Store, q queuedText) bool {
	q.name = "ssh"
	q.field = func(s *session.Session) *string { return &s.PendingSSH }
	q.wrap = sanitizeAgentText
	q.done = "delivered queued ssh text to the agent"
	// A human's own row shows their words. lola's own lines have no row, and the log's record
	// of them is hidden as an echo, so they need this one.
	q.label = func(s session.Session, _ string) string {
		own := false
		store.View(func(st *sshstore.State) {
			_, ids := st.NextBatch()
			for _, e := range st.Queue {
				own = own || (e.Item == "" && slices.Contains(ids, e.ID))
			}
		})
		if own {
			return "lola sent " + agentWord(s) + " an update"
		}
		return ""
	}
	q.took = func(_, text string) []string {
		var items []string
		_ = store.Update(func(st *sshstore.State) error {
			items = st.ClaimBatch(text)
			return nil
		})
		return items
	}
	q.sent = func(_, _ string, ok bool) {
		now := d.ssh.now()
		_ = store.Update(func(st *sshstore.State) error {
			if ok {
				st.Deliver(protocol.SSHViaPrompt, now, func(q sshstore.QueueEntry) bool { return q.Claimed })
				return nil
			}
			for i := range st.Queue {
				st.Queue[i].Claimed = false
			}
			return nil
		})
		d.sshSyncPending(id, store)
	}
	return d.deliverQueued(ctx, id, q)
}

// sshEnqueueHuman records what a human said as a timeline item and queues its framed
// form. It refuses past the queue cap, because the words exist nowhere else.
func (d *Daemon) sshEnqueueHuman(store *sshstore.Store, id string, m protocol.SSHHumanMessage, framed string) (string, error) {
	now := d.ssh.now()
	var item string
	err := store.Update(func(st *sshstore.State) error {
		if st.QueueBytes()+len(framed) > sshstore.MaxQueueBytes {
			return errSSHQueueFull
		}
		it := st.AddHuman(m, now)
		st.Enqueue(it.ID, framed)
		item = it.ID
		return nil
	})
	if err != nil {
		return "", err
	}
	d.sshSyncPending(id, store)
	return item, nil
}

// sshEnqueueLola queues one line of lola's own. It carries no remote text.
func (d *Daemon) sshEnqueueLola(store *sshstore.Store, id, text string) {
	err := store.Update(func(st *sshstore.State) error {
		if st.QueueBytes()+len(text) > sshstore.MaxQueueBytes {
			return errSSHQueueFull
		}
		st.Enqueue("", text)
		return nil
	})
	if err != nil {
		d.logf("", "ssh: nudge for %s dropped: %v", id, err)
		return
	}
	d.sshSyncPending(id, store)
}

// sshNudgeIfGone queues a line for an agent that stopped polling. One still polling
// reads the fact from its own reply, so it gets nothing extra.
func (d *Daemon) sshNudgeIfGone(store *sshstore.Store, id, text string) {
	d.ssh.mu.Lock()
	last, seen := d.ssh.lastPoll[id]
	d.ssh.mu.Unlock()
	if seen && d.ssh.now().Sub(last) < sshNudgeAfter {
		return
	}
	d.sshEnqueueLola(store, id, text)
}

// sshAgentAlive reports whether the session has a running agent to read anything.
func sshAgentAlive(s session.Session) bool {
	return !s.IsAgentless() && s.AgentState != state.AgentDead && s.AgentState != state.AgentExited
}

// sshDuration renders a span in a sentence lola writes.
func sshDuration(span time.Duration) string {
	switch {
	case span < time.Minute:
		n := int(span.Round(time.Second).Seconds())
		if n == 1 {
			return "1 second"
		}
		return strconv.Itoa(n) + " seconds"
	case span < 2*time.Minute:
		return "1 minute"
	case span < time.Hour:
		return strconv.Itoa(int(span.Minutes())) + " minutes"
	}
	h := int(span.Hours())
	if h == 1 {
		return "1 hour"
	}
	return strconv.Itoa(h) + " hours"
}
