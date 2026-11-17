package daemon

import (
	"context"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/sushidev-team/lola/internal/attention"
	"github.com/sushidev-team/lola/internal/notify"
	"github.com/sushidev-team/lola/internal/session"
)

// lockAutomationPrompt holds off other daemon sends and checkpoint restores
// from proof through submit. In particular, never prove emptiness and then wait
// behind a restore: the user may have started typing by the time it finishes.
// A successful caller must release the lock and use typeToAgentLocked.
func (d *Daemon) lockAutomationPrompt(ctx context.Context, s session.Session) (func(), bool) {
	g := d.sendGate(s.ID)
	g.Lock()
	cur, ok := d.sessions.Get(s.ID)
	// A person just pressed Stop. Nothing automated may type until the hold ends, or the
	// agent they stopped would start again on lola's say.
	if !ok || time.Now().Before(cur.StopHoldUntil) || !handoffDeliverable(cur) || !d.automationPromptReady(ctx, cur) {
		g.Unlock()
		return nil, false
	}
	return g.Unlock, true
}

// automationPromptReady checks human ownership separately from agent activity.
// Hooks describe a turn, not the draft or who currently owns the terminal.
// Run this AFTER slow content fetches and BEFORE consuming any pending message
// or retry guard. Failure leaves those untouched for the next observer pass.
func (d *Daemon) automationPromptReady(ctx context.Context, s session.Session) bool {
	cctx, cancel := context.WithTimeout(ctx, reactExecTimeout)
	defer cancel()
	if d.promptAttached == nil || (d.paneTail == nil && (d.paneCursor == nil || d.paneVisible == nil)) {
		return false
	}
	target := paneTarget(s)
	attached, err := d.promptAttached(cctx, target)
	if err != nil {
		d.automationDeferred(ctx, s, "Cannot verify whether the terminal is in use.")
		return false
	}
	if attached {
		d.automationDeferred(ctx, s, "Close or detach this session's live terminals to let lola send queued messages.")
		return false
	}
	read, err := d.readComposer(cctx, s)
	if err != nil {
		d.automationDeferred(ctx, s, "Cannot verify that the composer is empty.")
		return false
	}
	if read.activity != attention.ActivityWaiting {
		return false
	}
	if !read.empty {
		d.automationDeferred(ctx, s, "Waiting for an idle agent with an empty composer; any draft is left untouched.")
		return false
	}
	// A client could attach while the capture ran. Recheck ownership as the
	// final read before claiming the prompt. tmux is a terminal transport, not
	// an atomic editor API; this cannot promise isolation from external clients
	// that attach after the check, but never deliberately types into an owner.
	attached, err = d.promptAttached(cctx, target)
	if err != nil {
		d.automationDeferred(ctx, s, "Cannot verify whether the terminal is in use.")
		return false
	}
	if attached {
		d.automationDeferred(ctx, s, "Close or detach this session's live terminals to let lola send queued messages.")
		return false
	}
	d.sessions.Update(s.ID, func(cur *session.Session) bool {
		if cur.AutomationDeferredReason == "" {
			return false
		}
		cur.AutomationDeferredReason = ""
		return true
	})
	return true
}

// Explain a blocked delivery once per reason, rather than notifying on every
// observer pass. The pending payload stays in its existing durable queue.
func (d *Daemon) automationDeferred(ctx context.Context, s session.Session, reason string) {
	changed := false
	d.sessions.Update(s.ID, func(cur *session.Session) bool {
		if cur.AutomationDeferredReason == reason {
			return false
		}
		cur.AutomationDeferredReason = reason
		changed = true
		return true
	})
	if !changed {
		return
	}
	d.logf("", "automation: %s deferred — %s", s.ID, reason)
	d.mu.Lock()
	n := d.notifier
	d.mu.Unlock()
	if n != nil {
		n.Notify(ctx, notify.Note{Title: "Agent message waiting", Body: issueLabel(s) + ": " + reason, Priority: notify.Action, URL: prURL(s)})
	}
}

// ansiEscapeRe matches the ANSI escape sequences (CSI and OSC) that CI logs and
// terminal captures routinely emit, so they can be stripped whole rather than
// left as visible garbage once the lone ESC byte is removed.
var ansiEscapeRe = regexp.MustCompile("\x1b\\[[0-9;?:<>=]*[ -/]*[@-~]|\x1b\\][^\x07\x1b]*(?:\x07|\x1b\\\\)")

// sanitizeAgentText makes text safe to hand to tmux send-keys for typing into a
// live agent's pane. The transport (tmux.Client.SendKeys) types the payload
// with `send-keys -l` and then submits with a SEPARATE Enter (\r); any \r
// already inside the payload is an INDISTINGUISHABLE submit, so an embedded
// carriage return — routine in `gh run view --log-failed` output (npm/pytest/
// cargo/webpack progress bars) and injectable via attacker-authored PR/review
// bodies — would submit a partial prompt and defeat the AtPrompt send-keys gate
// mid-transport. So:
//
//   - ANSI escape sequences are stripped whole.
//   - CR (\r) is dropped: it is the submit vector. A CRLF collapses to a clean
//     LF; a bare CR (progress bars) vanishes.
//   - Other C0 controls (and DEL / C1) are dropped so nothing else can steer
//     the pane's line editor.
//   - LF (\n) and TAB (\t) are PRESERVED: reaction templates are intentionally
//     multi-line (see config.DefaultCIFailedMessage) and the agent pane inserts
//     LF literally — only CR/Enter submits.
//   - A trailing backslash goes (trimContinuation), or the message sits unsent.
//   - A message that would begin with "!" gets one leading space. In Claude Code's
//     composer a leading "!" runs the rest on this machine, with no permission prompt and
//     no hook, whether it is typed or pasted, and a leading space was measured to stop
//     that (the record keeps " !cmd" and nothing runs). Whitespace and invisible
//     characters ahead of the "!" go, so what reaches the composer is that measured
//     shape. It is idempotent, because several send paths sanitize twice.
func sanitizeAgentText(s string) string {
	s = ansiEscapeRe.ReplaceAllString(s, "")
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r == '\n' || r == '\t':
			b.WriteRune(r)
		case r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f):
			// CR, other C0 controls, DEL, and C1 controls: drop.
		default:
			b.WriteRune(r)
		}
	}
	out := b.String()
	bare := strings.TrimLeftFunc(out, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.Is(unicode.Cf, r)
	})
	if strings.HasPrefix(bare, "!") {
		out = " " + bare
	}
	return trimContinuation(out)
}

// trimContinuation drops a trailing backslash and the space around it, however many there
// are. A composer reads one as "keep going on the next line", so the Enter adds a line and
// nothing is submitted. Text that does not end in one is left exactly as it is.
func trimContinuation(s string) string {
	if !strings.HasSuffix(strings.TrimRightFunc(s, unicode.IsSpace), `\`) {
		return s
	}
	return strings.TrimRightFunc(s, func(r rune) bool { return r == '\\' || unicode.IsSpace(r) })
}
