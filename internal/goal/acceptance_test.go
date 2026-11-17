package goal

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func humanAcceptanceGoal(t *testing.T) Goal {
	t.Helper()
	g := readyRefreshGoal(t)
	g.Execution.Policy.PublishPR = true
	g.Execution.Policy.RequireHumanAcceptance = true
	g.Execution.Policy.Members[0].Repo = "owner/api"
	g.Execution.PolicyDigest, _ = g.Execution.Policy.Digest()
	g.Revision = 3
	d := Delivery{Operation: g.Execution.Final.ID, State: "merged", CheckedAt: time.Now().UTC(), Members: map[string]DeliveryMember{
		"api": {Repo: "owner/api", Target: "main", Head: g.Execution.Manifest["api"], State: "merged", Number: 1, URL: "https://github.com/owner/api/pull/1"},
	}}
	if err := g.ObserveDelivery(d); err != nil || g.State != "ready" {
		t.Fatalf("observed merge bypassed required acceptance: %s %v", g.State, err)
	}
	return g
}

func TestHumanAcceptanceRequiresExactDeliveredEvidenceAndPreservesTaskDelivery(t *testing.T) {
	g := humanAcceptanceGoal(t)
	if !g.Delivered() || g.WorkflowStage(g.Tasks[0].ID) != "delivered" || !strings.Contains(g.ProgressSummary(), "separate human acceptance decision") {
		t.Fatal("goal acceptance requirement hid observed task delivery or pending decision")
	}
	for _, kind := range []string{"open", "wrong-head", "partial", "paused", "brief", "sync", "unverified", "empty-reason", "early-time", "not-required"} {
		t.Run(kind, func(t *testing.T) {
			trial := copyGoal(g)
			now, reason := time.Now().UTC(), "Verified invitation acceptance in the delivered app"
			switch kind {
			case "open":
				trial.Execution.Delivery.State = "published"
			case "wrong-head":
				m := trial.Execution.Delivery.Members["api"]
				m.Head = strings.Repeat("f", 40)
				trial.Execution.Delivery.Members["api"] = m
			case "partial":
				delete(trial.Execution.Delivery.Members, "api")
			case "paused":
				trial.State = "paused"
			case "brief":
				trial.PendingBrief = "changed"
			case "sync":
				trial.SyncError = "outage"
			case "unverified":
				trial.Execution.Final.Receipt = ""
			case "empty-reason":
				reason = " \t"
			case "early-time":
				now = trial.Execution.Delivery.CheckedAt.Add(-time.Second)
			case "not-required":
				trial.Execution.Policy.RequireHumanAcceptance = false
			}
			before := copyGoal(trial)
			if err := trial.Control("accept-delivery", reason, now); err == nil || !reflect.DeepEqual(before, trial) {
				t.Fatal("invalid acceptance succeeded or mutated the goal")
			}
		})
	}
	if err := g.Control("accept-delivery", "Tested the delivered invitation flow", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	g.Revision++ // Save increments before validating the snapshot.
	if err := g.validateExecution(); err != nil {
		t.Fatal(err)
	}
	a := g.Execution.HumanAcceptance
	if a.Revision != 3 || a.Operation != g.Execution.Final.ID || a.Manifest["api"] != g.Execution.Manifest["api"] || g.State != "accepted" {
		t.Fatal("acceptance did not retain exact reviewed evidence")
	}
	g.Execution.Manifest["api"] = "different"
	if a.Manifest["api"] == "different" {
		t.Fatal("acceptance aliases mutable manifest")
	}
}

func TestHumanAcceptanceStoreMigrationIsAtomicAndRetainsLegacyGoals(t *testing.T) {
	path := filepath.Join(t.TempDir(), "goals.json")
	s, _ := Open(path)
	legacy, _ := New(testDraft(), "team", "project")
	legacy, err := s.Save(legacy)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(path)
	if s.data.Version != 1 {
		t.Fatal("legacy-only write changed format")
	}
	if _, err := Open(path); err != nil {
		t.Fatal(err)
	}
	afterRead, _ := os.ReadFile(path)
	if !bytes.Equal(before, afterRead) {
		t.Fatal("reading v1 rewrote it")
	}
	g := humanAcceptanceGoal(t)
	g.Revision = 0
	s.write = func(string, []byte) error { return errors.New("disk full") }
	if _, err := s.Save(g); err == nil || s.data.Version != 1 || len(s.List()) != 1 {
		t.Fatal("failed migration became visible")
	}
	afterFailure, _ := os.ReadFile(path)
	if !bytes.Equal(before, afterFailure) {
		t.Fatal("failed migration changed disk")
	}
	s.write = atomicWrite
	g, err = s.Save(g)
	if err != nil {
		t.Fatal(err)
	}
	if s.data.Version != 2 {
		t.Fatal("new permission did not upgrade the format")
	}
	if err := g.Control("accept-delivery", "Manually tested the merged flow", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	s.write = func(string, []byte) error { return errors.New("disk full during acceptance") }
	if _, err := s.Save(g); err == nil {
		t.Fatal("failed acceptance acknowledged")
	}
	uncommitted, _ := s.Get(g.ID)
	if uncommitted.State != "ready" || uncommitted.Execution.HumanAcceptance != nil {
		t.Fatal("failed decision became visible")
	}
	s.write = atomicWrite
	g, err = s.Save(g)
	if err != nil {
		t.Fatal(err)
	}
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	reopened, _ := s.Get(g.ID)
	if !reflect.DeepEqual(g, reopened) {
		t.Fatal("restart lost acceptance evidence")
	}
	gotLegacy, _ := s.Get(legacy.ID)
	if !reflect.DeepEqual(legacy, gotLegacy) {
		t.Fatal("migration changed legacy goal")
	}
	for _, change := range []string{"remove", "evidence", "revision", "state", "operation"} {
		trial := copyGoal(g)
		switch change {
		case "remove":
			trial.Execution.HumanAcceptance = nil
		case "evidence":
			trial.Execution.HumanAcceptance.Evidence = "different"
		case "revision":
			trial.Execution.HumanAcceptance.Revision++
		case "state":
			trial.State = "ready"
		case "operation":
			trial.Execution.Final.ID = "other"
		}
		if _, err := s.Save(trial); err == nil {
			t.Fatalf("accepted receipt rewrite: %s", change)
		}
	}
	// An unrelated subsequent write must not downgrade the shared snapshot.
	if _, err := s.Save(gotLegacy); err != nil || s.data.Version != 2 {
		t.Fatal("format downgraded", err)
	}
	corrupt := s.data
	corrupt.Version = 1
	b, _ := json.Marshal(corrupt)
	if err := os.WriteFile(path, b, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(path); err == nil {
		t.Fatal("v1 silently accepted a new acceptance permission")
	}
}
