package daemon

// The human's side of the conversation: saying something, stopping a turn, pressing a key
// on a card. Every one of these re-reads the pane, because a client's picture of the
// session is at best a second old and the wrong keystroke cannot be taken back.

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/sushidev-team/lola/internal/agent"
	"github.com/sushidev-team/lola/internal/agentlog"
	"github.com/sushidev-team/lola/internal/attention"
	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/session"
	"github.com/sushidev-team/lola/internal/sshelement"
	"github.com/sushidev-team/lola/internal/sshstore"
	"github.com/sushidev-team/lola/internal/state"
	"github.com/sushidev-team/lola/internal/taskboard"
)

const (
	// stopHold is how long every automated send waits after a human's Stop, so a CI
	// reaction or a queued message does not type the moment the pane rests.
	stopHold = 30 * time.Second
	// stopDebounce is per session: two Escs on an empty composer open Rewind.
	stopDebounce = 600 * time.Millisecond
	// escSettle is how long after an Esc the first keystroke may land, or it is eaten.
	escSettle = 50 * time.Millisecond
	// returnedWindow is how recent a delivery must be for an Esc to have handed it back.
	returnedWindow = 120 * time.Second

	goalRefusal = "A goal runs this session"
)

// How long the pane gets to settle before it is re-read. Variables so a test need not sit
// through them.
var (
	stopSettle   = 400 * time.Millisecond
	chooseSettle = 300 * time.Millisecond
	// stopSendWait bounds how long Stop & send waits for the pane to rest.
	stopSendWait = 1500 * time.Millisecond
)

// talkMem is the conversation's memory-only state. None of it survives a restart, and a
// prompt in particular never reaches the snapshot, a store or a log: it is pane text. Its
// zero value is ready to use.
type talkMem struct {
	mu          sync.Mutex
	lastEsc     map[string]time.Time
	prompts     map[string]*protocol.AgentPrompt
	turnStart   map[string]time.Time
	lastStop    map[string]time.Time
	skipReceipt map[string]bool
	ownPrompt   map[string]time.Time
	ciRed       map[string]int
	ended       []protocol.EndedSession
	endedAt     time.Time
}

// noteEsc records an Esc, and reports false when one was sent too recently for another.
func (t *talkMem) noteEsc(id string, now time.Time) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if last, ok := t.lastEsc[id]; ok && now.Sub(last) < stopDebounce {
		return false
	}
	if t.lastEsc == nil {
		t.lastEsc = map[string]time.Time{}
	}
	t.lastEsc[id] = now
	return true
}

// waitAfterEsc holds a send until the Esc before it has settled.
func (t *talkMem) waitAfterEsc(id string) {
	t.mu.Lock()
	last, ok := t.lastEsc[id]
	t.mu.Unlock()
	if !ok {
		return
	}
	if wait := time.Until(last.Add(escSettle)); wait > 0 {
		time.Sleep(wait)
	}
}

// noteTurnStart marks the first prompt since the last turn ended.
func (t *talkMem) noteTurnStart(id string, now time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.turnStart == nil {
		t.turnStart = map[string]time.Time{}
	}
	if _, ok := t.turnStart[id]; !ok {
		t.turnStart[id] = now
	}
}

// turnSpan ends a turn and answers how long it took: from its first prompt when a hook
// reported one, else from the last turn's end, for an agent with no turn-start hook. Zero
// means nobody knows, and a receipt then says no time at all.
func (t *talkMem) turnSpan(id string, now time.Time) time.Duration {
	t.mu.Lock()
	defer t.mu.Unlock()
	start, ok := t.turnStart[id]
	if !ok {
		start, ok = t.lastStop[id]
	}
	delete(t.turnStart, id)
	if t.lastStop == nil {
		t.lastStop = map[string]time.Time{}
	}
	t.lastStop[id] = now
	if !ok {
		return 0
	}
	return now.Sub(start)
}

