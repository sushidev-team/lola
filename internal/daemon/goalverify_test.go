package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/goal"
	"github.com/sushidev-team/lola/internal/goalpolicy"
	"github.com/sushidev-team/lola/internal/goalwork"
	"github.com/sushidev-team/lola/internal/reviewprofile"
)

// Host Git is used only to simulate a container's output in this fixture.
func goalFixtureGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "core.hooksPath=/dev/null", "-c", "commit.gpgsign=false", "-c", "user.name=Test", "-c", "user.email=test@localhost", "-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null")
	b, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git fixture: %v %s", err, b)
	}
	return strings.TrimSpace(string(b))
}

func submittedGoalFixture(t *testing.T, changePolicy func(*goalpolicy.Policy)) (*Daemon, goal.Goal, *goalwork.Manager, *int) {
	t.Helper()
	d, _, g, policy, launched := goalExecutionFixture(t)
	d.cfg.ReviewProviders = []config.ReviewProvider{{Provider: "codex-session", Enabled: true}}
	d.cfg.ReviewProfiles = []reviewprofile.Profile{{ID: "backend", Kind: "backend", Provider: "codex-session", Version: "1", Enabled: true, Required: true}}
	policy.Profiles = []string{"backend"}
	if changePolicy != nil {
		changePolicy(&policy)
	}
	var err error
	g, err = d.authorizeGoal(context.Background(), goal.Authorization{ID: g.ID, Revision: g.Revision, Policy: policy})
	if err != nil {
		t.Fatal(err)
	}
	if err = d.runGoal(context.Background(), g.ID); err != nil {
		t.Fatal(err)
	}
	g = submitRunningGoalTask(t, d, g.ID)
	m := &goalwork.Manager{Root: filepath.Join(d.home, "goals")}
	d.goalCheckRun = func(context.Context, string, []string) (string, error) { return "independent acceptance passed", nil }
	d.profileRun = func(_ context.Context, dir string, _ reviewprofile.Profile, in reviewprofile.Inputs) (reviewprofile.Result, error) {
		if strings.Contains(in.Contract, "CLAIM:") {
			t.Fatal("reviewer was primed with worker defense")
		}
		if got := goalFixtureGit(t, dir, "rev-parse", "HEAD"); got != in.Head {
			t.Fatal("reviewer saw wrong candidate")
		}
		return reviewprofile.Result{Complete: true, Evidence: []reviewprofile.Evidence{{Criterion: "accepted behavior", Observation: "candidate implements the contract"}}}, nil
	}
	return d, g, m, launched
}

func TestGoalVerificationIntegratesAndReleasesDependentWork(t *testing.T) {
	d, g, m, launched := submittedGoalFixture(t, nil)
	op := g.Execution.PendingIntegration
	base := g.Execution.Manifest["p1"]
	if err := d.verifyGoalIntegration(context.Background(), g.ID, op); err != nil {
		t.Fatal(err)
	}
	verified, _ := d.goals.Get(g.ID)
	if verified.Execution.PendingIntegration != "" || verified.Execution.Integrations[op].State != "complete" {
		t.Fatal("integration did not persist completion")
	}
	tip, err := m.Tip(context.Background(), g.ID, "p1")
	if err != nil || tip == base || tip != verified.Execution.Manifest["p1"] {
		t.Fatalf("wrong manifest: %s %v", tip, err)
	}
	if got := goalFixtureGit(t, g.Execution.Policy.Members[0].Source, "rev-parse", "main"); got != base {
		t.Fatal("source main changed")
	}
	r, err := m.Repository(context.Background(), g.ID, "p1")
	if err != nil {
		t.Fatal(err)
	}
	if got := goalFixtureGit(t, r.GitDir, "rev-parse", "main"); got != base {
		t.Fatal("private main changed")
	}
	if err := d.runGoal(context.Background(), g.ID); err != nil {
		t.Fatal(err)
	}
	if *launched != 2 {
		t.Fatal("verified dependency did not release next task")
	}
	next, _ := d.goals.Get(g.ID)
	for _, a := range next.Execution.Attempts {
		if a.State == "running" && a.Base["p1"] != tip {
			t.Fatal("dependent worker did not receive verified combination")
		}
	}
}

