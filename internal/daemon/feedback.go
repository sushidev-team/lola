package daemon

// feedback.go is the daemon half of the in-app diff viewer (SUSHI-618): a human
// reads what a session changed (cmd=diff) and sends line comments back to its
// coding agent (cmd=feedback) — the review loop every local orchestrator ships,
// without a round trip through GitHub.
//
// The send types into a live agent, so it obeys the send-keys invariant in
// CLAUDE.md to the letter, through the SAME gate the review hand-off uses:
//
//   - The text is lola's rendering of the human's comments, sanitized
//     immediately before the send (control bytes stripped) and never run as a
//     command.
//   - It is typed only when handoffDeliverable admits the session AND
//     paneWaitingNow sees a resting prompt in the live pane right now — a hook
//     verdict alone is never enough (see handoffPromptProof).
//   - It DEFERS, never refuses and never forces: every batch is first appended
//     to Session.PendingFeedback, and the stash is delivered by whichever comes
//     first — this request (when the agent is already resting), the Stop hook
//     (flushHandoffsOnStop) or the observer cycle (flushReviewHandoffs). That
//     differs from resolveConflict on purpose: a review a human spent ten
//     minutes writing must not be lost to "the agent was busy, try again".
//
// It is agent-agnostic — claude, codex and opencode all go through
// paneWaitingNow, whose classifier is agent-aware.

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/session"
	"github.com/sushidev-team/lola/internal/state"
)

// Bounds on one feedback batch. The whole message is typed into a pane, so it
// is kept to something an agent reads as one instruction, not a document.
const (
	feedbackMaxComments  = 50
	feedbackMaxBodyRunes = 4000
	feedbackMaxQuoteLine = 6
	feedbackMaxQuoteRune = 400
	feedbackMaxPathRunes = 300
	// feedbackMaxPending bounds the QUEUED text, so a human who keeps sending
	// to an agent that never comes back to its prompt cannot grow a session
	// record without limit.
	feedbackMaxPending = 64 << 10

	diffTimeout = 20 * time.Second
)

// handleDiff serves cmd=diff: the session's worktree against its merge-base
// with the project's default branch. Read-only; one bounded local git run.
func (d *Daemon) handleDiff(ctx context.Context, sessionID string) (protocol.DiffData, error) {
	if sessionID == "" {
		return protocol.DiffData{}, errors.New("session id required")
	}
	s, ok := d.sessions.Get(sessionID)
	if !ok {
		return protocol.DiffData{}, fmt.Errorf("unknown session %s", sessionID)
	}
	if s.Worktree == "" {
		return protocol.DiffData{}, fmt.Errorf("session %s has no worktree", sessionID)
	}
	if d.worktreeDiff == nil {
		return protocol.DiffData{}, errors.New("diff unavailable")
	}
	base := config.DefaultBranchName
	d.mu.Lock()
	if p := d.cfg.ProjectByName(s.Project); p != nil && p.DefaultBranch != "" {
		base = p.DefaultBranch
	}
	d.mu.Unlock()

	cctx, cancel := context.WithTimeout(ctx, diffTimeout)
	defer cancel()
	res, err := d.worktreeDiff(cctx, s.Worktree, base)
	if err != nil {
		return protocol.DiffData{}, fmt.Errorf("diff %s: %w", sessionID, err)
	}
	out := protocol.DiffData{
		Session:   s.ID,
		Base:      res.Base,
		MergeBase: res.MergeBase,
		Files:     make([]protocol.DiffFile, 0, len(res.Files)),
		Truncated: res.Truncated,
	}
	for _, f := range res.Files {
		out.Files = append(out.Files, protocol.DiffFile{
			Path:      f.Path,
			OldPath:   f.OldPath,
			Status:    f.Status,
			Additions: f.Additions,
			Deletions: f.Deletions,
			Binary:    f.Binary,
			Untracked: f.Untracked,
			TooLarge:  f.TooLarge,
			Patch:     f.Patch,
		})
	}
	return out, nil
}

