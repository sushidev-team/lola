// Package gitdiff reads what a session has CHANGED: its worktree compared with
// the merge-base of HEAD and the project's default branch, uncommitted edits and
// untracked files included. It is the data behind the in-app diff viewer (the
// desktop's Diff tab and the TUI's diff overlay), where a human reads the
// agent's work and leaves line comments that are sent back to it.
//
// Why the MERGE-BASE and not the branch tip: `git diff main` would also show
// every commit main gained since the session forked, as if the agent had
// reverted it. The merge-base is the commit the session actually started from,
// so the diff is exactly the session's own work — the same set a PR shows.
//
// Why the WORKING TREE and not HEAD: a human reviews while the agent works, and
// most of what an agent has done mid-turn is not committed yet. Diffing the
// merge-base against the working tree covers committed, staged and unstaged
// changes in one pass; untracked files are added separately because git diff
// never lists them.
//
// A stdlib leaf: it shells to git only (local, no network, no gh), behind an
// exec seam, and knows nothing about sessions or config — the daemon resolves
// the worktree and the base branch and hands them in.
package gitdiff

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Caps. The result travels over the daemon socket as one JSON line and is
// rendered in full by both clients, so a session that rewrote a lockfile or
// vendored a dependency must not produce a 50MB answer. Past a cap the diff is
// cut at a FILE boundary (a half file would mislead about what changed) and
// Result.Truncated says so.
const (
	// MaxPatchBytes bounds the summed patch text of every file returned.
	MaxPatchBytes = 2 << 20
	// MaxFilePatchBytes bounds one file's patch; a bigger one is listed with
	// its counts but no patch (TooLarge), like GitHub's "large diffs are not
	// rendered by default".
	MaxFilePatchBytes = 512 << 10
	// MaxUntracked bounds how many untracked files are read from disk.
	MaxUntracked = 200
)

// File is one changed file. Patch is the file's unified diff from the first
// "@@" hunk header on — the "diff --git"/"index"/"---"/"+++" preamble is
// stripped, because every field it carries is already a field here.
type File struct {
	Path      string `json:"path"`
	OldPath   string `json:"oldPath,omitempty"` // set on a rename/copy
	Status    string `json:"status"`            // added|modified|deleted|renamed
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
	Binary    bool   `json:"binary,omitempty"`
	Untracked bool   `json:"untracked,omitempty"` // a new file git does not track yet
	TooLarge  bool   `json:"tooLarge,omitempty"`  // patch omitted (MaxFilePatchBytes)
	Patch     string `json:"patch,omitempty"`
}

// Result is the whole diff of one worktree.
type Result struct {
	Base      string `json:"base"`      // the ref the merge-base was taken against (e.g. "origin/main")
	MergeBase string `json:"mergeBase"` // full commit sha
	Files     []File `json:"files"`
	Truncated bool   `json:"truncated,omitempty"` // MaxPatchBytes was reached; later files omitted
}

// Differ computes diffs. GitBin is the git binary ("" resolves "git").
type Differ struct {
	GitBin string

	// run is the exec seam; nil uses runGit. Tests inject it.
	run func(ctx context.Context, bin, dir string, args ...string) ([]byte, error)
}

// ErrNoBase is returned when no merge-base with the base branch can be found —
// the branch does not exist locally or remotely, or shares no history.
var ErrNoBase = errors.New("no merge-base with the base branch")

