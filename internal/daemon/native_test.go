package daemon

// Tests for the native-runtime wiring (PLAN P2): the per-poll dispatch
// switch, the cross-runtime cap math, hookEvent state transitions, the
// observer's native merge, adoption, and the native orphan reconcile.

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sushidev-team/lola/internal/ao"
	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/linear"
	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/runtime"
	"github.com/sushidev-team/lola/internal/scm"
	"github.com/sushidev-team/lola/internal/session"
)

// fakeNative is a hermetic NativeAPI: no git, tmux, or claude is ever executed.
type fakeNative struct {
	mu            sync.Mutex
	spawnErr      error
	spawns        []nativeSpawnCall
	spawnDeadline bool                                    // last Spawn ctx carried a deadline
	onSpawn       func(p config.Project, is linear.Issue) // runs before spawnErr is returned
	adopted       []session.Session
	adoptErr      error
	alive         map[string]bool // session ID -> tmux pane alive
	kills         []string
}

type nativeSpawnCall struct{ project, identifier string }

var _ NativeAPI = (*fakeNative)(nil)

func (f *fakeNative) Spawn(ctx context.Context, p config.Project, is linear.Issue) (session.Session, error) {
	f.mu.Lock()
	f.spawns = append(f.spawns, nativeSpawnCall{p.Name, is.Identifier})
	_, f.spawnDeadline = ctx.Deadline()
	hook, err := f.onSpawn, f.spawnErr
	f.mu.Unlock()
	if hook != nil {
		hook(p, is)
	}
	if err != nil {
		return session.Session{}, err
	}
	id := runtime.SessionID(p.Name, is.Identifier)
	return session.Session{
		ID:        id,
		Source:    "native",
		Project:   p.Name,
		Issue:     is.Identifier,
		IssueUUID: is.ID,
		Branch:    "lola/" + strings.ToLower(is.Identifier),
		Repo:      p.Repo,
		TmuxName:  id,
		Status:    runtime.StatusWorking,
	}, nil
}

func (f *fakeNative) Adopt(ctx context.Context) ([]session.Session, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.adoptErr != nil {
		return nil, f.adoptErr
	}
	return slices.Clone(f.adopted), nil
}

func (f *fakeNative) Kill(ctx context.Context, s session.Session, removeWorktree bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.kills = append(f.kills, s.ID)
	return nil
}

func (f *fakeNative) Alive(ctx context.Context, s session.Session) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.alive[s.ID]
}

func (f *fakeNative) spawnCalls() []nativeSpawnCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.spawns)
}

// nativePoll is labelPoll switched to the native runtime.
func nativePoll(name string) config.Poll {
	p := labelPoll(name)
	p.Runtime = config.RuntimeNative
	p.Project = "proj1"
	p.AOProject = ""
	return p
}

func nativeTestConfig(polls ...config.Poll) *config.Config {
	cfg := testConfig(polls...)
	cfg.Projects = []config.Project{{
		Name:          "proj1",
		Path:          "/tmp/proj1",
		Repo:          "acme/widgets",
		DefaultBranch: "main",
	}}
	return cfg
}

// nativeSess is a store-shaped native session record for seeding tests.
func nativeSess(ident, status string) session.Session {
	id := runtime.SessionID("proj1", ident)
	return session.Session{
		ID:       id,
		Source:   "native",
		Project:  "proj1",
		Issue:    ident,
		Branch:   "lola/" + strings.ToLower(ident),
		Repo:     "acme/widgets",
		TmuxName: id,
		Status:   status,
	}
}

// --- Dispatch switch --------------------------------------------------------

