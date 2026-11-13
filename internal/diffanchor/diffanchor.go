// Package diffanchor answers ONE question about a unified git diff: which
// (path, line) pairs may carry a GitHub inline review comment.
//
// It exists because GitHub's review API is ATOMIC and unforgiving. A review
// created with `POST /repos/{repo}/pulls/{n}/reviews` fails as a WHOLE with
// HTTP 422 when even one of its comments names a line that is not part of the
// PR's diff — and a review finding routinely names one: the reviewer is told to
// anchor at "the smallest line that carries the defect", which is very often an
// UNCHANGED line the diff never shows. So the diff has to be consulted before
// anything is posted, and a finding whose line cannot be anchored has to travel
// in the review's summary body instead of as a thread.
//
// It is a pure text leaf (stdlib only, no exec, no state), for the same reason
// internal/devurl and internal/reviewmd are: the daemon fetches the diff, this
// package decides what is anchorable, internal/reviewmd decides how it reads,
// and internal/scm posts it. None of them may import the others.
//
// What counts as anchorable is exactly GitHub's own rule, on the RIGHT side of
// the diff only:
//
//   - an ADDED line (`+`) — the common case for a review finding;
//   - a CONTEXT line (` `) — inside a hunk it is part of the diff and GitHub
//     accepts a comment on it;
//   - never a REMOVED line (`-`): it has no right-side number at all (a comment
//     there needs side=LEFT, which lola never posts — its findings are about the
//     code as it now stands);
//   - never a file with no right side (`+++ /dev/null`, i.e. deleted), and never
//     a file with no patch at all (binary, or a diff GitHub itself omitted).
//
// Parsing FAILS OPEN in one direction only: anything it does not understand
// yields FEWER anchors, never a wrong one. A truncated diff (the fetch is
// size-bounded), an unfamiliar header, a hunk whose counts do not add up — each
// costs some findings their inline thread and sends them to the summary body,
// which is the harmless direction. A wrong anchor would put a finding on an
// unrelated line, or 422 the entire review.
package diffanchor

import (
	"strconv"
	"strings"
)

// Set is the anchorable right-side lines of one diff, keyed by repo-relative
// path. The zero value is usable and empty (Has reports false for everything),
// so a caller that could not fetch a diff needs no nil check.
type Set struct {
	lines map[string]map[int]bool
}

// hunkHeaderRe is deliberately NOT a regexp: a hunk header is parsed by hand
// (see parseHunkHeader) so a malformed one degrades to "skip this hunk" instead
// of matching something unintended.

// Parse reads a unified diff (as `git diff` / `gh pr diff` emit it) and returns
// its anchorable right-side lines. A nil/empty diff yields an empty Set.
func Parse(diff string) Set {
	s := Set{lines: map[string]map[int]bool{}}
	path := ""  // current file's right-side path; "" while unknown/unanchorable
	right := 0  // next right-side line number inside the current hunk
	remain := 0 // right-side lines still expected in the current hunk
	inHunk := false

	for raw := range strings.SplitSeq(diff, "\n") {
		line := strings.TrimRight(raw, "\r")

		switch {
		case strings.HasPrefix(line, "diff --git "):
			// A new file section: forget everything about the previous one. The
			// path comes from the `+++` line below, never from here — this line's
			// two paths are ambiguous for renames and may be git-quoted.
			path, inHunk, right, remain = "", false, 0, 0
			continue
		case strings.HasPrefix(line, "+++ "):
			path, inHunk = newPath(strings.TrimSpace(line[4:])), false
			continue
		case strings.HasPrefix(line, "--- "):
			// Old-side header: carries no right-side information.
			continue
		case strings.HasPrefix(line, "@@"):
			start, count, ok := parseHunkHeader(line)
			if !ok || path == "" {
				inHunk = false // unparseable header (or no right side): skip it
				continue
			}
			right, remain, inHunk = start, count, true
			continue
		}

		if !inHunk || path == "" {
			continue // prologue, "Binary files … differ", trailing junk
		}
		if remain <= 0 {
			inHunk = false // hunk consumed; wait for the next @@ / file header
			continue
		}

		switch {
		case line == "": // the diff's own trailing newline, or an empty context line
			s.add(path, right)
			right, remain = right+1, remain-1
		case line[0] == '+' || line[0] == ' ':
			s.add(path, right)
			right, remain = right+1, remain-1
		case line[0] == '-':
			// Removed: no right-side line number, and the hunk's right-side
			// budget is untouched.
		case line[0] == '\\':
			// "\ No newline at end of file" — annotation, not a line.
		default:
			// Not a diff body line at all: the hunk ended without its counts
			// being satisfied (a clipped diff, or a format we don't know).
			inHunk = false
		}
	}
	return s
}

