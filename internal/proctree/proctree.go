// Package proctree is the ONE place lola signals a process it did not start
// itself: it reads the machine's process table and takes whole process GROUPS
// down.
//
// It exists because a process group is not the whole story. tmux gives each
// pane a fresh session and process group, so everything `composer dev` spawns
// is reachable by signalling the PANE's group — that is what internal/tmux's
// KillSessionTree relies on. But a coding agent's Bash tool deliberately puts
// every command it runs in its OWN group (so it can time one out without
// touching the agent), and such a child therefore SURVIVES a group kill of the
// pane. A `php artisan serve --port=8000` an agent started that way outlives
// its session, keeps the port bound, and the next session's dev server quietly
// lands on 8001 — the exact failure the dev tabs exist to remove. Reaching it
// needs the ppid tree, not the process group.
//
// Stdlib only (one `ps` exec + syscall), so internal/tmux and the daemon's dev
// sweep can both use it without importing each other.
package proctree

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"sort"
	"strconv"
	"strings"
)

// Proc is one row of the process table: a pid, its parent, and the process
// group it belongs to.
type Proc struct {
	PID  int
	PPID int
	PGID int
}

// Table is the whole process table, as read in one pass.
type Table []Proc

// maxDepth bounds the descendant walk. A ppid cycle is impossible on a sane
// kernel, but this code signals processes — it does not get to loop forever on
// a table that surprises it.
const maxDepth = 64

// Read snapshots the process table with one `ps` exec. Rows `ps` prints that
// this cannot parse are skipped rather than failing the read: a single odd line
// must not cost the caller the whole tree.
func Read(ctx context.Context) (Table, error) {
	cmd := exec.CommandContext(ctx, "ps", "-eo", "pid=,ppid=,pgid=")
	var out, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("ps: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	var tbl Table
	for _, line := range strings.Split(out.String(), "\n") {
		f := strings.Fields(line)
		if len(f) < 3 {
			continue
		}
		pid, err1 := strconv.Atoi(f[0])
		ppid, err2 := strconv.Atoi(f[1])
		pgid, err3 := strconv.Atoi(f[2])
		if err1 != nil || err2 != nil || err3 != nil || pid <= 0 {
			continue
		}
		tbl = append(tbl, Proc{PID: pid, PPID: ppid, PGID: pgid})
	}
	if len(tbl) == 0 {
		return nil, fmt.Errorf("ps: no parsable rows")
	}
	return tbl, nil
}

// Group returns the process group pid belongs to, or 0 when the table does not
// know the pid (it exited between the snapshot and the question).
func (t Table) Group(pid int) int {
	for _, p := range t {
		if p.PID == pid {
			return p.PGID
		}
	}
	return 0
}

// Parent returns pid's parent, or 0 when unknown.
func (t Table) Parent(pid int) int {
	for _, p := range t {
		if p.PID == pid {
			return p.PPID
		}
	}
	return 0
}

// Descendants returns every pid below root, breadth first. root itself is NOT
// included.
func (t Table) Descendants(root int) []int {
	if root <= 1 {
		return nil
	}
	children := map[int][]int{}
	for _, p := range t {
		if p.PID == p.PPID {
			continue // a self-parenting row would loop the walk
		}
		children[p.PPID] = append(children[p.PPID], p.PID)
	}
	var out []int
	seen := map[int]bool{root: true}
	frontier := []int{root}
	for depth := 0; depth < maxDepth && len(frontier) > 0; depth++ {
		var next []int
		for _, pid := range frontier {
			for _, kid := range children[pid] {
				if seen[kid] {
					continue
				}
				seen[kid] = true
				out = append(out, kid)
				next = append(next, kid)
			}
		}
		frontier = next
	}
	sort.Ints(out)
	return out
}

// TreeGroups returns the distinct process groups led by root and everything
// below it — the set a caller must signal to take the whole tree down, INCLUDING
// the children that left root's own group.
//
// Groups 0 and 1 are never returned: signalling them means "every process we may
// signal" and "init", and a lookup that produced either has gone wrong.
func (t Table) TreeGroups(root int) []int {
	pids := append([]int{root}, t.Descendants(root)...)
	seen := map[int]bool{}
	var out []int
	for _, pid := range pids {
		pgid := t.Group(pid)
		if pgid <= 1 || seen[pgid] {
			continue
		}
		seen[pgid] = true
		out = append(out, pgid)
	}
	sort.Ints(out)
	return out
}
