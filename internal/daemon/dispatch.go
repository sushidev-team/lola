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

	"github.com/sushidev-team/lola/internal/agent"
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
// (merged, dead, session_ended) hold no slot, so held PRs never stall new
// pickups. "draft" counts: a draft PR means the agent is still iterating and
// its claude process still occupies a runner.
var nativeCountingStatuses = map[string]bool{
	"working":           true,
	"needs_input":       true,
	"draft":             true,
	"ci_failed":         true,
	"changes_requested": true,
	"ci_pending":        true,
}

// NativeLiveCounted returns how many native sessions in sessions (a session
// store snapshot) currently occupy an agent slot. It is a tick's liveCounted:
//
//	budget = Budget(pollCap, globalCap, NativeLiveCounted(store))
//	       = min(pollCap, globalCap − NativeLiveCounted(store))
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

// ApplyLabelDelta returns current with every id in remove dropped and every
// id in add appended: order-stable, de-duplicated, empty ids skipped. Adding
// an id already present (and not being removed) is a no-op; removing an absent
// id is a no-op. Used symmetrically for the post-spawn flip (remove the
// trigger labels, add the sent label) and the reconcile revert (the reverse).
func ApplyLabelDelta(current, remove, add []string) []string {
	removeSet := make(map[string]bool, len(remove))
	for _, id := range remove {
		if id != "" {
			removeSet[id] = true
		}
	}
	out := make([]string, 0, len(current)+len(add))
	have := make(map[string]bool, len(current)+len(add))
	for _, id := range current {
		if id == "" || removeSet[id] || have[id] {
			continue
		}
		have[id] = true
		out = append(out, id)
	}
	for _, id := range add {
		if id == "" || have[id] {
			continue
		}
		have[id] = true
		out = append(out, id)
	}
	return out
}

