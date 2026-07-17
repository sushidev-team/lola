// Package session holds the session model and its JSON snapshot store
// (PLAN P1.5): read-only observability over agent sessions while AO still
// owns spawning. Pure data package — no exec, no ao/tmux imports; state is
// persisted as JSON with the same atomic temp+rename discipline as
// internal/config.
package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/sushidev-team/lola/internal/scm"
)

// Session is one observed agent session, regardless of who spawned it.
type Session struct {
	ID        string    `json:"id"`
	Source    string    `json:"source"`          // "ao" | "native"
	Agent     string    `json:"agent,omitempty"` // coding-agent kind: claude|codex|opencode; "" = legacy claude
	Project   string    `json:"project"`
	// Manual marks a session opened by hand via `lola open` (a branch/PR checked
	// out in a throwaway DETACHED worktree with a plain shell — no coding agent,
	// no Linear issue) rather than dispatched from a Linear match. It is the
	// control-loop opt-out: the observer derives such a session's status from tmux
	// liveness alone ("shell"/"dead") and the reaction / write-back / review /
	// coderabbit engines all skip it, so lola never send-keys into the human's
	// interactive shell. Persisted so the flag survives a daemon restart (adoption
	// re-detects it from the session-ID shape as a backstop).
	Manual    bool      `json:"manual,omitempty"`
	Issue     string    `json:"issue"`           // Linear identifier, e.g. ENG-123 ("" for a manual session)
	Title     string    `json:"title,omitempty"` // Linear issue title, so a session is identifiable by what it's about
	IssueUUID string    `json:"issue_uuid"`
	Branch    string    `json:"branch"`
	Repo      string    `json:"repo,omitempty"` // "owner/name" the PR lookup runs against
	TmuxName  string    `json:"tmux_name"`
	AOStatus  string    `json:"ao_status"`
	PR        *scm.PR   `json:"pr,omitempty"`
	Status    string    `json:"status"` // derived
	FirstSeen time.Time `json:"first_seen"`
	LastSeen  time.Time `json:"last_seen"`
	// LastActivityAt is the last time we had POSITIVE evidence of work on this
	// session: a tool_use / user_prompt hook heartbeat, or an observed pane the
	// activity classifier read as ActivityWorking. It is the anchor for the
	// observer's anti-false-working guard — a session whose stored status is
	// "working" but whose LastActivityAt has gone stale (and whose pane cannot
	// confirm work) is no longer trusted as working. Distinct from LastSeen,
	// which is stamped on EVERY store write (including a mere liveness touch)
	// and therefore is NOT evidence of activity. Persisted so the guard survives
	// a daemon restart.
	LastActivityAt time.Time `json:"last_activity_at,omitempty"`
	// RemovedLabels are the match-label UUIDs the post-spawn label flip
	// actually stripped from this issue (the trigger labels it carried at
	// flip time — a strict subset of match_labels under match_mode=any). An
	// orphan revert restores EXACTLY these, never the whole match_labels set,
	// so it never re-adds a label the issue never had.
	RemovedLabels []string `json:"removed_labels,omitempty"`

	// PollName is the poll that spawned this session; it selects that poll's
	// P4 Linear write-back configuration (state transitions + comments) as the
	// session progresses through its lifecycle. Empty for sessions adopted
	// without a prior record — write-back is then resolved by project or
	// skipped (see Daemon.pollForSession).
	PollName string `json:"poll_name,omitempty"`

	// Write-back one-shot guards (PLAN P4): each lifecycle write-back fires at
	// most once per session and the guard PERSISTS across restarts, so a
	// state-change/comment is never repeated on the 30s observer cadence. A
	// guard is set optimistically once its transition posts (or would post) a
	// comment, so a retry never double-comments. All-empty write-back config
	// leaves them untouched. See internal/daemon/writeback.go.
	WBSpawnDone   bool `json:"wb_spawn_done,omitempty"`
	WBPRDone      bool `json:"wb_pr_done,omitempty"`
	WBMergedDone  bool `json:"wb_merged_done,omitempty"`
	WBBlockedDone bool `json:"wb_blocked_done,omitempty"`

	// Reaction-state fields (PLAN P3): the persisted memory the reaction
	// engine keeps per session so a reaction fires once per state transition
	// (not on every 30s observer cycle) and gives up after a bounded number of
	// automatic retries. All persist in sessions.json.

	// CIRetries counts how many times the ci_failed reaction has already
	// re-prompted this agent for the CURRENT failing streak. It increments
	// each time a recovery prompt is sent and resets to 0 once checks pass
	// again; when it reaches the project's escalate_after it flips Escalated.
	CIRetries int `json:"ci_retries,omitempty"`

	// LastReactedStatus is the derived Status the engine last actually ACTED
	// on. The engine reacts only when the current Status differs from this,
	// then stamps it — so a persistent ci_failed / changes_requested state
	// re-prompts the agent once per transition into that state, never every
	// observer tick. Cleared/overwritten as the session moves between states.
	LastReactedStatus string `json:"last_reacted_status,omitempty"`

	// Escalated is set once the ci_failed retries are exhausted
	// (CIRetries ≥ escalate_after): the engine stops auto-retrying and hands
	// off to the notifier/human. Stays true until checks pass and reset it, so
	// an escalated session is never re-prompted in a loop.
	Escalated bool `json:"escalated,omitempty"`

	// AtPrompt is the send-keys SAFETY GATE: true only when the agent is idle
	// at its input prompt (set by the Claude Code Stop hook), cleared the
	// moment we send it a message or it resumes work (tool_use / notification).
	// The engine must never type into a pane while the agent is mid-turn, so a
	// reaction is dispatched only when AtPrompt is true; otherwise it parks in
	// PendingReaction for the next cycle.
	AtPrompt bool `json:"at_prompt,omitempty"`

	// PendingReaction holds a reaction (the target Status that triggered it,
	// e.g. "ci_failed") that was computed while the agent was mid-turn
	// (AtPrompt false) and therefore deferred. The engine retries delivering
	// it on the next observer cycle once AtPrompt becomes true, then clears it.
	PendingReaction string `json:"pending_reaction,omitempty"`

	// ReviewedPR is the PR number the P9 CodeRabbit "QA buddy" review pass last
	// ran for (0 = never reviewed). It is the per-PR one-shot guard: the pass
	// runs once when a session first opens a PR, and the daemon skips it while
	// ReviewedPR already equals the current PR number. A session that opens a
	// NEW PR (or a reopened PR that comes back under a different number) gets a
	// different PR number, so the guard no longer matches and the review
	// re-triggers exactly once for that PR. Persists across restarts so a review
	// is never repeated on the 30s observer cadence.
	ReviewedPR int `json:"reviewed_pr,omitempty"`

	// LastCodeRabbitAt is the watermark for the [coderabbit] PR-comment WATCH: the
	// timestamp of the newest CodeRabbit (or configured bot) comment/review the
	// observer has already routed for this session. The poll fires only on items
	// STRICTLY newer than this, so a comment is surfaced once and the watch
	// survives any daemon downtime (the next cycle reconciles current PR state
	// rather than replaying a missed webhook). Zero means "never polled" — the
	// first poll then surfaces the newest existing CodeRabbit comment once.
	// Persists across restarts so a comment is never re-fired on the 30s cadence.
	LastCodeRabbitAt time.Time `json:"last_coderabbit_at,omitempty"`

	// PendingCodeRabbit holds the short, single-line CodeRabbit hand-off POINTER
	// (see config.CodeRabbitAgentPointerFmt — our own text referencing the PR, not
	// the raw comment) that was ready to hand to the worker agent but could not be
	// sent because the agent was mid-turn (AtPrompt false) at route time. A later
	// observer cycle delivers it once the agent returns to its prompt, then clears
	// it. It is the [coderabbit] equivalent of PendingReviewFindings — kept a
	// SEPARATE field so a watch hand-off and a [review] hand-off never clobber each
	// other. Persists across restarts.
	PendingCodeRabbit string `json:"pending_coderabbit,omitempty"`

	// PendingReviewFindings holds the (raw, untrusted) CodeRabbit review findings
	// that were ready to hand to the worker agent but could not be sent because
	// the agent was mid-turn (AtPrompt false) at route time. A later observer
	// cycle delivers it once the agent returns to its prompt (the P3 send-keys
	// idle-gate), sanitizing it immediately before the send, then clears it. It
	// is the P9 equivalent of PendingReaction — a one-shot-per-PR deferral — so a
	// review hand-off is deferred rather than dropped when the agent is busy, and
	// never sent unsanitized or into a mid-turn pane. Persists across restarts.
	PendingReviewFindings string `json:"pending_review_findings,omitempty"`
}

