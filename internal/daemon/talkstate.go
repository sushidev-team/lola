package daemon

// talkState is what a mounted session view asks once a second. It has to be cheap, so it
// reads as little as it can: the cursor first (one exec), and the pane only when the cursor
// is hidden, the record says a human is needed, or the agent is not known to be working.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/sushidev-team/lola/internal/agent"
	"github.com/sushidev-team/lola/internal/attention"
	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/session"
	"github.com/sushidev-team/lola/internal/sshstore"
	"github.com/sushidev-team/lola/internal/state"
	"github.com/sushidev-team/lola/internal/usage"
)

const (
	talkFeedBudget = 2 << 20 // per talkState call
	endedCacheFor  = 30 * time.Second
)

func (d *Daemon) handleTalkState(ctx context.Context, a protocol.TalkStateArgs) (protocol.TalkStateData, error) {
	id := session.BaseID(strings.TrimSpace(a.Session))
	s, ok := d.sessions.Get(id)
	if !ok {
		// An ended session is read-only and has no pane, and its record is all that says so.
		if sshstore.ReadEnded(d.home, id) != nil {
			return protocol.TalkStateData{Session: id, Composer: protocol.ComposerGone, Health: protocol.FeedOK}, nil
		}
		return protocol.TalkStateData{}, fmt.Errorf("unknown session %s", id)
	}
	out := protocol.TalkStateData{
		Session:   id,
		Caps:      talkCaps(s),
		Deferred:  s.AutomationDeferredReason,
		QueueHeld: s.QueueHeld,
		Health:    protocol.FeedUnsupported,
	}
	if d.feedAdvance != nil {
		out.FeedSeq, out.Health = d.feedAdvance(ctx, s, talkFeedBudget)
		out.FeedEpoch = d.feedEpochOf(id)
	}
	cctx, cancel := context.WithTimeout(ctx, reactExecTimeout)
	defer cancel()
	out.Composer, out.Prompt = d.talkPaneState(cctx, s, false)
	if s2, ok := d.sessions.Get(id); ok {
		out.Deferred, out.QueueHeld = s2.AutomationDeferredReason, s2.QueueHeld
	}
	return out, nil
}

// talkPaneState answers the composer word and any prompt on screen, and moves the axis when
// the pane proves it wrong. The prompt stays in memory: it is pane text, so it never reaches
// the snapshot, a store or a log. pressed says a human just answered on a card, which is
// proof enough that a prompt the pane no longer shows has been dealt with.
func (d *Daemon) talkPaneState(ctx context.Context, s session.Session, pressed bool) (string, *protocol.AgentPrompt) {
	if s.IsAgentless() || s.GoalManaged() || !sshAgentAlive(s) {
		d.setTalkPrompt(s.ID, nil)
		if s.IsAgentless() || s.GoalManaged() {
			return protocol.ComposerUnknown, nil
		}
		return protocol.ComposerGone, nil
	}
	k := agent.Parse(s.Agent)
	caps := agent.Caps(k)
	waiting := s.AgentState == state.AgentWaitingInput
	if caps.Cursor && d.paneCursor != nil {
		c, err := d.paneCursor(ctx, paneTarget(s))
		if err == nil && c.Visible && s.AgentState == state.AgentWorking {
			d.setTalkPrompt(s.ID, nil)
			return protocol.ComposerWorking, nil
		}
	} else if s.AgentState == state.AgentWorking {
		return protocol.ComposerWorking, nil
	}
	p, err := d.readTalkPane(ctx, s)
	if err != nil {
		return protocol.ComposerUnknown, d.talkPromptOf(s.ID)
	}
	if !caps.Cards {
		switch {
		case p.act == attention.ActivityWorking:
			return protocol.ComposerWorking, nil
		case waiting:
			return protocol.ComposerPrompt, nil
		}
		return composerWord(p), nil
	}
	var prompt *attention.Prompt
	if p.hasCursor && d.readPrompt != nil {
		if got, ok := d.readPrompt(p.visible, p.cursor, k); ok {
			prompt = &got
		}
	}
	// An empty resting composer is the proof a question is gone. A draft is not, and neither is
	// a read straddling an answer, so the memory of a question lasts until a read proves it.
	gone := p.act == attention.ActivityWaiting && p.empty
	answered := prompt == nil && gone && s.InputReason != state.InputQuotaLimited &&
		(pressed || d.talkPromptOf(s.ID) != nil || parkedOnAnsweredPermission(s, p, d.readPrompt != nil))
	d.reconcileFromPane(s.ID, p, prompt, answered)
	if prompt == nil {
		if gone || p.act == attention.ActivityWorking {
			d.setTalkPrompt(s.ID, nil)
		}
		return composerWord(p), nil
	}
	wire := wirePrompt(*prompt)
	d.setTalkPrompt(s.ID, &wire)
	if prompt.Kind == "modal" {
		return protocol.ComposerModal, d.talkPromptOf(s.ID)
	}
	return protocol.ComposerPrompt, d.talkPromptOf(s.ID)
}

