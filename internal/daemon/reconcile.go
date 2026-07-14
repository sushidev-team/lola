package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"slices"
	"strings"
	"time"

	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/linear"
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
// on_sent_set_label with no counted AO session and no open PR after
// orphan_timeout are reverted to their trigger label and cleared from
// seen + in-flight so they re-queue.
//
// An AO session "counts for" an issue when its IssueID (the --issue value we
// passed to `ao spawn`) equals the Linear identifier. Session IDs themselves
// are AO-internal (<sessionPrefix>-<n>) and never match.
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

	if !aoc.Reachable(ctx) {
		d.logf("", "reconcile: AO not running, skipping")
		return
	}
	sessions, err := aoc.LiveSessions(ctx)
	if err != nil {
		d.logf("", "reconcile: ao session ls failed: %v", err)
		return
	}
	counted := map[string]bool{} // Linear identifier -> has a counted session
	for _, s := range sessions {
		if counting[s.Status] && s.IssueID != "" {
			counted[s.IssueID] = true
		}
	}

	// Clear in-flight claims whose issue has no counted AO session anymore.
	// The orphanTimeout grace avoids racing a spawn that hasn't shown up in
	// `ao session ls` yet.
	for uuid, e := range d.inflight.Entries() {
		if !counted[e.Identifier] && now.Sub(e.AddedAt) > orphanTimeout {
			d.inflight.Remove(uuid)
			d.logf("", "reconcile: cleared stale in-flight claim on %s", e.Identifier)
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
		d.reconcilePoll(ctx, api, p, counted, now)
	}
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
		if is.BranchName != "" {
			open, err := d.openPR(ctx, p.Repo, is.BranchName)
			if err != nil {
				// Cannot determine PR state: fail CLOSED. The SPEC only
				// allows the revert when there is provably no open PR —
				// reverting a held-for-review issue would re-spawn it.
				d.logf(p.Name, "reconcile: PR check for %s failed, skipping revert: %v", is.Identifier, err)
				continue
			}
			if open {
				continue // AO delivered a PR; not an orphan
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
		d.logf(p.Name, "reconcile: reverted orphaned %s (no counted AO session after %s)", is.Identifier, orphanTimeout)
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
