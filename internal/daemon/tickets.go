package daemon

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/sushidev-team/lola/internal/agent"
	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/linear"
	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/session"
)

// ticketsExecTimeout bounds the Linear query a cmd=tickets fetch runs.
const ticketsExecTimeout = 30 * time.Second

// handleTickets serves cmd=tickets: browse a project's Linear team for issues to
// start on demand. It reuses the dispatch filter (MatchingIssues) with a
// synthetic filter — the project's team, no cycle/state/label narrowing —
// scoped to the API key's viewer ("mine", default) or the whole team. A project
// with no team is a distinct error, never an empty list.
func (d *Daemon) handleTickets(ctx context.Context, a protocol.TicketsArgs) (protocol.TicketsData, error) {
	project := strings.TrimSpace(a.Project)
	if project == "" {
		return protocol.TicketsData{}, errors.New("tickets: project required")
	}
	d.mu.Lock()
	p := d.cfg.ProjectByName(project)
	var team string
	if p != nil {
		team = p.TeamID
	}
	d.mu.Unlock()
	if p == nil {
		return protocol.TicketsData{}, fmt.Errorf("unknown project %q", project)
	}
	if team == "" {
		return protocol.TicketsData{}, fmt.Errorf("project %q has no Linear team — set team_id to browse issues", project)
	}

	api, err := d.ensureLinear()
	if err != nil {
		return protocol.TicketsData{}, err
	}

	filter := config.Project{Name: project, TeamID: team, CycleMode: "none", MatchMode: "any", AssigneeMode: "anyone"}
	viewerID := ""
	if strings.TrimSpace(a.Scope) != "team" { // "mine" (default)
		filter.AssigneeMode = "me"
		if viewerID, err = d.viewer(ctx, api); err != nil {
			return protocol.TicketsData{}, fmt.Errorf("resolve viewer: %w", err)
		}
	}

	base := d.shutdownCtx
	if base == nil {
		base = context.WithoutCancel(ctx)
	}
	cctx, cancel := context.WithTimeout(base, ticketsExecTimeout)
	issues, err := api.MatchingIssues(cctx, filter, "", viewerID)
	cancel()
	if err != nil {
		if isAuthErr(err) {
			d.invalidateLinear()
		}
		return protocol.TicketsData{}, fmt.Errorf("list issues for %s: %w", project, err)
	}

	// Which issues a live session already holds (dedup hint for the picker).
	held := map[string]bool{}
	for _, s := range d.sessions.Snapshot() {
		if s.IssueUUID != "" && isLiveStatus(s.Status) {
			held[s.IssueUUID] = true
		}
	}

	data := protocol.TicketsData{Team: team, Issues: make([]protocol.TicketRow, 0, len(issues))}
	for _, is := range issues {
		data.Issues = append(data.Issues, protocol.TicketRow{
			Identifier:  is.Identifier,
			UUID:        is.ID,
			Title:       is.Title,
			Branch:      is.BranchName,
			Priority:    is.Priority,
			AlreadyLive: d.inflight.Has(is.ID) || held[is.ID],
		})
	}
	return data, nil
}

