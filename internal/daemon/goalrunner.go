package daemon

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/sushidev-team/lola/internal/agent"
	"github.com/sushidev-team/lola/internal/goal"
	"github.com/sushidev-team/lola/internal/goalwork"
	"github.com/sushidev-team/lola/internal/runtime"
	"github.com/sushidev-team/lola/internal/session"
	"github.com/sushidev-team/lola/internal/state"
)

func (d *Daemon) goalLoop(ctx context.Context) {
	defer d.wg.Done()
	timer := time.NewTicker(15 * time.Second)
	defer timer.Stop()
	for {
		d.runGoals(ctx)
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
	}
}

func (d *Daemon) runGoals(ctx context.Context) {
	if d.goals == nil || d.goalsErr != nil {
		return
	}
	d.goalRunMu.Lock()
	defer d.goalRunMu.Unlock()
	defer d.syncGoalProgress(ctx)
	defer func() {
		if v := recover(); v != nil {
			d.logf("", "goal controller panicked; durable intents retained: %v", v)
		}
	}()
	for _, g := range d.goals.List() {
		if ctx.Err() != nil {
			return
		}
		if g.Execution == nil {
			continue
		}
		if err := d.runGoal(ctx, g.ID); err != nil {
			d.logf("", "goal %s: %v", g.ID, err)
		}
	}
}

func (d *Daemon) runGoal(ctx context.Context, id string) error {
	g, ok := d.goals.Get(id)
	if !ok || g.Execution == nil {
		return nil
	}
	d.captureGoalDisputes(g)
	g, _ = d.goals.Get(id)
	if err := d.goalDisputeHold(g); err != nil {
		if g.State != "blocked" && g.State != "paused" && g.State != "canceled" && g.State != "accepted" {
			_ = d.holdGoal(id, err, true)
			g, _ = d.goals.Get(id)
		}
		if err := d.reconcileGoalAttempts(ctx, g); err != nil {
			return err
		}
		return nil
	}
	now := time.Now().UTC()
	var usageErr error
	g, usageErr = d.enforceGoalUsage(id, now)
	if usageErr != nil {
		return usageErr
	}
	if g.State == "ready" {
		if g.Delivered() {
			return nil // awaiting an optional human decision; never republish merged work
		}
		if g.Execution.Policy.PublishPR && (g.Execution.Delivery == nil || time.Since(g.Execution.Delivery.CheckedAt) >= 30*time.Second) {
			return d.queueGoalPublication(ctx, g)
		}
		return nil
	}
	if g.State == "accepted" {
		return nil
	}
	if g.Execution.Final != nil && g.Execution.Final.Receipt != "" {
		return nil
	} // paused/canceled verified artifacts no longer consume execution time
	if !now.Before(g.Execution.Deadline) && g.State != "canceled" && g.State != "failed" {
		g.State = "blocked"
		g.Execution.Reason = "goal runtime budget reached"
		g.Execution.StopWorkersAt = now
		var err error
		g, err = d.goals.Save(g)
		if err != nil {
			return err
		}
	}
	if err := d.reconcileGoalAttempts(ctx, g); err != nil {
		return err
	}
	g, _ = d.goals.Get(id)
	if !slices.Contains([]string{"preparing", "running", "verifying"}, g.State) {
		return nil
	}
	if err := d.validateGoalPolicy(g, g.Execution.Policy); err != nil {
		return d.holdGoal(id, err, true)
	}
	if err := d.goalBindingsCurrent(ctx, g); err != nil {
		return d.holdGoal(id, err, false)
	}
	if g.Execution.PendingReplan != "" {
		return d.queueGoalReplan(ctx, g)
	}
	if r, ok := g.ActiveRefresh(); ok {
		if !g.Execution.StopWorkersAt.IsZero() {
			return nil
		}
		return d.queueGoalVerification(ctx, g, r.Round.ID)
	}
	if g.State == "preparing" {
		m := &goalwork.Manager{Root: filepath.Join(d.home, "goals")}
		manifest := map[string]string{}
		for _, member := range g.Execution.Policy.Members {
			r, err := m.Initialize(ctx, g.ID, member.ID, member.Source, member.Target)
			if err != nil {
				return d.holdGoal(id, err, false)
			}
			manifest[member.ID] = r.Start
		}
		current, _ := d.goals.Get(id)
		if current.Revision != g.Revision {
			return nil
		}
		if err := current.Prepared(manifest); err != nil {
			return err
		}
		var err error
		g, err = d.goals.Save(current)
		if err != nil {
			return err
		}
	}
	if g.State == "verifying" {
		if g.Execution.Final == nil {
			if err := g.ReserveFinal(goal.NewID(), time.Now().UTC()); err != nil {
				return err
			}
			var err error
			g, err = d.goals.Save(g)
			if err != nil {
				return err
			}
		}
		return d.queueGoalVerification(ctx, g, g.Execution.Final.ID)
	}
	if g.State != "running" {
		return nil
	}
	if g.Execution.PendingIntegration != "" {
		return d.queueGoalVerification(ctx, g, g.Execution.PendingIntegration)
	} // verification controller owns this barrier
	// A submitted attempt awaits independent verification. Never interpret a
	// worker's report or a Linear workflow state as integration success.
	for _, a := range g.Execution.Attempts {
		if a.State == "submitted" {
			i, err := g.ReserveIntegration(goal.NewID(), a.ID, time.Now().UTC())
			if err != nil {
				return err
			}
			g, err = d.goals.Save(g)
			if err != nil {
				return err
			}
			return d.queueGoalVerification(ctx, g, i.ID)
		}
	}
	if task := g.ReplanTask(); task != "" {
		if err := g.ReserveReplan(task, goal.NewID(), now); err != nil {
			return err
		}
		var err error
		g, err = d.goals.Save(g)
		if err != nil {
			return err
		}
		return d.queueGoalReplan(ctx, g)
	}
	if len(g.Execution.Runs) >= g.Execution.Policy.Limits.AgentRuns {
		active := false
		for _, a := range g.Execution.Attempts {
			active = active || slices.Contains([]string{"reserved", "running", "submitted", "verifying"}, a.State)
		}
		if !active {
			return d.holdGoal(id, errors.New("goal agent-run budget reached; remaining tasks cannot be dispatched"), false)
		}
	}
	d.mu.Lock()
	nat := d.native
	cfg := d.cfg
	p := cfg.ProjectByName(g.Project)
	globalCap := cfg.Defaults.GlobalCap
	agentBin := agent.Parse(cfg.AgentForProject(g.Project)).Binary()
	d.mu.Unlock()
	if p == nil || nat == nil {
		return d.holdGoal(id, errors.New("goal runtime unavailable"), true)
	}
	if d.runtimeHealth != nil {
		if err := d.runtimeHealth(agentBin); err != nil {
			return d.holdGoal(id, err, false)
		}
	}
	if reason := d.dispatchHold(ctx, g.Project, now); reason != "" {
		return d.holdGoal(id, errors.New(reason), false)
	}
	// Recover an existing reserved launch even when its placeholder fills the cap.
	for _, a := range g.Execution.Attempts {
		if a.State == "reserved" {
			return d.launchGoalAttempt(ctx, g, a.ID)
		}
	}
	if globalCap > 0 && NativeLiveCounted(d.sessions.Snapshot()) >= globalCap {
		return nil
	}
	for _, task := range g.Tasks {
		attemptID := goal.NewID()
		sid := runtime.GoalSessionID(g.ID, attemptID)
		a, err := g.ReserveAttempt(task.ID, attemptID, sid, now)
		if err != nil {
			continue
		}
		g, err = d.goals.Save(g)
		if err != nil {
			return err
		}
		return d.launchGoalAttempt(ctx, g, a.ID)
	}
	return nil
}

