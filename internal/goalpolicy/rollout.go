package goalpolicy

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Rollout is part of the immutable execution authorization. It describes the
// compatibility required between manual merges, not permission to merge or
// deploy. Include unchanged members in MergeOrder so the contract stays complete
// when a later task or target refresh changes which members need PRs.
type Rollout struct {
	Compatibility []string `json:"compatibility"`
	MergeOrder    []string `json:"merge_order"`
}

func (p Policy) validateRollout(members map[string]bool) error {
	if p.Rollout == nil {
		return nil // retain legacy policy bytes, digests and active executions
	}
	r := p.Rollout
	if !p.PublishPR {
		return errors.New("a rollout contract requires final PR publication")
	}
	if len(r.Compatibility) < 1 || len(r.Compatibility) > 20 {
		return errors.New("rollout requires 1–20 explicit compatibility requirements")
	}
	for _, requirement := range r.Compatibility {
		if strings.TrimSpace(requirement) == "" || len(requirement) > 500 || !utf8.ValidString(requirement) || strings.ContainsFunc(requirement, unicode.IsControl) {
			return errors.New("each compatibility requirement must be nonblank, single-line text of at most 500 bytes")
		}
	}
	if len(r.MergeOrder) != len(members) {
		return errors.New("human merge order must include every repository member exactly once")
	}
	seen := map[string]bool{}
	for _, id := range r.MergeOrder {
		if !members[id] || seen[id] {
			return errors.New("human merge order must include every repository member exactly once")
		}
		seen[id] = true
	}
	return nil
}

// ValidateAuthorization adds requirements for NEW executions without changing
// the validity or digest of previously accepted policies.
func (p Policy) ValidateAuthorization() error {
	if err := p.Validate(); err != nil {
		return err
	}
	if p.PublishPR && len(p.Members) > 1 && p.Rollout == nil {
		return errors.New("stack publication requires reviewed compatibility requirements and a human merge order in policy.rollout")
	}
	return nil
}

// RolloutContract is shared context for workers, specialists and delivery. The
// JSON preserves the exact accepted wording and order; none of it is executed.
func (p Policy) RolloutContract() string {
	if p.Rollout == nil {
		return ""
	}
	b, _ := json.MarshalIndent(p.Rollout, "", "  ")
	return fmt.Sprintf("\n## Reviewed rollout contract\n\nPreserve these compatibility requirements throughout the human merge sequence, including intermediate repository combinations. Unchanged members need no PR. Merges and deployment remain human decisions; this contract grants no execution permission.\n\n    %s\n", strings.ReplaceAll(string(b), "\n", "\n    "))
}
