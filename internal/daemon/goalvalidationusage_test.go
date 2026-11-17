package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/sushidev-team/lola/internal/goal"
	"github.com/sushidev-team/lola/internal/goalcheck"
	"github.com/sushidev-team/lola/internal/goalpolicy"
	"github.com/sushidev-team/lola/internal/goalwork"
)

func TestGoalUsageIncludesValidationWithoutRunningChecks(t *testing.T) {
	d, _, draft := goalFixture(t)
	g, err := d.createGoal(context.Background(), draft)
	if err != nil {
		t.Fatal(err)
	}
	spec := goalpolicy.Check{ID: "checks", Command: "true", Image: "sha256:" + strings.Repeat("a", 64), TimeoutSeconds: 30}
	g.Execution = &goal.Execution{PolicyDigest: "p", Policy: goalpolicy.Policy{Checks: []goalpolicy.Check{spec}}, Integrations: map[string]goal.Integration{"op": {ID: "op"}}}
	m := &goalwork.Manager{Root: filepath.Join(d.home, "goals")}
	id := verificationID("op", "check", spec.ID)
	dir, err := m.ValidationDirectory(g.ID, id)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC().Add(-time.Minute)
	b, _ := json.Marshal(goalcheck.Receipt{ID: id, Goal: g.ID, Operation: "op", Policy: "p", Spec: spec, State: "fail", Stopped: true, StartedAt: at, FinishedAt: at.Add(3 * time.Second)})
	if err = os.WriteFile(filepath.Join(dir, "check.json"), b, 0600); err != nil {
		t.Fatal(err)
	}
	d.goalCheckRun = func(context.Context, string, []string) (string, error) { t.Fatal("usage ran a check"); return "", nil }
	result := d.goalUsageReport(context.Background(), g)
	if result.Validation.ElapsedMillis != 3000 || len(result.Validation.Runs) != 1 || result.Validation.Runs[0].State != "fail" {
		t.Fatalf("failed check not attributed: %+v", result.Validation)
	}
	if next := d.goalUsageReport(context.Background(), g); !reflect.DeepEqual(next, result) {
		t.Fatal("repeated request charged again")
	}
	stored, _ := d.goals.Get(g.ID)
	if stored.Revision != g.Revision {
		t.Fatal("usage request mutated goal")
	}
}
func TestValidationUsageIncludesArchivedVerificationRounds(t *testing.T) {
	g := goal.Goal{Execution: &goal.Execution{
		Integrations: map[string]goal.Integration{"a": {}, "b": {}},
		Final:        &goal.FinalVerification{ID: "c"}, FinalHistory: []goal.FinalVerification{{ID: "d"}},
		Refreshes: []goal.Refresh{{Round: goal.RefreshRound{ID: "e"}, History: []goal.RefreshRound{{ID: "f"}}, Final: goal.FinalVerification{ID: "g"}, FinalHistory: []goal.FinalVerification{{ID: "h"}}, SupersededTargets: []goal.RefreshCapture{{Round: goal.RefreshRound{ID: "i"}, History: []goal.RefreshRound{{ID: "j"}, {ID: "a"}}}}}},
	}}
	want := []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j"}
	if got := goalVerificationOperations(g); !reflect.DeepEqual(got, want) {
		t.Fatalf("lost or doubled history: %v", got)
	}
}
