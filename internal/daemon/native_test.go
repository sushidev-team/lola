package daemon

// Tests for the native-runtime wiring: the dispatch path, the budget cap math,
// hookEvent state transitions, the observer's native merge, adoption, and the
// native orphan reconcile.

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
	kills         []nativeKillCall
	killErr       error // returned from Kill (e.g. worktree.ErrDirty)
	revives       []string // session IDs passed to Revive
	reviveErr     error    // returned from Revive
}

type nativeSpawnCall struct{ project, identifier string }

type nativeKillCall struct {
	id             string
	removeWorktree bool
	force          bool
}

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

func (f *fakeNative) Kill(ctx context.Context, s session.Session, removeWorktree, force bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.kills = append(f.kills, nativeKillCall{id: s.ID, removeWorktree: removeWorktree, force: force})
	return f.killErr
}

func (f *fakeNative) killCalls() []nativeKillCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.kills)
}

func (f *fakeNative) Alive(ctx context.Context, s session.Session) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.alive[s.ID]
}

func (f *fakeNative) Revive(ctx context.Context, s session.Session) (session.Session, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.revives = append(f.revives, s.ID)
	if f.reviveErr != nil {
		return session.Session{}, f.reviveErr
	}
	s.Status = runtime.StatusWorking
	s.TmuxName = s.ID
	return s, nil
}

func (f *fakeNative) reviveCalls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.revives)
}

func (f *fakeNative) spawnCalls() []nativeSpawnCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.spawns)
}

// nativePoll is a label-mode native poll referencing the "proj1" [[project]].
func nativePoll(name string) config.Poll {
	return labelPoll(name)
}

// nativeTestConfig is the shared test config (it already defines [[project]]
// "proj1"); kept as a named helper for readability at native call sites.
func nativeTestConfig(polls ...config.Poll) *config.Config {
	return testConfig(polls...)
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
	nat := &fakeNative{}
	d := newTestDaemon(t, nativeTestConfig(nativePoll("p1")), fake, nat)

	// In-flight claimed and seen persisted BEFORE Spawn, labels untouched until
	// after success. The full resolved [[project]] must reach the runtime.
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

	// Label flip after a confirmed spawn.
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
	d := newTestDaemon(t, nativeTestConfig(nativePoll("p1")), fake, nat)

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
	d := newTestDaemon(t, cfg, fake, &fakeNative{})

	if _, err := d.tick(context.Background(), "p1", false); err == nil {
		t.Fatal("tick must fail for a poll whose project is unknown")
	}
	if names := fake.CallNames(); len(names) != 0 {
		t.Errorf("unknown project: no linear calls, got %v", names)
	}
	if d.inflight.Has(is.ID) {
		t.Error("unknown project: in-flight must not be mutated")
	}
}

// --- Budget: native cap math -------------------------------------------------

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
		{ID: "ao-1", Source: "ao", Status: "working"}, // non-native source: never
	}
	if got := NativeLiveCounted(sessions); got != 6 {
		t.Errorf("NativeLiveCounted = %d, want 6 (slot-occupying states incl. draft)", got)
	}
	if got := NativeLiveCounted(nil); got != 0 {
		t.Errorf("NativeLiveCounted(nil) = %d, want 0", got)
	}
}

// Native sessions already in the store count against a poll's budget: the
// global cap is measured against the native session store, the only source.
func TestTickNativePollBudgetCountsStoreSessions(t *testing.T) {
	is := testIssue("FE-1", 1, "2024-01-01T00:00:00Z")
	fake := &linear.Fake{Issues: []linear.Issue{is}}
	nat := &fakeNative{}
	cfg := nativeTestConfig(seenPoll("p1"))
	cfg.Defaults.GlobalCap = 3
	d := newTestDaemon(t, cfg, fake, nat)
	// Three slot-occupying native sessions == globalCap -> budget 0. The parked
	// "approved" session must NOT count.
	d.sessions.Upsert(nativeSess("FE-90", "working"))
	d.sessions.Upsert(nativeSess("FE-91", "ci_pending"))
	d.sessions.Upsert(nativeSess("FE-92", "draft"))
	d.sessions.Upsert(nativeSess("FE-93", "approved")) // parked

	res, err := d.tick(context.Background(), "p1", false)
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	if len(nat.spawnCalls()) != 0 {
		t.Errorf("combined cap must bind, got spawns %v", nat.spawnCalls())
	}
	if m := findMatch(t, res, "FE-1"); m.Action != "skipped" || m.Reason != "capped" {
		t.Errorf("match = %+v, want skipped/capped", m)
	}
}

