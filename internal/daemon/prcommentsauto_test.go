package daemon

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sushidev-team/lola/internal/linear"
	"github.com/sushidev-team/lola/internal/scm"
	"github.com/sushidev-team/lola/internal/session"
	"github.com/sushidev-team/lola/internal/state"
)

func commentAutoDaemon(t *testing.T) (*Daemon, *fakeReactSeams, string) {
	t.Helper()
	cfg := nativeTestConfig(nativePoll("p1"))
	cfg.Projects[0].AutoPRComments = true
	d := newTestDaemon(t, cfg, &linear.Fake{}, &fakeNative{})
	f := &fakeReactSeams{}
	f.install(d)
	d.promptAttached = func(context.Context, string) (bool, error) { return false, nil }
	s := reactSess("FE-1", "idle", openPR(7, "MERGEABLE", "", "pass"))
	s.SetAgentState(state.AgentIdle, "", time.Now())
	d.sessions.Upsert(s)
	d.prReadiness = func(context.Context, string, int) (scm.PRReadiness, error) {
		return scm.PRReadiness{Number: 7, State: "OPEN", Threads: []scm.ReviewThread{{ID: "one", Body: "untrusted finding"}, {ID: "two"}}}, nil
	}
	return d, f, s.ID
}

func idleComments(d *Daemon, id string) {
	d.sessions.Update(id, func(s *session.Session) bool { s.SetAgentState(state.AgentIdle, "", time.Now()); return true })
}

func TestAutoPRCommentsOncePerThreadRevision(t *testing.T) {
	d, f, id := commentAutoDaemon(t)
	ctx := context.Background()
	if !d.autoPRComments(ctx, id) {
		t.Fatal("did not send")
	}
	msg := f.sendCalls()[0].text
	for _, part := range []string{"validate each finding", "commit and push", "leave it open", "Do not merge", "resolveReviewThread"} {
		if !strings.Contains(msg, part) {
			t.Fatalf("missing %s", part)
		}
	}
	if strings.Contains(msg, "untrusted finding") {
		t.Fatal("copied untrusted data into instructions")
	}
	// Restart the store: the guard must survive and resolving one thread must
	// not cause the remaining disputed thread to be sent again.
	d.sessions = session.NewStore(filepath.Join(d.home, "state"))
	idleComments(d, id)
	d.prReadiness = func(context.Context, string, int) (scm.PRReadiness, error) {
		return scm.PRReadiness{Number: 7, State: "OPEN", Head: "new head", Threads: []scm.ReviewThread{{ID: "two", Outdated: true}}}, nil
	}
	if d.autoPRComments(ctx, id) {
		t.Fatal("repeated unresolved thread after restart")
	}
	d.prReadiness = func(context.Context, string, int) (scm.PRReadiness, error) {
		return scm.PRReadiness{Number: 7, State: "OPEN", Threads: []scm.ReviewThread{{ID: "two", Body: "edited finding"}}}, nil
	}
	if !d.autoPRComments(ctx, id) || len(f.sendCalls()) != 2 {
		t.Fatal("edited finding not delivered")
	}
}

