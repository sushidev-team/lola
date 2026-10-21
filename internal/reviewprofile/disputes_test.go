package reviewprofile

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func ownedFinding(t *testing.T, owner ReviewOwner) (*Store, Record, FindingReport) {
	t.Helper()
	s, p, old, _ := findingFixture(t)
	in := old.Inputs
	in.Owner = &owner
	r, err := s.Begin(p, in, false)
	if err != nil {
		t.Fatal(err)
	}
	r.State, r.Result = "findings", old.Result
	if err = s.Finish(r); err != nil {
		t.Fatal(err)
	}
	for _, f := range s.Findings() {
		if f.ReviewKey == r.Key {
			return s, r, f
		}
	}
	t.Fatal("finding missing")
	return nil, Record{}, FindingReport{}
}

func TestWorkerDisputeIsScopedAttributedAndCannotReopenHumanDecision(t *testing.T) {
	s, r, f := ownedFinding(t, ReviewOwner{Project: "p", Session: "s"})
	actor := DisputeActor{ReviewOwner: ReviewOwner{Project: "p", Session: "s"}}
	a := WorkerDispute{ID: f.ID, Revision: f.Revision, Evidence: "The concurrent reproduction bypasses the mandatory transaction lock; see test ConcurrentSave."}
	for _, wrong := range []DisputeActor{{ReviewOwner: ReviewOwner{Project: "other", Session: "s"}}, {ReviewOwner: ReviewOwner{Project: "p", Session: "other"}}, {ReviewOwner: ReviewOwner{Project: "p", Session: "s", Goal: "g", Task: "t"}, AllowedTasks: []string{"t"}}} {
		if _, err := s.DisputeWorker(wrong, a); err == nil {
			t.Fatal("foreign worker disputed finding")
		}
	}
	disputed, err := s.DisputeWorker(actor, a)
	if err != nil || !BlockingDispute(disputed) || disputed.Events[0].Worker.Session != "s" {
		t.Fatalf("dispute: %+v %v", disputed, err)
	}
	original, _ := s.Get(r.Key)
	if original.State != "findings" {
		t.Fatal("worker changed independent verdict")
	}
	accepted, err := s.Decide(FindingDecision{ID: f.ID, Revision: disputed.Revision, State: "accepted", Evidence: "Human reproduced the failure using the public transaction API"})
	if err != nil {
		t.Fatal(err)
	}
	if got, err := s.DisputeWorker(actor, a); err != nil || got.Revision != accepted.Revision || got.State != "accepted" {
		t.Fatal("lost-response replay reopened dispute", err)
	}
	a.Revision = accepted.Revision
	a.Evidence = "Different worker argument after the human decision."
	if _, err = s.DisputeWorker(actor, a); err == nil {
		t.Fatal("worker started an endless debate")
	}
	s, err = Open(s.path)
	if err != nil {
		t.Fatal(err)
	}
	var got FindingReport
	for _, report := range s.Findings() {
		if report.ID == f.ID {
			got = report
		}
	}
	if got.State != "accepted" || len(got.Events) != 2 || got.Events[0].Worker == nil || got.Events[1].Worker != nil {
		t.Fatal("restart lost attribution")
	}
	groups := s.Calibration()
	workers := 0
	for _, g := range groups {
		workers += g.WorkerDisputes
	}
	if workers != 1 {
		t.Fatal("worker reports conflated with operator assessments")
	}
}

func TestWorkerDisputeGoalTaskAncestryAndLegacyBoundary(t *testing.T) {
	s, _, f := ownedFinding(t, ReviewOwner{Project: "p", Session: "old-attempt", Goal: "g", Task: "parent"})
	a := WorkerDispute{ID: f.ID, Revision: f.Revision, Evidence: "Counterexample exercises the exact accepted requirement."}
	actor := DisputeActor{ReviewOwner: ReviewOwner{Project: "p", Session: "repair-worker", Goal: "g", Task: "child"}, AllowedTasks: []string{"child"}}
	if _, err := s.DisputeWorker(actor, a); err == nil {
		t.Fatal("unrelated task disputed parent")
	}
	actor.AllowedTasks = append(actor.AllowedTasks, "parent")
	if _, err := s.DisputeWorker(actor, a); err != nil {
		t.Fatal(err)
	}
	legacy, _, _, old := findingFixture(t)
	if _, err := legacy.DisputeWorker(DisputeActor{ReviewOwner: ReviewOwner{Project: "p", Session: "s"}}, WorkerDispute{ID: old.ID, Revision: old.Revision, Evidence: a.Evidence}); err == nil {
		t.Fatal("legacy record invented worker ownership")
	}
}

func TestWorkerDisputeRejectsSecretsAndCorruptAuthority(t *testing.T) {
	s, _, f := ownedFinding(t, ReviewOwner{Project: "p", Session: "s"})
	actor := DisputeActor{ReviewOwner: ReviewOwner{Project: "p", Session: "s"}}
	if _, err := s.DisputeWorker(actor, WorkerDispute{ID: f.ID, Revision: f.Revision, Evidence: "api_key=" + strings.Repeat("a", 20)}); err == nil {
		t.Fatal("stored secret")
	}
	if _, err := s.DisputeWorker(actor, WorkerDispute{ID: f.ID, Revision: f.Revision, Evidence: "Valid bounded counterexample evidence"}); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(s.path)
	var data disk
	if err := json.Unmarshal(raw, &data); err != nil {
		t.Fatal(err)
	}
	report := data.Findings[f.ID]
	report.Events[0].Worker.Session = "intruder"
	data.Findings[f.ID] = report
	raw, _ = json.Marshal(data)
	if err := os.WriteFile(s.path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(s.path); err == nil {
		t.Fatal("tampered worker ownership accepted")
	}
}