// The native spawn runs arbitrary post_create commands, so it must carve its
// own deadline even though the tick itself runs cancel-shielded (safeTick).
func TestTickNativeSpawnIsDeadlineBounded(t *testing.T) {
	is := testIssue("FE-7", 1, "2024-01-01T00:00:00Z")
	fake := &linear.Fake{
		Issues:          []linear.Issue{is},
		LabelIDsByIssue: map[string][]string{is.ID: {"lbl-trigger"}},
	}
	nat := &fakeNative{}
	d := newTestDaemon(t, nativeTestConfig(nativePoll("p1")), fake, nat)

	// context.Background has no deadline; any deadline observed below was added
	// by the tick itself.
	if _, err := d.tick(context.Background(), "p1", false); err != nil {
		t.Fatalf("tick: %v", err)
	}
	nat.mu.Lock()
	spawnDL := nat.spawnDeadline
	nat.mu.Unlock()
	if !spawnDL {
		t.Error("native Spawn must run under a deadline (post_create runs arbitrary user commands)")
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
		{"tool_use", "idle", "working"},           // heartbeat promotes idle back to working
		{"user_prompt", "idle", "working"},        // turn start on an idle agent
		{"user_prompt", "needs_input", "working"}, // human answered / nudged
	}
	for _, c := range cases {
		t.Run(c.event, func(t *testing.T) {
			d := newTestDaemon(t, nativeTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeNative{})
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

// A turn-START user_prompt must CLEAR the AtPrompt send-keys gate that a prior
// Stop set. Without it a human-initiated attach turn (whose text-only reply
// fires no PostToolUse) would leave AtPrompt stale-true and the reaction engine
// could send-keys into the mid-reply pane.
func TestHandleHookEventUserPromptClearsAtPromptGate(t *testing.T) {
	d := newTestDaemon(t, nativeTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeNative{})
	s := nativeSess("FE-1", "working")
	d.sessions.Upsert(s)

	// Agent finishes a turn: Stop sets AtPrompt=true (idle at the prompt).
	d.handleHookEvent(protocol.Request{Cmd: "hookEvent", Session: s.ID, Event: "stop"})
	if got, _ := d.sessions.Get(s.ID); !got.AtPrompt {
		t.Fatalf("stop must set AtPrompt=true, got %+v", got)
	}

	// Operator attaches and submits a nudge: turn starts, gate must close.
	resp := d.handleHookEvent(protocol.Request{Cmd: "hookEvent", Session: s.ID, Event: "user_prompt"})
	if !resp.OK {
		t.Fatalf("user_prompt must be OK, got %+v", resp)
	}
	got, _ := d.sessions.Get(s.ID)
	if got.AtPrompt {
		t.Error("user_prompt must clear AtPrompt so the reaction engine cannot send-keys mid-turn")
	}
	if got.Status != "working" {
		t.Errorf("user_prompt must promote idle → working, got %q", got.Status)
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
	d := newDaemon(nativeTestConfig(nativePoll("p1")), &linear.Fake{}, log.New(io.Discard, "", 0), home)

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
	// tool_use is POSITIVE evidence of work: it must stamp LastActivityAt (the
	// anchor for the observer's anti-false-working guard), which the seed left zero.
	if time.Since(s.LastActivityAt) > time.Minute {
		t.Errorf("tool_use must stamp LastActivityAt (positive activity), still %v", s.LastActivityAt)
	}
}

func TestHandleHookEventUnknownSessionOKAndLoggedOnce(t *testing.T) {
	home := t.TempDir()
	t.Setenv("LOLA_HOME", home)
	var buf bytes.Buffer
	d := newDaemon(nativeTestConfig(nativePoll("p1")), &linear.Fake{}, log.New(&buf, "", 0), home)

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
	s := nativeSess("FE-1", "idle")
	s.TmuxName = "" // adopted records may lack it; the observer must fill it
	nat := &fakeNative{alive: map[string]bool{s.ID: true}}
	d := newTestDaemon(t, nativeTestConfig(nativePoll("p1")), &linear.Fake{}, nat)
	d.sessions.Upsert(s)
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
		t.Errorf("native merge must persist the store: %v", err)
	}
}

func TestObserveNativeDeadPaneBecomesDead(t *testing.T) {
	s := nativeSess("FE-1", "working")
	d := newTestDaemon(t, nativeTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeNative{alive: map[string]bool{}})
	d.sessions.Upsert(s)
	(&fakeObsSeams{}).install(d) // authoritative "no PR"

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
	s := nativeSess("FE-1", "idle")
	d := newTestDaemon(t, nativeTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeNative{alive: map[string]bool{}})
	d.sessions.Upsert(s)
	seams := &fakeObsSeams{pr: &scm.PR{Number: 3, State: "MERGED"}}
	seams.install(d)

	d.observe(context.Background())

	if got := findSession(t, d.sessions.Snapshot(), s.ID); got.Status != "merged" {
		t.Errorf("status = %q, want merged (a merged PR outranks the dead pane)", got.Status)
	}
}

func TestObserveNativeNeedsInputOutranksPRStatus(t *testing.T) {
	s := nativeSess("FE-1", "needs_input")
	nat := &fakeNative{alive: map[string]bool{s.ID: true}}
	d := newTestDaemon(t, nativeTestConfig(nativePoll("p1")), &linear.Fake{}, nat)
	d.sessions.Upsert(s)
	seams := &fakeObsSeams{pr: &scm.PR{Number: 3, State: "OPEN", ChecksState: "pending"}}
	seams.install(d)

	d.observe(context.Background())

	if got := findSession(t, d.sessions.Snapshot(), s.ID); got.Status != "needs_input" {
		t.Errorf("status = %q, want needs_input to outrank ci_pending while the pane is alive", got.Status)
	}
}

// A hook event landing WHILE the observer is mid-cycle (between its snapshot
// and its write — the PR check alone can take seconds) must never be erased
// by the observer's write.
func TestObserveNativePreservesConcurrentHookNeedsInput(t *testing.T) {
	s := nativeSess("FE-1", "working")
	nat := &fakeNative{alive: map[string]bool{s.ID: true}}
	d := newTestDaemon(t, nativeTestConfig(nativePoll("p1")), &linear.Fake{}, nat)
	d.sessions.Upsert(s)
	// The PR-check seam doubles as the interleave point: the notification hook
	// fires while the observer is "inside" its gh exec, i.e. after the cycle's
	// snapshot was taken.
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
	s := nativeSess("FE-1", "working")
	s.Repo = "" // e.g. adopted before the repo was ever recorded
	nat := &fakeNative{alive: map[string]bool{s.ID: true}}
	d := newTestDaemon(t, nativeTestConfig(nativePoll("p1")), &linear.Fake{}, nat)
	d.sessions.Upsert(s)
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
// reply — the daemon must map Session.Source and derive the native worktree
// path (<home>/worktrees/<project>/<id>).
func TestSessionsDataIncludesSourceAndWorktree(t *testing.T) {
	d := newTestDaemon(t, nativeTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeNative{})
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
		t.Fatalf("non-native session missing from reply: %+v", data.Sessions)
	}
	if a.Source != "ao" || a.Worktree != "" {
		t.Errorf("non-native session = source %q worktree %q, want ao / empty", a.Source, a.Worktree)
	}
}

// reactingLabel is the pure derivation the TUI renders; the retry budget (M in
// "N/M") comes from the ci_failed reaction config.
func TestReactingLabel(t *testing.T) {
	const budget = 2
	cases := []struct {
		name      string
		status    string
		ciRetries int
		escalated bool
		want      string
	}{
		{"plain working session", "working", 0, false, ""},
		{"ci failed, no retry yet", "ci_failed", 0, false, "ci retry 0/2"},
		{"ci failed, mid retry", "ci_failed", 1, false, "ci retry 1/2"},
		{"ci pending after a retry send", "ci_pending", 1, false, "ci retry 1/2"},
		{"ci pending, no retry in flight", "ci_pending", 0, false, ""},
		{"escalated wins over ci_failed", "ci_failed", 2, true, "escalated"},
		{"changes requested", "changes_requested", 0, false, "addressing review"},
		{"merge conflict", "merge_conflict", 0, false, "rebasing"},
		{"approved and green", "approved", 0, false, "ready to merge"},
		{"green, awaiting a reviewer", "review_pending", 0, false, "awaiting review"},
		{"merged is not a posture", "merged", 0, false, ""},
	}
	for _, c := range cases {
		if got := reactingLabel(c.status, c.ciRetries, c.escalated, budget); got != c.want {
			t.Errorf("%s: reactingLabel(%q, %d, %v) = %q, want %q",
				c.name, c.status, c.ciRetries, c.escalated, got, c.want)
		}
	}
}

// The sessions reply flattens the persisted reaction state (CIRetries /
// Escalated) and derives the human posture label from status + those fields +
// the configured ci_failed retry budget, so the TUI renders it without touching
// internal/session.
func TestSessionsDataMapsReactionState(t *testing.T) {
	cfg := nativeTestConfig(nativePoll("p1"))
	cfg.Reactions.CIFailed.Retries = 2
	d := newTestDaemon(t, cfg, &linear.Fake{}, &fakeNative{})

	retrying := nativeSess("FE-1", "ci_failed")
	retrying.CIRetries = 1
	d.sessions.Upsert(retrying)

	escalated := nativeSess("FE-2", "ci_failed")
	escalated.CIRetries = 2
	escalated.Escalated = true
	d.sessions.Upsert(escalated)

	ready := nativeSess("FE-3", "approved")
	d.sessions.Upsert(ready)

	byID := map[string]protocol.SessionInfo{}
	for _, si := range d.sessionsData().Sessions {
		byID[si.ID] = si
	}

	if r := byID[retrying.ID]; r.CIRetries != 1 || r.Escalated || r.Reacting != "ci retry 1/2" {
		t.Errorf("retrying = {CIRetries %d, Escalated %v, Reacting %q}, want {1, false, %q}",
			r.CIRetries, r.Escalated, r.Reacting, "ci retry 1/2")
	}
	if e := byID[escalated.ID]; e.CIRetries != 2 || !e.Escalated || e.Reacting != "escalated" {
		t.Errorf("escalated = {CIRetries %d, Escalated %v, Reacting %q}, want {2, true, escalated}",
			e.CIRetries, e.Escalated, e.Reacting)
	}
	if r := byID[ready.ID]; r.Reacting != "ready to merge" {
		t.Errorf("approved session Reacting = %q, want %q", r.Reacting, "ready to merge")
	}
}

// --- Adoption -------------------------------------------------------------------

func TestAdoptNativeSessionsUpsertsAndPreservesFacts(t *testing.T) {
	d := newTestDaemon(t, nativeTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeNative{})
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

// Adoption must carry forward the hook-driven / one-shot state a tmux scan cannot
// observe — above all AtPrompt (the send-keys idle gate): dropping it wedges every
// DEFERRED hand-off (a restart would leave AtPrompt false, and only a fresh Stop
// hook reopens it, which an already-idle adopted agent never fires).
func TestAdoptNativeSessionsPreservesHookAndGuardState(t *testing.T) {
	d := newTestDaemon(t, nativeTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeNative{})
	prev := nativeSess("FE-1", "idle")
	prev.AtPrompt = true
	prev.LastReactedStatus = "ci_failed"
	prev.CIRetries = 2
	prev.Escalated = true
	prev.ReviewedPR = 7
	prev.LastCodeRabbitAt = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	prev.PendingCodeRabbit = "CodeRabbit posted new review feedback on PR #7. …"
	prev.RemovedLabels = []string{"lbl-trigger"}
	d.sessions.Upsert(prev)

	// The tmux scan yields a bare live record with none of that state.
	d.native = &fakeNative{adopted: []session.Session{
		{ID: prev.ID, Source: "native", Project: "proj1", Issue: "FE-1", TmuxName: prev.ID, Status: "working"},
	}}
	d.adoptNativeSessions(context.Background())

	got := findSession(t, d.sessions.Snapshot(), prev.ID)
	if !got.AtPrompt {
		t.Error("adoption must preserve AtPrompt (the send-keys gate) — else deferred hand-offs wedge")
	}
	if got.LastReactedStatus != "ci_failed" || got.CIRetries != 2 || !got.Escalated {
		t.Errorf("reaction guards must survive adoption, got %+v", got)
	}
	if got.ReviewedPR != 7 {
		t.Errorf("review guard must survive adoption, got ReviewedPR=%d", got.ReviewedPR)
	}
	if got.PendingCodeRabbit == "" || got.LastCodeRabbitAt.IsZero() {
		t.Errorf("coderabbit watermark + deferred hand-off must survive adoption, got %+v", got)
	}
	if len(got.RemovedLabels) != 1 || got.RemovedLabels[0] != "lbl-trigger" {
		t.Errorf("removed-labels (orphan revert) must survive adoption, got %v", got.RemovedLabels)
	}
}

func TestAdoptNativeSessionsLogsAnomaliesAndScanFailure(t *testing.T) {
	home := t.TempDir()
	t.Setenv("LOLA_HOME", home)
	var buf bytes.Buffer
	d := newDaemon(nativeTestConfig(nativePoll("p1")), &linear.Fake{}, log.New(&buf, "", 0), home)
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
	d2 := newDaemon(nativeTestConfig(nativePoll("p1")), &linear.Fake{}, log.New(&buf, "", 0), t.TempDir())
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
func TestReconcileNativeDeadSessionRevertsAndKeepsWorktree(t *testing.T) {
	is := testIssue("FE-231", 1, "2024-01-01T00:00:00Z")
	fake := &linear.Fake{
		Issues:          []linear.Issue{is},
		LabelIDsByIssue: map[string][]string{is.ID: {"lbl-sent", "lbl-other"}},
	}
	home := t.TempDir()
	t.Setenv("LOLA_HOME", home)
	var buf bytes.Buffer
	d := newDaemon(nativeTestConfig(nativePoll("p1")), fake, log.New(&buf, "", 0), home)
	d.native = &fakeNative{}
	d.runtimeHealth = func(string) error { return nil }

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
		d := newTestDaemon(t, nativeTestConfig(nativePoll("p1")), fake, &fakeNative{})
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

// The in-flight cleanup runs off the native session facts: a successfully
// spawned issue's claim must be released once its session is gone (settled
// dead), or it would block any later re-dispatch for the daemon's lifetime.
func TestReconcileNativeOnlyClearsStaleInflight(t *testing.T) {
	is := testIssue("FE-1", 1, "2024-01-01T00:00:00Z")
	d := newTestDaemon(t, nativeTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeNative{})
	// The session finished long ago: only a settled "dead" record remains,
	// which is not counted. Backdate the claim past the orphan grace.
	d.sessions.Upsert(nativeSess("FE-1", "dead"))
	d.inflight.Add(is.ID, is.Identifier)
	d.inflight.mu.Lock()
	d.inflight.m[is.ID] = inflightEntry{Identifier: is.Identifier, AddedAt: time.Now().Add(-2 * orphanTimeout)}
	d.inflight.mu.Unlock()

	d.reconcile(context.Background())

	if d.inflight.Has(is.ID) {
		t.Error("stale in-flight claim must be cleared when no counted session remains")
	}

	// A claim whose issue still has a counted (present) native session must
	// survive the same pass.
	d2 := newTestDaemon(t, nativeTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeNative{})
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

// multiAnyPoll is a label-mode poll that matches on ANY of two trigger labels
// — the exact shape (match_mode=any + >1 match_labels) whose flip/revert
// asymmetry the recorded-removed-labels fix addresses.
func multiAnyPoll(name string) config.Poll {
	p := labelPoll(name)
	p.MatchMode = "any"
	p.MatchLabels = []string{"lbl-a", "lbl-b"}
	p.OnSentSetLabel = "lbl-sent"
	return p
}

// An orphan that matched match_mode=any on a strict SUBSET of match_labels
// must revert to exactly the trigger labels the flip stripped (recorded on the
// session) — never every configured match_label. Restoring all would inject
// "lbl-b", a trigger label the issue never carried, corrupting Linear state and
// enabling spurious cross-poll spawns.
func TestReconcileRevertRestoresOnlyStrippedLabels(t *testing.T) {
	is := testIssue("FE-231", 1, "2024-01-01T00:00:00Z")
	// Post-flip the issue carries the sent label plus an unrelated label; the
	// only match_label it ever had was lbl-a.
	fake := &linear.Fake{
		Issues:          []linear.Issue{is},
		LabelIDsByIssue: map[string][]string{is.ID: {"lbl-sent", "lbl-other"}},
	}
	d := newReconcileDaemon(t, fake)
	seedOrphan(t, d, is)
	d.openPR = func(context.Context, string, string) (bool, error) { return false, nil }

	// The dead session records that only lbl-a was stripped at flip time.
	dead := nativeSess("FE-231", "dead")
	dead.RemovedLabels = []string{"lbl-a"}
	d.sessions.Upsert(dead)

	d.reconcilePoll(context.Background(), fake, multiAnyPoll("p1"), map[string]bool{}, time.Now())

	// lbl-a restored, lbl-sent dropped, lbl-other kept — and crucially NO lbl-b.
	want := []string{"lbl-other", "lbl-a"}
	if got := fake.LabelIDsByIssue[is.ID]; !reflect.DeepEqual(got, want) {
		t.Errorf("labels after revert = %v, want %v (must not re-add the never-carried lbl-b)", got, want)
	}
	if slices.Contains(fake.LabelIDsByIssue[is.ID], "lbl-b") {
		t.Error("revert re-added phantom trigger label lbl-b the issue never carried")
	}
}

// The post-spawn flip must record exactly which trigger labels it stripped
// (the subset the issue actually carried), so a later orphan revert can be its
// faithful inverse.
func TestTickFlipRecordsStrippedSubset(t *testing.T) {
	is := testIssue("FE-231", 1, "2024-01-01T00:00:00Z")
	// The issue matches match_mode=any via lbl-a only; lbl-b is absent.
	fake := &linear.Fake{
		Issues:          []linear.Issue{is},
		LabelIDsByIssue: map[string][]string{is.ID: {"lbl-a", "lbl-other"}},
	}
	nat := &fakeNative{}
	d := newTestDaemon(t, testConfig(multiAnyPoll("p1")), fake, nat)

	if _, err := d.tick(context.Background(), "p1", false); err != nil {
		t.Fatalf("tick: %v", err)
	}

	// Flip stripped lbl-a and added lbl-sent (lbl-b was never present).
	if got, want := fake.LabelIDsByIssue[is.ID], []string{"lbl-other", "lbl-sent"}; !reflect.DeepEqual(got, want) {
		t.Errorf("labels after flip = %v, want %v", got, want)
	}
	// The session must record exactly the stripped subset [lbl-a] — not [lbl-a lbl-b].
	sess, ok := d.nativeSessionForIssue("FE-231")
	if !ok {
		t.Fatal("no native session recorded for FE-231 after spawn")
	}
	if want := []string{"lbl-a"}; !reflect.DeepEqual(sess.RemovedLabels, want) {
		t.Errorf("session RemovedLabels = %v, want %v", sess.RemovedLabels, want)
	}
}
