package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/sushidev-team/lola/internal/goal"
	"github.com/sushidev-team/lola/internal/goalcheck"
	"github.com/sushidev-team/lola/internal/goalpolicy"
	"github.com/sushidev-team/lola/internal/goalwork"
	"github.com/sushidev-team/lola/internal/reviewprofile"
)

func (f *goalRefreshRemote) advance() string {
	f.t.Helper()
	if err := os.WriteFile(filepath.Join(f.source, "upstream.txt"), []byte("upstream after "+f.target+"\n"), 0600); err != nil {
		f.t.Fatal(err)
	}
	goalFixtureGit(f.t, f.source, "add", "upstream.txt")
	goalFixtureGit(f.t, f.source, "commit", "-m", "upstream moves again")
	f.target = goalFixtureGit(f.t, f.source, "rev-parse", "HEAD")
	if f.pr != nil {
		f.pr["base"].(map[string]any)["sha"] = f.target
	}
	return f.target
}

func TestGoalRefreshRetargetBeforeWorkOrAfterReview(t *testing.T) {
	for _, when := range []string{"before-work", "after-review", "resume"} {
		t.Run(when, func(t *testing.T) {
			d, g := readyPublicationFixture(t, func(p *goalpolicy.Policy) { p.Limits.TargetRefreshes = 2 })
			f := newGoalRefreshRemote(t, d, g)
			d.goalVerify = nil
			g = goalTick(t, d, g.ID)
			old, _ := g.ActiveRefresh()
			base, runs := g.Execution.Manifest["p1"], len(g.Execution.Runs)
			original := d.profileRun
			if when == "after-review" {
				d.profileRun = func(ctx context.Context, dir string, p reviewprofile.Profile, in reviewprofile.Inputs) (reviewprofile.Result, error) {
					f.advance()
					return original(ctx, dir, p, in)
				}
			} else {
				if when == "resume" {
					var err error
					g, err = d.controlGoal(context.Background(), goal.ControlRequest{ID: g.ID, Revision: g.Revision, Action: "pause"})
					if err != nil {
						t.Fatal(err)
					}
				}
				f.advance()
				if when == "resume" {
					var err error
					g, err = d.controlGoal(context.Background(), goal.ControlRequest{ID: g.ID, Revision: g.Revision, Action: "resume"})
					if err != nil {
						t.Fatal(err)
					}
				}
			}
			g = goalTick(t, d, g.ID)
			r, ok := g.ActiveRefresh()
			if !ok || g.State != "verifying" || r.ID != old.ID || r.Round.ID == old.Round.ID || len(r.SupersededTargets) != 1 || r.SupersededTargets[0].Targets["p1"] != old.Targets["p1"] || r.Targets["p1"] != f.target || g.Execution.Manifest["p1"] != base || g.RefreshCount() != 2 || f.pushes != 0 {
				t.Fatalf("retarget did not preserve operation: %s %s %+v", g.State, g.Execution.Reason, r)
			}
			spent := 0
			if when == "after-review" {
				spent = 1
			}
			if len(g.Execution.Runs) != runs+spent || f.fetches != spent {
				t.Fatal("stale capture consumed unexpected work")
			}
			d.profileRun = original
			var err error
			d.goals, err = goal.Open(filepath.Join(d.home, "state", "goals.json"))
			if err != nil {
				t.Fatal(err)
			}
			for range 3 {
				g = goalTick(t, d, g.ID)
			}
			if g.State != "ready" || g.Execution.PendingRefresh != "" || g.Execution.Targets["p1"] != f.target || g.Execution.Delivery == nil || g.Execution.Delivery.State != "published" || len(g.Execution.Runs) != runs+spent+2 || f.creates != 1 || f.pushes != 1 {
				t.Fatalf("new capture did not finish through fresh final: %s %s %+v", g.State, g.Execution.Reason, g.Execution.Delivery)
			}
			if when == "after-review" {
				if _, err := os.Stat(filepath.Join(d.home, "goals", g.ID, "candidates", old.Round.ID, "p1", "candidate.json")); err != nil {
					t.Fatal("superseded candidate lost", err)
				}
			}
		})
	}
}

