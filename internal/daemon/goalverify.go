package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/sushidev-team/lola/internal/goal"
	"github.com/sushidev-team/lola/internal/goalcheck"
	"github.com/sushidev-team/lola/internal/goalpublish"
	"github.com/sushidev-team/lola/internal/goalwork"
	"github.com/sushidev-team/lola/internal/knowledge"
	"github.com/sushidev-team/lola/internal/reviewprofile"
	"github.com/sushidev-team/lola/internal/session"
)

func verificationID(parts ...string) string {
	h := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(h[:])
}

func (d *Daemon) queueGoalVerification(ctx context.Context, g goal.Goal, operation string) error {
	d.goalJobsMu.Lock()
	defer d.goalJobsMu.Unlock()
	if d.goalJobs == nil {
		d.goalJobs = map[string]context.CancelFunc{}
	}
	if d.goalJobs[g.ID] != nil {
		return nil
	}
	if !d.beginConnWork() {
		return errors.New("daemon is shutting down")
	}
	jobCtx, cancel := context.WithDeadline(ctx, g.Execution.Deadline)
	d.goalJobs[g.ID] = cancel
	go func() {
		defer d.connWg.Done()
		defer cancel()
		defer func() { d.goalJobsMu.Lock(); delete(d.goalJobs, g.ID); d.goalJobsMu.Unlock() }()
		defer func() {
			if v := recover(); v != nil {
				_ = d.holdGoal(g.ID, fmt.Errorf("verification interrupted: %v", v), false)
			}
		}()
		run := d.goalVerify
		if run == nil {
			run = d.verifyGoalOperation
		}
		if err := run(jobCtx, g.ID, operation); err != nil {
			if errors.Is(jobCtx.Err(), context.Canceled) {
				return
			} // preserve newer steering and shutdown recovery intent
			if errors.Is(err, goalpublish.ErrTargetChanged) {
				if retargetErr := d.retargetGoalRefresh(jobCtx, g.ID, operation); retargetErr == nil {
					return
				} else {
					err = errors.Join(err, retargetErr)
				}
			}
			var repair *goalRepairError
			if errors.As(err, &repair) {
				if repairErr := d.repairGoal(jobCtx, g.ID, operation, repair.Feedback); repairErr == nil {
					return
				} else {
					err = errors.Join(err, repairErr)
				}
			}
			current, _ := d.goals.Get(g.ID)
			if current.State == "running" || current.State == "verifying" {
				_ = d.holdGoal(g.ID, err, false)
			}
			d.logf("", "goal %s verification: %v", g.ID, err)
		}
	}()
	return nil
}

