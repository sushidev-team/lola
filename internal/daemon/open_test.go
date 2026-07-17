package daemon

import (
	"context"
	"errors"
	"testing"

	"github.com/sushidev-team/lola/internal/linear"
	"github.com/sushidev-team/lola/internal/runtime"
	"github.com/sushidev-team/lola/internal/session"
)

// A numeric target opens a PR head: the fetch ref is pull/<n>/head, the branch
// label is pr-<n>, and the recorded session is Manual/"shell" with no Linear
// issue so the observer keeps it out of the control loop.
func TestHandleOpenPRHappyPath(t *testing.T) {
	nat := &fakeNative{}
	d := newTestDaemon(t, nativeTestConfig(nativePoll("p1")), &linear.Fake{}, nat)

	data, err := d.handleOpen(context.Background(), "proj1", "42")
	if err != nil {
		t.Fatalf("handleOpen: %v", err)
	}
	wantID := runtime.ManualSessionID("proj1", "pr-42")
	if data.SessionID != wantID {
		t.Errorf("SessionID = %q, want %q", data.SessionID, wantID)
	}
	if data.Branch != "pr-42" {
		t.Errorf("Branch = %q, want pr-42", data.Branch)
	}

	calls := nat.openCalls()
	if len(calls) != 1 {
		t.Fatalf("native Open calls = %d, want 1", len(calls))
	}
	if calls[0] != (nativeOpenCall{project: "proj1", id: wantID, ref: "pull/42/head", branch: "pr-42"}) {
		t.Errorf("open call = %+v", calls[0])
	}

	s, ok := d.sessions.Get(wantID)
	if !ok {
		t.Fatal("manual session must be upserted into the store")
	}
	if !s.Manual || s.Status != "shell" || s.Issue != "" {
		t.Errorf("session = %+v, want Manual shell with no issue", s)
	}
	// A shell must never occupy a dispatch slot.
	if n := NativeLiveCounted(d.sessions.Snapshot()); n != 0 {
		t.Errorf("NativeLiveCounted = %d, want 0 (a shell does not count)", n)
	}
}

// A non-numeric target opens a branch as-is (fetch ref == branch label).
func TestHandleOpenBranchTarget(t *testing.T) {
	nat := &fakeNative{}
	d := newTestDaemon(t, nativeTestConfig(nativePoll("p1")), &linear.Fake{}, nat)

	if _, err := d.handleOpen(context.Background(), "proj1", "feat/login"); err != nil {
		t.Fatalf("handleOpen: %v", err)
	}
	calls := nat.openCalls()
	if len(calls) != 1 || calls[0].ref != "feat/login" || calls[0].branch != "feat/login" {
		t.Errorf("branch open call = %+v, want ref/branch feat/login", calls)
	}
}

func TestHandleOpenUnknownProject(t *testing.T) {
	nat := &fakeNative{}
	d := newTestDaemon(t, nativeTestConfig(nativePoll("p1")), &linear.Fake{}, nat)

	if _, err := d.handleOpen(context.Background(), "nope", "42"); err == nil {
		t.Fatal("want an error for an unknown project")
	}
	if len(nat.openCalls()) != 0 {
		t.Error("must not open for an unknown project")
	}
}

func TestHandleOpenAlreadyOpen(t *testing.T) {
	nat := &fakeNative{}
	d := newTestDaemon(t, nativeTestConfig(nativePoll("p1")), &linear.Fake{}, nat)
	id := runtime.ManualSessionID("proj1", "pr-42")
	d.sessions.Upsert(session.Session{ID: id, Source: "native", Manual: true, Project: "proj1", Status: "shell"})

	if _, err := d.handleOpen(context.Background(), "proj1", "42"); err == nil {
		t.Fatal("want an error when the session is already open")
	}
	if len(nat.openCalls()) != 0 {
		t.Error("must not re-open an already-open session")
	}
}

func TestHandleOpenHealthGate(t *testing.T) {
	nat := &fakeNative{}
	d := newTestDaemon(t, nativeTestConfig(nativePoll("p1")), &linear.Fake{}, nat)
	d.runtimeHealth = func(string) error { return errors.New("missing git") }

	if _, err := d.handleOpen(context.Background(), "proj1", "42"); err == nil {
		t.Fatal("want an error when the runtime is unhealthy")
	}
	if len(nat.openCalls()) != 0 {
		t.Error("must not open when the health gate fails")
	}
}

func TestResolveOpenTarget(t *testing.T) {
	cases := []struct{ in, ref, branch string }{
		{"42", "pull/42/head", "pr-42"},
		{"1", "pull/1/head", "pr-1"},
		{"feat/x", "feat/x", "feat/x"},
		{"0", "0", "0"},    // 0 is not a valid PR number -> treated as a branch
		{"-3", "-3", "-3"}, // negative -> branch
		{"main", "main", "main"},
	}
	for _, c := range cases {
		if r, b := resolveOpenTarget(c.in); r != c.ref || b != c.branch {
			t.Errorf("resolveOpenTarget(%q) = (%q, %q), want (%q, %q)", c.in, r, b, c.ref, c.branch)
		}
	}
}

// A manual shell's status is pure tmux liveness: "shell" while alive, "dead"
// once the pane is gone. It never reaches the reaction / write-back engines.
func TestObserveManualShellLiveness(t *testing.T) {
	nat := &fakeNative{alive: map[string]bool{}}
	d := newTestDaemon(t, nativeTestConfig(nativePoll("p1")), &linear.Fake{}, nat)
	id := runtime.ManualSessionID("proj1", "pr-42")
	d.sessions.Upsert(session.Session{ID: id, Source: "native", Manual: true, Project: "proj1", TmuxName: id, Status: "shell"})

	nat.alive[id] = true
	s, _ := d.sessions.Get(id)
	d.observeManualShell(context.Background(), nat, s)
	if got, _ := d.sessions.Get(id); got.Status != "shell" {
		t.Errorf("alive shell status = %q, want shell", got.Status)
	}

	nat.alive[id] = false
	s, _ = d.sessions.Get(id)
	d.observeManualShell(context.Background(), nat, s)
	if got, _ := d.sessions.Get(id); got.Status != "dead" {
		t.Errorf("gone shell status = %q, want dead", got.Status)
	}
}
