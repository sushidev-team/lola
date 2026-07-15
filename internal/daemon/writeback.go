package daemon

// writeback.go is the P4 Linear write-back engine (PLAN P4.21–23): the thing
// AO's build cannot do — Lola narrates the agent's lifecycle back onto the
// Linear issue as workflow-state transitions and short comments.
//
// Three invariants dominate this file:
//
//   - FIRE ONCE PER LIFECYCLE TRANSITION, never per 30s observer cycle. Each
//     transition (spawn / pr-open / merged / blocked) has a persisted one-shot
//     guard on the session (WB*Done). The guard is set OPTIMISTICALLY the moment
//     a transition reaches its comment step — even if the comment call fails — so
//     a retry can never double-comment. The state/label write that PRECEDES the
//     comment is idempotent and, on failure, the helper returns WITHOUT setting
//     the guard so a later cycle retries it (no comment was posted yet, so that
//     retry is safe). Linear-unavailable is the same case: bail before the guard.
//
//   - OPTIONAL. Every field is opt-in: an empty on_*_state_id / blocked_label_id
//     and a false comment_on_* means the transition does nothing and makes ZERO
//     Linear calls. A poll that configures none of them behaves exactly as P3.
//
//   - NEVER BLOCK THE LIFECYCLE. Every write is best-effort: a failure is logged,
//     the agent keeps running, cleanup still happens. isAuthErr drops the cached
//     client (invalidateLinear) so a rotated key recovers next cycle.
//
// Guard writes go through the session Store.Update (atomic RMW) so a concurrent
// observer/hook write is never clobbered; the Linear network calls happen
// OUTSIDE the store lock and the guard is stamped only after.

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/linear"
	"github.com/sushidev-team/lola/internal/session"
)

// wbGuard names the one-shot guard a write-back transition stamps.
type wbGuard int

const (
	wbSpawn wbGuard = iota
	wbPR
	wbMerged
	wbBlocked
)

// pollForSession resolves the poll whose P4 write-back config applies to a
// session: by the recorded PollName, else (for records adopted without one) the
// first poll targeting the session's project. Returns nil — write-back then a
// no-op — when neither resolves. The returned poll is a copy, safe to read after
// the config lock is dropped.
func (d *Daemon) pollForSession(s session.Session) *config.Poll {
	d.mu.Lock()
	defer d.mu.Unlock()
	if s.PollName != "" {
		if p := d.cfg.PollByName(s.PollName); p != nil {
			pc := *p
			return &pc
		}
	}
	for i := range d.cfg.Polls {
		if d.cfg.Polls[i].Project == s.Project && s.Project != "" {
			pc := d.cfg.Polls[i]
			return &pc
		}
	}
	return nil
}

// renderWriteback fills a lifecycle-comment template. Like renderReaction it is
// a plain simultaneous string replace (strings.NewReplacer), NOT text/template,
// so an agent-authored PR link or an escalation detail can never inject template
// directives. The body is a Linear comment (an API payload, not tmux send-keys),
// so it needs no control-byte sanitization. Recognized placeholders:
//
//	{{.Session}} — the lola session id      (spawn)
//	{{.PR}}      — the PR URL, else #number  (pr / merged)
//	{{.Detail}}  — the escalation reason     (blocked)
func renderWriteback(tmpl string, s session.Session, detail string) string {
	pr := ""
	if s.PR != nil {
		if pr = s.PR.URL; pr == "" {
			pr = fmt.Sprintf("#%d", s.PR.Number)
		}
	}
	return strings.NewReplacer(
		"{{.Session}}", s.ID,
		"{{.PR}}", pr,
		"{{.Detail}}", detail,
	).Replace(tmpl)
}

// wbLinErr logs a write-back Linear failure and, on an auth error, drops the
// cached client so the next ensureLinear re-resolves the key (rotation).
func (d *Daemon) wbLinErr(stage string, err error) {
	if isAuthErr(err) {
		d.invalidateLinear()
	}
	d.logf("", "write-back: %s: %v", stage, err)
}

