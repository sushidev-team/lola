package projectgit

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// ChangeLines stages (area "unstaged") or unstages (area "staged") only the
// chosen changed lines of one tracked file. The caller sends the patch it drew
// and the indexes of its lines; the patch must equal the diff Git reports now,
// so a click never lands on lines that moved. The partial patch is built here
// and applied to the index only: working files are never touched.
func (c Client) ChangeLines(ctx context.Context, dir, area, path, patch string, lines []int, stage bool) error {
	if !validPath(path) {
		return errors.New("invalid file path")
	}
	if (stage && area != "unstaged") || (!stage && area != "staged") {
		return errors.New("lines can only be staged from unstaged changes or unstaged from staged changes")
	}
	if len(lines) == 0 || len(lines) > 100000 {
		return errors.New("select at least one changed line")
	}
	if err := c.requireIdle(ctx, dir); err != nil {
		return err
	}
	current, err := c.Diff(ctx, dir, "", "", area, path)
	if err != nil {
		return err
	}
	if len(current.Files) != 1 || current.Files[0].Path != path || current.Files[0].Patch != patch {
		return errors.New("this file changed since the diff was drawn; refresh and select the lines again")
	}
	file := current.Files[0]
	if file.Binary || file.TooLarge || file.Status != "modified" || file.OldPath != "" && file.OldPath != file.Path {
		return errors.New("only lines of modified text files can be staged one by one")
	}
	partial, err := partialPatch(patch, lines, stage)
	if err != nil {
		return err
	}
	header := fmt.Sprintf("diff --git a/%s b/%s\n--- a/%s\n+++ b/%s\n", path, path, path, path)
	args := []string{"-c", "core.fsmonitor=false", "apply", "--cached", "--recount", "--whitespace=nowarn"}
	if !stage {
		args = append(args, "--reverse")
	}
	_, err = c.git(ctx, dir, header+partial, append(args, "-")...)
	return err
}

// partialPatch keeps the hunks of patch that hold a chosen change. A change
// that is not chosen is left as the index has it: when staging, an unchosen
// removal stays as context and an unchosen addition is dropped; when
// unstaging (applied in reverse) the roles swap. The side that is the index
// keeps its original positions; the other side is renumbered, because git
// apply trusts those positions and would match a similar block elsewhere.
func partialPatch(patch string, chosen []int, stage bool) (string, error) {
	pick := map[int]bool{}
	for _, i := range chosen {
		pick[i] = true
	}
	rows := strings.Split(strings.TrimSuffix(patch, "\n"), "\n")
	for i := range pick {
		if i < 0 || i >= len(rows) || rows[i] == "" || (rows[i][0] != '+' && rows[i][0] != '-') {
			return "", errors.New("select only changed lines")
		}
	}
	type hunk struct {
		oldStart, newStart int
		suffix             string
		lines              []string
		oldN, newN         int
		changed            bool
	}
	var hunks []*hunk
	var cur *hunk
	dropping := false
	for i, row := range rows {
		switch {
		case strings.HasPrefix(row, "@@"):
			m := hunkHeader.FindStringSubmatch(row)
			if m == nil {
				return "", errors.New("unreadable hunk header")
			}
			cur = &hunk{oldStart: atoi(m[1]), newStart: atoi(m[2]), suffix: m[3]}
			hunks = append(hunks, cur)
		case cur == nil || row == "":
		case row[0] == '\\':
			if !dropping {
				cur.lines = append(cur.lines, row)
			}
		case row[0] == '+' || row[0] == '-':
			add := row[0] == '+'
			switch {
			case pick[i]:
				cur.lines = append(cur.lines, row)
				cur.changed, dropping = true, false
				if add {
					cur.newN++
				} else {
					cur.oldN++
				}
			case add == stage:
				// Not chosen and absent from the target: leave it out.
				dropping = true
			default:
				// Not chosen and already in the target: keep it as context.
				cur.lines = append(cur.lines, " "+row[1:])
				cur.oldN++
				cur.newN++
				dropping = false
			}
		default:
			cur.lines = append(cur.lines, row)
			cur.oldN++
			cur.newN++
			dropping = false
		}
	}
	var out strings.Builder
	shift := 0 // lines the kept hunks so far add to the new side
	kept := false
	for _, h := range hunks {
		if !h.changed {
			continue
		}
		kept = true
		oldStart, newStart := h.oldStart, h.newStart
		if stage {
			first := oldStart
			if h.oldN == 0 {
				first++
			}
			first += shift
			newStart = first
			if h.newN == 0 {
				newStart--
			}
		} else {
			first := newStart
			if h.newN == 0 {
				first++
			}
			first -= shift
			oldStart = first
			if h.oldN == 0 {
				oldStart--
			}
		}
		fmt.Fprintf(&out, "@@ -%d,%d +%d,%d @@%s\n", oldStart, h.oldN, newStart, h.newN, h.suffix)
		for _, l := range h.lines {
			out.WriteString(l + "\n")
		}
		shift += h.newN - h.oldN
	}
	if !kept {
		return "", errors.New("select at least one changed line")
	}
	return out.String(), nil
}

var hunkHeader = regexp.MustCompile(`^@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@(.*)$`)

func atoi(s string) int { n, _ := strconv.Atoi(s); return n }

// ChangeLinesAndStatus is ChangeLines followed by the authoritative file list.
func (c Client) ChangeLinesAndStatus(ctx context.Context, dir, area, path, patch string, lines []int, stage bool) ([]File, error) {
	if err := c.ChangeLines(ctx, dir, area, path, patch, lines, stage); err != nil {
		return nil, err
	}
	return c.status(ctx, dir)
}
