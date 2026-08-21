package daemon

// fallback.go is the agent-fallback engine (SUSHI-585): when the pane
// classifier sees a session's agent hit its usage limit
// (InputQuotaLimited), lola hands the session to the next kind in the
// project's agent_fallback chain — automatically when
// [reactions].agent_fallback.auto is set, otherwise via one notification
// suggesting the manual switch. The same path serves the manual
// cmd=switchAgent.
//
// Design rails:
//
//   - The fallback REPLACES the agent pane on the same worktree (runtime.
//     SwitchAgent) — it never types into the quota-dead agent, so the
//     send-keys gate is not involved at all.
//   - One-shot per quota episode: FallbackNotified records the kind the
//     engine last acted on, stamped BEFORE acting so a failed switch does not
//     retry every 30s observer cycle. It re-arms when the session leaves the
//     quota-limited state.
//   - The chain never revisits a kind the session already ran (AgentsTried):
//     after claude → codex, a codex quota banner continues to opencode, never
//     back to claude.
//   - The target kind's binary must resolve BEFORE anything is torn down —
//     a missing codex binary must never cost the running claude pane.

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/sushidev-team/lola/internal/agent"
	"github.com/sushidev-team/lola/internal/notify"
	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/session"
	"github.com/sushidev-team/lola/internal/state"
)

// handoffPaneLines is how many trailing rows of the dying agent's pane the
// fallback captures into the handoff briefing.
const handoffPaneLines = 80

// nextFallback picks the first chain entry that is neither the current kind
// nor one the session already ran. Entries are validated kinds
// (config.Validate) and Parse is total, so a stray value degrades to claude
// rather than failing — and claude-as-current is skipped like any other.
func nextFallback(chain []string, current agent.Kind, tried []string) (agent.Kind, bool) {
	for _, entry := range chain {
		k := agent.Parse(entry)
		if k == current || slices.Contains(tried, k.String()) {
			continue
		}
		return k, true
	}
	return "", false
}

// maybeFallback is the observer-side trigger, called with the post-update
// record each cycle. It also re-arms the one-shot guard when the session
// leaves the quota-limited state (the limit reset, or a switch happened).
func (d *Daemon) maybeFallback(ctx context.Context, s session.Session) {
	if s.IsAgentless() || s.TmuxName == "" {
		return
	}
	if s.AgentState != state.AgentWaitingInput || s.InputReason != state.InputQuotaLimited {
		if s.FallbackNotified != "" {
			d.sessions.Update(s.ID, func(cur *session.Session) bool {
				if cur.FallbackNotified == "" {
					return false
				}
				cur.FallbackNotified = ""
				return true
			})
			d.reactSave()
		}
		return
	}

	cur := agent.Parse(s.Agent)
	if s.FallbackNotified == cur.String() {
		return // already acted on this quota episode
	}

	d.mu.Lock()
	auto := d.cfg.Reactions.AgentFallback.Auto
	chain := d.cfg.FallbackChainFor(s.Project)
	notifier := d.notifier
	health := d.runtimeHealth
	d.mu.Unlock()
	if notifier == nil {
		notifier = notify.New(notify.NotifyConfig{})
	}

	next, ok := nextFallback(chain, cur, s.AgentsTried)
	if ok && health != nil {
		// The target binary must resolve before anything is promised or torn
		// down — a missing codex must never cost the running claude pane.
		if err := health(next.Binary()); err != nil {
			d.logf("", "fallback: %s next fallback %s is unavailable: %v", s.ID, next, err)
			ok = false
		}
	}

	// Stamp the one-shot guard FIRST: whatever happens next (notify, a failed
	// switch, a successful one), this quota episode is acted on exactly once.
	d.sessions.Update(s.ID, func(c *session.Session) bool {
		if c.FallbackNotified == cur.String() {
			return false
		}
		c.FallbackNotified = cur.String()
		return true
	})
	d.reactSave()

	if !ok {
		notifier.Notify(ctx, notify.Note{
			Title:    "Agent hit its usage limit",
			Body:     fmt.Sprintf("%s: %s hit its usage limit and no fallback agent is available (set agent_fallback and install its binary)", issueLabel(s), cur),
			Priority: notify.Urgent,
			URL:      prURL(s),
		})
		d.logf("", "fallback: %s is quota-limited on %s and no fallback is available", s.ID, cur)
		return
	}

	if !auto {
		notifier.Notify(ctx, notify.Note{
			Title:    "Agent hit its usage limit",
			Body:     fmt.Sprintf("%s: %s hit its usage limit — switch the session to %s to continue (or set reactions.agent_fallback.auto = true)", issueLabel(s), cur, next),
			Priority: notify.Urgent,
			URL:      prURL(s),
		})
		d.logf("", "fallback: %s is quota-limited on %s; suggesting %s (auto off)", s.ID, cur, next)
		return
	}

	if err := d.doSwitchAgent(ctx, s, next, "hit its usage limit"); err != nil {
		d.logf("", "fallback: %s auto-switch %s → %s failed: %v", s.ID, cur, next, err)
		notifier.Notify(ctx, notify.Note{
			Title:    "Agent fallback failed",
			Body:     fmt.Sprintf("%s: could not switch from %s to %s: %v", issueLabel(s), cur, next, err),
			Priority: notify.Urgent,
			URL:      prURL(s),
		})
		return
	}
	notifier.Notify(ctx, notify.Note{
		Title:    "Agent switched after usage limit",
		Body:     fmt.Sprintf("%s: %s hit its usage limit; the session was handed to %s on the same worktree", issueLabel(s), cur, next),
		Priority: notify.Action,
		URL:      prURL(s),
	})
	d.logf("", "fallback: %s auto-switched %s → %s", s.ID, cur, next)
}

