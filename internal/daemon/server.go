package daemon

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"reflect"
	"time"

	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/notify"
	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/session"
)

// serve runs the accept loop until the listener is closed at shutdown.
func (d *Daemon) serve(ctx context.Context, ln net.Listener) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) || ctx.Err() != nil {
				return
			}
			d.logf("", "accept: %v", err)
			continue
		}
		go d.handleConn(ctx, conn)
	}
}

// handleConn reads newline-delimited protocol.Requests and answers one JSON
// line per request.
func (d *Daemon) handleConn(ctx context.Context, conn net.Conn) {
	defer conn.Close()
	sc := bufio.NewScanner(conn)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	enc := json.NewEncoder(conn)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var req protocol.Request
		if err := json.Unmarshal(line, &req); err != nil {
			if enc.Encode(protocol.Response{OK: false, Error: "bad request: " + err.Error()}) != nil {
				return
			}
			continue
		}
		resp := d.handle(ctx, req)
		if enc.Encode(resp) != nil {
			return
		}
		if req.Cmd == "stop" && resp.OK {
			d.cancel() // reply is on the wire; now begin graceful shutdown
			return
		}
	}
}

func (d *Daemon) handle(ctx context.Context, req protocol.Request) protocol.Response {
	switch req.Cmd {
	case "stop":
		d.logf("", "stop requested via socket")
		return protocol.Response{OK: true}
	case "status":
		return dataResponse(d.statusData(ctx))
	case "sessions":
		return dataResponse(d.sessionsData())
	case "reload":
		if err := d.handleReload(ctx); err != nil {
			return protocol.Response{OK: false, Error: err.Error()}
		}
		return protocol.Response{OK: true}
	case "enable", "disable":
		if err := d.handleEnable(ctx, req.Poll, req.Cmd == "enable"); err != nil {
			return protocol.Response{OK: false, Error: err.Error()}
		}
		return protocol.Response{OK: true}
	case "pollOnce":
		data, err := d.handlePollOnce(ctx, req.Poll, req.DryRun)
		if err != nil {
			return protocol.Response{OK: false, Error: err.Error()}
		}
		return dataResponse(data)
	case "hookEvent":
		return d.handleHookEvent(req)
	case "kill":
		data, err := d.handleKill(ctx, req.Session, req.Force)
		if err != nil {
			return protocol.Response{OK: false, Error: err.Error()}
		}
		return dataResponse(data)
	default:
		return protocol.Response{OK: false, Error: fmt.Sprintf("unknown cmd %q", req.Cmd)}
	}
}

// handleHookEvent maps a Claude Code lifecycle hook (`lola hook <event>`,
// relayed over the socket by internal/hook.Post) onto the session store:
//
//	stop         → status "idle"           turn done; the observer's PR check
//	                                       may promote it later (ci_*, …)
//	notification → status "needs_input"    permission prompt / waiting on a human
//	session_end  → status "session_ended"  the claude process terminated
//	tool_use     → LastSeen touch only     liveness heartbeat; no status change
//	                                       unless currently "idle", which a new
//	                                       tool call promotes back to "working"
//	user_prompt  → status "working"        turn START (a prompt was submitted —
//	                                       including a human attach nudge), when
//	                                       currently idle / needs_input
//
// AtPrompt (PLAN P3 send-keys safety gate) is maintained alongside status: only
// "stop" sets it (the agent is idle at its input prompt and safe to send-keys
// into); every other event — a new tool_use, a notification the human must
// answer, session end, or a user_prompt that STARTS a turn — CLEARS it, so the
// reaction engine never types into a busy or human-blocked pane. user_prompt is
// the turn-START clear: without it a human-initiated attach turn whose reply is
// text-only (no PostToolUse) would leave AtPrompt stale-true for the whole turn
// and the observer could send-keys into the mid-reply pane.
//
// The reply is ALWAYS OK — a hook runs on the agent's critical path and must
// never fail or block its turn. An unknown session ID is logged once per ID
// and acknowledged.
func (d *Daemon) handleHookEvent(req protocol.Request) protocol.Response {
	ok := protocol.Response{OK: true}
	// The transition is applied via Store.Update — ONE atomic
	// read-modify-write under the store lock. Hook events race both each
	// other (each hook arrives on its own connection goroutine) and the
	// observer's native pass; a Get→mutate→Upsert here could base the write
	// on a stale status and resurrect state another writer just replaced.
	var (
		unknownEvent  bool
		statusChanged bool
		newStatus     string
	)
	_, known := d.sessions.Update(req.Session, func(sess *session.Session) bool {
		prev := sess.Status
		switch req.Event {
		case "stop":
			sess.Status = "idle"
			sess.AtPrompt = true // idle at the prompt: safe to send-keys into
		case "notification":
			sess.Status = "needs_input"
			sess.AtPrompt = false // waiting on a human: never send-keys
		case "session_end":
			sess.Status = "session_ended"
			sess.AtPrompt = false
		case "tool_use":
			sess.AtPrompt = false // mid-turn (busy): never send-keys
			if sess.Status == "idle" {
				sess.Status = "working"
			}
		case "user_prompt":
			// Turn START: a prompt was submitted (an autonomous turn, or a human
			// attach nudge). Clear the send-keys gate so the reaction engine never
			// types into the now-busy pane, and promote an idle / needs_input
			// session to working — the agent is actively processing again.
			sess.AtPrompt = false
			if sess.Status == "idle" || sess.Status == "needs_input" {
				sess.Status = "working"
			}
		default:
			unknownEvent = true
			return false
		}
		statusChanged = sess.Status != prev
		newStatus = sess.Status
		return true // always stamps LastSeen — this IS the heartbeat
	})
	if req.Session == "" || !known {
		d.warnUnknownHookSession(req.Session, req.Event)
		return ok
	}
	if unknownEvent {
		d.logf("", "hookEvent: unknown event %q for session %s (acknowledged)", req.Event, req.Session)
		return ok
	}
	if statusChanged {
		d.logf("", "hookEvent: %s → %s (event %s%s)", req.Session, newStatus, req.Event,
			map[bool]string{true: ", detail " + req.Detail, false: ""}[req.Detail != ""])
		if err := d.sessions.Save(); err != nil {
			d.logf("", "hookEvent: persist sessions: %v", err)
		}
	}
	return ok
}

