package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/linear"
	"github.com/sushidev-team/lola/internal/session"
	"github.com/sushidev-team/lola/internal/state"
)

const reconcileInterval = 5 * time.Minute

// orphanTimeout: config.toml has no orphan_timeout field (yet), so the SPEC
// default of 15m is hardcoded here.
const orphanTimeout = 15 * time.Minute

func (d *Daemon) reconcileLoop(ctx context.Context) {
	defer d.wg.Done()
	t := time.NewTicker(reconcileInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			d.safeReconcile(ctx)
		}
	}
}

func (d *Daemon) safeReconcile(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			d.logf("", "reconcile panic (daemon keeps running): %v", r)
		}
	}()
	if d.knowledge != nil {
		d.maintainKnowledge(ctx, d.knowledge.List())
	}
	d.reconcile(ctx)
}

// reconcile per SPEC "Reconciliation pass": issues still carrying the
// on_sent_set_label with no counted session and no open PR after
// orphan_timeout are reverted to their trigger labels (the exact match_labels
// the flip stripped are restored, sent label removed) and cleared from seen +
// in-flight so they re-queue.
//
// A native session "counts for" an issue while its tmux pane is alive (see
// nativeSessionPresent) — a dead pane is exactly the native orphan condition.
func (d *Daemon) reconcile(ctx context.Context) {
	d.mu.Lock()
	polls := d.cfg.PollingProjects() // returns a fresh slice
	d.mu.Unlock()
	now := time.Now()

	counted := map[string]bool{} // Linear identifier -> has a counted session
	for _, s := range d.sessions.Snapshot() {
		if s.Source == "native" && s.Issue != "" && nativeSessionPresent(s) {
			counted[s.Issue] = true
		}
	}

	// Clear in-flight claims whose issue has no counted session anymore. The
	// orphanTimeout grace avoids racing a spawn that hasn't produced a live
	// session yet. This is the only path that releases a claim after a
	// successful spawn, so it must always run — otherwise every dispatched
	// issue stays claimed for the daemon's lifetime and can never re-dispatch
	// (e.g. after re-adding the trigger label post-merge).
	for uuid, e := range d.inflight.Entries() {
		if !counted[e.Identifier] && now.Sub(e.AddedAt) > orphanTimeout {
			d.inflight.Remove(uuid)
			d.logf("", "reconcile: cleared stale in-flight claim on %s", e.Identifier)
		}
	}
	d.pruneLeases()

	api, err := d.ensureLinear()
	if err != nil {
		d.logf("", "reconcile: linear unavailable: %v", err)
		return
	}

	for _, p := range polls {
		if !p.Enabled || p.DedupMode != "label" || p.OnSentSetLabel == "" {
			continue
		}
		d.reconcilePoll(ctx, api, p, counted, now)
	}
}

// nativeSessionPresent reports whether a native session still accounts for its
// issue in the orphan reconciliation (state.Present over the two AXES). The set
// is deliberately WIDER than the budget's NativeLiveCounted — a parked session
// (approved, review_pending, …) holds no agent slot but must still shield its
// issue from an orphan revert: its pane is alive and its work is delivered. A
// closed PR no longer shields (a change from the pre-axis table): the work was
// explicitly rejected, so the issue must become revertable instead of being
// shielded forever by a lingering pane.
//
// Taking the SESSION rather than its rolled-up status is what lets the shield
// see both reasons a session is gone: the rollup ranks merged over a dead pane
// and any open PR over an exited agent, so a session whose agent had ended kept
// shielding its issue behind the PR's delivery word. EnsureAxes covers a
// pre-axis snapshot record; it is a no-op on everything the observer writes.
func nativeSessionPresent(s session.Session) bool {
	s.EnsureAxes()
	return state.Present(s.AgentState, s.Delivery)
}

// nativeSessionForIssue returns the stored native session working on the
// Linear identifier, or ok=false when none is on record.
func (d *Daemon) nativeSessionForIssue(identifier string) (session.Session, bool) {
	if all := d.nativeSessionsForIssue(identifier); len(all) > 0 {
		return all[0], true
	}
	return session.Session{}, false
}

// nativeSessionsForIssue returns every stored native session working on the
// Linear identifier (several for a best-of group), the kept attempt first.
func (d *Daemon) nativeSessionsForIssue(identifier string) []session.Session {
	if identifier == "" {
		return nil
	}
	var out []session.Session
	for _, s := range d.sessions.Snapshot() {
		if s.Source == "native" && s.Issue == identifier {
			out = append(out, s)
		}
	}
	slices.SortStableFunc(out, func(a, b session.Session) int {
		switch {
		case a.BestOfWinner == b.BestOfWinner:
			return 0
		case a.BestOfWinner:
			return -1
		}
		return 1
	})
	return out
}

