package daemon

// reviewgate.go turns a review pass into a check a ruleset can REQUIRE.
//
// A review lola posts is a COMMENT review under the operator's own gh login,
// and GitHub refuses REQUEST_CHANGES on a PR authored by that same account, so
// a review can never block a merge on its own. A commit status can: with
// `github_status = true` a pass posts `lola/review/<kind>` on the PR head —
// pending while it runs, then success, failure (blocking findings) or error
// (it could not run). Rules that keep it honest:
//
//   - A status belongs to ONE commit. A new head therefore has no lola status
//     at all, which a required check reads as "Expected" and refuses to merge,
//     and the head is re-reviewed once the agent has SETTLED (not mid-turn), so
//     a burst of pushes costs one round. This is also what makes resolving a
//     review thread a claim rather than the gate: whatever the worker says it
//     fixed, the next round reads the code.
//   - Rounds are capped per PR (reviewGateRounds). Past the cap the new head
//     gets an `error` status naming `lola review`, never a carried-over
//     success: fail closed, and a human can force the next round.
//   - Blocking = blocker + major + any finding whose severity could not be
//     parsed. A minor-only review passes; an unknown severity never does.
//   - A pass that could not answer and will be retried keeps `pending`; one
//     that gave up posts `error`.
//   - The status texts are lola's own: findings are model output and never
//     reach a status description.
//   - lola's own statuses are left out of the CI verdict (scm.IsLolaContext),
//     so a failing review never becomes a ci_failed prompt; the merge queue
//     reads the gate directly instead (reviewGatesOpen).

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"time"

	"github.com/sushidev-team/lola/internal/reviewmd"
	"github.com/sushidev-team/lola/internal/scm"
	"github.com/sushidev-team/lola/internal/session"
	"github.com/sushidev-team/lola/internal/state"
)

// reviewGateRounds caps the automatic review rounds per PR and kind.
const reviewGateRounds = 3

var errReviewHeadChanged = errors.New("review checkout does not match the PR head")

func (d *Daemon) verifyGatedReview(ctx context.Context, s session.Session, p reviewProvider, dir string) error {
	if !p.Status || s.PR == nil || s.PR.Number <= 0 {
		return nil
	}
	if s.PR.HeadSHA == "" || d.verifyReviewHead == nil {
		return errReviewHeadChanged
	}
	cctx, cancel := context.WithTimeout(ctx, observeExecTimeout)
	defer cancel()
	if err := d.verifyReviewHead(cctx, dir, s.PR.HeadSHA); err != nil {
		return fmt.Errorf("%w: %v", errReviewHeadChanged, err)
	}
	return nil
}

// reviewStatusContext is the commit-status context a kind posts under.
func reviewStatusContext(k provKind) string { return scm.LolaStatusPrefix + "review/" + string(k) }

// agentSettled reports whether the worker is between turns — the moment a new
// head is worth reviewing (a mid-turn head is likely to move again).
func agentSettled(s session.Session) bool {
	return s.AgentState != state.AgentWorking && s.AgentState != state.AgentStarting
}

// headReviewDue reports whether an already-reviewed PR has a new head the gate
// has not judged yet and the agent has settled on it.
func headReviewDue(s session.Session, p reviewProvider) bool {
	if !p.Status || s.PR == nil || s.PR.HeadSHA == "" {
		return false
	}
	g, ok := s.ReviewGates[string(p.Kind)]
	if !ok || g.PR != s.PR.Number {
		// Reviewed, but never gated: the PR predates `github_status`. Gate it
		// once, or a required lola status would never report on it.
		return agentSettled(s)
	}
	return g.Head != s.PR.HeadSHA && g.Posted != s.PR.HeadSHA && agentSettled(s)
}

// reviewPass runs one review chain with the gate around it. auto marks the
// observer-driven pass, whose "could not answer" outcomes are retried
// (noteReviewOutcome); a manual one is final.
func (d *Daemon) reviewPass(ctx context.Context, s session.Session, p reviewProvider, auto bool) reviewResult {
	repo, sha, gated := d.beginReviewGate(ctx, s, p)
	res := d.runReviewChain(ctx, s, p)
	retry := false
	if auto && s.PR != nil {
		retry = d.noteReviewOutcome(s, p, res, s.PR.Number)
	}
	if gated {
		state, desc := reviewVerdict(res, retry)
		d.postReviewStatus(ctx, s, p, repo, sha, state, desc)
	}
	return res
}

