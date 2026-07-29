package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/sushidev-team/lola/internal/scm"
	"github.com/sushidev-team/lola/internal/state"
)

func TestSetAgentStateStampsActivity(t *testing.T) {
	now := time.Now()
	s := &Session{AgentState: state.AgentIdle}
	if !s.SetAgentState(state.AgentWorking, state.SourceHook, now) {
		t.Fatal("SetAgentState reported no change")
	}
	if !s.LastActivityAt.Equal(now) || s.ActivitySource != state.SourceHook {
		t.Fatalf("working entry must stamp activity: at=%v src=%q", s.LastActivityAt, s.ActivitySource)
	}
	if !s.AgentStateSince.Equal(now) {
		t.Fatalf("AgentStateSince not stamped: %v", s.AgentStateSince)
	}
	if s.Status != "working" || !s.StatusSince.Equal(now) {
		t.Fatalf("rollup not recomputed: status=%q since=%v", s.Status, s.StatusSince)
	}
	// Re-asserting the same state is not a transition but still heartbeats.
	later := now.Add(time.Minute)
	if s.SetAgentState(state.AgentWorking, state.SourcePane, later) {
		t.Fatal("re-assert reported a change")
	}
	if !s.AgentStateSince.Equal(now) {
		t.Fatal("re-assert must not restamp AgentStateSince")
	}
	if !s.LastActivityAt.Equal(later) || s.ActivitySource != state.SourcePane {
		t.Fatal("re-asserting working must refresh the activity anchor")
	}
}

func TestSetAgentStateClearsTurnFields(t *testing.T) {
	now := time.Now()
	s := &Session{
		AgentState:  state.AgentWorking,
		CurrentTool: "Bash",
	}
	s.SetAgentState(state.AgentWaitingInput, "", now)
	s.InputReason = state.InputPermission
	if s.CurrentTool != "" {
		t.Fatalf("leaving working must clear CurrentTool, got %q", s.CurrentTool)
	}
	s.SetAgentState(state.AgentWorking, state.SourceHook, now.Add(time.Second))
	if s.InputReason != "" {
		t.Fatalf("leaving waiting_input must clear InputReason, got %q", s.InputReason)
	}
}

func TestSetDelivery(t *testing.T) {
	now := time.Now()
	s := &Session{AgentState: state.AgentWorking}
	if !s.SetDelivery(state.DeliveryCIPending, now) {
		t.Fatal("SetDelivery reported no change")
	}
	if s.Status != "ci_pending" {
		t.Fatalf("rollup = %q, want ci_pending (delivery owns post-PR rollup)", s.Status)
	}
	// The agent axis is untouched — that is the whole point of the split.
	if s.AgentState != state.AgentWorking {
		t.Fatalf("agent axis clobbered: %q", s.AgentState)
	}
	if s.SetDelivery(state.DeliveryCIPending, now.Add(time.Minute)) {
		t.Fatal("same delivery reported a change")
	}
	if !s.DeliverySince.Equal(now) {
		t.Fatal("re-assert must not restamp DeliverySince")
	}
}

