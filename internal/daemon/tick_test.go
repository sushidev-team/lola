package daemon

import (
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

	"github.com/you/aop/internal/ao"
	"github.com/you/aop/internal/config"
	"github.com/you/aop/internal/linear"
	"github.com/you/aop/internal/protocol"
)

// fakeAO is a hermetic AOAPI: no real ao binary is ever executed.
type fakeAO struct {
	mu          sync.Mutex
	unreachable bool
	sessions    []ao.SessionState
	sessionsErr error
	spawnErr    error
	spawns      []spawnCall
	onSpawn     func(project, identifier string) // runs before spawnErr is returned
}

type spawnCall struct{ project, identifier string }

// Like the real ao.Client (exec.CommandContext), every method fails once
// its context is cancelled — tests rely on this to prove ticks are shielded
// from the shutdown cancellation.
func (f *fakeAO) Reachable(ctx context.Context) bool {
	return ctx.Err() == nil && !f.unreachable
}

func (f *fakeAO) LiveSessions(ctx context.Context) ([]ao.SessionState, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.sessionsErr != nil {
		return nil, f.sessionsErr
	}
	return slices.Clone(f.sessions), nil
}

func (f *fakeAO) Spawn(ctx context.Context, project, identifier string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	f.mu.Lock()
	f.spawns = append(f.spawns, spawnCall{project, identifier})
	hook, err := f.onSpawn, f.spawnErr
	f.mu.Unlock()
	if hook != nil {
		hook(project, identifier)
	}
	return err
}

func (f *fakeAO) spawnCalls() []spawnCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.spawns)
}

func labelPoll(name string) config.Poll {
	return config.Poll{
		Name:              name,
		Enabled:           true,
		TeamID:            "team-1",
		CycleMode:         "none",
		MatchLabels:       []string{"lbl-trigger"},
		MatchMode:         "any",
		AssigneeMode:      "anyone",
		AOProject:         "proj",
		ConcurrencyCap:    10,
		DedupMode:         "label",
		OnSentSetLabel:    "lbl-sent",
		OnSentRemoveLabel: "lbl-trigger",
	}
}

func seenPoll(name string) config.Poll {
	p := labelPoll(name)
	p.DedupMode = "seen"
	p.OnSentSetLabel = ""
	p.OnSentRemoveLabel = ""
	return p
}

func testConfig(polls ...config.Poll) *config.Config {
	return &config.Config{
		Defaults: config.Defaults{
			PollInterval:   time.Minute,
			ConcurrencyCap: 10,
			GlobalCap:      10,
		},
		AO:    config.AOConfig{CountingStates: []string{"working", "in_progress"}},
		Polls: polls,
	}
}

// newTestDaemon builds a daemon on an AOP_HOME temp dir with fake Linear and
// AO backends. Nothing touches the network, keychain, or a real ao binary.
func newTestDaemon(t *testing.T, cfg *config.Config, fake *linear.Fake, aoc AOAPI) *Daemon {
	t.Helper()
	home := t.TempDir()
	t.Setenv("AOP_HOME", home)
	return newDaemon(cfg, fake, aoc, log.New(io.Discard, "", 0), home)
}

func testIssue(ident string, prio float64, created string) linear.Issue {
	return linear.Issue{
		ID:         "uuid-" + ident,
		Identifier: ident,
		Title:      "title " + ident,
		Priority:   prio,
		CreatedAt:  created,
	}
}

func seenPath(d *Daemon, poll string) string {
	return filepath.Join(d.home, "state", poll+".seen")
}

func findMatch(t *testing.T, res protocol.PollOnceData, ident string) protocol.Match {
	t.Helper()
	for _, m := range res.Matches {
		if m.Identifier == ident {
			return m
		}
	}
	t.Fatalf("no match entry for %s in %+v", ident, res.Matches)
	return protocol.Match{}
}

func countCalls(names []string, method string) int {
	n := 0
	for _, name := range names {
		if name == method {
			n++
		}
	}
	return n
}

// --- Dedup: label mode ---------------------------------------------------

