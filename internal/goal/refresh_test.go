package goal

import (
	"maps"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func readyRefreshGoal(t *testing.T) Goal {
	t.Helper()
	g := executable(t)
	g.Execution.Policy.Limits.TargetRefreshes = 2
	g.Execution.PolicyDigest, _ = g.Execution.Policy.Digest()
	for i, task := range g.Tasks {
		now := time.Now().UTC()
		id := task.ID
		a, err := g.ReserveAttempt(id, "a-"+id, "s-"+id, now)
		if err != nil {
			t.Fatal(err)
		}
		a.State = "submitted"
		g.Execution.Attempts[a.ID] = a
		iop, err := g.ReserveIntegration("i-"+id, a.ID, now)
		if err != nil {
			t.Fatal(err)
		}
		if err := g.CompleteIntegration(iop.ID, "receipt.json", map[string]string{"api": strings.Repeat(string(rune('b'+i)), 40)}, now); err != nil {
			t.Fatal(err)
		}
	}
	if err := g.ReserveFinal("final-original", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := g.CompleteFinal("final-original", "original-proof.json", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	return g
}

func TestRefreshStateRetainsEvidenceAndRequiresSeparateFinal(t *testing.T) {
	g := readyRefreshGoal(t)
	path := filepath.Join(t.TempDir(), "goals.json")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	g, err = s.Save(g)
	if err != nil {
		t.Fatal(err)
	}
	oldFinal := *g.Execution.Final
	base := maps.Clone(g.Execution.Manifest)
	prior := map[string]string{"api": strings.Repeat("a", 40)}
	targets := map[string]string{"api": strings.Repeat("d", 40)}
	if err := g.ReserveRefresh("refresh1", prior, targets, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if g.State != "verifying" || g.Execution.Final != nil || g.Execution.Refreshes[0].Final.ID != oldFinal.ID || !maps.Equal(g.Execution.Manifest, base) {
		t.Fatal("refresh discarded evidence or prematurely advanced goal")
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
	if err := g.ReserveFinal("too-early", time.Now().UTC()); err == nil {
		t.Fatal("final overlapped refresh barrier")
	}
	if err := g.Control("pause", "", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := g.CompleteRefresh("refresh1", "refresh-proof", targets, time.Now().UTC()); err == nil {
		t.Fatal("paused refresh advanced")
	}
	if err := g.Control("resume", "", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	result := map[string]string{"api": strings.Repeat("e", 40)}
	if err := g.CompleteRefresh("refresh1", "refresh-proof", result, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if g.State != "verifying" || g.Execution.Final != nil || !maps.Equal(g.Execution.Targets, targets) || g.Execution.PendingRefresh != "" {
		t.Fatal("refresh became ready without final verification")
	}
	g, err = s.Save(g)
	if err != nil {
		t.Fatal(err)
	}
	if err := g.ReserveFinal(oldFinal.ID, time.Now().UTC()); err == nil {
		t.Fatal("reused historical final identity")
	}
	if err := g.ReserveFinal("final-refreshed", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if !maps.Equal(g.Execution.Final.Targets, targets) {
		t.Fatal("new final lost target combination")
	}
	if err := g.CompleteFinal("final-refreshed", "new-proof.json", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	g, err = s.Save(g)
	if err != nil {
		t.Fatal(err)
	}
	changed := copyGoal(g)
	changed.Execution.Refreshes[0].Final.Receipt = "replaced.json"
	if _, err := s.Save(changed); err == nil {
		t.Fatal("rewrote completed refresh history")
	}
	if err := g.ReserveRefresh("refresh2", targets, map[string]string{"api": strings.Repeat("f", 40)}, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Save(g); err != nil {
		t.Fatal(err)
	}
	if _, err = Open(path); err != nil {
		t.Fatal("second refresh did not recover", err)
	}
}

func TestRefreshReservationsAreBoundedAndDoNotMutateOnFailure(t *testing.T) {
	for _, mode := range []string{"disabled", "limit", "runtime", "tokens", "paused", "unchanged", "incomplete", "stop"} {
		t.Run(mode, func(t *testing.T) {
			g := readyRefreshGoal(t)
			now := time.Now().UTC()
			prior := map[string]string{"api": strings.Repeat("a", 40)}
			targets := map[string]string{"api": strings.Repeat("d", 40)}
			switch mode {
			case "disabled":
				g.Execution.Policy.Limits.TargetRefreshes = 0
			case "limit":
				g.Execution.Policy.Limits.TargetRefreshes = 1
				g.Execution.Refreshes = []Refresh{{ID: "used"}}
			case "runtime":
				now = g.Execution.Deadline
			case "tokens":
				g.Execution.ObservedTokens = g.Execution.Policy.Limits.Tokens
			case "paused":
				g.State = "paused"
			case "unchanged":
				targets = maps.Clone(prior)
			case "incomplete":
				targets = map[string]string{}
			case "stop":
				g.Execution.StopWorkersAt = now
			}
			g.Execution.PolicyDigest, _ = g.Execution.Policy.Digest()
			before := copyGoal(g)
			if err := g.ReserveRefresh("new", prior, targets, now); err == nil {
				t.Fatal("unsafe reservation accepted")
			}
			if !reflect.DeepEqual(g, before) {
				t.Fatal("failed reservation mutated goal")
			}
		})
	}
}

func TestRefreshRetryKeepsCapturedInputsAndConsumesReviewRounds(t *testing.T) {
	g := readyRefreshGoal(t)
	prior := map[string]string{"api": strings.Repeat("a", 40)}
	targets := map[string]string{"api": strings.Repeat("d", 40)}
	if err := g.ReserveRefresh("refresh1", prior, targets, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	s, _ := Open(filepath.Join(t.TempDir(), "goals.json"))
	g, err := s.Save(g)
	if err != nil {
		t.Fatal(err)
	}
	g.State = "blocked"
	if err := g.RetryVerification("refresh2", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	r, _ := g.ActiveRefresh()
	if r.Round.ID != "refresh2" || len(r.History) != 1 || r.History[0].ID != "refresh1" || !maps.Equal(r.Targets, targets) {
		t.Fatal("retry lost captured operation")
	}
	g, err = s.Save(g)
	if err != nil {
		t.Fatal(err)
	}
	g.State = "blocked"
	if err := g.RetryVerification("refresh3", time.Now().UTC()); err == nil {
		t.Fatal("refresh retry exceeded bound")
	}
	changed := copyGoal(g)
	changed.Execution.Refreshes[0].Targets["api"] = strings.Repeat("f", 40)
	if _, err := s.Save(changed); err == nil {
		t.Fatal("recovery silently changed captured targets")
	}
}
