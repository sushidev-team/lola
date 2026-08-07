package daemon

// reviewworker.go moves the review PASS off the observe loop.
//
// A pass provider's exec is the one review-side call that runs for MINUTES: a
// claude-session review reads the PR's files before it reports anything, so a
// real PR takes 7-13 minutes where a CodeRabbit CLI pass takes one or two. Run
// inline (as it was until this file existed) that exec stalled the whole
// observe cycle — no tmux liveness, no PR facts, no reactions for anything else
// in the snapshot — for as long as it ran, which is also why its budget could
// never simply be raised.
//
// So the observer only ENQUEUES: it evaluates the cheap preconditions
// (independently-applying pass provider, native session, PR open, guard not yet
// stamped) and hands the job to a single worker that drains the queue one at a
// time. That serialization is deliberate — two concurrent claude reviews are
// two concurrent paid execs — and it is the same shape as the [statusagent]
// interpreter's worker.
//
// The watch shape stays inline in the observer: it is one bounded `gh` call.

import (
	"context"
	"fmt"

	"github.com/sushidev-team/lola/internal/session"
)

const (
	// reviewQueueCap bounds the pending-pass queue. A full queue DROPS the job
	// (logged); the next observe cycle re-queues it, because the once-per-PR
	// guard is only stamped by the run itself.
	reviewQueueCap = 8
	// reviewMaxAttempts bounds how often a single session/kind/PR may be retried
	// after a "could not answer" outcome (timeout / over quota / no provider
	// available). The first attempt plus reviewMaxAttempts-1 retries; after that
	// the guard is left stamped and the PR is not reviewed again. Without a
	// ceiling a permanently-slow provider would re-burn its full timeout every
	// observe cycle, forever.
	reviewMaxAttempts = 3
)

// reviewJob is one queued pass: run provider Kind for session ID.
type reviewJob struct {
	ID   string
	Kind provKind
}

// reviewKey identifies a session/kind pair for the busy set.
func reviewKey(id string, k provKind) string { return id + "|" + string(k) }

// reviewFailKey identifies a session/kind/PR triple for the attempt counter, so
// a new PR on the same session starts with a fresh budget.
func reviewFailKey(id string, k provKind, pr int) string {
	return fmt.Sprintf("%s|%s|%d", id, k, pr)
}

// queueReviewProviders is the observer's entry point, replacing the inline
// runReviewProviders: every independently-applying WATCH provider polls inline
// (one bounded gh call), every PASS provider is queued for the worker.
func (d *Daemon) queueReviewProviders(ctx context.Context, s session.Session) {
	for _, p := range d.appliesIndependently() {
		if p.Shape == shapeWatch {
			d.runProviderWatch(ctx, s, p)
			continue
		}
		d.queueReviewPass(s, p)
	}
}

// queueReviewPass enqueues one pass provider for session s when it is due. It
// re-checks the SAME preconditions runProviderPassOnPROpen checks (which the
// worker re-applies on the fresh record before the exec) so a session that
// cannot possibly review never occupies a queue slot, and skips a session/kind
// already queued or running.
func (d *Daemon) queueReviewPass(s session.Session, p reviewProvider) {
	if !p.Enabled || !p.OnPROpen {
		return
	}
	if s.Source != "native" || !isReviewablePROpen(s.PR) {
		return
	}
	if s.ReviewedPRs[string(p.Kind)] == s.PR.Number {
		return // already reviewed this PR for this kind
	}
	key := reviewKey(s.ID, p.Kind)
	d.reviewMu.Lock()
	if d.reviewBusy == nil {
		d.reviewBusy = map[string]bool{}
	}
	if d.reviewBusy[key] {
		d.reviewMu.Unlock()
		return // already queued or running
	}
	ch := d.reviewCh
	if ch == nil {
		d.reviewMu.Unlock()
		return // no worker (tests that never call Run): nothing to queue onto
	}
	d.reviewBusy[key] = true
	d.reviewMu.Unlock()

	select {
	case ch <- reviewJob{ID: s.ID, Kind: p.Kind}:
	default:
		d.clearReviewBusy(key)
		d.logf("", "review: pass queue full — dropping %s (%s), a later cycle will re-queue", s.ID, p.Kind)
	}
}