// setWBGuard stamps a write-back one-shot guard on the session via an atomic
// Store.Update (so a concurrent observer/hook write is not clobbered) and
// persists the store. A no-op when the guard is already set.
func (d *Daemon) setWBGuard(id string, g wbGuard) {
	changed := false
	d.sessions.Update(id, func(cur *session.Session) bool {
		switch g {
		case wbSpawn:
			if cur.WBSpawnDone {
				return false
			}
			cur.WBSpawnDone = true
		case wbPR:
			if cur.WBPRDone {
				return false
			}
			cur.WBPRDone = true
		case wbMerged:
			if cur.WBMergedDone {
				return false
			}
			cur.WBMergedDone = true
		case wbBlocked:
			if cur.WBBlockedDone {
				return false
			}
			cur.WBBlockedDone = true
		}
		changed = true
		return true
	})
	if changed {
		if err := d.sessions.Save(); err != nil {
			d.logf("", "write-back: persist guard: %v", err)
		}
	}
}

// writeBackSpawn performs the spawn-time write-back for a freshly spawned
// session (PLAN P4.21), called from dispatch on confirmed spawn success with the
// tick's already-resolved client. It moves the issue to OnSpawnStateID and/or
// posts the spawn comment.
//
// In dedup_mode=state the OnSpawnStateID transition IS the dedup — it moves the
// issue out of state_ids so MatchingIssues stops returning it. So a failure
// there is special: fall back to a persisted seen entry, or the issue would be
// re-dispatched forever. The guard is set optimistically even on comment failure
// (a retry could double-comment; the dedup already blocks re-dispatch).
func (d *Daemon) writeBackSpawn(ctx context.Context, api linear.API, p config.Poll, is linear.Issue, sess session.Session) {
	if p.OnSpawnStateID == "" && !p.CommentOnSpawn {
		return // nothing configured for spawn
	}
	if p.OnSpawnStateID != "" {
		if err := api.SetIssueState(ctx, is.ID, p.OnSpawnStateID); err != nil {
			d.wbLinErr("set spawn state for "+is.Identifier, err)
			if p.DedupMode == "state" {
				// The state move was the ONLY dedup; without it the issue keeps
				// matching. Persist a seen entry so it is not re-dispatched
				// (state mode otherwise writes no seen — see dispatch).
				d.seenFallback(p.Name, is.ID)
			}
		}
	}
	if p.CommentOnSpawn {
		if err := api.CreateComment(ctx, is.ID, renderWriteback(config.DefaultSpawnComment, sess, "")); err != nil {
			d.wbLinErr("spawn comment for "+is.Identifier, err)
		}
	}
	d.setWBGuard(sess.ID, wbSpawn)
}

// seenFallback persists a seen entry for issueUUID under pollName, best-effort.
// It is the dedup-of-last-resort for dedup_mode=state when the spawn state
// transition fails: state mode writes no seen entry on the happy path, so
// without this a failed transition leaves the issue matching forever. A direct
// load/save is safe because a state-mode tick never otherwise writes seen after
// its pre-loop prune, so there is no in-memory map to clobber.
func (d *Daemon) seenFallback(pollName, issueUUID string) {
	seen, err := d.seen.load(pollName)
	if err != nil || seen == nil {
		seen = map[string]time.Time{}
	}
	seen[issueUUID] = time.Now()
	if err := d.seen.save(pollName, seen); err != nil {
		d.logf(pollName, "write-back: seen fallback for %s failed: %v", issueUUID, err)
	}
}

// writeBack runs the observer-driven write-back transitions for a session as its
// PR-derived status advances (PLAN P4.21): PR-open and merged. It resolves the
// poll and client lazily so a session whose poll configures nothing makes ZERO
// Linear calls. Called from observeNative BEFORE react, so the merged write-back
// (and its guard) lands before react's merged-cleanup drops the session — a
// failed cleanup then retries without re-commenting.
func (d *Daemon) writeBack(ctx context.Context, s session.Session) {
	if s.Source != "native" {
		return
	}
	prOpen := s.PR != nil && strings.EqualFold(s.PR.State, "OPEN")
	merged := s.Status == "merged"
	if !((prOpen && !s.WBPRDone) || (merged && !s.WBMergedDone)) {
		return // nothing to transition, or already done
	}
	p := d.pollForSession(s)
	if p == nil {
		return
	}
	needPR := prOpen && !s.WBPRDone && (p.OnPRStateID != "" || p.CommentOnPR)
	needMerged := merged && !s.WBMergedDone && (p.OnMergedStateID != "" || p.CommentOnMerged)
	if !needPR && !needMerged {
		return // this poll configures nothing for the pending transition
	}
	api, err := d.ensureLinear()
	if err != nil {
		// Guards stay unset; a later cycle retries. For merged this means the
		// comment is lost if cleanup drops the session first — best-effort, like
		// every other Linear side effect in the daemon.
		d.logf("", "write-back: linear unavailable for %s: %v", s.ID, err)
		return
	}
	if needPR {
		d.writeBackState(ctx, api, s, p.OnPRStateID, p.CommentOnPR, config.DefaultPRComment, "", "pr", wbPR)
	}
	if needMerged {
		d.writeBackState(ctx, api, s, p.OnMergedStateID, p.CommentOnMerged, config.DefaultMergedComment, "", "merged", wbMerged)
	}
}

