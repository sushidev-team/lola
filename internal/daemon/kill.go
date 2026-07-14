package daemon

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/session"
	"github.com/sushidev-team/lola/internal/worktree"
)

// killExecTimeout bounds the tmux/git execs a kill drives, so a wedged git
// (e.g. a hung filter on `git worktree remove`) can never hang the socket
// handler goroutine that called handleKill.
const killExecTimeout = 30 * time.Second

// handleKill tears down one native session on explicit request (cmd=kill):
// terminate its tmux agent, and — for a clean worktree — remove the worktree,
// drop the store entry, free the issue's in-flight claim, and (label mode)
// strip the issue's set_label so the reconcile pass does not silently re-queue
// the just-killed issue (see clearLabelDispatch). A dirty worktree is refused
// unless force is set: the agent is
// still terminated (runtime.Kill kills tmux first), the worktree is kept for
// inspection, the store entry is marked "dead", and the caller is told to
// rerun with --force.
//
// Concurrency: this does NOT hold d.mu across the tmux/git execs — those can
// take seconds and would stall every other socket request and tick snapshot.
// It is safe without a per-poll tick lock because the store's Update/Delete are
// atomic under the store mutex and the worktree removal is idempotent (a second
// concurrent kill of the same session finds the tmux session and worktree
// already gone and no-ops). Sessions carry no poll name, so there is no owning
// tick mutex to reuse; the store atomicity is the serialization point.
func (d *Daemon) handleKill(ctx context.Context, sessionID string, force bool) (protocol.KillData, error) {
	if sessionID == "" {
		return protocol.KillData{}, errors.New("session id required")
	}
	s, ok := d.sessions.Get(sessionID)
	if !ok {
		return protocol.KillData{}, fmt.Errorf("unknown session %s", sessionID)
	}

	d.mu.Lock()
	nat := d.native
	p := d.cfg.ProjectByName(s.Project)
	home := d.home
	d.mu.Unlock()
	if nat == nil {
		return protocol.KillData{}, errors.New("native runtime unavailable")
	}

	ctx, cancel := context.WithTimeout(ctx, killExecTimeout)
	defer cancel()

	// Project gone from config: there is no safe worktree to target (the
	// project's path/repo are unknown), so terminate the agent only, then drop
	// the store entry and free the slot. removeWorktree=false keeps runtime.Kill
	// from touching git at all.
	if p == nil {
		if err := nat.Kill(ctx, s, false, force); err != nil {
			return protocol.KillData{}, err
		}
		d.clearLabelDispatch(ctx, s)
		d.dropSession(s)
		msg := fmt.Sprintf("session %s terminated; project %q is no longer in config, so its worktree (if any) was left untouched", s.ID, s.Project)
		d.logf("", "kill: %s", msg)
		return protocol.KillData{Removed: false, Message: msg}, nil
	}

	dir := filepath.Join(home, "worktrees", p.Name, s.ID)
	err := nat.Kill(ctx, s, true, force)
	if errors.Is(err, worktree.ErrDirty) {
		// The agent is already terminated (tmux is killed before the worktree
		// step), but the worktree has uncommitted changes: keep it and the store
		// entry, flag the session dead so the TUI shows it, and tell the caller
		// how to override.
		d.sessions.Update(s.ID, func(sess *session.Session) bool {
			sess.Status = "dead"
			return true
		})
		if saveErr := d.sessions.Save(); saveErr != nil {
			d.logf("", "kill: persist sessions: %v", saveErr)
		}
		msg := fmt.Sprintf("session %s terminated; worktree kept (uncommitted changes) at %s — rerun with --force to remove it", s.ID, dir)
		d.logf("", "kill: %s", msg)
		return protocol.KillData{Removed: false, Worktree: dir, Message: msg}, errors.New(msg)
	}
	if err != nil {
		return protocol.KillData{}, err
	}

	d.clearLabelDispatch(ctx, s)
	d.dropSession(s)
	msg := fmt.Sprintf("session %s killed; worktree removed at %s", s.ID, dir)
	d.logf("", "kill: %s", msg)
	return protocol.KillData{Removed: true, Worktree: dir, Message: msg}, nil
}