func (d *Daemon) clearReviewBusy(key string) {
	d.reviewMu.Lock()
	delete(d.reviewBusy, key)
	d.reviewMu.Unlock()
}

// reviewLoop is the single pass worker: it drains the queue one review at a
// time (a natural global concurrency cap of 1) until the run context is
// cancelled. It runs on the CANCELLABLE run context, deliberately not
// shutdown-shielded: a review is a read-only exec and safe to abort, and
// shielding a 15-minute pass would hang graceful shutdown at d.wg.Wait().
func (d *Daemon) reviewLoop(ctx context.Context) {
	defer d.wg.Done()
	for {
		select {
		case <-ctx.Done():
			return
		case job := <-d.reviewCh:
			d.runQueuedReview(ctx, job)
		}
	}
}

// runQueuedReview runs one queued pass against the FRESH record (the queued
// snapshot is by now at least a cycle old: the PR may have merged, the guard
// may have been stamped by the manual command). Panic-guarded so one bad pass
// can never take the worker down with it.
func (d *Daemon) runQueuedReview(ctx context.Context, job reviewJob) {
	defer d.clearReviewBusy(reviewKey(job.ID, job.Kind))
	defer func() {
		if r := recover(); r != nil {
			d.logf("", "review: pass for %s (%s) panicked: %v", job.ID, job.Kind, r)
		}
	}()
	s, ok := d.sessions.Get(job.ID)
	if !ok {
		return
	}
	p, ok := d.providerByKind(job.Kind)
	if !ok {
		return // the kind was reconfigured away while the job waited
	}
	d.runProviderPassOnPROpen(ctx, s, p)
}

// noteReviewOutcome records the result of one chain run for the retry budget.
//
// The chain guard (ReviewedPRs[kind]) is stamped BEFORE the exec so a crash
// mid-review can never double-fire. That is right for a crash and wrong for a
// timeout: a pass that never got to answer would keep its guard and the PR
// would never be reviewed at all — which is exactly what happened when the
// claude-session timeout was too small for a real PR. So a "could not answer"
// outcome (timeout / quota / no available provider) UN-stamps the guard, up to
// reviewMaxAttempts per PR, and a later cycle retries. A real answer (findings
// or clean) and a graceful stop (auth / nonzero exit — operator problems that a
// retry cannot fix) leave the guard stamped.
func (d *Daemon) noteReviewOutcome(s session.Session, p reviewProvider, res reviewResult, pr int) {
	if pr <= 0 {
		return
	}
	key := reviewFailKey(s.ID, p.Kind, pr)
	if !canRetryReview(res) {
		d.reviewMu.Lock()
		delete(d.reviewFails, key)
		d.reviewMu.Unlock()
		return
	}

	d.reviewMu.Lock()
	if d.reviewFails == nil {
		d.reviewFails = map[string]int{}
	}
	d.reviewFails[key]++
	attempts := d.reviewFails[key]
	d.reviewMu.Unlock()

	if attempts >= reviewMaxAttempts {
		d.logf("", "review: %s (%s) could not answer PR #%d after %d attempts — giving up on this PR",
			s.ID, p.Kind, pr, attempts)
		return
	}
	d.unstampReviewed(s.ID, p.Kind, pr)
	d.logf("", "review: %s (%s) will retry PR #%d on a later cycle (attempt %d of %d)",
		s.ID, p.Kind, pr, attempts, reviewMaxAttempts)
}

// canRetryReview reports whether a chain outcome is worth another attempt: the
// provider could not answer (a fallback-class error, or no available provider
// at all). A successful run — including a CLEAN one — and a graceful stop are
// both final.
func canRetryReview(res reviewResult) bool {
	if res.Ran {
		return false
	}
	if res.Err != nil {
		return isFallbackErr(res.Err)
	}
	return res.Skipped == noProviderSkip
}

// unstampReviewed clears the once-per-PR pass guard so the next observe cycle
// re-queues this session/kind. It only clears a guard that still points at pr,
// so it can never undo a stamp another writer just made for a newer PR.
func (d *Daemon) unstampReviewed(id string, k provKind, pr int) {
	d.sessions.Update(id, func(cur *session.Session) bool {
		if cur.ReviewedPRs == nil || cur.ReviewedPRs[string(k)] != pr {
			return false
		}
		delete(cur.ReviewedPRs, string(k))
		return true
	})
	d.reviewSave()
}
