package reviewprofile

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func findingFixture(t *testing.T) (*Store, Profile, Record, FindingReport) {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "profiles.json"))
	if err != nil {
		t.Fatal(err)
	}
	p, in := fixtures()
	in.Head = strings.Repeat("a", 40)
	r, err := s.Begin(p, in, false)
	if err != nil {
		t.Fatal(err)
	}
	r.State = "findings"
	r.Result = Result{Complete: true, Evidence: []Evidence{{Criterion: "No lost updates", Observation: "Concurrent save can overwrite state"}}, Findings: []Finding{{Requirement: "Preserve writes", Location: "store.go:12", Reproduction: "Run parallel saves", Evidence: "Lost write in concurrent test", Severity: "major", Confidence: "verified", Fix: "Hold lock across read and write"}}}
	if err = s.Finish(r); err != nil {
		t.Fatal(err)
	}
	return s, p, r, s.Findings()[0]
}

func TestFindingLifecyclePreservesIndependentEvidenceAndVerdict(t *testing.T) {
	s, p, original, f := findingFixture(t)
	decide := func(state string, extra func(*FindingDecision)) {
		t.Helper()
		a := FindingDecision{ID: f.ID, Revision: f.Revision, State: state, Evidence: "Reproduced and inspected the relevant test evidence"}
		if extra != nil {
			extra(&a)
		}
		var err error
		f, err = s.Decide(a)
		if err != nil {
			t.Fatal(err)
		}
		again, err := s.Decide(a)
		if err != nil || again.Revision != f.Revision {
			t.Fatalf("retry not idempotent: %+v %v", again, err)
		}
	}
	decide("accepted", nil)
	head := strings.Repeat("b", 40)
	decide("fixed", func(a *FindingDecision) { a.FixHead = head })
	in := original.Inputs
	in.Head = head
	proof, err := s.Begin(p, in, false)
	if err != nil {
		t.Fatal(err)
	}
	proof.State = "pass"
	proof.Result = Result{Complete: true, Evidence: []Evidence{{Criterion: "No lost updates", Observation: "The lock covers read, copy, update and atomic write"}}}
	if err = s.Finish(proof); err != nil {
		t.Fatal(err)
	}
	decide("verified", func(a *FindingDecision) { a.FixHead = head; a.VerificationKey = proof.Key })
	old, _ := s.Get(original.Key)
	if old.State != "findings" {
		t.Fatal("decision bypassed original review gate")
	}
	// A later retry/invalidation cannot erase the exact proof the operator used.
	if err = s.Invalidate(proof.Key, "upstream moved"); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(s.path)
	if err != nil {
		t.Fatal(err)
	}
	got := reopened.Findings()[0]
	if got.State != "verified" || got.Events[2].Verification.State != "pass" {
		t.Fatalf("lost historical proof: %+v", got)
	}
	got.Events[2].Verification.Result.Evidence[0].Observation = "tampered"
	if reopened.Findings()[0].Events[2].Verification.Result.Evidence[0].Observation == "tampered" {
		t.Fatal("aliased canonical evidence")
	}
	report := reopened.Calibration()
	if len(report) != 1 || report[0].Verified != 1 || report[0].Findings != 1 {
		t.Fatalf("calibration: %+v", report)
	}
}

