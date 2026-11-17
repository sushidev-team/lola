package daemon

// askmerge.go serves cmd=askMerge: a human looking at the project's branch
// graph asks a session's coding agent to merge another branch into the
// session's own branch.
//
// It exists for the checkout the host must not touch. A sandboxed session's
// worktree is off limits to host git once its agent runs (the mount list is the
// isolation), so the app cannot run that merge itself; and a merge under a live
// agent would rewrite files in the middle of its turn anyway. The agent is the
// one that can do it safely, at a moment it is resting.
//
// It types into a live agent, so it takes the same road as cmd=resolveConflict
// (typeAtRestingPrompt): the wide idle gate plus live pane proof through the
// human lock, an atomic consume, lola-authored text only. Like resolveConflict
// it REFUSES rather than defers. Unlike it there is no delivery-axis entry
// condition and no reaction guard to stamp: the click is the whole reason.

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/session"
)

// askMergeRefMax bounds the branch name that ends up in the instruction.
const askMergeRefMax = 200

// validMergeRef reports whether ref is a branch name lola is willing to put in
// an instruction: a conservative subset of what git accepts (letters, digits
// and . _ / - + @ only), so it can never read as an option, a revision
// expression or a second command, whatever the agent's shell makes of it.
func validMergeRef(ref string) bool {
	if ref == "" || len(ref) > askMergeRefMax {
		return false
	}
	if strings.HasPrefix(ref, "-") || strings.HasPrefix(ref, "/") || strings.HasSuffix(ref, "/") ||
		strings.HasSuffix(ref, ".") || strings.HasSuffix(ref, ".lock") ||
		strings.Contains(ref, "..") || strings.Contains(ref, "//") || strings.Contains(ref, "@{") {
		return false
	}
	for _, r := range ref {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '.', r == '_', r == '/', r == '-', r == '+', r == '@':
		default:
			return false
		}
	}
	return true
}

// handleAskMerge asks sessionID's agent to merge ref into its own branch. ref
// is a branch name as the host's repository shows it (a local branch, or a
// remote-tracking one such as origin/main): the session's checkout shares that
// repository's refs, so the name means the same commit there.
func (d *Daemon) handleAskMerge(ctx context.Context, sessionID, ref string) (protocol.AskMergeData, error) {
	if sessionID == "" {
		return protocol.AskMergeData{}, errors.New("session id required")
	}
	s, ok := d.sessions.Get(sessionID)
	if !ok {
		return protocol.AskMergeData{}, fmt.Errorf("unknown session %s", sessionID)
	}
	// A stack's branches live on its apps, one per checkout; "the session's
	// branch" names none of them.
	if s.Stack {
		return protocol.AskMergeData{}, fmt.Errorf("session %s is a stack; ask its agent in the conversation instead", sessionID)
	}
	if !validMergeRef(ref) {
		return protocol.AskMergeData{}, errors.New("that branch name cannot be sent to an agent")
	}
	if s.Branch != "" && ref == s.Branch {
		return protocol.AskMergeData{}, fmt.Errorf("%s is the session's own branch", ref)
	}

	refused := ""
	lock := func(ctx context.Context, cur session.Session) (func(), bool) {
		unlock, why := d.humanLock(ctx, cur)
		refused = why
		return unlock, unlock != nil
	}
	err := d.typeAtRestingPrompt(ctx, s, askMergeMessage(ref), nil, lock, noteFor(s, askMergeLabel(s, ref)))
	switch {
	case errors.Is(err, errNotResting):
		return protocol.AskMergeData{}, fmt.Errorf("session %s %s", sessionID, resolveRefusal(refused))
	case errors.Is(err, errLeftPrompt):
		return protocol.AskMergeData{}, fmt.Errorf("session %s left its prompt before the request could be sent", sessionID)
	case err != nil:
		return protocol.AskMergeData{}, fmt.Errorf("send merge request to %s: %w", sessionID, err)
	}
	d.logf("", "askMerge: %s — asked the agent to merge %s", s.ID, ref)

	return protocol.AskMergeData{
		Branch:  ref,
		Message: fmt.Sprintf("asked the agent to merge %s", ref),
	}, nil
}

// askMergeMessage is the instruction typed into the agent. It is lola's OWN
// text; the one outside value is the branch name, which validMergeRef already
// narrowed and sanitizeAgentText passes like every other send.
//
// It names no fetch: the human picked a branch as the host's repository shows
// it, and the session's checkout reads the same refs, so a fetch could only
// merge something other than what was clicked. It asks for uncommitted work to
// be committed first because a merge into a dirty tree either refuses or mixes
// the two.
func askMergeMessage(ref string) string {
	return sanitizeAgentText(fmt.Sprintf(
		"Merge the branch %s into this branch:\n"+
			"1. Commit your uncommitted work first. Never discard or stash it away to make the merge go through.\n"+
			"2. git merge %s\n"+
			"3. Resolve every conflicting file, keeping the intent of BOTH sides — never drop the other branch's changes to make the merge go through.\n"+
			"4. Run the project's checks, then commit the merge. Push it only if this branch is already pushed.\n"+
			"If a conflict is genuinely ambiguous, stop and say so instead of guessing.",
		ref, ref))
}