func TestTickNativePollSpawnsViaNativeRuntime(t *testing.T) {
	is := testIssue("FE-7", 1, "2024-01-01T00:00:00Z")
	fake := &linear.Fake{
		Issues:          []linear.Issue{is},
		LabelIDsByIssue: map[string][]string{is.ID: {"lbl-trigger"}},
	}
	// AO is DOWN: a native poll must dispatch anyway — that independence is
	// the whole point of the native runtime.
	aoc := &fakeAO{unreachable: true}
	nat := &fakeNative{}
	d := newTestDaemon(t, nativeTestConfig(nativePoll("p1")), fake, aoc)
	d.native = nat

	// Same ordering discipline as the AO path: in-flight claimed and seen
	// persisted BEFORE Spawn, labels untouched until after success. The full
	// resolved [[project]] must reach the runtime.
	nat.onSpawn = func(p config.Project, _ linear.Issue) {
		if !d.inflight.Has(is.ID) {
			t.Error("in-flight must be marked before native Spawn")
		}
		data, err := os.ReadFile(seenPath(d, "p1"))
		if err != nil || !strings.Contains(string(data), is.ID) {
			t.Errorf("seen must be persisted before native Spawn (err=%v, data=%s)", err, data)
		}
		names := fake.CallNames()
		if slices.Contains(names, "IssueLabelIDs") || slices.Contains(names, "SetIssueLabels") {
			t.Errorf("label calls must happen only after spawn success, already saw %v", names)
		}
		if p.Name != "proj1" || p.Repo != "acme/widgets" || p.Path != "/tmp/proj1" {
			t.Errorf("resolved project = %+v, want the full [[project]] proj1", p)
		}
	}

	res, err := d.tick(context.Background(), "p1", false)
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	if m := findMatch(t, res, "FE-7"); m.Action != "spawned" {
		t.Fatalf("match = %+v, want spawned", m)
	}
	if got := nat.spawnCalls(); len(got) != 1 || got[0] != (nativeSpawnCall{"proj1", "FE-7"}) {
		t.Errorf("native spawns = %+v, want [{proj1 FE-7}]", got)
	}
	if got := aoc.spawnCalls(); len(got) != 0 {
		t.Errorf("native poll must never call ao spawn, got %v", got)
	}

	// Same label flip as the AO path after a confirmed spawn.
	if got, want := fake.LabelIDsByIssue[is.ID], []string{"lbl-sent"}; !reflect.DeepEqual(got, want) {
		t.Errorf("labels after native spawn = %v, want %v", got, want)
	}

	// The spawned session is in the store immediately (it must count against
	// the very next budget) and persisted.
	id := runtime.SessionID("proj1", "FE-7")
	s, ok := d.sessions.Get(id)
	if !ok {
		t.Fatalf("native session %s must be upserted into the store on spawn", id)
	}
	if s.Source != "native" || s.Status != "working" || s.Repo != "acme/widgets" {
		t.Errorf("stored session = %+v, want native/working with the project repo", s)
	}
	if _, err := os.Stat(filepath.Join(d.home, "state", "sessions.json")); err != nil {
		t.Errorf("store must be persisted after a native spawn: %v", err)
	}
}

func TestTickNativeSpawnFailureRollsBackClaim(t *testing.T) {
	is := testIssue("FE-7", 1, "2024-01-01T00:00:00Z")
	fake := &linear.Fake{
		Issues:          []linear.Issue{is},
		LabelIDsByIssue: map[string][]string{is.ID: {"lbl-trigger"}},
	}
	nat := &fakeNative{spawnErr: errors.New("worktree add failed")}
	d := newTestDaemon(t, nativeTestConfig(nativePoll("p1")), fake, &fakeAO{unreachable: true})
	d.native = nat

	res, err := d.tick(context.Background(), "p1", false)
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	if m := findMatch(t, res, "FE-7"); m.Action != "skipped" || m.Reason != "error" {
		t.Errorf("match = %+v, want skipped/error", m)
	}
	if d.inflight.Has(is.ID) {
		t.Error("failed native spawn must drop the in-flight claim")
	}
	if names := fake.CallNames(); slices.Contains(names, "SetIssueLabels") {
		t.Errorf("failed native spawn must not mutate labels, calls = %v", names)
	}
	// Label mode: the seen entry stays as the short-TTL race guard.
	seen, err := d.seen.load("p1")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := seen[is.ID]; !ok {
		t.Error("seen entry must remain after a failed native spawn (race guard)")
	}
	if _, ok := d.sessions.Get(runtime.SessionID("proj1", "FE-7")); ok {
		t.Error("failed native spawn must not upsert a session")
	}
}

func TestTickNativeUnknownProjectFailsWithoutStateMutation(t *testing.T) {
	is := testIssue("FE-7", 1, "2024-01-01T00:00:00Z")
	fake := &linear.Fake{Issues: []linear.Issue{is}}
	cfg := nativeTestConfig(nativePoll("p1"))
	cfg.Polls[0].Project = "no-such-project"
	d := newTestDaemon(t, cfg, fake, &fakeAO{})
	d.native = &fakeNative{}

	if _, err := d.tick(context.Background(), "p1", false); err == nil {
		t.Fatal("tick must fail for a native poll whose project is unknown")
	}
	if names := fake.CallNames(); len(names) != 0 {
		t.Errorf("unknown project: no linear calls, got %v", names)
	}
	if d.inflight.Has(is.ID) {
		t.Error("unknown project: in-flight must not be mutated")
	}
}

// --- Combined cap across runtimes --------------------------------------------