// warnUnknownHookSession logs an unknown hookEvent session once per ID: hooks
// fire after every turn and tool call, so a session that raced adoption or
// aged out of the store would otherwise flood the daemon log.
func (d *Daemon) warnUnknownHookSession(id, event string) {
	d.hookWarnMu.Lock()
	defer d.hookWarnMu.Unlock()
	if d.hookWarned[id] {
		return
	}
	d.hookWarned[id] = true
	d.logf("", "hookEvent: unknown session %q (event %s) — acknowledged, not tracked", id, event)
}

// sessionsData builds the reply for cmd=sessions from the observer's cached
// store snapshot. Nothing is exec'd on the request path — a stale-but-instant
// answer beats a request that hangs on ao/gh/tmux (observer cadence is 30s).
func (d *Daemon) sessionsData() protocol.SessionsData {
	snap := d.sessions.Snapshot()
	now := time.Now()
	// The ci_failed retry budget is the "N/M" denominator of the reacting
	// label; reactions config is global, read once under the config lock.
	d.mu.Lock()
	ciBudget := d.cfg.Reactions.CIFailed.Retries
	d.mu.Unlock()
	out := protocol.SessionsData{Sessions: make([]protocol.SessionInfo, 0, len(snap))}
	for _, s := range snap {
		si := protocol.SessionInfo{
			ID:        s.ID,
			Project:   s.Project,
			Issue:     s.Issue,
			Branch:    s.Branch,
			Status:    s.Status,
			TmuxName:  s.TmuxName,
			Source:    s.Source,
			Age:       formatAge(now.Sub(s.FirstSeen)),
			CIRetries: s.CIRetries,
			Escalated: s.Escalated,
			Reacting:  reactingLabel(s.Status, s.CIRetries, s.Escalated, ciBudget),
		}
		if s.Source == "native" {
			// Native sessions live in worktrees the daemon created at
			// <home>/worktrees/<project>/<id> (see newNativeRuntime); the
			// store record carries no path, so derive it for the TUI.
			si.Worktree = filepath.Join(d.home, "worktrees", s.Project, s.ID)
		}
		if s.PR != nil {
			si.PRURL = s.PR.URL
			si.PRNumber = s.PR.Number
			si.Checks = s.PR.ChecksState
			si.Review = s.PR.ReviewDecision
		}
		out.Sessions = append(out.Sessions, si)
	}
	return out
}

