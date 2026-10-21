package goalwork

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type Repository struct {
	Goal   string `json:"goal"`
	Member string `json:"member"`
	Source string `json:"source"`
	Target string `json:"target"` // user's final delivery branch; never advanced here
	Start  string `json:"start"`
	Branch string `json:"branch"`
	GitDir string `json:"git_dir"`
}

type Workspace struct {
	Goal      string `json:"goal"`
	Member    string `json:"member"`
	Task      string `json:"task"`
	Attempt   string `json:"attempt"`
	Base      string `json:"base"`
	Branch    string `json:"branch"`
	Directory string `json:"directory"`
	GitDir    string `json:"git_dir"`
}

// LaunchDirectory is private control storage; the runtime mounts only its
// workspace child writable, never this directory or the broader goal tree.
func (m *Manager) LaunchDirectory(goal, attempt string) (string, error) {
	return m.path(goal, "launch", attempt)
}

// Initialize copies committed source history without hardlinks or alternates.
// The original checkout is only read. A staged directory becomes authoritative
// only after the complete owner receipt and goal ref have been written.
func (m *Manager) Initialize(ctx context.Context, goal, member, source, target string) (Repository, error) {
	branch, err := GoalBranch(goal)
	if err != nil {
		return Repository{}, err
	}
	dir, err := m.path(goal, "repositories", member)
	if err != nil {
		return Repository{}, err
	}
	if !filepath.IsAbs(source) || target == "" || strings.HasPrefix(target, "-") {
		return Repository{}, fmt.Errorf("source checkout and target branch required")
	}
	if old, err := readJSON[Repository](filepath.Join(dir, "repository.json")); err == nil {
		if old.Goal != goal || old.Member != member || old.Source != source || old.Target != target || old.Branch != branch || old.GitDir != filepath.Join(dir, "repo.git") {
			return old, errors.New("existing goal repository has different ownership or placement")
		}
		if _, err := m.resolve(ctx, old.GitDir, "refs/heads/"+branch); err != nil {
			return old, err
		}
		return old, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return Repository{}, err
	}
	if _, err := os.Lstat(dir); err == nil {
		return Repository{}, errors.New("unrecognized goal repository directory; preserving it for inspection")
	} else if !errors.Is(err, os.ErrNotExist) {
		return Repository{}, err
	}
	if _, err := m.git(ctx, source, "check-ref-format", "refs/heads/"+target); err != nil {
		return Repository{}, err
	}
	start, err := m.resolve(ctx, source, "refs/heads/"+target)
	if err != nil {
		return Repository{}, fmt.Errorf("read source target branch: %w", err)
	}
	parent := filepath.Dir(dir)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return Repository{}, err
	}
	stage, err := os.MkdirTemp(parent, ".initialize-")
	if err != nil {
		return Repository{}, err
	}
	store := filepath.Join(stage, "repo.git")
	if _, err := m.git(ctx, parent, "clone", "--bare", "--no-local", "--no-hardlinks", "--template=", source, store); err != nil {
		return Repository{}, err
	}
	if err := m.noRemote(ctx, store); err != nil {
		return Repository{}, err
	}
	if _, err := m.git(ctx, store, "update-ref", "refs/heads/"+branch, start, strings.Repeat("0", len(start))); err != nil {
		return Repository{}, err
	}
	if _, err := m.git(ctx, store, "symbolic-ref", "HEAD", "refs/heads/"+branch); err != nil {
		return Repository{}, err
	}
	r := Repository{Goal: goal, Member: member, Source: source, Target: target, Start: start, Branch: branch, GitDir: filepath.Join(dir, "repo.git")}
	if err := writeJSON(filepath.Join(stage, "repository.json"), r); err != nil {
		return Repository{}, err
	}
	if err := os.Rename(stage, dir); err != nil {
		return Repository{}, err
	}
	return r, nil
}

func (m *Manager) Repository(ctx context.Context, goal, member string) (Repository, error) {
	dir, err := m.path(goal, "repositories", member)
	if err != nil {
		return Repository{}, err
	}
	r, err := readJSON[Repository](filepath.Join(dir, "repository.json"))
	if err != nil {
		return r, err
	}
	branch, err := GoalBranch(goal)
	if err != nil {
		return r, err
	}
	if r.Goal != goal || r.Member != member || r.GitDir != filepath.Join(dir, "repo.git") || r.Branch != branch {
		return r, errors.New("goal repository receipt does not match its location")
	}
	return r, nil
}

