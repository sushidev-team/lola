package gitrepo

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// MaxScanChildren bounds how many subdirectories Scan inspects, so pointing the
// picker at a huge folder (a home directory) costs a bounded number of git
// probes instead of one per entry.
const MaxScanChildren = 64

// Scan lists the git checkouts DIRECTLY inside dir — the apps a stack is built
// from when it is created from a parent folder. Only an immediate,
// non-hidden subdirectory that is the ROOT of its own checkout counts: a plain
// folder inside a monorepo resolves to the monorepo's root and is skipped, so
// the parent's own repository is never offered once per subfolder.
//
// Results are sorted by directory name. Like Inspect it never errors: an
// unreadable dir is simply no apps.
func (i Inspector) Scan(ctx context.Context, dir string) []Info {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	sort.Slice(entries, func(a, b int) bool { return entries[a].Name() < entries[b].Name() })
	var out []Info
	checked := 0
	for _, e := range entries {
		if checked >= MaxScanChildren || ctx.Err() != nil {
			break
		}
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		child := filepath.Join(dir, e.Name())
		if !isDir(e, child) {
			continue
		}
		checked++
		info := i.Inspect(ctx, child)
		if !info.IsRepo || !sameDir(info.Root, child) {
			continue
		}
		info.Root = child // keep the path the human sees, not a resolved symlink
		out = append(out, info)
	}
	return out
}

// Scan is the package-level convenience for the default inspector.
func Scan(ctx context.Context, dir string) []Info { return Inspector{}.Scan(ctx, dir) }

// isDir follows a symlinked entry: a checkout linked into the parent folder is
// still one of its apps.
func isDir(e os.DirEntry, path string) bool {
	if e.IsDir() {
		return true
	}
	if e.Type()&os.ModeSymlink == 0 {
		return false
	}
	st, err := os.Stat(path)
	return err == nil && st.IsDir()
}

// sameDir compares two directories after resolving symlinks (macOS /tmp is
// /private/tmp, and git reports the resolved one).
func sameDir(a, b string) bool {
	if filepath.Clean(a) == filepath.Clean(b) {
		return true
	}
	ra, err1 := filepath.EvalSymlinks(a)
	rb, err2 := filepath.EvalSymlinks(b)
	return err1 == nil && err2 == nil && ra == rb
}
