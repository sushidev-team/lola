package daemon

// bestof.go is best-of-N: ONE Linear issue run as several independent attempts
// — different coding agents, or one agent on different models — on sibling
// branches, compared side by side, and narrowed to the one a human keeps.
//
// Three commands, each with its own rails:
//
//   - START (cmd=bestOf, or a poll match carrying [best_of].label — see
//     dispatch.go). Every attempt is a full native session (worktree + agent)
//     with a "-b<k>" ID/branch suffix, and EVERY one counts: the dispatch
//     budget is charged N, the manual command refuses past global_cap, and both
//     honour the spend/load hold. Each agent of the mix is health-gated BEFORE
//     anything is mutated, like the single-agent gate. The issue is deduped
//     exactly once (one in-flight claim, one seen entry, one label flip, one
//     spawn write-back) — the attempts share the issue, not N copies of it.
//   - COMPARE (cmd=bestOfCompare) is read-only: the store's facts (axes, PR,
//     CI, recorded review tallies, usage) plus ONE bounded local `git diff
//     --numstat` per attempt. bestof.Suggest marks a suggested winner, and it
//     stays a suggestion — nothing reads it but a human.
//   - PICK (cmd=bestOfPick) is the human's decision and runs once per group.
//     The winner is stamped FIRST (a crash mid-pick leaves the decision on
//     disk), its draft PR is marked ready, and every other attempt's PR is
//     CLOSED (branch kept — closing is reversible, deleting is not) and its
//     session torn down through the ordinary dirty-safe runtime.Kill: a
//     worktree with uncommitted work keeps both checkout and branch. A loser's
//     teardown deliberately does NOT free the issue's in-flight claim or strip
//     its sent label (clearLabelDispatch) — the winner is still working it.
//
// Until the pick every attempt is IN THE RACE (Session.InBestOfRace): its
// PR-open Linear write-back is held, so N draft PRs do not each move the issue
// and comment on it; the winner's fires on the first observe cycle after the
// pick. Everything else — reactions, reviews, CI — runs per attempt as usual,
// which is exactly what the comparison needs.

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/sushidev-team/lola/internal/agent"
	"github.com/sushidev-team/lola/internal/bestof"
	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/gitdiff"
	"github.com/sushidev-team/lola/internal/linear"
	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/reviewmd"
	"github.com/sushidev-team/lola/internal/runtime"
	"github.com/sushidev-team/lola/internal/session"
	"github.com/sushidev-team/lola/internal/state"
	"github.com/sushidev-team/lola/internal/worktree"
)

const (
	// bestOfStatTimeout bounds one attempt's `git diff --numstat` in the
	// compare view (local git, but a huge worktree must not hang the request).
	bestOfStatTimeout = 10 * time.Second
	// bestOfPRTimeout bounds one gh write of a pick (ready / close).
	bestOfPRTimeout = 30 * time.Second
)

// newBestOfGroup mints a group id: the issue plus a base-36 timestamp, so a
// re-run of the same issue is a distinct group.
func newBestOfGroup(identifier string, now time.Time) string {
	return strings.ToLower(identifier) + "-" + strconv.FormatInt(now.UnixNano()/int64(time.Millisecond), 36)
}

// bestOfMixForIssue returns the attempts to spawn for a poll-matched issue:
// the configured mix when [best_of].label is set and the issue carries it, nil
// (a plain single dispatch) otherwise. A mix that does not parse — validation
// reports it, but validation is not fatal — degrades to a plain dispatch with
// a log line rather than stranding the issue.
func (d *Daemon) bestOfMixForIssue(cfg config.BestOfConfig, mix []bestof.Attempt, mixErr error, poll string, is linear.Issue) []bestof.Attempt {
	if cfg.Label == "" || !slices.Contains(is.LabelIDs, cfg.Label) {
		return nil
	}
	if mixErr != nil {
		d.logf(poll, "best-of: %s carries the best-of label but the mix is invalid (%v); dispatching one attempt", is.Identifier, mixErr)
		return nil
	}
	return mix
}

