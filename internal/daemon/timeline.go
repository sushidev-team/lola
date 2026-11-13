package daemon

import (
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/reviewmd"
	"github.com/sushidev-team/lola/internal/session"
	"github.com/sushidev-team/lola/internal/sshstore"
	"github.com/sushidev-team/lola/internal/state"
)

// timelineBuffer bounds the system events waiting for the timeline worker.
const timelineBuffer = 256

// timelineEvent is one thing on its way into a session's store: a system event, a record of
// what lola typed, a human's first words, or any of them together. Writing them all through
// one worker keeps them in the order they happened, so a story never opens with its first
// message above its own "started" row.
//
// Ended marks an event allowed to land after the session left the session store, which is
// only true of the fact that ended it: a PR's merge is emitted moments before the cleanup
// drops the record. Everything else is refused for an unknown session, so a gone store's
// retention clock is not reset by a straggler.
type timelineEvent struct {
	Session string
	Event   protocol.SSHSystemEvent
	Ended   bool
	Sent    *sshstore.SentBatch
	Human   *protocol.SSHHumanMessage
	// At is when this happened, for an event written well after that. Zero means now.
	At time.Time
}

// emitSystem queues a system event and never blocks and never fails its caller, which
// is the opposite of a block's rule: a row in the story is not worth stalling anything
// for. Because it only enqueues, it is safe from inside a store callback.
func (d *Daemon) emitSystem(ev timelineEvent) {
	if d.timeline == nil {
		return
	}
	ev.Session = session.BaseID(ev.Session)
	select {
	case d.timeline <- ev:
	default:
		d.logf("", "timeline: dropped a %s event for %s, the worker is behind", ev.Event.Kind, ev.Session)
	}
}

// startTimeline runs the one worker. Nothing here takes a context: a store write is local
// and bounded, and shutdown waits for what is already queued rather than losing it.
func (d *Daemon) startTimeline() {
	d.timelineStop = make(chan struct{})
	d.timelineDone = make(chan struct{})
	go func() {
		defer close(d.timelineDone)
		for {
			select {
			case ev := <-d.timeline:
				d.applyTimeline(ev)
			case <-d.timelineStop:
				d.drainTimeline()
				return
			}
		}
	}()
}

// stopTimeline writes what is queued and ends the worker.
func (d *Daemon) stopTimeline() {
	if d.timelineStop == nil {
		return
	}
	close(d.timelineStop)
	<-d.timelineDone
	d.timelineStop = nil
}

// drainTimeline writes everything queued right now, on the calling goroutine.
func (d *Daemon) drainTimeline() {
	for {
		select {
		case ev := <-d.timeline:
			d.applyTimeline(ev)
		default:
			return
		}
	}
}

// applyTimeline writes one event. It is guarded on its own, because a panic here must cost
// one row and not the worker, and it is never called from inside a store callback: the
// session store's lock comes before a store's (ssh.go), and this takes both.
func (d *Daemon) applyTimeline(ev timelineEvent) {
	defer func() {
		if r := recover(); r != nil {
			d.logf("", "timeline: write for %s panicked: %v", ev.Session, r)
		}
	}()
	_, known := d.sessions.Get(ev.Session)
	if !known && !ev.Ended {
		return
	}
	store, err := d.sshStore(ev.Session, known)
	if err != nil || store == nil {
		if err != nil {
			d.logf("", "timeline: open the store for %s: %v", ev.Session, err)
		}
		return
	}
	at := time.Now()
	if d.ssh != nil && d.ssh.now != nil {
		at = d.ssh.now()
	}
	if !ev.At.IsZero() {
		at = ev.At
	}
	var oldSegments []string
	respawned := false
	err = store.Update(func(st *sshstore.State) error {
		if ev.Sent != nil {
			st.NoteSent(*ev.Sent)
		}
		if ev.Event.Kind != "" {
			if ev.Event.Kind == protocol.SSHEventSessionStarted && st.Ended != nil {
				// A respawned id. A new agent must not read as the old story's end, and
				// words meant for the dead agent are an instruction nobody gave this one.
				oldSegments, respawned = st.Ended.Segments, true
				st.Ended = nil
				dropQueueForNewLife(st)
			}
			st.AddEvent(ev.Event, at)
		}
		if ev.Human != nil {
			st.AddHuman(*ev.Human, at)
		}
		store.Prune(st)
		return nil
	})
	if err != nil {
		d.logf("", "timeline: write for %s: %v", ev.Session, err)
	}
	if ev.Event.Kind == protocol.SSHEventSessionStarted {
		d.talk.forgetEnded()
	}
	if respawned {
		d.carrySegments(ev.Session, oldSegments)
		d.sshSyncPending(ev.Session, store)
	}
}

// dropQueueForNewLife takes back everything waiting for the agent that ended. A person's
// message reads as recalled, so its row says what happened to it.
func dropQueueForNewLife(st *sshstore.State) {
	for _, q := range st.Queue {
		if it := st.Item(q.Item); it != nil && it.Human != nil && !it.Human.Delivered {
			it.Human.Recalled = true
			st.Touch(it)
		}
	}
	st.Queue = nil
}