func TestAutoPRCommentsDefersSafely(t *testing.T) {
	for _, reason := range []string{"disabled", "busy", "permission", "draft", "attached", "hold", "closed", "error", "empty", "changed PR", "disabled during fetch", "changed during proof", "goal", "local"} {
		t.Run(reason, func(t *testing.T) {
			d, f, id := commentAutoDaemon(t)
			fetch := d.prReadiness
			switch reason {
			case "disabled":
				d.cfg.Projects[0].AutoPRComments = false
			case "busy":
				d.sessions.Update(id, func(s *session.Session) bool { s.SetAgentState(state.AgentWorking, "", time.Now()); return true })
			case "permission":
				d.sessions.Update(id, func(s *session.Session) bool {
					s.SetAgentState(state.AgentWaitingInput, "", time.Now())
					s.InputReason = state.InputPermission
					return true
				})
			case "draft":
				d.paneTail = func(context.Context, string, int) (string, error) { return draftPane, nil }
			case "attached":
				d.promptAttached = func(context.Context, string) (bool, error) { return true, nil }
			case "hold":
				d.sessions.Update(id, func(s *session.Session) bool { s.StopHoldUntil = time.Now().Add(time.Hour); return true })
			case "goal":
				d.sessions.Update(id, func(s *session.Session) bool { s.Kind = session.KindGoal; return true })
			case "local":
				d.sessions.Update(id, func(s *session.Session) bool { s.Kind = session.KindLocal; return true })
			case "changed during proof":
				d.paneTail = func(context.Context, string, int) (string, error) {
					d.sessions.Update(id, func(s *session.Session) bool { s.PR.Number++; return true })
					return paneWaiting, nil
				}
			default:
				d.prReadiness = func(ctx context.Context, repo string, n int) (scm.PRReadiness, error) {
					pr, _ := fetch(ctx, repo, n)
					switch reason {
					case "closed":
						pr.State = "CLOSED"
					case "error":
						return pr, errors.New("offline")
					case "empty":
						pr.Threads = nil
					case "changed PR":
						pr.Number++
					case "disabled during fetch":
						d.cfg.Projects[0].AutoPRComments = false
					}
					return pr, nil
				}
			}
			if d.autoPRComments(context.Background(), id) || len(f.sendCalls()) != 0 {
				t.Fatal("unsafe send")
			}
			s, _ := d.sessions.Get(id)
			if len(s.PRCommentGuards) != 0 {
				t.Fatal("deferral consumed guard")
			}
		})
	}
}

func TestAutoPRCommentsStackLanesShareOnePrompt(t *testing.T) {
	d, f, id := commentAutoDaemon(t)
	d.sessions.Update(id, func(s *session.Session) bool {
		s.Stack = true
		s.Apps = []session.App{
			{Name: "web", Repo: "acme/web", PR: openPR(7, "MERGEABLE", "", "pass")},
			{Name: "api", Repo: "acme/api", PR: openPR(8, "MERGEABLE", "", "pass")},
		}
		return true
	})
	d.prReadiness = func(_ context.Context, _ string, n int) (scm.PRReadiness, error) {
		return scm.PRReadiness{Number: n, State: "OPEN", Threads: []scm.ReviewThread{{ID: "thread"}}}, nil
	}
	ctx := context.Background()
	if !d.autoPRComments(ctx, id) || len(f.sendCalls()) != 1 {
		t.Fatal("expected one lane handoff")
	}
	if d.autoPRComments(ctx, id) {
		t.Fatal("typed into busy shared prompt")
	}
	idleComments(d, id)
	if !d.autoPRComments(ctx, id) || !strings.Contains(f.sendCalls()[1].text, "acme/api PR #8") {
		t.Fatal("second lane did not receive its own PR")
	}
	d.sessions = session.NewStore(filepath.Join(d.home, "state"))
	idleComments(d, id)
	if d.autoPRComments(ctx, id) {
		t.Fatal("lane guards did not persist")
	}
}

func TestAutoPRCommentsNewThreadAndUncertainSend(t *testing.T) {
	d, f, id := commentAutoDaemon(t)
	ctx := context.Background()
	d.autoPRComments(ctx, id)
	idleComments(d, id)
	d.prReadiness = func(context.Context, string, int) (scm.PRReadiness, error) {
		return scm.PRReadiness{Number: 7, State: "OPEN", Threads: []scm.ReviewThread{{ID: "new"}}}, nil
	}
	f.sendErr = errors.New("uncertain transport")
	if !d.autoPRComments(ctx, id) || len(f.sendCalls()) != 2 {
		t.Fatal("new thread not attempted")
	}
	idleComments(d, id)
	if d.autoPRComments(ctx, id) || len(f.sendCalls()) != 2 {
		t.Fatal("uncertain send repeated")
	}
}
