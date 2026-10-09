package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/sushidev-team/lola/internal/linear"
	"github.com/sushidev-team/lola/internal/notify"
	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/session"
	"github.com/sushidev-team/lola/internal/state"
)

// The PLAN-APPROVAL GATE ([[project]].require_plan). A gated session is born
// with PlanGate=planning (the runtime sets it at spawn and adds a "Plan
// approval" section to the briefing); the agent investigates, submits a plan
// with `lola plan submit` (cmd=planSubmit → submitted) and stops; a HUMAN
// approves (→ approved, coding unlocked) or requests changes with a comment
// (→ planning again, the comment relayed). Three properties hold it together:
//
//   - The gate is CONTROL state written only here and by the runtime. The
//     agent's board (`lola report`) is a claim and never moves it; the plan
//     text is untrusted agent output shown to a human and never fed back.
//   - Enforcement is the PreToolUse hook: claude's file-edit tools ask
//     cmd=planGate before running and are denied while the gate Blocks(). The
//     hook fails OPEN on an unreachable daemon (a broken lola must never wedge
//     a turn), and codex/opencode have no such hook — for them the briefing is
//     the gate. That is documented, not hidden.
//   - The verdict reaches the agent as TYPED TEXT through the same wide hand-off
//     gate + live pane proof the review hand-off uses (flushAgentNotices). It is
//     queued, never forced: a decision recorded while the agent is mid-turn is
//     delivered at its next resting prompt.

// planMaxBytes bounds a submitted plan. A plan is read by a human in a panel
// and in a Linear activity; anything longer is clipped with a marker.
const planMaxBytes = 16 << 10

// maxPendingNotices bounds the typed-message queue so a stuck session cannot
// accumulate an unbounded backlog of Linear replies.
const maxPendingNotices = 20

// Plan-gate messages typed into the agent. lola's own text.
const (
	planApprovedNotice = "Your plan was approved by a human reviewer. The file-edit tools are unlocked — implement the plan now, following the rest of your briefing in .lola/prompt.md."
	planRejectedNotice = "Your plan was NOT approved. Reviewer feedback: %s\n\nRevise the plan to address this, then submit it again with `lola plan submit` and stop. Do not edit files yet."
	planWaivedNotice   = "A human waived the plan-approval step for this session. The file-edit tools are unlocked — go ahead and implement, following the rest of your briefing in .lola/prompt.md."
)

// sanitizePlan keeps a plan readable (newlines and tabs survive, it is
// Markdown) while dropping every other control byte and clipping it.
func sanitizePlan(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.TrimSpace(sanitizeAgentText(s))
	if len(s) > planMaxBytes {
		cut := planMaxBytes
		for cut > 0 && (s[cut]&0xC0) == 0x80 { // never split a UTF-8 sequence
			cut--
		}
		s = s[:cut] + "\n\n…(plan clipped by lola)"
	}
	return s
}

// handlePlanSubmit serves cmd=planSubmit: the AGENT hands in its plan.
func (d *Daemon) handlePlanSubmit(req protocol.Request) protocol.Response {
	var a protocol.PlanSubmitArgs
	if len(req.Args) > 0 {
		if err := json.Unmarshal(req.Args, &a); err != nil {
			return protocol.Response{OK: false, Error: "planSubmit: bad args: " + err.Error()}
		}
	}
	if req.Session == "" {
		return protocol.Response{OK: false, Error: "planSubmit: no session ($LOLA_SESSION unset)"}
	}
	plan := sanitizePlan(a.Plan)
	if plan == "" {
		return protocol.Response{OK: false, Error: "planSubmit: the plan is empty — pass it on stdin or as a file argument"}
	}
	now := time.Now()
	if !d.reports.allow(req.Session, now) {
		return protocol.Response{OK: false, Error: errReportRateLimited.Error()}
	}
	var refuse error
	updated, known := d.sessions.Update(req.Session, func(s *session.Session) bool {
		switch s.PlanGate {
		case session.PlanNone:
			refuse = errors.New("this session has no plan-approval gate — just proceed with your briefing")
			return false
		case session.PlanApproved:
			refuse = errors.New("your plan is already approved — implement it")
			return false
		}
		s.Plan = plan
		s.PlanRound++
		s.PlanGate = session.PlanSubmitted
		s.PlanSubmittedAt = now
		return true
	})
	if !known {
		return protocol.Response{OK: false, Error: "planSubmit: unknown session " + req.Session}
	}
	if refuse != nil {
		return protocol.Response{OK: false, Error: refuse.Error()}
	}
	if err := d.sessions.Save(); err != nil {
		d.logf("", "planSubmit: persist sessions: %v", err)
	}
	d.logf("", "plan gate: %s submitted plan round %d — waiting for approval", req.Session, updated.PlanRound)
	d.notifyPlan(updated)
	// The Linear agent session (if any) gets the plan as an approve/reject
	// elicitation on its next mirror pass; ring the loop so that is now.
	d.wakeLinearAgent()
	return protocol.Response{OK: true}
}

