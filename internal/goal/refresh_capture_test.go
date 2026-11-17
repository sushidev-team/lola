package goal

import (
	"maps"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestRefreshRetargetPreservesEvidenceAcrossRestart(t *testing.T) {
	g := readyRefreshGoal(t)
	prior := map[string]string{"api": strings.Repeat("a", 40)}
	first := map[string]string{"api": strings.Repeat("d", 40)}
	next := map[string]string{"api": strings.Repeat("e", 40)}
	if err := g.ReserveRefresh("capture1", prior, first, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	g.State = "blocked"
	if err := g.RetryVerification("retry1", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "goals.json")
	s, _ := Open(path)
	g, err := s.Save(g)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := g.ActiveRefresh()
	if err := g.RetargetRefresh("retry1", "capture2", next, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	g, err = s.Save(g)
	if err != nil {
		t.Fatal(err)
	}
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	g, _ = s.Get(g.ID)
	r, ok := g.ActiveRefresh()
	if !ok || r.ID != "capture1" || r.CaptureID() != "capture2" || r.Round.ID != "capture2" || len(r.History) != 0 || g.RefreshCount() != 2 || !maps.Equal(r.Targets, next) || !maps.Equal(g.Execution.Manifest, before.Base) || !reflect.DeepEqual(r.Final, before.Final) {
		t.Fatalf("retarget lost authority or evidence: %+v", r)
	}
	want := before.currentCapture()
	want.SupersededAt = r.Round.CreatedAt
	if len(r.SupersededTargets) != 1 || !reflect.DeepEqual(r.SupersededTargets[0], want) {
		t.Fatal("old capture was rewritten")
	}
	for _, id := range []string{"capture1", "retry1"} {
		if !g.verificationIdentityUsed(id) {
			t.Fatal("lost historical identity", id)
		}
		if err := g.CompleteRefresh(id, "stale-proof", next, time.Now().UTC()); err == nil {
			t.Fatal("stale capture advanced refs")
		}
	}
	changed := copyGoal(g)
	changed.Execution.Refreshes[0].SupersededTargets[0].Targets["api"] = strings.Repeat("f", 40)
	if _, err := s.Save(changed); err == nil {
		t.Fatal("rewrote superseded capture")
	}
	if err := g.CompleteRefresh("capture2", "proof", next, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := g.ReserveFinal("final-new", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := g.CompleteFinal("final-new", "final-proof", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Save(g); err != nil {
		t.Fatal(err)
	}
	if err := g.ReserveRefresh("capture3", next, first, time.Now().UTC()); err == nil {
		t.Fatal("superseded capture did not consume the global limit")
	}
}

func TestRefreshRetargetFailuresDoNotMutate(t *testing.T) {
	for _, mode := range []string{"unchanged", "prior", "empty", "reused", "limit", "paused", "tokens", "runtime", "runs", "stop", "early"} {
		t.Run(mode, func(t *testing.T) {
			g := readyRefreshGoal(t)
			prior := map[string]string{"api": strings.Repeat("a", 40)}
			first := map[string]string{"api": strings.Repeat("d", 40)}
			if err := g.ReserveRefresh("capture1", prior, first, time.Now().UTC()); err != nil {
				t.Fatal(err)
			}
			now, id := time.Now().UTC(), "capture2"
			targets := map[string]string{"api": strings.Repeat("e", 40)}
			switch mode {
			case "unchanged":
				targets = first
			case "prior":
				targets = prior
			case "empty":
				targets = nil
			case "reused":
				id = "capture1"
			case "limit":
				g.Execution.Policy.Limits.TargetRefreshes = 1
			case "paused":
				g.State = "paused"
			case "tokens":
				g.Execution.ObservedTokens = g.Execution.Policy.Limits.Tokens
			case "runtime":
				now = g.Execution.Deadline
			case "runs":
				g.Execution.Policy.Profiles = []string{"backend"}
				g.Execution.Policy.Limits.AgentRuns = len(g.Execution.Runs)
			case "stop":
				g.Execution.StopWorkersAt = now
			case "early":
				now = g.Execution.Refreshes[0].Round.CreatedAt.Add(-time.Second)
			}
			g.Execution.PolicyDigest, _ = g.Execution.Policy.Digest()
			before := copyGoal(g)
			if err := g.RetargetRefresh("capture1", id, targets, now); err == nil {
				t.Fatal("unsafe retarget accepted")
			}
			if !reflect.DeepEqual(g, before) {
				t.Fatal("failed retarget mutated goal")
			}
		})
	}
}
