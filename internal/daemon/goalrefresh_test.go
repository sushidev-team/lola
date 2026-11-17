package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/sushidev-team/lola/internal/goal"
	"github.com/sushidev-team/lola/internal/goalpolicy"
	"github.com/sushidev-team/lola/internal/goalpublish"
	"github.com/sushidev-team/lola/internal/goalwork"
	"github.com/sushidev-team/lola/internal/reviewprofile"
)

type goalRefreshRemote struct {
	t                        *testing.T
	d                        *Daemon
	g                        goal.Goal
	source, target, head     string
	pr                       map[string]any
	comments                 []map[string]any
	commentPosts             int
	fetches, pushes, creates int
	loseFetch                bool
}

func newGoalRefreshRemote(t *testing.T, d *Daemon, g goal.Goal) *goalRefreshRemote {
	t.Helper()
	f := &goalRefreshRemote{t: t, d: d, g: g, source: g.Execution.Policy.Members[0].Source}
	if err := os.WriteFile(filepath.Join(f.source, "upstream.txt"), []byte("upstream behavior\n"), 0600); err != nil {
		t.Fatal(err)
	}
	goalFixtureGit(t, f.source, "add", "upstream.txt")
	goalFixtureGit(t, f.source, "commit", "-m", "upstream advances")
	f.target = goalFixtureGit(t, f.source, "rev-parse", "HEAD")
	d.goalPublishRun = f.run
	return f
}

func (f *goalRefreshRemote) run(ctx context.Context, dir, bin string, args []string, input []byte) ([]byte, error) {
	encode := func(v any) []byte { b, _ := json.Marshal(v); return b }
	branch := "goal/" + f.g.ID
	if bin == "git" {
		if slices.Contains(args, "fetch") {
			current, _ := f.d.goals.Get(f.g.ID)
			r, ok := current.ActiveRefresh()
			if !ok || r.Targets["p1"] != args[len(args)-1] || current.State != "verifying" {
				f.t.Fatal("fetch before durable target reservation")
			}
			if dir == f.source || !strings.Contains(dir, string(filepath.Separator)+"targets"+string(filepath.Separator)) {
				f.t.Fatal("fetch outside independent target store")
			}
			f.fetches++
			goalFixtureGit(f.t, dir, "fetch", "--no-tags", "--no-write-fetch-head", f.source, args[len(args)-1])
			if f.loseFetch {
				f.loseFetch = false
				return nil, errors.New("lost fetch response")
			}
			return nil, nil
		}
		if !slices.Contains(args, "push") || !strings.HasSuffix(args[len(args)-1], ":refs/heads/"+branch) {
			f.t.Fatal("unexpected upstream mutation", args)
		}
		f.head = strings.TrimSuffix(args[len(args)-1], ":refs/heads/"+branch)
		f.pushes++
		if f.pr != nil {
			f.pr["head"].(map[string]any)["sha"] = f.head
		}
		return nil, nil
	}
	if bin != "gh" {
		f.t.Fatal("unexpected binary", bin)
	}
	if slices.Contains(args, "graphql") {
		var head any
		if f.head != "" {
			head = map[string]any{"target": map[string]string{"oid": f.head}}
		}
		return encode(map[string]any{"data": map[string]any{"repository": map[string]any{"nameWithOwner": "acme/widgets", "defaultBranchRef": map[string]string{"name": "main"}, "head": head, "base": map[string]any{"target": map[string]string{"oid": f.target}}}}}), nil
	}
	if slices.Contains(args, "repos/acme/widgets/issues/7/comments") {
		if slices.Contains(args, "GET") {
			return encode(f.comments), nil
		}
		if !slices.Contains(args, "POST") {
			f.t.Fatal("unexpected comment mutation", args)
		}
		var body map[string]any
		if err := json.Unmarshal(input, &body); err != nil {
			f.t.Fatal(err)
		}
		f.commentPosts++
		id := 100 + f.commentPosts
		c := map[string]any{"id": id, "body": body["body"], "html_url": fmt.Sprintf("https://github.com/acme/widgets/pull/7#issuecomment-%d", id)}
		f.comments = append(f.comments, c)
		return encode(c), nil
	}
	if slices.Contains(args, "GET") {
		if f.pr == nil {
			return []byte("[]"), nil
		}
		return encode([]any{f.pr}), nil
	}
	if !slices.Contains(args, "POST") || !slices.Contains(args, "repos/acme/widgets/pulls") {
		f.t.Fatal("unexpected GitHub mutation", args)
	}
	f.creates++
	var body map[string]any
	if err := json.Unmarshal(input, &body); err != nil {
		f.t.Fatal(err)
	}
	f.pr = map[string]any{"number": 7, "html_url": "https://github.com/acme/widgets/pull/7", "state": "open", "draft": true, "body": body["body"], "head": map[string]any{"ref": branch, "sha": f.head, "repo": map[string]string{"full_name": "acme/widgets"}}, "base": map[string]any{"ref": "main", "sha": f.target, "repo": map[string]string{"full_name": "acme/widgets"}}}
	return encode(f.pr), nil
}

