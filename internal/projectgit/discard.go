package projectgit

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// DiscardPlan pins the index and file contents reviewed in the confirmation.
// It is computed on demand, never as part of background repository polling.
type DiscardPlan struct {
	Token     string   `json:"token"`
	Tracked   []string `json:"tracked"`
	Untracked []string `json:"untracked"`
}

func (c Client) PrepareDiscard(ctx context.Context, dir string) (DiscardPlan, error) {
	plan := DiscardPlan{Tracked: []string{}, Untracked: []string{}}
	if err := c.requireIdle(ctx, dir); err != nil {
		return plan, err
	}
	files, err := c.status(ctx, dir)
	if err != nil {
		return plan, err
	}
	branch, branchErr := c.text(ctx, dir, "symbolic-ref", "--quiet", "HEAD")
	head, headErr := c.text(ctx, dir, "rev-parse", "--verify", "HEAD")
	// Detached HEAD has no symbolic ref; an unborn branch has no commit yet.
	if branchErr != nil && headErr != nil {
		return plan, errors.Join(branchErr, headErr)
	}
	index, err := c.git(ctx, dir, "", "ls-files", "--stage", "-z")
	if err != nil {
		return plan, err
	}
	submodules := map[string]bool{}
	for _, entry := range strings.Split(string(index), "\x00") {
		if strings.HasPrefix(entry, "160000 ") {
			_, path, _ := strings.Cut(entry, "\t")
			submodules[path] = true
		}
	}
	h := sha256.New()
	// Include all index entries and status so staging or adding files invalidates
	// the confirmation even if the previously selected working bytes are unchanged.
	_ = json.NewEncoder(h).Encode([]string{dir, branch, head, string(index)})
	_ = json.NewEncoder(h).Encode(files)
	root, err := os.OpenRoot(dir)
	if err != nil {
		return plan, err
	}
	defer root.Close()
	for _, f := range files {
		if f.Conflict {
			return plan, errors.New("resolve conflicts before discarding changes")
		}
		if !f.Untracked && f.Worktree == " " {
			continue
		}
		if !validPath(f.Path) || submodules[f.Path] || f.Worktree == "R" || f.Worktree == "C" {
			return plan, fmt.Errorf("discard files inside %q separately; this entry is a directory, submodule or worktree rename", f.Path)
		}
		if f.Index == " " && f.Worktree == "A" {
			return plan, fmt.Errorf("unstage the intent-to-add entry %q before discarding changes", f.Path)
		}
		// Do not let a changed parent directory redirect a restore through a symlink.
		for parent := filepath.Dir(f.Path); parent != "."; parent = filepath.Dir(parent) {
			info, e := root.Lstat(parent)
			if os.IsNotExist(e) {
				continue
			}
			if e != nil {
				return plan, e
			}
			if !info.IsDir() {
				return plan, fmt.Errorf("cannot discard through non-directory %q", parent)
			}
		}
		info, e := root.Lstat(f.Path)
		if e != nil && !os.IsNotExist(e) {
			return plan, e
		}
		if os.IsNotExist(e) {
			if f.Untracked {
				return plan, errors.New("files changed; review unstaged changes again")
			}
			_ = json.NewEncoder(h).Encode([]string{f.Path, "missing"})
		} else {
			_ = json.NewEncoder(h).Encode([]string{f.Path, info.Mode().String()})
			switch {
			case info.Mode()&os.ModeSymlink != 0:
				target, e := root.Readlink(f.Path)
				if e != nil {
					return plan, e
				}
				_ = json.NewEncoder(h).Encode(target)
			case info.Mode().IsRegular():
				file, e := root.Open(f.Path)
				if e != nil {
					return plan, e
				}
				content := sha256.New()
				_, e = io.Copy(content, file)
				file.Close()
				if e != nil {
					return plan, e
				}
				h.Write(content.Sum(nil))
			default:
				return plan, fmt.Errorf("discard files inside %q separately; directories and special files are kept", f.Path)
			}
		}
		if f.Untracked {
			plan.Untracked = append(plan.Untracked, f.Path)
		} else {
			plan.Tracked = append(plan.Tracked, f.Path)
		}
		if err := ctx.Err(); err != nil {
			return plan, err
		}
	}
	plan.Token = hex.EncodeToString(h.Sum(nil))
	return plan, nil
}

// DiscardUnstaged restores only the worktree from the index. Untracked files
// listed in the confirmation are removed individually, never recursively, so
// ignored files and nested repositories cannot be swept away.
func (c Client) DiscardUnstaged(ctx context.Context, dir, token string) error {
	plan, err := c.PrepareDiscard(ctx, dir)
	if err != nil {
		return err
	}
	if token == "" || token != plan.Token {
		return errors.New("files or staged changes changed; review unstaged changes and confirm again")
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer root.Close()
	if len(plan.Tracked) > 0 {
		_, err = c.git(ctx, dir, strings.Join(plan.Tracked, "\x00")+"\x00", "restore", "--worktree", "--pathspec-from-file=-", "--pathspec-file-nul")
		if err != nil {
			return err
		}
	}
	for _, path := range plan.Untracked {
		if err = ctx.Err(); err != nil {
			return err
		}
		if err = root.Remove(path); err != nil {
			return fmt.Errorf("could not remove %q: %w", path, err)
		}
	}
	return nil
}
