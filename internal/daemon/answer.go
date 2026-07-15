package daemon

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/sushidev-team/lola/internal/attention"
	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/session"
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
	if q, has := attention.Parse(text); has {
		pd.HasQuestion = true
		pd.Prompt = q.Prompt
		pd.FreeForm = q.FreeForm
		for _, c := range q.Choices {
			pd.Choices = append(pd.Choices, protocol.PaneChoice{Key: c.Key, Label: c.Label})
		}
	}
	return pd, nil
}

// handleAnswer serves cmd=answer (PLAN P7): a HUMAN's inline reply to a session
// that stopped for input. It is REFUSED unless the session's derived status is
// "needs_input" — the one moment the agent is provably parked at its own prompt,
// so typing cannot corrupt a mid-turn agent (the send-keys safety gate). On a
// delivered answer the send-keys (text + Enter) goes out under a bounded exec
// timeout, then the session is flipped AtPrompt=false / status "working" (the
// agent resumes; the next hook re-derives the truth).
//
// Concurrency mirrors handleKill: the store's Get/Update are atomic under the
// store mutex and d.mu (the config mutex) is never held across the tmux exec.
// The needs_input check is a Get→check→send: the human initiates every answer by
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
	if s.Status != "needs_input" {
		return fmt.Errorf("session %s is not waiting for input (status %s)", sessionID, s.Status)
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

	// The agent is resuming: close the send-keys gate and promote it back to
	// working. The next lifecycle hook (tool_use / stop / notification) corrects
	// this to the real state.
	d.sessions.Update(sessionID, func(cur *session.Session) bool {
		cur.AtPrompt = false
		cur.Status = "working"
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
