package daemon

// mergequeue.go is lola's LOCAL merge queue ([merge_queue], off by default):
// approved + green PRs of lola's own sessions land ONE AT A TIME per
// repository, and each one is brought up to date with the default branch and
// re-tested before it does. Parallel agents produce parallel PRs; merging them
// by hand means rebasing every survivor after each merge. The queue does that
// loop instead, through the agents that wrote the code.
//
// One pass per observe cycle, after the per-session loop has refreshed every
// PR's facts. Per repository:
//
//  1. MEMBERS are lola-owned agent sessions whose PR is open, not a draft,
//     approved and not failing CI. A failing PR is the ci_failed reaction's job
//     and leaves the queue until it is green again.
//  2. Members are ordered oldest PR first; only the HEAD is ever acted on, so
//     at most one merge per repository per cycle and the rest wait their turn.
//  3. The head is merged only when every fact agrees: checks pass, GitHub says
//     MERGEABLE, its base is the project's default branch, and the head commit
//     already contains the tip of that branch (REST compare, behind_by == 0) —
//     i.e. CI ran on exactly what will land. The merge re-reads the PR first
//     and is pinned to the judged commit (`gh pr merge --match-head-commit`),
//     so a push in between makes GitHub refuse it rather than land untested
//     code.
//  4. A head that is BEHIND gets one sync request per head commit: its agent
//     is asked to merge the default branch in and push, through the same idle
//     gate and live pane proof as every other send (typeAtRestingPrompt). Its
//     push moves the head, CI re-runs, and step 3 picks it up when green.
//  5. A conflicting head is left to the merge_conflict reaction (the existing
//     conflict-resolution prompt); the queue holds behind it.
//
// FAIL CLOSED on every unknown: stale PR facts, an UNKNOWN mergeability, a
// compare call that cannot answer, a base that is not the default branch, a
// project that left config — each holds the queue for that repository and
// takes no action. The worst outcome of a wrong "wait" is a slower merge; the
// worst outcome of a wrong "merge" is untested code on the default branch.
//
// What it deliberately does NOT do: merge a PR lola does not own (a `pr`
// session tracks someone else's branch), bypass branch protection (gh is
// refused like any user would be — recorded as failed, retried after a
// cool-down), or re-approve a PR whose approval GitHub dismissed on the sync
// push (that PR simply leaves the queue until a human approves it again).

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/notify"
	"github.com/sushidev-team/lola/internal/session"
)

// Merge-queue steps, as shipped in protocol.MergeQueueInfo.Step.
const (
	mqQueued   = "queued"
	mqWaiting  = "waiting"
	mqSyncing  = "syncing"
	mqMerging  = "merging"
	mqConflict = "conflict"
	mqBlocked  = "blocked"
	mqFailed   = "failed"
)

// Guard actions (session.MergeQueueGuard.Action).
const (
	mqActSync   = "sync"
	mqActMerge  = "merge"
	mqActFailed = "failed"
)

// mergeRetryAfter is how long a merge GitHub refused stays refused for the
// same head commit before the queue tries again. A refusal is usually durable
// (branch protection, a required check this lola cannot see), but a 502 is not,
// and a transient error must not stall a repository's queue forever.
const mergeRetryAfter = 10 * time.Minute

// queueMember reports whether a session's PR is in the merge queue.
func queueMember(s session.Session) bool {
	if s.Source != "native" || !s.HasAgent() || !s.OwnsBranch() || s.Branch == "" {
		return false
	}
	pr := s.PR
	if pr == nil || pr.State != "OPEN" || pr.IsDraft || pr.ReviewDecision != "APPROVED" {
		return false
	}
	return pr.ChecksState != "fail"
}

// queueEntry is one member with the repository facts it is judged against.
type queueEntry struct {
	s          session.Session
	repo, base string
}