func TestNativeLiveCounted(t *testing.T) {
	sessions := []session.Session{
		nativeSess("FE-1", "working"),                 // counts
		nativeSess("FE-2", "needs_input"),             // counts
		nativeSess("FE-3", "ci_failed"),               // counts
		nativeSess("FE-4", "changes_requested"),       // counts
		nativeSess("FE-5", "ci_pending"),              // counts
		nativeSess("FE-11", "draft"),                  // counts: agent still iterating on its draft PR
		nativeSess("FE-6", "approved"),                // parked: no slot
		nativeSess("FE-7", "review_pending"),          // parked: no slot
		nativeSess("FE-8", "merged"),                  // done: no slot
		nativeSess("FE-9", "dead"),                    // dead: no slot
		nativeSess("FE-10", "idle"),                   // between turns: no slot
		{ID: "ao-1", Source: "ao", Status: "working"}, // wrong source: never
	}
	if got := NativeLiveCounted(sessions); got != 6 {
		t.Errorf("NativeLiveCounted = %d, want 6 ([ao].counting_states parity incl. draft)", got)
	}
	if got := NativeLiveCounted(nil); got != 0 {
		t.Errorf("NativeLiveCounted(nil) = %d, want 0", got)
	}
}

// Native sessions in the store must count against an AO-runtime poll's
// budget: the global cap spans both runtimes.
func TestTickAOPollBudgetIncludesNativeSessions(t *testing.T) {
	is := testIssue("FE-1", 1, "2024-01-01T00:00:00Z")
	fake := &linear.Fake{Issues: []linear.Issue{is}}
	aoc := &fakeAO{sessions: []ao.SessionState{{ID: "s1", Status: "working"}}} // aoCounted = 1
	cfg := nativeTestConfig(seenPoll("p1"))
	cfg.Defaults.GlobalCap = 3
	d := newTestDaemon(t, cfg, fake, aoc)
	// nativeCounted = 2 (approved is parked and must NOT count).
	d.sessions.Upsert(nativeSess("FE-90", "working"))
	d.sessions.Upsert(nativeSess("FE-91", "ci_pending"))
	d.sessions.Upsert(nativeSess("FE-92", "approved"))

	// liveCounted = 1 + 2 = 3 = globalCap -> budget 0.
	res, err := d.tick(context.Background(), "p1", false)
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	if len(aoc.spawnCalls()) != 0 {
		t.Errorf("combined cap must bind, got spawns %v", aoc.spawnCalls())
	}
	if m := findMatch(t, res, "FE-1"); m.Action != "skipped" || m.Reason != "capped" {
		t.Errorf("match = %+v, want skipped/capped", m)
	}
}

// AO sessions must count against a native poll's budget, and the counting is
// best-effort: it must not fail the tick when AO cannot answer.
func TestTickNativePollBudgetIncludesAOSessions(t *testing.T) {
	issues := []linear.Issue{
		testIssue("FE-1", 1, "2024-01-01T00:00:00Z"),
		testIssue("FE-2", 2, "2024-01-01T00:00:00Z"),
	}
	fake := &linear.Fake{
		Issues: issues,
		LabelIDsByIssue: map[string][]string{
			issues[0].ID: {"lbl-trigger"},
			issues[1].ID: {"lbl-trigger"},
		},
	}
	aoc := &fakeAO{sessions: []ao.SessionState{
		{ID: "s1", Status: "working"},
		{ID: "s2", Status: "in_progress"},
		{ID: "s3", Status: "review"}, // not a counting state
	}}
	nat := &fakeNative{}
	cfg := nativeTestConfig(nativePoll("p1"))
	cfg.Defaults.GlobalCap = 3
	d := newTestDaemon(t, cfg, fake, aoc)
	d.native = nat

	// liveCounted = aoCounted(2) + native(0) -> budget = min(10, 3-2) = 1.
	res, err := d.tick(context.Background(), "p1", false)
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	spawns := nat.spawnCalls()
	if len(spawns) != 1 || spawns[0].identifier != "FE-1" {
		t.Fatalf("native spawns = %+v, want exactly [{proj1 FE-1}] (budget 1, priority order)", spawns)
	}
	if m := findMatch(t, res, "FE-2"); m.Action != "skipped" || m.Reason != "capped" {
		t.Errorf("FE-2 match = %+v, want skipped/capped", m)
	}
}