func TestGoalRefreshRepeatedTargetChangesStopAtAcceptedLimit(t *testing.T) {
	d, g := readyPublicationFixture(t, func(p *goalpolicy.Policy) { p.Limits.TargetRefreshes = 2 })
	f := newGoalRefreshRemote(t, d, g)
	d.goalVerify = nil
	g = goalTick(t, d, g.ID)
	base, runs := g.Execution.Manifest["p1"], len(g.Execution.Runs)
	f.advance()
	g = goalTick(t, d, g.ID)
	f.advance()
	g = goalTick(t, d, g.ID)
	if g.State != "blocked" || !strings.Contains(g.Execution.Reason, "target refresh limit exhausted") || g.RefreshCount() != 2 || g.Execution.Manifest["p1"] != base || f.fetches != 0 || len(g.Execution.Runs) != runs {
		t.Fatalf("unbounded retargeting: %s %s", g.State, g.Execution.Reason)
	}
	for range 3 {
		g = goalTick(t, d, g.ID)
	}
	if g.RefreshCount() != 2 || f.fetches != 0 || len(g.Execution.Runs) != runs {
		t.Fatal("held retarget kept doing work")
	}
}

func TestGoalRefreshRetargetRefusesAnyRefIntent(t *testing.T) {
	for _, kind := range []string{"manifest", "member"} {
		t.Run(kind, func(t *testing.T) {
			d, g := readyPublicationFixture(t, func(p *goalpolicy.Policy) { p.Limits.TargetRefreshes = 2 })
			f := newGoalRefreshRemote(t, d, g)
			g = goalTick(t, d, g.ID)
			r, _ := g.ActiveRefresh()
			path := filepath.Join(d.home, "goals", g.ID, "integrations", r.Round.ID+".json")
			if kind == "member" {
				path = filepath.Join(d.home, "goals", g.ID, "candidates", r.Round.ID, "p1", "integration.json")
			}
			if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte("{}"), 0600); err != nil {
				t.Fatal(err)
			}
			f.advance()
			if err := d.retargetGoalRefresh(context.Background(), g.ID, r.Round.ID); err == nil {
				t.Fatal("abandoned ref intent")
			}
			current, _ := d.goals.Get(g.ID)
			if current.Revision != g.Revision || current.RefreshCount() != 1 {
				t.Fatal("ref intent was superseded")
			}
		})
	}
}

func TestGoalRefreshRetargetPauseDuringCapturePreservesSteering(t *testing.T) {
	d, g := readyPublicationFixture(t, func(p *goalpolicy.Policy) { p.Limits.TargetRefreshes = 2 })
	f := newGoalRefreshRemote(t, d, g)
	g = goalTick(t, d, g.ID)
	r, _ := g.ActiveRefresh()
	f.advance()
	d.goalPublishRun = func(ctx context.Context, dir, bin string, args []string, input []byte) ([]byte, error) {
		if slices.Contains(args, "graphql") {
			current, _ := d.goals.Get(g.ID)
			if _, err := d.controlGoal(ctx, goal.ControlRequest{ID: g.ID, Revision: current.Revision, Action: "pause"}); err != nil {
				t.Fatal(err)
			}
		}
		return f.run(ctx, dir, bin, args, input)
	}
	if err := d.retargetGoalRefresh(context.Background(), g.ID, r.Round.ID); err == nil {
		t.Fatal("retarget overwrote pause")
	}
	current, _ := d.goals.Get(g.ID)
	if current.State != "paused" || current.RefreshCount() != 1 || current.Execution.Refreshes[0].Round.ID != r.Round.ID {
		t.Fatal("pause lost reserved capture")
	}
}