// runMergeQueue is one pass of the queue over the store. ctx is the observer's
// unbounded context; every gh call carves its own observeExecTimeout.
func (d *Daemon) runMergeQueue(ctx context.Context) {
	d.mu.Lock()
	mq := d.cfg.MergeQueue
	method := d.cfg.MergeQueueMethod()
	type proj struct{ repo, base string }
	projects := make(map[string]proj, len(d.cfg.Projects))
	for _, p := range d.cfg.Projects {
		base := p.DefaultBranch
		if base == "" {
			base = config.DefaultBranchName
		}
		projects[p.Name] = proj{repo: p.Repo, base: base}
	}
	notifier := d.notifier
	d.mu.Unlock()
	if notifier == nil {
		notifier = notify.New(notify.NotifyConfig{})
	}

	snap := d.sessions.Snapshot()
	type posture struct {
		pos  int
		step string
	}
	want := map[string]posture{}
	if mq.Enabled {
		byRepo := map[string][]queueEntry{}
		for _, s := range snap {
			p, ok := projects[s.Project]
			if !ok || !queueMember(s) {
				continue // a project that left config has no default branch to judge against
			}
			repo := s.Repo
			if repo == "" {
				repo = p.repo
			}
			if repo == "" {
				continue
			}
			byRepo[repo] = append(byRepo[repo], queueEntry{s: s, repo: repo, base: p.base})
		}
		for _, entries := range byRepo {
			sort.SliceStable(entries, func(i, j int) bool { return entries[i].s.PR.Number < entries[j].s.PR.Number })
			for i, e := range entries {
				want[e.s.ID] = posture{pos: i + 1, step: mqQueued}
			}
			head := entries[0]
			want[head.s.ID] = posture{pos: 1, step: d.advanceQueueHead(ctx, head, method, mq.AllowNoChecks, notifier)}
		}
	}

	for _, s := range snap {
		w := want[s.ID]
		if s.MergeQueuePos == w.pos && s.MergeQueueStep == w.step {
			continue
		}
		d.sessions.Update(s.ID, func(cur *session.Session) bool {
			if cur.MergeQueuePos == w.pos && cur.MergeQueueStep == w.step {
				return false
			}
			cur.MergeQueuePos, cur.MergeQueueStep = w.pos, w.step
			return true
		})
	}
}

// advanceQueueHead judges the head of one repository's queue, takes at most
// one action (a sync request or a merge) and returns its display step.
func (d *Daemon) advanceQueueHead(ctx context.Context, e queueEntry, method string, allowNoChecks bool, notifier notify.Notifier) string {
	s, pr := e.s, e.s.PR
	switch {
	case s.PRFetchFailures > 0, pr.HeadSHA == "", pr.BaseRef == "":
		return mqBlocked // facts are stale or predate the fields the queue needs
	case pr.BaseRef != e.base:
		return mqBlocked // merges into something other than the default branch: not ours to land
	case pr.Mergeable == "CONFLICTING":
		return mqConflict
	case pr.ChecksState == "pending":
		return mqWaiting
	case pr.ChecksState == "none" && !allowNoChecks:
		return mqBlocked
	case pr.Mergeable != "MERGEABLE":
		return mqWaiting // UNKNOWN: GitHub is still computing it after a push
	}

	if g := s.MergeQueueGuard; g != nil && g.HeadSHA == pr.HeadSHA {
		switch g.Action {
		case mqActMerge:
			return mqMerging // GitHub reports MERGED on the next fetch
		case mqActSync:
			return mqSyncing // asked already; the agent's push moves the head and re-arms this
		case mqActFailed:
			if time.Since(g.At) < mergeRetryAfter {
				return mqFailed
			}
		}
	}

	cctx, cancel := context.WithTimeout(ctx, observeExecTimeout)
	behind, err := d.behindBy(cctx, e.repo, e.base, pr.HeadSHA)
	cancel()
	if err != nil {
		d.logf("", "mergequeue: %s (#%d) behind-check failed, holding the queue: %v", s.ID, pr.Number, err)
		return mqBlocked
	}
	if behind > 0 {
		return d.queueSync(ctx, s, e.base, behind)
	}
	return d.queueMerge(ctx, e, method, allowNoChecks, notifier)
}

// queueSync asks the head's agent to merge the default branch in, once per
// head commit. A busy agent is simply asked again next cycle — nothing is
// stamped until the text was actually typed.
func (d *Daemon) queueSync(ctx context.Context, s session.Session, base string, behind int) string {
	sha := s.PR.HeadSHA
	err := d.typeAtRestingPrompt(ctx, s, mergeQueueSyncMessage(s, base, behind), func(cur *session.Session) {
		cur.MergeQueueGuard = &session.MergeQueueGuard{Action: mqActSync, HeadSHA: sha, At: time.Now()}
	})
	switch {
	case err == nil:
		d.logf("", "mergequeue: %s (#%d) is %d behind %s — asked the agent to merge it in", s.ID, s.PR.Number, behind, base)
		return mqSyncing
	case errors.Is(err, errNotResting), errors.Is(err, errLeftPrompt):
		return mqWaiting
	default:
		// The gate was consumed and the guard stamped: the send failed after
		// that, and like every send path it is not retried for this commit.
		d.logf("", "mergequeue: sync request to %s failed: %v", s.ID, err)
		return mqSyncing
	}
}

