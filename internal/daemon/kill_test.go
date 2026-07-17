package daemon

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/sushidev-team/lola/internal/linear"
	"github.com/sushidev-team/lola/internal/session"
	"github.com/sushidev-team/lola/internal/worktree"
)

// Happy path: a clean kill removes the worktree, drops the store entry, frees
// the issue's in-flight claim, and reports it. The runtime is asked to remove
// the worktree without force.
func TestHandleKillHappyPath(t *testing.T) {
	nat := &fakeNative{}
	d := newTestDaemon(t, nativeTestConfig(nativePoll("p1")), &linear.Fake{}, nat)

	s := nativeSess("FE-1", "working")
	s.IssueUUID = "uuid-fe-1"
	d.sessions.Upsert(s)
	d.inflight.Add(s.IssueUUID, s.Issue)

	data, err := d.handleKill(context.Background(), s.ID, false)
	if err != nil {
		t.Fatalf("handleKill: %v", err)
	}
	if !data.Removed {
		t.Errorf("KillData.Removed = false, want true")
	}
	if want := filepath.Join(d.home, "worktrees", "p1", s.ID); data.Worktree != want {
		t.Errorf("KillData.Worktree = %q, want %q", data.Worktree, want)
	}
	if calls := nat.killCalls(); len(calls) != 1 || calls[0] != (nativeKillCall{id: s.ID, removeWorktree: true, force: false}) {
		t.Errorf("native Kill calls = %+v, want one {id, removeWorktree:true, force:false}", calls)
	}
	if _, ok := d.sessions.Get(s.ID); ok {
		t.Error("killed session must be dropped from the store")
	}
	if d.inflight.Has(s.IssueUUID) {
		t.Error("kill must free the issue's in-flight claim")
	}
}

// force=true is threaded to the runtime.
func TestHandleKillForwardsForce(t *testing.T) {
	nat := &fakeNative{}
	d := newTestDaemon(t, nativeTestConfig(nativePoll("p1")), &linear.Fake{}, nat)
	s := nativeSess("FE-1", "working")
	d.sessions.Upsert(s)

	if _, err := d.handleKill(context.Background(), s.ID, true); err != nil {
		t.Fatalf("handleKill force: %v", err)
	}
	if calls := nat.killCalls(); len(calls) != 1 || !calls[0].force {
		t.Errorf("native Kill calls = %+v, want force:true", calls)
	}
}

// Dirty worktree: the agent is terminated but the worktree (and store entry)
// are kept, the session is flagged dead, and the error tells the user to rerun
// with --force. The in-flight claim is NOT freed (the worktree still exists).
func TestHandleKillDirtyKeepsEntryAndFlagsDead(t *testing.T) {
	nat := &fakeNative{killErr: worktree.ErrDirty}
	d := newTestDaemon(t, nativeTestConfig(nativePoll("p1")), &linear.Fake{}, nat)

	s := nativeSess("FE-1", "working")
	s.IssueUUID = "uuid-fe-1"
	d.sessions.Upsert(s)
	d.inflight.Add(s.IssueUUID, s.Issue)

	data, err := d.handleKill(context.Background(), s.ID, false)
	if err == nil {
		t.Fatal("handleKill on a dirty worktree must return an error")
	}
	if !strings.Contains(err.Error(), "--force") {
		t.Errorf("error %q must tell the user to rerun with --force", err)
	}
	if data.Removed {
		t.Error("KillData.Removed must be false on a dirty refusal")
	}
	got, ok := d.sessions.Get(s.ID)
	if !ok {
		t.Fatal("dirty kill must keep the store entry")
	}
	if got.Status != "dead" {
		t.Errorf("status = %q, want dead after a dirty kill", got.Status)
	}
	if !d.inflight.Has(s.IssueUUID) {
		t.Error("dirty kill must not free the in-flight claim (worktree still present)")
	}
}

