package daemon

// checkpoints.go is the daemon half of turn checkpoints (SUSHI-624): every time
// a coding agent's turn ends (the Stop hook) the session's whole worktree is
// snapshotted by internal/checkpoint onto a ref beside — never on — its branch,
// and a human can then read what any one turn changed (cmd=checkpointDiff), put
// the worktree back to a checkpoint (cmd=restoreCheckpoint) or start a second
// agent from one (cmd=forkCheckpoint). The point is that a bad turn costs a
// click, not a git session.
//
// Rules that hold it together:
//
//   - Recording runs OFF the hook's critical path. A hook is bounded at 2s and
//     always succeeds; a snapshot hashes every changed file, so it runs async on
//     the conn drain group (graceful shutdown waits for it, a draining daemon
//     skips it — the next turn records the state anyway).
//   - Recording never touches the real index or any branch (see the checkpoint
//     package), so it is safe while the agent keeps going. RESTORING does write
//     both the index and the files, so it is refused while the agent is
//     mid-turn — reverting files under an agent that is editing them leaves
//     neither state.
//   - All checkpoint work on one session is serialized (ckptLock), so a record
//     fired by a Stop hook can never interleave with a restore.
//   - A restore records the CURRENT state first and says which checkpoint holds
//     it: restoring is itself one click to undo.
//   - The client names a checkpoint by its per-session sequence number only.
//     The daemon builds the ref from the session it already knows, so no
//     request can reach another session's refs or an arbitrary object.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/sushidev-team/lola/internal/agent"
	"github.com/sushidev-team/lola/internal/checkpoint"
	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/runtime"
	"github.com/sushidev-team/lola/internal/session"
	"github.com/sushidev-team/lola/internal/state"
)

// checkpointStore is the daemon's seam over internal/checkpoint.
type checkpointStore interface {
	Record(ctx context.Context, dir, session, label string) (checkpoint.Checkpoint, bool, error)
	List(ctx context.Context, dir, session string) ([]checkpoint.Checkpoint, error)
	Get(ctx context.Context, dir, session string, seq int) (cp, prev checkpoint.Checkpoint, err error)
	Restore(ctx context.Context, dir, session string, seq int) (checkpoint.Checkpoint, error)
}

var _ checkpointStore = checkpoint.Git{}

// checkpointTimeout bounds one checkpoint operation. Generous: a first snapshot
// of a large worktree hashes every untracked file.
const checkpointTimeout = 90 * time.Second

// Labels. "start" is the state before the first turn, so even turn 1 can be
// rolled back — the runtime records it before every launch (any agent kind);
// "turn N" is the state a turn ended in.
const (
	ckptLabelStart = runtime.BaselineLabel
	ckptLabelTurn  = "turn "
)

func (d *Daemon) ckptLock(id string) *sync.Mutex {
	d.ckptMu.Lock()
	defer d.ckptMu.Unlock()
	if d.ckptLocks == nil {
		d.ckptLocks = map[string]*sync.Mutex{}
	}
	mu, ok := d.ckptLocks[id]
	if !ok {
		mu = &sync.Mutex{}
		d.ckptLocks[id] = mu
	}
	return mu
}

// sendGate is session id's send gate (see Daemon.sendGates).
func (d *Daemon) sendGate(id string) *sync.RWMutex {
	d.ckptMu.Lock()
	defer d.ckptMu.Unlock()
	if d.sendGates == nil {
		d.sendGates = map[string]*sync.RWMutex{}
	}
	g, ok := d.sendGates[id]
	if !ok {
		g = &sync.RWMutex{}
		d.sendGates[id] = g
	}
	return g
}

// typeToAgent is the ONE way the daemon types into a session's agent pane: it
// waits out a restore of that session's worktree (sendGate), then sends with
// its own timeout — taken AFTER the wait, so a send queued behind a restore is
// delayed, never timed out and lost.
func (d *Daemon) typeToAgent(ctx context.Context, id, target, text string, timeout time.Duration) error {
	g := d.sendGate(id)
	g.RLock()
	defer g.RUnlock()
	sctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return d.sendKeys(sctx, target, text)
}

// recordCheckpointAsync records a checkpoint for a hook event without holding
// up the hook's reply. event is "stop" (a turn ended) or "user_prompt" (a turn
// starts — recorded only as the session's very first checkpoint: a FALLBACK
// baseline for a session launched before the runtime recorded one itself).
func (d *Daemon) recordCheckpointAsync(id, event string) {
	if id == "" || d.checkpoints == nil || !d.beginConnWork() {
		return
	}
	go func() {
		defer d.connWg.Done()
		defer func() {
			if r := recover(); r != nil {
				d.logf("", "checkpoint: record panicked for %s: %v", id, r)
			}
		}()
		d.recordCheckpoint(context.Background(), id, event)
	}()
}