// intersectLabels returns the ids in want that are present in have,
// preserving want's order and de-duplicated (empty ids skipped). It names the
// trigger labels a flip actually strips, so the reconcile revert can restore
// exactly them rather than every configured match_label.
func intersectLabels(want, have []string) []string {
	haveSet := make(map[string]bool, len(have))
	for _, id := range have {
		if id != "" {
			haveSet[id] = true
		}
	}
	out := make([]string, 0, len(want))
	seen := make(map[string]bool, len(want))
	for _, id := range want {
		if id == "" || seen[id] || !haveSet[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}

// PruneSeen returns a pruned copy of seen so it never grows unbounded.
// label mode: drop entries older than ttl (seen is only a short race guard).
// seen / state mode: drop entries whose ID is not in the current match set, so
// reopened tickets re-queue. state mode only ever stores the fallback entry
// written when a spawn state move failed, and it must stay authoritative (no
// TTL) — the issue keeps matching until the state is fixed, so a TTL-pruned
// entry would let it re-dispatch forever.
func PruneSeen(seen map[string]time.Time, matched map[string]bool, dedupMode string, now time.Time, ttl time.Duration) map[string]time.Time {
	out := make(map[string]time.Time, len(seen))
	for id, t := range seen {
		switch dedupMode {
		case "seen", "state":
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
	// A poll IS a project's polling config now, so the poll and the [[project]]
	// it dispatches into are one and the same struct (clone every slice/map so
	// the tick never aliases the live config after d.mu is dropped).
	p := *pp
	p.StateIDs = slices.Clone(p.StateIDs)
	p.MatchLabels = slices.Clone(p.MatchLabels)
	p.PrioritySort = slices.Clone(p.PrioritySort)
	p.PostCreate = slices.Clone(p.PostCreate)
	p.Symlinks = slices.Clone(p.Symlinks)
	p.Env = maps.Clone(p.Env)
	pollCap := d.cfg.EffectiveCap(pp)
	globalCap := d.cfg.Defaults.GlobalCap
	pollRepo := d.cfg.PollRepo(pp) // the project's repo (PR checks)
	nat := d.native                // snapshot under d.mu: reload may swap it concurrently
	project := p                   // the poll IS the project
	// The coding-agent binary this project spawns (per-project override →
	// [defaults].agent → "claude"): the health gate must confirm THAT binary
	// is on PATH, not always "claude". Resolved under the config lock.
	agentBin := agent.Parse(d.cfg.AgentForProject(p.Name)).Binary()
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

	// 1. Precheck the runtime (SPEC step 1 successor). The health check —
	// tmux available, git resolvable, the poll's coding-agent binary on PATH —
	// runs ONCE per tick; on failure the tick is skipped WITHOUT touching
	// seen/labels/in-flight, the same discipline as the old AO-down rule. A
	// missing project means misconfiguration and equally fails before any
	// state is touched.
	if err := d.runtimeHealth(agentBin); err != nil {
		return fail("runtime unavailable: "+err.Error(), nil)
	}
	if project.Name == "" {
		return fail(fmt.Sprintf("unknown project %q", p.Name), nil)
	}
	if nat == nil {
		return fail("native runtime unavailable", nil)
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

	// 5. Cross-poll dedup via the daemon-global in-flight set, then
	// 6. mode dedup.
	skip := func(is linear.Issue, reason string) {
		res.Matches = append(res.Matches, protocol.Match{
			Identifier: is.Identifier, Title: is.Title, Action: "skipped", Reason: reason,
		})
	}
	// State-mode durable double-spawn guard. label/seen persist a seen entry
	// BEFORE Spawn; state mode's ONLY dedup is the post-spawn state move, so a
	// crash after the session is saved but before that move lands leaves — on
	// restart — an empty in-flight set, no seen entry, and an issue still inside
	// state_ids. Without this the still-matching issue would re-dispatch onto a
	// SECOND agent. Shield any issue that already has a live native session on
	// record (adoption repopulates the store but not the in-memory in-flight set).
	var stateLive map[string]bool
	if p.DedupMode == "state" {
		stateLive = map[string]bool{}
		for _, s := range d.sessions.Snapshot() {
			if s.Source == "native" && s.Issue != "" && nativeSessionPresent(s.Status) {
				stateLive[s.Issue] = true
			}
		}
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
		case "state":
			// state mode's dedup is the post-spawn state move (which drops the
			// issue from the match set). A live native session shields the
			// crash/restart window; a seen entry exists ONLY as the fallback
			// written when that state move FAILED (writeBackSpawn -> seenFallback)
			// and is authoritative — NO TTL. The issue keeps matching until the
			// state is fixed, so a TTL-expired entry would re-dispatch it forever.
			if stateLive[is.Identifier] {
				skip(is, "session-live")
				continue
			}
			if _, ok := seen[is.ID]; ok {
				skip(is, "dedup-state")
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

	// 8. Budget: min(pollCap, globalCap − liveCounted), where liveCounted is
	// the number of slot-occupying native sessions in the store — the native
	// store is the only session source.
	liveCounted := NativeLiveCounted(d.sessions.Snapshot())
	budget := Budget(pollCap, globalCap, liveCounted)
	if budget <= 0 && len(candidates) > 0 {
		d.logf(name, "capped out: budget=%d (pollCap=%d globalCap=%d liveCounted=%d), %d candidate(s) waiting",
			budget, pollCap, globalCap, liveCounted, len(candidates))
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

		// (a) Mark in-flight AND persist seen FIRST — the crash guard against a
		// double-spawn. dedup_mode=state writes NO seen entry: the post-spawn
		// SetIssueState(OnSpawnStateID) moves the issue out of state_ids and IS
		// the dedup (writeBackSpawn falls back to an AUTHORITATIVE seen entry only
		// if that state write fails). In-flight covers the transient spawn window;
		// across a crash/restart in that window a state-mode issue is instead
		// shielded by the live-native-session guard in the candidate loop above.
		d.inflight.Add(is.ID, is.Identifier)
		if p.DedupMode != "state" {
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
		}

		// (b) Spawn with the IDENTIFIER (FE-231), never the UUID: worktree +
		// tmux + claude via the native runtime; the returned session is
		// upserted into the store immediately so the very next budget
		// computation counts it. Deadline-bound the whole spawn (worktree +
		// post_create + tmux): see nativeSpawnTimeout — user-supplied
		// commands run in here and the shielded tick context alone could
		// never abort them.
		spawnTarget := "project " + project.Name
		cctx, cancel := context.WithTimeout(ctx, nativeSpawnTimeout)
		sess, spawnErr := nat.Spawn(cctx, project, is)
		cancel()
		if spawnErr == nil {
			if pollRepo != "" {
				// PR checks run against the poll's repo, falling back to the
				// project's (config.PollRepo) — stamp it on the record so the
				// observer looks in the right place.
				sess.Repo = pollRepo
			}
			// Stamp the owning poll so later lifecycle write-back (PR-open,
			// merged, blocked) can resolve THIS poll's P4 config (PLAN P4).
			sess.PollName = name
			d.sessions.Upsert(sess)
			// Record the birth in the activity feed. Spawn enters via Upsert,
			// which has no transition callback, so record it explicitly (from "").
			d.recordSessionEvent("", sess)
			if serr := d.sessions.Save(); serr != nil {
				d.logf(name, "persist sessions after native spawn of %s: %v", is.Identifier, serr)
			}
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
			// Record which trigger labels the flip actually strips (the subset
			// of match_labels the issue carries right now). match_mode=any can
			// match on a strict subset, so the reconcile revert must restore
			// exactly these — restoring all match_labels would inject phantom
			// labels the issue never had.
			removed := intersectLabels(p.MatchLabels, current)
			newIDs := ApplyLabelDelta(current, p.MatchLabels, []string{p.OnSentSetLabel})
			if err := api.SetIssueLabels(ctx, is.ID, newIDs); err != nil {
				d.logf(name, "label flip for %s failed (seen guards dedup): %v", is.Identifier, err)
				continue
			}
			// Persist the stripped set on the session record (atomic RMW so a
			// concurrent observer update is not clobbered) for the orphan revert.
			if _, ok := d.sessions.Update(sess.ID, func(s *session.Session) bool {
				s.RemovedLabels = removed
				return true
			}); ok {
				if serr := d.sessions.Save(); serr != nil {
					d.logf(name, "persist removed labels for %s: %v", is.Identifier, serr)
				}
			}
		}

		// (d) P4 Linear write-back on spawn (all dedup modes): move the issue to
		// on_spawn_state_id and/or post the spawn comment, once. In state mode the
		// state move is ALSO the dedup. A no-op when nothing is configured; never
		// blocks the running agent on a Linear failure.
		d.writeBackSpawn(ctx, api, p, is, sess)
	}

	// 10. Log and update status.
	d.logf(name, "tick: matched=%d spawned=%d capped=%d errors=%d%s",
		len(issues), spawned, capped, errored, map[bool]string{true: " (dry-run)", false: ""}[dryRun])
	if !dryRun {
		d.status.setError(name, lastSpawnErr) // clears the error on a clean tick
	}
	return res, nil
}