func TestTickLabelModeSeenWithinTTLBlocksRespawn(t *testing.T) {
	is := testIssue("FE-1", 1, "2024-01-01T00:00:00Z")
	fake := &linear.Fake{Issues: []linear.Issue{is}}
	aoc := &fakeAO{}
	d := newTestDaemon(t, testConfig(labelPoll("p1")), fake, aoc)

	if err := d.seen.save("p1", map[string]time.Time{is.ID: time.Now().Add(-10 * time.Minute)}); err != nil {
		t.Fatal(err)
	}

	res, err := d.tick(context.Background(), "p1", false)
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	if len(aoc.spawnCalls()) != 0 {
		t.Errorf("seen entry within TTL must block respawn, got spawns %v", aoc.spawnCalls())
	}
	m := findMatch(t, res, "FE-1")
	if m.Action != "skipped" || m.Reason != "dedup-label" {
		t.Errorf("match = %+v, want skipped/dedup-label", m)
	}
}

func TestTickLabelModeExpiredSeenDoesNotBlock(t *testing.T) {
	is := testIssue("FE-1", 1, "2024-01-01T00:00:00Z")
	fake := &linear.Fake{Issues: []linear.Issue{is}}
	aoc := &fakeAO{}
	d := newTestDaemon(t, testConfig(labelPoll("p1")), fake, aoc)

	if err := d.seen.save("p1", map[string]time.Time{is.ID: time.Now().Add(-2 * SeenTTL)}); err != nil {
		t.Fatal(err)
	}

	res, err := d.tick(context.Background(), "p1", false)
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	if got := aoc.spawnCalls(); len(got) != 1 {
		t.Fatalf("expired seen entry must not block respawn, got spawns %v", got)
	}
	if m := findMatch(t, res, "FE-1"); m.Action != "spawned" {
		t.Errorf("match = %+v, want spawned", m)
	}
}

// --- Dedup: seen mode ----------------------------------------------------

func TestTickSeenModeDropsSeenAndPrunesForRequeue(t *testing.T) {
	is := testIssue("FE-1", 1, "2024-01-01T00:00:00Z")
	fake := &linear.Fake{Issues: []linear.Issue{is}}
	aoc := &fakeAO{}
	d := newTestDaemon(t, testConfig(seenPoll("p1")), fake, aoc)
	ctx := context.Background()

	// Tick 1: fresh issue -> spawned, recorded in seen.
	if _, err := d.tick(ctx, "p1", false); err != nil {
		t.Fatalf("tick 1: %v", err)
	}
	if len(aoc.spawnCalls()) != 1 {
		t.Fatalf("tick 1: want 1 spawn, got %v", aoc.spawnCalls())
	}

	// Tick 2: same issue matches again, seen is authoritative -> dropped.
	// (The in-flight claim would also block it; assert the seen reason by
	// clearing the claim first, as the reconcile pass eventually would.)
	d.inflight.Remove(is.ID)
	res2, err := d.tick(ctx, "p1", false)
	if err != nil {
		t.Fatalf("tick 2: %v", err)
	}
	if len(aoc.spawnCalls()) != 1 {
		t.Fatalf("tick 2: seen ID must be dropped, got spawns %v", aoc.spawnCalls())
	}
	if m := findMatch(t, res2, "FE-1"); m.Action != "skipped" || m.Reason != "dedup-seen" {
		t.Errorf("tick 2 match = %+v, want skipped/dedup-seen", m)
	}

	// Tick 3: issue no longer matches -> its seen entry is pruned + persisted.
	fake.Issues = nil
	if _, err := d.tick(ctx, "p1", false); err != nil {
		t.Fatalf("tick 3: %v", err)
	}
	data, err := os.ReadFile(seenPath(d, "p1"))
	if err != nil {
		t.Fatalf("read seen file: %v", err)
	}
	if strings.Contains(string(data), is.ID) {
		t.Errorf("seen entry for non-matching issue must be pruned from disk, file = %s", data)
	}

	// Tick 4: reopened ticket matches again -> re-queues (second spawn).
	fake.Issues = []linear.Issue{is}
	d.inflight.Remove(is.ID)
	res4, err := d.tick(ctx, "p1", false)
	if err != nil {
		t.Fatalf("tick 4: %v", err)
	}
	if len(aoc.spawnCalls()) != 2 {
		t.Fatalf("reopened ticket must re-queue, got spawns %v", aoc.spawnCalls())
	}
	if m := findMatch(t, res4, "FE-1"); m.Action != "spawned" {
		t.Errorf("tick 4 match = %+v, want spawned", m)
	}
}

