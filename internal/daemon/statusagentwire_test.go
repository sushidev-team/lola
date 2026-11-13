package daemon

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/doctor"
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

// An unchanged input bundle refreshes the existing overlay's validity (the
// judgement still holds) instead of letting it expire on a still session.
func TestInterpretHashSkipRefreshesOverlay(t *testing.T) {
	d, s, calls := interpDaemon(t, `{"agent_state":"stuck","headline":"looping on a failing build","confidence":0.9}`)
	d.interpretOne(context.Background(), s.ID)
	if *calls != 1 {
		t.Fatalf("first run: %d calls, want 1", *calls)
	}
	// Age the judgement close to expiry, clear the debounce, re-run: the input
	// is unchanged, so no exec — but SummaryAt must be refreshed.
	old := time.Now().Add(-9 * time.Minute)
	d.sessions.Update(s.ID, func(cur *session.Session) bool {
		cur.SummaryAt = old
		cur.LastInterpretedAt = time.Now().Add(-time.Hour)
		return true
	})
	d.interpretOne(context.Background(), s.ID)
	if *calls != 1 {
		t.Fatalf("unchanged input exec'd again: %d calls", *calls)
	}
	got, _ := d.sessions.Get(s.ID)
	if !got.SummaryAt.After(old) {
		t.Fatal("hash skip must refresh SummaryAt while the judgement's basis is unchanged")
	}
}

// --- circuit breaker -------------------------------------------------------

// breakerDaemon builds a test daemon whose interpreter always fails, with the
// debounce OFF and a pane that changes on every read — so neither the debounce
// nor the input-hash skip can be mistaken for the breaker doing its job. The
// returned buffer captures the daemon log; the counter counts real exec calls.
func breakerDaemon(t *testing.T) (*Daemon, string, *int, *bytes.Buffer) {
	t.Helper()
	d, s, _ := interpDaemon(t, "")
	d.cfg.StatusAgent.MinIntervalSeconds = 0
	var logs bytes.Buffer
	d.log = log.New(&logs, "", 0)
	reads := 0
	d.paneTail = func(ctx context.Context, tmuxName string, lines int) (string, error) {
		reads++
		return fmt.Sprintf("pane read %d\n", reads), nil
	}
	calls := 0
	d.interpretSeam = func(ctx context.Context, contextText string) (string, error) {
		calls++
		return "", errors.New("statusagent: interpreter exited nonzero (exit status 1): You must run `claude` to review the updated terms.")
	}
	return d, s.ID, &calls, &logs
}

// rewindBreaker ages the trip so the cooldown has elapsed.
func rewindBreaker(d *Daemon) {
	b := d.interpretBreakerFor()
	b.mu.Lock()
	b.trippedAt = time.Now().Add(-interpretBreakerCooldown - time.Minute)
	b.mu.Unlock()
}

// N consecutive failures disable the interpreter: it stops exec'ing AND stops
// queueing, the trip is logged exactly once (not per attempt), and the reason
// is left where `lola doctor` can find it.
func TestInterpretBreakerTripsAfterConsecutiveFailures(t *testing.T) {
	d, id, calls, logs := breakerDaemon(t)

	for i := 0; i < interpretBreakerThreshold+4; i++ {
		d.interpretOne(context.Background(), id)
	}
	if *calls != interpretBreakerThreshold {
		t.Fatalf("interpreter ran %d times, want it to stop after %d", *calls, interpretBreakerThreshold)
	}
	// The queue is gated too, so a tripped interpreter also stops paying for
	// the per-candidate pane capture interpretOne would otherwise run.
	if d.maybeQueueInterpret(id) {
		t.Fatal("a tripped breaker must stop queueing")
	}
	if n := strings.Count(logs.String(), "interpreter DISABLED"); n != 1 {
		t.Fatalf("trip logged %d times, want exactly 1:\n%s", n, logs.String())
	}

	rep, ok := doctor.StatusAgentBreakerReport()
	if !ok {
		t.Fatal("no doctor breadcrumb for a tripped breaker")
	}
	if rep.Failures != interpretBreakerThreshold || !strings.Contains(rep.Reason, "updated terms") {
		t.Fatalf("breadcrumb = %+v, want the failure count and the reason", rep)
	}
	if rep.RetryAt.Sub(rep.TrippedAt) != interpretBreakerCooldown {
		t.Fatalf("retry %s after the trip, want the cooldown %s", rep.RetryAt.Sub(rep.TrippedAt), interpretBreakerCooldown)
	}
}