// Ticks run cancel-shielded (safeTick) under the poll's tick mutex, so every
// exec a native tick performs must carve its own deadline: the whole native
// spawn (it runs arbitrary post_create commands) and the best-effort AO
// probes for the cross-runtime budget.
func TestTickNativeSpawnAndAOProbesAreDeadlineBounded(t *testing.T) {
	is := testIssue("FE-7", 1, "2024-01-01T00:00:00Z")
	fake := &linear.Fake{
		Issues:          []linear.Issue{is},
		LabelIDsByIssue: map[string][]string{is.ID: {"lbl-trigger"}},
	}
	var reachDL, sessDL bool
	aoc := deadlineAO{fakeAO: &fakeAO{}, reachableDL: &reachDL, sessionsDL: &sessDL}
	nat := &fakeNative{}
	d := newTestDaemon(t, nativeTestConfig(nativePoll("p1")), fake, aoc)
	d.native = nat

	// context.Background has no deadline; every deadline observed below was
	// added by the tick itself.
	if _, err := d.tick(context.Background(), "p1", false); err != nil {
		t.Fatalf("tick: %v", err)
	}
	nat.mu.Lock()
	spawnDL := nat.spawnDeadline
	nat.mu.Unlock()
	if !spawnDL {
		t.Error("native Spawn must run under a deadline (post_create runs arbitrary user commands)")
	}
	if !reachDL {
		t.Error("budget AO Reachable probe must run under a per-exec deadline")
	}
	if !sessDL {
		t.Error("budget AO LiveSessions probe must run under a per-exec deadline")
	}
}

// --- hookEvent ---------------------------------------------------------------

func TestHandleHookEventStatusTransitions(t *testing.T) {
	cases := []struct {
		event, before, after string
	}{
		{"stop", "working", "idle"},
		{"notification", "working", "needs_input"},
		{"session_end", "working", "session_ended"},
		{"tool_use", "idle", "working"}, // heartbeat promotes idle back to working
	}
	for _, c := range cases {
		t.Run(c.event, func(t *testing.T) {
			d := newTestDaemon(t, nativeTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeAO{})
			s := nativeSess("FE-1", c.before)
			d.sessions.Upsert(s)

			resp := d.handle(context.Background(), protocol.Request{Cmd: "hookEvent", Session: s.ID, Event: c.event})
			if !resp.OK {
				t.Fatalf("hookEvent must always be OK, got %+v", resp)
			}
			got, ok := d.sessions.Get(s.ID)
			if !ok || got.Status != c.after {
				t.Errorf("status after %s = %q (ok=%v), want %q", c.event, got.Status, ok, c.after)
			}
		})
	}
}

func TestHandleHookEventToolUseTouchesLastSeenOnly(t *testing.T) {
	// Seed the on-disk store with an old LastSeen before the daemon (and its
	// store) is constructed, so the heartbeat's touch is observable.
	home := t.TempDir()
	t.Setenv("LOLA_HOME", home)
	old := time.Now().Add(-2 * time.Hour).Format(time.RFC3339)
	stateDir := filepath.Join(home, "state")
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	blob := `[{"id":"lola-proj1-fe-1","source":"native","project":"proj1","issue":"FE-1",` +
		`"status":"needs_input","first_seen":"` + old + `","last_seen":"` + old + `"}]`
	if err := os.WriteFile(filepath.Join(stateDir, "sessions.json"), []byte(blob), 0o600); err != nil {
		t.Fatal(err)
	}
	d := newDaemon(nativeTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeAO{}, log.New(io.Discard, "", 0), home)

	resp := d.handleHookEvent(protocol.Request{Cmd: "hookEvent", Session: "lola-proj1-fe-1", Event: "tool_use"})
	if !resp.OK {
		t.Fatalf("tool_use must be OK, got %+v", resp)
	}
	s, ok := d.sessions.Get("lola-proj1-fe-1")
	if !ok {
		t.Fatal("session vanished")
	}
	if s.Status != "needs_input" {
		t.Errorf("tool_use must not change a non-idle status, got %q", s.Status)
	}
	if time.Since(s.LastSeen) > time.Minute {
		t.Errorf("tool_use must touch LastSeen, still %v", s.LastSeen)
	}
}

func TestHandleHookEventUnknownSessionOKAndLoggedOnce(t *testing.T) {
	home := t.TempDir()
	t.Setenv("LOLA_HOME", home)
	var buf bytes.Buffer
	d := newDaemon(nativeTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeAO{}, log.New(&buf, "", 0), home)

	for i := 0; i < 3; i++ {
		resp := d.handle(context.Background(), protocol.Request{Cmd: "hookEvent", Session: "ghost-1", Event: "stop"})
		if !resp.OK {
			t.Fatalf("unknown session must still be acknowledged OK (agent must never error), got %+v", resp)
		}
	}
	if got := strings.Count(buf.String(), `unknown session "ghost-1"`); got != 1 {
		t.Errorf("unknown session must be logged exactly once per ID, got %d in:\n%s", got, buf.String())
	}
	// A different unknown ID gets its own single line.
	d.handle(context.Background(), protocol.Request{Cmd: "hookEvent", Session: "ghost-2", Event: "tool_use"})
	if got := strings.Count(buf.String(), `unknown session "ghost-2"`); got != 1 {
		t.Errorf("second unknown ID must be logged once, got %d", got)
	}
}