// Store is a mutex-guarded in-memory session map keyed by ID, persisted as
// JSON at <dir>/sessions.json. Loading is best-effort: a missing or corrupt
// file yields an empty store, never a fatal error — the poller repopulates
// on the next observation pass.
type Store struct {
	mu       sync.Mutex
	path     string
	sessions map[string]Session

	// onTransition, when set, is invoked by Update AFTER it commits a mutation
	// that CHANGED Status: from is the prior Status, s is the stored (new)
	// session copy. It runs UNDER the store lock — the callback MUST NOT call
	// back into the store (same rule as Update's fn) or it deadlocks. The daemon
	// registers it to feed its activity-event ring; nil in tests / bare Stores,
	// where Update behaves exactly as before.
	onTransition func(from string, s Session)
}

// OnTransition registers a status-transition callback (see the field doc). Pass
// nil to clear it. Setting it takes the store lock, so never call this from
// inside an Update closure.
func (s *Store) OnTransition(fn func(from string, s Session)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onTransition = fn
}

// NewStore returns a Store backed by <dir>/sessions.json and loads any
// existing snapshot. Corrupt or missing files are tolerated silently.
func NewStore(dir string) *Store {
	s := &Store{
		path:     filepath.Join(dir, "sessions.json"),
		sessions: make(map[string]Session),
	}
	s.load()
	return s
}

