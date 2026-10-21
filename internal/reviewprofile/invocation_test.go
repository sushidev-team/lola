package reviewprofile

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"
)

func TestInvocationSeparatesCalibrationAndPreservesLegacyHistory(t *testing.T) {
	s, p, original, finding := findingFixture(t)
	decision := FindingDecision{ID: finding.ID, Revision: finding.Revision, State: "accepted", Evidence: "Confirmed by the independent concurrent write reproduction"}
	if _, err := s.Decide(decision); err != nil {
		t.Fatal(err)
	}
	before := s.Findings()
	// Simulate a pre-provenance writer with real decision history.
	legacy, _ := json.Marshal(disk{Version: 1, Records: s.records, Findings: s.findings})
	if err := os.WriteFile(s.path, legacy, 0600); err != nil {
		t.Fatal(err)
	}
	var err error
	s, err = Open(s.path)
	if err != nil || !reflect.DeepEqual(before, s.Findings()) {
		t.Fatal("migration altered legacy evidence", err)
	}
	for _, v := range []*Invocation{
		NewInvocation("codex", "", "host-read-only", p.Instruction()),
		NewInvocation("codex", "model-a", "host-read-only", p.Instruction()),
		NewInvocation("codex", "model-b", "host-read-only", p.Instruction()),
		NewInvocation("codex", "model-b", "host-read-only", p.Instruction()+"\nNew instruction."),
		NewInvocation("codex", "model-b", "container-read-only", p.Instruction()),
	} {
		in := original.Inputs
		in.Invocation = v
		r, err := s.Begin(p, in, false)
		if err != nil {
			t.Fatal("invocations reused a key", err)
		}
		r.State, r.Result = "findings", original.Result
		if err = s.Finish(r); err != nil {
			t.Fatal(err)
		}
	}
	s, err = Open(s.path)
	if err != nil {
		t.Fatal(err)
	}
	groups := s.Calibration()
	if len(groups) != 6 {
		t.Fatalf("merged incomparable provenance: %+v", groups)
	}
	legacyCount, defaults := 0, 0
	for _, g := range groups {
		if g.Provenance == "legacy_unknown" {
			legacyCount++
			if g.ModelSelection != "unknown" || g.Accepted != 1 {
				t.Fatal("legacy data gained model provenance")
			}
		}
		if g.ModelSelection == "agent_default" {
			defaults++
		}
	}
	if legacyCount != 1 || defaults != 1 {
		t.Fatal("default and unknown models were conflated")
	}
	raw, _ := os.ReadFile(s.path)
	var saved disk
	if err = json.Unmarshal(raw, &saved); err != nil || saved.Version != 2 {
		t.Fatal("old writers can erase new provenance")
	}
}

func TestInvocationRefusesChangedReservationAndCorruptSnapshot(t *testing.T) {
	s, p, old, _ := findingFixture(t)
	in := old.Inputs
	in.Invocation = NewInvocation("codex", "model-a", "host-read-only", p.Instruction())
	r, err := s.Begin(p, in, false)
	if err != nil {
		t.Fatal(err)
	}
	r.State, r.Result = "findings", old.Result
	r.Inputs.Invocation = NewInvocation("codex", "model-b", "host-read-only", p.Instruction())
	if err = s.Finish(r); err == nil {
		t.Fatal("completion changed reserved model")
	}
	r.Inputs.Invocation = in.Invocation
	if err = s.Finish(r); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(s.path)
	var saved disk
	if err = json.Unmarshal(raw, &saved); err != nil {
		t.Fatal(err)
	}
	corrupt := saved.Records[r.Key]
	corrupt.Inputs.Invocation.Instruction += " altered"
	saved.Records[r.Key] = corrupt
	raw, _ = json.Marshal(saved)
	if err = os.WriteFile(s.path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = Open(s.path); err == nil {
		t.Fatal("corrupt prompt digest accepted")
	}
}
