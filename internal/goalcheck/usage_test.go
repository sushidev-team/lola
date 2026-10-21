package goalcheck

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/sushidev-team/lola/internal/goalwork"
)

func usageFixture(t *testing.T) (*Runner, Spec, func(string, string, bool, time.Duration)) {
	t.Helper()
	runner := &Runner{Workspaces: &goalwork.Manager{Root: t.TempDir()}, Run: func(context.Context, string, []string) (string, error) {
		t.Fatal("accounting executed Docker")
		return "", nil
	}}
	spec := Spec{ID: "tests", Command: "true", Image: "sha256:" + strings.Repeat("a", 64), TimeoutSeconds: 10}
	write := func(id, state string, stopped bool, duration time.Duration) {
		t.Helper()
		dir, err := runner.Workspaces.ValidationDirectory("g", id)
		if err != nil {
			t.Fatal(err)
		}
		if err = os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		at := time.Now().UTC().Add(-time.Hour)
		receipt := Receipt{ID: id, Goal: "g", Operation: "op", Policy: "policy", Spec: spec, State: state, Stopped: stopped, StartedAt: at, FinishedAt: at.Add(duration)}
		if state == "running" {
			receipt.FinishedAt = time.Time{}
		}
		b, _ := json.Marshal(receipt)
		if err = os.WriteFile(filepath.Join(dir, "check.json"), b, 0600); err != nil {
			t.Fatal(err)
		}
	}
	return runner, spec, write
}
func TestValidationUsageDeduplicatesAndDoesNotInventDuration(t *testing.T) {
	r, s, write := usageFixture(t)
	write("pass", "pass", true, 2*time.Second)
	write("fail", "fail", true, 3*time.Second)
	write("interrupted", "interrupted", true, time.Hour)
	write("running", "running", false, 0)
	write("stopping", "error", false, 2*time.Second)
	write("clock", "error", true, -time.Second)
	var refs []UsageRef
	for _, id := range []string{"pass", "fail", "interrupted", "running", "stopping", "clock", "missing", "pass"} {
		refs = append(refs, UsageRef{ID: id, Operation: "op", Spec: s})
	}
	a := r.Usage(context.Background(), "g", "policy", refs)
	b := r.Usage(context.Background(), "g", "policy", refs)
	if !reflect.DeepEqual(a, b) {
		t.Fatal("repeat inspection accumulated usage")
	}
	if a.Expected != 7 || a.Inspected != 7 || len(a.Runs) != 6 || a.Missing != 1 || a.UnknownDuration != 4 || a.ElapsedMillis != 5000 || !a.Partial || a.CostStatus != "unavailable" {
		t.Fatalf("incorrect coverage: %+v", a)
	}
	for _, run := range a.Runs {
		if run.State == "interrupted" && run.ElapsedMillis != nil {
			t.Fatal("daemon downtime charged as measured runtime")
		}
	}
	if a.CPULimit != 2 || a.MemoryLimitBytes != 2<<30 {
		t.Fatal("missing resource ceilings")
	}
}
func TestValidationUsageRejectsForeignAndUnsafeReceipts(t *testing.T) {
	r, s, write := usageFixture(t)
	write("valid", "pass", true, time.Second)
	refs := []UsageRef{{ID: "valid", Operation: "op", Spec: s}}
	if result := r.Usage(context.Background(), "g", "foreign-policy", refs); result.Invalid != 1 || len(result.Runs) != 0 {
		t.Fatal("foreign policy counted")
	}
	dir, _ := r.Workspaces.ValidationDirectory("g", "link")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	valid, _ := r.Workspaces.ValidationDirectory("g", "valid")
	if err := os.Symlink(filepath.Join(valid, "check.json"), filepath.Join(dir, "check.json")); err != nil {
		t.Fatal(err)
	}
	refs = []UsageRef{{ID: "link", Operation: "op", Spec: s}}
	if result := r.Usage(context.Background(), "g", "policy", refs); result.Invalid != 1 || len(result.Runs) != 0 {
		t.Fatal("symlink counted")
	}
	write("oversize", "pass", true, time.Second)
	dir, _ = r.Workspaces.ValidationDirectory("g", "oversize")
	if err := os.Truncate(filepath.Join(dir, "check.json"), maxUsageReceiptBytes+1); err != nil {
		t.Fatal(err)
	}
	refs = []UsageRef{{ID: "oversize", Operation: "op", Spec: s}}
	if result := r.Usage(context.Background(), "g", "policy", refs); result.Invalid != 1 || len(result.Runs) != 0 {
		t.Fatal("oversized receipt counted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if result := r.Usage(ctx, "g", "policy", refs); !result.Partial || result.Inspected != 0 || result.Expected != 1 {
		t.Fatal("canceled inspection looks complete")
	}
}

func TestValidationUsageReadBudget(t *testing.T) {
	r, _, write := usageFixture(t)
	write("valid", "pass", true, time.Second)
	dir, _ := r.Workspaces.ValidationDirectory("g", "valid")
	if _, n, err := readUsageReceipt(dir, 1); err != errUsageReadLimit || n != 0 {
		t.Fatalf("scan budget exceeded: bytes=%d err=%v", n, err)
	}
}
