package daemon

// Text waiting for the agent. A human talks to an agent that is usually mid-turn,
// and a pane mid-turn must never be typed into, so two paths deliver and whichever
// comes first wins: the next agent-side reply (Inbox, printed by the CLI), or the
// resting prompt (deliverQueued, with its idle gate and pane proof).
//
// The store's queue is the source of truth, and Session.PendingSSH is the join of
// what is waiting, rewritten whenever that changes. "Delivered" means lola put the
// words in front of the agent. It cannot mean the agent read them, and a UI says so.

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/session"
	"github.com/sushidev-team/lola/internal/sshstore"
	"github.com/sushidev-team/lola/internal/state"
)

var errSSHQueueFull = errors.New("too much text is already waiting for the agent. Wait for it to pick that up")

// sshSyncPending rewrites the session's PendingSSH from the store.
func (d *Daemon) sshSyncPending(id string, store *sshstore.Store) {
	changed := false
	d.sessions.Update(id, func(cur *session.Session) bool {
		store.View(func(st *sshstore.State) {
			if t := st.PendingText(); t != cur.PendingSSH {
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

// sshInbox hands everything waiting to the agent's current reply and marks it
// delivered. A CLI that dies between the reply and printing loses it, which is why
// the wording everywhere is "delivered" and never "read".
func (d *Daemon) sshInbox(id string) []string {
	store, err := d.sshStore(id, false)
	if err != nil || store == nil {
		return nil
	}
	var out []string
	now := d.ssh.now()
	has := false
	store.View(func(st *sshstore.State) {
		for _, q := range st.Queue {
			if !q.Claimed {
				has = true
			}
		}
	})
	if !has {
		return nil
	}
	if err := store.Update(func(st *sshstore.State) error {
		out = st.Deliver(protocol.SSHViaReply, now, func(q sshstore.QueueEntry) bool { return !q.Claimed })
		return nil
	}); err != nil {
		d.logf("", "ssh: inbox: %v", err)
	}
	d.sshSyncPending(id, store)
	for i, t := range out {
		out[i] = sanitizeAgentText(t)
	}
	return out
}

// deliverSSHQueue types what is waiting at the agent's resting prompt, through the
// same gate as diff feedback. It is one of flushReviewHandoffs's one-per-pass slots.
func (d *Daemon) deliverSSHQueue(ctx context.Context, id string) bool {
	store, err := d.sshStore(id, false)
	if err != nil || store == nil {
		return false
	}
	return d.deliverQueued(ctx, id, queuedText{
		name:  "ssh",
		field: func(s *session.Session) *string { return &s.PendingSSH },
		wrap:  sanitizeAgentText,
		done:  "delivered queued ssh text to the agent",
		took: func(_, text string) {
			_ = store.Update(func(st *sshstore.State) error {
				for i := range st.Queue {
					if strings.Contains(text, st.Queue[i].Text) {
						st.Queue[i].Claimed = true
					}
				}
				return nil
			})
		},
		sent: func(_, _ string, ok bool) {
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
		},
	})
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
