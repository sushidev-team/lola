package daemon

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/you/aop/internal/linear"
)

// orphanIssue returns a label-poll orphan candidate: it carries the set
// label, has a branch, and its seen entry is older than orphanTimeout.
func orphanIssue() linear.Issue {
	is := testIssue("FE-231", 1, "2024-01-01T00:00:00Z")
	is.BranchName = "feat/fe-231"
	return is
}

func newReconcileDaemon(t *testing.T, fake *linear.Fake) *Daemon {
	t.Helper()
	d := newTestDaemon(t, testConfig(labelPoll("p1")), fake, &fakeAO{})
	return d
}

func seedOrphan(t *testing.T, d *Daemon, is linear.Issue) {
	t.Helper()
	if err := d.seen.save("p1", map[string]time.Time{is.ID: time.Now().Add(-2 * orphanTimeout)}); err != nil {
		t.Fatal(err)
	}
	d.inflight.Add(is.ID, is.Identifier)
}

func TestReconcileRevertsOrphanWithNoOpenPR(t *testing.T) {
	is := orphanIssue()
	fake := &linear.Fake{
		Issues:          []linear.Issue{is},
		LabelIDsByIssue: map[string][]string{is.ID: {"lbl-sent", "lbl-other"}},
	}
	d := newReconcileDaemon(t, fake)
	seedOrphan(t, d, is)
	d.openPR = func(context.Context, string) (bool, error) { return false, nil } // definitively no PR

	d.reconcilePoll(context.Background(), fake, labelPoll("p1"), map[string]bool{}, time.Now())

	// set_label reverted to the trigger label.
	want := []string{"lbl-other", "lbl-trigger"}
	if got := fake.LabelIDsByIssue[is.ID]; !reflect.DeepEqual(got, want) {
		t.Errorf("labels after revert = %v, want %v", got, want)
	}
	seen, err := d.seen.load("p1")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := seen[is.ID]; ok {
		t.Error("reverted orphan must be cleared from seen so it re-queues")
	}
	if d.inflight.Has(is.ID) {
		t.Error("reverted orphan must be cleared from in-flight")
	}
}

func TestReconcileOpenPRBlocksRevert(t *testing.T) {
	is := orphanIssue()
	fake := &linear.Fake{
		Issues:          []linear.Issue{is},
		LabelIDsByIssue: map[string][]string{is.ID: {"lbl-sent"}},
	}
	d := newReconcileDaemon(t, fake)
	seedOrphan(t, d, is)
	d.openPR = func(context.Context, string) (bool, error) { return true, nil } // held for review

	d.reconcilePoll(context.Background(), fake, labelPoll("p1"), map[string]bool{}, time.Now())

	if slices.Contains(fake.CallNames(), "SetIssueLabels") {
		t.Error("an issue with an open PR must not be reverted")
	}
	seen, err := d.seen.load("p1")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := seen[is.ID]; !ok {
		t.Error("seen entry must be kept while the PR is open")
	}
}

// The PR check must fail CLOSED: when `gh` cannot answer (not on PATH, cwd
// not the issue's repo), reconcile must NOT treat that as "no PR" and revert
// — that would re-dispatch every issue whose PR is held for review.
func TestReconcilePRCheckErrorFailsClosed(t *testing.T) {
	is := orphanIssue()
	fake := &linear.Fake{
		Issues:          []linear.Issue{is},
		LabelIDsByIssue: map[string][]string{is.ID: {"lbl-sent"}},
	}
	d := newReconcileDaemon(t, fake)
	seedOrphan(t, d, is)
	d.openPR = func(context.Context, string) (bool, error) {
		return false, errors.New("gh: not a git repository")
	}

	d.reconcilePoll(context.Background(), fake, labelPoll("p1"), map[string]bool{}, time.Now())

	if slices.Contains(fake.CallNames(), "SetIssueLabels") {
		t.Error("PR-check failure must skip the revert (fail closed), labels were mutated")
	}
	seen, err := d.seen.load("p1")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := seen[is.ID]; !ok {
		t.Error("seen entry must be kept when the PR state is unknown")
	}
	if !d.inflight.Has(is.ID) {
		t.Error("in-flight claim must be kept when the PR state is unknown")
	}
}

func TestReconcileCountedSessionBlocksRevert(t *testing.T) {
	is := orphanIssue()
	fake := &linear.Fake{
		Issues:          []linear.Issue{is},
		LabelIDsByIssue: map[string][]string{is.ID: {"lbl-sent"}},
	}
	d := newReconcileDaemon(t, fake)
	seedOrphan(t, d, is)
	d.openPR = func(context.Context, string) (bool, error) { return false, nil }

	counted := map[string]bool{is.Identifier: true} // live AO session
	d.reconcilePoll(context.Background(), fake, labelPoll("p1"), counted, time.Now())

	if slices.Contains(fake.CallNames(), "SetIssueLabels") {
		t.Error("an issue with a counted AO session must not be reverted")
	}
}

// reconcilePoll and ticks both load-modify-save the poll's seen map; they
// must serialize on the poll's tick mutex or updates are lost.
func TestReconcilePollWaitsForTickMutex(t *testing.T) {
	is := orphanIssue()
	fake := &linear.Fake{
		Issues:          []linear.Issue{is},
		LabelIDsByIssue: map[string][]string{is.ID: {"lbl-sent"}},
	}
	d := newReconcileDaemon(t, fake)
	seedOrphan(t, d, is)
	d.openPR = func(context.Context, string) (bool, error) { return false, nil }

	mu := d.tickMutex("p1")
	mu.Lock()
	done := make(chan struct{})
	go func() {
		d.reconcilePoll(context.Background(), fake, labelPoll("p1"), map[string]bool{}, time.Now())
		close(done)
	}()
	select {
	case <-done:
		t.Fatal("reconcilePoll must wait for the poll's tick mutex")
	case <-time.After(50 * time.Millisecond):
	}
	mu.Unlock()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("reconcilePoll did not finish after the tick mutex was released")
	}
}
