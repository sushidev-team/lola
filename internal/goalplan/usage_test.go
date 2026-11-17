package goalplan

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/sushidev-team/lola/internal/usage"
)

func TestPlannerFailureStillRecordsUsageBeforeResult(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "plans.json"))
	if err != nil {
		t.Fatal(err)
	}
	r, _, err := store.Reserve(inputFixture(t), "codex", "image")
	if err != nil {
		t.Fatal(err)
	}
	runner := Runner{OnUsage: func(o usage.Observation) error { return store.ObserveUsage(r.ID, o) }, Run: func(_ context.Context, _ string, args []string, _ []byte, _ []string) ([]byte, error) {
		if args[0] == "run" {
			return fixtureAgentEvents(args, []byte(`{"wrong":"proposal"}`)), errors.New("exit status 1")
		}
		return nil, nil
	}}
	out, runErr := runner.Plan(context.Background(), r)
	if runErr == nil {
		t.Fatal("failed call passed")
	}
	saved, err := store.Finish(r.ID, &out, runErr)
	if err != nil {
		t.Fatal(err)
	}
	if saved.State != "failed" || saved.Usage == nil || usage.Sum(saved.Usage.Days).Tokens() != 120 {
		t.Fatal("failed call erased observed consumption")
	}
	reopened, err := Open(store.path)
	if err != nil {
		t.Fatal(err)
	}
	saved, _ = reopened.Get(r.ID)
	if saved.Usage == nil {
		t.Fatal("restart forgot usage")
	}
	if _, fresh, _ := reopened.Reserve(r.Input, "codex", "image"); fresh {
		t.Fatal("failure relaunched paid call")
	}
}

func TestPlannerInterruptedAfterUsageRetainsNumericEvidence(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "plans.json"))
	if err != nil {
		t.Fatal(err)
	}
	r, _, err := store.Reserve(inputFixture(t), "claude", "image")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	o := usage.Observation{Agent: "claude", Status: "partial", CostStatus: "estimate", At: now, Days: map[string]usage.Totals{now.Local().Format(usage.DayFormat): {Input: 100, CostUSD: 0.25}}}
	if err := store.ObserveUsage(r.ID, o); err != nil {
		t.Fatal(err)
	}
	if err := store.ObserveUsage(r.ID, o); err != nil {
		t.Fatal("idempotent callback failed", err)
	}
	changed := o
	changed.CostStatus = "unavailable"
	if err := store.ObserveUsage(r.ID, changed); err == nil {
		t.Fatal("usage evidence replaced")
	}
	reopened, err := Open(store.path)
	if err != nil {
		t.Fatal(err)
	}
	saved, _ := reopened.Get(r.ID)
	if saved.State != "interrupted" || saved.Usage == nil || usage.Sum(saved.Usage.Days).CostUSD != 0.25 {
		t.Fatal("interrupted call lost numeric evidence")
	}
}