// TestLoadMigratesAxes: a pre-axis snapshot (status string + PR facts only)
// loads with both axes backfilled and the same visible Status.
func TestLoadMigratesAxes(t *testing.T) {
	dir := t.TempDir()
	last := time.Now().Add(-time.Hour).Truncate(time.Second)
	legacy := []map[string]any{
		{
			"id": "lola-p-1", "source": "native", "project": "p",
			"status": "ci_pending", "tmux_name": "lola-p-1",
			"issue": "ENG-1", "issue_uuid": "u1", "branch": "b1",
			"last_seen": last, "first_seen": last.Add(-time.Hour),
			"pr": map[string]any{"number": 7, "state": "OPEN", "mergeable": "MERGEABLE", "checks_state": "pending"},
		},
		{
			"id": "lola-p-2", "source": "native", "project": "p",
			"status": "needs_input", "tmux_name": "lola-p-2",
			"issue": "ENG-2", "issue_uuid": "u2", "branch": "b2",
			"last_seen": last, "first_seen": last,
		},
		{ // delivery-owned status with NO PR facts: axis from the word itself
			"id": "lola-p-3", "source": "native", "project": "p",
			"status": "merged", "tmux_name": "lola-p-3",
			"issue": "ENG-3", "issue_uuid": "u3", "branch": "b3",
			"last_seen": last, "first_seen": last,
		},
	}
	data, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sessions.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}

	st := NewStore(dir)
	s1, ok := st.Get("lola-p-1")
	if !ok {
		t.Fatal("lola-p-1 missing")
	}
	if s1.AgentState != state.AgentIdle || s1.Delivery != state.DeliveryCIPending {
		t.Fatalf("s1 axes = (%s, %s)", s1.AgentState, s1.Delivery)
	}
	if s1.Status != "ci_pending" {
		t.Fatalf("s1 visible status changed: %q", s1.Status)
	}
	if !s1.AgentStateSince.Equal(last) || !s1.StatusSince.Equal(last) {
		t.Fatalf("s1 Since not seeded from LastSeen: %v / %v", s1.AgentStateSince, s1.StatusSince)
	}
	if !s1.AtPromptVerified {
		t.Fatal("s1 migration must seed AtPromptVerified")
	}

	s2, _ := st.Get("lola-p-2")
	if s2.AgentState != state.AgentWaitingInput || s2.Delivery != state.DeliveryNone {
		t.Fatalf("s2 axes = (%s, %s)", s2.AgentState, s2.Delivery)
	}

	s3, _ := st.Get("lola-p-3")
	if s3.Delivery != state.DeliveryMerged || s3.Status != "merged" {
		t.Fatalf("s3 = (%s, %q), want merged delivery from bare status word", s3.Delivery, s3.Status)
	}
}

// TestLoadKeepsAxisBearingRecords: migrateAxes must be idempotent.
func TestLoadKeepsAxisBearingRecords(t *testing.T) {
	dir := t.TempDir()
	st := NewStore(dir)
	now := time.Now()
	sess := Session{ID: "lola-p-1", Source: "native", Project: "p"}
	sess.SetAgentState(state.AgentWaitingInput, "", now)
	sess.InputReason = state.InputQuestion
	st.Upsert(sess)
	if err := st.Save(); err != nil {
		t.Fatal(err)
	}

	st2 := NewStore(dir)
	got, _ := st2.Get("lola-p-1")
	if got.AgentState != state.AgentWaitingInput || got.InputReason != state.InputQuestion {
		t.Fatalf("reload mangled axes: (%s, %s)", got.AgentState, got.InputReason)
	}
	if got.AtPromptVerified {
		t.Fatal("axis-bearing record must not be re-migrated (AtPromptVerified flipped)")
	}
}

func TestApplyInsertsAndUpdates(t *testing.T) {
	st := NewStore(t.TempDir())

	// Insert: fn sees exists=false, commit stores with stamps.
	got, committed := st.Apply("lola-p-1", func(s *Session, exists bool) bool {
		if exists {
			t.Fatal("exists=true for fresh id")
		}
		s.Project = "p"
		s.SetAgentState(state.AgentWorking, state.SourceHook, time.Now())
		return true
	})
	if !committed || got.FirstSeen.IsZero() || got.Status != "working" {
		t.Fatalf("insert: committed=%v first=%v status=%q", committed, got.FirstSeen, got.Status)
	}

	// Update: derives from current record.
	_, committed = st.Apply("lola-p-1", func(s *Session, exists bool) bool {
		if !exists || s.Project != "p" {
			t.Fatalf("update must see stored record, exists=%v project=%q", exists, s.Project)
		}
		s.SetDelivery(state.DeliveryCIPending, time.Now())
		return true
	})
	if !committed {
		t.Fatal("update not committed")
	}
	final, _ := st.Get("lola-p-1")
	if final.Status != "ci_pending" || final.AgentState != state.AgentWorking {
		t.Fatalf("final = (%q, %s)", final.Status, final.AgentState)
	}

	// Decline: nothing stored.
	_, committed = st.Apply("lola-p-x", func(s *Session, exists bool) bool { return false })
	if committed {
		t.Fatal("declined Apply reported committed")
	}
	if _, ok := st.Get("lola-p-x"); ok {
		t.Fatal("declined insert leaked into the store")
	}
}