// add records one anchorable line.
func (s Set) add(path string, line int) {
	if s.lines == nil || line <= 0 {
		return
	}
	set := s.lines[path]
	if set == nil {
		set = map[int]bool{}
		s.lines[path] = set
	}
	set[line] = true
}

// Has reports whether a comment may be anchored at path:line.
func (s Set) Has(path string, line int) bool {
	return s.lines[path][line]
}

// HasFile reports whether the diff carries an anchorable patch for path. It is
// the weaker question Has answers per line: a caller can fall back to a
// FILE-level comment (GitHub's subject_type=file, which needs no line) when the
// exact line is not in the diff but the file itself was touched.
func (s Set) HasFile(path string) bool {
	return len(s.lines[path]) > 0
}

// Len is the number of anchorable lines across all files (test/telemetry only).
func (s Set) Len() int {
	n := 0
	for _, set := range s.lines {
		n += len(set)
	}
	return n
}

// Nearest resolves a REPORTED location to a line that can actually carry a
// comment: line itself when it is anchorable, else the closest anchorable line
// in the same file within window, preferring the smaller distance and, on a tie,
// the EARLIER line (the defect's declaration reads before its use).
//
// The snap is what makes the inline transport worth having: a reviewer anchors
// at the line that carries the defect, which is frequently a context line just
// outside the hunk, and window is the "same statement, same block" tolerance.
// It is never silent — the caller renders the reported location in the comment
// body whenever the anchor moved (see reviewmd.RenderInline) — because a comment
// that appears on a line it is not about, with nothing saying so, is worse than
// one in the summary body.
//
// window <= 0 disables snapping (exact matches only).
func (s Set) Nearest(path string, line, window int) (int, bool) {
	if line <= 0 {
		return 0, false
	}
	if s.Has(path, line) {
		return line, true
	}
	if window <= 0 {
		return 0, false
	}
	for d := 1; d <= window; d++ {
		if s.Has(path, line-d) {
			return line - d, true
		}
		if s.Has(path, line+d) {
			return line + d, true
		}
	}
	return 0, false
}

// newPath extracts the right-side repo-relative path from a `+++ ` header.
// Returns "" when the file has no right side (`/dev/null`, i.e. deleted) or the
// header cannot be read — both mean "nothing here can be anchored".
func newPath(s string) string {
	// git appends nothing after the path in its own diffs, but a diff produced
	// with timestamps carries a TAB-separated one; cut at the first tab.
	if i := strings.IndexByte(s, '\t'); i >= 0 {
		s = s[:i]
	}
	s = strings.TrimSpace(s)
	if s == "" || s == "/dev/null" {
		return ""
	}
	// A path with a special character is git-quoted ("a/pkg/\"odd\".go").
	if strings.HasPrefix(s, `"`) {
		unq, err := strconv.Unquote(s)
		if err != nil {
			return "" // fail open: no anchors for a path we cannot read exactly
		}
		s = unq
	}
	// Strip git's destination prefix. `--dst-prefix` can change it, so only the
	// default "b/" is stripped; anything else keeps the path verbatim and simply
	// fails to match a finding's location (fewer anchors, never a wrong one).
	return strings.TrimPrefix(s, "b/")
}

// parseHunkHeader reads the right-side start line and line count out of
// "@@ -12,7 +14,9 @@ func foo()". A single-line hunk omits the count
// ("+14" ⇒ count 1); a hunk with count 0 (a pure deletion) yields ok with
// count 0, so nothing is anchored in it.
func parseHunkHeader(line string) (start, count int, ok bool) {
	i := strings.IndexByte(line, '+')
	if i < 0 {
		return 0, 0, false
	}
	rest := line[i+1:]
	// The right-side range ends at the next space (or the closing "@@").
	if j := strings.IndexAny(rest, " @"); j >= 0 {
		rest = rest[:j]
	}
	startStr, countStr := rest, "1"
	if j := strings.IndexByte(rest, ','); j >= 0 {
		startStr, countStr = rest[:j], rest[j+1:]
	}
	start, err := strconv.Atoi(startStr)
	if err != nil || start < 0 {
		return 0, 0, false
	}
	count, err = strconv.Atoi(countStr)
	if err != nil || count < 0 {
		return 0, 0, false
	}
	return start, count, true
}
