package daemon

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"math"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/sushidev-team/lola/internal/ao"
	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/linear"
	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/session"
)

// SeenTTL is the label-mode race-guard window: a seen entry younger than
// this suppresses a re-match (the label flip may not be visible in Linear
// yet); older entries are ignored and pruned.
const SeenTTL = time.Hour

// nativeSpawnTimeout bounds one whole native spawn: worktree add, symlinks,
// the user-supplied post_create commands, and tmux new-session. Ticks run on
// a cancel-shielded context (safeTick) while holding the poll's tick mutex,
// so without a deadline a single wedged post_create (say, `npm ci` against a
// black-holed registry) would freeze the poll forever, block reconcilePoll on
// the same mutex, and hang graceful shutdown at d.wg.Wait — the exact bug
// class the observer bounds with observeExecTimeout. Generous, because
// post_create legitimately runs package installs.
const nativeSpawnTimeout = 10 * time.Minute

// Budget returns min(pollCap, globalCap−liveCounted) per the SPEC formula.
// The result may be <= 0, which means capped out.
func Budget(pollCap, globalCap, liveCounted int) int {
	b := globalCap - liveCounted
	if pollCap < b {
		b = pollCap
	}
	return b
}

// nativeCountingStatuses are the derived session-store statuses under which a
// native session occupies an agent slot. Parked-for-review states (approved,
// review_pending, no_pr, idle after the PR opened, …) and terminal states
// (merged, dead, session_ended) hold no slot — mirroring how the AO runtime's
// [ao].counting_states excludes parked and dead AO sessions, so held PRs never
// stall new pickups. "draft" counts for the same reason it is in
// config.DefaultCountingStates: a draft PR means the agent is still iterating
// and its claude process still occupies a runner.
var nativeCountingStatuses = map[string]bool{
	"working":           true,
	"needs_input":       true,
	"draft":             true,
	"ci_failed":         true,
	"changes_requested": true,
	"ci_pending":        true,
}

// NativeLiveCounted returns how many native-runtime sessions in sessions
// (a session store snapshot) currently occupy an agent slot. Together with
// the AO-side count it forms a tick's budget — the global cap spans BOTH
// runtimes, and both counts are always computed together:
//
//	liveCounted = aoCounted                       (AO sessions in [ao].counting_states,
//	                                               from a fresh `ao session ls`)
//	            + NativeLiveCounted(store)        (native sessions in nativeCountingStatuses)
//	budget      = Budget(pollCap, globalCap, liveCounted)
//	            = min(pollCap, globalCap − liveCounted)
func NativeLiveCounted(sessions []session.Session) int {
	n := 0
	for _, s := range sessions {
		if s.Source == "native" && nativeCountingStatuses[s.Status] {
			n++
		}
	}
	return n
}

// SortIssues sorts in place by the given keys ("priority", "createdAt"),
// defaulting to ["priority","createdAt"]. Linear priority 1=urgent..4=low;
// 0 (none) sorts LAST. createdAt ascends (RFC3339 compares lexically).
// Ties break deterministically by identifier.
func SortIssues(issues []linear.Issue, prioritySort []string) {
	keys := prioritySort
	if len(keys) == 0 {
		keys = []string{"priority", "createdAt"}
	}
	sort.SliceStable(issues, func(i, j int) bool {
		a, b := issues[i], issues[j]
		for _, k := range keys {
			switch k {
			case "priority":
				pa, pb := priorityRank(a.Priority), priorityRank(b.Priority)
				if pa != pb {
					return pa < pb
				}
			case "createdAt":
				if a.CreatedAt != b.CreatedAt {
					return a.CreatedAt < b.CreatedAt
				}
			}
		}
		return a.Identifier < b.Identifier
	})
}

func priorityRank(p float64) float64 {
	if p == 0 {
		return math.Inf(1) // priority 0 = none sorts last
	}
	return p
}

// NewLabelIDs computes the post-spawn label set: (current − removeID) +
// setID, with duplicates removed. Removing an absent ID is a no-op.
func NewLabelIDs(current []string, removeID, setID string) []string {
	out := make([]string, 0, len(current)+1)
	have := make(map[string]bool, len(current)+1)
	for _, id := range current {
		if id == "" || id == removeID || have[id] {
			continue
		}
		have[id] = true
		out = append(out, id)
	}
	if setID != "" && !have[setID] {
		out = append(out, setID)
	}
	return out
}

// PruneSeen returns a pruned copy of seen so it never grows unbounded.
// label mode: drop entries older than ttl (seen is only a short race guard).
// seen mode: drop entries whose ID is not in the current match set, so
// reopened tickets re-queue.
func PruneSeen(seen map[string]time.Time, matched map[string]bool, dedupMode string, now time.Time, ttl time.Duration) map[string]time.Time {
	out := make(map[string]time.Time, len(seen))
	for id, t := range seen {
		switch dedupMode {
		case "seen":
			if !matched[id] {
				continue
			}
		default: // label
			if now.Sub(t) > ttl {
				continue
			}
		}
		out[id] = t
	}
	return out
}