// bestOfHealth health-gates every distinct agent of a mix, the multi-agent
// form of the single-binary gate every dispatch runs. The first failure names
// the agent.
func (d *Daemon) bestOfHealth(mix []bestof.Attempt) error {
	seen := map[string]bool{}
	for _, a := range mix {
		if seen[a.Agent] {
			continue
		}
		seen[a.Agent] = true
		if err := d.runtimeHealth(agent.Parse(a.Agent).Binary()); err != nil {
			return fmt.Errorf("%s: %w", a.Agent, err)
		}
	}
	return nil
}

// spawnIssue spawns issue is into project p: ONE session when mix is empty,
// else one per attempt of a fresh best-of group. Every spawned session is
// stamped (repo, poll), upserted, recorded and persisted immediately — the next
// Budget must count it. A group returns the attempts that came up (failed
// names the rest); the error is non-nil only when nothing spawned at all, so
// the caller's single-spawn failure handling (unmark seen, drop in-flight)
// applies unchanged.
func (d *Daemon) spawnIssue(ctx context.Context, nat NativeAPI, p config.Project, is linear.Issue, agentOverride string, mix []bestof.Attempt, repo, poll string) (sessions []session.Session, failed []string, err error) {
	store := func(sess session.Session) {
		if repo != "" {
			sess.Repo = repo
		}
		if poll != "" {
			sess.PollName = poll
		}
		d.sessions.Upsert(sess)
		d.recordSessionEvent("", sess)
		if serr := d.sessions.Save(); serr != nil {
			d.logf(poll, "persist sessions after spawn of %s: %v", sess.ID, serr)
		}
		sessions = append(sessions, sess)
	}
	if len(mix) == 0 {
		cctx, cancel := context.WithTimeout(ctx, nativeSpawnTimeout)
		sess, serr := nat.SpawnWith(cctx, p, is, runtime.SpawnOpts{Agent: agentOverride})
		cancel()
		if serr != nil {
			return nil, nil, serr
		}
		store(sess)
		return sessions, nil, nil
	}
	group := newBestOfGroup(is.Identifier, time.Now())
	var lastErr error
	for i, a := range mix {
		cctx, cancel := context.WithTimeout(ctx, nativeSpawnTimeout)
		sess, serr := nat.SpawnWith(cctx, p, is, runtime.SpawnOpts{
			Agent: a.Agent, Model: a.Model, Group: group, Attempt: i + 1, Of: len(mix),
		})
		cancel()
		if serr != nil {
			lastErr = serr
			failed = append(failed, fmt.Sprintf("attempt %d (%s): %v", i+1, a.Label(), serr))
			d.logf(poll, "best-of: %s attempt %d (%s) failed to spawn: %v", is.Identifier, i+1, a.Label(), serr)
			continue
		}
		store(sess)
	}
	if len(sessions) == 0 {
		return nil, failed, fmt.Errorf("best-of: no attempt spawned: %w", lastErr)
	}
	d.logf(poll, "best-of: %s spawned %d/%d attempt(s) as group %s", is.Identifier, len(sessions), len(mix), group)
	return sessions, failed, nil
}

// setRemovedLabels records the dispatch flip's stripped trigger labels on
// every session of one spawn, for the reconcile revert.
func (d *Daemon) setRemovedLabels(sessions []session.Session, removed []string) {
	for _, s := range sessions {
		d.sessions.Update(s.ID, func(cur *session.Session) bool {
			cur.RemovedLabels = slices.Clone(removed)
			return true
		})
	}
	if err := d.sessions.Save(); err != nil {
		d.logf("", "persist removed labels: %v", err)
	}
}