// queueMerge re-reads the PR, re-checks every fact against the judged head and
// merges it pinned to that commit.
func (d *Daemon) queueMerge(ctx context.Context, e queueEntry, method string, allowNoChecks bool, notifier notify.Notifier) string {
	s, judged := e.s, e.s.PR
	cctx, cancel := context.WithTimeout(ctx, observeExecTimeout)
	fresh, err := d.prForBranch(cctx, e.repo, s.Branch)
	cancel()
	if err != nil || fresh == nil {
		d.logf("", "mergequeue: %s (#%d) pre-merge re-read failed, not merging: %v", s.ID, judged.Number, err)
		return mqBlocked
	}
	checksOK := fresh.ChecksState == "pass" || (allowNoChecks && fresh.ChecksState == "none")
	if fresh.Number != judged.Number || fresh.State != "OPEN" || fresh.IsDraft ||
		fresh.HeadSHA != judged.HeadSHA || fresh.BaseRef != e.base ||
		fresh.ReviewDecision != "APPROVED" || fresh.Mergeable != "MERGEABLE" || !checksOK {
		return mqWaiting // the PR moved since this cycle's fetch: judge it again next cycle
	}

	cctx, cancel = context.WithTimeout(ctx, observeExecTimeout)
	err = d.mergePR(cctx, e.repo, fresh.Number, method, fresh.HeadSHA)
	cancel()
	guard := &session.MergeQueueGuard{Action: mqActMerge, HeadSHA: fresh.HeadSHA, At: time.Now()}
	if err != nil {
		guard.Action, guard.Note = mqActFailed, clipCause(err)
	}
	d.sessions.Update(s.ID, func(cur *session.Session) bool {
		cur.MergeQueueGuard = guard
		return true
	})
	if err := d.sessions.Save(); err != nil {
		d.logf("", "mergequeue: persist sessions: %v", err)
	}
	if err != nil {
		d.logf("", "mergequeue: %s — GitHub refused to merge #%d: %v", s.ID, fresh.Number, err)
		notifier.Notify(ctx, notify.Note{
			Title:    "Merge queue: merge refused",
			Body:     fmt.Sprintf("%s (#%d) could not be merged: %s", issueLabel(s), fresh.Number, guard.Note),
			Priority: notify.Action,
			URL:      prURL(s),
		})
		return mqFailed
	}
	d.logf("", "mergequeue: %s — merged #%d (%s) into %s", s.ID, fresh.Number, method, e.base)
	notifier.Notify(ctx, notify.Note{
		Title:    "Merge queue: merged",
		Body:     fmt.Sprintf("%s (#%d) landed on %s", issueLabel(s), fresh.Number, e.base),
		Priority: notify.Info,
		URL:      prURL(s),
	})
	return mqMerging
}

// mergeQueueSyncMessage is the sync request typed into the head's agent. Like
// resolveConflictMessage it is lola's OWN text — the outside values are the
// configured default branch, the session's Linear identifier, its PR number
// and a commit count — and it is sanitized like every other send.
func mergeQueueSyncMessage(s session.Session, base string, behind int) string {
	subject := "Your PR"
	if s.PR != nil {
		subject = fmt.Sprintf("Your PR (#%d)", s.PR.Number)
	}
	if s.Issue != "" {
		subject += " for " + s.Issue
	}
	return sanitizeAgentText(fmt.Sprintf(
		"%s is next in lola's merge queue, but %s has moved on by %d commit(s) since it was tested. Bring it up to date so CI runs on what will actually land:\n"+
			"1. git fetch origin %s\n"+
			"2. git merge origin/%s\n"+
			"3. If anything conflicts, resolve it keeping the intent of BOTH sides — never drop the other branch's changes to make the merge go through.\n"+
			"4. Run the project's checks, then commit the merge and push. Do not merge the PR yourself — lola merges it once CI is green.\n"+
			"If a conflict is genuinely ambiguous, stop and say so instead of guessing.",
		subject, base, behind, base, base))
}

// mergeQueueOn tells the approved reaction that the queue takes the PR from
// here, so its notification does not tell a human to merge something lola is
// about to merge itself.
func (d *Daemon) mergeQueueOn() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.cfg.MergeQueue.Enabled
}
