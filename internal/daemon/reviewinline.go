package daemon

// reviewinline.go is the INLINE shape of the `github` review transport: instead
// of one flat PR comment carrying every finding, a review is posted as a summary
// body plus one ANCHORED thread per finding — each with GitHub's own reply box
// and "Resolve conversation" button, which only a review thread ever gets.
//
// It sits UNDER postGithubSink, never beside it: the plain comment
// (scm.PostPRComment) stays the floor this feature stands on, and every reason
// the inline shape cannot be used lands there instead. That is the whole safety
// story, because the reviews endpoint is ATOMIC — one comment naming a line
// outside the PR's diff rejects the entire review with 422 — so the pipeline is:
//
//	fetch head sha + diff (scm.PRReviewTarget)
//	  → decide what may be anchored (internal/diffanchor)
//	  → split findings into threads + summary body (internal/reviewmd)
//	  → post ONE COMMENT review (scm.PostPRReview)
//
// and any step that cannot answer returns false, whereupon the caller posts the
// plain comment exactly as it always has. The one case that does NOT downgrade
// is a TRANSIENT gh failure: the settle guard is left unstamped and the next
// observer cycle retries the inline post, because silently flattening a review
// over a 502 would make the shape depend on the weather.
//
// Two rules keep the result honest rather than merely pretty:
//
//   - a finding whose line is not in the diff is NOT dropped and NOT re-anchored
//     at a guess: it stays in the summary body, and the body says how many did,
//     so the PR never shows fewer findings than the review produced;
//   - an anchor SNAPPED to a nearby diff line says so in its own body
//     (reviewmd renders the reported location) — a comment sitting on a line it
//     is not about, with nothing saying so, is worse than one in the body.
//
// The untrusted-output rules are unchanged from the flat comment: the findings
// are human-sink text (a PR comment), so they are NOT control-sanitized, but
// every body — summary and thread alike — goes through neutralizeBotTriggers so
// an `@coderabbitai` string inside a finding can never start a fresh CodeRabbit
// review on the PR.

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/sushidev-team/lola/internal/diffanchor"
	"github.com/sushidev-team/lola/internal/reviewmd"
	"github.com/sushidev-team/lola/internal/scm"
	"github.com/sushidev-team/lola/internal/session"
)

// inlineAnchorWindow is how far an anchor may move to reach a line the diff
// actually carries. The review instruction asks for "the smallest line that
// carries the defect", which is routinely a context line just outside a hunk;
// three lines is the "same statement, same block" tolerance. Wider starts
// attaching findings to unrelated code, and every snap is declared in the
// comment body anyway.
const inlineAnchorWindow = 3

// inlineFetchTimeout bounds the anchor fetch, which is TWO gh reads (the head sha
// and the whole PR diff) under one deadline — the shared reactExecTimeout is the
// budget for ONE, and a large diff plus a slow `pr view` would trip it and
// downgrade a perfectly postable review to a flat comment. It is safe to be
// longer here because this runs on the review WORKER, never on the observe loop
// (see the "a review PASS never runs on the observe loop" invariant), and each gh
// exec still self-bounds inside internal/scm.
const inlineFetchTimeout = 60 * time.Second

// repoSlugRe accepts exactly "owner/name" — the shape both the thread-listing
// instruction and every gh call interpolate. A repo that does not match is
// treated as unknown (no instruction, no post), never patched into a query.
var repoSlugRe = regexp.MustCompile(`^([A-Za-z0-9._-]+)/([A-Za-z0-9._-]+)$`)

