package daemon

// addapps.go serves cmd=addApps: a human adds further members of its stack to
// a RUNNING stack session, because the issue's labels missed one (the "app"
// label that would have routed the issue to the app repository was not set).
// Until now the agent could only report "needs <name>" and stop.
//
//   - A human act, from the desktop's session menu or `lola add-app`: tier
//     never for phones, named by no hook-bridge allowlist. An agent's own
//     "needs <name>" report is display; it never adds anything.
//   - The runtime prepares each new app exactly as a spawn does
//     (runtime.AddApps), all or nothing.
//   - A SANDBOXED session's agent is restarted (its container mounts are fixed
//     at launch), so it is refused while the agent is mid-turn: the human
//     stops it or waits, rather than lola cutting a turn short.
//   - The agent is told through the broadcast queue, so the note obeys the
//     idle gate and the pane proof like every other send.

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/sushidev-team/lola/internal/agent"
	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/session"
	"github.com/sushidev-team/lola/internal/state"
)

func (d *Daemon) handleAddApps(ctx context.Context, a protocol.AddAppsArgs) (protocol.AddAppsData, error) {
	id := strings.TrimSpace(a.Session)
	var names []string
	for _, n := range a.Apps {
		if n = strings.TrimSpace(n); n != "" && !slices.Contains(names, n) {
			names = append(names, n)
		}
	}
	if id == "" || len(names) == 0 {
		return protocol.AddAppsData{}, errors.New("addApps: session and at least one app required")
	}
	if _, ok := d.sessions.Get(id); !ok {
		return protocol.AddAppsData{}, fmt.Errorf("unknown session %q", id)
	}
	d.mu.Lock()
	nat := d.native
	health := d.runtimeHealth
	d.mu.Unlock()
	if nat == nil {
		return protocol.AddAppsData{}, errors.New("native runtime unavailable")
	}
	// Shutdown waits for the add instead of killing it between worktrees.
	if !d.beginConnWork() {
		return protocol.AddAppsData{}, errors.New("daemon is shutting down")
	}
	defer d.connWg.Done()

	data, err := d.addAppsLocked(ctx, nat, health, id, names)
	if err != nil {
		return protocol.AddAppsData{}, err
	}
	// After the gate is released: the send takes it itself. A busy or just
	// relaunched agent keeps the note queued for the next flush.
	d.deliverBroadcast(ctx, id)
	d.logf("", "addApps: %s", data.Message)
	return data, nil
}

// addAppsLocked does the add under the session's send gate, held exclusively
// like a checkpoint restore: nothing is typed into the agent while its folder
// changes (or, for a sandbox, while it is being restarted).
func (d *Daemon) addAppsLocked(ctx context.Context, nat NativeAPI, health func(string) error, id string, names []string) (protocol.AddAppsData, error) {
	gate := d.sendGate(id)
	gate.Lock()
	defer gate.Unlock()

	s, ok := d.sessions.Get(id)
	if !ok {
		return protocol.AddAppsData{}, fmt.Errorf("unknown session %q", id)
	}
	if !s.Stack {
		return protocol.AddAppsData{}, fmt.Errorf("%s is not a stack session — only a stack has further repositories to add", id)
	}
	if s.GoalManaged() {
		return protocol.AddAppsData{}, errors.New("a goal worker's repositories are set by its plan")
	}
	d.mu.Lock()
	members := d.cfg.StackMembers(s.Project)
	sandboxed := s.Sandbox != ""
	if p := d.cfg.ProjectByName(s.Project); p != nil && p.Sandboxed() {
		sandboxed = true
	}
	d.mu.Unlock()
	if sandboxed {
		switch s.AgentState {
		case state.AgentWorking, state.AgentStarting:
			return protocol.AddAppsData{}, fmt.Errorf("%s runs in a sandbox, which has to restart to mount another repository — stop the agent or wait for its turn to end, then add it", id)
		}
		if health != nil && !s.IsAgentless() {
			if err := health(d.launchBin(s.Project, agent.Parse(s.Agent).Binary())); err != nil {
				return protocol.AddAppsData{}, fmt.Errorf("runtime not ready: %w", err)
			}
		}
	}

	// Mutates worktrees and (sandbox) tmux: never cut halfway by a shutdown.
	sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), nativeSpawnTimeout)
	res, err := nat.AddApps(sctx, s, names)
	cancel()

	added := make([]string, 0, len(res.Apps))
	for _, app := range res.Apps {
		added = append(added, app.Name)
	}
	if len(res.Apps) > 0 || res.Relaunched {
		now := time.Now()
		note := addAppsNote(res.Apps)
		d.sessions.Update(id, func(cur *session.Session) bool {
			if len(res.Apps) > 0 {
				cur.Apps = mergeApps(cur.Apps, res.Apps, members)
				cur.Reaggregate(now)
				if broadcastTarget(*cur) || res.Relaunched {
					if cur.PendingBroadcast != "" {
						note = cur.PendingBroadcast + "\n\n" + note
					}
					if len(note) <= feedbackMaxPending {
						cur.PendingBroadcast = note
					}
				}
			}
			if res.Relaunched {
				cur.TmuxName = res.Session.TmuxName
				cur.Sandbox = res.Session.Sandbox
				cur.AtPrompt = false
				cur.AtPromptVerified = false
				cur.SetAgentState(state.AgentStarting, "", now)
			}
			return true
		})
		d.reactSave()
		d.syncGitPins()
	}
	if err != nil {
		return protocol.AddAppsData{}, err
	}
	msg := fmt.Sprintf("%s: added %s", id, strings.Join(added, ", "))
	if res.Relaunched {
		msg += " — the sandboxed agent was restarted and resumes its conversation"
	} else if sandboxed {
		msg += " — revive the session to start its agent with the new repository"
	}
	return protocol.AddAppsData{Apps: added, Relaunched: res.Relaunched, Message: msg}, nil
}

// mergeApps returns cur with added, in the stack's member order (apps of a
// member no longer configured keep their place at the end). A new slice:
// snapshots handed out earlier share cur's backing array.
func mergeApps(cur, added []session.App, members []config.Project) []session.App {
	next := slices.Clone(cur)
	for _, a := range added {
		if !slices.ContainsFunc(next, func(x session.App) bool { return x.Name == a.Name }) {
			next = append(next, a)
		}
	}
	rank := func(name string) int {
		if i := slices.IndexFunc(members, func(m config.Project) bool { return m.Name == name }); i >= 0 {
			return i
		}
		return len(members)
	}
	slices.SortStableFunc(next, func(a, b session.App) int { return rank(a.Name) - rank(b.Name) })
	return next
}

// addAppsNote is what the agent is told once it rests at its prompt. Every
// value in it is lola's own (a member name, its configured repo, the session's
// branch); it is sanitized with the rest of the queue before it is typed.
func addAppsNote(apps []session.App) string {
	var b strings.Builder
	b.WriteString("Further repositories were added to THIS session and are now checked out in your working directory, on the session's branch:\n")
	for _, a := range apps {
		repo := a.Repo
		if repo == "" {
			repo = "no GitHub repo configured"
		}
		fmt.Fprintf(&b, "- `%s/` (%s, branch `%s`, base `%s`)\n", a.Name, repo, a.Branch, appBase(a))
	}
	b.WriteString("The same rules apply as for the others: work inside the checkout, and for each repository you change, commit there, push the branch and open a pull request against its base, linked to the session's other pull requests. If you reported being blocked on one of these, continue with the task now.")
	return b.String()
}
