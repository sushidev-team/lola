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
	Source    string    `json:"source"` // "ao" | "native"
	Project   string    `json:"project"`
	Issue     string    `json:"issue"` // Linear identifier, e.g. ENG-123
	IssueUUID string    `json:"issue_uuid"`
	Branch    string    `json:"branch"`
	Repo      string    `json:"repo,omitempty"` // "owner/name" the PR lookup runs against
	TmuxName  string    `json:"tmux_name"`
	AOStatus  string    `json:"ao_status"`
	PR        *scm.PR   `json:"pr,omitempty"`
	Status    string    `json:"status"` // derived
	FirstSeen time.Time `json:"first_seen"`
	LastSeen  time.Time `json:"last_seen"`
	// RemovedLabels are the match-label UUIDs the post-spawn label flip
	// actually stripped from this issue (the trigger labels it carried at
	// flip time — a strict subset of match_labels under match_mode=any). An
	// orphan revert restores EXACTLY these, never the whole match_labels set,
	// so it never re-adds a label the issue never had.
	RemovedLabels []string `json:"removed_labels,omitempty"`

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
}

// Store is a mutex-guarded in-memory session map keyed by ID, persisted as
// JSON at <dir>/sessions.json. Loading is best-effort: a missing or corrupt
// file yields an empty store, never a fatal error — the poller repopulates
// on the next observation pass.
type Store struct {
	mu       sync.Mutex
	path     string
	sessions map[string]Session
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
