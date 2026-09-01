package daemon

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/sushidev-team/lola/internal/agent"
	"github.com/sushidev-team/lola/internal/attention"
	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/session"
	"github.com/sushidev-team/lola/internal/state"
)

// answerExecTimeout bounds the single tmux exec each pane/answer request drives
// (capture-pane or send-keys), so a wedged tmux can never hang the socket
// handler goroutine that called it.
const answerExecTimeout = 15 * time.Second

// defaultPaneLines is how many trailing rows of a session's pane cmd=pane
// captures when the request does not bound it — enough to hold a stopped
// agent's prompt block without dragging in a screen of scrollback.
const defaultPaneLines = 40

// handlePane serves cmd=pane (PLAN P7): the read-only compact-pane view. It
// captures the last `lines` rendered rows of the session's tmux pane and runs
// the attention parser over them, returning the text plus any extracted question
// so the TUI can render an answer card. Nothing is mutated. An unknown session
// is an error; a bounded exec timeout caps the capture.
func (d *Daemon) handlePane(ctx context.Context, sessionID string, lines int) (protocol.PaneData, error) {
	if sessionID == "" {
		return protocol.PaneData{}, errors.New("session id required")
	}
	s, ok := d.sessions.Get(sessionID)
	if !ok {
		return protocol.PaneData{}, fmt.Errorf("unknown session %s", sessionID)
	}
	if lines <= 0 {
		lines = defaultPaneLines
	}
	tmuxName := paneTarget(s)

	cctx, cancel := context.WithTimeout(ctx, answerExecTimeout)
	defer cancel()
	text, err := d.paneTail(cctx, tmuxName, lines)
	if err != nil {
		return protocol.PaneData{}, fmt.Errorf("capture pane for %s: %w", sessionID, err)
	}

	pd := protocol.PaneData{Text: text}
	// The pane text is attacker-influenceable; attention.Parse only CLASSIFIES
	// it (never executes or trusts it), and the human still authors the answer.
	// Parse against the session's coding-agent cues (empty/legacy Agent → Claude).
	if q, has := attention.Parse(text, agent.Parse(s.Agent)); has {
		pd.HasQuestion = true
		pd.Prompt = q.Prompt
		pd.FreeForm = q.FreeForm
		for _, c := range q.Choices {
			pd.Choices = append(pd.Choices, protocol.PaneChoice{Key: c.Key, Label: c.Label})
		}
	}
	return pd, nil
}

// answerable reports whether a session is somewhere a human's typed reply can
// safely land. It is the AXIS form of the gate handleAnswer used to express as
// `Status != "needs_input"`, and it mirrors reviewer.go's handoffDeliverable —
// deliberately, because both type prose into the same composer.
//
// The string gate had to go: with the flap fix (see server.go's notification
// split and observer.go's agentReconcile) a finished turn is now correctly
// AgentIdle rather than waiting_input, which is EXACTLY the session a human most
// wants to reply to — and the old check refused it. Meanwhile "needs_input" also
// covered a modal and a usage-limit banner, which it should never have accepted.
//
// Accepted:
//
//   - AgentWaitingInput with InputQuestion, InputPermission, or no reason at all
//     (a legacy record, or the "" the old pane rule minted). A real answerable
//     block: an answer is the whole remedy. InputPermission is admitted here and
//     NOT by handoffDeliverable because the difference is who is typing — lola
//     pasting review findings at a y/n approval answers the wrong question, a
//     human typing "1" at it answers the right one.
//
//   - AgentIdle with AtPrompt. The agent is resting at its own composer: the
//     Stop hook, the idle nudge, or a pane read put it there.
//
// Refused, and this is the load-bearing half:
//
//   - InputDialog — a keypress-driven modal SWALLOWS typed prose and reads the
//     submit Enter as an answer to its own widget.
//   - InputQuotaLimited — the agent cannot take another turn until its quota
//     resets, so the reply is typed into a pane that will never act on it.
//
// Both rules are already documented in CLAUDE.md; this is the first path that
// enforces them for a human's answer.
func answerable(s session.Session) bool {
	switch s.AgentState {
	case state.AgentWaitingInput:
		switch s.InputReason {
		case state.InputQuestion, state.InputPermission, "":
			return true
		}
		return false
	case state.AgentIdle:
		return s.AtPrompt
	}
	return false
}