// dropSession removes a killed session from the store and frees its issue's
// in-flight claim (keyed by issue UUID) so a still-matching issue can
// re-dispatch, then persists the store.
func (d *Daemon) dropSession(s session.Session) {
	d.sessions.Delete(s.ID)
	if s.IssueUUID != "" {
		d.inflight.Remove(s.IssueUUID)
	}
	if err := d.sessions.Save(); err != nil {
		d.logf("", "kill: persist sessions: %v", err)
	}
}

// clearLabelDispatch makes an explicit kill durable in label mode. A cleanly
// killed pre-PR session leaves its issue carrying the poll's on_sent_set_label
// with no live session and no PR yet — exactly the orphan shape the reconcile
// pass reverts and re-queues, so minutes later the just-killed issue would be
// silently respawned into a fresh worktree. (Seen-mode and post-PR kills
// already stay gone; this brings label mode in line.) So on a clean kill we
// strip the set_label off the issue — reconcile no longer matches it — and drop
// its seen guard so a later manual re-label re-dispatches promptly.
//
// Everything here is best-effort: the kill's core teardown (agent terminated,
// worktree removed, store entry dropped) has already succeeded and must never
// be blocked on Linear availability. When Linear is unreachable the issue keeps
// its set_label and reconcile may re-queue it — logged, not fatal. To re-run a
// killed issue on purpose, re-add a trigger label.
func (d *Daemon) clearLabelDispatch(ctx context.Context, s session.Session) {
	if s.IssueUUID == "" {
		return
	}
	// Label-mode polls for this session's project whose set_label the kill
	// should clear. A session records no poll name, so project scope is the
	// available key; multiple such polls are handled together.
	type target struct{ poll, label string }
	d.mu.Lock()
	var targets []target
	for _, p := range d.cfg.Polls {
		if p.DedupMode == "label" && p.OnSentSetLabel != "" && p.Project == s.Project {
			targets = append(targets, target{p.Name, p.OnSentSetLabel})
		}
	}
	d.mu.Unlock()
	if len(targets) == 0 {
		return
	}

	// Drop the seen guard first, under each poll's tick mutex (matching
	// reconcile's seen read-modify-write discipline), so a later re-label is not
	// suppressed by a stale entry within SeenTTL.
	for _, tg := range targets {
		mu := d.tickMutex(tg.poll)
		mu.Lock()
		if seen, err := d.seen.load(tg.poll); err == nil {
			if _, ok := seen[s.IssueUUID]; ok {
				delete(seen, s.IssueUUID)
				if err := d.seen.save(tg.poll, seen); err != nil {
					d.logf(tg.poll, "kill: drop seen for %s: %v", s.Issue, err)
				}
			}
		}
		mu.Unlock()
	}

	// Strip the set_label(s) off the issue so reconcile stops matching it.
	api, err := d.ensureLinear()
	if err != nil {
		d.logf("", "kill: Linear unavailable, %s keeps its sent label (reconcile may re-queue it): %v", s.Issue, err)
		return
	}
	current, err := api.IssueLabelIDs(ctx, s.IssueUUID)
	if err != nil {
		d.logf("", "kill: read labels for %s failed (reconcile may re-queue it): %v", s.Issue, err)
		return
	}
	have := make(map[string]bool, len(current))
	for _, id := range current {
		have[id] = true
	}
	remove := make([]string, 0, len(targets))
	present := false
	for _, tg := range targets {
		remove = append(remove, tg.label)
		if have[tg.label] {
			present = true
		}
	}
	if !present {
		return // no set_label on the issue; nothing to write
	}
	if err := api.SetIssueLabels(ctx, s.IssueUUID, ApplyLabelDelta(current, remove, nil)); err != nil {
		d.logf("", "kill: strip sent label from %s failed (reconcile may re-queue it): %v", s.Issue, err)
		return
	}
	d.logf("", "kill: stripped sent label from %s so it stays out of the queue (re-add a trigger label to re-dispatch)", s.Issue)
}
