package daemon

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/sushidev-team/lola/internal/notify"
	"github.com/sushidev-team/lola/internal/session"
	"github.com/sushidev-team/lola/internal/state"
)

// notifyUserReady runs after pane reconciliation and automated handoffs. Idle
// alone is not a reason to interrupt a human, nor is an approved PR alone.
func (d *Daemon) notifyUserReady(ctx context.Context, id string) {
	s, ok := d.sessions.Get(id)
	if !ok || s.Source != "native" || s.TmuxName == "" {
		return
	}
	d.mu.Lock()
	n, fetch := d.notifier, d.prReadiness
	d.mu.Unlock()
	if n == nil {
		return
	}
	if s.AgentState == state.AgentWaitingInput {
		d.notifyAgentInput(ctx, s, n)
		return
	}
	if s.Stack && s.Lane == "" {
		for _, app := range s.Apps {
			d.notifyUserReady(ctx, session.LaneID(id, app.Name))
		}
		return
	}
	if fetch == nil || !readyNotificationCandidate(s) || !d.notificationReviewsSettled(s) {
		return
	}
	// The merge queue owns the next action; there is nothing for a human yet.
	if d.mergeQueueOn() && queueMember(s) {
		return
	}
	key := readyNotificationKey(s)
	if s.ReadyNotified == key {
		return
	}
	readCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	pr, err := fetch(readCtx, s.Repo, s.PR.Number)
	if err != nil || pr.Number != s.PR.Number || pr.Head != s.PR.HeadSHA ||
		pr.Base == "" || pr.Base != s.PR.BaseSHA || pr.ChecksHead != pr.Head ||
		d.manualMergeBlocked(s, pr) != "" || !d.reviewGatesOpen(s) || !d.profileContractsCurrent(readCtx, s) {
		return // unknown, incomplete or changed facts never mean ready
	}
	cur, ok := d.sessions.Get(id)
	if !ok || !readyNotificationCandidate(cur) || readyNotificationKey(cur) != key || cur.PR.BaseSHA != pr.Base ||
		!d.notificationReviewsSettled(cur) || !d.reviewGatesOpen(cur) {
		return
	}
	claimed := false
	d.sessions.Update(id, func(cur *session.Session) bool {
		if !readyNotificationCandidate(*cur) || readyNotificationKey(*cur) != key || cur.PR.BaseSHA != pr.Base || cur.ReadyNotified == key ||
			cur.AgentStateSince != s.AgentStateSince || cur.LastActivityAt != s.LastActivityAt {
			return false
		}
		cur.ReadyNotified = key
		claimed = true
		return true
	})
	if !claimed {
		return
	}
	d.reactSave()
	checks := "CI passed"
	if pr.Checks == "none" {
		checks = "No CI checks"
	}
	_, repo, _ := strings.Cut(s.Repo, "/")
	subtitle := fmt.Sprintf("%s #%d", repo, pr.Number)
	if s.Issue != "" {
		subtitle = s.Issue + " · " + subtitle
	}
	n.Notify(ctx, notify.Note{
		Title: "Lola · PR ready", Subtitle: subtitle,
		Body: checks + " · No open review threads",
		URL:  pr.URL, Priority: notify.Action, ReadyForUser: true,
	})
}

func (d *Daemon) notifyAgentInput(ctx context.Context, s session.Session, n notify.Notifier) {
	var title, action string
	switch s.InputReason {
	case state.InputQuestion:
		title, action = "Question for you", "Answer in Lola."
	case state.InputPermission:
		title, action = "Approval needed", "Review the permission request in Lola."
	case state.InputDialog:
		title, action = "Input needed", "Respond to the dialog in Lola."
	default:
		return // idle nudges are not questions; quota has its own escalation
	}
	claimed := false
	d.sessions.Update(session.BaseID(s.ID), func(cur *session.Session) bool {
		if cur.AgentState != state.AgentWaitingInput || cur.InputReason != s.InputReason || cur.InputNotified != "" {
			return false
		}
		cur.InputNotified = s.InputReason
		claimed = true
		return true
	})
	if !claimed {
		return
	}
	d.reactSave()
	// Do not put raw permission commands or transcript text on the lock screen.
	n.Notify(ctx, notify.Note{Title: "Lola · " + title, Subtitle: issueLabel(s), Body: action, Priority: notify.Urgent})
}

func readyNotificationCandidate(s session.Session) bool {
	return s.Kind != session.KindLocal && !s.GoalManaged() && !strings.HasPrefix(s.Branch, "goal/") &&
		((s.AgentState == state.AgentIdle && s.AtPrompt && s.AtPromptVerified) || s.AgentState == state.AgentExited) &&
		(s.Delivery == state.DeliveryApproved || s.Delivery == state.DeliveryReviewPending) &&
		(s.BestOf == "" || s.BestOfWinner) &&
		s.PR != nil && s.PR.State == "OPEN" && !s.PR.IsDraft && s.PR.HeadSHA != "" && s.PR.BaseSHA != "" &&
		s.PRFetchFailures == 0 && repoSlugRe.MatchString(s.Repo) &&
		!s.QueueHeld && s.PendingReaction == "" && len(s.PendingHandoffs) == 0 && s.PendingFeedback == "" &&
		s.PendingBroadcast == "" && s.PendingSSH == ""
}

func readyNotificationKey(s session.Session) string {
	// Base movement must invalidate a read in flight, but should not re-alert a
	// person about the same finished work every time another PR lands on main.
	return fmt.Sprintf("%s#%d@%s", s.Repo, s.PR.Number, s.PR.HeadSHA)
}

// A once-per-PR execution guard is stamped BEFORE a review runs, so it cannot
// prove completion. Require an answered review and no pending work.
func (d *Daemon) notificationReviewsSettled(s session.Session) bool {
	d.reviewMu.Lock()
	busy := d.reviewActive[s.ID] > 0
	for key, running := range d.reviewBusy {
		if running && strings.HasPrefix(key, s.ID+"|") {
			busy = true
			break
		}
	}
	d.reviewMu.Unlock()
	if busy {
		return false
	}
	for _, p := range d.appliesIndependently() {
		if p.Shape != shapePass || !p.OnPROpen {
			continue
		}
		tally, ok := s.ReviewTallies[string(p.Kind)]
		if !ok || tally.PR != s.PR.Number {
			return false
		}
		// Findings in a plain comment have no resolvable threads. Only a clean
		// follow-up pass can prove those are finished; inline threads are checked
		// by the fresh, fully paginated GitHub readiness query above.
		if tally.Blocker+tally.Major+tally.Minor+tally.Other > 0 && s.InlineReviewPRs[string(p.Kind)] != s.PR.Number {
			return false
		}
	}
	return true
}