// Diff returns the changes in the worktree at dir relative to its merge-base
// with base (a branch name such as "main"). It prefers the REMOTE-tracking ref
// (origin/<base>), because a worktree's local <base> is whatever the main
// checkout last pulled and is routinely stale, then falls back to the local
// branch.
func (d Differ) Diff(ctx context.Context, dir, base string) (Result, error) {
	run, bin := d.exec()
	ref, sha, err := d.mergeBase(ctx, dir, base)
	if err != nil {
		return Result{}, err
	}
	res := Result{Base: ref, MergeBase: sha}

	// --no-ext-diff / --no-color / --no-textconv: the raw text, whatever the
	// user's git config says. -M finds renames, so a moved file is one entry
	// instead of a full delete plus a full add.
	out, err := run(ctx, bin, dir, "-c", "core.quotepath=off", "diff", "--no-color", "--no-ext-diff",
		"--no-textconv", "-M", "--unified=3", res.MergeBase, "--")
	if err != nil {
		return Result{}, fmt.Errorf("git diff: %w", err)
	}
	files := SplitPatch(string(out))

	if u, err := run(ctx, bin, dir, "ls-files", "--others", "--exclude-standard", "-z"); err == nil {
		untracked, more := untrackedFiles(dir, u)
		files = append(files, untracked...)
		res.Truncated = more // past MaxUntracked: say so, never drop files silently
	}

	budget := MaxPatchBytes
	for i := range files {
		f := &files[i]
		if len(f.Patch) > MaxFilePatchBytes {
			f.Patch, f.TooLarge = "", true
		}
		if len(f.Patch) > budget {
			res.Truncated = true
			break
		}
		budget -= len(f.Patch)
		res.Files = append(res.Files, *f)
	}
	if res.Files == nil {
		res.Files = []File{}
	}
	return res, nil
}

// ChangedFiles returns just the PATHS the worktree at dir has changed relative
// to its merge-base with base — committed, staged, unstaged and untracked, the
// same set Diff covers — sorted and de-duplicated. A rename contributes BOTH
// its old and its new path: either side can collide with another branch.
//
// It is the cheap half of Diff, for a caller that asks every observe cycle
// (cross-session overlap detection): `--name-only` reads no file content and
// renders no patch, and untracked files are listed, never read. Local git
// only, like everything here.
func (d Differ) ChangedFiles(ctx context.Context, dir, base string) ([]string, error) {
	run, bin := d.exec()
	_, sha, err := d.mergeBase(ctx, dir, base)
	if err != nil {
		return nil, err
	}
	// -z: NUL-separated, so a path with a newline or a quote is one entry and
	// is never C-quoted. --name-status (not --name-only) because only it names
	// both sides of a rename: "R100\x00old\x00new\x00".
	out, err := run(ctx, bin, dir, "diff", "--no-color", "--no-ext-diff", "-M", "--name-status", "-z", sha, "--")
	if err != nil {
		return nil, fmt.Errorf("git diff --name-status: %w", err)
	}
	seen := map[string]bool{}
	fields := strings.Split(string(out), "\x00")
	for i := 0; i < len(fields); i++ {
		status := fields[i]
		if status == "" {
			continue
		}
		n := 1
		if status[0] == 'R' || status[0] == 'C' {
			n = 2
		}
		for j := 0; j < n && i+1 < len(fields); j++ {
			i++
			if p := fields[i]; p != "" {
				seen[p] = true
			}
		}
	}
	if u, err := run(ctx, bin, dir, "ls-files", "--others", "--exclude-standard", "-z"); err == nil {
		for _, p := range strings.Split(string(u), "\x00") {
			if p != "" {
				seen[p] = true
			}
		}
	} else if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	files := make([]string, 0, len(seen))
	for p := range seen {
		files = append(files, p)
	}
	sort.Strings(files)
	return files, nil
}

// exec resolves the exec seam and the git binary.
func (d Differ) exec() (func(ctx context.Context, bin, dir string, args ...string) ([]byte, error), string) {
	run := d.run
	if run == nil {
		run = runGit
	}
	bin := d.GitBin
	if bin == "" {
		bin = "git"
	}
	return run, bin
}

// mergeBase resolves the commit the worktree at dir forked from: the
// merge-base of HEAD with origin/<base>, else with the local <base>.
func (d Differ) mergeBase(ctx context.Context, dir, base string) (ref, sha string, err error) {
	if strings.TrimSpace(dir) == "" {
		return "", "", errors.New("no worktree")
	}
	if base == "" || strings.HasPrefix(base, "-") {
		return "", "", fmt.Errorf("invalid base branch %q", base)
	}
	run, bin := d.exec()
	for _, ref := range []string{"origin/" + base, base} {
		out, err := run(ctx, bin, dir, "merge-base", "HEAD", ref)
		if err != nil {
			if ctx.Err() != nil {
				return "", "", ctx.Err()
			}
			continue
		}
		if sha := strings.TrimSpace(string(out)); sha != "" {
			return ref, sha, nil
		}
	}
	return "", "", fmt.Errorf("%w %q", ErrNoBase, base)
}

