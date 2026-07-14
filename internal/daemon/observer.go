package daemon

// Session observability (PLAN P1/P2): a read-only observer loop that merges
// lola's native sessions with GitHub PR state (scm), caching the result in a
// session.Store snapshot. The "sessions" socket command serves this cache — a
// client request never execs gh/tmux.

import (
	"context"
	"time"

	"github.com/sushidev-team/lola/internal/scm"
	"github.com/sushidev-team/lola/internal/session"
)

const observeInterval = 30 * time.Second

// observeExecTimeout bounds EVERY external exec (gh/tmux) of an observation
// cycle individually. The cycle runs on a WithoutCancel context (see
// safeObserve), so without per-call deadlines a single wedged gh call (dead
// network mid-TLS, an interactive prompt) would freeze the observer loop
// forever and block graceful shutdown at d.wg.Wait(). Every observer exec is
// read-only and always safe to abort.
const observeExecTimeout = 10 * time.Second

// sessionRetention: sessions not observed for this long age out of the store
// (a session that stops being upserted — a killed native runner — ages out).
const sessionRetention = 24 * time.Hour

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

// observe runs one observation cycle over lola's native sessions (PLAN P2).
// ctx is unbounded (WithoutCancel); every exec below carves its own
// observeExecTimeout deadline from it.
func (d *Daemon) observe(ctx context.Context) {
	d.observeNative(ctx)
}

// observeNative merges every native-runtime session already in the
// store with fresh facts — liveness
// via runtime.Alive (native sessions ARE tmux sessions, so TmuxName is the
// session ID), PR state via the session's repo (recorded at spawn from the
// poll's project, config.Project.Repo; the project registry is the fallback
// for adopted records), and status via nativeStatus. A dead pane whose PR is
// not merged becomes "dead"; a stale needs_input just stays needs_input —
// P2 never auto-kills, no matter how old. Settled terminal records (dead, or
// merged with the pane gone) are not re-written, so their LastSeen freezes
// and sessionRetention ages them out of the store. Each record is written via
// Store.Update (atomic read-modify-write), never a stale-snapshot Upsert —
// hook events land concurrently and must not be erased.
func (d *Daemon) observeNative(ctx context.Context) {
	d.mu.Lock()
	nat := d.native
	repoByProject := make(map[string]string, len(d.cfg.Projects))
	for _, p := range d.cfg.Projects {
		repoByProject[p.Name] = p.Repo
	}
	d.mu.Unlock()
	if nat == nil {
		return
	}

	touched := false
	for _, s := range d.sessions.Snapshot() {
		if s.Source != "native" {
			continue
		}
		cctx, cancel := context.WithTimeout(ctx, observeExecTimeout)
		alive := nat.Alive(cctx, s)
		cancel()

		repo := s.Repo
		if repo == "" {
			repo = repoByProject[s.Project]
		}

		// PR state, log-and-continue like the AO half: keep the last known
		// facts unless this cycle produced an authoritative answer.
		var pr *scm.PR
		prKnown := false
		if s.Branch != "" && repo != "" {
			cctx, cancel := context.WithTimeout(ctx, observeExecTimeout)
			p, err := d.prForBranch(cctx, repo, s.Branch)
			cancel()
			if err != nil {
				d.logf("", "observe: PR check for native %s (branch %s in %s) failed: %v", s.ID, s.Branch, repo, err)
			} else {
				pr, prKnown = p, true
			}
		}

		// Merge this cycle's facts as ONE atomic read-modify-write. The execs
		// above take seconds, and a hook event (needs_input / idle /
		// session_ended) can land on the record meanwhile — deriving the
		// status from this loop's stale snapshot and Upserting it back would
		// silently erase that transition, and permanently so: an agent
		// blocked on a permission prompt fires no further hooks. Update
		// re-reads the CURRENT record under the store lock, so a concurrent
		// needs_input flows into nativeStatus and is preserved.
		becameDead, applied := false, false
		updated, known := d.sessions.Update(s.ID, func(cur *session.Session) bool {
			if cur.Repo == "" {
				cur.Repo = repo
			}
			if prKnown {
				cur.PR = pr
			}
			status := nativeStatus(cur.Status, alive, cur.PR)
			if !alive && status == cur.Status {
				// Already-settled terminal record: discard so LastSeen
				// freezes and the store's retention prune eventually drops it.
				return false
			}
			becameDead = status == "dead" && cur.Status != "dead"
			cur.Status = status
			if cur.TmuxName == "" {
				cur.TmuxName = cur.ID
			}
			applied = true
			return true
		})
		if becameDead {
			d.logf("", "observe: native session %s pane is gone without a merged PR → dead", s.ID)
		}
		if applied {
			touched = true
		}
		// React to the just-updated record (PLAN P3): send-keys / notify /
		// merged-cleanup, each fired once per transition and gated on AtPrompt.
		// updated is the current record even when the merge above discarded
		// (a settled-merged session still needs its cleanup fired once).
		if known {
			d.react(ctx, updated)
		}
	}
	if !touched {
		return
	}
	d.sessions.PruneOlderThan(sessionRetention)
	if err := d.sessions.Save(); err != nil {
		d.logf("", "observe: persist sessions: %v", err)
	}
}

// nativeStatus derives a native session's status for this cycle from its
// hook-driven current status, tmux pane liveness, and the PR facts in hand:
//
//   - Known PR facts drive scm.DeriveStatus — the shared status vocabulary.
//   - A hook-reported needs_input outranks any PR-derived status while the
//     pane is alive (a human is being waited on), except "merged".
//   - No PR facts → the hook-driven status stands (working / idle / …).
//   - A dead tmux pane forces "dead" unless the PR is merged — a merged PR is
//     the one legitimate way for a native session to end in P2.
func nativeStatus(current string, alive bool, pr *scm.PR) string {
	status := current
	if pr != nil {
		status = scm.DeriveStatus(alive, pr)
	}
	if alive && current == "needs_input" && status != "merged" {
		status = "needs_input"
	}
	if !alive && status != "merged" {
		return "dead"
	}
	return status
}
