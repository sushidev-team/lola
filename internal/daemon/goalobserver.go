package daemon

import (
	"context"
	"time"

	"github.com/sushidev-team/lola/internal/agent"
	"github.com/sushidev-team/lola/internal/agentlog"
	"github.com/sushidev-team/lola/internal/attention"
	"github.com/sushidev-team/lola/internal/session"
	"github.com/sushidev-team/lola/internal/state"
)

// Goal agent observation deliberately reads no Git or GitHub data from worker
// repositories. The controller observes bundle submissions separately and owns
// their verified delivery. Hooks and pane evidence still drive the agent pill.
func (d *Daemon) observeGoalAgent(ctx context.Context, s session.Session, alive bool, activity time.Time) bool {
	act := attention.ActivityUnknown
	question := false
	if alive && d.paneTail != nil {
		cctx, cancel := context.WithTimeout(ctx, observeExecTimeout)
		text, err := d.paneTail(cctx, paneTarget(s), observePaneLines)
		cancel()
		if err == nil {
			k := agent.Parse(s.Agent)
			act = attention.Classify(text, k)
			_, question = attention.Parse(text, k)
		}
	}
	_, ok := d.sessions.Update(s.ID, func(cur *session.Session) bool {
		now := time.Now()
		if !alive {
			return cur.SetAgentState(state.AgentDead, "", now)
		}
		if activity.After(cur.LastActivityAt) && (cur.AgentState == state.AgentWorking || cur.AgentState == state.AgentStarting) {
			cur.TouchActivity(state.SourceTmuxActivity, now)
		}
		agentReconcile(cur, act, question, agentlog.Unknown, now)
		return true
	})
	if alive {
		d.deliverFeedback(ctx, s.ID)
	}
	return ok
}