// postGithubInline posts the findings as an anchored review and reports whether
// the github sink is DONE for this call. false means "fall through to the plain
// comment" — nil seams, an unavailable head sha/diff, nothing anchorable, or a
// permanent rejection. true means the sink is finished: either the review posted
// (guard stamped) or a transient failure should be retried on the next cycle
// (guard deliberately left unstamped).
func (d *Daemon) postGithubInline(ctx context.Context, s session.Session, p reviewProvider, findings string) bool {
	post, target := d.reviewPostSeams()
	if post == nil || target == nil {
		return false
	}
	if !repoSlugRe.MatchString(s.Repo) {
		return false // fail-closed: an unreadable repo never reaches the API
	}

	tctx, cancel := context.WithTimeout(ctx, inlineFetchTimeout)
	sha, diff, err := target(tctx, s.Repo, s.PR.Number)
	cancel()
	if err != nil {
		d.logf("", "review: %s (%s) could not read PR #%d's diff for inline anchors (posting a plain comment): %v",
			s.ID, p.Kind, s.PR.Number, err)
		return false
	}

	anchors := diffanchor.Parse(diff)
	rv := reviewmd.RenderInline(reviewmd.Options{
		Title: labelsFor(p.Kind).notifyTitle,
		Repo:  s.Repo,
		Ref:   s.Branch, // empty ⇒ body locations render as plain code, never a wrong link
	}, findings, func(path string, line int) (int, bool) {
		return anchors.Nearest(path, line, inlineAnchorWindow)
	})
	if len(rv.Comments) == 0 {
		// Nothing landed in the diff (a review about files this PR does not
		// touch, a diff too large to fetch whole). The plain comment says exactly
		// the same thing with one less API call.
		d.logf("", "review: %s (%s) no finding anchors to PR #%d's diff (posting a plain comment)",
			s.ID, p.Kind, s.PR.Number)
		return false
	}

	comments := make([]scm.InlineComment, 0, len(rv.Comments))
	for _, c := range rv.Comments {
		comments = append(comments, scm.InlineComment{
			Path: c.Path,
			Line: c.Line,
			Body: neutralizeBotTriggers(c.Body),
		})
	}
	pctx, pcancel := context.WithTimeout(ctx, reactExecTimeout)
	defer pcancel()
	err = post(pctx, s.Repo, s.PR.Number, sha, neutralizeBotTriggers(rv.Body), comments)
	if err == nil {
		d.stampGithubSettled(s.ID, p.Kind, s.PR.Number)
		d.stampInlineReview(s.ID, p.Kind, s.PR.Number)
		d.logf("", "review: %s (%s) posted %d inline review thread(s) on PR #%d",
			s.ID, p.Kind, len(comments), s.PR.Number)
		return true
	}
	if isPermanentGhError(err) {
		// 422 (a line GitHub does not consider part of the diff after all, e.g. a
		// push landed between the diff fetch and the post) or 403 (no write
		// access): the findings must still reach the PR, so hand back to the plain
		// comment INSTEAD of stamping the guard.
		d.logf("", "review: %s (%s) inline review of PR #%d rejected (posting a plain comment): %v",
			s.ID, p.Kind, s.PR.Number, err)
		return false
	}
	d.logf("", "review: %s (%s) inline review of PR #%d failed (transient, will retry): %v",
		s.ID, p.Kind, s.PR.Number, err)
	return true
}

// stampInlineReview records that p.Kind's findings reached PR #pr as anchored
// threads, so the worker hand-off can tell the agent to work and resolve them
// (see inlineThreadNote). Written only on a successful inline post — a run that
// fell back to a plain comment leaves no entry, which is what keeps the
// instruction from describing threads that do not exist.
func (d *Daemon) stampInlineReview(id string, k provKind, pr int) {
	d.sessions.Update(id, func(cur *session.Session) bool {
		if cur.InlineReviewPRs == nil {
			cur.InlineReviewPRs = map[string]int{}
		}
		if cur.InlineReviewPRs[string(k)] == pr {
			return false
		}
		cur.InlineReviewPRs[string(k)] = pr
		return true
	})
	d.reviewSave()
}