// --- Cross-poll dedup ----------------------------------------------------

func TestTickCrossPollDedupSingleSpawn(t *testing.T) {
	is := testIssue("FE-1", 1, "2024-01-01T00:00:00Z")
	fake := &linear.Fake{Issues: []linear.Issue{is}}
	aoc := &fakeAO{}
	p1, p2 := seenPoll("p1"), seenPoll("p2")
	d := newTestDaemon(t, testConfig(p1, p2), fake, aoc)
	ctx := context.Background()

	if _, err := d.tick(ctx, "p1", false); err != nil {
		t.Fatalf("tick p1: %v", err)
	}
	res2, err := d.tick(ctx, "p2", false)
	if err != nil {
		t.Fatalf("tick p2: %v", err)
	}

	if got := aoc.spawnCalls(); len(got) != 1 {
		t.Fatalf("same UUID matched by two polls must spawn exactly once, got %v", got)
	}
	if m := findMatch(t, res2, "FE-1"); m.Action != "skipped" || m.Reason != "in-flight" {
		t.Errorf("p2 match = %+v, want skipped/in-flight", m)
	}
}

// --- Dispatch ordering ---------------------------------------------------

func TestTickDispatchOrdering(t *testing.T) {
	is := testIssue("FE-231", 1, "2024-01-01T00:00:00Z")
	fake := &linear.Fake{
		Issues:          []linear.Issue{is},
		LabelIDsByIssue: map[string][]string{is.ID: {"lbl-trigger", "lbl-other"}},
	}
	aoc := &fakeAO{}
	d := newTestDaemon(t, testConfig(labelPoll("p1")), fake, aoc)

	// The spawn hook observes daemon state at the exact moment of the spawn:
	// in-flight claimed and seen persisted BEFORE, label reads strictly AFTER.
	aoc.onSpawn = func(project, identifier string) {
		if !d.inflight.Has(is.ID) {
			t.Error("in-flight must be marked before Spawn")
		}
		data, err := os.ReadFile(seenPath(d, "p1"))
		if err != nil || !strings.Contains(string(data), is.ID) {
			t.Errorf("seen must be persisted to disk before Spawn (err=%v, data=%s)", err, data)
		}
		names := fake.CallNames()
		if slices.Contains(names, "IssueLabelIDs") || slices.Contains(names, "SetIssueLabels") {
			t.Errorf("label calls must happen only after spawn success, already saw %v", names)
		}
	}

	res, err := d.tick(context.Background(), "p1", false)
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	if m := findMatch(t, res, "FE-231"); m.Action != "spawned" {
		t.Fatalf("match = %+v, want spawned", m)
	}

	// Spawn got the IDENTIFIER (FE-231) and the poll's ao_project.
	spawns := aoc.spawnCalls()
	if len(spawns) != 1 || spawns[0] != (spawnCall{project: "proj", identifier: "FE-231"}) {
		t.Errorf("spawns = %+v, want [{proj FE-231}]", spawns)
	}

	// Fresh IssueLabelIDs re-read precedes SetIssueLabels.
	names := fake.CallNames()
	iRead := slices.Index(names, "IssueLabelIDs")
	iWrite := slices.Index(names, "SetIssueLabels")
	if iRead == -1 || iWrite == -1 || iRead > iWrite {
		t.Fatalf("want IssueLabelIDs before SetIssueLabels, call order = %v", names)
	}

	// SetIssueLabels got the UUID and the full computed array.
	var setCall *linear.Call
	for _, c := range fake.CallLog() {
		if c.Method == "SetIssueLabels" {
			cc := c
			setCall = &cc
		}
	}
	if setCall == nil {
		t.Fatal("SetIssueLabels was never called")
	}
	if got := setCall.Args[0]; got != is.ID {
		t.Errorf("SetIssueLabels issue arg = %v, want UUID %q (not the identifier)", got, is.ID)
	}
	wantLabels := []string{"lbl-other", "lbl-sent"}
	if got, ok := setCall.Args[1].([]string); !ok || !reflect.DeepEqual(got, wantLabels) {
		t.Errorf("SetIssueLabels labels = %v, want %v", setCall.Args[1], wantLabels)
	}
	// IssueLabelIDs also got the UUID.
	for _, c := range fake.CallLog() {
		if c.Method == "IssueLabelIDs" && c.Args[0] != is.ID {
			t.Errorf("IssueLabelIDs arg = %v, want UUID %q", c.Args[0], is.ID)
		}
	}
}

