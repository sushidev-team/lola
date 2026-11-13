package goalwork

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"time"
)

// ManifestReceipt is the dependency-release boundary for a stack. Individual
// repository receipts can exist while Complete is false; none of them alone
// represents an accepted stack revision. Partial operations retain their exact
// original proof and resume without rerunning already acknowledged updates.
type ManifestReceipt struct {
	Goal         string               `json:"goal"`
	Operation    string               `json:"operation"`
	Base         map[string]string    `json:"base"`
	Manifest     map[string]string    `json:"manifest"`
	Candidates   map[string]Candidate `json:"candidates"`
	Verification Verification         `json:"verification"`
	Integrated   map[string]Receipt   `json:"integrated"`
	Complete     bool                 `json:"complete"`
	CreatedAt    time.Time            `json:"created_at"`
	CompletedAt  time.Time            `json:"completed_at,omitempty"`
}

// AdvanceManifest serializes the member updates and durably records partial
// progress. base must list EVERY authorized member, including unchanged ones.
// The daemon supplies that membership from the accepted contract, never from
// a worker submission. Checks and reviews must describe the full final map.
func (m *Manager) AdvanceManifest(ctx context.Context, goal, operation string, base map[string]string, candidates []Candidate, v Verification, policy string, checks, profiles []string) (ManifestReceipt, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	dir, err := m.path(goal, "integrations")
	if err != nil {
		return ManifestReceipt{}, err
	}
	if !segment.MatchString(operation) || len(base) == 0 || len(base) > 50 || len(candidates) == 0 {
		return ManifestReceipt{}, errors.New("integration requires a bounded base manifest and candidates")
	}
	if v.Goal != goal || v.Operation != operation {
		return ManifestReceipt{}, errors.New("verification belongs to another goal or operation")
	}
	next := maps.Clone(base)
	changes := map[string]Candidate{}
	for member, sha := range base {
		if !segment.MatchString(member) {
			return ManifestReceipt{}, errors.New("invalid manifest member")
		}
		if err := validSHA(sha); err != nil {
			return ManifestReceipt{}, err
		}
	}
	for _, c := range candidates {
		if c.Goal != goal || c.Operation != operation || c.Base != base[c.Member] {
			return ManifestReceipt{}, errors.New("candidate does not match accepted base manifest")
		}
		if _, exists := changes[c.Member]; exists {
			return ManifestReceipt{}, errors.New("duplicate candidate member")
		}
		if err := validSHA(c.Commit); err != nil {
			return ManifestReceipt{}, err
		}
		if err := v.Validate(c, policy, checks, profiles); err != nil {
			return ManifestReceipt{}, err
		}
		changes[c.Member] = c
		next[c.Member] = c.Commit
	}
	if !maps.Equal(next, v.Manifest) {
		return ManifestReceipt{}, errors.New("verification did not inspect this exact repository combination")
	}
	journal := filepath.Join(dir, operation+".json")
	intent, readErr := readJSON[ManifestReceipt](journal)
	if readErr == nil {
		if intent.Goal != goal || intent.Operation != operation || !maps.Equal(intent.Base, base) || !maps.Equal(intent.Manifest, next) || !reflect.DeepEqual(intent.Candidates, changes) || !reflect.DeepEqual(intent.Verification, v) {
			return ManifestReceipt{}, errors.New("manifest operation reused with changed inputs")
		}
	} else if !errors.Is(readErr, os.ErrNotExist) {
		return ManifestReceipt{}, readErr
	}
	// Check every member before the first write, including repositories that
	// this task does not change. An unrelated update still invalidates a
	// cross-repository acceptance result.
	for member, sha := range base {
		tip, err := m.Tip(ctx, goal, member)
		if err != nil {
			return intent, err
		}
		if readErr == nil && intent.Complete {
			r, err := m.Repository(ctx, goal, member)
			if err != nil {
				return intent, err
			}
			if _, err := m.git(ctx, r.GitDir, "merge-base", "--is-ancestor", next[member], tip); err != nil {
				return intent, ErrStale
			}
			continue // Historical completed receipts remain valid after later integrations.
		}
		if tip != sha {
			if readErr != nil || changes[member].Commit != tip {
				return intent, fmt.Errorf("%w: member %s moved", ErrStale, member)
			}
		}
	}
	if readErr == nil && intent.Complete {
		if len(intent.Integrated) != len(changes) {
			return intent, errors.New("completed manifest is missing member receipts")
		}
		for member, c := range changes {
			receipt, ok := intent.Integrated[member]
			if !ok || !receipt.Committed || !reflect.DeepEqual(receipt.Candidate, c) || !reflect.DeepEqual(receipt.Verification, v) {
				return intent, errors.New("completed manifest has inconsistent member evidence")
			}
		}
		return intent, nil
	}
	if readErr != nil {
		intent = ManifestReceipt{Goal: goal, Operation: operation, Base: maps.Clone(base), Manifest: next, Candidates: changes, Verification: v, Integrated: map[string]Receipt{}, CreatedAt: time.Now().UTC()}
		if err := os.MkdirAll(dir, 0700); err != nil {
			return intent, err
		}
		if err := writeJSON(journal, intent); err != nil {
			return intent, err
		}
	}
	if intent.Integrated == nil {
		return intent, errors.New("manifest receipt lost its integration journal")
	}
	members := slices.Sorted(maps.Keys(changes))
	for _, member := range members {
		// Always re-read the durable per-member receipt, including on recovery
		// after a ref advanced but before its manifest entry was acknowledged.
		receipt, err := m.Advance(ctx, changes[member], v, policy, checks, profiles)
		if err != nil {
			return intent, err
		}
		intent.Integrated[member] = receipt
		if err := writeJSON(journal, intent); err != nil {
			return intent, err
		}
	}
	for member, sha := range next {
		if tip, err := m.Tip(ctx, goal, member); err != nil || tip != sha {
			return intent, ErrStale
		}
	}
	intent.Complete = true
	intent.CompletedAt = time.Now().UTC()
	if err := writeJSON(journal, intent); err != nil {
		intent.Complete = false
		return intent, err
	}
	return intent, nil
}