// --- Observer: native merge ---------------------------------------------------

func TestObserveNativeMergesPRStateAndTmuxName(t *testing.T) {
	// AO down: the native half of the cycle must still run.
	d := newTestDaemon(t, nativeTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeAO{unreachable: true})
	s := nativeSess("FE-1", "idle")
	s.TmuxName = "" // adopted records may lack it; the observer must fill it
	d.sessions.Upsert(s)
	nat := &fakeNative{alive: map[string]bool{s.ID: true}}
	d.native = nat
	seams := &fakeObsSeams{pr: &scm.PR{Number: 3, URL: "u", State: "OPEN", ChecksState: "fail"}}
	seams.install(d)

	d.observe(context.Background())

	got := findSession(t, d.sessions.Snapshot(), s.ID)
	if got.Status != "ci_failed" {
		t.Errorf("status = %q, want ci_failed (PR facts promote the hook-driven idle)", got.Status)
	}
	if got.PR == nil || got.PR.Number != 3 {
		t.Errorf("PR = %+v, want number 3", got.PR)
	}
	if got.TmuxName != s.ID {
		t.Errorf("tmuxName = %q, want the session ID %q (native sessions ARE tmux sessions)", got.TmuxName, s.ID)
	}
	if pr, _ := seams.counts(); pr != 1 {
		t.Errorf("PR lookups = %d, want 1 (session repo + branch)", pr)
	}
	if _, err := os.Stat(filepath.Join(d.home, "state", "sessions.json")); err != nil {
		t.Errorf("native merge must persist the store even with AO down: %v", err)
	}
}

func TestObserveNativeDeadPaneBecomesDead(t *testing.T) {
	d := newTestDaemon(t, nativeTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeAO{unreachable: true})
	s := nativeSess("FE-1", "working")
	d.sessions.Upsert(s)
	d.native = &fakeNative{alive: map[string]bool{}} // pane gone
	(&fakeObsSeams{}).install(d)                     // authoritative "no PR"

	d.observe(context.Background())

	got := findSession(t, d.sessions.Snapshot(), s.ID)
	if got.Status != "dead" {
		t.Fatalf("status = %q, want dead (pane gone, PR not merged)", got.Status)
	}

	// A settled dead record is not re-upserted: LastSeen freezes so the
	// retention prune eventually drops it.
	before := got.LastSeen
	time.Sleep(5 * time.Millisecond)
	d.observe(context.Background())
	after := findSession(t, d.sessions.Snapshot(), s.ID).LastSeen
	if !after.Equal(before) {
		t.Errorf("settled dead session must not be re-upserted (LastSeen %v -> %v)", before, after)
	}
}

func TestObserveNativeDeadPaneWithMergedPRIsMerged(t *testing.T) {
	d := newTestDaemon(t, nativeTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeAO{unreachable: true})
	s := nativeSess("FE-1", "idle")
	d.sessions.Upsert(s)
	d.native = &fakeNative{alive: map[string]bool{}}
	seams := &fakeObsSeams{pr: &scm.PR{Number: 3, State: "MERGED"}}
	seams.install(d)

	d.observe(context.Background())

	if got := findSession(t, d.sessions.Snapshot(), s.ID); got.Status != "merged" {
		t.Errorf("status = %q, want merged (a merged PR outranks the dead pane)", got.Status)
	}
}

func TestObserveNativeNeedsInputOutranksPRStatus(t *testing.T) {
	d := newTestDaemon(t, nativeTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeAO{unreachable: true})
	s := nativeSess("FE-1", "needs_input")
	d.sessions.Upsert(s)
	d.native = &fakeNative{alive: map[string]bool{s.ID: true}}
	seams := &fakeObsSeams{pr: &scm.PR{Number: 3, State: "OPEN", ChecksState: "pending"}}
	seams.install(d)

	d.observe(context.Background())

	if got := findSession(t, d.sessions.Snapshot(), s.ID); got.Status != "needs_input" {
		t.Errorf("status = %q, want needs_input to outrank ci_pending while the pane is alive", got.Status)
	}
}

