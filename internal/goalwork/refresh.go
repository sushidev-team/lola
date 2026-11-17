package goalwork

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// TargetStore receives an exact upstream target commit into a daemon-owned
// clone. It is never mounted into a worker or used as the authoritative store.
type TargetStore struct {
	Goal      string `json:"goal"`
	Operation string `json:"operation"`
	Member    string `json:"member"`
	Base      string `json:"base"`
	Target    string `json:"target"`
	GitDir    string `json:"git_dir"`
}

func (m *Manager) PrepareTarget(ctx context.Context, goal, operation, member, base string) (TargetStore, error) {
	if err := validSHA(base); err != nil {
		return TargetStore{}, err
	}
	r, err := m.Repository(ctx, goal, member)
	if err != nil {
		return TargetStore{}, err
	}
	dir, err := m.path(goal, "targets", operation, member)
	if err != nil {
		return TargetStore{}, err
	}
	want := TargetStore{Goal: goal, Operation: operation, Member: member, Base: base, Target: r.Target, GitDir: filepath.Join(dir, "repo.git")}
	if old, err := readJSON[TargetStore](filepath.Join(dir, "target.json")); err == nil {
		if old != want {
			return old, errors.New("target fetch identity reused")
		}
		if _, err := m.resolve(ctx, old.GitDir, base); err != nil {
			return old, err
		}
		return old, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return TargetStore{}, err
	}
	if _, err := os.Lstat(dir); err == nil {
		return TargetStore{}, errors.New("partial target store retained; explicit recovery required")
	} else if !errors.Is(err, os.ErrNotExist) {
		return TargetStore{}, err
	}
	if tip, err := m.Tip(ctx, goal, member); err != nil || tip != base {
		return TargetStore{}, ErrStale
	}
	parent := filepath.Dir(dir)
	if err := os.MkdirAll(parent, 0700); err != nil {
		return TargetStore{}, err
	}
	stage, err := os.MkdirTemp(parent, ".target-")
	if err != nil {
		return TargetStore{}, err
	}
	store := filepath.Join(stage, "repo.git")
	if _, err := m.git(ctx, parent, "clone", "--bare", "--no-local", "--no-hardlinks", "--template=", r.GitDir, store); err != nil {
		return TargetStore{}, err
	}
	if err := m.noRemote(ctx, store); err != nil {
		return TargetStore{}, err
	}
	if err := writeJSON(filepath.Join(stage, "target.json"), want); err != nil {
		return TargetStore{}, err
	}
	if err := os.Rename(stage, dir); err != nil {
		return TargetStore{}, err
	}
	return want, nil
}

// TargetPresent checks the reserved store, never a caller-selected directory.
func (m *Manager) TargetPresent(ctx context.Context, s TargetStore, sha string) error {
	if err := validSHA(sha); err != nil {
		return err
	}
	known, err := m.PrepareTarget(ctx, s.Goal, s.Operation, s.Member, s.Base)
	if err != nil {
		return err
	}
	if known != s {
		return errors.New("target store ownership mismatch")
	}
	if actual, err := m.resolve(ctx, s.GitDir, sha); err != nil {
		return fmt.Errorf("captured target commit is unavailable: %w", err)
	} else if actual != sha {
		return errors.New("captured target did not resolve to the exact commit")
	}
	return nil
}

// TargetDescendsFrom checks continuity between target captures, including ones
// superseded before they were fetched or integrated into the goal repository.
func (m *Manager) TargetDescendsFrom(ctx context.Context, s TargetStore, earlier, target string) error {
	if err := validSHA(earlier); err != nil {
		return err
	}
	if err := m.TargetPresent(ctx, s, target); err != nil {
		return err
	}
	if _, err := m.git(ctx, s.GitDir, "merge-base", "--is-ancestor", earlier, target); err != nil {
		return fmt.Errorf("delivery target no longer descends from its previous capture: %w", err)
	}
	return nil
}