func (d *Daemon) cancelGoalVerification(id string) {
	d.goalJobsMu.Lock()
	cancel := d.goalJobs[id]
	d.goalJobsMu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (d *Daemon) verifyGoalIntegration(ctx context.Context, id, operation string) error {
	g, ok := d.goals.Get(id)
	if !ok || g.Execution == nil || g.State != "running" || g.Execution.PendingIntegration != operation {
		return errors.New("goal no longer authorizes this integration")
	}
	i := g.Execution.Integrations[operation]
	a := g.Execution.Attempts[i.AttemptID]
	if i.State != "reserved" || a.State != "verifying" || len(a.Heads) == 0 {
		return errors.New("integration has no submitted repository heads")
	}
	scopes, err := g.AttemptScopes(a)
	if err != nil {
		return err
	}
	m := &goalwork.Manager{Root: filepath.Join(d.home, "goals")}
	manifest := map[string]goalwork.Revision{}
	heads := maps.Clone(i.Base)
	for member, sha := range i.Base {
		manifest[member] = goalwork.Revision{Commit: sha}
	}
	var candidates []goalwork.Candidate
	for _, member := range slices.Sorted(maps.Keys(a.Heads)) {
		scope, assigned := scopes[member]
		if !assigned || scope.ReadOnly() {
			return &goalRepairError{Feedback: fmt.Sprintf("Submission includes unassigned or read-only repository %s. Change only the recorded task write paths; omit unchanged context repositories from submission.json.", member)}
		}
		w, err := m.PrepareWorker(ctx, g.ID, member, a.TaskID, a.ID, a.Base[member])
		if err != nil {
			return err
		}
		c, err := m.Stage(ctx, w, operation, i.Base[member], a.Heads[member], scope.Paths)
		if err != nil {
			if errors.Is(err, goalwork.ErrScope) {
				return &goalRepairError{Feedback: "Submission changes files outside this task's accepted ownership. Reimplement within the recorded allowed paths; do not expand scope. " + err.Error()}
			}
			if errors.Is(err, goalwork.ErrConflict) {
				return &goalRepairError{Feedback: "Submission conflicts with the current goal revision. Reimplement this task against its newly assigned starting revision, preserving the integrated work. " + err.Error()}
			}
			return err
		}
		if err := m.VerifyCandidate(ctx, c); err != nil {
			return err
		}
		candidates = append(candidates, c)
		heads[member] = c.Commit
		manifest[member] = goalwork.Revision{Commit: c.Commit, Operation: operation}
	}
	var task goal.Task
	for _, t := range g.Tasks {
		if t.ID == a.TaskID {
			task = t
		}
	}
	scope := goalReviewScope{Operation: operation, Subject: task.ID, Contract: g.ParentDescription() + "\n" + g.TaskContract(task), Session: a.SessionID, At: i.CreatedAt}
	v, checkIDs, err := d.verifyGoalCandidates(ctx, g, scope, candidates, manifest, heads, m)
	if err != nil {
		return err
	}
	// Serialize the final state check and ref updates with human steering. Slow
	// tests/reviews stay outside this lock and are cancellable by pause/cancel.
	d.goalMu.Lock()
	defer d.goalMu.Unlock()
	current, _ := d.goals.Get(g.ID)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if current.State != "running" || current.Execution == nil || current.Execution.PendingIntegration != operation {
		return errors.New("goal paused or changed before integration")
	}
	if err := d.goalDisputeHold(current); err != nil {
		return err
	}
	receipt, err := m.AdvanceManifest(ctx, g.ID, operation, i.Base, candidates, v, g.Execution.PolicyDigest, checkIDs, g.Execution.Policy.Profiles)
	if err != nil {
		return err
	}
	if !receipt.Complete {
		return errors.New("stack integration is not complete")
	}
	path := filepath.Join(m.Root, g.ID, "integrations", operation+".json")
	if err := current.CompleteIntegration(operation, path, heads, time.Now().UTC()); err != nil {
		return err
	}
	_, err = d.goals.Save(current)
	return err
}

type goalReviewScope struct {
	Operation, Subject, Contract, Session string
	At                                    time.Time
	NoRepair                              bool // final/refresh failures are not a task's repair attempt
}

func (d *Daemon) verifyGoalCandidates(ctx context.Context, g goal.Goal, scope goalReviewScope, candidates []goalwork.Candidate, manifest map[string]goalwork.Revision, heads map[string]string, m *goalwork.Manager) (v goalwork.Verification, checkIDs []string, err error) {
	d.mu.Lock()
	docker := d.cfg.Sandbox.DockerBin()
	d.mu.Unlock()
	checks := &goalcheck.Runner{Workspaces: m, Docker: docker, Run: d.goalCheckRun}
	refs := map[string]string{}
	var artifacts []reviewprofile.Artifact
	for _, spec := range g.Execution.Policy.Checks {
		if err := ctx.Err(); err != nil {
			return v, checkIDs, err
		}
		checkID := verificationID(scope.Operation, "check", spec.ID)
		if _, err := checks.Recover(ctx, g.ID, checkID); err != nil && !errors.Is(err, os.ErrNotExist) {
			return v, checkIDs, err
		}
		refs[spec.ID] = checkID
		checkIDs = append(checkIDs, spec.ID)
		r, err := checks.Execute(ctx, g.ID, scope.Operation, checkID, g.Execution.PolicyDigest, spec, manifest)
		if err != nil {
			if r.State == "fail" && scope.Subject != "final" && !scope.NoRepair {
				return v, checkIDs, &goalRepairError{Feedback: fmt.Sprintf("Independent acceptance check %s rejected the candidate. Output:\n%s\n%s", spec.ID, r.Output, r.Error)}
			}
			return v, checkIDs, fmt.Errorf("acceptance check %s: %w", spec.ID, err)
		}
		for _, artifact := range r.Artifacts {
			artifacts = append(artifacts, reviewprofile.Artifact{ID: spec.ID + "/" + artifact.ID, Kind: artifact.Kind, Path: artifact.Path, SHA256: artifact.SHA256})
		}
	}
	checkEvidence, err := checks.Evidence(g.ID, scope.Operation, g.Execution.PolicyDigest, manifest, g.Execution.Policy.Checks, refs)
	if err != nil {
		return v, checkIDs, err
	}
	var reviews []goalwork.Check
	records := map[string]reviewprofile.Record{}
	for _, profileID := range g.Execution.Policy.Profiles {
		var keys []string
		for _, c := range candidates {
			if err := m.VerifyCandidate(ctx, c); err != nil {
				return v, checkIDs, err
			}
			// Profile selection is explicit; reviewGoalProfile keeps same-provider
			// specialties independently keyed and budgeted.
			r, err := d.reviewGoalProfile(ctx, g, scope, c, profileID, heads, artifacts, m)
			if err != nil {
				return v, checkIDs, err
			}
			if r.State != "pass" && r.State != "not_applicable" {
				if r.State == "findings" && scope.Subject != "final" && !scope.NoRepair {
					return v, checkIDs, &goalRepairError{Feedback: d.specialistFeedback(r, true)}
				}
				return v, checkIDs, fmt.Errorf("specialist %s: %s: %s", profileID, r.State, r.Error)
			}
			keys = append(keys, r.Key)
			records[r.Key] = r
		}
		reviews = append(reviews, goalwork.Check{ID: profileID, State: "pass", Artifact: strings.Join(keys, ",")})
	}
	if err := d.goalBindingsCurrent(ctx, g); err != nil {
		return v, checkIDs, err
	}
	if err := d.validateGoalPolicy(g, g.Execution.Policy); err != nil {
		return v, checkIDs, err
	}
	if _, err := checks.Evidence(g.ID, scope.Operation, g.Execution.PolicyDigest, manifest, g.Execution.Policy.Checks, refs); err != nil {
		return v, checkIDs, err
	}
	for _, r := range records {
		p, _, err := d.profileFor(r.Profile)
		if err != nil || p.Key(r.Inputs) != r.Key || !d.profileInvocationCurrent(p, r.Inputs, true) {
			return v, checkIDs, errors.New("specialist rubric, model or prompt changed before integration")
		}
		if err := d.goalReviewEvidence(g, r, m); err != nil {
			return v, checkIDs, err
		}
		fresh, ok := d.profileReviews.Get(r.Key)
		if !ok || fresh.State != r.State || fresh.Attempt != r.Attempt {
			return v, checkIDs, errors.New("specialist evidence changed before integration")
		}
	}
	for _, c := range candidates {
		if err := m.VerifyCandidate(ctx, c); err != nil {
			return v, checkIDs, err
		}
	}
	v = goalwork.Verification{ID: verificationID(scope.Operation, "proof"), Goal: g.ID, Operation: scope.Operation, Manifest: heads, Policy: g.Execution.PolicyDigest, Checks: checkEvidence, Reviews: reviews, At: scope.At}
	return v, checkIDs, nil
}

func (d *Daemon) reviewGoalProfile(ctx context.Context, g goal.Goal, scope goalReviewScope, c goalwork.Candidate, id string, heads map[string]string, artifacts []reviewprofile.Artifact, m *goalwork.Manager) (reviewprofile.Record, error) {
	if d.profileReviews == nil {
		return reviewprofile.Record{}, errors.New("specialist review store unavailable")
	}
	p, provider, err := d.profileFor(id)
	if err != nil {
		return reviewprofile.Record{}, err
	}
	combination, _ := json.Marshal(heads)
	contract := scope.Contract + g.Execution.Policy.RolloutContract() + "\nVerified repository combination: " + string(combination)
	repo := c.Member
	for _, member := range g.Execution.Policy.Members {
		if member.ID == c.Member && member.Repo != "" {
			repo = member.Repo
		}
	}
	in := reviewprofile.Inputs{Invocation: profileInvocation(p, provider, true), Owner: &reviewprofile.ReviewOwner{Project: g.Project, Session: scope.Session, Goal: g.ID, Task: scope.Subject}, ContextID: scope.Operation, Repo: repo, Head: c.Commit, Base: c.Base, Contract: contract, Paths: c.Paths, Artifacts: artifacts}
	if d.knowledge != nil {
		in.Knowledge = d.selectKnowledge(ctx, knowledge.Query{Project: g.Project, GoalID: g.ID, Paths: c.Paths, Text: contract, MaxBytes: 4000}).Markdown()
	}
	runID := verificationID(scope.Operation, id, c.Member)
	if old, ok := d.profileReviews.Get(p.Key(in)); ok {
		if d.profileRun == nil && old.State != "not_applicable" {
			runner, err := d.goalReviewRunner(g, p.ID, m)
			if err != nil {
				return old, err
			}
			if err := runner.Recover(ctx, g.ID, runID); err != nil {
				return old, err
			}
			observed, err := runner.Usage(g.ID, runID)
			if err != nil {
				return old, err
			}
			if observed != nil {
				if err := d.observeGoalRunUsage(g.ID, runID, *observed); err != nil {
					return old, err
				}
			}
			if err := runner.Evidence(g.ID, runID, old.Key, old.Result); err != nil {
				return old, err
			}
		}
		return old, nil
	}
	if !p.Applies(g.Project, c.Paths) {
		r, err := d.profileReviews.Begin(p, in, false)
		if err != nil {
			return r, err
		}
		r.State = "not_applicable"
		return r, d.profileReviews.Finish(r)
	}
	if err := p.CheckInputs(in); err != nil {
		return reviewprofile.Record{}, err
	}
	subject := scope.Subject + "/" + id + "/" + c.Member
	current, _ := d.goals.Get(g.ID)
	if current.Execution == nil || (current.State != "running" && current.State != "verifying") {
		return reviewprofile.Record{}, errors.New("goal no longer authorizes reviews")
	}
	if _, ok := current.Execution.Runs[runID]; ok {
		return reviewprofile.Record{}, errors.New("review reservation has no matching result; explicit recovery required")
	} else {
		count := 0
		for _, run := range current.Execution.Runs {
			if run.Kind == "reviewer" && run.Subject == subject {
				count++
			}
		}
		if count >= current.Execution.Policy.Limits.ReviewRounds {
			return reviewprofile.Record{}, errors.New("goal specialist round budget reached")
		}
		if err := current.ReserveRun(runID, "reviewer", subject, time.Now().UTC()); err != nil {
			return reviewprofile.Record{}, err
		}
		if _, err := d.goals.Save(current); err != nil {
			return reviewprofile.Record{}, err
		}
	}
	r, err := d.profileReviews.Begin(p, in, false)
	if err != nil {
		return r, err
	}
	finish := func(status string, cause error) (reviewprofile.Record, error) {
		r.State = status
		if cause != nil {
			r.Error = cause.Error()
		}
		err := d.profileReviews.Finish(r)
		if err != nil {
			return r, err
		}
		return r, cause
	}
	run := d.profileRun
	if run == nil {
		runner, err := d.goalReviewRunner(g, p.ID, m)
		if err != nil {
			return finish("error", err)
		}
		run = func(ctx context.Context, _ string, p reviewprofile.Profile, in reviewprofile.Inputs) (reviewprofile.Result, error) {
			return runner.Review(ctx, runID, c, p, in)
		}
	}
	limit := time.Duration(p.TimeoutSeconds) * time.Second
	if limit <= 0 {
		limit = 5 * time.Minute
	}
	reviewCtx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	r.Result, err = run(reviewCtx, c.Directory, p, in)
	if err != nil {
		return finish("error", err)
	}
	b, _ := json.Marshal(r.Result)
	r.Result, err = reviewprofile.Parse(string(b), in, p)
	if err != nil {
		return finish("incomplete", err)
	}
	if err := m.VerifyCandidate(ctx, c); err != nil {
		return finish("error", err)
	}
	status := "pass"
	for _, f := range r.Result.Findings {
		if f.Severity == "major" || f.Severity == "blocker" {
			status = "findings"
		}
	}
	r, err = finish(status, nil)
	if err == nil {
		d.captureProfileFindings(session.Session{ID: scope.Session, Project: g.Project, GoalID: g.ID}, r)
	}
	return r, err
}