// A hook event landing WHILE the observer is mid-cycle (between its snapshot
// and its write — the PR check alone can take seconds) must never be erased
// by the observer's write. This matters permanently: an agent blocked on a
// permission prompt fires no further hooks, so a clobbered needs_input would
// read "working" forever (and wrongly hold a cap slot in the reverse case).
func TestObserveNativePreservesConcurrentHookNeedsInput(t *testing.T) {
	d := newTestDaemon(t, nativeTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeAO{unreachable: true})
	s := nativeSess("FE-1", "working")
	d.sessions.Upsert(s)
	d.native = &fakeNative{alive: map[string]bool{s.ID: true}}
	// The PR-check seam doubles as the interleave point: the notification
	// hook fires while the observer is "inside" its gh exec, i.e. after the
	// cycle's snapshot was taken.
	d.prForBranch = func(ctx context.Context, repo, branch string) (*scm.PR, error) {
		resp := d.handleHookEvent(protocol.Request{Cmd: "hookEvent", Session: s.ID, Event: "notification"})
		if !resp.OK {
			t.Errorf("hookEvent mid-cycle failed: %+v", resp)
		}
		return &scm.PR{Number: 3, State: "OPEN", ChecksState: "pending"}, nil
	}

	d.observe(context.Background())

	got := findSession(t, d.sessions.Snapshot(), s.ID)
	if got.Status != "needs_input" {
		t.Errorf("status = %q, want needs_input (hook transition must survive the observer's merge)", got.Status)
	}
	if got.PR == nil || got.PR.Number != 3 {
		t.Errorf("PR = %+v, want the cycle's fresh PR facts merged in", got.PR)
	}
}

func TestObserveNativeRepoFallsBackToProjectRegistry(t *testing.T) {
	d := newTestDaemon(t, nativeTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeAO{unreachable: true})
	s := nativeSess("FE-1", "working")
	s.Repo = "" // e.g. adopted before the repo was ever recorded
	d.sessions.Upsert(s)
	d.native = &fakeNative{alive: map[string]bool{s.ID: true}}
	seams := &fakeObsSeams{pr: &scm.PR{Number: 9, State: "OPEN", ChecksState: "pass"}}
	seams.install(d)

	d.observe(context.Background())

	seams.mu.Lock()
	calls := slices.Clone(seams.prCalls)
	seams.mu.Unlock()
	want := "acme/widgets|" + s.Branch
	if len(calls) != 1 || calls[0] != want {
		t.Errorf("PR lookup calls = %v, want [%s] (repo from config.Project.Repo)", calls, want)
	}
	if got := findSession(t, d.sessions.Snapshot(), s.ID); got.Repo != "acme/widgets" {
		t.Errorf("repo = %q, want backfilled acme/widgets", got.Repo)
	}
}

// --- sessions reply: source + worktree ----------------------------------------

// The TUI's source badge and worktree line render straight from the sessions
// reply — the daemon must actually map Session.Source and derive the native
// worktree path (<home>/worktrees/<project>/<id>), or every session reads
// [ao] with no worktree.
func TestSessionsDataIncludesSourceAndWorktree(t *testing.T) {
	d := newTestDaemon(t, nativeTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeAO{})
	native := nativeSess("FE-1", "working")
	d.sessions.Upsert(native)
	d.sessions.Upsert(session.Session{ID: "ao-1", Source: "ao", Project: "proj", Issue: "FE-9", Status: "working"})

	data := d.sessionsData()
	byID := map[string]protocol.SessionInfo{}
	for _, si := range data.Sessions {
		byID[si.ID] = si
	}
	n, ok := byID[native.ID]
	if !ok {
		t.Fatalf("native session missing from reply: %+v", data.Sessions)
	}
	if n.Source != "native" {
		t.Errorf("native source = %q, want native", n.Source)
	}
	if want := filepath.Join(d.home, "worktrees", "proj1", native.ID); n.Worktree != want {
		t.Errorf("native worktree = %q, want %q", n.Worktree, want)
	}
	a, ok := byID["ao-1"]
	if !ok {
		t.Fatalf("ao session missing from reply: %+v", data.Sessions)
	}
	if a.Source != "ao" || a.Worktree != "" {
		t.Errorf("ao session = source %q worktree %q, want ao / empty", a.Source, a.Worktree)
	}
}

// --- Adoption -------------------------------------------------------------------

func TestAdoptNativeSessionsUpsertsAndPreservesFacts(t *testing.T) {
	d := newTestDaemon(t, nativeTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeAO{})
	// A previous daemon persisted branch/repo facts adopt cannot observe.
	prev := nativeSess("FE-1", "working")
	prev.Branch = "feat/fe-1-custom"
	d.sessions.Upsert(prev)

	adopted := []session.Session{
		{ID: prev.ID, Source: "native", Project: "proj1", Issue: "FE-1", TmuxName: prev.ID, Status: "working"},
		{ID: "lola-proj1-fe-2", Source: "native", Project: "proj1", Issue: "FE-2", Status: "dead"},
		{ID: "lola-proj1-fe-3", Source: "native", Project: "proj1", Issue: "FE-3", Status: "orphaned"},
	}
	d.native = &fakeNative{adopted: adopted}

	d.adoptNativeSessions(context.Background())

	s1 := findSession(t, d.sessions.Snapshot(), prev.ID)
	if s1.Status != "working" || s1.Branch != "feat/fe-1-custom" || s1.Repo != "acme/widgets" {
		t.Errorf("re-adopted session = %+v, want working with persisted branch/repo preserved", s1)
	}
	if s2 := findSession(t, d.sessions.Snapshot(), "lola-proj1-fe-2"); s2.Status != "dead" || s2.TmuxName != s2.ID {
		t.Errorf("dead candidate = %+v, want status dead and TmuxName = ID", s2)
	}
	if s3 := findSession(t, d.sessions.Snapshot(), "lola-proj1-fe-3"); s3.Status != "orphaned" {
		t.Errorf("orphan candidate = %+v, want status orphaned", s3)
	}
	if _, err := os.Stat(filepath.Join(d.home, "state", "sessions.json")); err != nil {
		t.Errorf("adoption must persist the store: %v", err)
	}
}