// StageRefresh merges a captured target revision into an isolated candidate.
// Both the existing goal tip and prior target must be ancestors of the result.
// No goal ref changes until AdvanceManifest receives independent evidence.
func (m *Manager) StageRefresh(ctx context.Context, s TargetStore, priorTarget, target string) (Candidate, error) {
	if err := validSHA(priorTarget); err != nil {
		return Candidate{}, err
	}
	if err := m.TargetPresent(ctx, s, target); err != nil {
		return Candidate{}, err
	}
	if err := m.IsAncestor(ctx, s.Goal, s.Member, priorTarget, s.Base); err != nil {
		return Candidate{}, errors.New("goal base does not contain its prior delivery target")
	}
	if _, err := m.git(ctx, s.GitDir, "merge-base", "--is-ancestor", priorTarget, target); err != nil {
		return Candidate{}, errors.New("delivery target history was rewritten; preserving goal and prior target")
	}
	dir, err := m.path(s.Goal, "candidates", s.Operation, s.Member)
	if err != nil {
		return Candidate{}, err
	}
	if c, err := readJSON[Candidate](filepath.Join(dir, "candidate.json")); err == nil {
		if c.Goal != s.Goal || c.Operation != s.Operation || c.Member != s.Member || c.Base != s.Base || c.TargetSHA != target || c.PriorTarget != priorTarget || c.WorkerHead != "" {
			return c, errors.New("refresh candidate identity reused")
		}
		return c, m.VerifyCandidate(ctx, c)
	} else if !errors.Is(err, os.ErrNotExist) {
		return Candidate{}, err
	}
	if tip, err := m.Tip(ctx, s.Goal, s.Member); err != nil || tip != s.Base {
		return Candidate{}, ErrStale
	}
	if _, err := os.Lstat(dir); err == nil {
		return Candidate{}, errors.New("partial refresh candidate retained; explicit recovery required")
	} else if !errors.Is(err, os.ErrNotExist) {
		return Candidate{}, err
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return Candidate{}, err
	}
	tree := filepath.Join(dir, "tree")
	if _, err := m.git(ctx, dir, "clone", "--no-local", "--no-hardlinks", "--no-checkout", "--template=", s.GitDir, tree); err != nil {
		return Candidate{}, err
	}
	if err := m.noRemote(ctx, tree); err != nil {
		return Candidate{}, err
	}
	// Fetching an object into a bare store does not necessarily make it
	// reachable by clone's advertised refs. Import that exact object explicitly.
	if _, err := m.git(ctx, tree, "fetch", "--no-tags", "--no-recurse-submodules", s.GitDir, target); err != nil {
		return Candidate{}, err
	}
	if _, err := m.git(ctx, tree, "checkout", "--detach", s.Base); err != nil {
		return Candidate{}, err
	}
	if _, err := m.git(ctx, tree, "merge-base", "--is-ancestor", s.Base, target); err == nil {
		// This includes a target that already contains a human-merged goal.
		// Keep a true fast-forward so an unchanged member needs no new PR.
		if _, err := m.git(ctx, tree, "checkout", "--detach", target); err != nil {
			return Candidate{}, err
		}
	} else {
		if _, err := m.git(ctx, tree, "merge", "--no-ff", "--no-commit", "--no-edit", target); err != nil {
			return Candidate{}, fmt.Errorf("%w: target refresh: %v", ErrConflict, err)
		}
		if _, err := os.Stat(filepath.Join(tree, ".git", "MERGE_HEAD")); err == nil {
			if _, err := m.git(ctx, tree, "commit", "--no-gpg-sign", "-m", "Refresh verified goal against delivery target"); err != nil {
				return Candidate{}, err
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return Candidate{}, err
		}
	}
	head, err := m.resolve(ctx, tree, "HEAD")
	if err != nil {
		return Candidate{}, err
	}
	for _, ancestor := range []string{s.Base, target} {
		if _, err := m.git(ctx, tree, "merge-base", "--is-ancestor", ancestor, head); err != nil {
			return Candidate{}, errors.New("refresh candidate dropped goal or target history")
		}
	}
	paths, err := m.changedPaths(ctx, tree, s.Base, head)
	if err != nil {
		return Candidate{}, err
	}
	if err := checkPaths(paths, nil); err != nil {
		return Candidate{}, err
	}
	c := Candidate{Goal: s.Goal, Operation: s.Operation, Member: s.Member, Base: s.Base, Commit: head, Directory: tree, Paths: paths, TargetSHA: target, PriorTarget: priorTarget}
	return c, writeJSON(filepath.Join(dir, "candidate.json"), c)
}

func (m *Manager) IsAncestor(ctx context.Context, goal, member, ancestor, head string) error {
	if err := validSHA(ancestor); err != nil {
		return err
	}
	if err := validSHA(head); err != nil {
		return err
	}
	r, err := m.Repository(ctx, goal, member)
	if err != nil {
		return err
	}
	_, err = m.git(ctx, r.GitDir, "merge-base", "--is-ancestor", ancestor, head)
	return err
}

// TreesEqual recognizes a human squash merge without pretending its commit
// ancestry matches the goal branch. Both exact commits must already be in the
// daemon-owned store; no working tree or remote state is consulted.
func (m *Manager) TreesEqual(ctx context.Context, goal, member, left, right string) (bool, error) {
	if err := validSHA(left); err != nil {
		return false, err
	}
	if err := validSHA(right); err != nil {
		return false, err
	}
	r, err := m.Repository(ctx, goal, member)
	if err != nil {
		return false, err
	}
	out, err := m.git(ctx, r.GitDir, "rev-parse", left+"^{tree}", right+"^{tree}")
	if err != nil {
		return false, err
	}
	trees := strings.Fields(out)
	if len(trees) != 2 {
		return false, errors.New("cannot establish both repository trees")
	}
	for _, tree := range trees {
		if err := validSHA(tree); err != nil {
			return false, err
		}
	}
	return trees[0] == trees[1], nil
}
