package daemon

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/linear"
	"github.com/sushidev-team/lola/internal/session"
	"github.com/sushidev-team/lola/internal/state"
)

// interpDaemon builds a test daemon with the interpreter enabled through a
// fake seam returning cannedRaw, and one live working session seeded.
func interpDaemon(t *testing.T, cannedRaw string) (*Daemon, *session.Session, *int) {
	t.Helper()
	d := newTestDaemon(t, nativeTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeNative{})
	d.cfg.StatusAgent = config.StatusAgentConfig{
		Enabled: true, MinConfidence: 0.5, MaxPerCycle: 2,
		MinIntervalSeconds: 120, TimeoutSeconds: 60,
	}
	calls := 0
	d.interpretSeam = func(ctx context.Context, contextText string) (string, error) {
		calls++
		return cannedRaw, nil
	}
	d.interpretOn.Store(true)
	d.paneTail = func(ctx context.Context, tmuxName string, lines int) (string, error) {
		return paneUnknown, nil
	}

	s := nativeSess("FE-1", "working")
	d.sessions.Upsert(s)
	seeded, _ := d.sessions.Get(s.ID)
	return d, &seeded, &calls
}

// A valid judgement lands ONLY on the overlay fields — the deterministic
// record (Status, axes, AtPrompt, guards) is byte-identical.
func TestInterpretWriteIsolation(t *testing.T) {
	d, s, _ := interpDaemon(t, `{"agent_state":"stuck","headline":"looping on a failing build","waiting_on":"a human to unstick it","confidence":0.9}`)
	before, _ := d.sessions.Get(s.ID)

	d.interpretOne(context.Background(), s.ID)

	after, _ := d.sessions.Get(s.ID)
	if after.Summary != "looping on a failing build" || after.InterpretedState != "stuck" ||
		after.WaitingOn != "a human to unstick it" || after.InterpretedConfidence != 0.9 ||
		after.SummaryAt.IsZero() || after.InterpretedForAgentState != "working" {
		t.Fatalf("overlay not written: %+v", after)
	}
	// Deterministic fields untouched.
	clearOverlay := func(x session.Session) session.Session {
		x.InterpretedState, x.Summary, x.WaitingOn = "", "", ""
		x.InterpretedConfidence = 0
		x.SummaryAt = time.Time{}
		x.InterpretedForAgentState = ""
		x.LastInterpretedAt = time.Time{}
		x.LastInterpretedHash = ""
		x.LastSeen = time.Time{}
		return x
	}
	if !reflect.DeepEqual(clearOverlay(before), clearOverlay(after)) {
		t.Fatalf("interpreter write touched deterministic fields:\nbefore %+v\nafter  %+v", before, after)
	}
	if after.Status != "working" {
		t.Fatalf("Status changed to %q — the interpreter must never move it", after.Status)
	}
}

// Low confidence or parse failure clears the overlay and stamps the attempt.
func TestInterpretRejectsLowConfidenceAndGarbage(t *testing.T) {
	cases := []string{
		`{"agent_state":"working","headline":"maybe doing things","confidence":0.2}`, // below floor
		`no json here at all`,
		`{"agent_state":"launch_the_missiles","headline":"x","confidence":1}`, // unlisted
	}
	for _, raw := range cases {
		d, s, _ := interpDaemon(t, raw)
		d.interpretOne(context.Background(), s.ID)
		got, _ := d.sessions.Get(s.ID)
		if got.Summary != "" || got.InterpretedState != "" {
			t.Errorf("raw %q: overlay written (%q/%q), want cleared", raw, got.InterpretedState, got.Summary)
		}
		if got.LastInterpretedAt.IsZero() || got.LastInterpretedHash == "" {
			t.Errorf("raw %q: attempt not stamped", raw)
		}
	}
}

// The debounce and the input hash both skip the exec.
func TestInterpretDebounceAndHashSkip(t *testing.T) {
	d, s, calls := interpDaemon(t, `{"agent_state":"working","headline":"running tests","confidence":0.9}`)

	d.interpretOne(context.Background(), s.ID)
	if *calls != 1 {
		t.Fatalf("first run: %d calls, want 1", *calls)
	}

	// Within the debounce window: skipped before any gathering.
	d.interpretOne(context.Background(), s.ID)
	if *calls != 1 {
		t.Fatalf("debounced run still exec'd: %d calls", *calls)
	}

	// Debounce expired but the input bundle is unchanged: hash skip.
	d.sessions.Update(s.ID, func(cur *session.Session) bool {
		cur.LastInterpretedAt = time.Now().Add(-time.Hour)
		return true
	})
	d.interpretOne(context.Background(), s.ID)
	if *calls != 1 {
		t.Fatalf("unchanged input still exec'd: %d calls", *calls)
	}
	got, _ := d.sessions.Get(s.ID)
	if time.Since(got.LastInterpretedAt) > time.Minute {
		t.Fatal("hash skip must restamp the debounce anchor")
	}

	// The pane changed: a fresh exec.
	d.paneTail = func(ctx context.Context, tmuxName string, lines int) (string, error) {
		return "something completely different\n", nil
	}
	d.sessions.Update(s.ID, func(cur *session.Session) bool {
		cur.LastInterpretedAt = time.Now().Add(-time.Hour)
		return true
	})
	d.interpretOne(context.Background(), s.ID)
	if *calls != 2 {
		t.Fatalf("changed input: %d calls, want 2", *calls)
	}
}

