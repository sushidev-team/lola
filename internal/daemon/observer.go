package daemon

// Session observability (PLAN P1): a read-only observer loop that correlates
// AO's live sessions with Linear branch names (fed by ticks), GitHub PR state
// (scm) and tmux sessions, and caches the result in a session.Store snapshot.
// The "sessions" socket command serves this cache — a client request never
// execs ao/gh/tmux.

import (
	"context"
	"strings"
	"time"

	"github.com/sushidev-team/lola/internal/scm"
	"github.com/sushidev-team/lola/internal/session"
)

const observeInterval = 30 * time.Second

// observeExecTimeout bounds EVERY external exec (ao/gh/tmux) of an
// observation cycle individually. The cycle runs on a WithoutCancel context
// (see safeObserve), so without per-call deadlines a single wedged gh call
// (dead network mid-TLS, an interactive prompt) would freeze the observer
// loop forever and block graceful shutdown at d.wg.Wait(). Unlike a tick's
// spawn/label-flip, every observer exec is read-only and always safe to
// abort. Same pattern as aoProjectCheckTimeout (server.go).
const observeExecTimeout = 10 * time.Second

// sessionRetention: sessions not observed for this long age out of the store
// (dead AO sessions disappear from `ao session ls` and simply stop being
// upserted).
const sessionRetention = 24 * time.Hour

// branchPruneGrace: recently recorded branch entries survive pruneBranches
// even when their identifier is neither live in AO nor in-flight. A tick
// records branches for ALL matched issues before dispatching them one by one
// (each `ao spawn` exec takes seconds), so an observer cycle firing mid-tick
// would otherwise prune the branches of issues still waiting in the dispatch
// queue — losing them for good in label mode, where the flipped trigger label
// removes the issue from all future tick queries.
const branchPruneGrace = 15 * time.Minute

// branchInfo is one tick-recorded dispatch fact: the Linear branch name for
// an issue identifier plus the owning poll's repo ("owner/name") so the
// observer knows where to look for the PR, and when it was recorded (prune
// grace).
type branchInfo struct {
	branch string
	repo   string
	at     time.Time
}

// recordBranch stores identifier→branch/repo for the observer. Ticks call it
// for every matched issue (the issue data is already fetched there — the
// cheapest correct source for Linear's branchName). Empty identifiers or
// branches are ignored.
func (d *Daemon) recordBranch(identifier, branch, repo string) {
	if identifier == "" || branch == "" {
		return
	}
	d.branchMu.Lock()
	d.branches[identifier] = branchInfo{branch: branch, repo: repo, at: time.Now()}
	d.branchMu.Unlock()
}

// branchFor returns the recorded branch and repo for identifier, or "", "".
func (d *Daemon) branchFor(identifier string) (branch, repo string) {
	d.branchMu.Lock()
	defer d.branchMu.Unlock()
	bi := d.branches[identifier]
	return bi.branch, bi.repo
}

// pruneBranches bounds the branch map: entries whose identifier has neither a
// live AO session nor an in-flight dispatch claim are dropped — unless they
// were recorded within branchPruneGrace (see there: mid-tick race). (Seen
// state is keyed by issue UUID, not identifier, so the in-flight set — which
// carries identifiers — stands in for "recently dispatched, not yet visible
// in AO".)
func (d *Daemon) pruneBranches(liveIdents map[string]bool) {
	inflight := map[string]bool{}
	for _, e := range d.inflight.Entries() {
		inflight[e.Identifier] = true
	}
	cutoff := time.Now().Add(-branchPruneGrace)
	d.branchMu.Lock()
	defer d.branchMu.Unlock()
	for ident, bi := range d.branches {
		if !liveIdents[ident] && !inflight[ident] && bi.at.Before(cutoff) {
			delete(d.branches, ident)
		}
	}
}

// observeLoop runs observation cycles every observeInterval (plus one
// immediately at startup so the TUI has data right away) until shutdown.
// Same lifecycle discipline as reconcileLoop: registered on d.wg, stops on
// ctx cancellation.
func (d *Daemon) observeLoop(ctx context.Context) {
	defer d.wg.Done()
	t := time.NewTicker(observeInterval)
	defer t.Stop()
	d.safeObserve(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			d.safeObserve(ctx)
		}
	}
}

// safeObserve runs one cycle; a panic or error never crashes the daemon —
// problems surface in the daemon log only.
func (d *Daemon) safeObserve(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			d.logf("", "observe panic (daemon keeps running): %v", r)
		}
	}()
	// Shield the in-flight cycle from the shutdown cancellation like safeTick:
	// a cancelled context would SIGKILL a running gh/tmux exec and could leave
	// a half-written store. The loop itself still stops on ctx.Done, and every
	// exec inside the cycle is individually bounded by observeExecTimeout so
	// the shield can never turn into an indefinite hang.
	d.observe(context.WithoutCancel(ctx))
}

// aoAlive reports whether an AO status still counts as a live agent for
// status derivation. Anything AO reports that is not terminal-ish (merged /
// terminated / idle) is alive — including attention states like needs_input.
func aoAlive(status string) bool {
	switch status {
	case "merged", "terminated", "idle":
		return false
	}
	return true
}