// markSkipReceipt says the next recorded turn end was a human's Stop, whose own row is the
// receipt. takeSkipReceipt reads and clears that.
func (t *talkMem) markSkipReceipt(id string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.skipReceipt == nil {
		t.skipReceipt = map[string]bool{}
	}
	t.skipReceipt[id] = true
}

func (t *talkMem) takeSkipReceipt(id string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	skip := t.skipReceipt[id]
	delete(t.skipReceipt, id)
	return skip
}

// ownPromptWindow is how long after lola types its own prompt hook is expected.
const ownPromptWindow = 15 * time.Second

// markOwnPrompt says the next user_prompt hook is for something lola is typing right now, not
// a person at the terminal. It is taken before the send, because the hook can arrive before
// the send returns, and unmarked if the send fails.
func (t *talkMem) markOwnPrompt(id string, now time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.ownPrompt == nil {
		t.ownPrompt = map[string]time.Time{}
	}
	t.ownPrompt[id] = now
}

func (t *talkMem) unmarkOwnPrompt(id string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.ownPrompt, id)
}

// takeOwnPrompt reads and clears the mark. One send has one hook.
func (t *talkMem) takeOwnPrompt(id string, now time.Time) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	at, ok := t.ownPrompt[id]
	delete(t.ownPrompt, id)
	return ok && now.Sub(at) < ownPromptWindow
}

// noteCIRed remembers which PR last read red. takeCIGreen reads and clears it: a recovery is
// one row. Memory only, so a restart in between loses the green row and never invents one.
func (t *talkMem) noteCIRed(id string, pr int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.ciRed == nil {
		t.ciRed = map[string]int{}
	}
	t.ciRed[id] = pr
}

func (t *talkMem) takeCIGreen(id string, pr int) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.ciRed[id] != pr {
		return false
	}
	delete(t.ciRed, id)
	return true
}

func (t *talkMem) forgetEnded() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.endedAt = time.Time{}
}

// humanText is the one normalizer for words a person sent. Control bytes go and tabs become
// spaces, and otherwise the text keeps the shape it was written in, indentation and blank
// lines included, because the composer showed it that way and pasted code means it. Blank
// lines before the first and nothing after the last. The trailing backslash goes too
// (trimContinuation). It does not bound anything: the caller refuses what is too long.
func humanText(s string) string {
	s = strings.ReplaceAll(sshelement.Verbatim(s), "\t", "    ")
	s = trimContinuation(s)
	s = strings.TrimRightFunc(s, unicode.IsSpace)
	for {
		line, rest, more := strings.Cut(s, "\n")
		if !more || strings.TrimSpace(line) != "" {
			return s
		}
		s = rest
	}
}

// slashRe is a slash command: one word with no further slash, so /etc/hosts is a sentence.
var slashRe = regexp.MustCompile(`^/[A-Za-z][A-Za-z0-9_:-]*(\s|$)`)

func isSlash(text string) bool { return slashRe.MatchString(text) }

// screenRefusal is the one sentence for a command lola will not type.
func screenRefusal(word string) error {
	return fmt.Errorf("%s opens a screen that only takes keys, so lola did not send it. Use the terminal", word)
}

// talkPane is one fresh read of an agent's pane.
type talkPane struct {
	visible   string
	cursor    attention.Cursor
	hasCursor bool
	act       attention.Activity
	empty     bool
}

// readTalkPane captures the visible pane and then the cursor, in that order. A test or an
// agent with no cursor proof falls back to the tail and EmptyComposer.
func (d *Daemon) readTalkPane(ctx context.Context, s session.Session) (talkPane, error) {
	target := paneTarget(s)
	k := agent.Parse(s.Agent)
	var p talkPane
	var err error
	switch {
	case d.paneVisible != nil:
		p.visible, err = d.paneVisible(ctx, target)
	case d.paneTail != nil:
		p.visible, err = d.paneTail(ctx, target, observePaneLines)
	default:
		err = errors.New("no pane reader")
	}
	if err != nil {
		return talkPane{}, err
	}
	if agent.Caps(k).Cursor && d.paneCursor != nil {
		if c, cerr := d.paneCursor(ctx, target); cerr == nil {
			p.cursor, p.hasCursor = c, true
		}
	}
	p.act = attention.Classify(p.visible, k)
	if p.hasCursor {
		p.empty = attention.ComposerEmptyAt(p.visible, p.cursor, k)
	} else {
		p.empty = attention.EmptyComposer(p.visible, k)
	}
	return p, nil
}