func (d *Daemon) reconcilePoll(ctx context.Context, api linear.API, p config.Project, counted map[string]bool, now time.Time) {
	// Serialize with ticks for this poll: both sides do a load-modify-save
	// of the same seen map, and an unsynchronized interleave loses updates
	// (a tick's fresh entry erased, or a reverted orphan resurrected).
	mu := d.tickMutex(p.Name)
	mu.Lock()
	defer mu.Unlock()

	// Repo the PR check runs against when no session record carries one:
	// the poll's own `repo`, else its [[project]]'s repo (PollRepo).
	d.mu.Lock()
	pollRepo := d.cfg.PollRepo(&p)
	members := d.cfg.StackMembers(p.Name)
	d.mu.Unlock()

	// Find issues currently carrying set_label: a minimal poll copy keeping
	// only team/project scope. Cycle, states and assignee are cleared so
	// orphans that moved since dispatch are still found.
	fp := config.Project{
		Name:         p.Name,
		TeamID:       p.TeamID,
		ProjectID:    p.ProjectID,
		CycleMode:    "none",
		MatchLabels:  []string{p.OnSentSetLabel},
		MatchMode:    "any",
		AssigneeMode: "anyone",
	}
	issues, err := api.MatchingIssues(ctx, fp, "", "")
	if err != nil {
		if isAuthErr(err) {
			d.invalidateLinear() // re-resolve the key next time (rotation)
		}
		d.logf(p.Name, "reconcile: query failed: %v", err)
		return
	}
	d.setLinearOK(true)

	seen, err := d.seen.load(p.Name)
	if err != nil {
		d.logf(p.Name, "reconcile: seen state unreadable: %v", err)
		return
	}

	changed := false
	for _, is := range issues {
		if d.goalHold(is.ID, is.Description, is.IssueRelations) != "" {
			continue
		}
		if counted[is.Identifier] {
			continue // still has a live counted AO session
		}
		firstSeen, ok := seen[is.ID]
		if !ok {
			// No record of when it was dispatched (e.g. daemon restart):
			// start the orphan clock now instead of reverting immediately.
			seen[is.ID] = now
			changed = true
			continue
		}
		if now.Sub(firstSeen) < orphanTimeout {
			continue
		}
		// Prefer the session record's branch/repo for the PR check — the spawn
		// may have fallen back to "lola/<identifier>" when Linear provided no
		// branch name, and the PR lives in the project's repo (recorded on the
		// session at spawn). Fall back to the poll's repo (or its project's,
		// via PollRepo) when no session is on record.
		//
		// A best-of issue has one session (and one branch) per attempt, and ANY
		// of them holding an open PR means the issue delivered — so every
		// recorded branch is checked, not just the first.
		type prTarget struct{ branch, repo string }
		var targets []prTarget
		var natSess *session.Session
		for _, s := range d.nativeSessionsForIssue(is.Identifier) {
			if natSess == nil {
				natSess = &s
			}
			if s.Stack {
				// A stack session delivered when ANY of its apps holds an open PR.
				for _, a := range s.Apps {
					targets = append(targets, prTarget{a.Branch, a.Repo})
				}
				continue
			}
			t := prTarget{is.BranchName, pollRepo}
			if s.Branch != "" {
				t.branch = s.Branch
			}
			if s.Repo != "" {
				t.repo = s.Repo
			}
			targets = append(targets, t)
		}
		if len(targets) == 0 && p.IsStack() {
			// No record: the issue's branch in every member repository.
			for _, m := range members {
				targets = append(targets, prTarget{is.BranchName, m.Repo})
			}
		}
		if len(targets) == 0 {
			targets = []prTarget{{is.BranchName, pollRepo}}
		}
		delivered, unknown := false, false
		for _, t := range targets {
			if t.branch == "" {
				continue
			}
			open, err := d.openPR(ctx, t.repo, t.branch)
			if err != nil {
				// Cannot determine PR state: fail CLOSED. The SPEC only
				// allows the revert when there is provably no open PR —
				// reverting a held-for-review issue would re-spawn it.
				d.logf(p.Name, "reconcile: PR check for %s failed, skipping revert: %v", is.Identifier, err)
				unknown = true
				break
			}
			if open {
				delivered = true
				break
			}
		}
		if unknown || delivered {
			continue // could not tell, or the runner delivered a PR; not an orphan
		}

		current, err := api.IssueLabelIDs(ctx, is.ID)
		if err != nil {
			d.logf(p.Name, "reconcile: read labels for %s failed: %v", is.Identifier, err)
			continue
		}
		// Reverse of the dispatch flip: restore exactly the trigger labels the
		// flip stripped (recorded on the session), drop the sent label, so the
		// issue re-matches and re-queues. Restoring the recorded subset — not
		// every match_label — avoids re-adding labels the issue never carried
		// when match_mode=any matched on a subset of match_labels. When no
		// session record survives (e.g. pruned after a daemon restart) fall
		// back to all match_labels: a best-effort re-queue beats a stranded
		// orphan, and match_mode=all issues carried them all anyway.
		restore := p.MatchLabels
		if natSess != nil && len(natSess.RemovedLabels) > 0 {
			restore = natSess.RemovedLabels
		}
		newIDs := ApplyLabelDelta(current, []string{p.OnSentSetLabel}, restore)
		d.goalMu.Lock()
		if d.goalHold(is.ID, is.Description, is.IssueRelations) != "" {
			d.goalMu.Unlock()
			continue
		}
		err = api.SetIssueLabels(ctx, is.ID, newIDs)
		d.goalMu.Unlock()
		if err != nil {
			d.logf(p.Name, "reconcile: revert labels for %s failed: %v", is.Identifier, err)
			continue
		}
		delete(seen, is.ID)
		changed = true
		d.inflight.Remove(is.ID)
		d.logf(p.Name, "reconcile: reverted orphaned %s (no counted session after %s)", is.Identifier, orphanTimeout)
		if natSess != nil {
			// P4 write-back: mark the orphaned issue blocked (label + comment),
			// once. Only when a session record survives — the WBBlockedDone guard
			// lives on it, so without one we could re-fire every reconcile pass.
			d.writeBackBlocked(ctx, api, p, *natSess, "The agent session ended without delivering a PR.")
			// The dead session's worktree is never removed by reconcile
			// (destructive-op discipline: removal only for merged or
			// explicitly killed sessions) — name where it lives.
			d.logf(p.Name, "reconcile: native session %s worktree kept for inspection at %s",
				natSess.ID, filepath.Join(d.home, "worktrees", natSess.Project, natSess.ID))
		}
	}
	if changed {
		if err := d.seen.save(p.Name, seen); err != nil {
			d.logf(p.Name, "reconcile: persist seen: %v", err)
		}
	}
}