func TestAdoptNativeSessionsLogsAnomaliesAndScanFailure(t *testing.T) {
	home := t.TempDir()
	t.Setenv("LOLA_HOME", home)
	var buf bytes.Buffer
	d := newDaemon(nativeTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeAO{}, log.New(&buf, "", 0), home)
	d.native = &fakeNative{adopted: []session.Session{
		{ID: "lola-proj1-fe-2", Source: "native", Project: "proj1", Issue: "FE-2", Status: "dead"},
		{ID: "lola-proj1-fe-3", Source: "native", Project: "proj1", Status: "orphaned"},
	}}
	d.adoptNativeSessions(context.Background())
	for _, want := range []string{"lola-proj1-fe-2", "dead", "lola-proj1-fe-3", "orphaned"} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("adoption anomaly log must mention %q, got:\n%s", want, buf.String())
		}
	}

	// A failing scan is logged, never fatal, and upserts nothing.
	buf.Reset()
	d2 := newDaemon(nativeTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeAO{}, log.New(&buf, "", 0), t.TempDir())
	d2.native = &fakeNative{adoptErr: errors.New("tmux exploded")}
	d2.adoptNativeSessions(context.Background())
	if !strings.Contains(buf.String(), "tmux exploded") {
		t.Errorf("adopt failure must be logged, got:\n%s", buf.String())
	}
	if n := len(d2.sessions.Snapshot()); n != 0 {
		t.Errorf("failed adopt must not upsert, store has %d session(s)", n)
	}
}

// --- Reconcile: native orphans ----------------------------------------------------

// A dead native session (pane gone, PR never opened) is the native orphan:
// after orphan_timeout its labels revert and seen clears via the existing
// flow, the worktree stays on disk, and its path is logged for inspection.
// AO being down must not block any of it.
func TestReconcileNativeDeadSessionRevertsAndKeepsWorktree(t *testing.T) {
	is := testIssue("FE-231", 1, "2024-01-01T00:00:00Z")
	fake := &linear.Fake{
		Issues:          []linear.Issue{is},
		LabelIDsByIssue: map[string][]string{is.ID: {"lbl-sent", "lbl-other"}},
	}
	home := t.TempDir()
	t.Setenv("LOLA_HOME", home)
	var buf bytes.Buffer
	d := newDaemon(nativeTestConfig(nativePoll("p1")), fake, &fakeAO{unreachable: true}, log.New(&buf, "", 0), home)

	dead := nativeSess("FE-231", "dead")
	d.sessions.Upsert(dead)
	if err := d.seen.save("p1", map[string]time.Time{is.ID: time.Now().Add(-2 * orphanTimeout)}); err != nil {
		t.Fatal(err)
	}
	d.inflight.Add(is.ID, is.Identifier)
	var gotRepo, gotBranch string
	d.openPR = func(_ context.Context, repo, branch string) (bool, error) {
		gotRepo, gotBranch = repo, branch
		return false, nil // provably no PR
	}

	d.reconcile(context.Background())

	// The PR check used the session record's branch and the project's repo.
	if gotRepo != "acme/widgets" || gotBranch != dead.Branch {
		t.Errorf("openPR got (%q, %q), want (acme/widgets, %q)", gotRepo, gotBranch, dead.Branch)
	}
	want := []string{"lbl-other", "lbl-trigger"}
	if got := fake.LabelIDsByIssue[is.ID]; !reflect.DeepEqual(got, want) {
		t.Errorf("labels after revert = %v, want %v", got, want)
	}
	seen, err := d.seen.load("p1")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := seen[is.ID]; ok {
		t.Error("reverted native orphan must be cleared from seen")
	}
	if d.inflight.Has(is.ID) {
		t.Error("reverted native orphan must be cleared from in-flight")
	}
	wantPath := filepath.Join(home, "worktrees", "proj1", dead.ID)
	if !strings.Contains(buf.String(), wantPath) {
		t.Errorf("log must name the kept worktree %s, got:\n%s", wantPath, buf.String())
	}
}