// talkSentence is lola's reason a message stayed in the queue, from the gate's one word.
func talkSentence(why string, s session.Session) string {
	who := agentWord(s)
	if why == "" {
		switch {
		case !sshAgentAlive(s):
			why = "gone"
		case s.AgentState == state.AgentWaitingInput:
			why = "prompt"
		default:
			why = "working"
		}
	}
	switch why {
	case "draft":
		return "There is a draft in " + who + "'s prompt. This goes once it is empty."
	case "modal":
		return "A screen is open in " + who + "'s terminal. This goes once it closes."
	case "prompt":
		return who + " is asking something. This goes once that is answered."
	case "gone":
		return who + " is not running. This goes if it comes back."
	default:
		return who + " is working. This goes when it rests."
	}
}

// talkCaps maps what lola has measured onto the wire, for this one session.
func talkCaps(s session.Session) protocol.TalkCaps {
	c := agent.Caps(agent.Parse(s.Agent))
	out := protocol.TalkCaps{Feed: c.Feed, Send: c.Send, Stop: c.Stop, Cards: c.Cards}
	switch {
	case s.GoalManaged():
		out.Send, out.Stop, out.Cards = false, false, false
		out.Why = goalRefusal
	case s.IsAgentless():
		out = protocol.TalkCaps{}
	case !out.Send:
		out.Why = "lola can't confirm this agent's prompt is free, so messages wait for it to rest."
	}
	return out
}

// sshSpeakFrom applies the rule for a message's tab. With a host in scope, speaking hands
// that tab back and makes it the one the agent's next command goes to. Under All, neither:
// one chat message must not return every wheel. It answers the tab, and a display name when
// the agent cannot tell which host was meant.
func (d *Daemon) sshSpeakFrom(ctx context.Context, store *sshstore.Store, id, conn string) (tabID, onHost string) {
	driving := false
	_ = store.Update(func(st *sshstore.State) error {
		tab := st.Conn(conn)
		if tab == nil {
			return nil
		}
		tabID, st.TalkConn = tab.ID, tab.ID
		live := st.LiveConns()
		driving = tab.Wheel == protocol.SSHWheelHuman && !tab.Terminal()
		if len(live) > 1 {
			onHost = sshDisplay(tab)
		}
		return nil
	})
	if driving {
		if _, err := d.handleSSHHandback(ctx, protocol.SSHHandbackArgs{Conn: tabID}); err != nil {
			d.logf("", "ssh: %s: hand back %s: %v", id, tabID, err)
		}
	}
	return tabID, onHost
}

// handleAgentSay is a human's message. It records and queues first, because the words exist
// nowhere else, and only then tries to type them.
// typedSayBytes bounds what goes to an agent as keystrokes, the size every send used to be held to.
const typedSayBytes = 8 * 1024