// inlineThreadNote is the fixed instruction appended to a worker hand-off when
// this kind's findings are ALSO sitting on the PR as resolvable threads lola
// posted itself. It is lola's own text (never provider output), so it may
// safely carry directives: the point of the inline shape is that the agent
// works the threads and CLOSES them, and an agent that is not told the threads
// exist will fix the code and leave twelve open conversations behind.
//
// It returns "" whenever the threads cannot be described exactly — no PR, a
// different PR than the one the threads were posted for, a repo that is not
// "owner/name" — because a stale instruction is worse than none. The weaker
// prThreadNote covers the hand-offs where lola did NOT post the threads.
func inlineThreadNote(s session.Session, p reviewProvider) string {
	if !p.Inline || s.PR == nil || s.PR.Number <= 0 {
		return ""
	}
	if s.InlineReviewPRs[string(p.Kind)] != s.PR.Number {
		return ""
	}
	m := repoSlugRe.FindStringSubmatch(s.Repo)
	if m == nil {
		return ""
	}
	owner, name, pr := m[1], m[2], s.PR.Number
	var b strings.Builder
	fmt.Fprintf(&b, "\n\nThese findings are also open as inline review threads on PR #%d (%s). "+
		"Work them there too: fix the code, commit and push, then — for every finding you addressed — "+
		"reply to its thread naming the pushed commit and what changed, and resolve that thread; "+
		"if you disagree, reply with why and leave it open. Never resolve a thread you have not "+
		"addressed, and never resolve one before its fix is pushed.\n",
		pr, s.Repo)
	b.WriteString(threadWorkflow(owner, name, s.Repo, pr))
	return b.String()
}

// prThreadNote is the same close-the-loop instruction for feedback whose threads
// lola did NOT post: CodeRabbit's own inline comments (the watch hand-off), a
// human reviewer's (the changes_requested reaction), and the fallback plain
// comment a failed inline post degrades to. lola cannot prove such threads exist
// — nothing stamped InlineReviewPRs — so the wording is CONDITIONAL and the
// listing command is the source of truth: an empty list is simply nothing to
// close. That is the one honest way to instruct here; asserting threads that may
// not be there is exactly what inlineThreadNote's stamp exists to prevent.
//
// Like inlineThreadNote it is lola's own text, appended AFTER the untrusted
// feedback so nothing in that feedback can rewrite the instruction, and it
// returns "" unless the PR and repo can be named exactly.
func prThreadNote(s session.Session) string {
	if s.PR == nil || s.PR.Number <= 0 {
		return ""
	}
	m := repoSlugRe.FindStringSubmatch(s.Repo)
	if m == nil {
		return ""
	}
	owner, name, pr := m[1], m[2], s.PR.Number
	var b strings.Builder
	fmt.Fprintf(&b, "\n\nThis feedback may also be open as review threads on PR #%d (%s). "+
		"After you commit and push the fixes, close the loop there: list the open threads, "+
		"reply to each one you addressed naming the pushed commit and what changed, and resolve it; "+
		"where you disagree, reply with why and leave it open. Never resolve a thread you have not "+
		"addressed. An empty list means there is nothing to close — that is fine, do not invent work.\n",
		pr, s.Repo)
	b.WriteString(threadWorkflow(owner, name, s.Repo, pr))
	return b.String()
}

// threadWorkflow is the gh recipe both notes hand the agent: list the open
// threads, reply to one, resolve one. It is derived from the repo slug and PR
// number only (lola's own text — nothing attacker-authored reaches these
// commands), and it is what makes "resolve what you fixed" actionable rather
// than a wish: no agent guesses the resolveReviewThread mutation on its own.
func threadWorkflow(owner, name, repo string, pr int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "List the open threads:\ngh api graphql -f query='{repository(owner:\"%s\",name:\"%s\")"+
		"{pullRequest(number:%d){reviewThreads(first:100){nodes{id isResolved path line "+
		"comments(first:1){nodes{databaseId body}}}}}}}'\n", owner, name, pr)
	fmt.Fprintf(&b, "Reply to one (databaseId from above):\ngh api repos/%s/pulls/comments/<databaseId>/replies "+
		"-f body='fixed in <sha>: <what changed>'\n", repo)
	b.WriteString("Resolve one (id from above):\ngh api graphql -f query='mutation{resolveReviewThread" +
		"(input:{threadId:\"<id>\"}){thread{isResolved}}}'")
	return b.String()
}