// handleOpenTicket starts a Linear issue on demand (cmd=openTicket): a worktree +
// agent, a linear-kind session. It reproduces the dispatch dedup ordering EXACTLY
// so a manual open is indistinguishable from a poll dispatch and can never be
// double-spawned by a concurrent tick:
//
//  1. atomic in-flight CLAIM (fails if already claimed by a tick or a re-open);
//  2. for a POLLING project, persist seen BEFORE the spawn (crash guard), under
//     the project's tick mutex so it serializes with a real tick;
//  3. spawn, then Upsert immediately (so the next Budget counts it);
//  4. label flip + P4 write-back for a label-mode polling project, like a tick.
//
// A non-polling project skips seen/labels/write-back — the in-flight claim plus
// the live session are its only guard (and a re-pickup after teardown is fine).
func (d *Daemon) handleOpenTicket(ctx context.Context, a protocol.OpenTicketArgs) (protocol.OpenData, error) {
	project := strings.TrimSpace(a.Project)
	identifier := strings.TrimSpace(a.Identifier)
	uuid := strings.TrimSpace(a.UUID)
	if project == "" || identifier == "" || uuid == "" {
		return protocol.OpenData{}, errors.New("openTicket: project, identifier and uuid required")
	}

	d.mu.Lock()
	nat := d.native
	pp := d.cfg.ProjectByName(project)
	health := d.runtimeHealth
	home := d.home
	var proj config.Project
	polls := false
	if pp != nil {
		proj = *pp
		proj.MatchLabels = slices.Clone(pp.MatchLabels)
		proj.PostCreate = slices.Clone(pp.PostCreate)
		proj.Symlinks = slices.Clone(pp.Symlinks)
		polls = pp.Polls()
	}
	agentBin := agent.Parse(d.cfg.AgentForProject(project)).Binary()
	d.mu.Unlock()
	if pp == nil {
		return protocol.OpenData{}, fmt.Errorf("unknown project %q", project)
	}
	if nat == nil {
		return protocol.OpenData{}, errors.New("native runtime unavailable")
	}
	if err := health(agentBin); err != nil {
		return protocol.OpenData{}, fmt.Errorf("runtime not ready: %w", err)
	}

	name := proj.Name
	is := linear.Issue{ID: uuid, Identifier: identifier, Title: a.Title, BranchName: strings.TrimSpace(a.Branch)}

	// Shield the dedup-mutating spawn from shutdown cancellation exactly like a
	// tick (dispatch runs on context.WithoutCancel via safeTick/handlePollOnce):
	// register with the drain group so graceful shutdown WAITS for this open, and
	// run Spawn + label-flip + write-back on a non-cancellable context. Without
	// this, shutdown — or the TUI's ^r/^x daemon restart — landing mid-open would
	// SIGKILL the spawn and abort the post-spawn label flip, leaving the pre-spawn
	// seen entry to orphan the issue for up to SeenTTL with its trigger label never
	// flipped. Registering before any state mutation lets us refuse cleanly (having
	// touched nothing) when already shutting down.
	if !d.beginConnWork() {
		return protocol.OpenData{}, errors.New("daemon is shutting down")
	}
	defer d.connWg.Done()
	sctx := context.WithoutCancel(ctx)

	// For a polling project, serialize the whole dedup+spawn with that project's
	// ticks so the seen load-modify-save can't interleave with a tick.
	if polls {
		mu := d.tickMutex(name)
		mu.Lock()
		defer mu.Unlock()
	}

	// (1) Atomic in-flight claim.
	if !d.inflight.Claim(uuid, identifier) {
		return protocol.OpenData{}, fmt.Errorf("%s is already being worked on — check sessions", identifier)
	}

	// (2) Persist seen BEFORE the spawn for a polling project (crash guard),
	// matching dispatch — except dedup_mode=state, which dedups via the state
	// move, not a seen entry.
	if polls && proj.DedupMode != "state" {
		seen, _ := d.seen.load(name)
		if seen == nil {
			seen = map[string]time.Time{}
		}
		seen[uuid] = time.Now()
		if err := d.seen.save(name, seen); err != nil {
			d.inflight.Remove(uuid)
			return protocol.OpenData{}, fmt.Errorf("persist seen for %s: %w", identifier, err)
		}
	}

	// (3) Spawn (bounded + shutdown behavior identical to dispatch).
	cctx, cancel := context.WithTimeout(sctx, nativeSpawnTimeout)
	sess, err := nat.Spawn(cctx, proj, is)
	cancel()
	if err != nil {
		d.inflight.Remove(uuid)
		if polls && proj.DedupMode == "seen" {
			// seen is authoritative and never expires while matching — remove it so
			// the issue is not silently dropped forever.
			if seen, lerr := d.seen.load(name); lerr == nil {
				delete(seen, uuid)
				_ = d.seen.save(name, seen)
			}
		}
		return protocol.OpenData{}, err
	}
	if polls {
		sess.PollName = name
	}
	if proj.Repo != "" {
		sess.Repo = proj.Repo
	}
	d.sessions.Upsert(sess)
	d.recordSessionEvent("", sess)
	if serr := d.sessions.Save(); serr != nil {
		d.logf(name, "openTicket: persist sessions after spawn of %s: %v", identifier, serr)
	}

	// (4) Label flip + P4 write-back for a label-mode polling project.
	if polls && proj.DedupMode == "label" {
		if api, aerr := d.ensureLinear(); aerr == nil {
			if current, lerr := api.IssueLabelIDs(sctx, uuid); lerr == nil {
				removed := intersectLabels(proj.MatchLabels, current)
				newIDs := ApplyLabelDelta(current, proj.MatchLabels, []string{proj.OnSentSetLabel})
				if serr := api.SetIssueLabels(sctx, uuid, newIDs); serr == nil {
					d.sessions.Update(sess.ID, func(s *session.Session) bool { s.RemovedLabels = removed; return true })
					_ = d.sessions.Save()
				} else {
					d.logf(name, "openTicket: label flip for %s failed (seen guards dedup): %v", identifier, serr)
				}
			}
			d.writeBackSpawn(sctx, api, proj, is, sess)
		}
	} else if polls {
		if api, aerr := d.ensureLinear(); aerr == nil {
			d.writeBackSpawn(sctx, api, proj, is, sess)
		}
	}

	dir := filepath.Join(home, "worktrees", proj.Name, sess.ID)
	msg := fmt.Sprintf("started %s in %s at %s — attach in the TUI, or: tmux -L lola attach -t %s", identifier, proj.Name, dir, sess.ID)
	d.logf(name, "openTicket: %s", msg)
	return protocol.OpenData{SessionID: sess.ID, Worktree: dir, Branch: sess.Branch, Message: msg}, nil
}