func (d *Daemon) handleAgentSay(ctx context.Context, a protocol.AgentSayArgs) (protocol.AgentSayData, error) {
	id := session.BaseID(strings.TrimSpace(a.Session))
	store, alive, err := d.sshTalkStore(id)
	if err != nil {
		return protocol.AgentSayData{}, err
	}
	s, _ := d.sessions.Get(id)
	if s.GoalManaged() {
		return protocol.AgentSayData{}, errors.New(goalRefusal)
	}
	text := humanText(a.Text)
	if text == "" {
		return protocol.AgentSayData{}, errors.New("nothing to say")
	}
	if len(text) > protocol.SSHMaxSayBytes {
		return protocol.AgentSayData{}, fmt.Errorf("this is %d bytes and the limit is %d, so nothing was sent", len(text), protocol.SSHMaxSayBytes)
	}
	// An agent whose paste timing was never measured gets keystrokes, which tmux refuses past about
	// 16 KB, so the larger limit is only for the agents that take a paste.
	if !agent.Caps(agent.Parse(s.Agent)).Paste && len(text) > typedSayBytes {
		return protocol.AgentSayData{}, fmt.Errorf("this is %d bytes and this agent can take %d, so nothing was sent", len(text), typedSayBytes)
	}
	// An indented command is still a command to the agent. A client can have any idea of what
	// a line is, so the rule against typing a screen command holds here, ahead of any effect.
	command := isSlash(strings.TrimSpace(text))
	if command {
		text = strings.TrimSpace(text)
		if word := strings.Fields(text)[0]; agent.ScreenCommand(agent.Parse(s.Agent), word) {
			return protocol.AgentSayData{}, screenRefusal(word)
		}
	}
	tabID, onHost := d.sshSpeakFrom(ctx, store, id, a.Conn)

	queued := text
	if onHost != "" && !command {
		queued = "[lola] Said while looking at " + onHost + ":\n" + text
	}
	if len(s.Tasks) > 0 && !command && !strings.Contains(queued, taskboard.ConversationHint) {
		queued += "\n\n" + taskboard.ConversationHint
	}
	now := d.ssh.now()
	var item string
	err = store.Update(func(st *sshstore.State) error {
		if st.QueueBytes()+len(queued) > sshstore.MaxQueueBytes {
			return errSSHQueueFull
		}
		it := st.AddHuman(protocol.SSHHumanMessage{Origin: protocol.SSHHumanSay, Text: text, Conn: tabID}, now)
		st.EnqueueEntry(sshstore.QueueEntry{Item: it.ID, Text: queued, Human: true, Command: command})
		store.Prune(st)
		item = it.ID
		return nil
	})
	if err != nil {
		return protocol.AgentSayData{}, err
	}
	stopped := false
	if a.StopFirst && agent.Caps(agent.Parse(s.Agent)).Stop {
		var leftInsert bool
		stopped, leftInsert = d.stopThenRest(ctx, id)
		if leftInsert {
			return d.holdForVim(id, item, store, alive), nil
		}
	}
	var keepHold func() bool
	if stopped {
		// The agent was just stopped and these words did not reach it, so nothing automated
		// may be the next thing it hears.
		keepHold = func() bool { return !sshItemDelivered(store, item) }
	}
	out := d.talkAttempt(ctx, id, store, keepHold, !a.StopFirst)
	out.Item = item
	out.Delivered = sshItemDelivered(store, item)
	if out.Delivered {
		out.Why = ""
	}
	out.AgentGone = !alive
	return out, nil
}

// handleAgentQueueRelease is "Send them": a press, so it takes the same path as a message
// sent now. What it attempts is the head batch.
func (d *Daemon) handleAgentQueueRelease(ctx context.Context, a protocol.AgentQueueReleaseArgs) (protocol.AgentSayData, error) {
	id := session.BaseID(strings.TrimSpace(a.Session))
	store, alive, err := d.sshTalkStore(id)
	if err != nil {
		return protocol.AgentSayData{}, err
	}
	var head string
	store.View(func(st *sshstore.State) {
		if len(st.Queue) > 0 {
			head = st.Queue[0].Item
		}
	})
	out := d.talkAttempt(ctx, id, store, nil, true)
	out.Item = head
	out.AgentGone = !alive
	if head != "" {
		out.Delivered = sshItemDelivered(store, head)
	}
	if out.Delivered {
		out.Why = ""
	}
	return out, nil
}