// carrySegments keeps the old life's transcript in the story of a respawned id, as the first
// files of the new record. The reader in memory still has them, so without this the old answers
// vanish at the next restart while the old "you" rows stay.
func (d *Daemon) carrySegments(id string, old []string) {
	if len(old) == 0 {
		return
	}
	changed := false
	d.sessions.Update(id, func(cur *session.Session) bool {
		var keep []string
		for _, seg := range old {
			if !slices.Contains(cur.TranscriptSegments, seg) {
				keep = append(keep, seg)
			}
		}
		if len(keep) == 0 {
			return false
		}
		cur.TranscriptSegments = append(keep, cur.TranscriptSegments...)
		if over := len(cur.TranscriptSegments) - maxSegments; over > 0 {
			cur.TranscriptSegments = slices.Delete(cur.TranscriptSegments, 0, over)
		}
		changed = true
		return true
	})
	if changed {
		if err := d.sessions.Save(); err != nil {
			d.logf("", "timeline: persist sessions: %v", err)
		}
	}
}

// noteSessionStarted writes the row a story starts with. It runs at a birth, which excludes
// a revive and an agent switch: those carry on the same story. A shell has no story.
func (d *Daemon) noteSessionStarted(s session.Session) {
	if s.IsAgentless() {
		return
	}
	d.emitSystem(timelineEvent{Session: s.ID, Event: protocol.SSHSystemEvent{
		Kind: protocol.SSHEventSessionStarted,
		Text: agentWord(s) + " started",
	}})
}

// noteAgentExited writes the row for an agent that stopped running on its own.
func (d *Daemon) noteAgentExited(s session.Session) {
	d.emitSystem(timelineEvent{Session: s.ID, Event: protocol.SSHSystemEvent{
		Kind: protocol.SSHEventAgentExited,
		Text: agentWord(s) + " exited",
	}})
}

// noteDelivery writes the PR's facts as they change, in lola's own words. The numbers are
// gh's and the wording never derives from the rollup. A merge can be followed at once by the
// cleanup that drops the record, so that row may land after the session has gone.
func (d *Daemon) noteDelivery(s session.Session, prev state.DeliveryState) {
	if s.PR == nil || s.PR.Number <= 0 {
		return
	}
	pr := "PR #" + strconv.Itoa(s.PR.Number)
	row := func(kind, text string, ended bool) {
		d.emitSystem(timelineEvent{Session: s.ID, Ended: ended, Event: protocol.SSHSystemEvent{Kind: kind, Text: text}})
	}
	switch s.Delivery {
	case state.DeliveryMerged:
		row(protocol.SSHEventPR, pr+" merged", true)
		return
	case state.DeliveryClosed:
		row(protocol.SSHEventPR, pr+" closed", true)
		return
	}
	if prev == state.DeliveryNone || prev == "" {
		row(protocol.SSHEventPR, pr+" opened", false)
	}
	if s.Delivery == state.DeliveryCIFailed {
		row(protocol.SSHEventCI, "CI failed on "+pr, false)
	}
}

// noteCI writes the green row, which is a fact about the checks and not about a state change.
// Going from ci_failed to anything proves nothing: a push whose checks have not registered yet
// reads as no checks at all, and an ordinary recovery passes through ci_pending. So the red is
// remembered, and only a real pass answers it, in whichever cycle it shows up.
func (d *Daemon) noteCI(s session.Session) {
	if s.PR == nil || s.PR.Number <= 0 {
		return
	}
	if s.Delivery == state.DeliveryCIFailed {
		d.talk.noteCIRed(session.BaseID(s.ID), s.PR.Number)
		return
	}
	if s.PR.ChecksState != "pass" || !d.talk.takeCIGreen(session.BaseID(s.ID), s.PR.Number) {
		return
	}
	d.emitSystem(timelineEvent{Session: s.ID, Event: protocol.SSHSystemEvent{
		Kind: protocol.SSHEventCI,
		Text: "CI passed on PR #" + strconv.Itoa(s.PR.Number),
	}})
}

// noteReviewTally writes how a review came out as a count. The findings themselves are
// attacker-influenceable text and never reach this row.
func (d *Daemon) noteReviewTally(s session.Session, p reviewProvider, pr int, findings string) {
	if pr <= 0 {
		return
	}
	who := labelsFor(p).who
	text := who + " reviewed PR #" + strconv.Itoa(pr) + ": clean"
	if strings.TrimSpace(findings) != "" {
		c := reviewmd.Count(findings)
		switch n := c.Blocker + c.Major + c.Minor + c.Other; n {
		case 0:
			text = who + " reviewed PR #" + strconv.Itoa(pr) + " and left findings"
		case 1:
			text = who + " reviewed PR #" + strconv.Itoa(pr) + ": 1 finding"
		default:
			text = who + " reviewed PR #" + strconv.Itoa(pr) + ": " + strconv.Itoa(n) + " findings"
		}
	}
	d.emitSystem(timelineEvent{Session: s.ID, Event: protocol.SSHSystemEvent{Kind: protocol.SSHEventReview, Text: text}})
}