// notifyPlan tells the operator a plan is waiting — a human decision is the
// only thing that moves the session now.
func (d *Daemon) notifyPlan(s session.Session) {
	d.mu.Lock()
	n := d.notifier
	d.mu.Unlock()
	if n == nil {
		return
	}
	go n.Notify(context.Background(), notify.Note{
		Title:    "Plan ready for approval",
		Body:     fmt.Sprintf("%s: the agent submitted a plan (round %d) — approve or request changes", issueLabel(s), s.PlanRound),
		Priority: notify.Urgent,
	})
}

// handlePlanDecide serves cmd=planDecide: a HUMAN's verdict from the app, the
// TUI or the CLI. The Linear path calls decidePlan directly.
func (d *Daemon) handlePlanDecide(ctx context.Context, req protocol.Request) protocol.Response {
	var a protocol.PlanDecideArgs
	if len(req.Args) > 0 {
		if err := json.Unmarshal(req.Args, &a); err != nil {
			return protocol.Response{OK: false, Error: "planDecide: bad args: " + err.Error()}
		}
	}
	if err := d.decidePlan(ctx, req.Session, a.Approve, a.Comment, "lola"); err != nil {
		return protocol.Response{OK: false, Error: err.Error()}
	}
	return protocol.Response{OK: true}
}

// decidePlan records a verdict and queues the matching message for the agent.
// source names where it came from ("lola" | "linear"), so a decision made in
// lola is echoed into the Linear agent session and one made in Linear is not
// echoed back to itself.
//
// Approve is accepted from planning as well as submitted — a human may waive
// the gate before any plan arrives. Requesting changes needs a submitted plan
// and a comment: "no" with nothing to act on would only make the agent guess.
func (d *Daemon) decidePlan(ctx context.Context, sessionID string, approve bool, comment, source string) error {
	if sessionID == "" {
		return errors.New("session id required")
	}
	comment = strings.TrimSpace(sanitizePlan(comment))
	var (
		refuse error
		notice string
	)
	updated, known := d.sessions.Update(sessionID, func(s *session.Session) bool {
		switch {
		case !s.PlanGate.Blocks():
			if s.PlanGate == session.PlanApproved {
				refuse = fmt.Errorf("session %s: the plan is already approved", sessionID)
			} else {
				refuse = fmt.Errorf("session %s has no plan-approval gate", sessionID)
			}
			return false
		case approve:
			notice = planApprovedNotice
			if s.PlanGate == session.PlanPlanning {
				notice = planWaivedNotice
			}
			s.PlanGate = session.PlanApproved
		default:
			if s.PlanGate != session.PlanSubmitted {
				refuse = fmt.Errorf("session %s has not submitted a plan yet — nothing to request changes on", sessionID)
				return false
			}
			if comment == "" {
				refuse = errors.New("say what should change: requesting changes needs a comment")
				return false
			}
			notice = fmt.Sprintf(planRejectedNotice, comment)
			s.PlanGate = session.PlanPlanning
			s.PlanFeedback = comment
		}
		s.PendingNotices = appendNotice(s.PendingNotices, notice)
		return true
	})
	if !known {
		return fmt.Errorf("unknown session %s", sessionID)
	}
	if refuse != nil {
		return refuse
	}
	if err := d.sessions.Save(); err != nil {
		d.logf("", "planDecide: persist sessions: %v", err)
	}
	verdict := "approved"
	if !approve {
		verdict = "sent back for changes"
	}
	d.logf("", "plan gate: %s plan %s (via %s)", sessionID, verdict, source)
	if source != "linear" && updated.AgentSessionID != "" {
		body := "Plan approved in lola — implementing."
		if !approve {
			body = "Changes requested in lola: " + comment
		}
		d.postAgentActivityAsync(updated.AgentSessionID, linear.Thought(body, false))
	}
	d.flushAgentNoticesAsync(sessionID)
	return nil
}