// recordCheckpoint is the synchronous body of recordCheckpointAsync.
func (d *Daemon) recordCheckpoint(ctx context.Context, id, event string) {
	s, ok := d.sessions.Get(id)
	if !ok || s.Worktree == "" || s.IsAgentless() {
		return
	}
	if _, err := os.Stat(s.Worktree); err != nil {
		return
	}
	mu := d.ckptLock(id)
	mu.Lock()
	defer mu.Unlock()

	cctx, cancel := context.WithTimeout(ctx, checkpointTimeout)
	defer cancel()
	list, err := d.checkpoints.List(cctx, s.Worktree, id)
	if err != nil {
		d.logf("", "checkpoint: %s: %v", id, err)
		return
	}
	label := ckptLabelStart
	if event == "user_prompt" {
		if len(list) > 0 {
			return // only the baseline is taken at turn start
		}
	} else {
		turns := 0
		for _, c := range list {
			if strings.HasPrefix(c.Label, ckptLabelTurn) {
				turns++
			}
		}
		label = fmt.Sprintf("%s%d", ckptLabelTurn, turns+1)
	}
	cp, recorded, err := d.checkpoints.Record(cctx, s.Worktree, id, label)
	if err != nil {
		d.logf("", "checkpoint: %s: %v", id, err)
		return
	}
	if recorded {
		d.logf("", "checkpoint: %s #%d (%s)", id, cp.Seq, cp.Label)
	}
}

// checkpointSession resolves a request's session to one whose worktree is on
// disk — the precondition of every checkpoint command.
func (d *Daemon) checkpointSession(id string) (session.Session, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return session.Session{}, errors.New("session id required")
	}
	s, ok := d.sessions.Get(id)
	if !ok {
		return session.Session{}, fmt.Errorf("unknown session %s", id)
	}
	if s.Worktree == "" {
		return session.Session{}, fmt.Errorf("session %s has no worktree", id)
	}
	if _, err := os.Stat(s.Worktree); err != nil {
		return session.Session{}, fmt.Errorf("session %s: worktree is gone", id)
	}
	if d.checkpoints == nil {
		return session.Session{}, errors.New("checkpoints unavailable")
	}
	return s, nil
}

// midTurn reports whether s's agent is (or may be) editing its worktree right
// now — the state a restore must not run in.
func midTurn(s session.Session) bool {
	return s.AgentState == state.AgentWorking || s.AgentState == state.AgentStarting
}

// liveAgent reports whether s has an agent process that could still act on
// its worktree: anything but gone (dead / exited), a shell, or orphaned.
func liveAgent(s session.Session) bool {
	if s.IsAgentless() {
		return false
	}
	switch state.DisplayFor(s.AgentState) {
	case state.DisplayGone, state.DisplayShell, state.DisplayOrphaned:
		return false
	}
	return true
}

// handleCheckpoints serves cmd=checkpoints. Read-only.
func (d *Daemon) handleCheckpoints(ctx context.Context, id string) (protocol.CheckpointsData, error) {
	s, err := d.checkpointSession(id)
	if err != nil {
		return protocol.CheckpointsData{}, err
	}
	cctx, cancel := context.WithTimeout(ctx, diffTimeout)
	defer cancel()
	list, err := d.checkpoints.List(cctx, s.Worktree, s.ID)
	if err != nil {
		return protocol.CheckpointsData{}, err
	}
	out := protocol.CheckpointsData{
		Session:     s.ID,
		Checkpoints: make([]protocol.CheckpointInfo, 0, len(list)),
		Restorable:  !midTurn(s),
	}
	for _, c := range list {
		out.Checkpoints = append(out.Checkpoints, protocol.CheckpointInfo{
			Seq: c.Seq, SHA: c.SHA, Head: c.Head, Label: c.Label, Created: c.Created,
		})
	}
	return out, nil
}

// handleCheckpointDiff serves cmd=checkpointDiff: what one turn changed — the
// previous checkpoint against this one, or for the oldest kept, the HEAD it was
// taken on against it. Read-only.
func (d *Daemon) handleCheckpointDiff(ctx context.Context, a protocol.CheckpointArgs) (protocol.DiffData, error) {
	s, err := d.checkpointSession(a.Session)
	if err != nil {
		return protocol.DiffData{}, err
	}
	if d.commitDiff == nil {
		return protocol.DiffData{}, errors.New("diff unavailable")
	}
	cctx, cancel := context.WithTimeout(ctx, diffTimeout)
	defer cancel()
	cp, prev, err := d.checkpoints.Get(cctx, s.Worktree, s.ID, a.Seq)
	if err != nil {
		return protocol.DiffData{}, err
	}
	from, base := cp.Head, "its HEAD"
	if prev.SHA != "" {
		from, base = prev.SHA, fmt.Sprintf("checkpoint #%d", prev.Seq)
	}
	res, err := d.commitDiff(cctx, s.Worktree, from, cp.SHA)
	if err != nil {
		return protocol.DiffData{}, fmt.Errorf("checkpoint #%d: %w", a.Seq, err)
	}
	return diffData(s.ID, base, from, res), nil
}