// handleBestOf serves cmd=bestOf: start one issue as a best-of group on demand.
// It follows handleOpenTicket's dedup order exactly (claim → seen → spawn →
// label flip + write-back) and adds what an N-way start needs: every agent of
// the mix health-gated, the spend/load hold honoured, and the WHOLE group
// fitted under global_cap before anything is touched.
func (d *Daemon) handleBestOf(ctx context.Context, a protocol.BestOfArgs) (protocol.BestOfData, error) {
	ident := strings.TrimSpace(a.Issue)
	if ident == "" {
		return protocol.BestOfData{}, errors.New("bestOf: issue required")
	}
	d.mu.Lock()
	nat := d.native
	var mix []bestof.Attempt
	var mixErr error
	if strings.TrimSpace(a.Agents) != "" {
		mix, mixErr = bestof.ParseMixString(a.Agents)
	} else {
		mix, mixErr = d.cfg.BestOfMix()
	}
	globalCap := d.cfg.Defaults.GlobalCap
	d.mu.Unlock()
	if mixErr != nil {
		return protocol.BestOfData{}, mixErr
	}
	if nat == nil {
		return protocol.BestOfData{}, errors.New("native runtime unavailable")
	}
	if err := d.bestOfHealth(mix); err != nil {
		return protocol.BestOfData{}, fmt.Errorf("runtime not ready: %w", err)
	}

	api, err := d.ensureLinear()
	if err != nil {
		return protocol.BestOfData{}, err
	}
	lctx, lcancel := context.WithTimeout(ctx, planApplyTimeout)
	detail, err := api.IssueDetail(lctx, ident)
	lcancel()
	if err != nil {
		return protocol.BestOfData{}, d.linearErr("read "+ident, err)
	}
	proj, err := d.planProject(a.Project, detail.TeamID)
	if err != nil {
		return protocol.BestOfData{}, err
	}
	if msg := d.dispatchHold(ctx, proj.Name, time.Now()); msg != "" {
		return protocol.BestOfData{}, errors.New("dispatch held: " + msg)
	}
	for _, s := range d.sessions.Snapshot() {
		if s.Source == "native" && s.Issue == detail.Identifier && nativeSessionPresent(s) {
			return protocol.BestOfData{}, fmt.Errorf("%s is already being worked on by %s — kill it first or compare its group", detail.Identifier, s.ID)
		}
	}
	if globalCap > 0 {
		if live := NativeLiveCounted(d.sessions.Snapshot()); live+len(mix) > globalCap {
			return protocol.BestOfData{}, fmt.Errorf("best-of needs %d slot(s) but only %d of global_cap=%d are free", len(mix), max(globalCap-live, 0), globalCap)
		}
	}

	if !d.beginConnWork() {
		return protocol.BestOfData{}, errors.New("daemon is shutting down")
	}
	defer d.connWg.Done()
	sctx := context.WithoutCancel(ctx)

	name, polls := proj.Name, proj.Polls()
	if polls {
		mu := d.tickMutex(name)
		mu.Lock()
		defer mu.Unlock()
	}
	if !d.inflight.Claim(detail.ID, detail.Identifier) {
		return protocol.BestOfData{}, fmt.Errorf("%s is already being worked on — check sessions", detail.Identifier)
	}
	if polls && proj.DedupMode != "state" {
		seen, _ := d.seen.load(name)
		if seen == nil {
			seen = map[string]time.Time{}
		}
		seen[detail.ID] = time.Now()
		if err := d.seen.save(name, seen); err != nil {
			d.inflight.Remove(detail.ID)
			return protocol.BestOfData{}, fmt.Errorf("persist seen for %s: %w", detail.Identifier, err)
		}
	}

	is := linear.Issue{ID: detail.ID, Identifier: detail.Identifier, Title: detail.Title, LabelIDs: detail.LabelIDs}
	pollName := ""
	if polls {
		pollName = name
	}
	sessions, failed, err := d.spawnIssue(sctx, nat, proj, is, "", mix, d.repoFor(proj), pollName)
	if err != nil {
		d.inflight.Remove(detail.ID)
		if polls && proj.DedupMode == "seen" {
			if seen, lerr := d.seen.load(name); lerr == nil {
				delete(seen, detail.ID)
				_ = d.seen.save(name, seen)
			}
		}
		return protocol.BestOfData{Issue: detail.Identifier, Failed: failed}, err
	}

	if polls {
		if proj.DedupMode == "label" {
			if current, lerr := api.IssueLabelIDs(sctx, detail.ID); lerr == nil {
				removed := intersectLabels(proj.MatchLabels, current)
				newIDs := ApplyLabelDelta(current, proj.MatchLabels, []string{proj.OnSentSetLabel})
				if serr := api.SetIssueLabels(sctx, detail.ID, newIDs); serr == nil {
					d.setRemovedLabels(sessions, removed)
				} else {
					d.logf(name, "bestOf: label flip for %s failed (seen guards dedup): %v", detail.Identifier, serr)
				}
			}
		}
		d.writeBackSpawn(sctx, api, proj, is, sessions[0])
	}

	res := protocol.BestOfData{Group: sessions[0].BestOf, Issue: detail.Identifier, Failed: failed}
	for _, s := range sessions {
		res.Sessions = append(res.Sessions, s.ID)
	}
	res.Message = fmt.Sprintf("started %s as best-of-%d in %s (group %s): %s — compare with: lola bestof compare %s",
		detail.Identifier, len(sessions), name, res.Group, strings.Join(res.Sessions, ", "), detail.Identifier)
	if len(failed) > 0 {
		res.Message += fmt.Sprintf(" (%d attempt(s) failed to spawn)", len(failed))
	}
	d.logf(name, "bestOf: %s", res.Message)
	return res, nil
}

