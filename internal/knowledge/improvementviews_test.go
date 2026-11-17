package knowledge

import (
	"bytes"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestImprovementSourcePagesRetainExactHistory(t *testing.T) {
	s, p := fixture(t)
	scope := Scope{Project: "nori", GoalID: "pilot"}
	var lessons []Lesson
	for i := 0; i < 53; i++ {
		lessons = append(lessons, captureImprovement(t, s, scope, p, fmt.Sprintf("report-%03d", i), "worker"))
	}
	proposal := s.Improvements(scope, "").Proposals[0]
	if len(proposal.Sources) != 20 || proposal.OmittedSources != 33 {
		t.Fatal("fixture does not exceed preview")
	}
	if _, err := s.DecideImprovement(scope, proposal.ID, proposal.Revision, "tracked", "Tracked in a normal task for validation."); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(s.path)
	page, err := s.ImprovementSourcePage(scope, proposal.ID, proposal.Revision, "")
	if err != nil || page.Total != 53 || len(page.References) != 50 || page.NextAfter == "" {
		t.Fatal(page, err)
	}
	next, err := s.ImprovementSourcePage(scope, proposal.ID, proposal.Revision, page.NextAfter)
	if err != nil || len(next.References) != 3 || next.NextAfter != "" {
		t.Fatal(next, err)
	}
	refs := append(append([]Reference{}, page.References...), next.References...)
	for i, r := range refs {
		if i > 0 && r.ID <= refs[i-1].ID {
			t.Fatal("duplicate or unsorted page")
		}
		l, ok := s.Version(r)
		if !ok || l.Status != Candidate || !sameImprovementScope(l.Scope, scope) {
			t.Fatal("missing exact evidence")
		}
	}
	history := s.ImprovementDecisionHistory(scope, proposal.ID)
	if len(history) != 1 || history[0].SourceVersions != nil || history[0].Revision != proposal.Revision {
		t.Fatal(history)
	}
	history[0].Evidence = "mutated"
	page.References[0].Version = 99
	retained := s.ImprovementHistory(scope, proposal.ID)
	if len(retained[0].SourceVersions) != 53 || retained[0].SourceVersions[0].Version != 1 || retained[0].Evidence == "mutated" {
		t.Fatal("read mutated stored receipts")
	}
	after, _ := os.ReadFile(s.path)
	if !bytes.Equal(before, after) {
		t.Fatal("inspection wrote store")
	}
	// A current proposal's revision changes, but its durable decision still pins
	// all prior sources. No old content is inferred from a newer lesson version.
	if err = s.Dispute(lessons[0].ID, lessons[0].Version, "Evidence contradicts this report."); err != nil {
		t.Fatal(err)
	}
	current, _ := s.Improvement(scope, proposal.ID)
	if current.Revision == proposal.Revision {
		t.Fatal("dispute failed to change revision")
	}
	old, err := s.ImprovementSourcePage(scope, proposal.ID, proposal.Revision, "")
	if err != nil || old.Total != 53 {
		t.Fatal(old, err)
	}
	for _, bad := range []Scope{{Project: "other", GoalID: "pilot"}, {Project: "nori"}, {Project: "nori", GoalID: "other"}} {
		if _, err = s.ImprovementSourcePage(bad, proposal.ID, proposal.Revision, ""); err == nil {
			t.Fatal("foreign scope exposed sources")
		}
		if len(s.ImprovementDecisionHistory(bad, proposal.ID)) != 0 {
			t.Fatal("foreign history")
		}
	}
	if _, err = s.ImprovementSourcePage(scope, proposal.ID, "unrecorded", ""); err == nil {
		t.Fatal("fabricated historic revision")
	}
	reopened, err := Open(s.path)
	if err != nil {
		t.Fatal(err)
	}
	again, err := reopened.ImprovementSourcePage(scope, proposal.ID, proposal.Revision, "")
	if err != nil || !reflect.DeepEqual(old, again) {
		t.Fatal("restart lost source receipt", err)
	}
}
func TestImprovementCatalogRetainsWithdrawnDecisions(t *testing.T) {
	s, p := fixture(t)
	scope := Scope{Project: "nori"}
	p.Prevention = &PreventionIdea{Kind: "regression_test", Change: "Add a regression for the exact failure.", Validation: "Prove it fails before the fix and passes afterward."}
	var withdrawn string
	for i := 0; i < 52; i++ {
		q := p
		q.Topic = fmt.Sprintf("Topic %03d", i)
		l := captureImprovement(t, s, scope, q, fmt.Sprintf("source-%03d", i), "worker")
		if i == 0 {
			proposal := s.Improvements(scope, "").Proposals[0]
			withdrawn = proposal.ID
			if _, err := s.DecideImprovement(scope, proposal.ID, proposal.Revision, "dismissed", "Cannot reproduce this reported behavior."); err != nil {
				t.Fatal(err)
			}
			if err := s.Dispute(l.ID, l.Version, "Reproduction contradicts the source."); err != nil {
				t.Fatal(err)
			}
		}
	}
	captureImprovement(t, s, Scope{Project: "nori", GoalID: "separate"}, p, "goal-source", "worker")
	first := s.ImprovementCatalog(scope, "")
	second := s.ImprovementCatalog(scope, first.NextAfter)
	if first.Total != 52 || len(first.Records) != 50 || first.NextAfter == "" || second.Total != 52 || len(second.Records) != 2 || second.NextAfter != "" {
		t.Fatal("bad catalog pagination", first.Total, second.Total)
	}
	all := append(first.Records, second.Records...)
	found := false
	for i, r := range all {
		if i > 0 && r.ID <= all[i-1].ID {
			t.Fatal("duplicate or reordered catalog")
		}
		if r.ID == withdrawn {
			found = true
			if r.Available || r.State != "dismissed" || r.Topic != "Topic 000" || r.DecisionCount != 1 {
				t.Fatal(r)
			}
		} else if !r.Available {
			t.Fatal("active proposal marked withdrawn")
		}
	}
	if !found {
		t.Fatal("withdrawn decision disappeared")
	}
	if _, err := s.Improvement(scope, withdrawn); err == nil {
		t.Fatal("withdrawn proposal remains actionable")
	}
	if len(s.ImprovementCatalog(Scope{Project: "other"}, "").Records) != 0 {
		t.Fatal("cross-project catalog")
	}
	reopened, err := Open(s.path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, reopened.ImprovementCatalog(scope, "")) {
		t.Fatal("restart changed catalog")
	}
	first.Records[0].Scope.Paths = append(first.Records[0].Scope.Paths, "mutated")
	if strings.Contains(fmt.Sprint(s.ImprovementCatalog(scope, "")), "mutated") {
		t.Fatal("catalog aliases store")
	}
}
func TestImprovementUnrecordedRevisionCannotBeReconstructed(t *testing.T) {
	s, p := fixture(t)
	scope := Scope{Project: "nori"}
	captureImprovement(t, s, scope, p, "one", "worker")
	captureImprovement(t, s, scope, p, "two", "worker")
	old := s.Improvements(scope, "").Proposals[0]
	captureImprovement(t, s, scope, p, "three", "worker")
	if _, err := s.ImprovementSourcePage(scope, old.ID, old.Revision, ""); err == nil {
		t.Fatal("unretained old preview became historic evidence")
	}
	current, _ := s.Improvement(scope, old.ID)
	page, err := s.ImprovementSourcePage(scope, current.ID, current.Revision, "")
	if err != nil || page.Total != 3 {
		t.Fatal(page, err)
	}
}
