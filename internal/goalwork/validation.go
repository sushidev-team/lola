package goalwork

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
)

// Revision identifies one member of the exact combination to validate. A
// candidate operation selects its immutable staged commit; an empty operation
// selects an already integrated commit from the authoritative goal repository.
type Revision struct {
	Commit    string `json:"commit"`
	Operation string `json:"operation,omitempty"`
}

type ValidationWorkspace struct {
	Goal      string              `json:"goal"`
	ID        string              `json:"id"`
	Manifest  map[string]Revision `json:"manifest"`
	Directory string              `json:"directory"`
}

// PrepareValidation copies the full combination into an expendable workspace.
// After returning, none of its Git metadata is consulted by host Git. A new
// check requires a new ID, so an earlier command cannot pollute its inputs.
func (m *Manager) PrepareValidation(ctx context.Context, goal, id string, manifest map[string]Revision) (ValidationWorkspace, error) {
	if len(manifest) == 0 || len(manifest) > 50 {
		return ValidationWorkspace{}, errors.New("validation requires 1–50 repository revisions")
	}
	dir, err := m.path(goal, "validation", id)
	if err != nil {
		return ValidationWorkspace{}, err
	}
	v := ValidationWorkspace{Goal: goal, ID: id, Manifest: manifest, Directory: filepath.Join(dir, "workspace")}
	if old, err := readJSON[ValidationWorkspace](filepath.Join(dir, "workspace.json")); err == nil {
		if !reflect.DeepEqual(old, v) {
			return ValidationWorkspace{}, errors.New("validation identity already describes different inputs")
		}
		return old, nil // Never inspect mutable validation Git metadata on resume.
	} else if !errors.Is(err, os.ErrNotExist) {
		return ValidationWorkspace{}, err
	}
	if _, err := os.Lstat(dir); err == nil {
		return ValidationWorkspace{}, errors.New("partial validation workspace retained; reserve a fresh check")
	} else if !errors.Is(err, os.ErrNotExist) {
		return ValidationWorkspace{}, err
	}
	// Resolve all sources before creating anything. No caller-supplied path is
	// accepted; source paths come only from daemon-owned repository receipts.
	sources := map[string]string{}
	for member, revision := range manifest {
		if !segment.MatchString(member) {
			return ValidationWorkspace{}, errors.New("invalid validation member")
		}
		if err := validSHA(revision.Commit); err != nil {
			return ValidationWorkspace{}, err
		}
		if revision.Operation != "" {
			candidateDir, err := m.path(goal, "candidates", revision.Operation, member)
			if err != nil {
				return ValidationWorkspace{}, err
			}
			c, err := readJSON[Candidate](filepath.Join(candidateDir, "candidate.json"))
			if err != nil {
				return ValidationWorkspace{}, err
			}
			if c.Goal != goal || c.Member != member || c.Operation != revision.Operation || c.Commit != revision.Commit || c.Directory != filepath.Join(candidateDir, "tree") {
				return ValidationWorkspace{}, errors.New("validation candidate receipt mismatch")
			}
			sources[member] = c.Directory
		} else {
			r, err := m.Repository(ctx, goal, member)
			if err != nil {
				return ValidationWorkspace{}, err
			}
			if _, err := m.git(ctx, r.GitDir, "merge-base", "--is-ancestor", revision.Commit, "refs/heads/"+r.Branch); err != nil {
				return ValidationWorkspace{}, fmt.Errorf("validation revision is not integrated: %w", err)
			}
			sources[member] = r.GitDir
		}
	}
	if err := os.MkdirAll(v.Directory, 0700); err != nil {
		return ValidationWorkspace{}, err
	}
	for member, source := range sources {
		tree := filepath.Join(v.Directory, member)
		if _, err := m.git(ctx, v.Directory, "clone", "--no-local", "--no-hardlinks", "--no-checkout", "--template=", source, tree); err != nil {
			return ValidationWorkspace{}, err
		}
		if err := m.noRemote(ctx, tree); err != nil {
			return ValidationWorkspace{}, err
		}
		if _, err := m.git(ctx, tree, "checkout", "--detach", manifest[member].Commit); err != nil {
			return ValidationWorkspace{}, err
		}
	}
	if err := writeJSON(filepath.Join(dir, "workspace.json"), v); err != nil {
		return ValidationWorkspace{}, err
	}
	return v, nil
}

// ValidationDirectory returns the daemon-owned receipt/evidence directory, not
// the mutable workspace mounted into a container.
func (m *Manager) ValidationDirectory(goal, id string) (string, error) {
	return m.path(goal, "validation", id)
}

// ReviewDirectory is daemon-owned control/evidence storage, never mounted as a
// whole into the reviewer. Only its copied evidence subdirectory is exposed.
func (m *Manager) ReviewDirectory(goal, id string) (string, error) {
	return m.path(goal, "reviews", id)
}