// spawnPrompt builds the --prompt text for a spawned agent (PLAN P0.2):
// identifier + title plus an instruction to fetch the full issue from Linear.
// Newlines are fine — AO strips them for display only.
func spawnPrompt(is linear.Issue) string {
	return fmt.Sprintf("Linear issue %s: %s\nFetch the full issue (description, comments, acceptance criteria) from Linear before starting — e.g. via the linearis CLI or Linear MCP. Work only on this issue.",
		is.Identifier, is.Title)
}

// isAuthErr classifies Linear client errors: the client wraps 401/403 as
// "linear auth failed: http NNN".
func isAuthErr(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "auth failed")
}

// tick runs one dispatch pass for the named poll, per SPEC "Dispatch flow".
// dryRun evaluates everything (dedup, budget, cross-poll overlap) with ZERO
// side effects: no in-flight add, no seen write, no spawn, no label write,
// no status mutation.
func (d *Daemon) tick(ctx context.Context, name string, dryRun bool) (protocol.PollOnceData, error) {
	res := protocol.PollOnceData{Poll: name, DryRun: dryRun}

	// Snapshot poll + relevant defaults under lock; the tick itself runs
	// without holding d.mu.
	d.mu.Lock()
	pp := d.cfg.PollByName(name)
	if pp == nil {
		d.mu.Unlock()
		return res, fmt.Errorf("unknown poll %q", name)
	}
	p := *pp
	p.StateIDs = slices.Clone(p.StateIDs)
	p.MatchLabels = slices.Clone(p.MatchLabels)
	p.PrioritySort = slices.Clone(p.PrioritySort)
	pollCap := d.cfg.EffectiveCap(pp)
	globalCap := d.cfg.Defaults.GlobalCap
	counting := make(map[string]bool, len(d.cfg.AO.CountingStates))
	for _, s := range d.cfg.AO.CountingStates {
		counting[s] = true
	}
	aoc := d.aoc    // snapshot under d.mu: reload may swap the client concurrently
	nat := d.native // ditto
	nativeRt := p.Runtime == config.RuntimeNative
	var project config.Project // resolved [[project]] for runtime=native
	if nativeRt {
		if prj := d.cfg.ProjectByName(p.Project); prj != nil {
			project = *prj
			project.PostCreate = slices.Clone(project.PostCreate)
			project.Symlinks = slices.Clone(project.Symlinks)
			project.Env = maps.Clone(project.Env)
		}
	}
	d.mu.Unlock()

	now := time.Now()
	if !dryRun {
		d.status.begin(name)
		defer d.status.end(name, now)
	}

	fail := func(msg string, err error) (protocol.PollOnceData, error) {
		full := msg
		if err != nil {
			full = msg + ": " + err.Error()
		}
		if !dryRun {
			d.status.setError(name, full)
		}
		d.logf(name, "%s", full)
		return res, errors.New(full)
	}
	linFail := func(stage string, err error) (protocol.PollOnceData, error) {
		if isAuthErr(err) {
			// Drop the cached client so the next tick re-resolves the key
			// (Keychain > env) — recovers from key rotation without restart.
			d.invalidateLinear()
			return fail("Linear auth failed", err)
		}
		return fail(stage, err)
	}

	// 1. Precheck the spawn backend. runtime=ao: AO down → skip WITHOUT
	// touching seen/labels/in-flight. runtime=native has no external
	// orchestrator to probe — worktree/tmux failures surface per spawn — but a
	// missing project or runtime means misconfiguration, so fail before any
	// state is touched.
	if nativeRt {
		if project.Name == "" {
			return fail(fmt.Sprintf("unknown project %q (runtime=native)", p.Project), nil)
		}
		if nat == nil {
			return fail("native runtime unavailable", nil)
		}
	} else if !aoc.Reachable(ctx) {
		return fail("AO not running", nil)
	}

	// 2. Linear client (key resolved at startup; retried here if missing).
	api, err := d.ensureLinear()
	if err != nil {
		return fail("Linear auth failed", err)
	}

	viewerID := ""
	if p.AssigneeMode == "me" {
		if viewerID, err = d.viewer(ctx, api); err != nil {
			return linFail("resolve viewer", err)
		}
	}

	// 3. cycle_mode=active: resolve team.activeCycle.id FRESH every tick.
	cycleID := ""
	switch p.CycleMode {
	case "active":
		active, _, err := api.Cycles(ctx, p.TeamID)
		if err != nil {
			return linFail("resolve active cycle", err)
		}
		if active == nil {
			return fail("no active cycle for team", nil)
		}
		cycleID = active.ID
	case "pinned":
		cycleID = p.CycleID
	}

	// 4. Matching issues (client paginates internally).
	issues, err := api.MatchingIssues(ctx, p, cycleID, viewerID)
	if err != nil {
		return linFail("query issues", err)
	}
	d.setLinearOK(true)

	seen, err := d.seen.load(name)
	if err != nil {
		d.logf(name, "seen state unreadable, starting empty: %v", err)
		seen = map[string]time.Time{}
	}
	matched := make(map[string]bool, len(issues))
	for _, is := range issues {
		matched[is.ID] = true
	}

	// Feed the observer's identifier→branch map from the issue data already
	// in hand — the cheapest correct source for Linear's branchName (PLAN P1).
	// Real ticks only: dry runs stay side-effect free. Native polls record the
	// project's repo (config.Project.Repo) — that is where their PRs live.
	if !dryRun {
		repoHint := p.Repo
		if nativeRt && project.Repo != "" {
			repoHint = project.Repo
		}
		for _, is := range issues {
			d.recordBranch(is.Identifier, is.BranchName, repoHint)
		}
	}

	// 5. Cross-poll dedup via the daemon-global in-flight set, then
	// 6. mode dedup.
	skip := func(is linear.Issue, reason string) {
		res.Matches = append(res.Matches, protocol.Match{
			Identifier: is.Identifier, Title: is.Title, Action: "skipped", Reason: reason,
		})
	}
	var candidates []linear.Issue
	for _, is := range issues {
		if d.inflight.Has(is.ID) {
			skip(is, "in-flight")
			continue
		}
		switch p.DedupMode {
		case "seen":
			// seen is authoritative.
			if _, ok := seen[is.ID]; ok {
				skip(is, "dedup-seen")
				continue
			}
		default: // label: query already excludes flipped issues; seen is
			// only a short-TTL race guard, stale entries are ignored.
			if t, ok := seen[is.ID]; ok && now.Sub(t) <= SeenTTL {
				skip(is, "dedup-label")
				continue
			}
		}
		candidates = append(candidates, is)
	}

	// Prune seen so it never grows unbounded (persist only on real ticks).
	origLen := len(seen)
	seen = PruneSeen(seen, matched, p.DedupMode, now, SeenTTL)
	if !dryRun && len(seen) != origLen {
		if err := d.seen.save(name, seen); err != nil {
			d.logf(name, "persist pruned seen: %v", err)
		}
	}

	// 7. Deterministic order when capped.
	SortIssues(candidates, p.PrioritySort)

	// 8. Budget. The global cap spans BOTH runtimes and both counts are
	// always computed together (see NativeLiveCounted for the full formula):
	//
	//	liveCounted = aoCounted + NativeLiveCounted(session store)
	//	budget      = min(pollCap, globalCap − liveCounted)
	//
	// aoCounted comes from a FRESH `ao session ls --json`, counting sessions
	// in counting_states across ALL projects. For runtime=ao it is mandatory
	// (a failure fails the tick, as before); for runtime=native it is
	// best-effort — an unreachable AO must never block native dispatch and
	// contributes 0 with a log line.
	aoCounted := 0
	countAO := func(sessions []ao.SessionState) {
		for _, s := range sessions {
			if counting[s.Status] {
				aoCounted++
			}
		}
	}
	if !nativeRt {
		sessions, err := aoc.LiveSessions(ctx)
		if err != nil {
			return fail("AO not running", err)
		}
		countAO(sessions)
	} else {
		// Best-effort AO probe on the native path, each exec individually
		// deadline-bounded (same bound as the observer's execs): the tick
		// runs on a cancel-shielded context under the poll's tick mutex, and
		// a wedged ao binary must never block native dispatch.
		cctx, cancel := context.WithTimeout(ctx, observeExecTimeout)
		reachable := aoc.Reachable(cctx)
		cancel()
		if reachable {
			cctx, cancel := context.WithTimeout(ctx, observeExecTimeout)
			sessions, err := aoc.LiveSessions(cctx)
			cancel()
			if err != nil {
				d.logf(name, "budget: ao session ls failed, counting native sessions only: %v", err)
			} else {
				countAO(sessions)
			}
		}
	}
	nativeCounted := NativeLiveCounted(d.sessions.Snapshot())
	liveCounted := aoCounted + nativeCounted
	budget := Budget(pollCap, globalCap, liveCounted)
	if budget <= 0 && len(candidates) > 0 {
		d.logf(name, "capped out: budget=%d (pollCap=%d globalCap=%d aoCounted=%d nativeCounted=%d), %d candidate(s) waiting",
			budget, pollCap, globalCap, aoCounted, nativeCounted, len(candidates))
	}

	// 9. Dispatch per issue, up to budget.
	spawned, capped, errored := 0, 0, 0
	lastSpawnErr := ""
	for _, is := range candidates {
		m := protocol.Match{Identifier: is.Identifier, Title: is.Title}
		if spawned >= budget {
			capped++
			m.Action, m.Reason = "skipped", "capped"
			res.Matches = append(res.Matches, m)
			continue
		}
		if dryRun {
			spawned++
			m.Action = "would-spawn"
			res.Matches = append(res.Matches, m)
			continue
		}

		// (a) Mark in-flight AND persist seen FIRST.
		d.inflight.Add(is.ID, is.Identifier)
		seen[is.ID] = now
		if err := d.seen.save(name, seen); err != nil {
			// Without the on-disk guard a crash could double-spawn: skip.
			d.inflight.Remove(is.ID)
			delete(seen, is.ID)
			d.logf(name, "persist seen for %s failed, skipping spawn: %v", is.Identifier, err)
			errored++
			m.Action, m.Reason = "skipped", "error"
			res.Matches = append(res.Matches, m)
			continue
		}

		// (b) Spawn with the IDENTIFIER (FE-231), never the UUID — this is the
		// per-poll runtime switch. runtime=native: worktree + tmux + claude
		// via the native runtime; the returned session is upserted into the
		// store immediately so the very next budget computation counts it.
		// runtime=ao (default): `ao spawn` with a context prompt (AO's own
		// issue resolution is GitHub-only, so without it agents spawned for
		// Linear issues start blind — PLAN P0.2). The in-flight/seen-first
		// ordering above and the label flip below are identical either way.
		var spawnErr error
		spawnTarget := "ao project " + p.AOProject
		if nativeRt {
			spawnTarget = "native project " + project.Name
			// Deadline-bound the whole spawn (worktree + post_create + tmux):
			// see nativeSpawnTimeout — user-supplied commands run in here and
			// the shielded tick context alone could never abort them.
			cctx, cancel := context.WithTimeout(ctx, nativeSpawnTimeout)
			sess, err := nat.Spawn(cctx, project, is)
			cancel()
			if err == nil {
				d.sessions.Upsert(sess)
				if serr := d.sessions.Save(); serr != nil {
					d.logf(name, "persist sessions after native spawn of %s: %v", is.Identifier, serr)
				}
			}
			spawnErr = err
		} else {
			spawnErr = aoc.Spawn(ctx, p.AOProject, is.Identifier, spawnPrompt(is))
		}
		if spawnErr != nil {
			// Drop the in-flight claim. In label mode the seen entry stays
			// as a short-TTL race guard (the un-flipped label retries after
			// SeenTTL); in seen mode seen is authoritative and never expires
			// while the issue matches, so keeping the entry would silently
			// drop the issue forever — remove it so the next tick retries.
			d.inflight.Remove(is.ID)
			if p.DedupMode == "seen" {
				delete(seen, is.ID)
				if serr := d.seen.save(name, seen); serr != nil {
					d.logf(name, "unmark seen for %s after failed spawn: %v", is.Identifier, serr)
				}
			}
			d.logf(name, "spawn %s in %s failed: %v", is.Identifier, spawnTarget, spawnErr)
			errored++
			lastSpawnErr = fmt.Sprintf("spawn %s failed: %v", is.Identifier, spawnErr)
			m.Action, m.Reason = "skipped", "error"
			res.Matches = append(res.Matches, m)
			continue
		}
		spawned++
		d.status.setLastSpawn(name, time.Now())
		d.logf(name, "spawned %s into %s", is.Identifier, spawnTarget)
		m.Action = "spawned"
		res.Matches = append(res.Matches, m)

		// (c) Only on confirmed spawn success + label mode: re-read labels
		// FRESH, flip, write back with the UUID. A label write failure is
		// logged only — no spawn retry, seen stays as the guard.
		if p.DedupMode == "label" {
			current, err := api.IssueLabelIDs(ctx, is.ID)
			if err != nil {
				d.logf(name, "read labels for %s failed (seen guards dedup): %v", is.Identifier, err)
				continue
			}
			newIDs := NewLabelIDs(current, p.OnSentRemoveLabel, p.OnSentSetLabel)
			if err := api.SetIssueLabels(ctx, is.ID, newIDs); err != nil {
				d.logf(name, "label flip for %s failed (seen guards dedup): %v", is.Identifier, err)
			}
		}
	}

	// 10. Log and update status.
	d.logf(name, "tick: matched=%d spawned=%d capped=%d errors=%d%s",
		len(issues), spawned, capped, errored, map[bool]string{true: " (dry-run)", false: ""}[dryRun])
	if !dryRun {
		d.status.setError(name, lastSpawnErr) // clears the error on a clean tick
	}
	return res, nil
}
