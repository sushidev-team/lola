package daemon

import (
	"context"
	"errors"
	"testing"

	"github.com/sushidev-team/lola/internal/linear"
	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/runtime"
	"github.com/sushidev-team/lola/internal/session"
)

func TestHandleOpenManualCreatesShell(t *testing.T) {
	nat := &fakeNative{}
	d := newTestDaemon(t, nativeTestConfig(nativePoll("p1")), &linear.Fake{}, nat)

	data, err := d.handleOpenManual(context.Background(), protocol.OpenManualArgs{Project: "p1", Branch: "feat/x", Base: "develop"})
	if err != nil {
		t.Fatalf("handleOpenManual: %v", err)
	}
	wantID := runtime.ManualSessionID("p1", "feat/x")
	if data.SessionID != wantID || data.Branch != "feat/x" {
		t.Errorf("data = %+v, want id=%s branch=feat/x", data, wantID)
	}

	calls := nat.openManualCalls()
	if len(calls) != 1 || calls[0] != (nativeOpenManualCall{project: "p1", id: wantID, branch: "feat/x", base: "develop"}) {
		t.Fatalf("openManual calls = %+v", calls)
	}

	s, ok := d.sessions.Get(wantID)
	if !ok {
		t.Fatal("manual session must be upserted")
	}
	if s.Kind != session.KindManual || s.Status != "shell" || s.Issue != "" {
		t.Errorf("session = %+v, want manual shell with no issue", s)
	}
	if !s.OwnsBranch() {
		t.Error("a manual new-branch session must own its branch (deleted on teardown)")
	}
	if n := NativeLiveCounted(d.sessions.Snapshot()); n != 0 {
		t.Errorf("NativeLiveCounted = %d, want 0 (a shell does not count)", n)
	}
}

func TestHandleOpenManualUnknownProject(t *testing.T) {
	nat := &fakeNative{}
	d := newTestDaemon(t, nativeTestConfig(nativePoll("p1")), &linear.Fake{}, nat)
	if _, err := d.handleOpenManual(context.Background(), protocol.OpenManualArgs{Project: "nope", Branch: "b"}); err == nil {
		t.Fatal("want an error for an unknown project")
	}
	if len(nat.openManualCalls()) != 0 {
		t.Error("must not create a worktree for an unknown project")
	}
}

func TestHandleOpenManualRequiresBranch(t *testing.T) {
	nat := &fakeNative{}
	d := newTestDaemon(t, nativeTestConfig(nativePoll("p1")), &linear.Fake{}, nat)
	if _, err := d.handleOpenManual(context.Background(), protocol.OpenManualArgs{Project: "p1"}); err == nil {
		t.Fatal("want an error when branch is empty")
	}
}

func TestHandleOpenManualAlreadyOpen(t *testing.T) {
	nat := &fakeNative{}
	d := newTestDaemon(t, nativeTestConfig(nativePoll("p1")), &linear.Fake{}, nat)
	id := runtime.ManualSessionID("p1", "feat/x")
	d.sessions.Upsert(session.Session{ID: id, Source: "native", Kind: session.KindManual, Agentless: true, Project: "p1", Status: "shell"})

	if _, err := d.handleOpenManual(context.Background(), protocol.OpenManualArgs{Project: "p1", Branch: "feat/x"}); err == nil {
		t.Fatal("want an error when the branch is already open")
	}
	if len(nat.openManualCalls()) != 0 {
		t.Error("must not re-create an already-open session")
	}
}

func TestHandleOpenManualHealthGate(t *testing.T) {
	nat := &fakeNative{}
	d := newTestDaemon(t, nativeTestConfig(nativePoll("p1")), &linear.Fake{}, nat)
	d.runtimeHealth = func(string) error { return errors.New("missing git") }

	if _, err := d.handleOpenManual(context.Background(), protocol.OpenManualArgs{Project: "p1", Branch: "feat/x"}); err == nil {
		t.Fatal("want an error when the runtime is unhealthy")
	}
	if len(nat.openManualCalls()) != 0 {
		t.Error("must not create a worktree when the health gate fails")
	}
}

func TestHandleOpenURLRejectsNonHTTP(t *testing.T) {
	d := newTestDaemon(t, nativeTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeNative{})
	for _, bad := range []string{"", "file:///etc/passwd", "javascript:alert(1)", "; rm -rf /"} {
		if err := d.handleOpenURL(context.Background(), protocol.OpenURLArgs{URL: bad}); err == nil {
			t.Errorf("openURL(%q) must be refused", bad)
		}
	}
}