func TestGoalVerificationFailuresNeverAdvance(t *testing.T) {
	for _, mode := range []string{"check", "review", "missing-artifact", "candidate-write", "pause", "rubric-change", "invalidated-review"} {
		t.Run(mode, func(t *testing.T) {
			d, g, m, launched := submittedGoalFixture(t, func(p *goalpolicy.Policy) {
				if mode == "missing-artifact" {
					p.Checks[0].Artifacts = []goalpolicy.ArtifactSpec{{ID: "screen", Kind: "screenshot", Path: "screen.png"}}
				}
			})
			base := g.Execution.Manifest["p1"]
			original := d.profileRun
			if mode == "check" {
				d.goalCheckRun = func(context.Context, string, []string) (string, error) {
					return "assertion failed", errors.New("failed")
				}
			}
			d.profileRun = func(ctx context.Context, dir string, p reviewprofile.Profile, in reviewprofile.Inputs) (reviewprofile.Result, error) {
				switch mode {
				case "review":
					return reviewprofile.Result{}, errors.New("review unavailable")
				case "candidate-write":
					if err := os.WriteFile(filepath.Join(dir, "app.txt"), []byte("unverified edit"), 0600); err != nil {
						t.Fatal(err)
					}
				case "pause":
					current, _ := d.goals.Get(g.ID)
					if _, err := d.controlGoal(ctx, goal.ControlRequest{ID: g.ID, Revision: current.Revision, Action: "pause"}); err != nil {
						t.Fatal(err)
					}
				case "rubric-change":
					d.cfg.ReviewProfiles[0].Version = "2"
				case "invalidated-review":
					if err := d.profileReviews.Invalidate(p.Key(in), "new evidence"); err != nil {
						t.Fatal(err)
					}
				}
				return original(ctx, dir, p, in)
			}
			if err := d.verifyGoalIntegration(context.Background(), g.ID, g.Execution.PendingIntegration); err == nil {
				t.Fatal("unverified integration accepted")
			}
			if tip, _ := m.Tip(context.Background(), g.ID, "p1"); tip != base {
				t.Fatal("failed verification advanced goal branch")
			}
			current, _ := d.goals.Get(g.ID)
			if current.Execution.Manifest["p1"] != base || *launched != 1 {
				t.Fatal("failed verification released work")
			}
		})
	}
}

func TestGoalVerificationJobDeduplicatesAndCancels(t *testing.T) {
	d, g, _, _ := submittedGoalFixture(t, nil)
	started, stopped := make(chan struct{}), make(chan struct{})
	d.goalVerify = func(ctx context.Context, _, _ string) error {
		close(started)
		<-ctx.Done()
		close(stopped)
		return ctx.Err()
	}
	if err := d.queueGoalVerification(context.Background(), g, g.Execution.PendingIntegration); err != nil {
		t.Fatal(err)
	}
	<-started
	if err := d.queueGoalVerification(context.Background(), g, g.Execution.PendingIntegration); err != nil {
		t.Fatal(err)
	}
	if _, err := d.controlGoal(context.Background(), goal.ControlRequest{ID: g.ID, Revision: g.Revision, Action: "pause"}); err != nil {
		t.Fatal(err)
	}
	<-stopped
	d.connWg.Wait()
	current, _ := d.goals.Get(g.ID)
	if current.State != "paused" {
		t.Fatal("canceled verification overwrote pause")
	}
}