// repoFor is the repo a project's sessions check PRs against (config.PollRepo).
func (d *Daemon) repoFor(p config.Project) string {
	d.mu.Lock()
	defer d.mu.Unlock()
	if pp := d.cfg.ProjectByName(p.Name); pp != nil {
		return d.cfg.PollRepo(pp)
	}
	return p.Repo
}

// bestOfGroup resolves a compare/pick target to the group's sessions, in
// attempt order. Exactly one of group / issue / session is consulted, in that
// order; an issue resolves to its most recently started group.
func (d *Daemon) bestOfGroup(group, issue, sessionID string) (string, []session.Session, error) {
	snap := d.sessions.Snapshot()
	group = strings.TrimSpace(group)
	switch {
	case group != "":
	case strings.TrimSpace(sessionID) != "":
		for _, s := range snap {
			if s.ID == strings.TrimSpace(sessionID) {
				if s.BestOf == "" {
					return "", nil, fmt.Errorf("session %s is not a best-of attempt", s.ID)
				}
				group = s.BestOf
			}
		}
		if group == "" {
			return "", nil, fmt.Errorf("unknown session %s", sessionID)
		}
	case strings.TrimSpace(issue) != "":
		want := strings.ToUpper(strings.TrimSpace(issue))
		var newest time.Time
		for _, s := range snap {
			if s.BestOf != "" && strings.EqualFold(s.Issue, want) && (group == "" || s.FirstSeen.After(newest)) {
				group, newest = s.BestOf, s.FirstSeen
			}
		}
		if group == "" {
			return "", nil, fmt.Errorf("no best-of group on record for %s", want)
		}
	default:
		return "", nil, errors.New("a group, issue or session is required")
	}
	var out []session.Session
	for _, s := range snap {
		if s.BestOf == group {
			out = append(out, s)
		}
	}
	if len(out) == 0 {
		return "", nil, fmt.Errorf("unknown best-of group %s", group)
	}
	slices.SortStableFunc(out, func(a, b session.Session) int { return a.Attempt - b.Attempt })
	return group, out, nil
}

// reviewTallyFor sums the recorded review tallies that belong to s's CURRENT
// PR — a tally for an earlier PR of the same session describes code that is
// no longer what is being compared. nil when no pass answered for it.
func reviewTallyFor(s session.Session) *protocol.BestOfReviewTally {
	if s.PR == nil || s.PR.Number <= 0 {
		return nil
	}
	var t protocol.BestOfReviewTally
	for _, rt := range s.ReviewTallies {
		if rt.PR != s.PR.Number {
			continue
		}
		t.Passes++
		t.Blocker += rt.Blocker
		t.Major += rt.Major
		t.Minor += rt.Minor
		t.Other += rt.Other
	}
	if t.Passes == 0 {
		return nil
	}
	return &t
}

