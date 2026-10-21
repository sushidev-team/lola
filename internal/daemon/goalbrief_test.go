package daemon

import (
	"context"
	"fmt"
	"github.com/sushidev-team/lola/internal/goalwork"
	"github.com/sushidev-team/lola/internal/linear"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/sushidev-team/lola/internal/goal"
)

func TestGoalBriefReconciliation(t *testing.T) {
	d, f, draft := goalFixture(t)
	ctx := context.Background()
	g, err := d.createGoal(ctx, draft)
	if err != nil {
		t.Fatal(err)
	}
	g, err = d.publishGoal(ctx, g.ID)
	if err != nil {
		t.Fatal(err)
	}
	detail := f.Details[0]
	detail.Description += "\nHuman clarification: keyboard interaction."
	f.Details[0] = detail
	g, err = d.syncGoalBrief(ctx, g.ID)
	if err != nil {
		t.Fatal(err)
	}
	if g.PendingBrief == "" || g.SyncError == "" || len(g.BriefSources) != 1 {
		t.Fatal("edit was not recorded as a proposal")
	}
	originalOutcome := g.Outcome
	c := g.BriefContract()
	c.Outcome += " with keyboard access"
	a := goal.BriefAcceptance{ID: g.ID, Revision: g.Revision, SourceID: g.PendingBrief, Contract: c, Reason: "The retained UI task covers keyboard access"}
	detail.Description += "\nAnother concurrent edit"
	f.Details[0] = detail
	if _, err = d.acceptGoalBrief(ctx, a); err == nil {
		t.Fatal("concurrent remote edit accepted")
	}
	stored, _ := d.goals.Get(g.ID)
	if stored.Outcome != originalOutcome || stored.Revision != g.Revision {
		t.Fatal("rejected acceptance changed store")
	}
	g, err = d.syncGoalBrief(ctx, g.ID)
	if err != nil {
		t.Fatal(err)
	}
	a.Revision, a.SourceID = g.Revision, g.PendingBrief
	g, err = d.acceptGoalBrief(ctx, a)
	if err != nil {
		t.Fatal(err)
	}
	if g.Outcome != c.Outcome || len(g.BriefRevisions) != 1 || len(g.BriefSources) != 2 || g.PendingBrief != "" {
		t.Fatal("accepted revision missing")
	}
	if err = d.goalBindingsCurrent(ctx, g); err != nil {
		t.Fatal(err)
	}
	if f.Details[0].Description != detail.Description || len(f.Created) != 3 {
		t.Fatal("reconciliation wrote over Linear or created issues")
	}
	if _, err = d.publishGoal(ctx, g.ID); err != nil {
		t.Fatal("republishing lost accepted human prose", err)
	}
}
func TestGoalBriefAutomaticHold(t *testing.T) {
	d, f, g, policy, launched := goalExecutionFixture(t)
	g, err := d.authorizeGoal(context.Background(), goal.Authorization{ID: g.ID, Revision: g.Revision, Policy: policy})
	if err != nil {
		t.Fatal(err)
	}
	detail := f.Details[0]
	detail.Description += "\nPlease clarify expiry."
	f.Details[0] = detail
	if err = d.runGoal(context.Background(), g.ID); err == nil {
		t.Fatal("changed brief did not hold execution")
	}
	got, _ := d.goals.Get(g.ID)
	if got.State != "blocked" || got.PendingBrief == "" || len(got.BriefSources) != 1 || *launched != 0 {
		t.Fatal("drift launched work or lost proposal")
	}
	if _, err = d.controlGoal(context.Background(), goal.ControlRequest{ID: g.ID, Revision: got.Revision, Action: "resume"}); err == nil {
		t.Fatal("unaccepted brief resumed")
	}
	// Reverting Linear clears the pending proposal only after a complete read;
	// it does not resume work or delete the prior observation.
	detail.Description = g.ParentDescription()
	f.Details[0] = detail
	got, err = d.syncGoalBrief(context.Background(), g.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.PendingBrief != "" || got.SyncError != "" || got.State != "blocked" || len(got.BriefSources) != 1 {
		t.Fatal("revert did not retain hold/history")
	}
}
func TestGoalBriefPlacementCannotBecomeContract(t *testing.T) {
	d, f, draft := goalFixture(t)
	g, _ := d.createGoal(context.Background(), draft)
	g, err := d.publishGoal(context.Background(), g.ID)
	if err != nil {
		t.Fatal(err)
	}
	detail := f.Details[0]
	detail.ProjectID = "other"
	detail.Description += "\nchanged"
	f.Details[0] = detail
	g, err = d.syncGoalBrief(context.Background(), g.ID)
	if err != nil {
		t.Fatal(err)
	}
	if g.PendingBrief != "" || !strings.Contains(g.SyncError, "moved") {
		t.Fatal("placement treated as acceptable prose")
	}
	if _, err := goal.NewBriefSource("title", strings.Repeat("x", (128<<10)+1), time.Now()); err == nil {
		t.Fatal("unbounded snapshot")
	}
}

func TestGoalBriefAfterFinalRequiresNewEvidence(t *testing.T) {
	for _, intent := range []bool{false, true} {
		t.Run(fmt.Sprint(intent), func(t *testing.T) {
			d, g := readyPublicationFixture(t)
			ctx := context.Background()
			g, err := d.controlGoal(ctx, goal.ControlRequest{ID: g.ID, Revision: g.Revision, Action: "pause"})
			if err != nil {
				t.Fatal(err)
			}
			f := d.lin.(*linear.Fake)
			for i := range f.Details {
				if f.Details[i].ID == g.Parent.ID {
					f.Details[i].Description += "\nHuman clarification."
				}
			}
			g, err = d.syncGoalBrief(ctx, g.ID)
			if err != nil {
				t.Fatal(err)
			}
			oldID := g.Execution.Final.ID
			if intent {
				m := &goalwork.Manager{Root: filepath.Join(d.home, "goals")}
				dir, err := m.DeliveryDirectory(g.ID, oldID)
				if err != nil {
					t.Fatal(err)
				}
				if err = os.MkdirAll(dir, 0700); err != nil {
					t.Fatal(err)
				}
				if err = os.WriteFile(filepath.Join(dir, "publication.json"), []byte("ambiguous intent"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			c := g.BriefContract()
			c.Outcome += " with clarification"
			a := goal.BriefAcceptance{ID: g.ID, Revision: g.Revision, SourceID: g.PendingBrief, Contract: c, Reason: "Retained plan covers this clarification; repeat all final evidence"}
			out, err := d.acceptGoalBrief(ctx, a)
			if intent {
				if err == nil || !strings.Contains(err.Error(), "publication may") {
					t.Fatal("uncertain publication was discarded", err)
				}
				stored, _ := d.goals.Get(g.ID)
				if stored.Revision != g.Revision {
					t.Fatal("rejected request changed authority")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if out.Execution.Final != nil || out.State != "paused" || len(out.BriefRevisions[0].ArchivedFinals) != 1 {
				t.Fatal("old result retained as current")
			}
			ops := goalVerificationOperations(out)
			if !slices.Contains(ops, oldID) {
				t.Fatal("old validation usage disappeared")
			}
			out, err = d.controlGoal(ctx, goal.ControlRequest{ID: g.ID, Revision: out.Revision, Action: "resume"})
			if err != nil || out.State != "verifying" {
				t.Fatal("resume skipped final review", err)
			}
		})
	}
}
