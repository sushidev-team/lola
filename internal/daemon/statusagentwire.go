package daemon

// statusagentwire.go wires the OPT-IN status interpreter (internal/statusagent,
// the [statusagent] table) into the daemon. One worker goroutine drains a small
// queue of session IDs; per ID it gathers bounded read-only context (pane tail,
// recent transitions, PR facts, optionally the agent's transcript tail), runs
// ONE interpreter call, parses/clamps the judgement, and writes back ONLY the
// display-overlay fields on the Session.
//
// Trust boundary (the whole point of this file's shape):
//
//   - The interpreter's output is UNTRUSTED and DISPLAY-ONLY. The write-back
//     touches InterpretedState/Summary/WaitingOn/InterpretedConfidence/
//     SummaryAt/InterpretedForAgentState plus the two cost fields — NEVER
//     Status, NEVER the axes, NEVER AtPrompt or any reaction/write-back guard.
//     Since Status is untouched, the store's OnTransition never fires from an
//     interpreter write: no feed noise, no re-entrancy.
//   - displayOverlay (consumed only by sessionsData) is the ONE reader of the
//     overlay. react/dispatch/writeback/answer/reconcile must never read these
//     fields.
//
// Cost controls: interpretations run only on notable rollup transitions and on
// ambiguous/stale states each observe cycle (capped per cycle), debounced per
// session (min_interval_seconds, stamped on every ATTEMPT including errors and
// skips), and an unchanged input bundle (hash over pane + deterministic state)
// skips the call entirely. The worker runs on the CANCELLABLE run context —
// an interpretation is read-only and safe to abort at shutdown, so unlike the
// observer it is deliberately NOT shutdown-shielded.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/session"
	"github.com/sushidev-team/lola/internal/state"
	"github.com/sushidev-team/lola/internal/statusagent"
)

const (
	// interpretExpiry is how long an interpretation may overlay the display
	// before it silently expires (a fresh one replaces it earlier when the
	// session stays ambiguous).
	interpretExpiry = 10 * time.Minute
	// interpretStaleWorking is how long a working axis may sit without
	// positive activity before the observer queues an interpretation ("it
	// says working — is it really?").
	interpretStaleWorking = 5 * time.Minute
	// interpretQueueCap bounds the worker queue; a full queue drops (and
	// logs) — a dropped interpretation is re-queued by a later cycle.
	interpretQueueCap = 8
	// interpretEventLines bounds how many recent transitions feed the context.
	interpretEventLines = 10
	// transcriptTailBytes bounds the transcript tail fed when
	// include_transcript is on.
	transcriptTailBytes = 4 * 1024
)

// buildStatusAgent constructs the interpreter client, or nil when
// [statusagent] is disabled OR its binary is unavailable — the daemon's
// "deterministic display only" signal, degrading gracefully like the brain.
func buildStatusAgent(sc config.StatusAgentConfig) *statusagent.Client {
	if !sc.Enabled {
		return nil
	}
	cl := &statusagent.Client{Bin: sc.Bin, Model: sc.Model}
	if sc.TimeoutSeconds > 0 {
		cl.Timeout = time.Duration(sc.TimeoutSeconds) * time.Second
	}
	if !cl.Available() {
		return nil // enabled but the binary is missing: caller logs once
	}
	return cl
}

