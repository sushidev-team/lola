// Package claudetrust records Claude Code's per-folder workspace trust for the
// worktrees lola itself creates, so a spawned agent never parks on the
// "Is this a project you created or one you trust?" dialog.
//
// Claude Code keeps that decision in its global config (~/.claude.json, or
// $CLAUDE_CONFIG_DIR/.claude.json) as projects[<path>].hasTrustDialogAccepted.
// Its lookup walks up from the working directory but STOPS at the git root,
// and every lola worktree is its own git root (a `.git` file), so trusting
// ~/.lola/worktrees once does nothing: the decision has to be recorded per
// worktree. That is what Trust does, just before the agent launches; Untrust
// removes it again when the worktree is torn down, so the file does not
// collect one entry per session forever.
//
// The file is SHARED with every running Claude Code process, which rewrites
// it constantly. The rules that keep this safe:
//
//   - Only the one key is touched. Every other byte of meaning is preserved:
//     the document is decoded into json.RawMessage maps, so unknown keys and
//     values round-trip untouched (Go re-sorts the keys; Claude Code does not
//     depend on key order and rewrites the file in its own order anyway).
//   - Read-modify-write is atomic (temp file in the same directory + rename),
//     follows a symlinked config to its target, and keeps the file's mode. A
//     file that does not parse is NEVER rewritten — it may be mid-write by
//     Claude Code, and replacing it would destroy the user's settings.
//   - A no-op change writes nothing, so Untrust on a path that was never
//     trusted (every rollback) costs one read.
//   - Callers treat every error as advisory: the worst case of a failed or
//     clobbered write is the dialog the user already saw before this existed.
package claudetrust

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
)

// trustKey is the per-project flag Claude Code checks before showing its
// workspace-trust dialog.
const trustKey = "hasTrustDialogAccepted"

// mu serializes this process's own read-modify-write cycles, so two spawns
// finishing together cannot each read the old file and drop the other's
// entry. It cannot coordinate with Claude Code; see the package comment.
var mu sync.Mutex

// ConfigPath returns the Claude Code global config file the daemon's agents
// read: $CLAUDE_CONFIG_DIR/.claude.json when that is set, else
// ~/.claude.json. "" when no home directory can be resolved.
func ConfigPath() string {
	if d := os.Getenv("CLAUDE_CONFIG_DIR"); d != "" {
		return filepath.Join(d, ".claude.json")
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".claude.json")
}

// Trust records dir as trusted in the config file at path. A missing file is
// created (0600) holding only the entry. An existing entry keeps every other
// field; one already trusted is left alone without a write.
func Trust(path, dir string) error {
	return update(path, dir, true)
}

// Untrust removes the trust flag for dir from the config file at path. An
// entry left holding nothing else is removed whole; one carrying other state
// (allowed tools, MCP choices) keeps it. A missing file or entry is a no-op.
func Untrust(path, dir string) error {
	return update(path, dir, false)
}

// Keys returns the spellings Claude Code may look dir up under: the path as
// given and, when it differs, its symlink-resolved form (macOS's /var →
// /private/var, a symlinked home). Both are written so either lookup hits.
func Keys(dir string) []string {
	clean := filepath.Clean(dir)
	keys := []string{clean}
	if real, err := filepath.EvalSymlinks(clean); err == nil && real != clean {
		keys = append(keys, real)
	}
	return keys
}

func update(path, dir string, trust bool) error {
	if path == "" {
		return errors.New("claudetrust: no claude config path")
	}
	if dir == "" || !filepath.IsAbs(dir) {
		return fmt.Errorf("claudetrust: %q is not an absolute directory", dir)
	}
	mu.Lock()
	defer mu.Unlock()

	// Write next to the REAL file: renaming over a symlink would replace a
	// dotfiles-managed link with a plain file.
	target := path
	if real, err := filepath.EvalSymlinks(path); err == nil {
		target = real
	}
	data, err := os.ReadFile(target)
	mode := fs.FileMode(0o600)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		if !trust {
			return nil
		}
		data = nil
	case err != nil:
		return fmt.Errorf("claudetrust: read %s: %w", target, err)
	default:
		if st, serr := os.Stat(target); serr == nil {
			mode = st.Mode().Perm()
		}
	}

	doc := map[string]json.RawMessage{}
	if len(bytes.TrimSpace(data)) > 0 {
		if err := json.Unmarshal(data, &doc); err != nil {
			return fmt.Errorf("claudetrust: %s does not parse, left untouched: %w", target, err)
		}
	}
	projects := map[string]json.RawMessage{}
	if raw, ok := doc["projects"]; ok && !isNull(raw) {
		if err := json.Unmarshal(raw, &projects); err != nil {
			return fmt.Errorf("claudetrust: %s: projects is not an object, left untouched: %w", target, err)
		}
	}

	changed := false
	for _, key := range Keys(dir) {
		c, err := setEntry(projects, key, trust)
		if err != nil {
			return fmt.Errorf("claudetrust: %s: %w", target, err)
		}
		changed = changed || c
	}
	if !changed {
		return nil
	}
	rawProjects, err := json.Marshal(projects)
	if err != nil {
		return err
	}
	doc["projects"] = rawProjects
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(target, append(out, '\n'), mode)
}

// setEntry sets or clears the trust flag on projects[key], reporting whether
// anything changed. An entry that is not a JSON object is an error: it is not
// a shape this package understands, so nothing is written.
func setEntry(projects map[string]json.RawMessage, key string, trust bool) (bool, error) {
	entry := map[string]json.RawMessage{}
	raw, exists := projects[key]
	if exists && !isNull(raw) {
		if err := json.Unmarshal(raw, &entry); err != nil {
			return false, fmt.Errorf("projects[%q] is not an object, left untouched: %w", key, err)
		}
	}
	if trust {
		if v, ok := entry[trustKey]; ok && string(bytes.TrimSpace(v)) == "true" {
			return false, nil
		}
		entry[trustKey] = json.RawMessage("true")
	} else {
		if _, ok := entry[trustKey]; !ok {
			return false, nil
		}
		delete(entry, trustKey)
		if len(entry) == 0 {
			delete(projects, key)
			return true, nil
		}
	}
	b, err := json.Marshal(entry)
	if err != nil {
		return false, err
	}
	projects[key] = b
	return true, nil
}

func isNull(raw json.RawMessage) bool {
	return string(bytes.TrimSpace(raw)) == "null"
}

// writeAtomic replaces path with data via a temp file in the same directory,
// so a reader (Claude Code starting up) sees either the old or the new file,
// never a torn one.
func writeAtomic(path string, data []byte, mode fs.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".claude.json.lola-*")
	if err != nil {
		return fmt.Errorf("claudetrust: %w", err)
	}
	name := tmp.Name()
	ok := false
	defer func() {
		if !ok {
			_ = os.Remove(name)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("claudetrust: write %s: %w", name, err)
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return fmt.Errorf("claudetrust: chmod %s: %w", name, err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("claudetrust: sync %s: %w", name, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("claudetrust: close %s: %w", name, err)
	}
	if err := os.Rename(name, path); err != nil {
		return fmt.Errorf("claudetrust: replace %s: %w", path, err)
	}
	ok = true
	return nil
}