// tmuxNameMatches reports whether tmux session name refers to AO session id:
// the name equals id or contains id delimited by non-alphanumeric characters
// (or the string ends). Plain substring matching must not be used here: AO
// IDs are <prefix>-<n>, so once ten sessions exist "sess-1" is a substring of
// "sess-12" and would claim the wrong pane — preview and attach would target
// another agent.
func tmuxNameMatches(name, id string) bool {
	if id == "" {
		return false
	}
	for from := 0; ; {
		i := strings.Index(name[from:], id)
		if i < 0 {
			return false
		}
		start := from + i
		end := start + len(id)
		beforeOK := start == 0 || !isAlnum(name[start-1])
		afterOK := end == len(name) || !isAlnum(name[end])
		if beforeOK && afterOK {
			return true
		}
		from = start + 1
	}
}

func isAlnum(b byte) bool {
	return b >= '0' && b <= '9' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z'
}

// observe runs one observation cycle: AO sessions → branch → PR → tmux →
// store upsert + prune + save. ctx is unbounded (WithoutCancel); every exec
// below carves its own observeExecTimeout deadline from it.
func (d *Daemon) observe(ctx context.Context) {
	d.mu.Lock()
	aoc := d.aoc // snapshot under d.mu: reload may swap the client concurrently
	d.mu.Unlock()

	// AO down → skip the cycle silently: status already reports AO down, and
	// logging it every 30s would drown the daemon log.
	cctx, cancel := context.WithTimeout(ctx, observeExecTimeout)
	reachable := aoc.Reachable(cctx)
	cancel()
	if !reachable {
		return
	}
	cctx, cancel = context.WithTimeout(ctx, observeExecTimeout)
	aoSessions, err := aoc.LiveSessions(cctx)
	cancel()
	if err != nil {
		d.logf("", "observe: ao session ls failed: %v", err)
		return
	}

	// tmux listing once per cycle, best effort: desktop AO may not use tmux
	// at all, so an unavailable tmux only costs the TmuxName correlation.
	var tmuxNames []string
	cctx, cancel = context.WithTimeout(ctx, observeExecTimeout)
	tms, err := d.tmuxSessions(cctx)
	cancel()
	if err != nil {
		d.logf("", "observe: tmux ls failed (tmux correlation skipped): %v", err)
	} else {
		for _, tm := range tms {
			tmuxNames = append(tmuxNames, tm.Name)
		}
	}

	liveIdents := make(map[string]bool, len(aoSessions))
	for _, s := range aoSessions {
		if s.IssueID != "" {
			liveIdents[s.IssueID] = true
		}
	}
	d.pruneBranches(liveIdents)

	for _, s := range aoSessions {
		sess := session.Session{
			Source:   "ao",
			ID:       s.ID,
			Project:  s.Project,
			Issue:    s.IssueID,
			AOStatus: s.Status,
		}
		prev, hasPrev := d.sessions.Get(s.ID)

		branch, repo := d.branchFor(s.IssueID)
		if branch == "" && hasPrev {
			// The branch map is in-memory and tick-fed: after a daemon restart
			// it is empty, and in label mode the flipped trigger label removes
			// the issue from tick queries so it never refills. The persisted
			// record is the durable source — never clobber good facts with
			// empties.
			branch, repo = prev.Branch, prev.Repo
		}
		sess.Branch, sess.Repo = branch, repo

		// PR state, sequential and log-and-continue: one failing gh call must
		// not lose the rest of the cycle.
		var pr *scm.PR
		prKnown := false
		if branch != "" && repo != "" {
			cctx, cancel := context.WithTimeout(ctx, observeExecTimeout)
			p, err := d.prForBranch(cctx, repo, branch)
			cancel()
			if err != nil {
				d.logf("", "observe: PR check for %s (branch %s in %s) failed: %v", s.IssueID, branch, repo, err)
			} else {
				pr, prKnown = p, true
			}
		}
		if !prKnown && hasPrev {
			// This cycle could not check (no branch/repo on record, or gh
			// failed): keep the last known PR facts rather than flipping e.g.
			// a persisted ci_failed back to "working". Only an authoritative
			// answer (gh succeeded, prKnown) may replace or clear them.
			pr = prev.PR
		}
		sess.PR = pr

		// Status precedence: AO attention states (needs_input / no_signal)
		// outrank the PR-derived status — a session waiting for a human must
		// never be masked by e.g. "ci_pending". Everything else is the single
		// deterministic scm.DeriveStatus; the raw AOStatus stays visible on
		// the session record either way.
		sess.Status = scm.DeriveStatus(aoAlive(s.Status), pr)
		switch s.Status {
		case "needs_input", "no_signal":
			sess.Status = s.Status
		}

		// tmux correlation is best effort: a tmux session whose name contains
		// the AO session ID (delimited, see tmuxNameMatches) claims it;
		// absent is fine.
		if s.ID != "" {
			for _, name := range tmuxNames {
				if tmuxNameMatches(name, s.ID) {
					sess.TmuxName = name
					break
				}
			}
		}

		d.sessions.Upsert(sess)
	}

	d.sessions.PruneOlderThan(sessionRetention)
	if err := d.sessions.Save(); err != nil {
		d.logf("", "observe: persist sessions: %v", err)
	}
}
