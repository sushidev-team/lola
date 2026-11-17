package knowledge

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestReviewPromotionFormatPreservesLegacyLessonsAndDeliveries(t *testing.T) {
	s, p := fixture(t)
	l, err := s.Capture(Scope{Project: "p"}, p, Source{Kind: "correction", ID: "human"}, true)
	if err != nil {
		t.Fatal(err)
	}
	refs := []Reference{{ID: l.ID, Version: l.Version}}
	if err = s.RecordDelivery("worker", refs); err != nil {
		t.Fatal(err)
	}
	if err = s.Dispute(l.ID, l.Version, "A later human correction requires a new decision"); err != nil {
		t.Fatal(err)
	}
	legacy := s.copy()
	legacy.Version = 1
	raw, _ := json.Marshal(legacy)
	if err = os.WriteFile(s.path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	s, err = Open(s.path)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.RecordDelivery("worker2", refs); err != nil {
		t.Fatal(err)
	}
	legacy.Deliveries["worker2"] = refs
	loaded, err := Open(s.path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.data.Version != 2 || !reflect.DeepEqual(legacy.Lessons, loaded.data.Lessons) || !reflect.DeepEqual(legacy.History, loaded.data.History) || !reflect.DeepEqual(legacy.Deliveries, loaded.data.Deliveries) {
		t.Fatal("format upgrade lost historical evidence or allowed old writers")
	}
}

func TestReviewPromotionPreservesSourceAndCannotOverrideCuration(t *testing.T) {
	s, p := fixture(t)
	origin, err := s.Capture(Scope{Project: "p", GoalID: "g"}, p, Source{Kind: "review", ID: "review:0"}, false)
	if err != nil {
		t.Fatal(err)
	}
	proof := ReviewProof{Source: Reference{ID: origin.ID, Version: origin.Version}, FindingID: "finding", FindingRevision: 4, VerificationKey: "backend:proof", FixCommit: strings.Repeat("a", 40)}
	code := []CodeReference{{CodeTarget: CodeTarget{Member: "p", Path: "src"}, Source: "/repo", Branch: "main", Commit: strings.Repeat("b", 40), Digest: strings.Repeat("c", 64)}}
	derived, err := s.PromoteReview(proof, []string{"src"}, code)
	if err != nil || derived.Status != Active || derived.Scope.GoalID != "" || derived.ReviewProof.Source.ID != origin.ID {
		t.Fatalf("promotion: %+v %v", derived, err)
	}
	if retained, ok := s.Version(proof.Source); !ok || retained.Scope.GoalID != "g" || retained.Status != Candidate {
		t.Fatal("source history changed")
	}
	if err = s.Dispute(derived.ID, derived.Version, "Human found a counterexample to the generalization"); err != nil {
		t.Fatal(err)
	}
	replay, err := s.PromoteReview(proof, []string{"src"}, code)
	if err != nil || replay.Status != Disputed || len(s.List()) != 2 {
		t.Fatal("retry overrode curation", err)
	}
	s, err = Open(s.path)
	if err != nil {
		t.Fatal(err)
	}
	if len(Select(s.List(), Query{Project: "p", Paths: []string{"src/file"}}, time.Now()).Lessons) != 0 {
		t.Fatal("disputed promotion still selected")
	}
}

func TestReviewPromotionRefusesPolicyConflictsAndStaleSource(t *testing.T) {
	for _, mode := range []string{"policy", "conflict", "overlapping-conflict", "stale", "worker"} {
		t.Run(mode, func(t *testing.T) {
			s, p := fixture(t)
			if mode == "policy" {
				p.Policy = true
			}
			kind := "review"
			if mode == "worker" {
				kind = "worker"
			}
			l, err := s.Capture(Scope{Project: "p"}, p, Source{Kind: kind, ID: "r"}, false)
			if err != nil {
				t.Fatal(err)
			}
			proof := ReviewProof{Source: Reference{ID: l.ID, Version: l.Version}, FindingID: "f", FindingRevision: 4, VerificationKey: "proof", FixCommit: strings.Repeat("a", 40)}
			code := []CodeReference{{CodeTarget: CodeTarget{Member: "p", Path: "."}, Source: "/repo", Branch: "main", Commit: strings.Repeat("a", 40), Digest: strings.Repeat("b", 64)}}
			if mode == "conflict" || mode == "overlapping-conflict" {
				p.Summary = "An incompatible active lesson controls this topic."
				if mode == "overlapping-conflict" {
					p.Paths = []string{"src"}
				}
				if _, err = s.Capture(Scope{Project: "p"}, p, Source{Kind: "correction", ID: "h"}, true); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "stale" {
				if err = s.Dispute(l.ID, l.Version, "The source candidate is disputed by the operator"); err != nil {
					t.Fatal(err)
				}
			}
			var paths []string
			if mode == "overlapping-conflict" {
				paths = []string{"src/store.go"}
			}
			if _, err = s.PromoteReview(proof, paths, code); err == nil {
				t.Fatal("unsafe promotion accepted")
			}
		})
	}
}