func (m *Manager) Tip(ctx context.Context, goal, member string) (string, error) {
	r, err := m.Repository(ctx, goal, member)
	if err != nil {
		return "", err
	}
	return m.resolve(ctx, r.GitDir, "refs/heads/"+r.Branch)
}

// PrepareWorker creates a standalone private store with an ordinary worktree.
// The runtime may mount only this Directory and GitDir, with config/hooks and
// the .git pointer read-only. It must never mount the authoritative repository.
func (m *Manager) PrepareWorker(ctx context.Context, goal, member, task, attempt, base string) (Workspace, error) {
	if err := validSHA(base); err != nil {
		return Workspace{}, err
	}
	branch, err := TaskBranch(task, attempt)
	if err != nil {
		return Workspace{}, err
	}
	dir, err := m.path(goal, "attempts", task, attempt, member)
	if err != nil {
		return Workspace{}, err
	}
	w := Workspace{Goal: goal, Member: member, Task: task, Attempt: attempt, Base: base, Branch: branch, Directory: filepath.Join(dir, "tree"), GitDir: filepath.Join(dir, "store.git")}
	if old, err := readJSON[Workspace](filepath.Join(dir, "workspace.json")); err == nil {
		if old != w {
			return Workspace{}, errors.New("attempt identity already belongs to a different base or scope")
		}
		// Do not execute host git in a resumed worker workspace. Its metadata
		// has been writable by the worker since the initial launch.
		return w, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return Workspace{}, err
	}
	if _, err := os.Lstat(dir); err == nil {
		return Workspace{}, errors.New("partial worker workspace retained; choose a new attempt")
	} else if !errors.Is(err, os.ErrNotExist) {
		return Workspace{}, err
	}
	r, err := m.Repository(ctx, goal, member)
	if err != nil {
		return Workspace{}, err
	}
	if _, err := m.resolve(ctx, r.GitDir, base); err != nil {
		return Workspace{}, err
	}
	if _, err := m.git(ctx, r.GitDir, "merge-base", "--is-ancestor", base, "refs/heads/"+r.Branch); err != nil {
		return Workspace{}, errors.New("worker base is not part of the integrated goal history")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return Workspace{}, err
	}
	if _, err := m.git(ctx, dir, "clone", "--no-local", "--no-hardlinks", "--no-checkout", "--template=", "--separate-git-dir="+w.GitDir, r.GitDir, w.Directory); err != nil {
		return Workspace{}, err
	}
	// Git canonicalizes ancestor aliases (macOS /var -> /private/var). Docker
	// mounts this private store at the configured lexical path, so pin the
	// pointer to that exact in-container path while the directory is still
	// exclusively daemon-owned. Resumed workers never take this write path.
	if err := os.WriteFile(filepath.Join(w.Directory, ".git"), []byte("gitdir: "+w.GitDir+"\n"), 0600); err != nil {
		return Workspace{}, err
	}
	if err := m.noRemote(ctx, w.Directory); err != nil {
		return Workspace{}, err
	}
	if _, err := m.git(ctx, w.Directory, "checkout", "-b", branch, base); err != nil {
		return Workspace{}, err
	}
	if err := os.MkdirAll(filepath.Join(w.Directory, ".lola"), 0o700); err != nil {
		return Workspace{}, err
	}
	if err := os.MkdirAll(filepath.Join(w.GitDir, "info"), 0o700); err != nil {
		return Workspace{}, err
	}
	if err := os.WriteFile(filepath.Join(w.GitDir, "info", "exclude"), []byte(".lola/\n"), 0o600); err != nil {
		return Workspace{}, err
	}
	if err := os.MkdirAll(filepath.Join(w.GitDir, "hooks"), 0o700); err != nil {
		return Workspace{}, err
	}
	if err := os.WriteFile(filepath.Join(w.GitDir, "config.worktree"), nil, 0o600); err != nil {
		return Workspace{}, err
	}
	if err := writeJSON(filepath.Join(dir, "workspace.json"), w); err != nil {
		return Workspace{}, err
	}
	return w, nil
}

func readJSON[T any](path string) (T, error) {
	var out T
	f, err := os.Open(path)
	if err != nil {
		return out, err
	}
	defer f.Close()
	err = json.NewDecoder(f).Decode(&out)
	return out, err
}

func writeJSON(path string, value any) error {
	b, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".receipt-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(append(b, '\n')); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err := os.Rename(f.Name(), path); err != nil {
		return err
	}
	d, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