// handleFeedback serves cmd=feedback: queue the human's batch on the session,
// then try to deliver it right away. The reply says whether it was typed now or
// is waiting for the agent to come back to its prompt.
func (d *Daemon) handleFeedback(ctx context.Context, a protocol.FeedbackArgs) (protocol.FeedbackData, error) {
	id := strings.TrimSpace(a.Session)
	if id == "" {
		return protocol.FeedbackData{}, errors.New("session id required")
	}
	s, ok := d.sessions.Get(id)
	if !ok {
		return protocol.FeedbackData{}, fmt.Errorf("unknown session %s", id)
	}
	if s.IsAgentless() {
		return protocol.FeedbackData{}, fmt.Errorf("%s has no coding agent (it is a shell session)", id)
	}
	block, err := renderFeedback(a)
	if err != nil {
		return protocol.FeedbackData{}, err
	}

	var full bool
	d.sessions.Update(id, func(cur *session.Session) bool {
		next := block
		if cur.PendingFeedback != "" {
			next = cur.PendingFeedback + "\n\n" + block
		}
		if len(next) > feedbackMaxPending {
			full = true
			return false
		}
		cur.PendingFeedback = next
		return true
	})
	if full {
		return protocol.FeedbackData{}, fmt.Errorf(
			"%s already has too much undelivered feedback queued — wait for the agent to pick it up", id)
	}
	if err := d.sessions.Save(); err != nil {
		d.logf("", "feedback: persist sessions: %v", err)
	}

	if d.deliverFeedback(ctx, id) {
		return protocol.FeedbackData{Delivered: true, Message: "sent to the agent"}, nil
	}
	d.logf("", "feedback: %s queued — the agent is not resting at its prompt", id)
	return protocol.FeedbackData{
		Queued:  true,
		Message: "queued — it will be sent when the agent is waiting at its prompt",
	}, nil
}

// deliverFeedback types the session's queued feedback into its pane when the
// agent is provably resting there, and reports whether it did. Every "no"
// leaves the queue untouched for a later attempt.
func (d *Daemon) deliverFeedback(ctx context.Context, id string) bool {
	s, ok := d.sessions.Get(id)
	if !ok || s.PendingFeedback == "" || s.TmuxName == "" || !handoffDeliverable(s) {
		return false
	}
	if !d.paneWaitingNow(ctx, s) {
		return false
	}

	// Consume the gate and the queue atomically: a hook that resumed the agent
	// between the pane read and here must cancel the send, not lose the race.
	var (
		text     string
		tmuxName string
	)
	d.sessions.Update(id, func(cur *session.Session) bool {
		if !handoffDeliverable(*cur) || cur.PendingFeedback == "" {
			return false
		}
		text, cur.PendingFeedback = cur.PendingFeedback, ""
		cur.AtPrompt = false
		// CLAIM the prompt in the same atomic step: handoffDeliverable admits
		// AgentIdle whatever AtPrompt says, so clearing AtPrompt alone left the
		// gate open, and a concurrent flush (the Stop hook's and the observer's
		// run side by side) could type a review hand-off into the same prompt
		// while this send was still in flight. AgentWorking closes every case of
		// the gate; the next lifecycle hook corrects it to the real state.
		cur.SetAgentState(state.AgentWorking, "", time.Now())
		tmuxName = cur.TmuxName
		return true
	})
	if text == "" {
		return false
	}

	sctx, cancel := context.WithTimeout(ctx, reactExecTimeout)
	defer cancel()
	if err := d.sendKeys(sctx, tmuxName, feedbackMessage(text)); err != nil {
		// Unlike a review hand-off (which other sinks also received), this text
		// exists nowhere else: put it back in front of anything queued since.
		d.sessions.Update(id, func(cur *session.Session) bool {
			if cur.PendingFeedback != "" {
				cur.PendingFeedback = text + "\n\n" + cur.PendingFeedback
			} else {
				cur.PendingFeedback = text
			}
			return true
		})
		_ = d.sessions.Save()
		d.logf("", "feedback: %s send-keys failed (re-queued): %v", id, err)
		return false
	}
	if err := d.sessions.Save(); err != nil {
		d.logf("", "feedback: persist sessions: %v", err)
	}
	d.logf("", "feedback: %s delivered the human's diff review to the agent", id)
	return true
}

