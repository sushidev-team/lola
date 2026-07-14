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
	d.reconcile(ctx)
}

// reconcile per SPEC "Reconciliation pass": issues still carrying the
// on_sent_set_label with no counted session and no open PR after
// orphan_timeout are reverted to their trigger label and cleared from
// seen + in-flight so they re-queue. Native-runtime polls use the exact same
// orphan logic — only the "counts for" source differs per runtime.
//
// An AO session "counts for" an issue when its IssueID (the --issue value we
// passed to `ao spawn`) equals the Linear identifier. Session IDs themselves
// are AO-internal (<sessionPrefix>-<n>) and never match. A native session
// counts while its tmux pane is alive (see nativeSessionPresent) — a dead
// pane is exactly the native orphan condition.
func (d *Daemon) reconcile(ctx context.Context) {
	d.mu.Lock()
	polls := slices.Clone(d.cfg.Polls)
	counting := make(map[string]bool, len(d.cfg.AO.CountingStates))
	for _, s := range d.cfg.AO.CountingStates {
		counting[s] = true
	}
	aoc := d.aoc // snapshot under d.mu: reload may swap the client concurrently
	d.mu.Unlock()
	now := time.Now()

	counted := map[string]bool{} // Linear identifier -> has a counted session
	for _, s := range d.sessions.Snapshot() {
		if s.Source == "native" && s.Issue != "" && nativeSessionPresent(s.Status) {
			counted[s.Issue] = true
		}
	}

	// AO reachability only gates the AO side: native-runtime polls reconcile
	// even while AO is down or unreadable.
	aoUp := aoc.Reachable(ctx)
	if aoUp {
		sessions, err := aoc.LiveSessions(ctx)
		if err != nil {
			d.logf("", "reconcile: ao session ls failed, skipping ao-runtime polls: %v", err)
			aoUp = false
		} else {
			for _, s := range sessions {
				if counting[s.Status] && s.IssueID != "" {
					counted[s.IssueID] = true
				}
			}
		}
	} else {
		d.logf("", "reconcile: AO not running, skipping ao-runtime polls")
	}

	// Clear in-flight claims whose issue has no counted session anymore.
	// The orphanTimeout grace avoids racing a spawn that hasn't shown up in
	// `ao session ls` yet. Only with the full picture: while any enabled poll
	// dispatches via AO, an unanswered AO leaves its sessions invisible and
	// every ao-runtime claim would be cleared spuriously. A pure
	// runtime=native deployment has no such blind spot — native session facts
	// are already in counted regardless of AO reachability — and MUST still
	// clear claims: this is the only path that releases a claim after a
	// successful spawn, so gating it on an AO that is never up would leave
	// every dispatched issue claimed for the daemon's lifetime and block any
	// later re-dispatch (e.g. re-adding the trigger label after a merge).
	aoRelevant := false
	for _, p := range polls {
		if p.Enabled && p.Runtime != config.RuntimeNative {
			aoRelevant = true
			break
		}
	}
	if aoUp || !aoRelevant {
		for uuid, e := range d.inflight.Entries() {
			if !counted[e.Identifier] && now.Sub(e.AddedAt) > orphanTimeout {
				d.inflight.Remove(uuid)
				d.logf("", "reconcile: cleared stale in-flight claim on %s", e.Identifier)
			}
		}
	}

	api, err := d.ensureLinear()
	if err != nil {
		d.logf("", "reconcile: linear unavailable: %v", err)
		return
	}

	for _, p := range polls {
		if !p.Enabled || p.DedupMode != "label" || p.OnSentSetLabel == "" {
			continue
		}
		if p.Runtime != config.RuntimeNative && !aoUp {
			continue // counted has no AO facts this pass; fail closed
		}
		d.reconcilePoll(ctx, api, p, counted, now)
	}
}

// nativeSessionPresent reports whether a native session's status still
// accounts for its issue in the orphan reconciliation: everything except a
// dead pane ("dead" / "session_ended"). This is deliberately WIDER than the
// budget's NativeLiveCounted — a parked session (approved, review_pending, …)
// holds no agent slot but must still shield its issue from an orphan revert:
// its pane is alive and its work is delivered.
func nativeSessionPresent(status string) bool {
	switch status {
	case "dead", "session_ended":
		return false
	}
	return true
}

// nativeSessionForIssue returns the stored native session working on the
// Linear identifier, or ok=false when none is on record.
func (d *Daemon) nativeSessionForIssue(identifier string) (session.Session, bool) {
	if identifier == "" {
		return session.Session{}, false
	}
	for _, s := range d.sessions.Snapshot() {
		if s.Source == "native" && s.Issue == identifier {
			return s, true
		}
	}
	return session.Session{}, false
}

func (d *Daemon) reconcilePoll(ctx context.Context, api linear.API, p config.Poll, counted map[string]bool, now time.Time) {
	// Serialize with ticks for this poll: both sides do a load-modify-save
	// of the same seen map, and an unsynchronized interleave loses updates
	// (a tick's fresh entry erased, or a reverted orphan resurrected).
	mu := d.tickMutex(p.Name)
	mu.Lock()
	defer mu.Unlock()

	// Find issues currently carrying set_label: a minimal poll copy keeping
	// only team/project scope. Cycle, states and assignee are cleared so
	// orphans that moved since dispatch are still found.
	fp := config.Poll{
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
		// Native runtime: prefer the session record's branch/repo for the PR
		// check — the spawn may have fallen back to "lola/<identifier>" when
		// Linear provided no branch name, and the PR lives in the project's
		// repo (config.Project.Repo, recorded on the session at spawn).
		branch, repo := is.BranchName, p.Repo
		var natSess *session.Session
		if p.Runtime == config.RuntimeNative {
			if s, ok := d.nativeSessionForIssue(is.Identifier); ok {
				natSess = &s
				if s.Branch != "" {
					branch = s.Branch
				}
				if s.Repo != "" {
					repo = s.Repo
				}
			}
		}
		if branch != "" {
			open, err := d.openPR(ctx, repo, branch)
			if err != nil {
				// Cannot determine PR state: fail CLOSED. The SPEC only
				// allows the revert when there is provably no open PR —
				// reverting a held-for-review issue would re-spawn it.
				d.logf(p.Name, "reconcile: PR check for %s failed, skipping revert: %v", is.Identifier, err)
				continue
			}
			if open {
				continue // the runner delivered a PR; not an orphan
			}
		}

		trigger := ""
		if len(p.MatchLabels) > 0 {
			trigger = p.MatchLabels[0] // revert to the poll's trigger label
		}
		current, err := api.IssueLabelIDs(ctx, is.ID)
		if err != nil {
			d.logf(p.Name, "reconcile: read labels for %s failed: %v", is.Identifier, err)
			continue
		}
		newIDs := NewLabelIDs(current, p.OnSentSetLabel, trigger)
		if err := api.SetIssueLabels(ctx, is.ID, newIDs); err != nil {
			d.logf(p.Name, "reconcile: revert labels for %s failed: %v", is.Identifier, err)
			continue
		}
		delete(seen, is.ID)
		changed = true
		d.inflight.Remove(is.ID)
		d.logf(p.Name, "reconcile: reverted orphaned %s (no counted session after %s)", is.Identifier, orphanTimeout)
		if natSess != nil {
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
