package daemon

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/sushidev-team/lola/internal/linear"
	"github.com/sushidev-team/lola/internal/session"
)

const draftPane = "────────────\n❯ Check PR comments and resolve th\n────────────\n"

func TestAutomaticReactionPreservesHumanInputAndRetries(t *testing.T) {
	for _, reason := range []string{"attached", "draft", "attachment query failed", "capture failed", "cached idle but working", "cached idle but modal", "attaches during capture", "draft during detail fetch"} {
		t.Run(reason, func(t *testing.T) {
			d := newTestDaemon(t, reactTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeNative{})
			seams := &fakeReactSeams{failing: "logs"}
			seams.install(d)
			s := reactSess("FE-1", "ci_failed", openPR(7, "MERGEABLE", "", "fail"))
			s.AtPrompt, s.AtPromptVerified = true, true
			d.sessions.Upsert(s)
			attached, pane := false, paneWaiting
			d.promptAttached = func(context.Context, string) (bool, error) {
				if reason == "attachment query failed" {
					return false, errors.New("tmux unavailable")
				}
				return attached, nil
			}
			d.paneTail = func(context.Context, string, int) (string, error) {
				if reason == "capture failed" {
					return "", errors.New("no pane")
				}
				if reason == "attaches during capture" {
					attached = true
				}
				return pane, nil
			}
			switch reason {
			case "attached":
				attached = true
			case "draft":
				pane = draftPane
			case "cached idle but working":
				pane = paneWorking
			case "cached idle but modal":
				pane = paneModal
			case "draft during detail fetch":
				d.failingChecks = func(context.Context, string, int) (string, error) { pane = draftPane; return "logs", nil }
			}
			d.react(context.Background(), s)
			d.react(context.Background(), s)
			got, _ := d.sessions.Get(s.ID)
			if len(seams.sendCalls()) != 0 || got.PendingReaction != "ci_failed" || got.CIRetries != 0 || got.LastReactedStatus != "" || !got.AtPrompt {
				t.Fatalf("deferred delivery consumed input or guards: sends=%v session=%+v", seams.sendCalls(), got)
			}
			if len(seams.notes) > 1 {
				t.Fatal("duplicate deferral notifications")
			}
			// The same queued reaction delivers once after ownership and the
			// draft are cleared. No timeout ever deletes the draft.
			d.promptAttached = func(context.Context, string) (bool, error) { return false, nil }
			d.paneTail = func(context.Context, string, int) (string, error) { return paneWaiting, nil }
			d.react(context.Background(), got)
			got, _ = d.sessions.Get(s.ID)
			d.react(context.Background(), got)
			if len(seams.sendCalls()) != 1 || got.PendingReaction != "" || got.CIRetries != 1 || got.AutomationDeferredReason != "" {
				t.Fatalf("queue did not drain once: sends=%v session=%+v", seams.sendCalls(), got)
			}
		})
	}
}

func TestQueuedFeedbackPreservesDraft(t *testing.T) {
	d := newTestDaemon(t, conflictConfig(), &linear.Fake{}, &fakeNative{})
	seams := &fakeReactSeams{}
	seams.install(d)
	id := feedbackSess(t, d, "FE-1", "claude")
	d.paneTail = func(context.Context, string, int) (string, error) { return draftPane, nil }
	data, err := d.handleFeedback(context.Background(), oneComment(id))
	if err != nil || !data.Queued {
		t.Fatalf("feedback = %+v, %v", data, err)
	}
	s, _ := d.sessions.Get(id)
	if s.PendingFeedback == "" || !s.AtPrompt || len(seams.sendCalls()) != 0 {
		t.Fatal("feedback lost or typed over draft")
	}
	d.paneTail = func(context.Context, string, int) (string, error) { return paneWaiting, nil }
	if !d.deliverFeedback(context.Background(), id) || d.deliverFeedback(context.Background(), id) {
		t.Fatal("feedback must drain once")
	}
}