func goalTick(t *testing.T, d *Daemon, id string) goal.Goal {
	t.Helper()
	if err := d.runGoal(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	d.connWg.Wait()
	g, _ := d.goals.Get(id)
	return g
}

func TestGoalTargetRefreshRunsThroughDurableControllerAndFreshFinal(t *testing.T) {
	d, g := readyPublicationFixture(t, func(p *goalpolicy.Policy) { p.Limits.TargetRefreshes = 2 })
	f := newGoalRefreshRemote(t, d, g)
	d.goalVerify = nil
	oldFinal := g.Execution.Final.ID
	oldHead := g.Execution.Manifest["p1"]
	runs := len(g.Execution.Runs)
	g = goalTick(t, d, g.ID)
	r, ok := g.ActiveRefresh()
	if !ok || g.State != "verifying" || r.Final.ID != oldFinal || r.Delivery == nil || r.Targets["p1"] != f.target || f.fetches != 0 || f.pushes != 0 {
		t.Fatalf("drift was not durably reserved: %+v", g.Execution)
	}
	// Losing a fetch acknowledgment holds the same captured operation. Resume
	// after reopening the journal imports no second object and spends no call.
	f.loseFetch = true
	g = goalTick(t, d, g.ID)
	if g.State != "blocked" || f.fetches != 1 || g.Execution.Manifest["p1"] != oldHead {
		t.Fatalf("lost fetch changed goal: %s %s", g.State, g.Execution.Reason)
	}
	var err error
	d.goals, err = goal.Open(filepath.Join(d.home, "state", "goals.json"))
	if err != nil {
		t.Fatal(err)
	}
	g, err = d.controlGoal(context.Background(), goal.ControlRequest{ID: g.ID, Revision: g.Revision, Action: "resume"})
	if err != nil {
		t.Fatal(err)
	}
	g = goalTick(t, d, g.ID)
	if g.State != "verifying" || g.Execution.PendingRefresh != "" || g.Execution.Final != nil || g.Execution.Targets["p1"] != f.target || g.Execution.Manifest["p1"] == oldHead || f.fetches != 1 {
		t.Fatalf("refresh did not integrate verified combination: %s %+v", g.State, g.Execution)
	}
	g = goalTick(t, d, g.ID)
	if g.State != "ready" || g.Execution.Final.ID == oldFinal || g.Execution.Final.Targets["p1"] != f.target || len(g.Execution.Runs) != runs+2 {
		t.Fatalf("refresh skipped independent final checks/review: %+v", g.Execution)
	}
	m := &goalwork.Manager{Root: filepath.Join(d.home, "goals")}
	if _, err := d.readyGoalEvidence(context.Background(), g, m); err != nil {
		t.Fatal(err)
	}
	g = goalTick(t, d, g.ID)
	if g.Execution.Delivery == nil || g.Execution.Delivery.State != "published" || f.pushes != 1 || f.creates != 1 {
		t.Fatalf("refreshed goal not publishable: %+v", g.Execution.Delivery)
	}
	if goalFixtureGit(t, f.source, "rev-parse", "main") != f.target {
		t.Fatal("goal refresh changed source main")
	}
	if err := m.IsAncestor(context.Background(), g.ID, "p1", oldHead, g.Execution.Manifest["p1"]); err != nil {
		t.Fatal("refresh dropped goal history", err)
	}
	if err := m.IsAncestor(context.Background(), g.ID, "p1", f.target, g.Execution.Manifest["p1"]); err != nil {
		t.Fatal("refresh dropped upstream history", err)
	}
}

func TestGoalTargetRefreshPauseRetainsCandidateAndReviewBudget(t *testing.T) {
	d, g := readyPublicationFixture(t, func(p *goalpolicy.Policy) { p.Limits.TargetRefreshes = 1 })
	newGoalRefreshRemote(t, d, g)
	d.goalVerify = nil
	g = goalTick(t, d, g.ID)
	oldHead := g.Execution.Manifest["p1"]
	r, _ := g.ActiveRefresh()
	original := d.profileRun
	d.profileRun = func(ctx context.Context, dir string, p reviewprofile.Profile, in reviewprofile.Inputs) (reviewprofile.Result, error) {
		current, _ := d.goals.Get(g.ID)
		if _, err := d.controlGoal(ctx, goal.ControlRequest{ID: g.ID, Revision: current.Revision, Action: "pause"}); err != nil {
			t.Fatal(err)
		}
		return original(ctx, dir, p, in)
	}
	g = goalTick(t, d, g.ID)
	if g.State != "paused" || g.Execution.Manifest["p1"] != oldHead || g.Execution.PendingRefresh != r.ID {
		t.Fatal("pause lost refresh barrier")
	}
	runs := len(g.Execution.Runs)
	d.profileRun = original
	var err error
	g, err = d.controlGoal(context.Background(), goal.ControlRequest{ID: g.ID, Revision: g.Revision, Action: "resume"})
	if err != nil {
		t.Fatal(err)
	}
	g = goalTick(t, d, g.ID)
	if g.State != "blocked" || g.Execution.PendingRefresh == "" || g.Execution.Manifest["p1"] != oldHead || len(g.Execution.Runs) != runs {
		t.Fatalf("resumed refresh reran an interrupted paid review: %s %s", g.State, g.Execution.Reason)
	}
	// A review interrupted before its post-run candidate check is uncertain.
	// Resume must hold it; only explicit recovery reserves another paid pass.
	g, err = d.controlGoal(context.Background(), goal.ControlRequest{ID: g.ID, Revision: g.Revision, Action: "retry-verification"})
	if err != nil {
		t.Fatal(err)
	}
	g = goalTick(t, d, g.ID)
	if g.Execution.PendingRefresh != "" || g.Execution.Manifest["p1"] == oldHead || len(g.Execution.Runs) != runs+1 {
		t.Fatalf("explicit recovery failed or miscounted review: %s %s", g.State, g.Execution.Reason)
	}
}

func TestGoalTargetRefreshFailureHoldsWithoutTaskRepair(t *testing.T) {
	d, g := readyPublicationFixture(t, func(p *goalpolicy.Policy) { p.Limits.TargetRefreshes = 1 })
	newGoalRefreshRemote(t, d, g)
	d.goalVerify = nil
	g = goalTick(t, d, g.ID)
	oldHead := g.Execution.Manifest["p1"]
	oldAttempts := len(g.Execution.Attempts)
	d.profileRun = func(context.Context, string, reviewprofile.Profile, reviewprofile.Inputs) (reviewprofile.Result, error) {
		return reviewprofile.Result{Complete: true, Evidence: []reviewprofile.Evidence{{Criterion: "whole-goal behavior", Observation: "reproduced regression"}}, Findings: []reviewprofile.Finding{{Requirement: "Preserve upstream behavior", Location: "app.txt:1", Reproduction: "run goal with upstream change", Evidence: "upstream contract regressed", Severity: "major", Confidence: "verified", Fix: "resolve regression before delivery"}}}, nil
	}
	g = goalTick(t, d, g.ID)
	if g.State != "blocked" || len(g.Execution.Attempts) != oldAttempts || g.Execution.Manifest["p1"] != oldHead || g.Execution.PendingRefresh == "" {
		t.Fatal("refresh finding was mistaken for task repair")
	}
	d.profileRun = func(context.Context, string, reviewprofile.Profile, reviewprofile.Inputs) (reviewprofile.Result, error) {
		return reviewprofile.Result{Complete: true, Evidence: []reviewprofile.Evidence{{Criterion: "whole-goal behavior", Observation: "independently verified"}}}, nil
	}
	var err error
	g, err = d.controlGoal(context.Background(), goal.ControlRequest{ID: g.ID, Revision: g.Revision, Action: "retry-verification"})
	if err != nil {
		t.Fatal(err)
	}
	if len(g.Execution.Refreshes[0].History) != 1 {
		t.Fatal("retry lost rejected review identity")
	}
	g = goalTick(t, d, g.ID)
	if g.Execution.PendingRefresh != "" || g.Execution.Manifest["p1"] == oldHead {
		t.Fatalf("explicit retry failed: %s %s", g.State, g.Execution.Reason)
	}
}

func TestGoalTargetRefreshCannotExceedAcceptedLimit(t *testing.T) {
	d, g := readyPublicationFixture(t)
	f := newGoalRefreshRemote(t, d, g)
	g = goalTick(t, d, g.ID)
	if g.State != "ready" || g.Execution.PendingRefresh != "" || len(g.Execution.Refreshes) != 0 || f.fetches != 0 || !strings.Contains(g.Execution.Delivery.Error, goalpublish.ErrTargetChanged.Error()) {
		t.Fatal("disabled refresh ran or drift disappeared")
	}
	// Ready publication may outlive execution time; new refresh work may not.
	d, g = readyPublicationFixture(t, func(p *goalpolicy.Policy) { p.Limits.TargetRefreshes = 1 })
	newGoalRefreshRemote(t, d, g)
	g.Execution.Deadline = time.Now().Add(-time.Second)
	g.Execution.StartedAt = g.Execution.Deadline.Add(-time.Hour)
	// Use a direct authority call with the expired timestamp instead of writing
	// a journal whose original attempt deadlines would become inconsistent.
	if err := g.ReserveRefresh("late", map[string]string{"p1": strings.Repeat("a", 40)}, map[string]string{"p1": strings.Repeat("b", 40)}, time.Now()); err == nil {
		t.Fatal("refresh extended original deadline")
	}
}

func TestGoalTargetRefreshRecoversRefReceiptAheadOfGoalSnapshot(t *testing.T) {
	d, g := readyPublicationFixture(t, func(p *goalpolicy.Policy) { p.Limits.TargetRefreshes = 2 })
	f := newGoalRefreshRemote(t, d, g)
	d.goalVerify = nil
	g = goalTick(t, d, g.ID)
	statePath := filepath.Join(d.home, "state", "goals.json")
	var reserved []byte
	original := d.profileRun
	reviews := 0
	d.profileRun = func(ctx context.Context, dir string, p reviewprofile.Profile, in reviewprofile.Inputs) (reviewprofile.Result, error) {
		reviews++
		var err error
		reserved, err = os.ReadFile(statePath)
		if err != nil {
			t.Fatal(err)
		}
		return original(ctx, dir, p, in)
	}
	g = goalTick(t, d, g.ID)
	if len(reserved) == 0 || g.Execution.PendingRefresh != "" {
		t.Fatal("fixture did not reach complete ref receipt")
	}
	wantHead := g.Execution.Manifest["p1"]
	wantTarget := g.Execution.Targets["p1"]
	// Restore only the isolated test's goal snapshot, modeling a lost Save
	// acknowledgment while retaining durable ref/check/review receipts.
	if err := os.WriteFile(statePath, reserved, 0600); err != nil {
		t.Fatal(err)
	}
	var err error
	d.goals, err = goal.Open(statePath)
	if err != nil {
		t.Fatal(err)
	}
	f.target = strings.Repeat("f", 40) // upstream moved after the ref intent
	g = goalTick(t, d, g.ID)
	if g.State != "verifying" || g.Execution.PendingRefresh != "" || g.Execution.Manifest["p1"] != wantHead || g.Execution.Targets["p1"] != wantTarget || reviews != 1 || f.fetches != 1 {
		t.Fatalf("receipt recovery abandoned captured proof: %s %s", g.State, g.Execution.Reason)
	}
}

func TestGoalTargetRefreshPauseCancelsGuardedFetch(t *testing.T) {
	d, g := readyPublicationFixture(t, func(p *goalpolicy.Policy) { p.Limits.TargetRefreshes = 1 })
	f := newGoalRefreshRemote(t, d, g)
	d.goalVerify = nil
	g = goalTick(t, d, g.ID)
	started := make(chan struct{})
	d.goalPublishRun = func(ctx context.Context, dir, bin string, args []string, input []byte) ([]byte, error) {
		if bin == "git" && slices.Contains(args, "fetch") {
			close(started)
			<-ctx.Done()
			return nil, ctx.Err()
		}
		return f.run(ctx, dir, bin, args, input)
	}
	if err := d.runGoal(context.Background(), g.ID); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("fetch did not start")
	}
	var err error
	g, err = d.controlGoal(context.Background(), goal.ControlRequest{ID: g.ID, Revision: g.Revision, Action: "pause"})
	if err != nil {
		t.Fatal(err)
	}
	d.connWg.Wait()
	if g.State != "paused" || g.Execution.PendingRefresh == "" {
		t.Fatal("pause lost durable refresh")
	}
	d.goalPublishRun = f.run
	g, err = d.controlGoal(context.Background(), goal.ControlRequest{ID: g.ID, Revision: g.Revision, Action: "resume"})
	if err != nil {
		t.Fatal(err)
	}
	g = goalTick(t, d, g.ID)
	if g.State != "verifying" || g.Execution.PendingRefresh != "" {
		t.Fatalf("canceled fetch did not resume: %s %s", g.State, g.Execution.Reason)
	}
}

