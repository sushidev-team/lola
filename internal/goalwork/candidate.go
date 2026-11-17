package goalwork

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
)

// VerifyCandidate reads only daemon-owned metadata. Reviewers never receive
// worker Git stores, and their candidate must remain clean and at its receipt.
func (m *Manager) VerifyCandidate(ctx context.Context, c Candidate) error {
	dir, err := m.path(c.Goal, "candidates", c.Operation, c.Member)
	if err != nil {
		return err
	}
	known, err := readJSON[Candidate](filepath.Join(dir, "candidate.json"))
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(c, known) || c.Directory != filepath.Join(dir, "tree") {
		return errors.New("candidate ownership changed")
	}
	if head, err := m.resolve(ctx, c.Directory, "HEAD"); err != nil {
		return err
	} else if head != c.Commit {
		return errors.New("candidate head changed")
	}
	status, err := m.git(ctx, c.Directory, "status", "--porcelain", "--untracked-files=all")
	if err != nil {
		return err
	}
	if strings.TrimSpace(status) != "" {
		return errors.New("candidate was modified outside verified commits")
	}
	return nil
}

// FinalCandidate snapshots the complete goal diff from its recorded start.
// Unlike Stage, it imports no worker output and cannot advance any refs.
func (m *Manager) FinalCandidate(ctx context.Context, goal, operation, member, head string) (Candidate, error) {
	r, err := m.Repository(ctx, goal, member)
	if err != nil {
		return Candidate{}, err
	}
	return m.FinalCandidateAt(ctx, goal, operation, member, head, r.Start)
}

// FinalCandidateAt snapshots the goal against a captured delivery target after
// refresh. The original repository start remains immutable history.
func (m *Manager) FinalCandidateAt(ctx context.Context, goal, operation, member, head, target string) (Candidate, error) {
	r, err := m.Repository(ctx, goal, member)
	if err != nil {
		return Candidate{}, err
	}
	if err := validSHA(head); err != nil {
		return Candidate{}, err
	}
	if err := validSHA(target); err != nil {
		return Candidate{}, err
	}
	if err := m.IsAncestor(ctx, goal, member, r.Start, target); err != nil {
		return Candidate{}, errors.New("final target does not preserve the original starting revision")
	}
	if err := m.IsAncestor(ctx, goal, member, target, head); err != nil {
		return Candidate{}, errors.New("final head does not contain the captured delivery target")
	}
	tip, err := m.Tip(ctx, goal, member)
	if err != nil || tip != head {
		return Candidate{}, ErrStale
	}
	dir, err := m.path(goal, "candidates", operation, member)
	if err != nil {
		return Candidate{}, err
	}
	path := filepath.Join(dir, "candidate.json")
	if c, err := readJSON[Candidate](path); err == nil {
		if c.Goal != goal || c.Operation != operation || c.Member != member || c.Commit != head || c.Base != target || c.WorkerHead != "" || c.TargetSHA != "" || c.PriorTarget != "" {
			return Candidate{}, errors.New("final candidate identity reused")
		}
		return c, m.VerifyCandidate(ctx, c)
	} else if !errors.Is(err, os.ErrNotExist) {
		return Candidate{}, err
	}
	if _, err := os.Lstat(dir); err == nil {
		return Candidate{}, errors.New("partial final candidate retained; explicit recovery required")
	} else if !errors.Is(err, os.ErrNotExist) {
		return Candidate{}, err
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return Candidate{}, err
	}
	tree := filepath.Join(dir, "tree")
	if _, err := m.git(ctx, dir, "clone", "--no-local", "--no-hardlinks", "--no-checkout", "--template=", r.GitDir, tree); err != nil {
		return Candidate{}, err
	}
	if err := m.noRemote(ctx, tree); err != nil {
		return Candidate{}, err
	}
	if _, err := m.git(ctx, tree, "checkout", "--detach", head); err != nil {
		return Candidate{}, err
	}
	paths, err := m.changedPaths(ctx, tree, target, head)
	if err != nil {
		return Candidate{}, err
	}
	c := Candidate{Goal: goal, Operation: operation, Member: member, Base: target, Commit: head, Directory: tree, Paths: paths}
	return c, writeJSON(path, c)
}
