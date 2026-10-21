package daemon

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"sort"
	"time"

	"github.com/sushidev-team/lola/internal/agent"
	"github.com/sushidev-team/lola/internal/goal"
	"github.com/sushidev-team/lola/internal/goalplan"
	"github.com/sushidev-team/lola/internal/goalreview"
	"github.com/sushidev-team/lola/internal/goalwork"
	"github.com/sushidev-team/lola/internal/usage"
)

func (d *Daemon) observeGoalRunUsage(goalID, runID string, o usage.Observation) error {
	d.goalMu.Lock()
	g, ok := d.goals.Get(goalID)
	var err error
	if !ok || g.Execution == nil {
		d.goalMu.Unlock()
		return errors.New("usage has no authorized goal")
	}
	if old, exists := g.Execution.Runs[runID]; exists && old.Usage != nil && reflect.DeepEqual(*old.Usage, o) {
		d.goalMu.Unlock()
		_, err = d.enforceGoalUsage(goalID, time.Now().UTC())
		return err
	}
	if ok && g.Execution != nil {
		err = g.ObserveUsage(runID, o)
		if err == nil {
			_, err = d.goals.Save(g)
		}
	}
	d.goalMu.Unlock()
	if err != nil {
		return err
	}
	_, err = d.enforceGoalUsage(goalID, time.Now().UTC())
	return err
}

// The independent usage loop reads private logs outside the goal and spend
// locks. Persistence re-reads current intent so telemetry cannot undo steering.
func (d *Daemon) goalUsagePass(ctx context.Context, now time.Time) {
	if d.goals == nil || d.goalsErr != nil {
		return
	}

	ctx, cancelPass := context.WithTimeout(ctx, 10*time.Second)
	defer cancelPass()
	m := &goalwork.Manager{Root: filepath.Join(d.home, "goals")}
	scan := d.goalUsageScan
	if scan == nil {
		scan = usage.Private
	}
	type pending struct {
		goal, run, agent string
		at               time.Time
	}
	var pendingRuns []pending
	for _, snapshot := range d.goals.List() {
		if snapshot.Execution == nil {
			continue
		}
		for id, run := range snapshot.Execution.Runs {
			if ctx.Err() != nil {
				return
			}
			if run.Kind != "worker" {
				var observed *usage.Observation
				if run.Kind == "coordinator" && d.goalPlans != nil {
					if record, ok := d.goalPlans.Get(id); ok && record.GoalID == snapshot.ID {
						observed = record.Usage
					}
				} else if run.Kind == "reviewer" {
					observed, _ = (goalreview.Runner{Workspaces: m}).Usage(snapshot.ID, id)
				}
				if observed != nil && !reflect.DeepEqual(run.Usage, observed) {
					if err := d.observeGoalRunUsage(snapshot.ID, id, *observed); err != nil {
						d.logf("", "goal %s usage recovery: %v", snapshot.ID, err)
					}
				}
				continue
			}
			if run.Kind != "worker" || (run.Usage != nil && now.Sub(run.Usage.At) < time.Minute) {
				continue
			}
			a, ok := snapshot.Execution.Attempts[id]
			if !ok {
				continue
			}
			s, ok := d.sessions.Get(a.SessionID)
			if !ok {
				continue
			}
			job := pending{goal: snapshot.ID, run: id, agent: string(agent.Parse(s.Agent))}
			if run.Usage != nil {
				job.at = run.Usage.At
			}
			pendingRuns = append(pendingRuns, job)
		}
	}
	// Oldest observations first avoids starving later goals when a large or
	// unavailable transcript consumes the bounded scan window.
	sort.Slice(pendingRuns, func(i, j int) bool {
		a, b := pendingRuns[i], pendingRuns[j]
		if !a.at.Equal(b.at) {
			return a.at.Before(b.at)
		}
		return a.goal+":"+a.run < b.goal+":"+b.run
	})
	for _, job := range pendingRuns {
		if ctx.Err() != nil {
			return
		}
		dir, err := m.LaunchDirectory(job.goal, job.run)
		if err != nil {
			continue
		}
		cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		observed := scan(cctx, dir, job.agent, now)
		cancel()
		if ctx.Err() != nil {
			return
		}
		d.goalMu.Lock()
		current, exists := d.goals.Get(job.goal)
		if exists && current.Execution != nil {
			if err = current.ObserveUsage(job.run, observed); err == nil {
				_, err = d.goals.Save(current)
			}
		}
		d.goalMu.Unlock()
		if err != nil {
			d.logf("", "goal %s usage: %v", job.goal, err)
		}
		if _, err := d.enforceGoalUsage(job.goal, now); err != nil {
			d.logf("", "goal %s usage hold: %v", job.goal, err)
		}
	}
}

func (d *Daemon) enforceGoalUsage(id string, now time.Time) (goal.Goal, error) {
	d.goalMu.Lock()
	g, _ := d.goals.Get(id)
	if !g.UsageExceeded() || g.State == "ready" || g.State == "accepted" || g.State == "canceled" || g.State == "failed" {
		d.goalMu.Unlock()
		return g, nil
	}
	const reason = "goal observed usage budget reached; reported usage may be incomplete"
	var err error
	if g.State != "blocked" || g.Execution.StopWorkersAt.IsZero() {
		g.State = "blocked"
		g.Execution.Reason = reason
		g.Execution.StopWorkersAt = now
		g, err = d.goals.Save(g)
	}
	d.goalMu.Unlock()
	if err == nil {
		d.cancelGoalVerification(id)
	}
	return g, err
}

// Each private run has one ledger source. Restarts and rescans replace absolute
// totals rather than accumulating the same transcript a second time.
func recordGoalUsage(ledger *usage.Ledger, goals []goal.Goal) bool {
	changed := false
	for _, g := range goals {
		if g.Execution == nil {
			continue
		}
		for id, run := range g.Execution.Runs {
			if run.Usage == nil {
				continue
			}
			for day, total := range run.Usage.Days {
				if ledger.Set(day, "goal:"+g.ID+":"+id, usage.Entry{Project: g.Project, Agent: run.Usage.Agent, Totals: total}) {
					changed = true
				}
			}
		}
	}
	return changed
}

// A proposal has a reserved goal/run identity even before authorization. Failed
// and unaccepted calls still count against project/global usage. The same source
// key is used after acceptance, so it can never be charged twice.
func recordGoalPlanUsage(ledger *usage.Ledger, records []goalplan.UsageRecord) bool {
	changed := false
	for _, r := range records {
		if r.Usage == nil {
			continue
		}
		for day, total := range r.Usage.Days {
			if ledger.Set(day, "goal:"+r.GoalID+":"+r.ID, usage.Entry{Project: r.Project, Agent: r.Usage.Agent, Totals: total}) {
				changed = true
			}
		}
	}
	return changed
}
