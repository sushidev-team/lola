package knowledge

import (
	"fmt"
	"slices"
	"strings"
	"time"
)

// ReviewProof links a derived lesson to an immutable source version and the
// human-verified finding's independent review receipt. Workers cannot author it.
type ReviewProof struct {
	ReplacedByHuman bool      `json:"replaced_by_human,omitempty"`
	Source          Reference `json:"source"`
	FindingID       string    `json:"finding_id"`
	FindingRevision uint64    `json:"finding_revision"`
	VerificationKey string    `json:"verification_key"`
	FixCommit       string    `json:"fix_commit"`
}

func validateReviewProof(p *ReviewProof) error {
	if p == nil {
		return nil
	}
	if p.Source.ID == "" || p.Source.Version < 1 || p.FindingID == "" || len(p.FindingID) > 100 || p.FindingRevision < 2 || p.VerificationKey == "" || len(p.VerificationKey) > 240 || !codeCommit.MatchString(p.FixCommit) {
		return fmt.Errorf("incomplete verified review provenance")
	}
	return nil
}

// PromoteReview creates project guidance only from a still-current candidate.
// The daemon checks delivery, independent evidence and code equality first.
// Derived identity makes retries safe, including after a human disputes or
// maintenance demotes the new lesson. Promotion never overrides either decision.
func (s *Store) PromoteReview(proof ReviewProof, paths []string, code []CodeReference) (Lesson, error) {
	if proof.ReplacedByHuman {
		return Lesson{}, fmt.Errorf("automatic promotion cannot claim human acceptance")
	}
	if err := validateReviewProof(&proof); err != nil {
		return Lesson{}, err
	}
	if err := validateCode(code); err != nil {
		return Lesson{}, err
	}
	if len(code) == 0 {
		return Lesson{}, fmt.Errorf("promotion requires observed accepted code")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	next := s.copy()
	origin, ok := next.Lessons[proof.Source.ID]
	if !ok || origin.Version != proof.Source.Version || origin.Status != Candidate || origin.Policy || origin.ReviewReason != "" || origin.Source.Kind != "review" {
		return Lesson{}, fmt.Errorf("promotion source is no longer an uncurated review candidate")
	}
	p := origin.Proposal
	p.Paths = slices.Clone(paths)
	scope := Scope{Project: origin.Scope.Project, Paths: slices.Clone(paths)}
	source := Source{Kind: "verification", ID: fmt.Sprintf("finding:%s:%d", proof.FindingID, proof.FindingRevision), Session: origin.Source.Session, Commit: proof.FixCommit}
	if err := Validate(scope, p, source); err != nil {
		return Lesson{}, err
	}
	id := identity(scope, p, source)
	if old, exists := next.Lessons[id]; exists {
		return old, nil
	}
	if len(next.Lessons) >= 10000 {
		return Lesson{}, fmt.Errorf("knowledge capacity reached")
	}
	for _, other := range next.Lessons {
		if other.Status == Active && other.Scope.Project == scope.Project && other.Scope.GoalID == "" && other.Topic == p.Topic && promotionPathsOverlap(other.Paths, p.Paths) && other.Summary != p.Summary {
			return Lesson{}, fmt.Errorf("promotion conflicts with active guidance; human curation required")
		}
	}
	now := time.Now().UTC()
	l := Lesson{ID: id, Version: 1, Scope: scope, Proposal: p, Source: source, Status: Active, CreatedAt: now, VerifiedAt: now, Verification: "Independent fix review " + proof.VerificationKey + " matches accepted code", Code: slices.Clone(code), ReviewProof: &proof}
	next.Lessons[id] = l
	if err := s.commit(next); err != nil {
		return Lesson{}, err
	}
	return s.copy().Lessons[id], nil
}

// An automatic derivation cannot infer that a narrower contradictory lesson is
// an intentional exception to existing project/directory guidance.
func promotionPathsOverlap(a, b []string) bool {
	if len(a) == 0 || len(b) == 0 {
		return true
	}
	for _, left := range a {
		for _, right := range b {
			if left == right || left == "." || right == "." || strings.HasPrefix(left, right+"/") || strings.HasPrefix(right, left+"/") {
				return true
			}
		}
	}
	return false
}