// Any success closes the breaker and zeroes the count, so a flapping
// interpreter never accumulates its way to disabled.
func TestInterpretBreakerSuccessResets(t *testing.T) {
	d, id, calls, _ := breakerDaemon(t)
	fail := d.interpretSeam

	for i := 0; i < interpretBreakerThreshold-1; i++ {
		d.interpretOne(context.Background(), id)
	}
	d.interpretSeam = func(ctx context.Context, contextText string) (string, error) {
		*calls++
		return `{"agent_state":"working","headline":"running tests","confidence":0.9}`, nil
	}
	d.interpretOne(context.Background(), id)
	d.interpretSeam = fail
	for i := 0; i < interpretBreakerThreshold-1; i++ {
		d.interpretOne(context.Background(), id)
	}

	want := 2*(interpretBreakerThreshold-1) + 1
	if *calls != want {
		t.Fatalf("%d calls, want %d — a success must reset the consecutive count", *calls, want)
	}
	if !d.maybeQueueInterpret(id) {
		t.Fatal("breaker still refusing work after a success")
	}
}

// After the cooldown the breaker goes half-open: exactly one probe. A failing
// probe re-trips (silently — the operator was told once) and restarts the
// wait; a passing one re-enables the feature and retires the doctor row.
func TestInterpretBreakerHalfOpenProbe(t *testing.T) {
	d, id, calls, logs := breakerDaemon(t)
	for i := 0; i < interpretBreakerThreshold; i++ {
		d.interpretOne(context.Background(), id)
	}
	tripCalls := *calls

	// Still cooling: nothing runs.
	d.interpretOne(context.Background(), id)
	if *calls != tripCalls {
		t.Fatalf("ran during the cooldown: %d calls, want %d", *calls, tripCalls)
	}

	// Cooldown elapsed: ONE probe, which fails and re-trips.
	rewindBreaker(d)
	d.interpretOne(context.Background(), id)
	if *calls != tripCalls+1 {
		t.Fatalf("half-open probe: %d calls, want %d", *calls, tripCalls+1)
	}
	d.interpretOne(context.Background(), id)
	if *calls != tripCalls+1 {
		t.Fatalf("a failed probe must re-trip: %d calls, want %d", *calls, tripCalls+1)
	}
	if n := strings.Count(logs.String(), "interpreter DISABLED"); n != 1 {
		t.Fatalf("re-trip logged again (%d total) — the trip is announced once", n)
	}

	// Cooldown elapsed again, and this time the interpreter answers.
	rewindBreaker(d)
	d.interpretSeam = func(ctx context.Context, contextText string) (string, error) {
		*calls++
		return `{"agent_state":"working","headline":"running tests","confidence":0.9}`, nil
	}
	d.interpretOne(context.Background(), id)
	if *calls != tripCalls+2 {
		t.Fatalf("recovery probe: %d calls, want %d", *calls, tripCalls+2)
	}
	if !d.maybeQueueInterpret(id) {
		t.Fatal("a successful probe must close the breaker")
	}
	if !strings.Contains(logs.String(), "re-enabled") {
		t.Fatalf("recovery not logged:\n%s", logs.String())
	}
	if _, ok := doctor.StatusAgentBreakerReport(); ok {
		t.Fatal("recovery must retire the doctor breadcrumb")
	}
}