// handleBestOfCompare serves cmd=bestOfCompare (read-only).
func (d *Daemon) handleBestOfCompare(ctx context.Context, a protocol.BestOfCompareArgs) (protocol.BestOfCompareData, error) {
	group, sessions, err := d.bestOfGroup(a.Group, a.Issue, a.Session)
	if err != nil {
		return protocol.BestOfCompareData{}, err
	}
	first := sessions[0]
	d.mu.Lock()
	base := config.DefaultBranchName
	if pp := d.cfg.ProjectByName(first.Project); pp != nil && pp.DefaultBranch != "" {
		base = pp.DefaultBranch
	}
	home := d.home
	stat := d.worktreeStat
	d.mu.Unlock()
	if stat == nil {
		stat = gitdiff.Differ{}.Stat
	}

	now := time.Now()
	out := protocol.BestOfCompareData{Group: group, Issue: first.Issue, Title: first.Title, Project: first.Project}
	facts := make([]bestof.Facts, len(sessions))
	for i, s := range sessions {
		s.EnsureAxes()
		at := protocol.BestOfAttempt{
			Session:  s.ID,
			Attempt:  s.Attempt,
			Agent:    agent.Parse(s.Agent).String(),
			Model:    s.Model,
			Branch:   s.Branch,
			Display:  string(state.DisplayFor(s.AgentState)),
			Delivery: string(s.Delivery),
			Review:   reviewTallyFor(s),
			Usage:    d.sessionUsage(s.ID),
			Age:      formatAge(now.Sub(s.FirstSeen)),
			Winner:   s.BestOfWinner,
		}
		if s.PR != nil && s.PR.Number > 0 {
			at.PRNumber, at.PRURL, at.PRState, at.Draft, at.Checks = s.PR.Number, s.PR.URL, s.PR.State, s.PR.IsDraft, s.PR.ChecksState
		}
		dir := filepath.Join(home, "worktrees", s.Project, s.ID)
		sctx, cancel := context.WithTimeout(ctx, bestOfStatTimeout)
		st, serr := stat(sctx, dir, base)
		cancel()
		if serr != nil {
			at.DiffError = serr.Error()
		} else {
			at.Files, at.Additions, at.Deletions = st.Files, st.Additions, st.Deletions
		}
		if s.BestOfWinner {
			out.Picked = s.ID
		}
		f := bestof.Facts{OpenPR: strings.EqualFold(at.PRState, "OPEN"), Checks: at.Checks, Lines: at.Additions + at.Deletions}
		if at.Review != nil {
			f.Reviewed, f.Blocker, f.Major, f.Minor = true, at.Review.Blocker, at.Review.Major, at.Review.Minor+at.Review.Other
		}
		facts[i] = f
		out.Attempts = append(out.Attempts, at)
	}
	if i := bestof.Suggest(facts); i >= 0 {
		out.Suggested = sessions[i].ID
	}
	return out, nil
}

// handleBestOfPick serves cmd=bestOfPick: keep one attempt, discard the rest.
func (d *Daemon) handleBestOfPick(ctx context.Context, a protocol.BestOfPickArgs) (protocol.BestOfPickData, error) {
	id := strings.TrimSpace(a.Session)
	if id == "" {
		return protocol.BestOfPickData{}, errors.New("bestOfPick: session required")
	}
	group, sessions, err := d.bestOfGroup("", "", id)
	if err != nil {
		return protocol.BestOfPickData{}, err
	}
	for _, s := range sessions {
		if s.BestOfWinner {
			return protocol.BestOfPickData{}, fmt.Errorf("group %s already kept %s", group, s.ID)
		}
	}
	d.mu.Lock()
	nat := d.native
	d.mu.Unlock()
	if nat == nil {
		return protocol.BestOfPickData{}, errors.New("native runtime unavailable")
	}
	if !d.beginConnWork() {
		return protocol.BestOfPickData{}, errors.New("daemon is shutting down")
	}
	defer d.connWg.Done()
	sctx := context.WithoutCancel(ctx)

	// (1) The decision first, so a crash part-way leaves it on disk and a
	// retried pick is refused rather than choosing differently.
	winner, ok := d.sessions.Update(id, func(s *session.Session) bool {
		s.BestOfWinner = true
		return true
	})
	if !ok {
		return protocol.BestOfPickData{}, fmt.Errorf("unknown session %s", id)
	}
	if err := d.sessions.Save(); err != nil {
		d.logf("", "bestOfPick: persist sessions: %v", err)
	}
	res := protocol.BestOfPickData{Winner: winner.ID}
	problem := func(format string, args ...any) {
		msg := fmt.Sprintf(format, args...)
		res.Problems = append(res.Problems, msg)
		d.logf("", "bestOfPick: %s", msg)
	}

	// (2) The kept PR leaves draft (best-effort: the pick stands either way).
	if pr := winner.PR; pr != nil && pr.Number > 0 && strings.EqualFold(pr.State, "OPEN") {
		res.PR = pr.Number
		if pr.IsDraft && winner.Repo != "" && d.markPRReady != nil {
			pctx, cancel := context.WithTimeout(sctx, bestOfPRTimeout)
			if err := d.markPRReady(pctx, winner.Repo, pr.Number); err != nil {
				problem("could not mark PR #%d ready: %v", pr.Number, err)
			}
			cancel()
		}
	}

	// (3) Every other attempt: close its PR, then tear it down dirty-safe.
	for _, s := range sessions {
		if s.ID == winner.ID {
			continue
		}
		if pr := s.PR; pr != nil && pr.Number > 0 && strings.EqualFold(pr.State, "OPEN") {
			if s.Repo == "" || d.closePR == nil {
				problem("PR #%d of %s left open (no repo on record)", pr.Number, s.ID)
			} else {
				comment := fmt.Sprintf("Closed by lola: best-of pick for %s kept %s", s.Issue, winner.ID)
				if res.PR > 0 {
					comment += fmt.Sprintf(" (#%d)", res.PR)
				}
				comment += ". The branch is kept; reopen this PR to revisit the attempt."
				pctx, cancel := context.WithTimeout(sctx, bestOfPRTimeout)
				if err := d.closePR(pctx, s.Repo, pr.Number, comment); err != nil {
					problem("could not close PR #%d of %s: %v", pr.Number, s.ID, err)
				} else {
					res.Closed = append(res.Closed, pr.Number)
				}
				cancel()
			}
		}
		switch kept, err := d.discardAttempt(sctx, nat, s); {
		case err != nil:
			problem("teardown of %s failed: %v", s.ID, err)
		case kept:
			res.Kept = append(res.Kept, s.ID)
		default:
			res.Removed = append(res.Removed, s.ID)
		}
	}

	res.Message = fmt.Sprintf("kept %s for %s", winner.ID, winner.Issue)
	if res.PR > 0 {
		res.Message += fmt.Sprintf(" (PR #%d)", res.PR)
	}
	if n := len(res.Removed); n > 0 {
		res.Message += fmt.Sprintf("; removed %d other attempt(s)", n)
	}
	if n := len(res.Kept); n > 0 {
		res.Message += fmt.Sprintf("; %d attempt(s) stopped but their worktree KEPT (uncommitted changes): %s", n, strings.Join(res.Kept, ", "))
	}
	if n := len(res.Problems); n > 0 {
		res.Message += fmt.Sprintf("; %d step(s) need a look", n)
	}
	d.logf("", "bestOfPick: %s", res.Message)
	return res, nil
}