// An erroring interpreter stamps attempt + hash so the same input is not
// retried every cycle, and leaves the overlay untouched.
func TestInterpretErrorStampsAttempt(t *testing.T) {
	d, s, _ := interpDaemon(t, "")
	d.interpretSeam = func(ctx context.Context, contextText string) (string, error) {
		return "", errors.New("claude exploded")
	}
	d.interpretOne(context.Background(), s.ID)
	got, _ := d.sessions.Get(s.ID)
	if got.LastInterpretedAt.IsZero() || got.LastInterpretedHash == "" {
		t.Fatal("error attempt not stamped")
	}
	if got.Summary != "" {
		t.Fatal("error must not write an overlay")
	}
}

// displayOverlay's precedence: fresh+matching ships; expiry, low confidence,
// and a real agent-axis transition all hide it. Agreement ships the headline
// with NO interpreted-state override.
func TestDisplayOverlayPrecedence(t *testing.T) {
	now := time.Now()
	base := session.Session{
		AgentState:               state.AgentWorking,
		AgentStateSince:          now.Add(-time.Hour),
		InterpretedState:         "stuck",
		Summary:                  "looping on a failing build",
		WaitingOn:                "",
		InterpretedConfidence:    0.9,
		SummaryAt:                now.Add(-time.Minute),
		InterpretedForAgentState: "working",
	}

	if istate, headline, _, _ := displayOverlay(base, 0.5, now); istate != "stuck" || headline == "" {
		t.Fatalf("fresh disagreeing overlay = (%q, %q), want (stuck, headline)", istate, headline)
	}

	agrees := base
	agrees.InterpretedState = "working"
	if istate, headline, _, _ := displayOverlay(agrees, 0.5, now); istate != "" || headline == "" {
		t.Fatalf("agreement = (%q, %q), want (\"\", headline ships)", istate, headline)
	}

	expired := base
	expired.SummaryAt = now.Add(-11 * time.Minute)
	if _, headline, _, _ := displayOverlay(expired, 0.5, now); headline != "" {
		t.Fatal("expired overlay must not ship")
	}

	lowConf := base
	lowConf.InterpretedConfidence = 0.3
	if _, headline, _, _ := displayOverlay(lowConf, 0.5, now); headline != "" {
		t.Fatal("low-confidence overlay must not ship")
	}

	superseded := base
	superseded.AgentState = state.AgentIdle // a real transition since the judgement
	superseded.AgentStateSince = now.Add(-10 * time.Second)
	if _, headline, _, _ := displayOverlay(superseded, 0.5, now); headline != "" {
		t.Fatal("a real agent-axis transition must supersede the overlay")
	}
}

// The overlay rides the wire pre-gated: sessionsData ships headline/state for
// a valid judgement and nothing once superseded.
func TestSessionsDataCarriesOverlay(t *testing.T) {
	d, s, _ := interpDaemon(t, `{"agent_state":"stuck","headline":"looping on a failing build","confidence":0.9}`)
	d.interpretOne(context.Background(), s.ID)

	data := d.sessionsData()
	if len(data.Sessions) != 1 {
		t.Fatalf("want 1 session, got %d", len(data.Sessions))
	}
	si := data.Sessions[0]
	if si.InterpretedState != "stuck" || si.Headline == "" || si.HeadlineAgo == "" {
		t.Fatalf("overlay missing on the wire: %+v", si)
	}

	// A real transition supersedes: the wire drops the overlay instantly.
	d.sessions.Update(s.ID, func(cur *session.Session) bool {
		cur.SetAgentState(state.AgentIdle, "", time.Now())
		return true
	})
	data = d.sessionsData()
	if si := data.Sessions[0]; si.Headline != "" || si.InterpretedState != "" {
		t.Fatalf("superseded overlay still on the wire: %+v", si)
	}
}

// Dead/exited/shell sessions and disabled interpreters never exec.
func TestInterpretGates(t *testing.T) {
	d, s, calls := interpDaemon(t, `{"agent_state":"working","headline":"x","confidence":1}`)

	d.sessions.Update(s.ID, func(cur *session.Session) bool {
		cur.SetAgentState(state.AgentDead, "", time.Now())
		return true
	})
	d.interpretOne(context.Background(), s.ID)
	if *calls != 0 {
		t.Fatal("dead session must not be interpreted")
	}

	d.sessions.Update(s.ID, func(cur *session.Session) bool {
		cur.SetAgentState(state.AgentWorking, "", time.Now())
		return true
	})
	d.cfg.StatusAgent.Enabled = false
	d.interpretOne(context.Background(), s.ID)
	if *calls != 0 {
		t.Fatal("disabled interpreter must not exec")
	}
}

// The observer's ambiguous-state sweep queues at most max_per_cycle.
func TestObserverQueuesAmbiguousCapped(t *testing.T) {
	d := newTestDaemon(t, nativeTestConfig(nativePoll("p1")), &linear.Fake{},
		&fakeNative{alive: map[string]bool{"lola-p1-fe-1": true, "lola-p1-fe-2": true, "lola-p1-fe-3": true}})
	(&fakeObsSeams{}).install(d)
	d.paneTail = func(ctx context.Context, tmuxName string, lines int) (string, error) {
		return paneUnknown, nil
	}
	d.cfg.StatusAgent = config.StatusAgentConfig{Enabled: true, MaxPerCycle: 2, MinConfidence: 0.5}
	d.interpretOn.Store(true)

	for _, ident := range []string{"FE-1", "FE-2", "FE-3"} {
		s := nativeSess(ident, "needs_input") // waiting_input axis: always ambiguous
		d.sessions.Upsert(s)
	}
	d.observe(context.Background())

	queued := len(d.interpretCh)
	if queued != 2 {
		t.Fatalf("queued %d interpretations, want the max_per_cycle cap of 2", queued)
	}
}