// parkedOnAnsweredPermission is a permission park with no question left to read. A denial
// fires no hook and a late notification hook parks on a question already answered, while the
// observer will not release this reason from a bare resting pane, so nothing else gets it out.
// A shown cursor resting in the composer's own column is proof a dialog cannot give, because a
// dialog hides it. A modal or a usage limit can leave the composer looking at rest with nothing
// able to proceed, so neither is released this way.
func parkedOnAnsweredPermission(s session.Session, p talkPane, canReadPrompt bool) bool {
	return s.AgentState == state.AgentWaitingInput && s.InputReason == state.InputPermission && p.hasCursor && canReadPrompt
}

func composerWord(p talkPane) string {
	switch p.act {
	case attention.ActivityWorking:
		return protocol.ComposerWorking
	case attention.ActivityBlocked:
		return protocol.ComposerModal
	case attention.ActivityWaiting:
		if p.empty {
			return protocol.ComposerResting
		}
		return protocol.ComposerDraft
	}
	return protocol.ComposerUnknown
}

func wirePrompt(p attention.Prompt) protocol.AgentPrompt {
	w := protocol.AgentPrompt{Kind: p.Kind, Hash: p.Hash, Lines: p.Lines}
	for _, c := range p.Choices {
		w.Choices = append(w.Choices, protocol.AgentChoice{Key: c.Key, Label: c.Label, Detail: c.Detail})
	}
	return w
}

// setTalkPrompt keeps the prompt on screen, and when it first appeared.
func (d *Daemon) setTalkPrompt(id string, p *protocol.AgentPrompt) {
	d.talk.mu.Lock()
	defer d.talk.mu.Unlock()
	if p == nil {
		delete(d.talk.prompts, id)
		return
	}
	if d.talk.prompts == nil {
		d.talk.prompts = map[string]*protocol.AgentPrompt{}
	}
	if old := d.talk.prompts[id]; old != nil && old.Hash == p.Hash {
		p.Since = old.Since
	} else {
		p.Since = time.Now()
	}
	d.talk.prompts[id] = p
}

func (d *Daemon) talkPromptOf(id string) *protocol.AgentPrompt {
	d.talk.mu.Lock()
	defer d.talk.mu.Unlock()
	if p := d.talk.prompts[id]; p != nil {
		cp := *p
		return &cp
	}
	return nil
}

// talkReadAsync reads the pane soon after a hook says a prompt is up. The hook is async and
// can land after the answer, so it writes nothing itself: the pane is the evidence.
func (d *Daemon) talkReadAsync(id string) {
	if !d.beginConnWork() {
		return
	}
	go func() {
		defer d.connWg.Done()
		defer func() {
			if r := recover(); r != nil {
				d.logf("", "talk: pane read panicked for %s: %v", id, r)
			}
		}()
		s, ok := d.sessions.Get(id)
		if !ok {
			return
		}
		cctx, cancel := context.WithTimeout(context.Background(), reactExecTimeout)
		defer cancel()
		d.talkPaneState(cctx, s, false)
	}()
}

// recordEnded writes what is left of a session before its record goes, so a client that
// sees the row disappear can already read it. A shell has no story.
func (d *Daemon) recordEnded(s session.Session) {
	d.recordEndedAs(s, endedReason(s))
}

