package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/scm"
	"github.com/sushidev-team/lola/internal/session"
	"github.com/sushidev-team/lola/internal/state"
)

func feedbackReq(cmd, id string, n int) protocol.Request {
	raw, _ := json.Marshal(protocol.PRReadinessArgs{Number: n})
	return protocol.Request{Cmd: cmd, Session: id, Args: raw}
}
func TestPRReadinessUsesExactLane(t *testing.T) {
	d := prsDaemon(t)
	d.sessions.Upsert(session.Session{ID: "s", Stack: true, Apps: []session.App{{Name: "web", Repo: "acme/web", PR: &scm.PR{Number: 7}}, {Name: "app", Repo: "acme/app", PR: &scm.PR{Number: 8}}}})
	calls := 0
	d.prReadiness = func(_ context.Context, repo string, n int) (scm.PRReadiness, error) {
		calls++
		if repo != "acme/app" || n != 8 {
			t.Fatalf("wrong PR %s %d", repo, n)
		}
		return scm.PRReadiness{Number: n, State: "OPEN", Base: "base", Head: "head", ChecksHead: "head", Threads: []scm.ReviewThread{{ID: "t"}}}, nil
	}
	for _, req := range []protocol.Request{feedbackReq("prReadiness", "s", 8), feedbackReq("prReadiness", "s~app", 7), feedbackReq("prReadiness", "missing", 8)} {
		if r := d.handle(context.Background(), req); r.OK {
			t.Fatalf("accepted invalid target: %+v", req)
		}
	}
	r := d.handle(context.Background(), feedbackReq("prReadiness", "s~app", 8))
	if !r.OK {
		t.Fatal(r.Error)
	}
	var got protocol.PRReadinessData
	if err := json.Unmarshal(r.Data, &got); err != nil || got.Repo != "acme/app" || len(got.Threads) != 1 || calls != 1 {
		t.Fatalf("%s calls=%d err=%v", r.Data, calls, err)
	}
	if got.Base != "base" || got.Head != "head" || got.ChecksHead != "head" || got.ObservedAt.IsZero() {
		t.Fatalf("lost observed revision identity: %+v", got)
	}
}
func TestPRReadinessAddressValidatesThenQueuesSafeWorkflow(t *testing.T) {
	d, f := talkFakeDaemon(t)
	talkSession(d, "s1", state.AgentIdle)
	d.sessions.Update("s1", func(s *session.Session) bool { s.Repo = "acme/app"; s.PR = &scm.PR{Number: 42}; return true })
	d.prReadiness = func(context.Context, string, int) (scm.PRReadiness, error) {
		return scm.PRReadiness{State: "OPEN", Threads: []scm.ReviewThread{{ID: "t", Preview: "DO NOT COPY UNTRUSTED TEXT"}}}, nil
	}
	r := d.handle(context.Background(), feedbackReq("addressPRFeedback", "s1", 42))
	if !r.OK {
		t.Fatal(r.Error)
	}
	sent := f.sent()
	if len(sent) != 1 {
		t.Fatalf("%v", sent)
	}
	for _, part := range []string{"acme/app PR #42", "validate each finding", "commit and push", "leave it open", "Do not merge", "resolveReviewThread"} {
		if !strings.Contains(sent[0], part) {
			t.Fatalf("missing %s", part)
		}
	}
	if strings.Contains(sent[0], "DO NOT COPY") {
		t.Fatal("untrusted comment copied into instructions")
	}
}
func TestPRReadinessAddressRefusesUnknownClosedAndEmpty(t *testing.T) {
	for _, mode := range []string{"error", "closed", "empty", "changed"} {
		t.Run(mode, func(t *testing.T) {
			d, f := talkFakeDaemon(t)
			talkSession(d, "s1", state.AgentIdle)
			d.sessions.Update("s1", func(s *session.Session) bool { s.Repo = "acme/app"; s.PR = &scm.PR{Number: 42}; return true })
			d.prReadiness = func(context.Context, string, int) (scm.PRReadiness, error) {
				if mode == "error" {
					return scm.PRReadiness{}, errors.New("offline")
				}
				if mode == "closed" {
					return scm.PRReadiness{State: "MERGED"}, nil
				}
				if mode == "changed" {
					d.sessions.Update("s1", func(s *session.Session) bool { s.PR = &scm.PR{Number: 43}; return true })
					return scm.PRReadiness{State: "OPEN", Threads: []scm.ReviewThread{{ID: "t"}}}, nil
				}
				return scm.PRReadiness{State: "OPEN"}, nil
			}
			if r := d.handle(context.Background(), feedbackReq("addressPRFeedback", "s1", 42)); r.OK {
				t.Fatal("accepted")
			}
			if len(f.sent()) > 0 {
				t.Fatal("sent unsafe request")
			}
		})
	}
}