func TestGoalConclusiveFindingsScheduleOnlyBoundedRepairs(t *testing.T) {
	d, g, m, launched := submittedGoalFixture(t, nil)
	base := g.Execution.Manifest["p1"]
	d.goalVerify = nil
	d.profileRun = func(context.Context, string, reviewprofile.Profile, reviewprofile.Inputs) (reviewprofile.Result, error) {
		return reviewprofile.Result{Complete: true, Evidence: []reviewprofile.Evidence{{Criterion: "accepted behavior", Observation: "reproduced missing case"}}, Findings: []reviewprofile.Finding{{Requirement: "Handle missing data", Location: "app.txt:1", Reproduction: "pass empty data", Evidence: "the contract requires an empty state", Severity: "major", Confidence: "verified", Fix: "add the missing empty state"}}}, nil
	}
	for round := 0; round < 2; round++ {
		if err := d.queueGoalVerification(context.Background(), g, g.Execution.PendingIntegration); err != nil {
			t.Fatal(err)
		}
		d.connWg.Wait()
		g, _ = d.goals.Get(g.ID)
		if g.Execution.PendingIntegration != "" {
			t.Fatal("rejected candidate kept integration queue occupied")
		}
		if tip, _ := m.Tip(context.Background(), g.ID, "p1"); tip != base {
			t.Fatal("findings advanced goal")
		}
		for _, entry := range d.knowledge.List() {
			if entry.Scope.GoalID != g.ID {
				t.Fatal("experimental findings leaked into project knowledge")
			}
		}
		if round == 0 {
			if g.State != "running" {
				t.Fatalf("repair not scheduled: %s", g.State)
			}
			if err := d.runGoal(context.Background(), g.ID); err != nil {
				t.Fatal(err)
			}
			if *launched != 2 {
				t.Fatal("repair worker not launched")
			}
			// Submit through the real ingestion path, holding its async job so
			// the next loop controls the verification completion deterministically.
			d.goalVerify = func(context.Context, string, string) error { return nil }
			g = submitRunningGoalTask(t, d, g.ID)
			d.goalVerify = nil
		} else if g.State != "blocked" || *launched != 2 {
			t.Fatal("repair exceeded accepted attempt budget")
		}
	}
}