// talkAttempt releases the held queue, because the human just spoke, and tries the head
// batch once. The stop hold is cleared AFTER the attempt, so nothing automated can type
// between the release and the lock. What does not go now stays queued for the ordinary
// flush, attached check included. keepHold, when set, answers at that point whether the hold
// stays, for a stop that has only just been pressed.
func (d *Daemon) talkAttempt(ctx context.Context, id string, store *sshstore.Store, keepHold func() bool, midTurn bool) protocol.AgentSayData {
	d.sessions.Update(id, func(cur *session.Session) bool {
		if !cur.QueueHeld {
			return false
		}
		cur.QueueHeld = false
		return true
	})
	d.sshSyncPending(id, store)
	s, _ := d.sessions.Get(id)
	// A permission park has no hook to end it when the answer was No. A person pressing send
	// is reason enough to look now, or the message waits on a question that is gone.
	if s.AgentState == state.AgentWaitingInput && s.InputReason == state.InputPermission {
		cctx, cancel := context.WithTimeout(ctx, reactExecTimeout)
		d.talkPaneState(cctx, s, false)
		cancel()
		s, _ = d.sessions.Get(id)
	}
	var why string
	refused := false
	if agent.Caps(agent.Parse(s.Agent)).Send {
		lock, admit := d.humanLock, humanSendable
		// Slash commands belong at rest. Ordinary messages use the agent's own
		// mid-turn input handling; automated review/CI queues retain their idle gate.
		if midTurn && !isSlash(strings.TrimSpace(s.PendingSSH)) {
			lock = d.lockHumanMessage
			admit = func(cur session.Session) bool {
				return humanMessageSendable(cur) && !isSlash(strings.TrimSpace(cur.PendingSSH))
			}
		}
		d.deliverSSH(ctx, id, store, queuedText{
			lock:  lock,
			admit: admit,
			refused: func(w string) {
				why, refused = w, true
			},
		})
	} else {
		d.deliverSSHQueue(ctx, id)
		why = "no-send"
	}
	if keepHold == nil || !keepHold() {
		d.sessions.Update(id, func(cur *session.Session) bool {
			if cur.StopHoldUntil.IsZero() {
				return false
			}
			cur.StopHoldUntil = time.Time{}
			return true
		})
	}
	if err := d.sessions.Save(); err != nil {
		d.logf("", "talk: persist sessions: %v", err)
	}
	s, _ = d.sessions.Get(id)
	out := protocol.AgentSayData{}
	switch {
	case why == "no-send":
		out.Why = talkCaps(s).Why
	case refused:
		out.Why = talkSentence(why, s)
	default:
		out.Why = talkSentence("", s)
	}
	return out
}

// stopLocked is the Esc and its proof, with no hold and no queue change, for an agent whose
// stop was measured. The caller holds the send gate. It answers false with lola's sentence
// when nothing was sent, and the pane it read before the Esc.
func (d *Daemon) stopLocked(ctx context.Context, id string) (bool, string, talkPane) {
	s, ok := d.sessions.Get(id)
	if !ok {
		return false, "Already stopped", talkPane{}
	}
	if !agent.Caps(agent.Parse(s.Agent)).Stop {
		return false, "lola can't stop this agent from here. Use its terminal.", talkPane{}
	}
	return d.escIfWorking(ctx, id, s)
}

// escIfWorking is the one Esc that interrupts a turn. It needs a working pane on a fresh read,
// because at rest an Esc means something else, and the debounce, because two on an empty
// composer open Rewind. The caller holds the send gate.
func (d *Daemon) escIfWorking(ctx context.Context, id string, s session.Session) (bool, string, talkPane) {
	if d.sendEscape == nil || !sshAgentAlive(s) {
		return false, "Already stopped", talkPane{}
	}
	cctx, cancel := context.WithTimeout(ctx, reactExecTimeout)
	defer cancel()
	p, err := d.readTalkPane(cctx, s)
	if err != nil || p.act != attention.ActivityWorking {
		return false, "Already stopped", p
	}
	if !d.talk.noteEsc(id, time.Now()) {
		return false, "Already stopped", p
	}
	if err := d.sendEscape(cctx, paneTarget(s)); err != nil {
		d.logf("", "talk: %s: stop: %v", id, err)
		return false, "Could not reach the terminal", p
	}
	return true, "", p
}