func TestReviewHandoffPreservesDraft(t *testing.T) {
	d := newTestDaemon(t, reviewTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeNative{})
	syncProviders(d)
	seams := &fakeReactSeams{}
	seams.install(d)
	fr := &fakeReview{findings: "QUEUED-FINDING"}
	fr.install(d)
	s := reactSess("FE-1", "review_pending", openPR(7, "MERGEABLE", "", "pass"))
	s.AtPrompt, s.AtPromptVerified = true, true
	d.sessions.Upsert(s)
	d.paneTail = func(context.Context, string, int) (string, error) { return draftPane, nil }
	d.runReviewProviders(context.Background(), s)
	got, _ := d.sessions.Get(s.ID)
	if len(got.PendingHandoffs) == 0 || len(seams.sendCalls()) != 0 || !got.AtPrompt {
		t.Fatal("review lost or typed over draft")
	}
	d.paneTail = func(context.Context, string, int) (string, error) { return paneWaiting, nil }
	d.flushReviewHandoffs(context.Background(), s.ID)
	d.flushReviewHandoffs(context.Background(), s.ID)
	if len(seams.sendCalls()) != 1 || !strings.Contains(seams.sendCalls()[0].text, "QUEUED-FINDING") {
		t.Fatal("review must drain once")
	}
}

func TestMergeQueueSyncWaitsForHumanToDetach(t *testing.T) {
	d, gh, seams := queueDaemon(t)
	id := queueSess(d, gh, "FE-1", greenPR(5, sha(5)))
	gh.behind[sha(5)] = 1
	d.promptAttached = func(context.Context, string) (bool, error) { return true, nil }
	d.runMergeQueue(context.Background())
	s, _ := d.sessions.Get(id)
	if len(seams.sendCalls()) != 0 || s.MergeQueueGuard != nil || s.MergeQueueStep != mqWaiting {
		t.Fatal("sync must wait without stamping its guard")
	}
	d.promptAttached = func(context.Context, string) (bool, error) { return false, nil }
	d.runMergeQueue(context.Background())
	if len(seams.sendCalls()) != 1 {
		t.Fatal("sync did not resume")
	}
}

func TestReactionDoesNotSendAConflictThatClearedDuringProof(t *testing.T) {
	d := newTestDaemon(t, reactTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeNative{})
	seams := &fakeReactSeams{}
	seams.install(d)
	id := conflictSess(t, d, "FE-1")
	d.sessions.Update(id, func(cur *session.Session) bool { cur.AtPromptVerified = true; return true })
	s, _ := d.sessions.Get(id)
	d.paneTail = func(context.Context, string, int) (string, error) {
		d.sessions.Update(id, func(cur *session.Session) bool { cur.SetDelivery("review_pending", cur.PRObservedAt); return true })
		return paneWaiting, nil
	}
	d.react(context.Background(), s)
	if len(seams.sendCalls()) != 0 {
		t.Fatal("sent obsolete conflict instruction")
	}
}

func TestAutomaticPromptProofWaitsForRestore(t *testing.T) {
	d := newTestDaemon(t, reactTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeNative{})
	seams := &fakeReactSeams{}
	seams.install(d)
	s := reactSess("FE-1", "ci_failed", openPR(7, "MERGEABLE", "", "fail"))
	s.AtPrompt, s.AtPromptVerified = true, true
	d.sessions.Upsert(s)
	fetched := make(chan struct{})
	d.failingChecks = func(context.Context, string, int) (string, error) {
		close(fetched)
		return "logs", nil
	}
	// The restore owns the gate while a human starts a draft. The sender must
	// read the pane AFTER the restore releases it, never trust an earlier read.
	gate := d.sendGate(s.ID)
	gate.Lock()
	done := make(chan struct{})
	go func() { d.react(context.Background(), s); close(done) }()
	select {
	case <-fetched:
	case <-time.After(5 * time.Second):
		gate.Unlock()
		t.Fatal("reaction never fetched details")
	}
	d.paneTail = func(context.Context, string, int) (string, error) { return draftPane, nil }
	gate.Unlock()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("reaction deadlocked behind restore")
	}
	got, _ := d.sessions.Get(s.ID)
	if len(seams.sendCalls()) != 0 || got.PendingReaction != "ci_failed" || !got.AtPrompt {
		t.Fatal("reaction used a proof from before the restore")
	}
}