func TestGoalTargetRefreshUpdatesExistingPRAndObservesHumanMerge(t *testing.T) {
	d, g := readyPublicationFixture(t, func(p *goalpolicy.Policy) { p.Limits.TargetRefreshes = 2 })
	m := &goalwork.Manager{Root: filepath.Join(d.home, "goals")}
	repo, err := m.Repository(context.Background(), g.ID, "p1")
	if err != nil {
		t.Fatal(err)
	}
	f := &goalRefreshRemote{t: t, d: d, g: g, source: repo.Source, target: repo.Start}
	d.goalPublishRun = f.run
	g = goalTick(t, d, g.ID)
	if g.Execution.Delivery == nil || g.Execution.Delivery.State != "published" {
		t.Fatal("initial publication failed")
	}
	original := f.pr["body"].(string) + "\n\nHuman release note: keep this."
	f.pr["body"] = original
	if err := os.WriteFile(filepath.Join(f.source, "upstream.txt"), []byte("new upstream\n"), 0600); err != nil {
		t.Fatal(err)
	}
	goalFixtureGit(t, f.source, "add", "upstream.txt")
	goalFixtureGit(t, f.source, "commit", "-m", "new upstream")
	f.target = goalFixtureGit(t, f.source, "rev-parse", "HEAD")
	f.pr["base"].(map[string]any)["sha"] = f.target
	g.Execution.Delivery.CheckedAt = time.Now().Add(-time.Minute)
	g, err = d.goals.Save(g)
	if err != nil {
		t.Fatal(err)
	}
	d.goalVerify = nil
	for range 4 {
		g = goalTick(t, d, g.ID)
	} // drift -> refresh -> final -> publication
	if g.State != "ready" || g.Execution.Delivery == nil || g.Execution.Delivery.State != "published" || g.Execution.Delivery.Members["p1"].Number != 7 || f.pushes != 2 || f.creates != 1 || f.commentPosts != 1 || f.pr["body"] != original {
		t.Fatalf("existing PR did not survive refresh: %+v, pushes=%d creates=%d comments=%d", g.Execution.Delivery, f.pushes, f.creates, f.commentPosts)
	}
	merged := time.Now().UTC().Format(time.RFC3339)
	f.pr["state"], f.pr["merged_at"] = "closed", merged
	r, err := d.publishVerifiedGoal(context.Background(), g.ID)
	if err != nil {
		t.Fatal(err)
	}
	d.recordGoalPublication(g.ID, r, nil)
	g, _ = d.goals.Get(g.ID)
	if g.State != "accepted" || f.pushes != 2 || f.creates != 1 || f.commentPosts != 1 {
		t.Fatal("human merge of refreshed exact head was not accepted")
	}
}