// setStatusAgentLocked (re)builds the interpreter client and its exec seam.
// Caller holds d.mu. Called from Run and handleReload so enabling/disabling
// (or changing bin/model/timeout) takes effect live. The enabled flag is
// mirrored into an atomic so the store's OnTransition callback — which runs
// under the STORE lock and must not take d.mu (lock order is d.mu → store) —
// can gate cheaply.
func (d *Daemon) setStatusAgentLocked(sc config.StatusAgentConfig) {
	d.statusAgent = buildStatusAgent(sc)
	if d.statusAgent == nil {
		d.interpretSeam = nil
		d.interpretOn.Store(false)
		if sc.Enabled {
			d.statusAgentWarn.Do(func() {
				d.logf("", "statusagent: enabled but %q is not on PATH — interpretations disabled", firstNonEmpty(sc.Bin, "claude"))
			})
		}
		return
	}
	d.interpretSeam = d.statusAgent.Interpret
	d.interpretOn.Store(true)
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// maybeQueueInterpret enqueues a session for interpretation without ever
// blocking (a full queue drops; a later cycle re-queues). Safe under any lock:
// it only reads an atomic and performs a non-blocking channel send.
func (d *Daemon) maybeQueueInterpret(id string) bool {
	if id == "" || !d.interpretOn.Load() {
		return false
	}
	select {
	case d.interpretCh <- id:
		return true
	default:
		return false
	}
}

// interpretOnTransition is composed into the store's OnTransition callback:
// a notable rollup transition is exactly the moment a fresh judgement is
// worth paying for. It runs UNDER the store lock — nothing here but cheap
// field checks on the passed copy and the non-blocking enqueue. (Post-PR
// agent-axis moves that do not change the rollup are picked up by the
// observer's ambiguous-state sweep instead.)
func (d *Daemon) interpretOnTransition(_ string, s session.Session) {
	if s.Source != "native" || !s.HasAgent() {
		return
	}
	switch s.AgentState {
	case state.AgentDead, state.AgentExited, state.AgentShell, state.AgentOrphaned:
		return
	}
	d.maybeQueueInterpret(s.ID)
}

// shouldInterpretAmbiguous is the observer's per-cycle trigger: a session
// whose deterministic story is thin — blocked on a human, an unreadable pane,
// a long-quiet "working", or an expired overlay while still ambiguous.
func shouldInterpretAmbiguous(s session.Session, paneUnknown bool, now time.Time) bool {
	switch s.AgentState {
	case state.AgentDead, state.AgentExited, state.AgentShell, state.AgentOrphaned, "":
		return false
	}
	if s.AgentState == state.AgentWaitingInput {
		return true
	}
	if paneUnknown {
		return true
	}
	if s.AgentState == state.AgentWorking && !s.LastActivityAt.IsZero() &&
		now.Sub(s.LastActivityAt) > interpretStaleWorking {
		return true
	}
	return false
}

// interpretLoop is the single worker: it drains the queue one interpretation
// at a time (a natural global concurrency cap of 1) until the run context is
// cancelled.
func (d *Daemon) interpretLoop(ctx context.Context) {
	defer d.wg.Done()
	for {
		select {
		case <-ctx.Done():
			return
		case id := <-d.interpretCh:
			d.interpretOne(ctx, id)
		}
	}
}

// interpretOne runs the whole pipeline for one session: re-read → gate →
// debounce → gather → hash-skip → exec → parse/clamp → overlay write-back.
func (d *Daemon) interpretOne(ctx context.Context, id string) {
	d.mu.Lock()
	sc := d.cfg.StatusAgent
	seam := d.interpretSeam
	d.mu.Unlock()
	if seam == nil || !sc.Enabled {
		return
	}
	s, ok := d.sessions.Get(id)
	if !ok || s.Source != "native" || s.IsAgentless() {
		return
	}
	switch s.AgentState {
	case state.AgentDead, state.AgentExited, state.AgentShell, state.AgentOrphaned:
		return
	}
	if ivl := time.Duration(sc.MinIntervalSeconds) * time.Second; ivl > 0 &&
		!s.LastInterpretedAt.IsZero() && time.Since(s.LastInterpretedAt) < ivl {
		return // debounced; a later trigger retries
	}
	d.interpretMu.Lock()
	if d.interpretBusy[id] {
		d.interpretMu.Unlock()
		return
	}
	d.interpretBusy[id] = true
	d.interpretMu.Unlock()
	defer func() {
		d.interpretMu.Lock()
		delete(d.interpretBusy, id)
		d.interpretMu.Unlock()
	}()

	// Context gathering: every read bounded like the observer's execs.
	pane := ""
	if d.paneTail != nil {
		cctx, cancel := context.WithTimeout(ctx, observeExecTimeout)
		if txt, err := d.paneTail(cctx, paneTarget(s), observePaneLines); err == nil {
			pane = txt
		}
		cancel()
	}
	events := d.eventsFor(id, interpretEventLines, time.Now())
	transcript := ""
	if sc.IncludeTranscript && s.TranscriptPath != "" {
		transcript = tailFile(s.TranscriptPath, transcriptTailBytes)
	}

	// Input hash over the STABLE inputs only (pane text + deterministic state
	// + PR facts — no timestamps, no ago strings): an unchanged session skips
	// the call entirely, and the debounce window restarts for free.
	hash := interpretHash(s, pane)
	now := time.Now()
	if hash != "" && hash == s.LastInterpretedHash {
		d.sessions.Update(id, func(cur *session.Session) bool {
			cur.LastInterpretedAt = now
			// The input bundle is UNCHANGED, so the existing judgement still
			// holds: refresh its validity (SummaryAt) instead of letting the
			// overlay silently expire on a session that just sat still.
			if cur.Summary != "" && cur.InterpretedForAgentState == string(cur.AgentState) {
				cur.SummaryAt = now
			}
			return true
		})
		return
	}

	raw, err := seam(ctx, buildInterpretContext(s, pane, events, transcript))
	now = time.Now()
	if err != nil {
		d.logf("", "statusagent: interpret %s failed: %v", id, err)
		d.sessions.Update(id, func(cur *session.Session) bool {
			// Stamp the attempt AND the hash: an input that errors must not be
			// retried every cycle until it actually changes.
			cur.LastInterpretedAt = now
			cur.LastInterpretedHash = hash
			return true
		})
		return
	}
	interp, perr := statusagent.Parse(raw)
	if perr != nil {
		d.logf("", "statusagent: interpret %s unparsable: %v", id, perr)
	}
	d.sessions.Update(id, func(cur *session.Session) bool {
		// DISPLAY-ONLY write-back: overlay + cost fields, nothing else — never
		// Status, never the axes, never AtPrompt or any one-shot guard.
		cur.LastInterpretedAt = now
		cur.LastInterpretedHash = hash
		if perr != nil || interp.AgentState == "unknown" || interp.Confidence < sc.MinConfidence {
			cur.InterpretedState = ""
			cur.Summary = ""
			cur.WaitingOn = ""
			cur.InterpretedConfidence = 0
			cur.SummaryAt = time.Time{}
			cur.InterpretedForAgentState = ""
			return true
		}
		cur.InterpretedState = interp.AgentState
		cur.Summary = interp.Headline
		cur.WaitingOn = interp.WaitingOn
		cur.InterpretedConfidence = interp.Confidence
		cur.SummaryAt = now
		// Record the deterministic basis: any real agent-axis transition after
		// this instant supersedes the overlay (see displayOverlay).
		cur.InterpretedForAgentState = string(cur.AgentState)
		return true
	})
	if err := d.sessions.Save(); err != nil {
		d.logf("", "statusagent: persist sessions: %v", err)
	}
}

// interpretHash fingerprints the stable interpreter inputs.
func interpretHash(s session.Session, pane string) string {
	h := sha256.New()
	fmt.Fprintf(h, "%s\x00%s\x00%s\x00", s.AgentState, s.Delivery, s.Status)
	if s.PR != nil {
		fmt.Fprintf(h, "pr%d:%s:%s:%s:%s\x00", s.PR.Number, s.PR.State, s.PR.ChecksState, s.PR.ReviewDecision, s.PR.Mergeable)
	}
	h.Write([]byte(pane))
	return hex.EncodeToString(h.Sum(nil))
}

// buildInterpretContext assembles the material handed to the interpreter
// (capped again inside statusagent.Interpret). Everything here is DATA about
// the session; the instruction lives in statusagent.Instruction.
func buildInterpretContext(s session.Session, pane string, events []string, transcript string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Session %s (issue %s, agent %s) — deterministic status: %s (agent axis %s",
		s.ID, firstNonEmpty(s.Issue, "none"), firstNonEmpty(s.Agent, "claude"), s.Status, s.AgentState)
	if s.Delivery != "" && s.Delivery != state.DeliveryNone {
		fmt.Fprintf(&b, ", delivery %s", s.Delivery)
	}
	b.WriteString(")\n")
	if s.PR != nil {
		fmt.Fprintf(&b, "PR #%d state=%s checks=%s review=%s mergeable=%s\n",
			s.PR.Number, s.PR.State, s.PR.ChecksState, s.PR.ReviewDecision, s.PR.Mergeable)
	}
	if s.LastNotification != "" {
		fmt.Fprintf(&b, "Last notification: %s\n", s.LastNotification)
	}
	if s.CurrentTool != "" {
		fmt.Fprintf(&b, "Last tool: %s\n", s.CurrentTool)
	}
	if len(events) > 0 {
		b.WriteString("Recent transitions (newest first):\n")
		for _, e := range events {
			b.WriteString("  " + e + "\n")
		}
	}
	b.WriteString("Agent pane (tail):\n")
	b.WriteString(pane)
	if transcript != "" {
		b.WriteString("\nAgent transcript (tail):\n")
		b.WriteString(transcript)
	}
	return b.String()
}

// tailFile reads at most max bytes from the end of path, best-effort ("" on
// any error). The transcript is the agent's own file; reading it is as safe
// as reading the pane — and as untrusted.
func tailFile(path string, max int64) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return ""
	}
	off := fi.Size() - max
	if off < 0 {
		off = 0
	}
	buf := make([]byte, fi.Size()-off)
	if _, err := f.ReadAt(buf, off); err != nil {
		return ""
	}
	return string(buf)
}