// beginReviewGate records the round and posts `pending` on the head being
// reviewed. gated is false when the provider posts no status or the PR cannot
// be addressed.
func (d *Daemon) beginReviewGate(ctx context.Context, s session.Session, p reviewProvider) (repo, sha string, gated bool) {
	if !p.Status || d.setCommitStatus == nil || (s.Stack && s.Lane == "") || s.PR == nil || s.PR.Number <= 0 || s.PR.HeadSHA == "" {
		return "", "", false
	}
	repo = d.repoForSession(s)
	if repo == "" {
		return "", "", false
	}
	sha = s.PR.HeadSHA
	round := 0
	d.sessions.Update(s.ID, func(cur *session.Session) bool {
		g := cur.ReviewGates[string(p.Kind)]
		if g.PR != s.PR.Number {
			g = session.ReviewGate{PR: s.PR.Number}
		}
		if g.Head != sha {
			g.Head = sha
			g.Rounds++
		}
		// Invalidate an earlier success even if posting pending fails.
		g.State, g.Posted = scm.StatusPending, ""
		round = g.Rounds
		cur.ReviewGates = maps.Clone(cur.ReviewGates)
		if cur.ReviewGates == nil {
			cur.ReviewGates = map[string]session.ReviewGate{}
		}
		cur.ReviewGates[string(p.Kind)] = g
		return true
	})
	d.postReviewStatus(ctx, s, p, repo, sha, scm.StatusPending,
		fmt.Sprintf("Review running (round %d)", round))
	d.reviewSave()
	return repo, sha, true
}

// reviewVerdict maps a chain outcome onto a commit status.
func reviewVerdict(res reviewResult, retry bool) (string, string) {
	switch {
	case res.Ran:
		c := reviewmd.Count(res.Findings)
		total := c.Blocker + c.Major + c.Minor + c.Other
		blocking := c.Blocker + c.Major + c.Other
		switch {
		case total == 0:
			return scm.StatusSuccess, "No findings"
		case blocking == 0:
			return scm.StatusSuccess, fmt.Sprintf("%d minor finding(s), none blocking", total)
		}
		return scm.StatusFailure, fmt.Sprintf("%d blocking finding(s) of %d — see the review on the PR", blocking, total)
	case retry:
		return scm.StatusPending, "Review could not answer yet — retrying"
	}
	return scm.StatusError, "Review did not run — check the lola log, then run `lola review`"
}

// gateLimitReached posts the round-cap error on the new head, once.
func (d *Daemon) gateLimitReached(ctx context.Context, s session.Session, p reviewProvider) {
	repo := d.repoForSession(s)
	if repo == "" {
		return
	}
	d.logf("", "review: %s (%s) PR #%d reached %d review rounds — not re-reviewing head %s on its own",
		s.ID, p.Kind, s.PR.Number, reviewGateRounds, shortSHA(s.PR.HeadSHA))
	d.postReviewStatus(ctx, s, p, repo, s.PR.HeadSHA, scm.StatusError,
		fmt.Sprintf("Stopped after %d review rounds — run `lola review %s` to review this commit", reviewGateRounds, s.ID))
}

// postReviewStatus posts one status and records it on the gate. A failed post
// is logged and left unrecorded: a required check that never reports blocks
// the merge, which is the safe direction.
func (d *Daemon) postReviewStatus(ctx context.Context, s session.Session, p reviewProvider, repo, sha, st, desc string) {
	url := ""
	if s.PR != nil {
		url = s.PR.URL
	}
	cctx, cancel := context.WithTimeout(ctx, observeExecTimeout)
	err := d.setCommitStatus(cctx, repo, sha, st, reviewStatusContext(p.Kind), desc, url)
	cancel()
	if err != nil {
		d.logf("", "review: %s could not post %s status %q on %s: %v", s.ID, p.Kind, st, shortSHA(sha), err)
		return
	}
	d.sessions.Update(s.ID, func(cur *session.Session) bool {
		if cur.PR == nil || s.PR == nil || cur.PR.Number != s.PR.Number {
			return false // a completed pass must not overwrite a newer PR's gate
		}
		g, ok := cur.ReviewGates[string(p.Kind)]
		if !ok || cur.PR == nil || g.PR != cur.PR.Number {
			g = session.ReviewGate{PR: s.PR.Number, Head: g.Head, Rounds: g.Rounds}
		}
		g.State, g.Posted, g.At = st, sha, time.Now()
		cur.ReviewGates = maps.Clone(cur.ReviewGates)
		if cur.ReviewGates == nil {
			cur.ReviewGates = map[string]session.ReviewGate{}
		}
		cur.ReviewGates[string(p.Kind)] = g
		return true
	})
	d.reviewSave()
}

// reviewGatesOpen reports whether every enabled status-posting provider has
// passed THIS head of s's PR. The merge queue reads it because lola's own
// statuses are deliberately absent from the CI verdict.
func (d *Daemon) reviewGatesOpen(s session.Session) bool {
	if s.PR == nil {
		return false
	}
	for _, p := range d.appliesIndependently() {
		if p.Shape != shapePass || !p.Status {
			continue
		}
		g := s.ReviewGates[string(p.Kind)]
		if g.PR != s.PR.Number || g.Posted != s.PR.HeadSHA || g.State != scm.StatusSuccess {
			return false
		}
	}
	return true
}

// repoForSession resolves the GitHub repo a session's PR lives in.
func (d *Daemon) repoForSession(s session.Session) string {
	if s.Repo != "" {
		return s.Repo
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if p := d.cfg.ProjectByName(s.Project); p != nil {
		return p.Repo
	}
	return ""
}
