package gitdiff

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

// Stat is a worktree's change SIZE against its merge-base: how many files and
// how many lines. It is the cheap summary the best-of compare view puts side by
// side, so it never renders a patch.
type Stat struct {
	Files     int `json:"files"`
	Additions int `json:"additions"`
	Deletions int `json:"deletions"`
	// Untracked counts new files git does not track yet. They are in Files but
	// not in the line counts: `--numstat` cannot see them, and reading every
	// one to count its lines is the expensive half Stat exists to avoid.
	Untracked int `json:"untracked,omitempty"`
}

// Stat returns the change size of the worktree at dir relative to its
// merge-base with base — the same set Diff covers (committed, staged, unstaged,
// untracked). A binary file counts as a file with no lines. Local git only.
func (d Differ) Stat(ctx context.Context, dir, base string) (Stat, error) {
	run, bin := d.exec()
	_, sha, err := d.mergeBase(ctx, dir, base)
	if err != nil {
		return Stat{}, err
	}
	out, err := run(ctx, bin, dir, "diff", "--no-color", "--no-ext-diff", "-M", "--numstat", "-z", sha, "--")
	if err != nil {
		return Stat{}, fmt.Errorf("git diff --numstat: %w", err)
	}
	st := parseNumstatZ(string(out))
	if u, err := run(ctx, bin, dir, "ls-files", "--others", "--exclude-standard", "-z"); err == nil {
		for _, p := range strings.Split(string(u), "\x00") {
			if p != "" {
				st.Files++
				st.Untracked++
			}
		}
	} else if ctx.Err() != nil {
		return Stat{}, ctx.Err()
	}
	return st, nil
}

// parseNumstatZ reads `git diff --numstat -z`: one "<add>\t<del>\t<path>\x00"
// record per file, where a rename leaves <path> empty and follows it with
// "<old>\x00<new>\x00", and a binary file prints "-" for both counts.
func parseNumstatZ(z string) Stat {
	var st Stat
	fields := strings.Split(z, "\x00")
	for i := 0; i < len(fields); i++ {
		rec := fields[i]
		if rec == "" {
			continue
		}
		parts := strings.SplitN(rec, "\t", 3)
		if len(parts) != 3 {
			continue
		}
		st.Files++
		if a, err := strconv.Atoi(parts[0]); err == nil {
			st.Additions += a
		}
		if d, err := strconv.Atoi(parts[1]); err == nil {
			st.Deletions += d
		}
		if parts[2] == "" { // rename/copy: skip its old and new path fields
			i += 2
		}
	}
	return st
}