// TestApplyTransitionCallback: updates fire the transition callback on a
// Status change; inserts do NOT (birth is recorded by the spawn/adopt site,
// matching Upsert's behavior).
func TestApplyTransitionCallback(t *testing.T) {
	st := NewStore(t.TempDir())
	var mu sync.Mutex
	var fired []string
	st.OnTransition(func(from string, s Session) {
		mu.Lock()
		defer mu.Unlock()
		fired = append(fired, from+"→"+s.Status)
	})

	st.Apply("lola-p-1", func(s *Session, exists bool) bool {
		s.SetAgentState(state.AgentWorking, state.SourceHook, time.Now())
		return true
	})
	mu.Lock()
	if len(fired) != 0 {
		t.Fatalf("insert fired transition callback: %v", fired)
	}
	mu.Unlock()

	st.Apply("lola-p-1", func(s *Session, exists bool) bool {
		s.SetAgentState(state.AgentWaitingInput, "", time.Now())
		return true
	})
	mu.Lock()
	defer mu.Unlock()
	if len(fired) != 1 || fired[0] != "working→needs_input" {
		t.Fatalf("update transitions = %v", fired)
	}
}

// TestApplyDoesNotRaceHookWrites: an Apply mutation derives from the record
// under the lock, so a concurrent Update (the hook handler) is never erased —
// the failure mode of the old Get→mutate→Upsert.
func TestApplyDoesNotRaceHookWrites(t *testing.T) {
	st := NewStore(t.TempDir())
	st.Upsert(Session{ID: "lola-p-1", Project: "p"})

	var wg sync.WaitGroup
	for range 50 {
		wg.Add(2)
		go func() {
			defer wg.Done()
			st.Update("lola-p-1", func(s *Session) bool {
				s.CIRetries++
				return true
			})
		}()
		go func() {
			defer wg.Done()
			st.Apply("lola-p-1", func(s *Session, exists bool) bool {
				s.Escalated = !s.Escalated
				return true
			})
		}()
	}
	wg.Wait()
	got, _ := st.Get("lola-p-1")
	if got.CIRetries != 50 {
		t.Fatalf("CIRetries = %d, want 50 (Apply erased hook writes)", got.CIRetries)
	}
}

// Sanity: axis fields survive a JSON round-trip with their snake_case tags.
func TestAxisFieldsPersist(t *testing.T) {
	dir := t.TempDir()
	st := NewStore(dir)
	now := time.Now()
	sess := Session{ID: "lola-p-1", Project: "p",
		PR: &scm.PR{Number: 3, State: "OPEN", ChecksState: "fail"}}
	sess.SetAgentState(state.AgentWorking, state.SourceHook, now)
	sess.SetDelivery(state.DeliveryCIFailed, now)
	sess.CurrentTool = "Edit"
	sess.TranscriptPath = "/tmp/t.jsonl"
	sess.PRObservedAt = now
	sess.PRFetchFailures = 2
	sess.Summary = "fixing the flaky auth test"
	sess.InterpretedState = "working"
	st.Upsert(sess)
	if err := st.Save(); err != nil {
		t.Fatal(err)
	}
	got, _ := NewStore(dir).Get("lola-p-1")
	if got.AgentState != state.AgentWorking || got.Delivery != state.DeliveryCIFailed ||
		got.CurrentTool != "Edit" || got.TranscriptPath != "/tmp/t.jsonl" ||
		got.PRFetchFailures != 2 || got.Summary == "" || got.InterpretedState != "working" {
		t.Fatalf("round-trip lost fields: %+v", got)
	}
}
