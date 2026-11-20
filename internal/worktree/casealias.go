package worktree

import (
	"context"
	"os"
	"path/filepath"
	"strings"
)

// A shared repo config can describe a case-sensitive main checkout while the
// linked worktree lives on a case-insensitive volume. Git then reports files
// under two tracked spellings of the same directory as both tracked and new.
func (m *Manager) checkoutStatus(ctx context.Context, dir string) (dirty, caseAliases bool, err error) {
	out, _, err := m.git(ctx, "-C", dir, "status", "--porcelain")
	if err != nil || out == "" {
		return false, false, err
	}
	for line := range strings.SplitSeq(strings.TrimSuffix(out, "\n"), "\n") {
		if !strings.HasPrefix(line, "?? ") || !strings.Contains(line, "/") {
			return true, false, nil
		}
	}

	// Expand collapsed directories and handle filenames verbatim. Every reported
	// file must alias an index entry. A distinct untracked file always wins.
	out, _, err = m.git(ctx, "-C", dir, "status", "--porcelain", "-z", "--untracked-files=all")
	if err != nil {
		return false, false, err
	}
	index, _, err := m.git(ctx, "-C", dir, "ls-files", "-v", "-z")
	if err != nil {
		return false, false, err
	}
	aliases := onlyTrackedAliases(dir, out, index)
	return !aliases, aliases, nil
}

func onlyTrackedAliases(dir, status, index string) bool {
	// Require BOTH directory spellings in the index. A person's case-only
	// rename of a directory also resolves to the same inode on this volume.
	directories := map[string]bool{}
	tracked := map[string]string{}
	for entry := range strings.SplitSeq(index, "\x00") {
		// Only ordinary tracked entries. Git may have skipped checking an
		// assume-unchanged or skip-worktree file against its index content.
		path, ok := strings.CutPrefix(entry, "H ")
		if !ok || !filepath.IsLocal(path) {
			continue
		}
		parent := filepath.Dir(path)
		key := filepath.Join(strings.ToLower(parent), filepath.Base(path))
		if _, collision := tracked[key]; collision {
			tracked[key] = "" // multiple tracked files at this spelling are ambiguous
		} else {
			tracked[key] = path
		}
		for parent != "." && !directories[parent] {
			directories[parent] = true
			parent = filepath.Dir(parent)
		}
	}
	for entry := range strings.SplitSeq(strings.TrimSuffix(status, "\x00"), "\x00") {
		path, ok := strings.CutPrefix(entry, "?? ")
		if !ok || !filepath.IsLocal(path) {
			return false
		}
		parent := filepath.Dir(path)
		other := tracked[filepath.Join(strings.ToLower(parent), filepath.Base(path))]
		if other == "" || other == path {
			return false
		}
		// Walk past the identical suffix to the directory whose spelling
		// differs. Its on-disk spelling must be one the repo actually uses.
		otherParent := filepath.Dir(other)
		for filepath.Base(parent) == filepath.Base(otherParent) && parent != "." {
			parent, otherParent = filepath.Dir(parent), filepath.Dir(otherParent)
		}
		if filepath.Dir(parent) != filepath.Dir(otherParent) || !directories[parent] {
			return false
		}
		a, errA := os.Lstat(filepath.Join(dir, filepath.Dir(path)))
		b, errB := os.Lstat(filepath.Join(dir, filepath.Dir(other)))
		if errA != nil || errB != nil || !a.IsDir() || !b.IsDir() || !os.SameFile(a, b) {
			return false
		}
	}
	return true
}
