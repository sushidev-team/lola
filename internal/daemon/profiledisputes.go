package daemon

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"syscall"
	"time"

	"github.com/sushidev-team/lola/internal/goal"
	"github.com/sushidev-team/lola/internal/reviewprofile"
	"github.com/sushidev-team/lola/internal/session"
)

// Worker reports use the existing session file boundary: no daemon socket or
// caller-supplied identity is exposed to isolated goal workers.
func readWorkerDisputes(s session.Session) ([]reviewprofile.WorkerDispute, error) {
	root, err := os.OpenRoot(s.Worktree)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	path := ".lola/review-disputes.json"
	names := []string{".lola", path}
	if s.GoalManaged() {
		path = "review-disputes.json"
		names = []string{path}
	}
	for _, name := range names {
		info, err := root.Lstat(name)
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		if info.Mode()&os.ModeSymlink != 0 || (name == path && !info.Mode().IsRegular()) || info.Size() > 65536 {
			return nil, errors.New("review disputes must be regular files inside the workspace, at most 64 KiB")
		}
	}
	f, err := root.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return nil, errors.New("review disputes must be a regular file")
	}
	body, err := io.ReadAll(io.LimitReader(f, 65537))
	if err != nil {
		return nil, err
	}
	if len(body) > 65536 {
		return nil, errors.New("review dispute file exceeds 64 KiB")
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	var reports []reviewprofile.WorkerDispute
	if err = decoder.Decode(&reports); err != nil {
		return nil, err
	}
	if decoder.Decode(new(any)) != io.EOF {
		return nil, errors.New("review dispute file has trailing data")
	}
	if len(reports) > 20 {
		return nil, errors.New("at most 20 review disputes per turn")
	}
	return reports, nil
}

func (d *Daemon) captureProfileDisputesAsync(id string) {
	if d.profileReviews == nil || !d.disputeCaptures.allow(id, time.Now()) || !d.beginConnWork() {
		return
	}
	go func() {
		defer d.connWg.Done()
		if err := d.captureProfileDisputes(id); err != nil {
			d.logf("", "worker review disputes for %s: %v", id, err)
		}
	}()
}

func (d *Daemon) captureProfileDisputes(id string) error {
	if d.profileReviews == nil {
		return nil
	}
	s, ok := d.sessions.Get(id)
	if !ok || s.Worktree == "" || s.Source != "native" || s.IsAgentless() {
		return nil
	}
	if s.Stack && s.Lane == "" {
		var failures []error
		for _, lane := range s.Lanes() {
			if err := d.captureProfileDisputes(lane.ID); err != nil {
				failures = append(failures, err)
			}
		}
		return errors.Join(failures...)
	}
	reports, err := readWorkerDisputes(s)
	if err != nil {
		return err
	}
	var failures []error
	for _, report := range reports {
		if err := d.recordWorkerDispute(s, report); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

func disputeActor(s session.Session, g goal.Goal) (reviewprofile.DisputeActor, error) {
	actor := reviewprofile.DisputeActor{ReviewOwner: reviewprofile.ReviewOwner{Project: s.Project, Session: s.ID}}
	if !s.GoalManaged() {
		return actor, nil
	}
	if g.Execution == nil || g.Project != s.Project || g.ID != s.GoalID || g.State == "canceled" || g.State == "accepted" {
		return actor, errors.New("goal no longer authorizes worker reports")
	}
	attempt, ok := g.Execution.Attempts[s.AttemptID]
	if !ok || attempt.SessionID != s.ID || attempt.TaskID != s.TaskID || !slices.Contains([]string{"reserved", "running", "submitted", "verifying"}, attempt.State) {
		return actor, errors.New("worker is not a current goal attempt")
	}
	actor.Goal, actor.Task = g.ID, s.TaskID
	task := s.TaskID
	seen := map[string]bool{}
	for task != "" && !seen[task] && len(actor.AllowedTasks) < 1000 {
		found := false
		for _, t := range g.Tasks {
			if t.ID == task {
				seen[task] = true
				actor.AllowedTasks = append(actor.AllowedTasks, task)
				task = t.DecomposedFrom
				found = true
				break
			}
		}
		if !found {
			return actor, errors.New("goal task ancestry is unavailable")
		}
	}
	if task != "" {
		return actor, errors.New("goal task ancestry is cyclic or exceeds the report limit")
	}
	return actor, nil
}

func (d *Daemon) recordWorkerDispute(s session.Session, report reviewprofile.WorkerDispute) error {
	// Serialize a goal dispute and its hold with integration and human steering.
	// A recorded disagreement cannot race through the final state check unnoticed.
	if s.GoalManaged() {
		d.goalMu.Lock()
		defer d.goalMu.Unlock()
	}
	var g goal.Goal
	if s.GoalManaged() {
		if d.goals == nil {
			return errors.New("goal store unavailable")
		}
		g, _ = d.goals.Get(s.GoalID)
	}
	actor, err := disputeActor(s, g)
	if err != nil {
		return err
	}
	f, err := d.profileReviews.DisputeWorker(actor, report)
	if err != nil {
		return err
	}
	if s.GoalManaged() && reviewprofile.BlockingDispute(f) {
		return d.holdGoalDispute(g, f.ID)
	}
	return nil
}

func (d *Daemon) goalDisputeHold(g goal.Goal) error {
	if d.profileReviews == nil {
		return nil
	}
	for _, f := range d.profileReviews.Findings() {
		if f.Inputs.Owner != nil && f.Inputs.Owner.Project == g.Project && f.Inputs.Owner.Goal == g.ID && reviewprofile.BlockingDispute(f) {
			return fmt.Errorf("blocking specialist dispute %s requires human resolution", f.ID)
		}
	}
	return nil
}

func (d *Daemon) captureGoalDisputes(g goal.Goal) {
	for _, a := range g.Execution.Attempts {
		if !slices.Contains([]string{"reserved", "running", "submitted", "verifying"}, a.State) {
			continue
		}
		if err := d.captureProfileDisputes(a.SessionID); err != nil {
			d.logf(g.Project, "goal worker dispute rejected: %v", err)
		}
	}
}

func (d *Daemon) sessionDisputeHold(s session.Session) bool {
	if d.profileReviews == nil {
		return false
	}
	for _, f := range d.profileReviews.Findings() {
		if f.Inputs.Owner != nil && f.Inputs.Owner.Goal == "" && f.Inputs.Owner.Project == s.Project && f.Inputs.Owner.Session == s.ID && reviewprofile.BlockingDispute(f) {
			return true
		}
	}
	return false
}

// Caller holds goalMu, the same serialization boundary used by publication and
// integration. Replays leave an existing durable hold unchanged.
func (d *Daemon) holdGoalDispute(g goal.Goal, finding string) error {
	if g.Execution == nil || g.State == "accepted" || g.State == "canceled" {
		return nil
	}
	reason := fmt.Sprintf("blocking specialist dispute %s requires human resolution", finding)
	d.cancelGoalVerification(g.ID)
	if g.Execution.Reason == reason && !g.Execution.StopWorkersAt.IsZero() {
		return nil
	}
	if g.State != "paused" {
		g.State = "blocked"
	}
	g.Execution.Reason, g.SyncError = reason, reason
	g.Execution.StopWorkersAt = time.Now().UTC()
	_, err := d.goals.Save(g)
	return err
}
