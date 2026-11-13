package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sushidev-team/lola/internal/goalreview"
	"github.com/sushidev-team/lola/internal/goalwork"
	"github.com/sushidev-team/lola/internal/reviewprofile"
)

func isolateGoalReviewFixture(t *testing.T, d *Daemon) *int {
	t.Helper()
	d.profileRun = nil
	d.cfg.Sandbox.Image = "local/reviewer:1"
	calls := new(int)
	d.goalReviewRun = func(_ context.Context, _ string, args []string, _ []byte, _ []string) ([]byte, error) {
		switch args[0] {
		case "image":
			return []byte("sha256:" + strings.Repeat("a", 64)), nil
		case "rm":
			return nil, nil
		case "run":
			*calls++
			b, _ := json.Marshal(reviewprofile.Result{Complete: true, Evidence: []reviewprofile.Evidence{{Criterion: "accepted behavior", Observation: "Candidate satisfies the accepted goal contract"}}})
			return fixtureAgentEvents(args, b), nil
		default:
			t.Fatalf("unexpected review command %v", args)
			return nil, nil
		}
	}
	return calls
}

func TestGoalDefaultReviewContainerGatesIntegrationAndPublication(t *testing.T) {
	d, g, m, _ := submittedGoalFixture(t, nil)
	calls := isolateGoalReviewFixture(t, d)
	ctx := context.Background()
	original := d.goalReviewRun
	d.goalReviewRun = func(ctx context.Context, bin string, args []string, input []byte, env []string) ([]byte, error) {
		if args[0] == "run" {
			current, _ := d.goals.Get(g.ID)
			reserved := false
			for _, run := range current.Execution.Runs {
				if run.Kind == "reviewer" {
					reserved = true
				}
			}
			if !reserved {
				t.Fatal("paid specialist call launched before durable reservation")
			}
		}
		return original(ctx, bin, args, input, env)
	}
	if err := d.verifyGoalIntegration(ctx, g.ID, g.Execution.PendingIntegration); err != nil {
		t.Fatal(err)
	}
	if *calls != 1 {
		t.Fatal("missing default container review")
	}
	if err := d.runGoal(ctx, g.ID); err != nil {
		t.Fatal(err)
	}
	g = submitRunningGoalTask(t, d, g.ID)
	if err := d.verifyGoalIntegration(ctx, g.ID, g.Execution.PendingIntegration); err != nil {
		t.Fatal(err)
	}
	if err := d.runGoal(ctx, g.ID); err != nil {
		t.Fatal(err)
	}
	d.connWg.Wait()
	g, _ = d.goals.Get(g.ID)
	if err := d.verifyGoalFinal(ctx, g.ID, g.Execution.Final.ID); err != nil {
		t.Fatal(err)
	}
	g, _ = d.goals.Get(g.ID)
	if _, err := d.readyGoalEvidence(ctx, g, m); err != nil {
		t.Fatal(err)
	}
	if *calls != 3 {
		t.Fatalf("expected task, dependent task and independent final reviews, got %d", *calls)
	}
	dir, err := m.ReviewDirectory(g.ID, verificationID(g.Execution.Final.ID, "backend", "p1"))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "review.json")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var receipt goalreview.Receipt
	if err := json.Unmarshal(b, &receipt); err != nil {
		t.Fatal(err)
	}
	receipt.Stopped = false
	b, err = json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := d.readyGoalEvidence(ctx, g, m); err == nil {
		t.Fatal("publication accepted an uncertain reviewer process")
	}
	if *calls != 3 {
		t.Fatal("publication launched a reviewer")
	}
}

func TestGoalContainerReviewFailureRetainsGoalBase(t *testing.T) {
	for _, mode := range []string{"launch", "cleanup", "missing-image"} {
		t.Run(mode, func(t *testing.T) {
			d, g, m, _ := submittedGoalFixture(t, nil)
			isolateGoalReviewFixture(t, d)
			original := d.goalReviewRun
			if mode == "missing-image" {
				d.cfg.Sandbox.Image = ""
			}
			d.goalReviewRun = func(ctx context.Context, bin string, args []string, input []byte, env []string) ([]byte, error) {
				if mode == "launch" && args[0] == "run" || mode == "cleanup" && args[0] == "rm" {
					return nil, errors.New("unavailable")
				}
				if mode == "cleanup" && args[0] == "ps" {
					return []byte("still-running"), nil
				}
				return original(ctx, bin, args, input, env)
			}
			if err := d.verifyGoalIntegration(context.Background(), g.ID, g.Execution.PendingIntegration); err == nil {
				t.Fatal("unisolated result accepted")
			}
			if tip, err := m.Tip(context.Background(), g.ID, "p1"); err != nil || tip != g.Execution.Manifest["p1"] {
				t.Fatal("failed specialist advanced canonical goal revision")
			}
			if mode == "cleanup" {
				g, _ = d.goals.Get(g.ID)
				if err := d.recoverGoalReviews(context.Background(), g, m); err == nil {
					t.Fatal("uncertain container allowed fresh verification")
				}
				d.goalReviewRun = original
				current, _ := d.goals.Get(g.ID)
				if err := d.recoverGoalReviews(context.Background(), current, m); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestGoalHostReviewEvidenceCannotAuthorizePublication(t *testing.T) {
	d, g := readyPublicationFixture(t)
	isolateGoalReviewFixture(t, d)
	m := &goalwork.Manager{Root: filepath.Join(d.home, "goals")}
	if _, err := d.readyGoalEvidence(context.Background(), g, m); err == nil {
		t.Fatal("legacy host review treated as isolated evidence")
	}
}