// The half-open window buys exactly ONE call, not one per queued session, and
// a closed breaker never blocks. Unit-level, because the daemon path resolves
// each probe before it returns.
func TestInterpretBreakerStateMachine(t *testing.T) {
	b := &interpretBreaker{}
	now := time.Now()

	if b.blocked(now) || !b.begin(now) {
		t.Fatal("a closed breaker must allow work")
	}
	for i := 0; i < interpretBreakerThreshold-1; i++ {
		if rep, tripped := b.fail("boom", now); rep != nil || tripped {
			t.Fatalf("failure %d tripped early", i+1)
		}
	}
	rep, tripped := b.fail("boom", now)
	if !tripped || rep == nil || rep.Failures != interpretBreakerThreshold {
		t.Fatalf("failure %d did not trip: rep=%+v tripped=%v", interpretBreakerThreshold, rep, tripped)
	}
	if !b.blocked(now) || b.begin(now) {
		t.Fatal("a tripped breaker must refuse work")
	}

	after := now.Add(interpretBreakerCooldown + time.Second)
	if b.blocked(after) {
		t.Fatal("the cooldown must expire")
	}
	if !b.begin(after) {
		t.Fatal("the cooldown must buy one probe")
	}
	if !b.blocked(after) || b.begin(after) {
		t.Fatal("the probe is exclusive: nothing else may run beside it")
	}
	// A probe failure re-trips but is NOT announced again.
	if rep, tripped := b.fail("boom again", after); tripped || rep == nil {
		t.Fatalf("re-trip: rep=%+v tripped=%v, want a report and no new announcement", rep, tripped)
	}
	if !b.blocked(after) {
		t.Fatal("a failed probe must restart the cooldown")
	}

	if !b.succeed() {
		t.Fatal("succeed must report the recovery")
	}
	if b.blocked(after) || !b.begin(after) {
		t.Fatal("a success must close the breaker")
	}
	if b.succeed() {
		t.Fatal("a success on a closed breaker is not a recovery")
	}
}

// A restart or a reload re-arms the breaker — it is process state, never
// config: lola must not turn the user's feature off on their behalf.
func TestInterpretBreakerRearmsOnReload(t *testing.T) {
	d, id, _, _ := breakerDaemon(t)
	for i := 0; i < interpretBreakerThreshold; i++ {
		d.interpretOne(context.Background(), id)
	}
	if _, ok := doctor.StatusAgentBreakerReport(); !ok {
		t.Fatal("breaker did not trip")
	}

	// What handleReload does: rebuild the interpreter from the (here disabled)
	// table. Both paths through setStatusAgentLocked re-arm.
	d.mu.Lock()
	d.setStatusAgentLocked(config.StatusAgentConfig{})
	d.mu.Unlock()

	if d.interpretBreakerFor().blocked(time.Now()) {
		t.Fatal("a reload must re-arm the breaker")
	}
	if _, ok := doctor.StatusAgentBreakerReport(); ok {
		t.Fatal("a reload must retire the doctor breadcrumb")
	}
}

// Unparsable output counts as a failure — the call cost the same and produced
// the same nothing — while a legitimate "unknown" judgement does not.
func TestInterpretBreakerCountsUnparsableButNotUnknown(t *testing.T) {
	d, id, calls, _ := breakerDaemon(t)
	d.interpretSeam = func(ctx context.Context, contextText string) (string, error) {
		*calls++
		return "[ACTION REQUIRED] please run claude to accept the terms", nil
	}
	for i := 0; i < interpretBreakerThreshold+2; i++ {
		d.interpretOne(context.Background(), id)
	}
	if *calls != interpretBreakerThreshold {
		t.Fatalf("garbage output ran %d times, want the breaker to stop it at %d", *calls, interpretBreakerThreshold)
	}

	d2, id2, calls2, _ := breakerDaemon(t)
	d2.interpretSeam = func(ctx context.Context, contextText string) (string, error) {
		*calls2++
		return `{"agent_state":"unknown","headline":"","waiting_on":"","confidence":0}`, nil
	}
	for i := 0; i < interpretBreakerThreshold+2; i++ {
		d2.interpretOne(context.Background(), id2)
	}
	if *calls2 != interpretBreakerThreshold+2 {
		t.Fatalf(`"unknown" ran %d times — it is the interpreter working, not failing`, *calls2)
	}
}

// The retained reason is one safe, clipped line: it is rendered CLI output and
// ends up in a log line and a doctor row.
func TestSanitizeInterpretReason(t *testing.T) {
	got := sanitizeInterpretReason(errors.New("exit 1:\n\x1b[31mfatal\x1b[0m\r  accept\tthe terms\x07"))
	if got != "exit 1: fatal accept the terms" {
		t.Fatalf("sanitized to %q", got)
	}
	long := sanitizeInterpretReason(errors.New(strings.Repeat("x", interpretBreakerReasonRunes*3)))
	if r := []rune(long); len(r) != interpretBreakerReasonRunes || r[len(r)-1] != '…' {
		t.Fatalf("clip: %d runes, want %d ending in an ellipsis", len(r), interpretBreakerReasonRunes)
	}
	if sanitizeInterpretReason(nil) != "" {
		t.Fatal("a nil error must sanitize to nothing")
	}
}