func TestGoalVerificationRecoveryUsesFreshIdentityAndPreservesFailedEvidence(t *testing.T) {
	d, g, m, _ := submittedGoalFixture(t, nil)
	old := g.Execution.PendingIntegration
	d.goalVerify = nil
	d.goalCheckRun = func(_ context.Context, _ string, args []string) (string, error) {
		if args[0] == "rm" {
			return "", nil
		}
		return "", errors.New("Docker unavailable")
	}
	if err := d.queueGoalVerification(context.Background(), g, old); err != nil {
		t.Fatal(err)
	}
	d.connWg.Wait()
	g, _ = d.goals.Get(g.ID)
	if g.State != "blocked" {
		t.Fatal("uncertain check scheduled automatic repair")
	}
	// Any journal, even an unreadable one, holds an operation for recovery.
	journal := filepath.Join(m.Root, g.ID, "integrations", old+".json")
	if err := os.MkdirAll(filepath.Dir(journal), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(journal, []byte("partial intent"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := d.controlGoal(context.Background(), goal.ControlRequest{ID: g.ID, Revision: g.Revision, Action: "retry-verification"}); err == nil {
		t.Fatal("partial integration was abandoned")
	}
	// Remove only the synthetic test journal, never production recovery state.
	if err := os.Remove(journal); err != nil {
		t.Fatal(err)
	}
	g, err := d.controlGoal(context.Background(), goal.ControlRequest{ID: g.ID, Revision: g.Revision, Action: "retry-verification"})
	if err != nil {
		t.Fatal(err)
	}
	if g.Execution.PendingIntegration == old || g.Execution.Integrations[old].State != "failed" {
		t.Fatal("retry reused failed identity")
	}
	d.goalCheckRun = func(context.Context, string, []string) (string, error) { return "pass", nil }
	if err := d.runGoal(context.Background(), g.ID); err != nil {
		t.Fatal(err)
	}
	d.connWg.Wait()
	current, _ := d.goals.Get(g.ID)
	if current.Execution.PendingIntegration != "" {
		t.Fatalf("recovery failed: %s", current.Execution.Reason)
	}
	path, err := m.ValidationDirectory(g.ID, verificationID(old, "check", "tests"))
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(path, "check.json"))
	if err != nil || !strings.Contains(string(b), "Docker unavailable") {
		t.Fatal("failed evidence was discarded")
	}
}

func TestGoalUIReviewReceivesCapturedArtifactsAndRechecksHashes(t *testing.T) {
	for _, tamper := range []bool{false, true} {
		name := "captured"
		if tamper {
			name = "changed-after-review"
		}
		t.Run(name, func(t *testing.T) {
			d, g, m, _ := submittedGoalFixture(t, func(p *goalpolicy.Policy) {
				p.Checks[0].Artifacts = []goalpolicy.ArtifactSpec{{ID: "screen", Kind: "screenshot", Path: "p1/screen.png"}, {ID: "keyboard", Kind: "interaction", Path: "p1/keyboard.json"}}
			})
			d.cfg.ReviewProfiles[0].Kind = "uiux"
			op := g.Execution.PendingIntegration
			d.goalCheckRun = func(_ context.Context, _ string, args []string) (string, error) {
				if args[0] == "rm" {
					return "", nil
				}
				dir, err := m.ValidationDirectory(g.ID, verificationID(op, "check", "tests"))
				if err != nil {
					return "", err
				}
				// Test seam supplies bytes; the real container must produce the
				// project's screenshot and interaction report itself.
				for name, body := range map[string]string{"screen.png": "test image bytes", "keyboard.json": "{\"keyboard\":\"pass\"}"} {
					if err := os.WriteFile(filepath.Join(dir, "workspace", "p1", name), []byte(body), 0600); err != nil {
						return "", err
					}
				}
				return "captured", nil
			}
			calls := 0
			d.profileRun = func(_ context.Context, _ string, _ reviewprofile.Profile, in reviewprofile.Inputs) (reviewprofile.Result, error) {
				calls++
				if len(in.Artifacts) != 2 {
					t.Fatal("reviewer did not receive runtime evidence")
				}
				var evidence []reviewprofile.Evidence
				for _, artifact := range in.Artifacts {
					if strings.Contains(artifact.Path, "/workspace/") || artifact.SHA256 == "" {
						t.Fatal("reviewer received mutable validation output")
					}
					if _, err := os.ReadFile(artifact.Path); err != nil {
						t.Fatal(err)
					}
					evidence = append(evidence, reviewprofile.Evidence{Criterion: artifact.Kind, Observation: "test seam inspected captured evidence", ArtifactID: artifact.ID})
				}
				if tamper {
					if err := os.WriteFile(in.Artifacts[0].Path, []byte("different evidence"), 0600); err != nil {
						t.Fatal(err)
					}
				}
				return reviewprofile.Result{Complete: true, Evidence: evidence}, nil
			}
			err := d.verifyGoalIntegration(context.Background(), g.ID, op)
			if calls != 1 || (err != nil) != tamper {
				t.Fatalf("artifact review: calls=%d tamper=%v err=%v", calls, tamper, err)
			}
			current, _ := d.goals.Get(g.ID)
			if tamper && current.Execution.Manifest["p1"] != g.Execution.Manifest["p1"] {
				t.Fatal("changed evidence advanced integration")
			}
		})
	}
}

func TestGoalFinalVerificationCoversWholeDiffAndRetainsEvidence(t *testing.T) {
	d, g, m, _ := submittedGoalFixture(t, nil)
	start := g.Execution.Manifest["p1"]
	if err := d.verifyGoalIntegration(context.Background(), g.ID, g.Execution.PendingIntegration); err != nil {
		t.Fatal(err)
	}
	if err := d.runGoal(context.Background(), g.ID); err != nil {
		t.Fatal(err)
	}
	g = submitRunningGoalTask(t, d, g.ID)
	if err := d.verifyGoalIntegration(context.Background(), g.ID, g.Execution.PendingIntegration); err != nil {
		t.Fatal(err)
	}
	g, _ = d.goals.Get(g.ID)
	if g.State != "verifying" || g.Execution.Final != nil {
		t.Fatal("task integrations skipped final verification")
	}
	if err := d.runGoal(context.Background(), g.ID); err != nil {
		t.Fatal(err)
	}
	d.connWg.Wait()
	g, _ = d.goals.Get(g.ID)
	if g.Execution.Final == nil {
		t.Fatal("controller did not reserve final verification")
	}
	calls := 0
	d.profileRun = func(_ context.Context, _ string, _ reviewprofile.Profile, in reviewprofile.Inputs) (reviewprofile.Result, error) {
		calls++
		if in.Base != start || in.Head != g.Execution.Manifest["p1"] || !strings.Contains(in.Contract, "complete goal") {
			t.Fatal("final review only covered the last task")
		}
		return reviewprofile.Result{Complete: true, Evidence: []reviewprofile.Evidence{{Criterion: "whole goal", Observation: "every accepted behavior verified"}}}, nil
	}
	if err := d.verifyGoalFinal(context.Background(), g.ID, g.Execution.Final.ID); err != nil {
		t.Fatal(err)
	}
	ready, _ := d.goals.Get(g.ID)
	if ready.State != "ready" || ready.Execution.Final.Receipt == "" || calls != 1 {
		t.Fatal("final evidence did not produce a ready artifact")
	}
	if _, err := os.Stat(ready.Execution.Final.Receipt); err != nil {
		t.Fatal(err)
	}
	tip, _ := m.Tip(context.Background(), g.ID, "p1")
	if tip != g.Execution.Manifest["p1"] {
		t.Fatal("final review unexpectedly moved refs")
	}
	d.goals, _ = goal.Open(filepath.Join(d.home, "state", "goals.json"))
	if err := d.runGoal(context.Background(), g.ID); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatal("restart repeated final review")
	}

}

func submitRunningGoalTask(t *testing.T, d *Daemon, id string) goal.Goal {
	t.Helper()
	g, _ := d.goals.Get(id)
	var err error
	var a goal.Attempt
	for _, attempt := range g.Execution.Attempts {
		if attempt.State == "running" {
			a = attempt
			break
		}
	}
	m := &goalwork.Manager{Root: filepath.Join(d.home, "goals")}
	w, err := m.PrepareWorker(context.Background(), g.ID, "p1", a.TaskID, a.ID, a.Base["p1"])
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(w.Directory, "app.txt"), []byte("implemented "+a.ID+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	goalFixtureGit(t, w.Directory, "add", "app.txt")
	goalFixtureGit(t, w.Directory, "commit", "-m", "worker output")
	head := goalFixtureGit(t, w.Directory, "rev-parse", "HEAD")
	goalFixtureGit(t, w.Directory, "bundle", "create", filepath.Join(w.Directory, ".lola", "submission.bundle"), "HEAD", "^"+w.Base)
	s, _ := d.sessions.Get(a.SessionID)
	b, _ := json.Marshal(goalwork.Submission{Goal: g.ID, Task: a.TaskID, Attempt: a.ID, Heads: map[string]string{"p1": head}, Summary: "CLAIM: all checks passed"})
	if err = os.WriteFile(filepath.Join(s.Worktree, "submission.json"), b, 0600); err != nil {
		t.Fatal(err)
	}
	if err = d.runGoal(context.Background(), g.ID); err != nil {
		t.Fatal(err)
	}
	d.connWg.Wait() // fixture's no-op verifier, not a real external process
	g, _ = d.goals.Get(g.ID)
	return g
}