func (d *Daemon) holdGoal(id string, cause error, stop bool) error {
	g, ok := d.goals.Get(id)
	if !ok || g.Execution == nil {
		return cause
	}
	if g.State == "canceled" || g.State == "accepted" {
		return cause
	}
	if g.State != "paused" {
		g.State = "blocked"
	}
	if err := captureGoalBrief(&g, cause); err != nil {
		cause = errors.Join(cause, err)
	}
	g.Execution.Reason = cause.Error()
	g.SyncError = cause.Error()
	if stop {
		g.Execution.StopWorkersAt = time.Now().UTC()
	}
	_, err := d.goals.Save(g)
	return errors.Join(cause, err)
}

func (d *Daemon) launchGoalAttempt(ctx context.Context, g goal.Goal, id string) error {
	current, _ := d.goals.Get(g.ID)
	if current.Revision != g.Revision || current.State != "running" {
		return nil
	}
	a := g.Execution.Attempts[id]
	// Count a reserved launch before slow repository preparation. Recovery can
	// reconstruct it from the attempt even if the session snapshot was lost.
	placeholder := session.Session{ID: a.SessionID, Source: "native", Kind: session.KindGoal, GoalID: g.ID, TaskID: a.TaskID, AttemptID: a.ID, Project: g.Project, TmuxName: a.SessionID, Sandbox: "docker", FirstSeen: a.ReservedAt}
	placeholder.SetAgentState(state.AgentStarting, "", time.Now())
	d.sessions.Upsert(placeholder)
	if err := d.sessions.Save(); err != nil {
		return err
	}
	d.mu.Lock()
	nat := d.native
	spawn := d.goalSpawn
	d.mu.Unlock()
	if spawn == nil {
		if goalRuntime, ok := nat.(interface {
			SpawnGoal(context.Context, goal.Goal, string) (session.Session, error)
		}); ok {
			spawn = goalRuntime.SpawnGoal
		}
	}
	if spawn == nil {
		return d.holdGoal(g.ID, errors.New("runtime does not support isolated goal workers"), false)
	}
	s, launchErr := spawn(ctx, g, id)
	if s.ID != "" {
		d.sessions.Upsert(s)
	} else {
		d.sessions.Update(a.SessionID, func(s *session.Session) bool { s.SetAgentState(state.AgentDead, "", time.Now()); return true })
	}
	if err := d.sessions.Save(); err != nil {
		return errors.Join(launchErr, err)
	}
	current, _ = d.goals.Get(g.ID)
	a = current.Execution.Attempts[id]
	if s.ID != "" {
		a.State = "running"
	} else {
		a.State = "failed"
		a.FinishedAt = time.Now().UTC()
		progress := current.Execution.Tasks[a.TaskID]
		progress.State = "pending"
		current.Execution.Tasks[a.TaskID] = progress
	}
	if launchErr != nil {
		a.Error = launchErr.Error()
	}
	current.Execution.Attempts[id] = a
	_, err := d.goals.Save(current)
	return errors.Join(launchErr, err)
}