// ghOpenPR checks for an open PR on branch via `gh pr list --repo <repo>`.
// The per-poll `repo` config ("owner/name") makes the check independent of
// the daemon's cwd (launchd sets WorkingDirectory=$HOME, which is not a git
// checkout). Any failure — repo not configured, gh missing, gh error, bad
// JSON — returns an error so the caller fails closed: "could not check" must
// never be conflated with "no PR".
func (d *Daemon) ghOpenPR(ctx context.Context, repo, branch string) (bool, error) {
	if repo == "" {
		return false, fmt.Errorf(`no repo configured for poll: set repo = "owner/name" in config.toml to enable open-PR checks`)
	}
	gh, err := exec.LookPath("gh")
	if err != nil {
		d.ghWarn.Do(func() {
			d.logf("", "gh not on PATH: open-PR checks unavailable, orphan reverts are skipped")
		})
		return false, fmt.Errorf("gh not on PATH: %w", err)
	}
	out, err := exec.CommandContext(ctx, gh, "pr", "list", "--repo", repo, "--head", branch, "--json", "state", "--limit", "1").Output()
	if err != nil {
		return false, fmt.Errorf("gh pr list --repo %s --head %s: %w", repo, branch, err)
	}
	var prs []struct{ State string }
	if err := json.Unmarshal(out, &prs); err != nil {
		return false, fmt.Errorf("gh pr list --repo %s --head %s: bad output: %w", repo, branch, err)
	}
	return len(prs) > 0 && strings.EqualFold(prs[0].State, "open"), nil
}

// pruneLeases releases the port leases of sessions that are no longer in the
// store. The runtime releases a lease when it removes a worktree, but a session
// can leave the store without that — a dirty worktree kept on merge, a kill of
// a session whose project left config — and its ports would otherwise be
// leased forever. A session gone from the store has no dev tabs left to bind
// them. The orphanTimeout grace covers a spawn in flight, which leases its
// ports before its session is upserted.
func (d *Daemon) pruneLeases() {
	if d.ports == nil {
		return
	}
	live := map[string]bool{}
	for _, s := range d.sessions.Snapshot() {
		live[s.ID] = true
	}
	// A stack app's lease is keyed by its lane ID (runtime.EnvVars.LeaseKey)
	// and lives as long as its session.
	dropped, err := d.ports.Prune(func(id string) bool { return live[session.BaseID(id)] }, orphanTimeout)
	if err != nil {
		d.logf("", "reconcile: prune port leases: %v", err)
		return
	}
	if len(dropped) > 0 {
		d.logf("", "reconcile: released the port leases of %s (session gone)", strings.Join(dropped, ", "))
	}
}
