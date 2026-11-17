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
	"github.com/sushidev-team/lola/internal/gitdiff"
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
	if s.GoalManaged() {
		return protocol.DiffData{}, errors.New("goal diffs are available from staged integration candidates, not mutable worker metadata")
	}
	if s.Worktree == "" {
		return protocol.DiffData{}, fmt.Errorf("session %s has no worktree", sessionID)
	}
	if d.worktreeDiff == nil {
		return protocol.DiffData{}, errors.New("diff unavailable")
	}
	if s.Stack {
		cctx, cancel := context.WithTimeout(ctx, diffTimeout)
		defer cancel()
		return d.stackDiff(cctx, s)
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
	return diffData(s.ID, res.Base, res.MergeBase, res), nil
}

// diffData maps a gitdiff result onto the wire. base and mergeBase are what
// the client shows the diff is against.
func diffData(id, base, mergeBase string, res gitdiff.Result) protocol.DiffData {
	out := protocol.DiffData{
		Session:   id,
		Base:      base,
		MergeBase: mergeBase,
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
	return out
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
	return d.deliverQueued(ctx, id, queuedText{
		name:  "feedback",
		field: func(s *session.Session) *string { return &s.PendingFeedback },
		wrap:  feedbackMessage,
		done:  "delivered the human's diff review to the agent",
		label: feedbackLabel,
	})
}

// queuedText names one HUMAN-written queue on the session record (diff
// feedback, a project broadcast) for deliverQueued: which field holds it, how
// the queued text is wrapped into the message the agent reads, and what to log.
type queuedText struct {
	name  string
	field func(*session.Session) *string
	wrap  func(string) string
	done  string
	// took and sent are optional, for a queue with a second delivery path. took
	// runs right after the text left the field and before the send, and sent reports
	// how the send went, after a failure has already put the text back.
	// took answers the store items the text belongs to, for the record of the send.
	took func(id, text string) []string
	sent func(id, text string, ok bool)
	// label is lola's sentence for the row this send leaves in the session's
	// timeline, from the text that left the field. nil means "": the queue holds a
	// human's own words, and their own row already shows them.
	label func(s session.Session, text string) string
	// lock and admit are for a send a person pressed this instant. nil means today's
	// behaviour: lockAutomationPrompt and handoffDeliverable.
	lock  func(ctx context.Context, s session.Session) (unlock func(), why string)
	admit func(s session.Session) bool
	// refused hears why a send with its own lock did not go. Empty means the session
	// was not even a candidate.
	refused func(why string)
}

// deliverQueued types one queue into the session's pane when the agent is
// provably resting there, and reports whether it did. Every "no" leaves the
// queue untouched for a later attempt; a failed send re-queues the text, since
// it exists nowhere else.
func (d *Daemon) deliverQueued(ctx context.Context, id string, q queuedText) bool {
	admit := q.admit
	if admit == nil {
		admit = handoffDeliverable
	}
	s, ok := d.sessions.Get(id)
	if !ok || *q.field(&s) == "" || s.TmuxName == "" || !admit(s) {
		if q.refused != nil {
			q.refused("")
		}
		return false
	}
	var unlock func()
	if q.lock != nil {
		u, why := q.lock(ctx, s)
		if u == nil {
			if q.refused != nil {
				q.refused(why)
			}
			return false
		}
		unlock = u
	} else {
		u, ready := d.lockAutomationPrompt(ctx, s)
		if !ready {
			return false
		}
		unlock = u
	}
	defer unlock()

	// Consume the gate and the queue atomically: a hook that resumed the agent
	// between the pane read and here must cancel the send, not lose the race.
	var (
		text     string
		tmuxName string
		note     typedNote
	)
	d.sessions.Update(id, func(cur *session.Session) bool {
		f := q.field(cur)
		if !admit(*cur) || *f == "" {
			return false
		}
		text, *f = *f, ""
		cur.AtPrompt = false
		// CLAIM the prompt in the same atomic step: handoffDeliverable admits
		// AgentIdle whatever AtPrompt says, so clearing AtPrompt alone left the
		// gate open, and a concurrent flush (the Stop hook's and the observer's
		// run side by side) could type a review hand-off into the same prompt
		// while this send was still in flight. AgentWorking closes every case of
		// the gate; the next lifecycle hook corrects it to the real state.
		cur.SetAgentState(state.AgentWorking, "", time.Now())
		tmuxName = cur.TmuxName
		note = noteFor(*cur, "")
		if q.label != nil {
			note.Label = q.label(*cur, text)
		}
		return true
	})
	if text == "" {
		return false
	}
	if q.took != nil {
		note.Items = q.took(id, text)
	}

	if err := d.typeToAgentLocked(ctx, note, tmuxName, q.wrap(text), reactExecTimeout); err != nil {
		// Unlike a review hand-off (which other sinks also received), this text
		// exists nowhere else: put it back in front of anything queued since.
		d.sessions.Update(id, func(cur *session.Session) bool {
			f := q.field(cur)
			if *f != "" {
				*f = text + "\n\n" + *f
			} else {
				*f = text
			}
			return true
		})
		_ = d.sessions.Save()
		d.logf("", "%s: %s send-keys failed (re-queued): %v", q.name, id, err)
		if q.sent != nil {
			q.sent(id, text, false)
		}
		return false
	}
	if q.sent != nil {
		q.sent(id, text, true)
	}
	if err := d.sessions.Save(); err != nil {
		d.logf("", "%s: persist sessions: %v", q.name, err)
	}
	d.logf("", "%s: %s %s", q.name, id, q.done)
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
	// A review note contains multiple screenshot comments and revision context.
	// Bound the complete batch below; never silently truncate a human review.
	if note := strings.TrimSpace(a.Note); note != "" {
		blocks = append(blocks, "- General note:\n"+indent(note, "  "))
	}
	if len(blocks) == 0 {
		return "", errors.New("feedback is empty — add a comment or a note")
	}
	block := strings.Join(blocks, "\n\n")
	if len(block) > feedbackMaxPending {
		return "", errors.New("feedback exceeds 64 KiB — shorten the draft or send it in parts")
	}
	return block, nil
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