func TestTickSpawnFailureNoLabelMutation(t *testing.T) {
	is := testIssue("FE-1", 1, "2024-01-01T00:00:00Z")
	fake := &linear.Fake{
		Issues:          []linear.Issue{is},
		LabelIDsByIssue: map[string][]string{is.ID: {"lbl-trigger"}},
	}
	aoc := &fakeAO{spawnErr: errors.New("boom")}
	d := newTestDaemon(t, testConfig(labelPoll("p1")), fake, aoc)

	res, err := d.tick(context.Background(), "p1", false)
	if err != nil {
		t.Fatalf("tick: %v", err)
	}

	names := fake.CallNames()
	if slices.Contains(names, "IssueLabelIDs") || slices.Contains(names, "SetIssueLabels") {
		t.Errorf("spawn failure must not mutate labels, calls = %v", names)
	}
	if m := findMatch(t, res, "FE-1"); m.Action != "skipped" || m.Reason != "error" {
		t.Errorf("match = %+v, want skipped/error", m)
	}
	if d.inflight.Has(is.ID) {
		t.Error("failed spawn must drop the in-flight claim")
	}
	if got := d.status.get("p1").LastError; !strings.Contains(got, "spawn FE-1 failed") {
		t.Errorf("status lastError = %q, want it to mention the spawn failure", got)
	}
	// seen stays as the race guard after a failed spawn.
	seen, err := d.seen.load("p1")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := seen[is.ID]; !ok {
		t.Error("seen entry must remain after failed spawn (race guard)")
	}
}

// --- AO unreachable ------------------------------------------------------

func TestTickAOUnreachableNoLinearCallsNoStateMutation(t *testing.T) {
	is := testIssue("FE-1", 1, "2024-01-01T00:00:00Z")
	fake := &linear.Fake{Issues: []linear.Issue{is}}
	aoc := &fakeAO{unreachable: true}
	d := newTestDaemon(t, testConfig(labelPoll("p1")), fake, aoc)

	_, err := d.tick(context.Background(), "p1", false)
	if err == nil {
		t.Fatal("tick must fail when AO is unreachable")
	}
	if names := fake.CallNames(); len(names) != 0 {
		t.Errorf("AO down: tick must make NO linear calls, got %v", names)
	}
	if len(aoc.spawnCalls()) != 0 {
		t.Errorf("AO down: no spawns, got %v", aoc.spawnCalls())
	}
	if d.inflight.Has(is.ID) {
		t.Error("AO down: in-flight must not be mutated")
	}
	if _, err := os.Stat(seenPath(d, "p1")); !os.IsNotExist(err) {
		t.Errorf("AO down: seen state must not be written, stat err = %v", err)
	}
	if got := d.status.get("p1").LastError; got != "AO not running" {
		t.Errorf("status lastError = %q, want %q", got, "AO not running")
	}
}

// --- Budget / caps -------------------------------------------------------