// appendNotice returns a NEW slice (store records must never alias) with msg
// appended, dropping the oldest entries past maxPendingNotices.
func appendNotice(q []string, msg string) []string {
	out := append(slices.Clone(q), msg)
	if len(out) > maxPendingNotices {
		out = out[len(out)-maxPendingNotices:]
	}
	return out
}

// handlePlanGate serves cmd=planGate: the PreToolUse hook's question "may this
// session edit files?". An unknown session is not blocked — the hook is wired
// into every claude session, gated or not.
func (d *Daemon) handlePlanGate(req protocol.Request) protocol.Response {
	s, ok := d.sessions.Get(req.Session)
	data := protocol.PlanGateData{}
	if ok && s.PlanGate.Blocks() {
		data.Blocked = true
		if s.PlanGate == session.PlanSubmitted {
			data.Reason = "lola plan gate: your plan is waiting for human approval. Do not edit files — end your turn and wait; you will be told when it is approved or what to change."
		} else {
			data.Reason = "lola plan gate: this session must have its plan approved before editing files. Investigate read-only, submit your plan with `lola plan submit`, then end your turn and wait."
		}
	}
	return dataResponse(data)
}

// planInfo flattens the gate for the wire, nil when the session has none.
func planInfo(s session.Session) *protocol.PlanInfo {
	if s.PlanGate == session.PlanNone {
		return nil
	}
	return &protocol.PlanInfo{
		Gate:        string(s.PlanGate),
		Text:        s.Plan,
		Round:       s.PlanRound,
		Feedback:    s.PlanFeedback,
		SubmittedAt: s.PlanSubmittedAt,
	}
}

// noticeDeliverable is the candidate filter for typing a queued notice: the
// review hand-off's wide gate (lola's own text at a resting prompt) or the
// answer gate (a human's relayed reply may answer a question the agent asked).
// A modal or a usage-limit banner fails both.
func noticeDeliverable(s session.Session) bool {
	if s.AgentState == state.AgentWaitingInput &&
		(s.InputReason == state.InputDialog || s.InputReason == state.InputQuotaLimited) {
		return false
	}
	return handoffDeliverable(s) || answerable(s)
}

// flushAgentNotices types the OLDEST queued notice into the agent once it rests
// at its prompt, and reports whether it typed. One per call, like the review
// hand-off flush, so two notices never land in the same composer back to back.
// Called every observer cycle, off the Stop hook, and right after a notice is
// queued; a session that is not deliverable keeps its queue.
func (d *Daemon) flushAgentNotices(ctx context.Context, id string) bool {
	s, ok := d.sessions.Get(id)
	if !ok || len(s.PendingNotices) == 0 || s.TmuxName == "" || s.IsAgentless() || !noticeDeliverable(s) {
		return false
	}
	if !d.handoffPromptProof(ctx, s) {
		return false
	}
	var (
		msg      string
		tmuxName string
	)
	d.sessions.Update(id, func(cur *session.Session) bool {
		if len(cur.PendingNotices) == 0 || !noticeDeliverable(*cur) {
			return false
		}
		msg = cur.PendingNotices[0]
		cur.PendingNotices = slices.Clone(cur.PendingNotices[1:])
		if len(cur.PendingNotices) == 0 {
			cur.PendingNotices = nil
		}
		cur.AtPrompt = false
		tmuxName = cur.TmuxName
		return true
	})
	if msg == "" {
		return false
	}
	sctx, cancel := context.WithTimeout(ctx, reactExecTimeout)
	defer cancel()
	if err := d.sendKeys(sctx, tmuxName, sanitizeAgentText(msg)); err != nil {
		// Consumed; re-queueing would risk a double send on a partial write.
		d.logf("", "notice: send-keys to %s failed: %v", id, err)
		return false
	}
	d.sessions.Update(id, func(cur *session.Session) bool {
		cur.SetAgentState(state.AgentWorking, "", time.Now())
		return true
	})
	if err := d.sessions.Save(); err != nil {
		d.logf("", "notice: persist sessions: %v", err)
	}
	d.logf("", "notice: delivered a queued message to %s", id)
	return true
}

// flushAgentNoticesAsync runs flushAgentNotices off the caller's goroutine
// (a socket handler must not hold its connection across a tmux exec), on the
// drain group so shutdown waits for an in-flight send.
func (d *Daemon) flushAgentNoticesAsync(id string) {
	if id == "" || !d.beginConnWork() {
		return
	}
	go func() {
		defer d.connWg.Done()
		defer func() {
			if r := recover(); r != nil {
				d.logf("", "notice: flush panicked for %s: %v", id, r)
			}
		}()
		d.flushAgentNotices(context.Background(), id)
	}()
}