// writeBackState is the shared PR-open/merged transition: an optional workflow
// state move followed by an optional comment, then the one-shot guard. A failed
// state move returns WITHOUT the guard (idempotent, retried next cycle, no
// comment posted yet); once the state move is done the guard is stamped even if
// the comment fails, so the comment never double-fires.
func (d *Daemon) writeBackState(ctx context.Context, api linear.API, s session.Session, stateID string, comment bool, tmpl, detail, label string, g wbGuard) {
	if stateID != "" {
		if err := api.SetIssueState(ctx, s.IssueUUID, stateID); err != nil {
			d.wbLinErr("set "+label+" state for "+issueLabel(s), err)
			return
		}
	}
	if comment {
		if err := api.CreateComment(ctx, s.IssueUUID, renderWriteback(tmpl, s, detail)); err != nil {
			d.wbLinErr(label+" comment for "+issueLabel(s), err)
		}
	}
	d.setWBGuard(s.ID, g)
}

// writeBackEscalation fires the blocked write-back (PLAN P4.22) for a session
// that just escalated (CI retries exhausted, s.Escalated). Called from
// observeNative AFTER react — react is what sets Escalated — so it picks up the
// transition in the same cycle. Resolves poll + client itself.
func (d *Daemon) writeBackEscalation(ctx context.Context, s session.Session) {
	if !s.Escalated || s.WBBlockedDone {
		return
	}
	p := d.pollForSession(s)
	if p == nil || (p.BlockedLabelID == "" && !p.CommentOnBlocked) {
		return
	}
	api, err := d.ensureLinear()
	if err != nil {
		d.logf("", "write-back: linear unavailable for escalation of %s: %v", s.ID, err)
		return
	}
	d.writeBackBlocked(ctx, api, *p, s, "CI is still failing after automatic retries.")
}

// writeBackBlocked adds the blocked label and/or posts the blocked comment for a
// session that is stuck (CI escalation) or orphaned (reconcile revert), PLAN
// P4.22. The label is added via a FRESH read + delta so a concurrent label edit
// is preserved. A failed label write returns without the guard (retried next
// cycle, no comment yet); once past it the guard is stamped even on comment
// failure. Callers pass their already-resolved client.
func (d *Daemon) writeBackBlocked(ctx context.Context, api linear.API, p config.Poll, s session.Session, reason string) {
	if s.WBBlockedDone {
		return
	}
	if p.BlockedLabelID == "" && !p.CommentOnBlocked {
		return
	}
	if p.BlockedLabelID != "" {
		current, err := api.IssueLabelIDs(ctx, s.IssueUUID)
		if err != nil {
			d.wbLinErr("read labels for blocked "+issueLabel(s), err)
			return
		}
		if err := api.SetIssueLabels(ctx, s.IssueUUID, ApplyLabelDelta(current, nil, []string{p.BlockedLabelID})); err != nil {
			d.wbLinErr("add blocked label for "+issueLabel(s), err)
			return
		}
	}
	if p.CommentOnBlocked {
		if err := api.CreateComment(ctx, s.IssueUUID, renderWriteback(config.DefaultBlockedComment, s, reason)); err != nil {
			d.wbLinErr("blocked comment for "+issueLabel(s), err)
		}
	}
	d.setWBGuard(s.ID, wbBlocked)
}