func TestFindingRejectsStaleSelfCertifiedAndUnrelatedDecisions(t *testing.T) {
	s, p, r, f := findingFixture(t)
	a := FindingDecision{ID: f.ID, Revision: 1, State: "verified", Evidence: "Worker says all tests pass"}
	if _, err := s.Decide(a); err == nil {
		t.Fatal("self-certified finding")
	}
	a.State = "accepted"
	if _, err := s.Decide(a); err != nil {
		t.Fatal(err)
	}
	a.State = "dismissed"
	if _, err := s.Decide(a); err == nil {
		t.Fatal("stale decision accepted")
	}
	a.Revision = 2
	a.State = "fixed"
	a.FixHead = strings.Repeat("b", 40)
	if _, err := s.Decide(a); err != nil {
		t.Fatal(err)
	}
	for _, wrong := range []string{"repo", "profile", "rubric", "contract", "head", "findings", "incomplete"} {
		t.Run(wrong, func(t *testing.T) {
			in := r.Inputs
			in.Head = a.FixHead
			p2 := p
			switch wrong {
			case "repo":
				in.Repo = "other/repo"
			case "profile":
				p2.ID = "security"
			case "rubric":
				p2.Version = "2"
			case "contract":
				in.Contract = "different"
			case "head":
				in.Head = strings.Repeat("c", 40)
			}
			proof := Record{Key: "proof-" + wrong, Profile: p2.ID, Provider: p2.Provider, Version: p2.Version, Inputs: in, State: "pass", Attempt: 1, UpdatedAt: time.Now(), Result: Result{Complete: true, Evidence: []Evidence{{Criterion: "c", Observation: "o"}}}}
			if wrong == "findings" {
				proof.Result.Findings = r.Result.Findings
			}
			if wrong == "incomplete" {
				proof.Result.Complete = false
			}
			s.records[proof.Key] = proof
			request := a
			request.Revision = 3
			request.State = "verified"
			request.VerificationKey = proof.Key
			if _, err := s.Decide(request); err == nil {
				t.Fatalf("accepted %s verification", wrong)
			}
		})
	}
	if s.Findings()[0].State != "fixed" {
		t.Fatal("failed decision mutated history")
	}
}

func TestFindingRetryAndLegacyImport(t *testing.T) {
	s, p, r, f := findingFixture(t)
	decision := FindingDecision{ID: f.ID, Revision: 1, State: "disputed", Evidence: "Reproduction uses an unsupported configuration"}
	if _, err := s.Decide(decision); err != nil {
		t.Fatal(err)
	}
	retried, err := s.Begin(p, r.Inputs, true)
	if err != nil {
		t.Fatal(err)
	}
	retried.State = "findings"
	retried.Result = r.Result
	if err = s.Finish(retried); err != nil {
		t.Fatal(err)
	}
	if len(s.Findings()) != 2 {
		t.Fatal("retry erased old findings")
	}
	counts := s.Calibration()[0]
	if counts.Reported != 1 || counts.Disputed != 1 || counts.BlockingDisputes != 1 {
		t.Fatalf("counts: %+v", counts)
	}
	// Legacy v1 data remains readable and gets reported claims, never acceptance.
	legacy := filepath.Join(t.TempDir(), "legacy.json")
	r.UpdatedAt = time.Now().UTC()
	body, _ := json.Marshal(disk{Version: 1, Records: map[string]Record{r.Key: r}})
	if err = os.WriteFile(legacy, body, 0600); err != nil {
		t.Fatal(err)
	}
	migrated, err := Open(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if len(migrated.Findings()) != 1 || migrated.Findings()[0].State != "reported" {
		t.Fatal("legacy import invented a decision")
	}
}

func TestFindingRejectsCorruptHistoryAndSecretEvidence(t *testing.T) {
	s, _, _, f := findingFixture(t)
	a := FindingDecision{ID: f.ID, Revision: 1, State: "accepted", Evidence: "api_key=abcdefghi123456789"}
	if _, err := s.Decide(a); err == nil {
		t.Fatal("stored suspected credential")
	}
	a.Evidence = "Observed the failing concurrent test"
	if _, err := s.Decide(a); err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(s.path)
	var data disk
	if err := json.Unmarshal(body, &data); err != nil {
		t.Fatal(err)
	}
	corrupt := data.Findings[f.ID]
	corrupt.Events[0].State = "verified"
	corrupt.State = "verified"
	data.Findings[f.ID] = corrupt
	body, _ = json.Marshal(data)
	if err := os.WriteFile(s.path, body, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(s.path); err == nil {
		t.Fatal("corrupt verified history loaded")
	}
}