// displayOverlay is the ONE consumer of the interpreter overlay, called only
// by sessionsData. The overlay ships iff it is confident enough, fresh
// (interpretExpiry), and not superseded — any real agent-axis transition since
// the judgement invalidates it instantly, because the basis it interpreted is
// gone. The returned interpretedState is "" when the interpreter merely AGREES
// with the deterministic axis (no override marker), while the headline and
// waiting-on text still ship — agreement plus a headline is the common,
// useful case.
func displayOverlay(s session.Session, minConfidence float64, now time.Time) (interpretedState, headline, waitingOn string, at time.Time) {
	if s.InterpretedState == "" || s.SummaryAt.IsZero() {
		return "", "", "", time.Time{}
	}
	if s.InterpretedConfidence < minConfidence {
		return "", "", "", time.Time{}
	}
	if now.Sub(s.SummaryAt) > interpretExpiry {
		return "", "", "", time.Time{}
	}
	if s.InterpretedForAgentState != string(s.AgentState) || s.AgentStateSince.After(s.SummaryAt) {
		return "", "", "", time.Time{}
	}
	interpretedState = s.InterpretedState
	if interpretedState == string(s.AgentState) {
		interpretedState = ""
	}
	return interpretedState, s.Summary, s.WaitingOn, s.SummaryAt
}