func TestTickBudgetCountsOnlyCountingStates(t *testing.T) {
	issues := []linear.Issue{
		testIssue("FE-LOW", 4, "2024-01-01T00:00:00Z"),
		testIssue("FE-URGENT", 1, "2024-01-02T00:00:00Z"),
		testIssue("FE-NONE", 0, "2024-01-01T00:00:00Z"),
	}
	fake := &linear.Fake{Issues: issues}
	aoc := &fakeAO{sessions: []ao.SessionState{
		{ID: "s1", Status: "working"},
		{ID: "s2", Status: "working"},
		{ID: "s3", Status: "review"},  // held PR: must NOT count
		{ID: "s4", Status: "blocked"}, // must NOT count
	}}
	cfg := testConfig(seenPoll("p1"))
	cfg.Defaults.GlobalCap = 3 // budget = min(10, 3-2) = 1
	d := newTestDaemon(t, cfg, fake, aoc)

	res, err := d.tick(context.Background(), "p1", false)
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	spawns := aoc.spawnCalls()
	if len(spawns) != 1 {
		t.Fatalf("budget 1: want exactly 1 spawn, got %v", spawns)
	}
	// Deterministic selection under cap: urgent first, priority 0 last.
	if spawns[0].identifier != "FE-URGENT" {
		t.Errorf("capped selection spawned %s, want FE-URGENT (priority sort)", spawns[0].identifier)
	}
	for _, ident := range []string{"FE-LOW", "FE-NONE"} {
		if m := findMatch(t, res, ident); m.Action != "skipped" || m.Reason != "capped" {
			t.Errorf("%s match = %+v, want skipped/capped", ident, m)
		}
	}
}

func TestTickBudgetZeroNoMutation(t *testing.T) {
	is := testIssue("FE-1", 1, "2024-01-01T00:00:00Z")
	fake := &linear.Fake{Issues: []linear.Issue{is}}
	aoc := &fakeAO{sessions: []ao.SessionState{
		{ID: "s1", Status: "working"},
		{ID: "s2", Status: "in_progress"},
	}}
	cfg := testConfig(seenPoll("p1"))
	cfg.Defaults.GlobalCap = 2 // liveCounted=2 -> budget 0
	d := newTestDaemon(t, cfg, fake, aoc)

	res, err := d.tick(context.Background(), "p1", false)
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	if len(aoc.spawnCalls()) != 0 {
		t.Errorf("budget 0: no spawns, got %v", aoc.spawnCalls())
	}
	if m := findMatch(t, res, "FE-1"); m.Action != "skipped" || m.Reason != "capped" {
		t.Errorf("match = %+v, want skipped/capped", m)
	}
	if d.inflight.Has(is.ID) {
		t.Error("capped-out issue must not be marked in-flight")
	}
	seen, err := d.seen.load("p1")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := seen[is.ID]; ok {
		t.Error("capped-out issue must not be written to seen")
	}
}

// --- Dry run ---------------------------------------------------------------

func TestTickDryRunNoSideEffects(t *testing.T) {
	is := testIssue("FE-1", 1, "2024-01-01T00:00:00Z")
	fake := &linear.Fake{
		Issues:          []linear.Issue{is},
		LabelIDsByIssue: map[string][]string{is.ID: {"lbl-trigger"}},
	}
	aoc := &fakeAO{}
	d := newTestDaemon(t, testConfig(labelPoll("p1")), fake, aoc)

	res, err := d.tick(context.Background(), "p1", true)
	if err != nil {
		t.Fatalf("dry-run tick: %v", err)
	}
	if !res.DryRun {
		t.Error("result must be flagged DryRun")
	}
	if m := findMatch(t, res, "FE-1"); m.Action != "would-spawn" {
		t.Errorf("match = %+v, want would-spawn", m)
	}
	if len(aoc.spawnCalls()) != 0 {
		t.Errorf("dry run must not spawn, got %v", aoc.spawnCalls())
	}
	if d.inflight.Has(is.ID) {
		t.Error("dry run must not mark in-flight")
	}
	if _, err := os.Stat(seenPath(d, "p1")); !os.IsNotExist(err) {
		t.Errorf("dry run must not write seen state, stat err = %v", err)
	}
	names := fake.CallNames()
	if slices.Contains(names, "SetIssueLabels") || slices.Contains(names, "IssueLabelIDs") {
		t.Errorf("dry run must not touch labels, calls = %v", names)
	}
	st := d.status.get("p1")
	if !st.LastRun.IsZero() || !st.LastSpawn.IsZero() || st.LastError != "" {
		t.Errorf("dry run must not mutate status, got %+v", st)
	}
}