func TestGoalRefreshRetargetRejectsRewrittenCapturedHistory(t *testing.T) {
	d, g := readyPublicationFixture(t, func(p *goalpolicy.Policy) { p.Limits.TargetRefreshes = 3 })
	f := newGoalRefreshRemote(t, d, g)
	d.goalVerify = nil
	g = goalTick(t, d, g.ID)
	m := &goalwork.Manager{Root: filepath.Join(d.home, "goals")}
	repo, err := m.Repository(context.Background(), g.ID, "p1")
	if err != nil {
		t.Fatal(err)
	}
	// Create an alternative descendant of the original base without rewriting
	// any checkout/ref. It deliberately omits the first captured upstream commit.
	tree := goalFixtureGit(t, f.source, "rev-parse", repo.Start+"^{tree}")
	f.target = goalFixtureGit(t, f.source, "commit-tree", tree, "-p", repo.Start, "-m", "alternative upstream history")
	g = goalTick(t, d, g.ID)
	// Move once more before fetching: every skipped capture must still be an
	// ancestor, not merely the immediately preceding capture.
	f.target = goalFixtureGit(t, f.source, "commit-tree", tree, "-p", f.target, "-m", "descendant of rewritten history")
	g = goalTick(t, d, g.ID)
	runs := len(g.Execution.Runs)
	g = goalTick(t, d, g.ID)
	if g.State != "blocked" || !strings.Contains(g.Execution.Reason, "previous capture") || g.RefreshCount() != 3 || len(g.Execution.Runs) != runs || f.pushes != 0 {
		t.Fatalf("rewritten upstream was integrated: %s %s", g.State, g.Execution.Reason)
	}
}

// Keep cancellation distinct from a remote move: an expired/paused job must
// not turn its old operation into fresh authority.
func TestGoalRefreshRetargetCanceledContext(t *testing.T) {
	d, g := readyPublicationFixture(t, func(p *goalpolicy.Policy) { p.Limits.TargetRefreshes = 2 })
	f := newGoalRefreshRemote(t, d, g)
	g = goalTick(t, d, g.ID)
	r, _ := g.ActiveRefresh()
	f.advance()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := d.retargetGoalRefresh(ctx, g.ID, r.Round.ID); err == nil {
		t.Fatal("canceled retarget succeeded")
	}
	current, _ := d.goals.Get(g.ID)
	if current.Revision != g.Revision {
		t.Fatal("canceled retarget saved")
	}
}

func TestGoalRefreshRetargetRequiresConfirmedCheckStop(t *testing.T) {
	d, g := readyPublicationFixture(t, func(p *goalpolicy.Policy) { p.Limits.TargetRefreshes = 2 })
	f := newGoalRefreshRemote(t, d, g)
	g = goalTick(t, d, g.ID)
	r, _ := g.ActiveRefresh()
	f.advance()
	m := &goalwork.Manager{Root: filepath.Join(d.home, "goals")}
	checkID := verificationID(r.Round.ID, "check", g.Execution.Policy.Checks[0].ID)
	dir, err := m.ValidationDirectory(g.ID, checkID)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "check.json")
	b, _ := json.Marshal(goalcheck.Receipt{ID: checkID, Goal: g.ID, Operation: r.Round.ID, State: "running"})
	if err := os.WriteFile(path, b, 0600); err != nil {
		t.Fatal(err)
	}
	stops := 0
	d.goalCheckRun = func(_ context.Context, _ string, args []string) (string, error) {
		if len(args) != 3 || args[0] != "rm" || args[1] != "-f" {
			t.Fatal("recovery launched a new check", args)
		}
		stops++
		return "", errors.New("Docker stop unavailable")
	}
	if err := d.retargetGoalRefresh(context.Background(), g.ID, r.Round.ID); err == nil {
		t.Fatal("superseded a potentially live check")
	}
	current, _ := d.goals.Get(g.ID)
	if current.Revision != g.Revision || stops != 1 {
		t.Fatal("unconfirmed stop changed authority")
	}
	d.goalCheckRun = func(context.Context, string, []string) (string, error) { stops++; return "", nil }
	if err := d.retargetGoalRefresh(context.Background(), g.ID, r.Round.ID); err != nil {
		t.Fatal(err)
	}
	var receipt goalcheck.Receipt
	b, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(b, &receipt); err != nil {
		t.Fatal(err)
	}
	current, _ = d.goals.Get(g.ID)
	if current.RefreshCount() != 2 || stops != 2 || !receipt.Stopped || receipt.State != "interrupted" {
		t.Fatal("retarget lost stop evidence")
	}
}