// answerRefusal explains, in the terms of the axes, why answerable said no —
// the caller is a human at a CLI or an answer card, so "not waiting for input"
// with no further detail was the least useful thing lola could have said.
func answerRefusal(sessionID string, s session.Session) error {
	switch {
	case s.AgentState == state.AgentWaitingInput && s.InputReason == state.InputDialog:
		return fmt.Errorf("session %s is showing a modal dialog — typed text is swallowed by the widget; answer it in the pane (lola attach %s)", sessionID, sessionID)
	case s.AgentState == state.AgentWaitingInput && s.InputReason == state.InputQuotaLimited:
		return fmt.Errorf("session %s has hit its usage limit — it cannot act on a reply until the quota resets", sessionID)
	case s.AgentState == state.AgentIdle:
		return fmt.Errorf("session %s is idle but not parked at its prompt (the send-keys gate is closed); try again once it settles", sessionID)
	}
	return fmt.Errorf("session %s is not waiting for input (agent %s, delivery %s)", sessionID, s.AgentState, s.Delivery)
}

// handleAnswer serves cmd=answer (PLAN P7): a HUMAN's inline reply to a session
// that stopped for input. It is REFUSED unless BOTH halves of the send-keys
// safety gate hold, so typing cannot corrupt a mid-turn agent:
//
//  1. the AXIS gate (answerable above) — the record says this is a session a
//     reply belongs in;
//  2. a live PANE PROOF — handoffPromptProof captures the pane right now and
//     insists attention.Classify reads it as ActivityWaiting. This is the same
//     evidence step the review hand-off and cmd=resolveConflict take, and for
//     the same reason: a record is a claim about the moment it was written, and
//     claude-code can end a turn (Stop hook → AtPrompt) and THEN cover the pane
//     with a modal. Reusing the helper rather than copying it is deliberate —
//     a second implementation of this check would drift out of step with the
//     rendering quirks that keep it working.
//
// Like cmd=resolveConflict and unlike the reaction engine, a failed proof
// REFUSES rather than defers: a human is watching for the reply to land, and a
// send that happens minutes later with no further sign of it is worse than an
// honest "not now".
//
// On a delivered answer the send-keys (text + Enter) goes out under a bounded
// exec timeout, then the session is flipped AtPrompt=false / status "working"
// (the agent resumes; the next hook re-derives the truth).
//
// Concurrency mirrors handleKill: the store's Get/Update are atomic under the
// store mutex and d.mu (the config mutex) is never held across the tmux exec.
// The gate check is a Get→check→send: the human initiates every answer by
// hand, so there is no auto-loop to race — unlike the reaction engine, which
// consumes AtPrompt inside Update to guard against double-sends.
func (d *Daemon) handleAnswer(ctx context.Context, sessionID, text string) error {
	if sessionID == "" {
		return errors.New("session id required")
	}
	s, ok := d.sessions.Get(sessionID)
	if !ok {
		return fmt.Errorf("unknown session %s", sessionID)
	}
	if !answerable(s) {
		return answerRefusal(sessionID, s)
	}
	if !d.handoffPromptProof(ctx, s) {
		return fmt.Errorf("session %s is not resting at its prompt right now — lola will not type into a mid-turn agent; try again when it is idle", sessionID)
	}

	cctx, cancel := context.WithTimeout(ctx, answerExecTimeout)
	defer cancel()
	// The human's answer is verbatim operator input (CLI args, or a TUI card that
	// accepts bracketed pastes), so it can carry an embedded CR — which the
	// send-keys transport types as an INDISTINGUISHABLE submit, submitting the
	// first fragment and firing the trailing bytes into the now-resumed (mid-turn)
	// agent, the exact corruption the needs_input gate exists to prevent. Route it
	// through sanitizeAgentText (as the reaction path does) so only the explicit
	// trailing Enter submits. Choice keys are already safe (constrained to
	// [0-9A-Za-z] by the parser).
	if err := d.sendKeys(cctx, paneTarget(s), sanitizeAgentText(text)); err != nil {
		return fmt.Errorf("send answer to %s: %w", sessionID, err)
	}

	// The agent is resuming: close the send-keys gate and promote its axis back
	// to working. SetAgentState stamps LastActivityAt, so the answered session
	// gets the full anti-false-working grace window even for agents that emit
	// no user_prompt hook (codex/opencode) — the old bare Status write skipped
	// the stamp and the guard downgraded them within a cycle. The next
	// lifecycle hook corrects this to the real state.
	d.sessions.Update(sessionID, func(cur *session.Session) bool {
		cur.AtPrompt = false
		cur.SetAgentState(state.AgentWorking, "", time.Now())
		return true
	})
	if err := d.sessions.Save(); err != nil {
		d.logf("", "answer: persist sessions: %v", err)
	}
	d.logf("", "answered %s", sessionID)
	return nil
}

// paneTarget is the tmux session name to capture/send for s: native sessions ARE
// tmux sessions, so a record whose TmuxName was never filled (e.g. adopted)
// falls back to the session ID.
func paneTarget(s session.Session) string {
	if s.TmuxName != "" {
		return s.TmuxName
	}
	return s.ID
}