func TestTickDryRunReportsCrossPollOverlap(t *testing.T) {
	is := testIssue("FE-1", 1, "2024-01-01T00:00:00Z")
	fake := &linear.Fake{Issues: []linear.Issue{is}}
	aoc := &fakeAO{}
	d := newTestDaemon(t, testConfig(seenPoll("p1"), seenPoll("p2")), fake, aoc)
	ctx := context.Background()

	// p1 really dispatches the issue; a p2 dry run must surface the overlap.
	if _, err := d.tick(ctx, "p1", false); err != nil {
		t.Fatalf("tick p1: %v", err)
	}
	res, err := d.tick(ctx, "p2", true)
	if err != nil {
		t.Fatalf("dry-run tick p2: %v", err)
	}
	if m := findMatch(t, res, "FE-1"); m.Action != "skipped" || m.Reason != "in-flight" {
		t.Errorf("overlap match = %+v, want skipped/in-flight", m)
	}
	if len(aoc.spawnCalls()) != 1 {
		t.Errorf("dry run must not spawn the overlapping issue, got %v", aoc.spawnCalls())
	}
}

func TestTickSeenModeSpawnFailureClearsSeen(t *testing.T) {
	is := testIssue("FE-1", 1, "2024-01-01T00:00:00Z")
	fake := &linear.Fake{Issues: []linear.Issue{is}}
	aoc := &fakeAO{spawnErr: errors.New("tmux hiccup")}
	d := newTestDaemon(t, testConfig(seenPoll("p1")), fake, aoc)
	ctx := context.Background()

	if _, err := d.tick(ctx, "p1", false); err != nil {
		t.Fatalf("tick 1: %v", err)
	}
	// seen is authoritative in seen mode: a failed spawn must NOT leave the
	// entry behind, or the issue is dropped forever (nothing prunes it while
	// it still matches, and reconcile only handles label mode).
	seen, err := d.seen.load("p1")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := seen[is.ID]; ok {
		t.Fatal("seen-mode spawn failure must clear the seen entry so the issue retries")
	}
	if d.inflight.Has(is.ID) {
		t.Fatal("failed spawn must drop the in-flight claim")
	}

	// Next tick retries and succeeds.
	aoc.mu.Lock()
	aoc.spawnErr = nil
	aoc.mu.Unlock()
	res, err := d.tick(ctx, "p1", false)
	if err != nil {
		t.Fatalf("tick 2: %v", err)
	}
	if got := aoc.spawnCalls(); len(got) != 2 {
		t.Fatalf("issue must be retried after a failed spawn, spawns = %v", got)
	}
	if m := findMatch(t, res, "FE-1"); m.Action != "spawned" {
		t.Errorf("tick 2 match = %+v, want spawned", m)
	}
}

// --- Linear auth failure ---------------------------------------------------

func TestTickAuthFailureInvalidatesLinearClient(t *testing.T) {
	is := testIssue("FE-1", 1, "2024-01-01T00:00:00Z")
	fake := &linear.Fake{
		Issues: []linear.Issue{is},
		Errs:   map[string]error{"MatchingIssues": errors.New("linear auth failed: http 401")},
	}
	d := newTestDaemon(t, testConfig(seenPoll("p1")), fake, &fakeAO{})

	if _, err := d.tick(context.Background(), "p1", false); err == nil {
		t.Fatal("tick must fail on auth error")
	}
	d.mu.Lock()
	lin, linOK := d.lin, d.linOK
	d.mu.Unlock()
	if lin != nil {
		t.Error("auth failure must drop the cached Linear client so the API key is re-resolved (key rotation)")
	}
	if linOK {
		t.Error("linOK must be false after an auth failure")
	}
	if got := d.status.get("p1").LastError; !strings.Contains(got, "Linear auth failed") {
		t.Errorf("status lastError = %q, want Linear auth failed", got)
	}
}

