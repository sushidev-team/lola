package daemon

// reviewremote.go is the phone's forced review and the display-only review
// facts on a session row.
//
// A forced review runs for minutes. The desktop and the CLI wait for it on the
// socket, but a phone gives up on a request after 15 seconds and cancels the
// connection's context when the app goes to the background, which used to
// abort the pass. A phone's request therefore starts the pass detached and is
// answered at once; the row's reviewRunning and reviewTallies, the timeline's
// tally line and the PR's own threads report how it ended.

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/session"
)

// errReviewRunning refuses a phone's forced review of a session that already
// has a review pass running: a phone's detached one, a queued worker pass or a
// desktop/CLI forced pass (lanes included).
var errReviewRunning = errors.New("A review is already running for this session.")

// errReviewsBusy refuses a phone's forced review while maxDetachedReviews
// detached passes already run daemon-wide.
var errReviewsBusy = errors.New("Too many reviews are running. Try again when one finishes.")

// maxDetachedReviews bounds the phone-forced passes running at once across the
// daemon. A detached pass no longer holds its connection's request slot, so
// without this a phone could start one agent exec per session.
const maxDetachedReviews = 2

// reviewDetachMargin is added to the review budget for the detached pass, so
// the provider's own exec bound fires first and reports its own outcome.
const reviewDetachMargin = time.Minute

// startDetachedReview checks what can be answered at once (session, provider,
// in-flight guards), then runs the forced pass on its own goroutine under a
// context the phone's connection cannot cancel but daemon shutdown does (a
// review is read-only and safe to abort). The goroutine is counted as socket
// work, so shutdown's drainConnWork waits for it after cancelling it. Started
// is set when the pass was launched; Skipped answers exactly as the
// synchronous path would.
func (d *Daemon) startDetachedReview(ctx context.Context, sessionID, kind string) (protocol.ReviewData, error) {
	if sessionID == "" {
		return protocol.ReviewData{}, errors.New("session id required")
	}
	if _, ok := d.sessions.Get(sessionID); !ok {
		return protocol.ReviewData{}, errors.New("unknown session " + sessionID)
	}
	p, ok := d.resolveReviewForce(kind)
	if !ok || p.Shape != shapePass {
		// Not runnable: the synchronous path answers at once with its own words.
		return d.handleReviewProvider(ctx, sessionID, kind)
	}
	d.reviewMu.Lock()
	if d.reviewRunningLocked(sessionID) {
		d.reviewMu.Unlock()
		return protocol.ReviewData{}, errReviewRunning
	}
	if len(d.reviewDetached) >= maxDetachedReviews {
		d.reviewMu.Unlock()
		return protocol.ReviewData{}, errReviewsBusy
	}
	if !d.beginConnWork() {
		d.reviewMu.Unlock()
		return protocol.ReviewData{}, errors.New("daemon is shutting down")
	}
	if d.reviewDetached == nil {
		d.reviewDetached = map[string]bool{}
	}
	d.reviewDetached[sessionID] = true
	d.reviewMu.Unlock()

	budget := d.reviewChainBudget(p) + reviewDetachMargin
	run := d.runDetachedReview
	if run == nil {
		run = func(ctx context.Context, id, kind string) (protocol.ReviewData, error) {
			return d.handleReviewProvider(ctx, id, kind)
		}
	}
	parent := d.shutdownCtx
	if parent == nil {
		parent = context.WithoutCancel(ctx)
	}
	runCtx, cancel := context.WithTimeout(parent, budget)
	go func() {
		defer d.connWg.Done()
		defer cancel()
		defer func() {
			d.reviewMu.Lock()
			delete(d.reviewDetached, sessionID)
			d.reviewMu.Unlock()
			d.pokeReviewRow(sessionID)
		}()
		defer func() {
			if r := recover(); r != nil {
				d.logf("", "review: detached pass for %s panicked: %v", sessionID, r)
			}
		}()
		if data, err := run(runCtx, sessionID, kind); err != nil {
			d.logf("", "review: detached pass for %s: %v", sessionID, err)
		} else if data.Skipped != "" {
			d.logf("", "review: detached pass for %s skipped: %s", sessionID, data.Skipped)
		}
	}()
	return protocol.ReviewData{Started: true, Message: "review started: findings appear on the pull request and in the session timeline"}, nil
}

// reviewChainBudget is the time runReviewChain may take for p: the chain runs
// the primary and then each fallback in sequence, so the bound is the SUM of
// their exec timeouts (0 or an unknown kind counts the default).
func (d *Daemon) reviewChainBudget(p reviewProvider) time.Duration {
	d.mu.Lock()
	defer d.mu.Unlock()
	timeout := func(k provKind) int {
		for _, q := range d.reviewProviders {
			if q.Kind == k && q.TimeoutSeconds > 0 {
				return q.TimeoutSeconds
			}
		}
		return config.DefaultReviewTimeoutSeconds
	}
	secs := 0
	if p.TimeoutSeconds > 0 {
		secs = p.TimeoutSeconds
	} else {
		secs = timeout(p.Kind)
	}
	for _, k := range p.Fallback {
		secs += timeout(k)
	}
	return time.Duration(secs) * time.Second
}

// pokeReviewRow tells the sessions topic a row's review facts may have moved.
// Blind and lock-free like every poke.
func (d *Daemon) pokeReviewRow(id string) {
	if d.watch != nil {
		d.watch.PokeSession(session.BaseID(id), pokeRow)
	}
}

// reviewRunningFor reports whether any review pass (queued worker pass,
// forced pass or a phone's detached one) is running for the session or one of
// its stack lanes. Display only.
func (d *Daemon) reviewRunningFor(id string) bool {
	d.reviewMu.Lock()
	defer d.reviewMu.Unlock()
	return d.reviewRunningLocked(id)
}

// reviewRunningLocked is reviewRunningFor with reviewMu held.
func (d *Daemon) reviewRunningLocked(id string) bool {
	if d.reviewDetached[id] {
		return true
	}
	for key, n := range d.reviewActive {
		if n > 0 && (key == id || strings.HasPrefix(key, id+"~")) {
			return true
		}
	}
	return false
}

// reviewTallyInfos lists the per-kind tallies recorded for the session's
// CURRENT PR, by kind. A tally for an earlier PR is not this PR's review.
func reviewTallyInfos(s session.Session) []protocol.ReviewTallyInfo {
	if s.PR == nil || s.PR.Number <= 0 || len(s.ReviewTallies) == 0 {
		return nil
	}
	var out []protocol.ReviewTallyInfo
	for kind, t := range s.ReviewTallies {
		if t.PR != s.PR.Number {
			continue
		}
		out = append(out, protocol.ReviewTallyInfo{Kind: kind, Blocker: t.Blocker, Major: t.Major, Minor: t.Minor, Other: t.Other, At: t.At})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Kind < out[j].Kind })
	return out
}