// feedbackMessage wraps the queued comment blocks in lola's own instruction
// and sanitizes the result — the human's text goes to the pane only as data
// inside it.
func feedbackMessage(blocks string) string {
	return sanitizeAgentText(
		"A human reviewed your changes in lola's diff viewer and left the feedback below. " +
			"Address every point (a line reference is path:line in the current working tree " +
			"unless marked as a removed line), then commit and push. If a point is unclear or " +
			"you disagree, say so instead of guessing.\n\n" + blocks)
}

// renderFeedback turns one batch into the text queued for the agent: one block
// per line comment (path:line, the quoted code, the comment), then the free
// note. The output is not yet sanitized — feedbackMessage does that at send
// time, once, over the whole message.
func renderFeedback(a protocol.FeedbackArgs) (string, error) {
	if len(a.Comments) > feedbackMaxComments {
		return "", fmt.Errorf("too many comments (%d, max %d)", len(a.Comments), feedbackMaxComments)
	}
	var blocks []string
	for i, c := range a.Comments {
		b, err := renderFeedbackComment(c)
		if err != nil {
			return "", fmt.Errorf("comment %d: %w", i+1, err)
		}
		blocks = append(blocks, b)
	}
	if note := clipRunes(strings.TrimSpace(a.Note), feedbackMaxBodyRunes); note != "" {
		blocks = append(blocks, "- General note:\n"+indent(note, "  "))
	}
	if len(blocks) == 0 {
		return "", errors.New("feedback is empty — add a comment or a note")
	}
	return strings.Join(blocks, "\n\n"), nil
}

func renderFeedbackComment(c protocol.FeedbackComment) (string, error) {
	path := strings.TrimSpace(c.Path)
	if path == "" {
		return "", errors.New("path required")
	}
	// A path is interpolated into an instruction: one carrying a newline or a
	// control byte could start a line of its own in the agent's prompt.
	if strings.ContainsFunc(path, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
		return "", errors.New("path contains control characters")
	}
	if filepath.IsAbs(path) || strings.HasPrefix(filepath.Clean(path), "..") {
		return "", fmt.Errorf("path %q is not inside the worktree", path)
	}
	path = clipRunes(path, feedbackMaxPathRunes)
	if c.Line < 1 {
		return "", errors.New("line must be >= 1")
	}
	if c.EndLine != 0 && c.EndLine < c.Line {
		return "", errors.New("endLine is before line")
	}
	body := clipRunes(strings.TrimSpace(c.Body), feedbackMaxBodyRunes)
	if body == "" {
		return "", errors.New("comment text required")
	}

	loc := fmt.Sprintf("%s:%d", path, c.Line)
	if c.EndLine > c.Line {
		loc += fmt.Sprintf("-%d", c.EndLine)
	}
	switch c.Side {
	case "", "new":
	case "old":
		loc += " (removed line(s); numbers refer to the base version)"
	default:
		return "", fmt.Errorf("unknown side %q", c.Side)
	}

	var b strings.Builder
	b.WriteString("- ")
	b.WriteString(loc)
	b.WriteString("\n")
	if q := quoteLines(c.Quote); q != "" {
		b.WriteString(q)
	}
	b.WriteString(indent(body, "  "))
	return b.String(), nil
}

// quoteLines renders the commented code as "  > " lines, clipped: it is there
// to help the agent find the spot, not to carry the file.
func quoteLines(q string) string {
	q = strings.TrimRight(clipRunes(q, feedbackMaxQuoteRune), "\n")
	if strings.TrimSpace(q) == "" {
		return ""
	}
	lines := strings.Split(q, "\n")
	more := len(lines) > feedbackMaxQuoteLine
	if more {
		lines = lines[:feedbackMaxQuoteLine]
	}
	var b strings.Builder
	for _, l := range lines {
		b.WriteString("  > ")
		b.WriteString(strings.TrimRight(l, "\r"))
		b.WriteString("\n")
	}
	if more {
		b.WriteString("  > …\n")
	}
	return b.String()
}

func indent(s, pfx string) string {
	return pfx + strings.ReplaceAll(s, "\n", "\n"+pfx)
}

// clipRunes cuts s to at most n runes, marking the cut.
func clipRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