// handleRestoreCheckpoint serves cmd=restoreCheckpoint.
func (d *Daemon) handleRestoreCheckpoint(ctx context.Context, a protocol.CheckpointArgs) (protocol.RestoreCheckpointData, error) {
	s, err := d.checkpointSession(a.Session)
	if err != nil {
		return protocol.RestoreCheckpointData{}, err
	}
	mu := d.ckptLock(s.ID)
	mu.Lock()
	defer mu.Unlock()
	// Hold the send gate EXCLUSIVELY for the whole restore: a review hand-off,
	// queued feedback, a reaction or an answer would otherwise start a turn
	// after the idle check below and let the agent edit files while they are
	// being replaced — edits no checkpoint would hold. Sends that arrive now
	// wait and go out after the restore.
	gate := d.sendGate(s.ID)
	gate.Lock()
	defer gate.Unlock()
	// Re-read under both locks: the agent may have started a turn while this
	// request waited (a send that was in flight has finished by now).
	cur, ok := d.sessions.Get(s.ID)
	if !ok {
		return protocol.RestoreCheckpointData{}, fmt.Errorf("unknown session %s", s.ID)
	}
	if midTurn(cur) {
		return protocol.RestoreCheckpointData{}, fmt.Errorf(
			"%s is mid-turn — wait for the agent to finish (or stop it) before restoring", s.ID)
	}
	cctx, cancel := context.WithTimeout(ctx, checkpointTimeout)
	defer cancel()
	// The axis is the agent's last REPORT; a live agent must also be visibly
	// resting in its pane right now (the same proof every send path demands).
	// A gone agent cannot edit anything and needs no proof.
	if liveAgent(cur) && !d.paneWaitingNow(cctx, cur) {
		return protocol.RestoreCheckpointData{}, fmt.Errorf(
			"%s's agent is not visibly resting at its prompt — wait for it to finish (or stop it) before restoring", s.ID)
	}
	safety, err := d.checkpoints.Restore(cctx, s.Worktree, s.ID, a.Seq)
	if err != nil {
		return protocol.RestoreCheckpointData{}, err
	}
	d.logf("", "checkpoint: %s restored to #%d (previous state kept as #%d)", s.ID, a.Seq, safety.Seq)
	msg := fmt.Sprintf("restored checkpoint #%d — the state before it is checkpoint #%d", a.Seq, safety.Seq)
	if !s.IsAgentless() {
		msg += "; the agent still remembers the discarded changes, so tell it before its next turn"
	}
	return protocol.RestoreCheckpointData{Seq: a.Seq, Safety: safety.Seq, Message: msg}, nil
}

// forkSlotMax bounds the "-fork-N-<k>" search for a free branch/session name.
const forkSlotMax = 20