func (d *Daemon) reconcileGoalAttempts(ctx context.Context, g goal.Goal) error {
	if len(g.Execution.Attempts) == 0 {
		return nil
	}
	// Unlike the display observer, execution never turns an ambiguous tmux
	// query into "dead" and launches a duplicate attempt.
	if d.listTmuxSessions == nil {
		return errors.New("authoritative goal liveness probe unavailable")
	}
	cctx, cancel := context.WithTimeout(ctx, observeExecTimeout)
	live, err := d.listTmuxSessions(cctx)
	cancel()
	if err != nil {
		return err
	}
	byID := map[string]bool{}
	for _, s := range live {
		byID[s.Name] = true
	}
	d.mu.Lock()
	nat := d.native
	d.mu.Unlock()
	if nat == nil {
		return errors.New("goal runtime unavailable")
	}
	m := &goalwork.Manager{Root: filepath.Join(d.home, "goals")}
	for id, a := range g.Execution.Attempts {
		if !slices.Contains([]string{"reserved", "running"}, a.State) {
			continue
		}
		stop := !g.Execution.StopWorkersAt.IsZero() || g.State == "canceled" || !time.Now().Before(a.Deadline)
		if a.State == "reserved" && !stop {
			continue
		}
		s, ok := d.sessions.Get(a.SessionID)
		if !ok {
			s = session.Session{ID: a.SessionID, Source: "native", Kind: session.KindGoal, GoalID: g.ID, TaskID: a.TaskID, AttemptID: a.ID, Project: g.Project, TmuxName: a.SessionID, Sandbox: "docker"}
			dir, _ := m.LaunchDirectory(g.ID, a.ID)
			s.Worktree = filepath.Join(dir, "workspace")
		}
		var submission goalwork.Submission
		hasSubmission := false
		if !stop {
			submission, err = m.ReadSubmission(g.ID, a.TaskID, a.ID, a.Members)
			if err == nil {
				hasSubmission = true
			} else if !errors.Is(err, os.ErrNotExist) {
				return d.holdGoal(g.ID, fmt.Errorf("worker submission: %w", err), false)
			}
		}
		if !stop && !hasSubmission && byID[a.SessionID] {
			continue
		}
		// Container death, submission and explicit stops all preserve its files.
		if err := nat.Kill(ctx, s, false, false); err != nil {
			return err
		}
		current, _ := d.goals.Get(g.ID)
		a = current.Execution.Attempts[id]
		if hasSubmission {
			a.State = "submitted"
			a.Heads = submission.Heads
			a.Summary = submission.Summary
		} else {
			a.State = "stopped"
			a.Error = "worker stopped before submitting verified output"
		}
		a.FinishedAt = time.Now().UTC()
		run := current.Execution.Runs[id]
		run.FinishedAt = a.FinishedAt
		current.Execution.Runs[id] = run
		current.Execution.Attempts[id] = a
		if !hasSubmission {
			progress := current.Execution.Tasks[a.TaskID]
			progress.State = "blocked"
			current.Execution.Tasks[a.TaskID] = progress
			if current.State != "canceled" {
				current.State = "blocked"
				if current.Execution.Reason == "" {
					current.Execution.Reason = a.Error
				}
			}
		}
		if _, err := d.goals.Save(current); err != nil {
			return err
		}
		d.sessions.Upsert(s)
		d.sessions.Update(s.ID, func(cur *session.Session) bool { cur.SetAgentState(state.AgentExited, "", time.Now()); return true })
		if err := d.sessions.Save(); err != nil {
			return err
		}
	}
	return nil
}
