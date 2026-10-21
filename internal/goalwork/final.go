package goalwork

import (
	"context"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"reflect"
)

// RecordFinal persists whole-goal evidence without changing a Git reference.
// All members must still be at the inspected revision, including unchanged ones.
func (m *Manager) RecordFinal(ctx context.Context, candidates []Candidate, v Verification, policy string, checks, profiles []string) (string, error) {
	if len(candidates) == 0 || len(candidates) != len(v.Manifest) {
		return "", errors.New("final verification must cover every repository")
	}
	seen := map[string]bool{}
	for _, c := range candidates {
		if seen[c.Member] || c.WorkerHead != "" || c.TargetSHA != "" || c.PriorTarget != "" {
			return "", errors.New("final verification requires distinct complete-goal snapshots")
		}
		seen[c.Member] = true
		if err := m.finalBase(ctx, c, v.Targets); err != nil {
			return "", err
		}
		if err := v.Validate(c, policy, checks, profiles); err != nil {
			return "", err
		}
		if err := m.VerifyCandidate(ctx, c); err != nil {
			return "", err
		}
		if tip, err := m.Tip(ctx, c.Goal, c.Member); err != nil || tip != c.Commit {
			return "", ErrStale
		}
	}
	dir, err := m.path(v.Goal, "final", v.Operation)
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, "verification.json")
	if old, err := readJSON[Verification](path); err == nil {
		if !reflect.DeepEqual(old, v) {
			return "", errors.New("final verification identity reused with different evidence")
		}
		return path, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	return path, writeJSON(path, v)
}

// VerifyFinal reads the durable final proof and revalidates the owned source
// snapshots and current tips before publication. It creates no new evidence.
func (m *Manager) VerifyFinal(ctx context.Context, goal, operation, policy string, manifest map[string]string, checks, profiles []string) (Verification, error) {
	return m.VerifyFinalAt(ctx, goal, operation, policy, manifest, nil, checks, profiles)
}

// VerifyFinalAt binds publication to the caller's captured target manifest as
// well as the verified goal heads. A legacy proof cannot authorize a refresh.
func (m *Manager) VerifyFinalAt(ctx context.Context, goal, operation, policy string, manifest, targets map[string]string, checks, profiles []string) (Verification, error) {
	dir, err := m.path(goal, "final", operation)
	if err != nil {
		return Verification{}, err
	}
	v, err := readJSON[Verification](filepath.Join(dir, "verification.json"))
	if err != nil {
		return v, err
	}
	if !maps.Equal(v.Manifest, manifest) || !maps.Equal(v.Targets, targets) || len(manifest) == 0 {
		return v, errors.New("final proof describes another repository combination")
	}
	for member, head := range manifest {
		cdir, err := m.path(goal, "candidates", operation, member)
		if err != nil {
			return v, err
		}
		c, err := readJSON[Candidate](filepath.Join(cdir, "candidate.json"))
		if err != nil {
			return v, err
		}
		if c.Goal != goal || c.Operation != operation || c.Member != member || c.WorkerHead != "" || c.TargetSHA != "" || c.PriorTarget != "" || c.Commit != head {
			return v, errors.New("final snapshot identity changed")
		}
		if err := m.finalBase(ctx, c, targets); err != nil {
			return v, err
		}
		if err := v.Validate(c, policy, checks, profiles); err != nil {
			return v, err
		}
		if err := m.VerifyCandidate(ctx, c); err != nil {
			return v, err
		}
		if tip, err := m.Tip(ctx, goal, member); err != nil || tip != head {
			return v, ErrStale
		}
	}
	return v, nil
}

func (m *Manager) finalBase(ctx context.Context, c Candidate, targets map[string]string) error {
	r, err := m.Repository(ctx, c.Goal, c.Member)
	if err != nil {
		return err
	}
	base := r.Start
	if len(targets) > 0 {
		base = targets[c.Member]
	}
	if c.Base != base {
		return errors.New("final snapshot has a different delivery target")
	}
	if err := m.IsAncestor(ctx, c.Goal, c.Member, r.Start, base); err != nil {
		return errors.New("final target lost original starting history")
	}
	return m.IsAncestor(ctx, c.Goal, c.Member, base, c.Commit)
}

func (m *Manager) DeliveryDirectory(goal, operation string) (string, error) {
	return m.path(goal, "delivery", operation)
}
