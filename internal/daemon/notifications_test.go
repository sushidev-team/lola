package daemon

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sushidev-team/lola/internal/notify"
	"github.com/sushidev-team/lola/internal/scm"
	"github.com/sushidev-team/lola/internal/session"
	"github.com/sushidev-team/lola/internal/state"
)

func notificationFixture(t *testing.T) (*Daemon, *recordingNotifier, session.Session, scm.PRReadiness) {
	t.Helper()
	d := prsDaemon(t)
	n := &recordingNotifier{}
	d.notifier = n
	s := session.Session{ID: "s", Issue: "APP-42", Project: "alpha", Source: "native", TmuxName: "s", Repo: "acme/app",
		AgentState: state.AgentIdle, AtPrompt: true, AtPromptVerified: true, Delivery: state.DeliveryApproved,
		PR: &scm.PR{Number: 42, State: "OPEN", HeadSHA: headA, BaseSHA: headB}}
	pr := readyToMerge()
	pr.ChecksHead, pr.URL = pr.Head, "https://github.com/acme/app/pull/42"
	d.sessions.Upsert(s)
	d.prReadiness = func(context.Context, string, int) (scm.PRReadiness, error) { return pr, nil }
	return d, n, s, pr
}

func TestReadyNotificationRequiresFinishedWork(t *testing.T) {
	for _, mode := range []string{"ready", "no-required-review", "no-checks", "working", "starting", "question", "dead", "draft", "closed", "ci-pending", "ci-failed", "ci-unknown", "checks-wrong-head", "unknown-merge", "blocked", "required-review", "changes-requested", "threads", "outdated-threads", "fetch-error", "wrong-pr", "wrong-head", "wrong-base", "missing-base", "stale", "handoff", "feedback", "broadcast", "ssh", "reaction", "goal", "local", "bestof", "busy-review", "review-not-run", "plain-findings", "review-gate", "resumed-during-fetch", "new-pr-during-fetch", "queue"} {
		t.Run(mode, func(t *testing.T) {
			d, n, s, pr := notificationFixture(t)
			var fetchErr error
			switch mode {
			case "no-required-review":
				s.Delivery, pr.Review = state.DeliveryReviewPending, ""
			case "no-checks":
				pr.Checks = "none"
			case "working":
				s.AgentState = state.AgentWorking
			case "starting":
				s.AgentState = state.AgentStarting
			case "question":
				s.AgentState, s.InputReason = state.AgentWaitingInput, state.InputQuestion
			case "dead":
				s.AgentState = state.AgentDead
			case "draft":
				pr.Draft = true
			case "closed":
				pr.State = "CLOSED"
			case "ci-pending":
				pr.Checks = "pending"
			case "ci-failed":
				pr.Checks = "fail"
			case "ci-unknown":
				pr.Checks = "unknown"
			case "checks-wrong-head":
				pr.ChecksHead = "old"
			case "unknown-merge":
				pr.Mergeable = "UNKNOWN"
			case "blocked":
				pr.MergeState = "BLOCKED"
			case "required-review":
				pr.Review = "REVIEW_REQUIRED"
			case "changes-requested":
				pr.Review = "CHANGES_REQUESTED"
			case "threads", "outdated-threads":
				pr.Threads = []scm.ReviewThread{{ID: "t", Outdated: mode == "outdated-threads"}}
			case "fetch-error":
				fetchErr = errors.New("offline")
			case "wrong-pr":
				pr.Number++
			case "wrong-head":
				pr.Head = "new"
			case "wrong-base":
				pr.Base = "new"
			case "missing-base":
				pr.Base = ""
			case "stale":
				s.PRFetchFailures = 1
			case "handoff":
				s.PendingHandoffs = map[string]string{"review": "fix"}
			case "feedback":
				s.PendingFeedback = "fix"
			case "broadcast":
				s.PendingBroadcast = "fix"
			case "ssh":
				s.PendingSSH = "fix"
			case "reaction":
				s.PendingReaction = "ci_failed"
			case "goal":
				s.GoalID = "g"
			case "local":
				s.Kind = session.KindLocal
			case "bestof":
				s.BestOf = "g"
			case "busy-review":
				d.reviewBusy = map[string]bool{"s|codex-session": true}
			case "review-not-run", "plain-findings", "review-gate":
				d.reviewProviders = []reviewProvider{{Kind: "codex-session", Enabled: true, Shape: shapePass, OnPROpen: true, Status: mode == "review-gate"}}
				if mode != "review-not-run" {
					s.ReviewTallies = map[string]session.ReviewTally{"codex-session": {PR: 42, Major: 1}}
				}
				if mode == "review-gate" {
					s.InlineReviewPRs = map[string]int{"codex-session": 42}
				}
			case "queue":
				d.cfg.MergeQueue.Enabled = true
				s.Branch = "lola/APP-42"
				s.PR.ReviewDecision = "APPROVED"
			}
			d.sessions.Upsert(s)
			d.prReadiness = func(context.Context, string, int) (scm.PRReadiness, error) {
				if mode == "resumed-during-fetch" || mode == "new-pr-during-fetch" {
					d.sessions.Update(s.ID, func(cur *session.Session) bool {
						if mode == "resumed-during-fetch" {
							cur.SetAgentState(state.AgentWorking, state.SourceHook, time.Now())
						} else {
							cur.PR.Number++
						}
						return true
					})
				}
				return pr, fetchErr
			}
			d.notifyUserReady(context.Background(), s.ID)
			ready := 0
			for _, note := range n.notes() {
				if note.ReadyForUser {
					ready++
				}
			}
			want := 0
			if mode == "ready" || mode == "no-required-review" || mode == "no-checks" {
				want = 1
			}
			if ready != want {
				t.Fatalf("ready notifications = %d, want %d: %+v", ready, want, n.notes())
			}
		})
	}
}

