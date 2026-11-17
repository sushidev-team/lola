package daemon

import (
	"context"
	"errors"

	"github.com/sushidev-team/lola/internal/agent"
	"github.com/sushidev-team/lola/internal/attention"
	"github.com/sushidev-team/lola/internal/session"
	"github.com/sushidev-team/lola/internal/state"
)

// Why lockHumanPrompt said no. Callers put these in lola's own words.
const (
	humanWhyWorking = "working" // the agent is mid-turn, or the pane cannot be read as resting
	humanWhyDraft   = "draft"   // the agent's own prompt holds text
	humanWhyModal   = "modal"   // a screen is open that would swallow typed text
	humanWhyPrompt  = "prompt"  // a question or a usage limit is up, and typing would not answer it
	humanWhyGone    = "gone"    // no agent, or no pane to read
)

var errNoPaneSeam = errors.New("no way to read the pane")

// lockHumanPrompt is the gate for a send a person pressed this instant. It takes
// sendGate, re-reads the session, requires humanSendable, and proves from a fresh capture
// of the visible pane and cursor that the composer is resting and empty. Unlike
// lockAutomationPrompt it has no attached check and no deferral notice: the person is the
// one with the terminal, and the caller is watching for an answer.
//
// It answers unlock, held until the send is done, or a reason from the list above and a
// nil unlock. What is left is one race, and tmux offers nothing atomic to close it:
// keystrokes in an attached terminal between this proof and the send can merge with it.
func (d *Daemon) lockHumanPrompt(ctx context.Context, s session.Session) (unlock func(), why string) {
	return d.lockHumanComposer(ctx, s, false)
}

// A message submitted by the human may reach the agent during a turn. The agent
// owns when it acts on it; lola still proves that the input is an empty composer.
func (d *Daemon) lockHumanMessage(ctx context.Context, s session.Session) (unlock func(), why string) {
	return d.lockHumanComposer(ctx, s, true)
}

func (d *Daemon) lockHumanComposer(ctx context.Context, s session.Session, midTurn bool) (unlock func(), why string) {
	g := d.sendGate(s.ID)
	g.Lock()
	cur, ok := d.sessions.Get(s.ID)
	if !ok {
		g.Unlock()
		return nil, humanWhyGone
	}
	if why := recordRefusal(cur); why != "" && !(midTurn && humanMessageSendable(cur)) {
		g.Unlock()
		return nil, why
	}
	if why := d.humanComposerProof(ctx, cur, midTurn); why != "" {
		g.Unlock()
		return nil, why
	}
	return g.Unlock, ""
}

// humanSendable is handoffDeliverable plus a session that has not reported in yet. The
// pane proof still has to pass, so a starting agent is not typed at on trust.
func humanSendable(s session.Session) bool {
	return handoffDeliverable(s) || s.AgentState == state.AgentStarting
}

func humanMessageSendable(s session.Session) bool {
	if s.IsAgentless() || s.GoalManaged() {
		return false
	}
	switch s.AgentState {
	case state.AgentWorking, state.AgentStarting, state.AgentIdle:
		return true
	case state.AgentWaitingInput:
		return s.InputReason == state.InputIdleNotify
	}
	return false
}

// recordRefusal is what the record alone rules out. A dead agent comes first because a
// dying pane does not close AtPrompt, so handoffDeliverable can still say yes for it.
func recordRefusal(s session.Session) string {
	switch s.AgentState {
	case state.AgentDead, state.AgentExited, state.AgentShell, state.AgentOrphaned:
		return humanWhyGone
	}
	if humanSendable(s) {
		return ""
	}
	if s.AgentState != state.AgentWaitingInput {
		return humanWhyWorking
	}
	if s.InputReason == state.InputDialog {
		return humanWhyModal
	}
	return humanWhyPrompt
}

// humanComposerProof names what prevents safely typing into the input.
func (d *Daemon) humanComposerProof(ctx context.Context, s session.Session, midTurn bool) string {
	k := agent.Parse(s.Agent)
	p, err := d.readComposer(ctx, s)
	if err != nil {
		return humanWhyGone
	}
	switch p.activity {
	case attention.ActivityWaiting:
	case attention.ActivityWorking:
		// Mid-turn sends require the cursor proof, never just a stale prompt in scrollback.
		if !midTurn || !p.cursorKnown {
			return humanWhyWorking
		}
		p.empty = attention.MessageComposerEmptyAt(p.visible, p.cursor, k)
	case attention.ActivityBlocked:
		return humanWhyModal
	case attention.ActivityQuotaLimited:
		return humanWhyPrompt
	default:
		// A settings screen can read as nothing at all. With the cursor hidden nobody is
		// reading a line, so calling it a screen is the honest guess. Either way nothing
		// is typed.
		if p.cursorKnown && !p.cursor.Visible {
			return humanWhyModal
		}
		return humanWhyWorking
	}
	if p.empty {
		return ""
	}
	if p.cursorKnown && d.readPrompt != nil {
		if q, ok := d.readPrompt(p.visible, p.cursor, k); ok {
			if q.Kind == attention.PromptModal {
				return humanWhyModal
			}
			return humanWhyPrompt
		}
	}
	return humanWhyDraft
}

// composerRead is one proof-grade look at a pane.
type composerRead struct {
	visible     string
	cursor      attention.Cursor
	cursorKnown bool
	activity    attention.Activity
	empty       bool
}

// readComposer captures the pane and judges its composer. The screen is read before the
// cursor, so a proof that the cursor sits in the composer never rests on a screen older
// than itself. A daemon with no cursor seams answers from paneTail and EmptyComposer, the
// stricter check, and production's wiring is pinned by TestDaemonWiresPaneSeams. The
// exec is bounded here, because this runs with the send gate held.
func (d *Daemon) readComposer(ctx context.Context, s session.Session) (composerRead, error) {
	cctx, cancel := context.WithTimeout(ctx, reactExecTimeout)
	defer cancel()
	k := agent.Parse(s.Agent)
	target := paneTarget(s)
	if d.paneCursor == nil || d.paneVisible == nil {
		if d.paneTail == nil {
			return composerRead{}, errNoPaneSeam
		}
		pane, err := d.paneTail(cctx, target, observePaneLines)
		if err != nil {
			return composerRead{}, err
		}
		return composerRead{visible: pane, activity: attention.Classify(pane, k), empty: attention.EmptyComposer(pane, k)}, nil
	}
	visible, err := d.paneVisible(cctx, target)
	if err != nil {
		return composerRead{}, err
	}
	c, err := d.paneCursor(cctx, target)
	if err != nil {
		return composerRead{}, err
	}
	return composerRead{
		visible:     visible,
		cursor:      c,
		cursorKnown: true,
		activity:    attention.Classify(visible, k),
		empty:       attention.ComposerEmptyAt(visible, c, k),
	}, nil
}