// handleForkCheckpoint serves cmd=forkCheckpoint: a NEW agent session that
// starts from one of s's checkpoints, on its own lola-owned branch.
func (d *Daemon) handleForkCheckpoint(ctx context.Context, a protocol.CheckpointArgs) (protocol.OpenData, error) {
	s, err := d.checkpointSession(a.Session)
	if err != nil {
		return protocol.OpenData{}, err
	}
	cctx, cancel := context.WithTimeout(ctx, diffTimeout)
	cp, _, err := d.checkpoints.Get(cctx, s.Worktree, s.ID, a.Seq)
	cancel()
	if err != nil {
		return protocol.OpenData{}, err
	}

	kind := strings.TrimSpace(a.Agent)
	if kind != "" && !agent.Valid(kind) {
		return protocol.OpenData{}, fmt.Errorf("unknown agent %q", kind)
	}
	if kind == "" && agent.Valid(s.Agent) {
		kind = s.Agent
	}
	d.mu.Lock()
	nat := d.native
	p := d.cfg.ProjectByName(s.Project)
	health := d.runtimeHealth
	if kind == "" && p != nil {
		kind = d.cfg.AgentForProject(p.Name)
	}
	d.mu.Unlock()
	if p == nil {
		return protocol.OpenData{}, fmt.Errorf("unknown project %q", s.Project)
	}
	if nat == nil {
		return protocol.OpenData{}, errors.New("native runtime unavailable")
	}
	if err := health(agent.Parse(kind).Binary()); err != nil {
		return protocol.OpenData{}, fmt.Errorf("runtime not ready: %w", err)
	}

	stem := s.Branch
	if stem == "" {
		stem = "lola/" + s.ID
	}
	stem = fmt.Sprintf("%s-fork-%d", stem, cp.Seq)
	var id, branch string
	for k := 1; k <= forkSlotMax; k++ {
		branch = stem
		if k > 1 {
			branch = fmt.Sprintf("%s-%d", stem, k)
		}
		id = runtime.ManualSessionID(p.Name, branch)
		// A slot is free only when no session record, no leftover worktree and
		// no local branch claims it — an older fork killed with its worktree kept
		// leaves both behind, and reusing them would fail the fork or adopt
		// someone else's branch.
		if _, taken := d.sessions.Get(id); !taken {
			onDisk, err := nat.SlotTaken(ctx, *p, id, branch)
			if err != nil {
				return protocol.OpenData{}, fmt.Errorf("fork: check %s: %w", branch, err)
			}
			if !onDisk {
				break
			}
		}
		id = ""
	}
	if id == "" {
		return protocol.OpenData{}, fmt.Errorf("no free fork name after %d attempts — kill older forks of %s first", forkSlotMax, s.ID)
	}

	title := s.Title
	if s.Issue != "" {
		title = s.Issue + ": " + s.Title
	}
	spec := runtime.ForkSpec{
		SessionID:  id,
		Branch:     branch,
		Head:       cp.Head,
		Tree:       cp.Tree,
		ContextKey: runtime.ContextKey(s),
		Title:      fmt.Sprintf("fork of %s @#%d", strings.TrimSpace(title), cp.Seq),
		Prompt:     forkPrompt(s, p.DefaultBranch, cp, branch),
		Agent:      kind,
	}
	sctx, scancel := context.WithTimeout(ctx, nativeSpawnTimeout)
	sess, err := nat.ForkAgent(sctx, *p, spec)
	scancel()
	if err != nil {
		return protocol.OpenData{}, err
	}
	sess.Repo = p.Repo
	d.sessions.Upsert(sess)
	d.recordSessionEvent("", sess)
	if serr := d.sessions.Save(); serr != nil {
		d.logf("", "forkCheckpoint: persist sessions after fork %s: %v", id, serr)
	}
	msg := fmt.Sprintf("forked %s at checkpoint #%d (%s) into %s on branch %s", s.ID, cp.Seq, cp.Label, id, branch)
	d.logf("", "forkCheckpoint: %s", msg)
	return protocol.OpenData{SessionID: id, Worktree: sess.Worktree, Branch: branch, Message: msg}, nil
}

// forkPrompt briefs a forked agent: what it was forked from and what state its
// worktree is in. For a Linear session it points at the issue exactly as the
// original briefing did (fetched live, never inlined).
func forkPrompt(s session.Session, defaultBranch string, cp checkpoint.Checkpoint, branch string) string {
	var b strings.Builder
	b.WriteString("# Fork of " + s.ID + "\n\n")
	fmt.Fprintf(&b, "You are a FORK of lola session `%s`, started from its checkpoint #%d (%q). ", s.ID, cp.Seq, cp.Label)
	fmt.Fprintf(&b, "Your worktree holds exactly the state that session had then: its committed history up to `%s`, plus the uncommitted and untracked changes it had at that moment (they show as uncommitted here). ", shortSHA(cp.Head))
	b.WriteString("The original session keeps running separately on its own branch — never touch that branch or its worktree.\n\n")
	if s.Issue != "" {
		fmt.Fprintf(&b, "## Task\n\nThe task is Linear issue **%s** (%q). Fetch its full description and all comments from Linear (Linear MCP tools or CLI; the API key, if present, is in `LINEAR_API_KEY` — never print it or copy it anywhere).\n\n", s.Issue, s.Title)
	} else if s.Title != "" {
		fmt.Fprintf(&b, "## Task\n\nThe original session was %q.\n\n", s.Title)
	}
	b.WriteString("Start by reviewing the current state (`git status`, `git diff`, the shared context folder below), then continue the work from here — a human forked at this point to try a different direction, so do not assume the original session's later turns happened.\n\n")
	b.WriteString("## Git and PR expectations\n\n")
	fmt.Fprintf(&b, "- You are on your own branch `%s`; commit your work here and never switch branches.\n", branch)
	if defaultBranch != "" {
		fmt.Fprintf(&b, "- When the work is done, push the branch and open a pull request against `%s`.\n", defaultBranch)
	}
	b.WriteString("- Never merge the pull request yourself; a human reviews and merges.\n")
	return b.String()
}

func shortSHA(s string) string {
	if len(s) > 12 {
		return s[:12]
	}
	return s
}