// A native session whose pane is still alive shields its issue from the
// revert regardless of derived status — even parked ones that hold no slot.
func TestReconcileNativeAliveSessionBlocksRevert(t *testing.T) {
	for _, status := range []string{"working", "idle", "needs_input", "approved", "review_pending"} {
		is := testIssue("FE-231", 1, "2024-01-01T00:00:00Z")
		fake := &linear.Fake{
			Issues:          []linear.Issue{is},
			LabelIDsByIssue: map[string][]string{is.ID: {"lbl-sent"}},
		}
		d := newTestDaemon(t, nativeTestConfig(nativePoll("p1")), fake, &fakeAO{unreachable: true})
		d.sessions.Upsert(nativeSess("FE-231", status))
		if err := d.seen.save("p1", map[string]time.Time{is.ID: time.Now().Add(-2 * orphanTimeout)}); err != nil {
			t.Fatal(err)
		}
		d.openPR = func(context.Context, string, string) (bool, error) { return false, nil }

		d.reconcile(context.Background())

		if slices.Contains(fake.CallNames(), "SetIssueLabels") {
			t.Errorf("status %q: a present native session must block the revert", status)
		}
	}
}

// In a native-only deployment AO is expected to be unreachable forever; the
// in-flight cleanup must still run off the native session facts, or a
// successfully spawned issue's claim would survive for the daemon's lifetime
// and block any later re-dispatch ("in-flight" on every tick).
func TestReconcileNativeOnlyDeploymentClearsStaleInflightWithAODown(t *testing.T) {
	is := testIssue("FE-1", 1, "2024-01-01T00:00:00Z")
	d := newTestDaemon(t, nativeTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeAO{unreachable: true})
	// The session finished long ago: only a settled "dead" record remains,
	// which is not counted. Backdate the claim past the orphan grace.
	d.sessions.Upsert(nativeSess("FE-1", "dead"))
	d.inflight.Add(is.ID, is.Identifier)
	d.inflight.mu.Lock()
	d.inflight.m[is.ID] = inflightEntry{Identifier: is.Identifier, AddedAt: time.Now().Add(-2 * orphanTimeout)}
	d.inflight.mu.Unlock()

	d.reconcile(context.Background())

	if d.inflight.Has(is.ID) {
		t.Error("native-only deployment: stale in-flight claim must be cleared even with AO down")
	}

	// A claim whose issue still has a counted (present) native session must
	// survive the same pass.
	d2 := newTestDaemon(t, nativeTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeAO{unreachable: true})
	d2.sessions.Upsert(nativeSess("FE-2", "working"))
	is2 := testIssue("FE-2", 1, "2024-01-01T00:00:00Z")
	d2.inflight.Add(is2.ID, is2.Identifier)
	d2.inflight.mu.Lock()
	d2.inflight.m[is2.ID] = inflightEntry{Identifier: is2.Identifier, AddedAt: time.Now().Add(-2 * orphanTimeout)}
	d2.inflight.mu.Unlock()

	d2.reconcile(context.Background())

	if !d2.inflight.Has(is2.ID) {
		t.Error("claim with a counted native session must not be cleared")
	}
}

// With AO down, ao-runtime polls must be skipped (their counted picture is
// blind) while in-flight claims stay untouched.
func TestReconcileAODownSkipsAOPollsButKeepsClaims(t *testing.T) {
	is := testIssue("FE-1", 1, "2024-01-01T00:00:00Z")
	fake := &linear.Fake{
		Issues:          []linear.Issue{is},
		LabelIDsByIssue: map[string][]string{is.ID: {"lbl-sent"}},
	}
	d := newTestDaemon(t, testConfig(labelPoll("p1")), fake, &fakeAO{unreachable: true})
	if err := d.seen.save("p1", map[string]time.Time{is.ID: time.Now().Add(-2 * orphanTimeout)}); err != nil {
		t.Fatal(err)
	}
	backdated := time.Now().Add(-2 * orphanTimeout)
	d.inflight.Add(is.ID, is.Identifier)
	d.inflight.mu.Lock() // backdate so only the AO-down guard protects it
	d.inflight.m[is.ID] = inflightEntry{Identifier: is.Identifier, AddedAt: backdated}
	d.inflight.mu.Unlock()
	d.openPR = func(context.Context, string, string) (bool, error) { return false, nil }

	d.reconcile(context.Background())

	if slices.Contains(fake.CallNames(), "SetIssueLabels") {
		t.Error("AO down: ao-runtime polls must not revert anything")
	}
	if !d.inflight.Has(is.ID) {
		t.Error("AO down: in-flight claims must not be cleared (AO sessions are invisible)")
	}
}
