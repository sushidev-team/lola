package daemon

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/sushidev-team/lola/internal/protocol"
)

// reviveExecTimeout bounds the tmux execs a revive drives (new-session +
// chrome), matching handleKill's discipline: a wedged tmux can never hang the
// socket handler goroutine that called handleRevive.
const reviveExecTimeout = 30 * time.Second

// handleRevive brings a dead session back on explicit request (cmd=revive):
// relaunch its coding agent on the worktree that was kept for inspection, so a
// pane that died — an instant launch failure, a crashed agent, a machine that
// slept — can be resumed instead of only re-dispatched from scratch. Claude
// resumes its prior conversation via --continue when a transcript exists
// (runtime.Revive decides); otherwise the agent restarts fresh on the same
// worktree.
//
// Guards: the session must be known and NOT already alive (reviving a live pane
// would spawn a second agent into the same worktree — the send-keys corruption
// class). On success the session is re-counted in the store (StatusWorking) and
// its issue's in-flight claim is re-established, mirroring Spawn's "mark
// in-flight first" so a still-matching issue is not dispatched a SECOND time
// alongside the revived session.
//
// Concurrency mirrors handleKill: no d.mu is held across the tmux execs; the
// store's atomic Upsert/Save and the in-flight set are the serialization point.
func (d *Daemon) handleRevive(ctx context.Context, sessionID string) (protocol.ReviveData, error) {
	if sessionID == "" {
		return protocol.ReviveData{}, errors.New("session id required")
	}
	s, ok := d.sessions.Get(sessionID)
	if !ok {
		return protocol.ReviveData{}, fmt.Errorf("unknown session %s", sessionID)
	}

	d.mu.Lock()
	nat := d.native
	d.mu.Unlock()
	if nat == nil {
		return protocol.ReviveData{}, errors.New("native runtime unavailable")
	}

	ctx, cancel := context.WithTimeout(ctx, reviveExecTimeout)
	defer cancel()

	if nat.Alive(ctx, s) {
		return protocol.ReviveData{}, fmt.Errorf("session %s is already running — nothing to revive", sessionID)
	}

	revived, err := nat.Revive(ctx, s)
	if err != nil {
		return protocol.ReviveData{}, err
	}

	// Re-establish the dispatch guard BEFORE the store is observed for pickup, so
	// the revived issue cannot be spawned a second time. The Upsert also makes the
	// session count toward liveCounted again.
	if revived.IssueUUID != "" {
		d.inflight.Add(revived.IssueUUID, revived.Issue)
	}
	d.sessions.Upsert(revived)
	if err := d.sessions.Save(); err != nil {
		d.logf("", "revive: persist sessions: %v", err)
	}

	msg := fmt.Sprintf("session %s revived (%s)", sessionID, revived.TmuxName)
	d.logf("", "revive: %s", msg)
	return protocol.ReviveData{Revived: true, TmuxName: revived.TmuxName, Message: msg}, nil
}