func TestReadyNotificationsDeduplicateAndFollowNewCommits(t *testing.T) {
	d, n, s, pr := notificationFixture(t)
	d.notifyUserReady(context.Background(), s.ID)
	d.notifyUserReady(context.Background(), s.ID)
	if len(n.notes()) != 1 {
		t.Fatal("repeated ready alert")
	}
	// Reload the actual persisted store: the guard is not just an in-memory cache.
	d.sessions = session.NewStore(filepath.Join(d.home, "state"))
	d.notifyUserReady(context.Background(), s.ID)
	if len(n.notes()) != 1 {
		t.Fatal("restart repeated ready alert")
	}
	// Other PRs landing on main must not repeatedly announce this same head.
	d.sessions.Update(s.ID, func(cur *session.Session) bool { cur.PR.BaseSHA = "new-base"; return true })
	pr.Base = "new-base"
	d.prReadiness = func(context.Context, string, int) (scm.PRReadiness, error) { return pr, nil }
	d.notifyUserReady(context.Background(), s.ID)
	if len(n.notes()) != 1 {
		t.Fatal("base movement repeated ready alert")
	}
	pr.Head, pr.ChecksHead = "new", "new"
	d.sessions.Update(s.ID, func(cur *session.Session) bool { cur.PR.HeadSHA = "new"; return true })
	d.prReadiness = func(context.Context, string, int) (scm.PRReadiness, error) { return pr, nil }
	d.notifyUserReady(context.Background(), s.ID)
	if len(n.notes()) != 2 {
		t.Fatal("new commit did not get a ready alert")
	}
}

func TestReadyNotificationNeedsConfirmedIdle(t *testing.T) {
	for _, mode := range []string{"unverified", "no-prompt", "held"} {
		t.Run(mode, func(t *testing.T) {
			d, n, s, _ := notificationFixture(t)
			switch mode {
			case "unverified":
				s.AtPromptVerified = false
			case "no-prompt":
				s.AtPrompt = false
			case "held":
				s.QueueHeld = true
			}
			d.sessions.Upsert(s)
			d.notifyUserReady(context.Background(), s.ID)
			if len(n.notes()) != 0 {
				t.Fatalf("unconfirmed completion notified: %+v", n.notes())
			}
		})
	}
}