// Diff is the package-level convenience for the default differ.
func Diff(ctx context.Context, dir, base string) (Result, error) {
	return Differ{}.Diff(ctx, dir, base)
}

func runGit(ctx context.Context, bin, dir string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, bin, append([]string{"-C", dir}, args...)...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return out, fmt.Errorf("%w: %s", err, firstLine(msg))
		}
	}
	return out, err
}

// untrackedFiles renders each untracked path (NUL-separated, as `ls-files -z`
// prints them) as an all-added file read straight from disk. A file is read
// only when it is a regular file INSIDE dir — a symlink is listed but never
// followed, since its target can be anywhere on the machine. more reports that
// files past MaxUntracked were left out.
func untrackedFiles(dir string, z []byte) (out []File, more bool) {
	for _, p := range strings.Split(string(z), "\x00") {
		if p == "" {
			continue
		}
		if len(out) == MaxUntracked {
			return out, true
		}
		f := File{Path: p, Status: "added", Untracked: true}
		full := filepath.Join(dir, filepath.FromSlash(p))
		info, err := os.Lstat(full)
		switch {
		case err != nil || !info.Mode().IsRegular():
			f.Binary = true // not readable as text: list it, show no content
		case info.Size() > MaxFilePatchBytes:
			f.TooLarge = true
		default:
			b, err := os.ReadFile(full)
			if err != nil || bytes.IndexByte(b, 0) >= 0 {
				f.Binary = true
				break
			}
			f.Patch, f.Additions = addedPatch(string(b))
		}
		out = append(out, f)
	}
	return out, false
}

// addedPatch renders a new file's content as a single all-"+" hunk.
func addedPatch(content string) (string, int) {
	if content == "" {
		return "", 0
	}
	noEOL := !strings.HasSuffix(content, "\n")
	lines := strings.Split(strings.TrimSuffix(content, "\n"), "\n")
	var b strings.Builder
	fmt.Fprintf(&b, "@@ -0,0 +1,%d @@\n", len(lines))
	for _, l := range lines {
		b.WriteString("+")
		b.WriteString(l)
		b.WriteString("\n")
	}
	if noEOL {
		b.WriteString("\\ No newline at end of file\n")
	}
	return b.String(), len(lines)
}

// SplitPatch splits a multi-file `git diff` into per-file entries. Pure.
func SplitPatch(diff string) []File {
	var (
		files  []File
		cur    *File
		inHunk bool
		body   strings.Builder
	)
	flush := func() {
		if cur == nil {
			return
		}
		cur.Patch = body.String()
		if cur.Status == "" {
			cur.Status = "modified"
		}
		files = append(files, *cur)
		body.Reset()
		cur = nil
	}
	for _, line := range strings.SplitAfter(diff, "\n") {
		if line == "" {
			continue
		}
		bare := strings.TrimSuffix(line, "\n")
		if strings.HasPrefix(bare, "diff --git ") {
			flush()
			cur = &File{}
			inHunk = false
			cur.OldPath, cur.Path = parseDiffGitHeader(bare)
			continue
		}
		if cur == nil {
			continue
		}
		if !inHunk {
			switch {
			case strings.HasPrefix(bare, "new file mode"):
				cur.Status = "added"
			case strings.HasPrefix(bare, "deleted file mode"):
				cur.Status = "deleted"
			case strings.HasPrefix(bare, "rename from "):
				cur.OldPath = strings.TrimPrefix(bare, "rename from ")
				cur.Status = "renamed"
			case strings.HasPrefix(bare, "rename to "):
				cur.Path = strings.TrimPrefix(bare, "rename to ")
				cur.Status = "renamed"
			case strings.HasPrefix(bare, "Binary files "), bare == "GIT binary patch":
				cur.Binary = true
			case strings.HasPrefix(bare, "--- "):
				if p := stripPrefix(strings.TrimPrefix(bare, "--- "), "a/"); p != "" {
					cur.OldPath = p
				}
			case strings.HasPrefix(bare, "+++ "):
				if p := stripPrefix(strings.TrimPrefix(bare, "+++ "), "b/"); p != "" {
					cur.Path = p
				}
			case strings.HasPrefix(bare, "@@"):
				inHunk = true
			}
			if !inHunk {
				continue
			}
		}
		switch {
		case strings.HasPrefix(bare, "+"):
			cur.Additions++
		case strings.HasPrefix(bare, "-"):
			cur.Deletions++
		}
		body.WriteString(line)
	}
	flush()
	for i := range files {
		f := &files[i]
		if f.Status != "renamed" && f.OldPath == f.Path {
			f.OldPath = ""
		}
		if f.Status == "added" || f.Status == "deleted" {
			f.OldPath = ""
		}
		if f.Path == "" {
			f.Path = f.OldPath // a deletion's "+++ /dev/null"
		}
	}
	return files
}

