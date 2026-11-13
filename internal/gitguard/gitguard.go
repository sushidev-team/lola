// Package gitguard keeps HOST-side git from following metadata a SANDBOXED
// agent can rewrite.
//
// A sandboxed agent (internal/runtime/sandbox.go) can write its worktree —
// including the `.git` pointer file — and the repository's git store (it must,
// to commit). Plain `git -C <worktree> …` on the host resolves its repository
// through exactly those files: the pointer names the admin dir, the admin dir's
// `commondir` names the store, and the store's config is then honoured. An
// agent that re-points `.git` at metadata of its own gets its own config read
// by the daemon's next host git call — `core.fsmonitor`, a `filter.*.clean`
// driver, and every other config-named command run ON THE HOST. That is a
// sandbox escape through lola's own checkpoints, diffs and dirty checks.
//
// So the runtime PINS each sandboxed worktree when it creates it, before any
// agent runs: the admin dir and store as they were then. Every host git call
// on such a worktree goes through Env/Apply, which hands git
// GIT_DIR/GIT_COMMON_DIR/GIT_WORK_TREE — git then never reads the pointer or
// `commondir` — plus command-line-level overrides for the config-named hooks
// that need no attacker-chosen name (core.fsmonitor, core.hooksPath). Config
// then comes only from the store's `config`, which the container mounts
// READ-ONLY, and the admin dir's `config.worktree`, likewise read-only.
//
// A stdlib leaf with a process-wide registry, because the callers are spread
// over packages that must not know about sessions (checkpoint, gitdiff,
// worktree, reviewagent).
package gitguard

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

// Pin is a sandboxed worktree's trusted git layout, captured at creation.
type Pin struct {
	GitDir    string `json:"git_dir"`    // the worktree's admin dir (<store>/worktrees/<name>)
	CommonDir string `json:"common_dir"` // the repository's git store
}

// Valid reports whether both paths are set and absolute.
func (p Pin) Valid() bool {
	return filepath.IsAbs(p.GitDir) && filepath.IsAbs(p.CommonDir)
}

var (
	mu   sync.RWMutex
	pins = map[string]Pin{}
)

// Set pins worktree (an absolute path) to p. An invalid pin is ignored.
func Set(worktree string, p Pin) {
	if !filepath.IsAbs(worktree) || !p.Valid() {
		return
	}
	mu.Lock()
	defer mu.Unlock()
	pins[filepath.Clean(worktree)] = Pin{GitDir: filepath.Clean(p.GitDir), CommonDir: filepath.Clean(p.CommonDir)}
}

// Deny marks worktree as sandboxed with NO trusted layout (a record that lost
// its pin): every host git call on it then fails rather than following
// metadata the agent may have rewritten. Fail closed.
func Deny(worktree string) {
	if !filepath.IsAbs(worktree) {
		return
	}
	mu.Lock()
	defer mu.Unlock()
	pins[filepath.Clean(worktree)] = Pin{}
}

// Clear drops worktree's pin.
func Clear(worktree string) {
	mu.Lock()
	defer mu.Unlock()
	delete(pins, filepath.Clean(worktree))
}

// Lookup returns the pin that governs dir: the pinned worktree that is dir or
// its NEAREST ancestor (linked-repo worktrees nest inside the primary one, so
// the longest match wins).
func Lookup(dir string) (worktree string, p Pin, ok bool) {
	if dir == "" {
		return "", Pin{}, false
	}
	d := filepath.Clean(dir)
	mu.RLock()
	defer mu.RUnlock()
	for {
		if p, ok := pins[d]; ok {
			return d, p, true
		}
		parent := filepath.Dir(d)
		if parent == d {
			return "", Pin{}, false
		}
		d = parent
	}
}

// Env is the environment a host git call on dir must add: nil when dir is not
// inside a pinned worktree.
func Env(dir string) []string {
	wt, p, ok := Lookup(dir)
	if !ok {
		return nil
	}
	if !p.Valid() { // Deny: no git dir git could find, so the command fails
		return []string{"GIT_DIR=" + filepath.Join(wt, ".lola-unpinned-sandbox"), "GIT_WORK_TREE=" + wt}
	}
	return []string{
		"GIT_DIR=" + p.GitDir,
		"GIT_COMMON_DIR=" + p.CommonDir,
		"GIT_WORK_TREE=" + wt,
		// Command-line-level config outranks every config file, so these hold
		// even against a store config that somehow names them.
		"GIT_CONFIG_COUNT=2",
		"GIT_CONFIG_KEY_0=core.fsmonitor", "GIT_CONFIG_VALUE_0=false",
		"GIT_CONFIG_KEY_1=core.hooksPath", "GIT_CONFIG_VALUE_1=/dev/null",
	}
}

// Apply adds Env for the directory a git command runs in — its `-C` argument
// when present, else cmd.Dir — to cmd. A no-op for an unpinned directory.
func Apply(cmd *exec.Cmd) {
	dir := cmd.Dir
	for i := 1; i+1 < len(cmd.Args); i++ {
		if cmd.Args[i] == "-C" {
			dir = cmd.Args[i+1]
			break
		}
		if !strings.HasPrefix(cmd.Args[i], "-") {
			break // the subcommand: global options end here
		}
	}
	env := Env(dir)
	if env == nil {
		return
	}
	if cmd.Env == nil {
		cmd.Env = os.Environ()
	}
	cmd.Env = append(cmd.Env, env...)
}

// Pinned reports whether dir lies inside a pinned worktree.
func Pinned(dir string) bool {
	_, _, ok := Lookup(dir)
	return ok
}

// Capture reads a freshly created worktree's layout from its `.git` pointer
// and admin dir. Call it ONLY before any agent has run in worktree: afterwards
// those files are the agent's to rewrite, which is the whole reason a pin is
// captured up front.
func Capture(worktree string) (Pin, error) {
	raw, err := os.ReadFile(filepath.Join(worktree, ".git"))
	if err != nil {
		return Pin{}, err
	}
	gitDir, ok := strings.CutPrefix(strings.TrimSpace(string(raw)), "gitdir: ")
	if !ok {
		return Pin{}, &os.PathError{Op: "gitguard", Path: worktree, Err: os.ErrInvalid}
	}
	if !filepath.IsAbs(gitDir) {
		gitDir = filepath.Join(worktree, gitDir)
	}
	gitDir = filepath.Clean(gitDir)
	common := gitDir
	if c, err := os.ReadFile(filepath.Join(gitDir, "commondir")); err == nil {
		common = strings.TrimSpace(string(c))
		if !filepath.IsAbs(common) {
			common = filepath.Join(gitDir, common)
		}
	}
	return Pin{GitDir: gitDir, CommonDir: filepath.Clean(common)}, nil
}