func TestAgentInputNotificationOncePerEpisode(t *testing.T) {
	for _, reason := range []state.InputReason{state.InputQuestion, state.InputPermission, state.InputDialog, state.InputIdleNotify, state.InputQuotaLimited, ""} {
		t.Run(string(reason), func(t *testing.T) {
			d, n, s, _ := notificationFixture(t)
			s.AgentState, s.InputReason, s.LastNotification = state.AgentWaitingInput, reason, "secret command"
			d.sessions.Upsert(s)
			d.notifyUserReady(context.Background(), s.ID)
			d.notifyUserReady(context.Background(), s.ID)
			want := 0
			if reason == state.InputQuestion || reason == state.InputPermission || reason == state.InputDialog {
				want = 1
			}
			if len(n.notes()) != want {
				t.Fatalf("notes = %+v", n.notes())
			}
			if want == 0 {
				return
			}
			note := n.notes()[0]
			if note.Priority != notify.Urgent || strings.Contains(note.Body, "secret command") {
				t.Fatalf("note: %+v", note)
			}
			d.sessions = session.NewStore(filepath.Join(d.home, "state"))
			d.notifyUserReady(context.Background(), s.ID)
			if len(n.notes()) != 1 {
				t.Fatal("restart repeated input alert")
			}
			d.sessions.Update(s.ID, func(cur *session.Session) bool {
				cur.SetAgentState(state.AgentWorking, state.SourceHook, time.Now())
				cur.SetAgentState(state.AgentWaitingInput, "", time.Now())
				cur.InputReason = reason
				return true
			})
			d.notifyUserReady(context.Background(), s.ID)
			if len(n.notes()) != 2 {
				t.Fatal("new question episode was suppressed")
			}
		})
	}
}

func TestObserverAnnouncesReadyAfterFreshReadiness(t *testing.T) {
	d, n, s, pr := notificationFixture(t)
	s.Branch = "feature"
	s.PR.Mergeable, s.PR.ReviewDecision, s.PR.ChecksState = "MERGEABLE", "APPROVED", "pass"
	s.AtPrompt = true
	d.sessions.Upsert(s)
	d.native = &fakeNative{alive: map[string]bool{s.ID: true}}
	d.listTmuxSessions = nil
	d.paneTail = func(context.Context, string, int) (string, error) { return "", nil }
	(&fakeObsSeams{pr: s.PR}).install(d)
	d.requiredChecks = nil
	pr.Threads = []scm.ReviewThread{{ID: "unresolved"}}
	d.prReadiness = func(context.Context, string, int) (scm.PRReadiness, error) { return pr, nil }
	d.observeNative(context.Background())
	for _, note := range n.notes() {
		if note.ReadyForUser {
			t.Fatal("approved alone announced readiness")
		}
	}
	pr.Threads = nil
	d.observeNative(context.Background())
	d.observeNative(context.Background())
	ready := 0
	for _, note := range n.notes() {
		if note.ReadyForUser {
			ready++
		}
	}
	if ready != 1 {
		t.Fatalf("observer ready alerts = %d: %+v", ready, n.notes())
	}
}

func TestManualReviewHoldsReadyNotificationUntilItFinishes(t *testing.T) {
	d, n, s, _ := notificationFixture(t)
	p := reviewProvider{Kind: "codex-session", Enabled: true, Shape: shapePass}
	d.setPassRun(p.Kind, func(context.Context, string, string, string) (string, error) {
		d.notifyUserReady(context.Background(), s.ID)
		if len(n.notes()) != 0 {
			t.Fatal("announced readiness during a manual review")
		}
		return "", nil
	})
	result := d.reviewPass(context.Background(), s, p, false)
	if !result.Ran {
		t.Fatalf("review did not run: %+v", result)
	}
	d.notifyUserReady(context.Background(), s.ID)
	if len(n.notes()) != 1 || !n.notes()[0].ReadyForUser {
		t.Fatalf("ready after review: %+v", n.notes())
	}
}

func TestReadyNotificationStackLanes(t *testing.T) {
	d, n, s, pr := notificationFixture(t)
	s.Stack = true
	s.Apps = []session.App{{Name: "web", Repo: s.Repo, PR: s.PR, Delivery: s.Delivery}, {Name: "api", Repo: "acme/api", PR: s.PR, Delivery: s.Delivery}}
	d.sessions.Upsert(s)
	d.prReadiness = func(context.Context, string, int) (scm.PRReadiness, error) { return pr, nil }
	d.notifyUserReady(context.Background(), s.ID)
	d.notifyUserReady(context.Background(), s.ID)
	if len(n.notes()) != 2 {
		t.Fatalf("lane alerts: %+v", n.notes())
	}
	root, _ := d.sessions.Get(s.ID)
	if root.ReadyNotified != "" || root.Apps[0].ReadyNotified == "" || root.Apps[1].ReadyNotified == "" {
		t.Fatalf("guards not lane scoped: %+v", root.Apps)
	}
}