// doSwitchAgent performs the switch itself: capture the dying agent's pane
// tail (the briefing's last-known-output section), hand the session to
// runtime.SwitchAgent, then write the agent-owning fields back to the store.
// The store update touches ONLY those fields so a concurrent hook write (a
// last stop event from the old pane) is never clobbered.
func (d *Daemon) doSwitchAgent(ctx context.Context, s session.Session, kind agent.Kind, reason string) error {
	d.mu.Lock()
	nat := d.native
	d.mu.Unlock()
	if nat == nil {
		return errors.New("native runtime unavailable")
	}

	// Best-effort capture BEFORE the pane goes down; a capture failure costs
	// one briefing section, never the switch.
	cctx, cancel := context.WithTimeout(ctx, reactExecTimeout)
	tail, err := d.paneTail(cctx, paneTarget(s), handoffPaneLines)
	cancel()
	if err != nil {
		d.logf("", "fallback: %s pane capture for the handoff failed (continuing without it): %v", s.ID, err)
		tail = ""
	}

	old := agent.Parse(s.Agent)
	// The switch mutates tmux + the worktree and must not be SIGKILLed halfway
	// by a shutdown — same shield discipline as a spawn.
	sctx, cancel2 := context.WithTimeout(context.WithoutCancel(ctx), nativeSpawnTimeout)
	switched, err := nat.SwitchAgent(sctx, s, kind, reason, tail)
	cancel2()
	if err != nil {
		return err
	}

	d.sessions.Update(s.ID, func(cur *session.Session) bool {
		cur.Agent = switched.Agent
		cur.AtPrompt = false
		cur.AtPromptVerified = false
		cur.SetAgentState(state.AgentStarting, "", time.Now())
		if !slices.Contains(cur.AgentsTried, old.String()) {
			cur.AgentsTried = append(cur.AgentsTried, old.String())
		}
		cur.FallbackNotified = ""
		return true
	})
	d.reactSave()
	return nil
}

// handleSwitchAgent (cmd=switchAgent) replaces a session's coding agent with a
// different kind on the SAME worktree and branch — the manual half of the
// fallback feature. It is refused for an unknown session, an agentless shell,
// an invalid kind, the kind already running, or a kind whose binary is not on
// PATH (checked before anything is torn down). Unlike the send-keys paths
// there is no idle gate: the old pane is replaced, not typed into, and the
// caller is a human watching a button.
func (d *Daemon) handleSwitchAgent(ctx context.Context, a protocol.SwitchAgentArgs) (protocol.SwitchAgentData, error) {
	id := strings.TrimSpace(a.Session)
	kindArg := strings.TrimSpace(a.Agent)
	if id == "" || kindArg == "" {
		return protocol.SwitchAgentData{}, errors.New("switchAgent: session and agent required")
	}
	if !agent.Valid(kindArg) {
		return protocol.SwitchAgentData{}, fmt.Errorf("switchAgent: unknown agent kind %q (must be claude|codex|opencode)", kindArg)
	}
	kind := agent.Parse(kindArg)

	s, ok := d.sessions.Get(id)
	if !ok {
		return protocol.SwitchAgentData{}, fmt.Errorf("unknown session %q", id)
	}
	if s.IsAgentless() {
		return protocol.SwitchAgentData{}, fmt.Errorf("%s has no coding agent (it is a shell session)", id)
	}
	if old := agent.Parse(s.Agent); old == kind {
		return protocol.SwitchAgentData{}, fmt.Errorf("%s already runs %s", id, kind)
	}

	d.mu.Lock()
	health := d.runtimeHealth
	d.mu.Unlock()
	if health != nil {
		if err := health(kind.Binary()); err != nil {
			return protocol.SwitchAgentData{}, fmt.Errorf("runtime not ready: %w", err)
		}
	}

	// Register with the drain group so graceful shutdown waits for the switch
	// instead of SIGKILLing it mid-tmux-replace (same discipline as
	// handleOpenTicket).
	if !d.beginConnWork() {
		return protocol.SwitchAgentData{}, errors.New("daemon is shutting down")
	}
	defer d.connWg.Done()

	if err := d.doSwitchAgent(ctx, s, kind, "switched by the operator"); err != nil {
		return protocol.SwitchAgentData{}, err
	}
	msg := fmt.Sprintf("%s: switched %s → %s — worktree and branch kept, briefing at .lola/handoff.md", id, agent.Parse(s.Agent), kind)
	d.logf("", "switchAgent: %s", msg)
	return protocol.SwitchAgentData{Agent: kind.String(), Message: msg}, nil
}
