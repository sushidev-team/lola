package daemon

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/sushidev-team/lola/internal/linear"
)

// A dead session is relaunched: Revive is called once, the store entry flips
// back to working, and the issue's in-flight claim is re-established so the poll
// cannot dispatch a SECOND agent for it alongside the revived one.
func TestHandleReviveRelaunchesDeadSession(t *testing.T) {
	nat := &fakeNative{}
	d := newTestDaemon(t, nativeTestConfig(nativePoll("p1")), &linear.Fake{}, nat)
	s := nativeSess("FE-1", "dead")
	s.IssueUUID = "uuid-fe-1"
	d.sessions.Upsert(s)

	data, err := d.handleRevive(context.Background(), s.ID)
	if err != nil {
		t.Fatalf("handleRevive: %v", err)
	}
	if !data.Revived {
		t.Error("ReviveData.Revived = false, want true")
	}
	if calls := nat.reviveCalls(); len(calls) != 1 || calls[0] != s.ID {
		t.Errorf("Revive calls = %v, want [%s]", calls, s.ID)
	}
	after, ok := d.sessions.Get(s.ID)
	if !ok {
		t.Fatal("session vanished after revive")
	}
	if after.Status != "working" {
		t.Errorf("status after revive = %q, want working", after.Status)
	}
	if !d.inflight.Has(s.IssueUUID) {
		t.Error("revive must re-establish the in-flight claim so the issue is not double-dispatched")
	}
}

// A session whose pane is still alive must NOT be revived — that would launch a
// second agent into the same worktree (the send-keys corruption class).
func TestHandleReviveRefusesLiveSession(t *testing.T) {
	nat := &fakeNative{}
	d := newTestDaemon(t, nativeTestConfig(nativePoll("p1")), &linear.Fake{}, nat)
	s := nativeSess("FE-2", "working")
	nat.alive = map[string]bool{s.ID: true}
	d.sessions.Upsert(s)

	if _, err := d.handleRevive(context.Background(), s.ID); err == nil {
		t.Fatal("handleRevive must refuse a session that is already alive")
	}
	if calls := nat.reviveCalls(); len(calls) != 0 {
		t.Errorf("a live session must not be revived; calls=%v", calls)
	}
}

func TestHandleReviveUnknownSession(t *testing.T) {
	d := newTestDaemon(t, nativeTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeNative{})
	if _, err := d.handleRevive(context.Background(), "does-not-exist"); err == nil {
		t.Fatal("handleRevive must error on an unknown session")
	}
}

func TestHandleReviveUnsupportedCodexLeavesSessionUnchanged(t *testing.T) {
	nat := &fakeNative{}
	d := newTestDaemon(t, nativeTestConfig(nativePoll("p1")), &linear.Fake{}, nat)
	s := nativeSess("FE-3", "dead")
	s.Agent = "codex"
	s.IssueUUID = "uuid-fe-3"
	d.sessions.Upsert(s)
	before, _ := d.sessions.Get(s.ID)
	var checked string
	d.runtimeHealth = func(binary string) error {
		checked = binary
		return errors.New("Codex does not support --approve-for-me; upgrade Codex CLI")
	}
	data, err := d.handleRevive(context.Background(), s.ID)
	if err == nil || !strings.Contains(err.Error(), "upgrade Codex CLI") || data.Revived {
		t.Fatalf("unsupported Codex must fail revive: %+v, %v", data, err)
	}
	if checked != "codex" {
		t.Fatalf("health check used %q, want persisted session agent codex", checked)
	}
	if calls := nat.reviveCalls(); len(calls) != 0 {
		t.Fatalf("unsupported Codex must not launch: %v", calls)
	}
	after, ok := d.sessions.Get(s.ID)
	if !ok || !reflect.DeepEqual(before, after) {
		t.Fatalf("failed health check must preserve the session: before=%+v after=%+v", before, after)
	}
	if d.inflight.Has(s.IssueUUID) {
		t.Fatal("failed health check must not establish an in-flight claim")
	}
}
