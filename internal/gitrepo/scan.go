package gitrepo

import (
	"context"
	"os"
	"path/filepath"
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
	infos, _ := i.scan(ctx, dir, MaxScanChildren)
	return infos
}

// ScanAll lists every immediate checkout for bulk import. A read failure or
// timeout is reported so the picker never presents a partial list as complete.
func ScanAll(ctx context.Context, dir string) ([]Info, error) {
	return Inspector{}.scan(ctx, dir, 0)
}

func (i Inspector) scan(ctx context.Context, dir string, limit int) ([]Info, error) {
	dir = strings.TrimSpace(dir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []Info
	checked := 0
	for _, e := range entries {
		if (limit > 0 && checked >= limit) || ctx.Err() != nil {
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
		// info.Root stays git's RESOLVED root — what InspectPath and the
		// project forms save — so a symlinked app matches its configured
		// project instead of being added a second time under another path.
		out = append(out, info)
	}
	return out, ctx.Err()
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