// A clean kill in label mode strips the issue's set_label and drops its seen
// guard, so the reconcile pass no longer sees an orphaned set_label issue to
// silently re-queue the just-killed issue. Unrelated labels are preserved.
func TestHandleKillClearsLabelDispatch(t *testing.T) {
	fake := &linear.Fake{
		LabelIDsByIssue: map[string][]string{"uuid-fe-1": {"lbl-sent", "lbl-other"}},
	}
	d := newTestDaemon(t, nativeTestConfig(nativePoll("p1")), fake, &fakeNative{})

	s := nativeSess("FE-1", "working")
	s.IssueUUID = "uuid-fe-1"
	d.sessions.Upsert(s)

	// Seed a seen entry as dispatch would have.
	seen, _ := d.seen.load("p1")
	seen[s.IssueUUID] = time.Now()
	if err := d.seen.save("p1", seen); err != nil {
		t.Fatalf("seed seen: %v", err)
	}

	if _, err := d.handleKill(context.Background(), s.ID, false); err != nil {
		t.Fatalf("handleKill: %v", err)
	}

	got, err := fake.IssueLabelIDs(context.Background(), s.IssueUUID)
	if err != nil {
		t.Fatalf("IssueLabelIDs: %v", err)
	}
	if slices.Contains(got, "lbl-sent") {
		t.Errorf("kill must strip the set_label, got %v", got)
	}
	if !slices.Contains(got, "lbl-other") {
		t.Errorf("kill must preserve unrelated labels, got %v", got)
	}
	if seen, _ := d.seen.load("p1"); func() bool { _, ok := seen[s.IssueUUID]; return ok }() {
		t.Error("kill must drop the issue's seen entry")
	}
}

// The set_label strip is best-effort: a Linear failure never fails the kill —
// the agent teardown and worktree removal have already succeeded.
func TestHandleKillDurableIsBestEffort(t *testing.T) {
	fake := &linear.Fake{
		LabelIDsByIssue: map[string][]string{"uuid-fe-1": {"lbl-sent"}},
		Errs:            map[string]error{"SetIssueLabels": errors.New("linear down")},
	}
	d := newTestDaemon(t, nativeTestConfig(nativePoll("p1")), fake, &fakeNative{})

	s := nativeSess("FE-1", "working")
	s.IssueUUID = "uuid-fe-1"
	d.sessions.Upsert(s)

	data, err := d.handleKill(context.Background(), s.ID, false)
	if err != nil {
		t.Fatalf("a Linear label-write failure must not fail the kill: %v", err)
	}
	if !data.Removed {
		t.Error("kill must still report the worktree removed despite the Linear error")
	}
	if _, ok := d.sessions.Get(s.ID); ok {
		t.Error("kill must still drop the store entry despite the Linear error")
	}
}

func TestHandleKillUnknownSession(t *testing.T) {
	d := newTestDaemon(t, nativeTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeNative{})
	_, err := d.handleKill(context.Background(), "lola-p1-ghost", false)
	if err == nil || !strings.Contains(err.Error(), "unknown session") {
		t.Fatalf("handleKill unknown = %v, want an 'unknown session' error", err)
	}
}

// Project gone from config: terminate the agent (removeWorktree=false, so the
// runtime never touches git), drop the store entry and free the slot, and say
// the worktree was left untouched.
func TestHandleKillProjectMissingSkipsWorktree(t *testing.T) {
	nat := &fakeNative{}
	d := newTestDaemon(t, nativeTestConfig(nativePoll("p1")), &linear.Fake{}, nat)

	s := session.Session{
		ID: "lola-ghost-fe-1", Source: "native", Project: "ghost",
		Issue: "FE-1", IssueUUID: "uuid-fe-1", Status: "working", TmuxName: "lola-ghost-fe-1",
	}
	d.sessions.Upsert(s)
	d.inflight.Add(s.IssueUUID, s.Issue)

	data, err := d.handleKill(context.Background(), s.ID, false)
	if err != nil {
		t.Fatalf("handleKill project-missing: %v", err)
	}
	if data.Removed {
		t.Error("KillData.Removed must be false when the project is gone")
	}
	if !strings.Contains(data.Message, "config") {
		t.Errorf("message %q must explain the project is no longer in config", data.Message)
	}
	if calls := nat.killCalls(); len(calls) != 1 || calls[0].removeWorktree {
		t.Errorf("native Kill calls = %+v, want removeWorktree:false (no safe worktree target)", calls)
	}
	if _, ok := d.sessions.Get(s.ID); ok {
		t.Error("store entry must be dropped even when the project is gone")
	}
	if d.inflight.Has(s.IssueUUID) {
		t.Error("in-flight claim must be freed even when the project is gone")
	}
}