// recordEndedAs is recordEnded with lola's own words for why.
func (d *Daemon) recordEndedAs(s session.Session, reason string) {
	if s.IsAgentless() {
		return
	}
	id := session.BaseID(s.ID)
	store, err := d.sshStore(id, true)
	if err != nil || store == nil {
		if err != nil {
			d.logf("", "talk: %s: open the store to record its end: %v", id, err)
		}
		return
	}
	now := d.ssh.now()
	info := &sshstore.EndedInfo{
		EndedSession: protocol.EndedSession{
			ID: id, Project: s.Project, Title: s.DisplayTitle(), Issue: s.Issue,
			Agent: agent.Parse(s.Agent).String(), EndedAt: now, Reason: reason, PRURL: prURL(s),
		},
		Segments: sessionSegments(s),
	}
	if s.PR != nil {
		info.PRNumber = s.PR.Number
	}
	for _, a := range s.Apps {
		if a.PR != nil && a.PR.URL != "" && !slices.Contains(info.PRURLs, a.PR.URL) {
			info.PRURLs = append(info.PRURLs, a.PR.URL)
		}
	}
	if wt := sessionWorktree(d.home, s); wt != "" {
		info.Slug = filepath.Base(usage.SlugDir("/", wt)) // the name, wherever the root is
		if fi, err := os.Stat(wt); err == nil && fi.IsDir() {
			info.KeptWorktree = wt
		}
	}
	err = store.Update(func(st *sshstore.State) error {
		// Cleaning a rediscovered leftover must not replace its original
		// title, merged PR, transcript or end time with a bare recovery record.
		// An actual new launch clears Ended in recordSessionEvent.
		if st.Ended != nil {
			return nil
		}
		st.Ended = info
		st.AddEvent(protocol.SSHSystemEvent{Kind: protocol.SSHEventSessionEnded, Text: info.Reason}, now)
		store.Prune(st)
		return nil
	})
	if err != nil {
		d.logf("", "talk: %s: record its end: %v", id, err)
	}
	d.talk.forgetEnded()
	d.setTalkPrompt(id, nil)
}

// endedReason is lola's own words for why a session is gone.
func endedReason(s session.Session) string {
	if s.PR != nil && s.PR.Number > 0 {
		switch s.Delivery {
		case state.DeliveryMerged:
			return fmt.Sprintf("PR #%d merged", s.PR.Number)
		case state.DeliveryClosed:
			return fmt.Sprintf("PR #%d closed", s.PR.Number)
		}
	}
	return "Session closed"
}

// sessionSegments are the session's transcript files, seeded from the one path a session
// that predates the field has. Only a base name in the expected shape is ever kept.
func sessionSegments(s session.Session) []string {
	if len(s.TranscriptSegments) > 0 {
		return slices.Clone(s.TranscriptSegments)
	}
	if b := filepath.Base(s.TranscriptPath); segmentRe.MatchString(b) {
		return []string{b}
	}
	return nil
}

// handleEndedSessions lists the sessions the roster can still open. It walks the store root,
// because the daemon holds only the stores opened since it started, and it caches briefly,
// because that walk reads a file per session.
func (d *Daemon) handleEndedSessions(_ context.Context) (protocol.EndedSessionsData, error) {
	now := time.Now()
	d.talk.mu.Lock()
	if !d.talk.endedAt.IsZero() && now.Sub(d.talk.endedAt) < endedCacheFor {
		out := protocol.EndedSessionsData{Sessions: slices.Clone(d.talk.ended)}
		d.talk.mu.Unlock()
		return out, nil
	}
	d.talk.mu.Unlock()

	list := []protocol.EndedSession{}
	entries, err := os.ReadDir(sshstore.Root(d.home))
	if err == nil {
		for _, e := range entries {
			if !e.IsDir() || !sshstore.ValidSession(e.Name()) {
				continue
			}
			if _, live := d.sessions.Get(e.Name()); live {
				continue
			}
			info := sshstore.ReadEnded(d.home, e.Name())
			if info == nil || now.Sub(info.EndedAt) > sshstore.RetainAfterGone {
				continue
			}
			list = append(list, info.EndedSession)
		}
	}
	slices.SortFunc(list, func(a, b protocol.EndedSession) int { return b.EndedAt.Compare(a.EndedAt) })

	d.talk.mu.Lock()
	d.talk.ended, d.talk.endedAt = list, now
	d.talk.mu.Unlock()
	return protocol.EndedSessionsData{Sessions: slices.Clone(list)}, nil
}