// parseDiffGitHeader reads "diff --git a/<old> b/<new>". With quotepath off and
// no spaces in either name it splits cleanly; otherwise it splits on " b/",
// which is ambiguous only for a path that itself contains " b/" — the
// "---"/"+++" lines that follow then correct it.
func parseDiffGitHeader(h string) (oldPath, newPath string) {
	rest := strings.TrimPrefix(h, "diff --git ")
	if i := strings.Index(rest, " b/"); i >= 0 {
		return strings.TrimPrefix(rest[:i], "a/"), rest[i+3:]
	}
	return "", ""
}

// stripPrefix trims a "--- a/x" style path, and maps /dev/null to "".
func stripPrefix(p, prefix string) string {
	p = strings.TrimSuffix(p, "\t")
	if p == "/dev/null" {
		return ""
	}
	return strings.TrimPrefix(p, prefix)
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// Line is one rendered row of a parsed patch.
type Line struct {
	Kind    byte   // ' ' context, '+' added, '-' removed, '@' hunk header, '\\' no-newline marker
	OldLine int    // 1-based line in the old file; 0 when the row has none
	NewLine int    // 1-based line in the new file; 0 when the row has none
	Text    string // the content without its leading marker (the full header for '@')
}

// ParseLines numbers every row of a file's Patch. Pure; tolerant — a row it
// cannot place is kept with zero line numbers rather than dropped.
func ParseLines(patch string) []Line {
	var (
		out      []Line
		old, new int
	)
	for _, raw := range strings.Split(strings.TrimSuffix(patch, "\n"), "\n") {
		if raw == "" {
			continue
		}
		switch raw[0] {
		case '@':
			old, new = parseHunkHeader(raw)
			out = append(out, Line{Kind: '@', Text: raw})
		case '+':
			out = append(out, Line{Kind: '+', NewLine: new, Text: raw[1:]})
			new++
		case '-':
			out = append(out, Line{Kind: '-', OldLine: old, Text: raw[1:]})
			old++
		case '\\':
			out = append(out, Line{Kind: '\\', Text: raw})
		default:
			out = append(out, Line{Kind: ' ', OldLine: old, NewLine: new, Text: raw[1:]})
			old++
			new++
		}
	}
	return out
}

// parseHunkHeader reads "@@ -a,b +c,d @@" into the first old and new line.
func parseHunkHeader(h string) (old, new int) {
	fields := strings.Fields(h)
	for _, f := range fields[1:] {
		if f == "@@" {
			break
		}
		start, _, _ := strings.Cut(f[1:], ",")
		n, _ := strconv.Atoi(start)
		switch f[0] {
		case '-':
			old = n
		case '+':
			new = n
		}
	}
	return old, new
}