// load replaces the in-memory map with the on-disk snapshot. Any read or
// parse failure leaves the store empty.
func (s *Store) load() {
	data, err := os.ReadFile(s.path)
	if err != nil {
		return
	}
	var sessions []Session
	if err := json.Unmarshal(data, &sessions); err != nil {
		return
	}
	for _, sess := range sessions {
		if sess.ID == "" {
			continue
		}
		s.sessions[sess.ID] = sess
	}
}

// Snapshot returns all sessions sorted by Project, then Issue, then ID —
// a stable order for the TUI. PR snapshots are copied, so mutating the
// result never aliases store state.
func (s *Store) Snapshot() []Session {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snapshotLocked()
}

func (s *Store) snapshotLocked() []Session {
	out := make([]Session, 0, len(s.sessions))
	for _, sess := range s.sessions {
		if sess.PR != nil {
			pr := *sess.PR
			sess.PR = &pr
		}
		out = append(out, sess)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Project != out[j].Project {
			return out[i].Project < out[j].Project
		}
		if out[i].Issue != out[j].Issue {
			return out[i].Issue < out[j].Issue
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// Get returns a copy of the session with the given ID (PR copied, so the
// result never aliases store state), or ok=false when unknown. The observer
// uses it to carry persisted Branch/Repo/PR facts into cycles that could not
// re-derive them (e.g. right after a daemon restart, when the tick-fed branch
// map is still empty).
func (s *Store) Get(id string) (Session, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[id]
	if !ok {
		return Session{}, false
	}
	if sess.PR != nil {
		pr := *sess.PR
		sess.PR = &pr
	}
	return sess, true
}

// Upsert inserts or updates a session by ID. FirstSeen of an existing entry
// is preserved (stamped now for new entries without one); LastSeen is always
// stamped now.
func (s *Store) Upsert(sess Session) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	if old, ok := s.sessions[sess.ID]; ok {
		sess.FirstSeen = old.FirstSeen
	}
	if sess.FirstSeen.IsZero() {
		sess.FirstSeen = now
	}
	sess.LastSeen = now
	if sess.PR != nil {
		pr := *sess.PR
		sess.PR = &pr // never share a pointer with the caller
	}
	s.sessions[sess.ID] = sess
}

// Update applies fn to the stored session with the given id as ONE atomic
// read-modify-write under the store lock, returning the resulting session (a
// copy) and whether the id was known. fn receives a copy of the current
// record (PR copied, never aliasing store state) and returns whether to keep
// the mutation: true stores it back (LastSeen stamped now, like Upsert),
// false discards it and leaves the record — including LastSeen — untouched.
// fn must not change the ID and must not call back into the store.
//
// Callers whose new state DERIVES from the current record (the observer's
// status merge, hook-event transitions) must use Update instead of
// Get→mutate→Upsert: the unlocked variant races concurrent writers and a
// stale snapshot would silently erase their transitions.
func (s *Store) Update(id string, fn func(sess *Session) bool) (Session, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[id]
	if !ok {
		return Session{}, false
	}
	oldStatus := sess.Status // captured before fn mutates the copy
	if sess.PR != nil {
		pr := *sess.PR
		sess.PR = &pr
	}
	if !fn(&sess) {
		return sess, true
	}
	sess.ID = id // the key is immutable
	sess.LastSeen = time.Now()
	if sess.FirstSeen.IsZero() {
		sess.FirstSeen = sess.LastSeen
	}
	stored := sess
	if stored.PR != nil {
		pr := *stored.PR
		stored.PR = &pr // never share a pointer with the caller
	}
	s.sessions[id] = stored
	// Fire the transition callback only when Status actually changed. It runs
	// under the store lock (see OnTransition) — the daemon's handler only
	// touches its own event ring, never the store.
	if s.onTransition != nil && stored.Status != oldStatus {
		s.onTransition(oldStatus, stored)
	}
	return sess, true
}

// Delete removes the session with the given id under the store lock and
// reports whether it existed. Used by an explicit kill once the session's
// worktree has been removed; callers Save afterwards to persist the drop.
func (s *Store) Delete(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.sessions[id]; !ok {
		return false
	}
	delete(s.sessions, id)
	return true
}

// PruneOlderThan drops sessions whose LastSeen is older than d and returns
// how many were removed. Dead sessions age out of the snapshot this way.
func (s *Store) PruneOlderThan(d time.Duration) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	cutoff := time.Now().Add(-d)
	n := 0
	for id, sess := range s.sessions {
		if sess.LastSeen.Before(cutoff) {
			delete(s.sessions, id)
			n++
		}
	}
	return n
}

// Save writes the snapshot atomically: parents are created 0700, the JSON is
// written to a temp file in the destination directory (so the rename cannot
// cross filesystems), then renamed into place with final mode 0600.
func (s *Store) Save() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := json.MarshalIndent(s.snapshotLocked(), "", "  ")
	if err != nil {
		return err
	}

	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}

	tmp, err := os.CreateTemp(dir, ".sessions-*.json")
	if err != nil {
		return err
	}
	defer func() {
		if tmp != nil {
			tmp.Close()
			os.Remove(tmp.Name())
		}
	}()

	if err := tmp.Chmod(0o600); err != nil {
		return err
	}
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	name := tmp.Name()
	tmp = nil // written and closed; disarm the cleanup deferral
	return os.Rename(name, s.path)
}