// --- Graceful shutdown -------------------------------------------------------

func TestSafeTickFinishesAfterShutdownCancel(t *testing.T) {
	is := testIssue("FE-1", 1, "2024-01-01T00:00:00Z")
	fake := &linear.Fake{Issues: []linear.Issue{is}}
	aoc := &fakeAO{} // fails all calls on a cancelled ctx, like the real client
	d := newTestDaemon(t, testConfig(seenPoll("p1")), fake, aoc)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // shutdown already requested

	d.safeTick(ctx, "p1")

	if got := aoc.spawnCalls(); len(got) != 1 {
		t.Fatalf("in-flight tick must finish despite shutdown cancellation, spawns = %v", got)
	}
	if got := d.status.get("p1").LastError; got != "" {
		t.Errorf("tick under shutdown must complete cleanly, lastError = %q", got)
	}
}

func TestPollOnceRefusedWhileDraining(t *testing.T) {
	fake := &linear.Fake{}
	d := newTestDaemon(t, testConfig(seenPoll("p1")), fake, &fakeAO{})
	d.drainConnWork()
	if _, err := d.handlePollOnce(context.Background(), "p1", false); err == nil {
		t.Fatal("pollOnce during shutdown drain must be refused")
	}
}

// --- Invalid config holds polls ---------------------------------------------

func TestInvalidConfigHoldsWorkersAndSurfacesInStatus(t *testing.T) {
	fake := &linear.Fake{}
	d := newTestDaemon(t, testConfig(labelPoll("p1")), fake, &fakeAO{})
	d.cfgErr = "config invalid: boom"

	ctx := context.Background()
	d.syncWorkers(ctx)
	d.mu.Lock()
	n := len(d.workers)
	d.mu.Unlock()
	if n != 0 {
		t.Fatalf("invalid config must hold all polls, %d worker(s) started", n)
	}

	sd := d.statusData(ctx)
	if len(sd.Polls) != 1 || sd.Polls[0].LastError != "config invalid: boom" {
		t.Errorf("status must surface the config error per poll, got %+v", sd.Polls)
	}

	if _, err := d.handlePollOnce(ctx, "p1", false); err == nil || !strings.Contains(err.Error(), "config invalid") {
		t.Errorf("pollOnce on an invalid config must be refused, err = %v", err)
	}

	// A valid config lifts the hold.
	d.mu.Lock()
	d.cfgErr = ""
	d.mu.Unlock()
	d.syncWorkers(ctx)
	d.mu.Lock()
	n = len(d.workers)
	d.mu.Unlock()
	if n != 1 {
		t.Fatalf("clearing the config error must start workers again, got %d", n)
	}
	d.stopAllWorkers()
}

// --- Active cycle resolution ----------------------------------------------

func TestTickActiveCycleResolvedFreshEveryTick(t *testing.T) {
	is := testIssue("FE-1", 1, "2024-01-01T00:00:00Z")
	fake := &linear.Fake{
		Issues:            []linear.Issue{is},
		ActiveCycleByTeam: map[string]*linear.Cycle{"team-1": {ID: "cyc-1", Number: 7}},
	}
	aoc := &fakeAO{}
	p := seenPoll("p1")
	p.CycleMode = "active"
	d := newTestDaemon(t, testConfig(p), fake, aoc)
	ctx := context.Background()

	if _, err := d.tick(ctx, "p1", false); err != nil {
		t.Fatalf("tick 1: %v", err)
	}
	if _, err := d.tick(ctx, "p1", false); err != nil {
		t.Fatalf("tick 2: %v", err)
	}

	names := fake.CallNames()
	if got := countCalls(names, "Cycles"); got != 2 {
		t.Errorf("active cycle must be resolved fresh per tick: Cycles called %d times, want 2", got)
	}
	for _, c := range fake.CallLog() {
		if c.Method == "MatchingIssues" {
			if got := c.Args[1]; got != "cyc-1" {
				t.Errorf("MatchingIssues activeCycleID = %v, want cyc-1", got)
			}
		}
	}
}