// stopThenRest is Stop & send's stop: the Esc under the gate, then at least the settle, then
// up to stopSendWait for the pane to rest and be empty. The gate is released before it
// returns, because sendGate is not reentrant and the send takes it next. The hold starts with
// the Esc, so nothing automated can be first into the pane, and the caller ends it.
//
// It answers whether an Esc went, and whether that took a vim composer out of INSERT. The
// second is judged by comparing the pane across the Esc, because a pane cannot show NORMAL.
func (d *Daemon) stopThenRest(ctx context.Context, id string) (stopped, leftInsert bool) {
	g := d.sendGate(id)
	g.Lock()
	defer g.Unlock()
	stopped, _, before := d.stopLocked(ctx, id)
	if !stopped {
		return false, false
	}
	s, ok := d.sessions.Get(id)
	if !ok {
		return true, false
	}
	d.sessions.Update(id, func(cur *session.Session) bool {
		cur.StopHoldUntil = time.Now().Add(stopHold)
		return true
	})
	time.Sleep(escSettle)
	deadline := time.Now().Add(stopSendWait)
	var last talkPane
	read := false
	for {
		cctx, cancel := context.WithTimeout(ctx, reactExecTimeout)
		p, err := d.readTalkPane(cctx, s)
		cancel()
		if err == nil {
			last, read = p, true
		}
		if read && last.act == attention.ActivityWaiting && last.empty {
			break
		}
		if time.Now().After(deadline) {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if read && last.act == attention.ActivityWaiting {
		// Esc fires no Stop hook, so the axis has to learn this here, or the gate the send
		// is about to use still reads a working agent.
		d.reconcileFromPane(id, last, nil, false)
		if !last.empty {
			d.markReturned(id) // the same hand back afterStop looks for
		}
		leftInsert = attention.VimInsert(before.visible) && !attention.VimInsert(last.visible)
	}
	d.noteStopped(id)
	return true, leftInsert
}

// holdForVim keeps a message off a composer that left INSERT. Typed there it would run as vim
// commands, so it stays queued behind the hold, where "Send them" is a person's choice once the
// mode is back. The hold on automation stays for the same reason.
func (d *Daemon) holdForVim(id, item string, store *sshstore.Store, alive bool) protocol.AgentSayData {
	d.sessions.Update(id, func(cur *session.Session) bool {
		cur.QueueHeld = true
		return true
	})
	d.sshSyncPending(id, store)
	s, _ := d.sessions.Get(id)
	return protocol.AgentSayData{
		Item:      item,
		AgentGone: !alive,
		Why:       "The stop took " + agentWord(s) + "'s prompt out of vim INSERT. This waits until you press i in its terminal and send it.",
	}
}

// handleAgentStop stops this turn. It needs a working pane on a fresh capture, because at
// rest an Esc means something else, and it holds the human's queue and all automation so
// nothing types the moment the pane rests after someone said stop.
func (d *Daemon) handleAgentStop(ctx context.Context, a protocol.AgentStopArgs) (protocol.AgentStopData, error) {
	id := session.BaseID(strings.TrimSpace(a.Session))
	s, ok := d.sessions.Get(id)
	if !ok {
		return protocol.AgentStopData{}, fmt.Errorf("unknown session %s", id)
	}
	if s.GoalManaged() {
		return protocol.AgentStopData{}, errors.New(goalRefusal)
	}
	if s.IsAgentless() {
		return protocol.AgentStopData{}, fmt.Errorf("%s has no coding agent", id)
	}
	held := 0
	g := d.sendGate(id)
	g.Lock()
	stopped, why, _ := d.stopLocked(ctx, id)
	if stopped {
		store, _ := d.sshStore(id, false)
		if store != nil {
			store.View(func(st *sshstore.State) { held = len(st.Queue) })
		}
		d.sessions.Update(id, func(cur *session.Session) bool {
			cur.StopHoldUntil = time.Now().Add(stopHold)
			cur.QueueHeld = held > 0
			return true
		})
		if store != nil {
			d.sshSyncPending(id, store)
		}
		if err := d.sessions.Save(); err != nil {
			d.logf("", "talk: persist sessions: %v", err)
		}
	}
	g.Unlock()
	if !stopped {
		return protocol.AgentStopData{Stopped: false, Why: why}, nil
	}
	if d.beginConnWork() {
		go func() {
			defer d.connWg.Done()
			defer func() {
				if r := recover(); r != nil {
					d.logf("", "talk: after-stop panicked for %s: %v", id, r)
				}
			}()
			time.Sleep(stopSettle)
			d.afterStop(context.Background(), id)
		}()
	}
	return protocol.AgentStopData{Stopped: true, Held: held}, nil
}

// afterStop reads the pane once it has settled. Esc fires no Stop hook, so without this the
// pill says working for up to 30 seconds. An Esc before the first output also puts the
// message back in the composer while the log says it was sent, and the row has to say so.
func (d *Daemon) afterStop(ctx context.Context, id string) {
	s, ok := d.sessions.Get(id)
	if !ok {
		return
	}
	cctx, cancel := context.WithTimeout(ctx, reactExecTimeout)
	defer cancel()
	p, err := d.readTalkPane(cctx, s)
	if err == nil {
		d.reconcileFromPane(id, p, nil, false)
		if p.act == attention.ActivityWaiting && !p.empty {
			d.markReturned(id)
		}
	}
	d.noteStopped(id)
}

// noteStopped records the turn's end and leaves the row that says who ended it.
func (d *Daemon) noteStopped(id string) {
	span := d.talk.turnSpan(id, time.Now())
	if d.checkpoints != nil {
		d.talk.markSkipReceipt(id) // the stopped row is this turn's receipt
		d.recordCheckpointAsync(id, "stop")
	}
	d.emitSystem(timelineEvent{Session: id, Event: protocol.SSHSystemEvent{
		Kind:       protocol.SSHEventStopped,
		Text:       "You stopped this turn. Background shells keep running.",
		DurationMs: span.Milliseconds(),
	}})
}

// markReturned flags the newest message delivered in the last while as back in the prompt.
func (d *Daemon) markReturned(id string) {
	store, err := d.sshStore(id, false)
	if err != nil || store == nil {
		return
	}
	now := d.ssh.now()
	_ = store.Update(func(st *sshstore.State) error {
		for i := len(st.Items) - 1; i >= 0; i-- {
			it := st.Items[i]
			if it.Human == nil || !it.Human.Delivered || it.Human.Origin == protocol.SSHHumanChoice {
				continue
			}
			if now.Sub(it.Human.DeliveredAt) <= returnedWindow {
				it.Human.Returned = true
				st.Touch(it)
			}
			return nil
		}
		return nil
	})
}

// reconcileFromPane moves the agent axis from a fresh read, through agentReconcile rather
// than a second set of rules. answered means a prompt lola had read is gone: agentReconcile
// will not release a permission or dialog park from a resting pane, because the composer can
// look at rest while nothing can proceed, but here the pane showed the block and no longer
// does, and a denied prompt fires no hook. A modal raises nothing.
func (d *Daemon) reconcileFromPane(id string, p talkPane, prompt *attention.Prompt, answered bool) {
	now := time.Now()
	changed := false
	d.sessions.Update(id, func(cur *session.Session) bool {
		switch {
		case prompt != nil && prompt.Kind == "modal":
			return false
		case prompt != nil:
			changed = agentReconcile(cur, attention.ActivityWaiting, true, agentlog.Unknown, now)
			if cur.AgentState == state.AgentWaitingInput {
				reason := state.InputQuestion
				if prompt.Kind == "permission" {
					reason = state.InputPermission
				}
				if cur.InputReason != reason {
					cur.InputReason = reason
					changed = true
				}
			}
		case p.act == attention.ActivityWorking:
			changed = agentReconcile(cur, p.act, false, agentlog.Unknown, now)
		case p.act == attention.ActivityWaiting:
			if answered && cur.AgentState == state.AgentWaitingInput {
				cur.InputReason = ""
			}
			if !answered && cur.AgentState != state.AgentWorking {
				return false // the observer owns a resting pane on a resting record
			}
			_, question := attention.Parse(p.visible, agent.Parse(cur.Agent))
			changed = agentReconcile(cur, p.act, question, agentlog.Unknown, now)
		}
		return changed
	})
	if changed {
		if err := d.sessions.Save(); err != nil {
			d.logf("", "talk: persist sessions: %v", err)
		}
	}
}

// handleAgentChoose presses one key on a card. The read sits immediately before the press,
// under the gate: a stale digit would become a draft, and labels are identical across
// prompts, so the hash of the lines a human read is what pins the answer to the question.
// It is one key with no Enter, and it never goes near sendKeys.
func (d *Daemon) handleAgentChoose(ctx context.Context, a protocol.AgentChooseArgs) (protocol.AgentChooseData, error) {
	id := session.BaseID(strings.TrimSpace(a.Session))
	s, ok := d.sessions.Get(id)
	if !ok {
		return protocol.AgentChooseData{}, fmt.Errorf("unknown session %s", id)
	}
	if s.GoalManaged() {
		return protocol.AgentChooseData{}, errors.New(goalRefusal)
	}
	if s.IsAgentless() {
		return protocol.AgentChooseData{}, fmt.Errorf("%s has no coding agent", id)
	}
	k := agent.Parse(s.Agent)
	if !agent.Caps(k).Cards {
		return protocol.AgentChooseData{Why: "lola can't read this agent's prompts. Use its terminal."}, nil
	}

	label := ""
	sent, why := func() (bool, string) {
		g := d.sendGate(id)
		g.Lock()
		defer g.Unlock()
		cctx, cancel := context.WithTimeout(ctx, reactExecTimeout)
		defer cancel()
		p, err := d.readTalkPane(cctx, s)
		if err != nil || !p.hasCursor {
			return false, "Could not read the terminal"
		}
		prompt, found := d.readPrompt(p.visible, p.cursor, k)
		if !found || prompt.Hash != a.Hash {
			return false, "This question has changed. Read it again."
		}
		known := false
		for _, c := range prompt.Choices {
			if c.Key == a.Key {
				known, label = true, c.Label
				break
			}
		}
		if !known {
			return false, "That answer is no longer offered."
		}
		if d.sendKey == nil {
			return false, "Could not reach the terminal"
		}
		if err := d.sendKey(cctx, paneTarget(s), a.Key); err != nil {
			d.logf("", "talk: %s: choose: %v", id, err)
			return false, "Could not reach the terminal"
		}
		return true, ""
	}()
	if !sent {
		out := protocol.AgentChooseData{Why: why}
		// A refused card has a stale question on it, so hand back what the pane asks now.
		cctx, cancel := context.WithTimeout(ctx, reactExecTimeout)
		defer cancel()
		_, out.Prompt = d.talkPaneState(cctx, s, false)
		return out, nil
	}

	// A human's pressed key is a row, drawn as a moment because the label is pane text.
	if store, err := d.sshStore(id, true); err == nil && store != nil {
		now := d.ssh.now()
		text := "You chose: " + sshelement.Line(a.Key+" "+label, 120)
		if err := store.Update(func(st *sshstore.State) error {
			st.AddHuman(protocol.SSHHumanMessage{Origin: protocol.SSHHumanChoice, Text: text, Delivered: true, DeliveredAt: now}, now)
			store.Prune(st)
			return nil
		}); err != nil {
			d.logf("", "talk: %s: record the choice: %v", id, err)
		}
	}

	time.Sleep(chooseSettle)
	cctx, cancel := context.WithTimeout(ctx, reactExecTimeout)
	defer cancel()
	cur, ok := d.sessions.Get(id)
	if !ok {
		return protocol.AgentChooseData{Sent: true}, nil
	}
	_, next := d.talkPaneState(cctx, cur, true)
	return protocol.AgentChooseData{Sent: true, Prompt: next}, nil
}