// reactingLabel summarizes the reaction engine's current posture for a session
// into a short human label for the TUI, derived purely from the persisted
// reaction state (status + CIRetries + Escalated) plus the configured ci_failed
// retry budget (the "N/M" denominator). "" means there is no reaction posture
// worth surfacing beyond the STATUS column; the label never re-states the raw
// status verbatim. Escalated wins over everything: it is set only while CI is
// still failing and the session has been handed to a human.
func reactingLabel(status string, ciRetries int, escalated bool, ciBudget int) string {
	switch {
	case escalated:
		return "escalated"
	case status == "ci_failed":
		return fmt.Sprintf("ci retry %d/%d", ciRetries, ciBudget)
	case status == "ci_pending" && ciRetries > 0:
		// A recovery prompt is in flight and CI is re-running.
		return fmt.Sprintf("ci retry %d/%d", ciRetries, ciBudget)
	case status == "changes_requested":
		return "addressing review"
	case status == "merge_conflict":
		return "rebasing"
	case status == "approved":
		return "ready to merge"
	case status == "review_pending":
		return "awaiting review"
	}
	return ""
}

// formatAge renders a duration TUI-compactly: "42s", "12m", "3h05m", "2d14h".
func formatAge(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
	default:
		days := int(d.Hours()) / 24
		return fmt.Sprintf("%dd%dh", days, int(d.Hours())%24)
	}
}

func dataResponse(v any) protocol.Response {
	b, err := json.Marshal(v)
	if err != nil {
		return protocol.Response{OK: false, Error: err.Error()}
	}
	return protocol.Response{OK: true, Data: b}
}

// handleReload re-reads config.DefaultPath and applies it live. An invalid
// config is rejected: the old one keeps running.
func (d *Daemon) handleReload(ctx context.Context) error {
	path, err := config.DefaultPath()
	if err != nil {
		return err
	}
	nc, err := config.Load(path)
	if err != nil {
		return err
	}
	if err := nc.Validate(); err != nil {
		return fmt.Errorf("config invalid, keeping previous: %w", err)
	}

	d.mu.Lock()
	old := d.cfg
	d.cfg = nc
	d.cfgErr = "" // a validated config lifts the startup hold on polls
	if old.Linear != nc.Linear {
		d.lin = nil // key source / endpoint changed: re-resolve lazily
		d.viewerID = ""
	}
	// Rebuild the reaction notifier from the new [notify] table (the resolved
	// [reactions] config lives on d.cfg and is read live by the engine). The
	// webhook URL is re-resolved from its env-var name and never logged.
	d.notifier = notify.New(nc.ResolveNotify())
	if d.realNative && !reflect.DeepEqual(old.Projects, nc.Projects) {
		// The native runtime holds a config reference for its project
		// registry: recreate it whenever the [[project]] set changes.
		d.native = newNativeRuntime(nc, d.home, d.lolaBin, d.linearKey)
	}
	d.mu.Unlock()

	d.syncWorkers(ctx)
	d.logf("", "config reloaded")
	return nil
}

// handleEnable flips a poll's Enabled flag, validates the whole config
// (which resolves the poll's [[project]] reference), saves, and applies live.
func (d *Daemon) handleEnable(ctx context.Context, name string, enable bool) error {
	if name == "" {
		return errors.New("poll name required")
	}

	d.mu.Lock()
	p := d.cfg.PollByName(name)
	if p == nil {
		d.mu.Unlock()
		return fmt.Errorf("unknown poll %q", name)
	}
	prev := p.Enabled
	p.Enabled = enable

	fail := func(err error) error {
		p.Enabled = prev
		d.mu.Unlock()
		return err
	}
	if err := d.cfg.Validate(); err != nil {
		return fail(err)
	}
	path, err := config.DefaultPath()
	if err == nil {
		err = d.cfg.Save(path)
	}
	if err != nil {
		return fail(err)
	}
	d.mu.Unlock()

	d.syncWorkers(ctx)
	verb := "disabled"
	if enable {
		verb = "enabled"
	}
	d.logf(name, "poll %s", verb)
	return nil
}

// handlePollOnce runs one tick now, mutually exclusive with the poll's
// ticker (a tick never runs twice concurrently for one poll). dryRun
// evaluates with zero side effects.
func (d *Daemon) handlePollOnce(ctx context.Context, name string, dryRun bool) (protocol.PollOnceData, error) {
	if name == "" {
		return protocol.PollOnceData{}, errors.New("poll name required")
	}
	d.mu.Lock()
	cfgErr := d.cfgErr
	d.mu.Unlock()
	if cfgErr != "" {
		return protocol.PollOnceData{}, errors.New(cfgErr + " (fix config.toml and run `lola reload`)")
	}
	// Register with the drain group so graceful shutdown waits for this
	// tick, and shield it from the shutdown cancellation like worker ticks.
	if !d.beginConnWork() {
		return protocol.PollOnceData{}, errors.New("daemon is shutting down")
	}
	defer d.connWg.Done()
	mu := d.tickMutex(name)
	mu.Lock()
	defer mu.Unlock()
	return d.tick(context.WithoutCancel(ctx), name, dryRun)
}
