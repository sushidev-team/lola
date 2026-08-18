package daemon

// resolveconflict.go is the MANUAL half of the merge_conflict reaction: a human
// looking at a conflicting session asks lola to hand the job to that session's
// coding agent right now, instead of waiting for the observer cycle that fires
// [reactions].merge_conflict (or getting nothing at all when that reaction is
// switched off).
//
// It types into a live agent, so it is held to exactly the same send-keys safety
// rules as every other path that does (see the send-keys invariant in
// CLAUDE.md): the wide idle gate PLUS live pane proof, an atomic consume of that
// gate, and lola-authored text only. What it does NOT share with the reaction
// engine is the one-shot LastReactedStatus guard as an ENTRY condition — a human
// clicking the button is the intent, so a second click re-sends. It still STAMPS
// the guard, so the automatic reaction cannot pile a second rebase prompt on top
// of the merge this just asked for.

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/session"
	"github.com/sushidev-team/lola/internal/state"
)

// handleResolveConflict serves cmd=resolveConflict: ask the session's agent to
// merge the project's default branch into the session branch and resolve the
// conflicts.
//
// Fail closed in three places, each for its own reason:
//
//   - The DELIVERY axis must actually say merge_conflict. The axis is the gh
//     fact (the rollup can be masked by a waiting agent), and a merge prompt
//     sent at a PR that does not conflict is a pointless turn on a worker.
//   - The project must still be in config, because its default_branch is the
//     whole content of the instruction — guessing "main" for a project that
//     merges into "develop" would send the agent at the wrong branch.
//   - The agent must be provably resting at its prompt right now
//     (handoffDeliverable + handoffPromptProof), or nothing is typed at all.
//     Unlike the reaction engine this does NOT defer: the caller is a human
//     watching a button, so an honest "not now" beats a send that happens
//     minutes later with no further sign of it.
func (d *Daemon) handleResolveConflict(ctx context.Context, sessionID string) (protocol.ResolveConflictData, error) {
	if sessionID == "" {
		return protocol.ResolveConflictData{}, errors.New("session id required")
	}
	s, ok := d.sessions.Get(sessionID)
	if !ok {
		return protocol.ResolveConflictData{}, fmt.Errorf("unknown session %s", sessionID)
	}
	if s.Delivery != state.DeliveryMergeConflict {
		return protocol.ResolveConflictData{}, fmt.Errorf("session %s has no merge conflict (delivery %q)", sessionID, s.Delivery)
	}

	d.mu.Lock()
	p := d.cfg.ProjectByName(s.Project)
	d.mu.Unlock()
	if p == nil {
		return protocol.ResolveConflictData{}, fmt.Errorf("project %q is no longer in config", s.Project)
	}
	base := p.DefaultBranch
	if base == "" {
		base = config.DefaultBranchName
	}

	if !handoffDeliverable(s) || !d.handoffPromptProof(ctx, s) {
		return protocol.ResolveConflictData{}, fmt.Errorf(
			"session %s is not resting at its prompt — lola will not type into a mid-turn agent; try again when it is idle", sessionID)
	}

	// Consume the gate atomically, exactly as reactSendAgent does: the copy above
	// is microseconds old, and a hook that resumed the agent meanwhile must cancel
	// the send rather than lose the race.
	var (
		sent     bool
		tmuxName string
	)
	d.sessions.Update(s.ID, func(cur *session.Session) bool {
		if !handoffDeliverable(*cur) {
			return false
		}
		cur.AtPrompt = false
		// Stamp the automatic reaction's one-shot guard: this IS the reaction for
		// this entry into merge_conflict, just triggered by hand.
		cur.LastReactedStatus = "merge_conflict"
		cur.PendingReaction = ""
		// The agent is about to work, and AgentWorking is also what closes the
		// wide idle gate against a second delivery (as handleAnswer and the review
		// hand-off do). The next lifecycle hook corrects this to the real state.
		cur.SetAgentState(state.AgentWorking, "", time.Now())
		tmuxName = cur.TmuxName
		sent = true
		return true
	})
	if !sent {
		return protocol.ResolveConflictData{}, fmt.Errorf("session %s left its prompt before the request could be sent", sessionID)
	}
	if tmuxName == "" {
		tmuxName = paneTarget(s)
	}

	msg := resolveConflictMessage(s, base)
	sctx, cancel := context.WithTimeout(ctx, reactExecTimeout)
	defer cancel()
	if err := d.sendKeys(sctx, tmuxName, msg); err != nil {
		return protocol.ResolveConflictData{}, fmt.Errorf("send conflict-resolution request to %s: %w", sessionID, err)
	}
	if err := d.sessions.Save(); err != nil {
		d.logf("", "resolveConflict: persist sessions: %v", err)
	}
	d.logf("", "resolveConflict: %s — asked the agent to merge %s and resolve the conflicts", s.ID, base)

	return protocol.ResolveConflictData{
		Branch:  base,
		Message: fmt.Sprintf("asked the agent to merge %s and resolve the conflicts", base),
	}, nil
}

// resolveConflictMessage is the instruction typed into the agent. It is lola's
// OWN text — the only outside values in it are the configured default branch,
// the session's Linear identifier and its PR number — and it deliberately does
// not reuse [reactions].merge_conflict.message: that template says "rebase onto
// the base branch", while the button a human pressed promises a MERGE of the
// project's default branch, and the two must not disagree about what was asked.
// Sanitized like every other send, so a branch name carrying control bytes can
// never steer the pane's line editor.
func resolveConflictMessage(s session.Session, base string) string {
	subject := "Your PR"
	if s.PR != nil {
		subject = fmt.Sprintf("Your PR (#%d)", s.PR.Number)
	}
	if s.Issue != "" {
		subject += " for " + s.Issue
	}
	return sanitizeAgentText(fmt.Sprintf(
		"%s has merge conflicts. Merge the project's default branch into this branch and resolve them:\n"+
			"1. git fetch origin %s\n"+
			"2. git merge origin/%s\n"+
			"3. Resolve every conflicting file, keeping the intent of BOTH sides — never drop the other branch's changes to make the merge go through.\n"+
			"4. Run the project's checks, then commit the merge and push.\n"+
			"If a conflict is genuinely ambiguous, stop and say so instead of guessing.",
		subject, base, base))
}
