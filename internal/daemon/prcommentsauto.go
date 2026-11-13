package daemon

import (
	"context"
	"fmt"
	"maps"
	"strings"
	"time"

	"github.com/sushidev-team/lola/internal/scm"
	"github.com/sushidev-team/lola/internal/session"
	"github.com/sushidev-team/lola/internal/state"
)

// autoPRComments runs after human queues and provider handoffs. It fetches fresh
// unresolved threads only for an eligible idle agent and never queues stale PR
// instructions. A busy agent is reconsidered on the next observer cycle.
func (d *Daemon) autoPRComments(ctx context.Context, id string) bool {
	s, ok := d.sessions.Get(id)
	if !ok || !prCommentsEligible(s) {
		return false
	}
	if s.Stack && s.Lane == "" {
		for _, app := range s.Apps {
			if d.autoPRComments(ctx, session.LaneID(id, app.Name)) {
				return true
			}
		}
		return false
	}
	if s.PR == nil || s.PR.State != "OPEN" || !repoSlugRe.MatchString(s.Repo) || !d.autoPRCommentsEnabled(s) {
		return false
	}
	d.mu.Lock()
	fetch := d.prReadiness
	d.mu.Unlock()
	if fetch == nil {
		return false
	}
	readCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	pr, err := fetch(readCtx, s.Repo, s.PR.Number)
	if err != nil || pr.State != "OPEN" || pr.Number != s.PR.Number || !newPRComments(s, pr.Threads) {
		return false
	}
	unlock, ready := d.lockAutomationPrompt(ctx, s)
	if !ready {
		return false
	}
	defer unlock()
	// Config and identity may have changed during the network call or pane proof.
	cur, ok := d.sessions.Get(id)
	if !ok || !d.autoPRCommentsEnabled(cur) {
		return false
	}
	claimed := false
	d.sessions.Update(id, func(cur *session.Session) bool {
		if !prCommentsEligible(*cur) || cur.PR == nil || cur.PR.State != "OPEN" || cur.Repo != s.Repo || cur.PR.Number != pr.Number || !newPRComments(*cur, pr.Threads) {
			return false
		}
		guards := maps.Clone(cur.PRCommentGuards)
		if guards == nil {
			guards = map[string]string{}
		}
		for _, thread := range pr.Threads {
			if thread.ID != "" {
				guards[thread.ID] = prCommentRevision(*cur, thread)
			}
		}
		cur.PRCommentGuards = guards
		cur.AtPrompt = false
		cur.SetAgentState(state.AgentWorking, "", time.Now())
		claimed = true
		return true
	})
	if !claimed {
		return false
	}
	// Persist the claim before typing. Like review handoffs, an uncertain send
	// must not repeat automatically; the manual action remains available.
	d.reviewSave()
	if err := d.typeToAgentLocked(ctx, noteFor(cur, "Addressing PR comments automatically"), cur.TmuxName, sanitizeAgentText(addressPRCommentsMessage(cur)), reactExecTimeout); err != nil {
		d.logf("", "PR comments: %s send failed: %v", id, err)
	}
	return true
}

func prCommentsEligible(s session.Session) bool {
	return s.Source == "native" && s.TmuxName != "" && s.Kind != session.KindLocal &&
		!s.GoalManaged() && !strings.HasPrefix(s.Branch, "goal/") &&
		s.AgentState != state.AgentDead && s.AgentState != state.AgentExited &&
		s.AgentState != state.AgentShell && s.AgentState != state.AgentOrphaned &&
		handoffDeliverable(s)
}

func (d *Daemon) autoPRCommentsEnabled(s session.Session) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	p := d.cfg.ProjectByName(s.Project)
	return p != nil && p.AutoPRComments
}

func prCommentRevision(s session.Session, t scm.ReviewThread) string {
	// Ignore head, line and outdated changes: pushing fixes or resolving a
	// subset must not re-prompt for unchanged findings left open deliberately.
	return sentHash(fmt.Sprintf("%s#%d\n%s\n%s", s.Repo, s.PR.Number, t.ID, t.Body))
}

func newPRComments(s session.Session, threads []scm.ReviewThread) bool {
	for _, thread := range threads {
		if thread.ID != "" && s.PRCommentGuards[thread.ID] != prCommentRevision(s, thread) {
			return true
		}
	}
	return false
}
