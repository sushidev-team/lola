package knowledge

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

func captureImprovement(t *testing.T, s *Store, scope Scope, p Proposal, session, kind string) Lesson {
	t.Helper()
	l, err := s.Capture(scope, p, Source{Kind: kind, ID: session + "-" + kind, Session: session}, false)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func TestImprovementRecurrenceUsesDistinctSessionsAndPreservesScope(t *testing.T) {
	s, p := fixture(t)
	scope := Scope{Project: "nori"}
	one := captureImprovement(t, s, scope, p, "one", "worker")
	captureImprovement(t, s, scope, p, "one", "review")
	captureImprovement(t, s, scope, p, "one", "worker") // repeated Stop delivery
	if got := s.Improvements(scope, ""); got.Total != 0 {
		t.Fatal("one session manufactured recurrence")
	}
	captureImprovement(t, s, Scope{Project: "nori", GoalID: "experiment"}, p, "two", "worker")
	captureImprovement(t, s, Scope{Project: "other"}, p, "two", "worker")
	if s.Improvements(scope, "").Total != 0 {
		t.Fatal("different scope inflated recurrence")
	}
	p.Summary = "  " + p.Summary + "\n" // harmless formatting is grouped
	two := captureImprovement(t, s, scope, p, "two", "worker")
	report := s.Improvements(scope, "")
	if report.Total != 1 || report.Proposals[0].Occurrences != 2 || report.Proposals[0].Trigger != "recurring" {
		t.Fatalf("missing recurrence: %+v", report)
	}
	proposal := report.Proposals[0]
	if proposal.Suggestions[0].Kind != "regression_test" || !strings.Contains(proposal.Suggestions[0].Change, strings.TrimSpace(p.Summary)) {
		t.Fatal("proposal lacks concrete problem")
	}
	if len(Select(s.List(), Query{Project: "nori"}, time.Now()).Lessons) != 0 {
		t.Fatal("repetition promoted candidate guidance")
	}
	if err := s.Dispute(two.ID, two.Version, "Reproduction contradicts this claim"); err != nil {
		t.Fatal(err)
	}
	if s.Improvements(scope, "").Total != 0 {
		t.Fatal("disputed evidence retained recurrence")
	}
	if old, ok := s.Version(Reference{ID: one.ID, Version: one.Version}); !ok || old.Status != Candidate {
		t.Fatal("proposal mutated lesson")
	}
}

func TestImprovementExplicitIdeasPolicyAndValidation(t *testing.T) {
	for _, kind := range []string{"regression_test", "lint", "skill"} {
		t.Run(kind, func(t *testing.T) {
			s, p := fixture(t)
			p.Prevention = &PreventionIdea{Kind: kind, Change: "Check persistence before dispatch in the failure case.", Validation: "Reproduce disk-full failure, then check the corrected behavior."}
			captureImprovement(t, s, Scope{Project: "nori"}, p, "one", "worker")
			// Capture owns a copy of caller-supplied advice.
			p.Prevention.Change = "changed after capture"
			got := s.Improvements(Scope{Project: "nori"}, "")
			if got.Total != 1 || got.Proposals[0].Trigger != "explicit" || got.Proposals[0].Suggestions[0].Kind != kind || got.Proposals[0].Suggestions[0].Change == p.Prevention.Change {
				t.Fatalf("idea: %+v", got)
			}
			files := ExportImprovement(got.Proposals[0])
			if len(files) != 2 || !strings.Contains(files["IMPROVEMENT.md"], "human-controlled") || !strings.Contains(files["IMPROVEMENT.md"], "independent proof") {
				t.Fatal("export lacks review boundary")
			}
			if _, ok := files["AGENTS.md"]; ok {
				t.Fatal("installed a policy")
			}
		})
	}
	s, p := fixture(t)
	p.Policy = true
	l := captureImprovement(t, s, Scope{Project: "nori"}, p, "policy-request", "worker")
	report := s.Improvements(Scope{Project: "nori"}, "")
	if report.Total != 1 || report.Proposals[0].Trigger != "policy" || len(report.Proposals[0].Suggestions) != 0 {
		t.Fatal("policy silently became an executable prevention")
	}
	if _, err := s.Accept(l.ID, l.Version, "claimed policy approval", nil, time.Time{}); err == nil {
		t.Fatal("policy activated")
	}
	for _, idea := range []*PreventionIdea{
		{Kind: "shell", Change: "Run shell command", Validation: "Claim it works"},
		{Kind: "lint", Change: "short", Validation: "Run the linter"},
		{Kind: "skill", Change: "Set api_key=abcdefgh123456789", Validation: "Try the workflow"},
		{Kind: "regression_test", Change: "Run the suite\x00now", Validation: "Run the check"},
	} {
		p.Prevention = idea
		if _, err := s.Capture(Scope{Project: "nori"}, p, Source{Kind: "worker", ID: "invalid"}, false); err == nil {
			t.Fatalf("accepted invalid idea: %+v", idea)
		}
	}
}

func TestImprovementDecisionsPinSourcesAndSurviveRestart(t *testing.T) {
	s, p := fixture(t)
	scope := Scope{Project: "nori", GoalID: "experiment"}
	captureImprovement(t, s, scope, p, "one", "worker")
	captureImprovement(t, s, scope, p, "two", "worker")
	proposal := s.Improvements(scope, "").Proposals[0]
	if _, err := s.DecideImprovement(scope, proposal.ID, "stale", "tracked", "Tracked in normal issue ENG-42"); err == nil {
		t.Fatal("stale decision accepted")
	}
	if _, err := s.DecideImprovement(Scope{Project: "nori"}, proposal.ID, proposal.Revision, "tracked", "Tracked in normal issue ENG-42"); err == nil {
		t.Fatal("goal proposal escaped its scope")
	}
	for range 2 {
		got, err := s.DecideImprovement(scope, proposal.ID, proposal.Revision, "tracked", "Tracked in normal issue ENG-42")
		if err != nil || got.State != "tracked" || len(got.Decisions) != 1 {
			t.Fatalf("decision/retry: %+v %v", got, err)
		}
	}
	s, err := Open(s.path)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Improvement(scope, proposal.ID); got.State != "tracked" {
		t.Fatal("restart lost triage")
	}
	got, err := s.DecideImprovement(scope, proposal.ID, proposal.Revision, "reopened", "The task was closed without a fix")
	if err != nil || got.State != "proposed" || len(got.Decisions) != 2 {
		t.Fatalf("reopen: %+v %v", got, err)
	}
	captureImprovement(t, s, scope, p, "three", "worker")
	got, err = s.Improvement(scope, proposal.ID)
	if err != nil || got.State != "proposed" || got.Revision == proposal.Revision || len(got.Decisions) != 2 {
		t.Fatalf("new evidence: %+v %v", got, err)
	}
	if _, err = s.DecideImprovement(scope, proposal.ID, proposal.Revision, "dismissed", "Old evidence should not authorize this"); err == nil {
		t.Fatal("old revision affected new evidence")
	}
	if len(Select(s.List(), Query{Project: "nori", GoalID: "experiment"}, time.Now()).Lessons) != 0 {
		t.Fatal("triage promoted source lessons")
	}
}

func TestImprovementBoundsAndExcludedSources(t *testing.T) {
	s, p := fixture(t)
	scope := Scope{Project: "nori"}
	for i := range 23 {
		p.Prevention = &PreventionIdea{Kind: "lint", Change: fmt.Sprintf("Consider a scoped lint check number %d", i), Validation: "Demonstrate rejected and permitted source examples."}
		captureImprovement(t, s, scope, p, fmt.Sprint(i), "worker")
	}
	proposal := s.Improvements(scope, "").Proposals[0]
	if proposal.Occurrences != 23 || len(proposal.Sources) != 20 || proposal.OmittedSources != 3 || len(proposal.Suggestions) != 5 || proposal.OmittedSuggestions != 18 {
		t.Fatalf("unbounded preview: %+v", proposal)
	}
	captureImprovement(t, s, scope, p, "additional", "worker")
	if got := s.Improvements(scope, "").Proposals[0]; got.Revision == proposal.Revision {
		t.Fatal("omitted source did not affect revision")
	}
	s, p = fixture(t)
	l := captureImprovement(t, s, scope, p, "one", "worker")
	l, err := s.Accept(l.ID, l.Version, "Verified before expiry", nil, time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	captureImprovement(t, s, scope, p, "two", "worker")
	if s.Improvements(scope, "").Total != 0 {
		t.Fatal("expired evidence retained")
	}
	if err = s.ReviewCode(l.ID, l.Version, "lesson expired"); err != nil {
		t.Fatal(err)
	}
	if s.Improvements(scope, "").Total != 0 {
		t.Fatal("maintenance candidate became fresh evidence")
	}
}

func TestImprovementPaginationAndDeterminism(t *testing.T) {
	s, p := fixture(t)
	scope := Scope{Project: "nori"}
	p.Prevention = &PreventionIdea{Kind: "skill", Change: "Document the repeatable recovery workflow.", Validation: "Compare representative positive and negative task outcomes."}
	for i := range 53 {
		p.Topic = fmt.Sprintf("Workflow %d", i)
		captureImprovement(t, s, scope, p, "one", "worker")
	}
	first := s.Improvements(scope, "")
	second := s.Improvements(scope, first.NextAfter)
	if first.Total != 53 || len(first.Proposals) != 50 || first.NextAfter == "" || len(second.Proposals) != 3 || second.NextAfter != "" {
		t.Fatal("pagination lost proposals")
	}
	s, err := Open(s.path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, s.Improvements(scope, "")) {
		t.Fatal("restart changed proposals without new evidence")
	}
}

func TestImprovementLegacySnapshotAndCorruptDecision(t *testing.T) {
	s, p := fixture(t)
	scope := Scope{Project: "nori"}
	captureImprovement(t, s, scope, p, "one", "worker")
	raw, err := os.ReadFile(s.path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "improvement_decisions") {
		t.Fatal("empty new field unnecessarily persisted")
	}
	s, err = Open(s.path)
	if err != nil {
		t.Fatal("legacy snapshot failed", err)
	}
	captureImprovement(t, s, scope, p, "two", "worker")
	proposal := s.Improvements(scope, "").Proposals[0]
	if _, err = s.DecideImprovement(scope, proposal.ID, proposal.Revision, "dismissed", "Cannot reproduce the reported failure"); err != nil {
		t.Fatal(err)
	}
	var snap snapshot
	raw, _ = os.ReadFile(s.path)
	if err = json.Unmarshal(raw, &snap); err != nil {
		t.Fatal(err)
	}
	history := snap.ImprovementDecisions[proposal.ID]
	history[0].Decision = "verified"
	snap.ImprovementDecisions[proposal.ID] = history
	raw, _ = json.Marshal(snap)
	if err = os.WriteFile(s.path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = Open(s.path); err == nil {
		t.Fatal("corrupt triage authority accepted")
	}
}

func TestImprovementRepeatedOperatorCaptureDoesNotInventRecurrence(t *testing.T) {
	s, p := fixture(t)
	scope := Scope{Project: "nori"}
	for _, source := range []Source{{Kind: "operator", ID: "request-one"}, {Kind: "operator", ID: "request-two"}, {Kind: "correction", ID: "request-three"}} {
		if _, err := s.Capture(scope, p, source, false); err != nil {
			t.Fatal(err)
		}
	}
	if s.Improvements(scope, "").Total != 0 {
		t.Fatal("CLI retry counted as a separate observed failure")
	}
	captureImprovement(t, s, scope, p, "worker-session", "worker")
	report := s.Improvements(scope, "")
	if report.Total != 1 || report.Proposals[0].Occurrences != 2 {
		t.Fatal("attributable worker occurrence was lost")
	}
}

func TestImprovementHistorySurvivesWithdrawnProposal(t *testing.T) {
	s, p := fixture(t)
	scope := Scope{Project: "nori", GoalID: "experiment"}
	one := captureImprovement(t, s, scope, p, "one", "worker")
	captureImprovement(t, s, scope, p, "two", "worker")
	proposal := s.Improvements(scope, "").Proposals[0]
	if _, err := s.DecideImprovement(scope, proposal.ID, proposal.Revision, "dismissed", "Unable to reproduce this report"); err != nil {
		t.Fatal(err)
	}
	if err := s.Dispute(one.ID, one.Version, "Source evidence contradicted the report"); err != nil {
		t.Fatal(err)
	}
	s, err := Open(s.path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Improvement(scope, proposal.ID); err == nil {
		t.Fatal("withdrawn proposal still current")
	}
	history := s.ImprovementHistory(scope, proposal.ID)
	if len(history) != 1 || len(history[0].SourceVersions) != 2 || history[0].Revision != proposal.Revision {
		t.Fatal("lost complete reviewed source receipt")
	}
	for _, ref := range history[0].SourceVersions {
		if _, ok := s.Version(ref); !ok {
			t.Fatal("source version was lost")
		}
	}
	if len(s.ImprovementHistory(Scope{Project: "nori"}, proposal.ID)) != 0 {
		t.Fatal("goal receipt escaped scope")
	}
}