// discardAttempt tears down one losing attempt: its review pane, its agent and
// aux tabs, and — when clean — its worktree and local branch (runtime.Kill,
// force=false). A dirty worktree is KEPT and the record stays, marked dead, so
// a human finds the uncommitted work; kept reports that case. Unlike handleKill
// it never frees the issue's in-flight claim and never strips its sent label:
// the winner is still working the issue.
func (d *Daemon) discardAttempt(ctx context.Context, nat NativeAPI, s session.Session) (kept bool, err error) {
	kctx, cancel := context.WithTimeout(ctx, killExecTimeout)
	defer cancel()
	d.killReviewPane(kctx, s.ID)
	err = nat.Kill(kctx, s, true, false)
	if errors.Is(err, worktree.ErrDirty) {
		d.sessions.Update(s.ID, func(cur *session.Session) bool {
			cur.SetAgentState(state.AgentDead, "", time.Now())
			return true
		})
		if serr := d.sessions.Save(); serr != nil {
			d.logf("", "bestOfPick: persist sessions: %v", serr)
		}
		return true, nil
	}
	if err != nil {
		return false, err
	}
	d.sessions.Delete(s.ID)
	if serr := d.sessions.Save(); serr != nil {
		d.logf("", "bestOfPick: persist sessions: %v", serr)
	}
	return false, nil
}

// recordReviewTally stores one ANSWERED review pass's severity tally on the
// session, keyed by provider kind (display-only; the compare view reads it).
func (d *Daemon) recordReviewTally(sessionID, kind string, pr int, findings string) {
	if pr <= 0 {
		return
	}
	c := reviewmd.Count(findings)
	d.sessions.Update(sessionID, func(s *session.Session) bool {
		m := maps.Clone(s.ReviewTallies)
		if m == nil {
			m = map[string]session.ReviewTally{}
		}
		m[kind] = session.ReviewTally{PR: pr, Blocker: c.Blocker, Major: c.Major, Minor: c.Minor, Other: c.Other, At: time.Now()}
		s.ReviewTallies = m
		return true
	})
}
