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

func TestPRReadinessCopiesTheCheckListForDisplay(t *testing.T) {
	d := prsDaemon(t)
	d.sessions.Upsert(session.Session{ID: "s", Repo: "acme/app", PR: &scm.PR{Number: 8, BaseRef: "main"}})
	var branches []string
	d.requiredChecks = func(_ context.Context, repo, branch string) ([]string, error) {
		branches = append(branches, repo+"@"+branch)
		return []string{"test", "e2e"}, nil
	}
	long := strings.Repeat("x", 500)
	d.prReadiness = func(context.Context, string, int) (scm.PRReadiness, error) {
		return scm.PRReadiness{Number: 8, State: "OPEN", Head: "h", ChecksHead: "h", BaseRef: "release", CheckList: []scm.ReadinessCheck{
			{Name: "test", Workflow: "CI", State: "FAILURE", Bucket: "fail", URL: "https://github.com/acme/app/actions/runs/1"},
			{Name: "evil\n‮line", State: "SUCCESS", Bucket: "pass", URL: "javascript:alert(1)", Description: long},
			{Name: "lola/review/codex", State: "SUCCESS", Bucket: "pass", URL: "http://example.com", Status: true},
		}}, nil
	}
	r := d.handle(context.Background(), feedbackReq("prReadiness", "s", 8))
	if !r.OK {
		t.Fatal(r.Error)
	}
	var got protocol.PRReadinessData
	if err := json.Unmarshal(r.Data, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.CheckList) != 4 {
		t.Fatalf("%+v", got.CheckList)
	}
	test, evil, gate, expected := got.CheckList[0], got.CheckList[1], got.CheckList[2], got.CheckList[3]
	if !test.Required || test.Gate || test.URL == "" || test.Workflow != "CI" || test.Bucket != "fail" {
		t.Fatalf("required check: %+v", test)
	}
	if evil.Name != "evil line" || evil.URL != "" || len([]rune(evil.Description)) != checkTextMax+1 || evil.Required {
		t.Fatalf("untrusted check text: %+v", evil)
	}
	if !gate.Gate || gate.URL != "" {
		t.Fatalf("review gate: %+v", gate)
	}
	if expected.Name != "e2e" || expected.State != "EXPECTED" || expected.Bucket != "pending" || !expected.Required {
		t.Fatalf("required check that never reported: %+v", expected)
	}
	// Truncated lists never invent an Expected entry; an unbound list stays
	// absent. Without GitHub's base branch the session's is asked.
	d.prReadiness = func(context.Context, string, int) (scm.PRReadiness, error) {
		return scm.PRReadiness{Number: 8, State: "OPEN", CheckList: nil}, nil
	}
	r = d.handle(context.Background(), feedbackReq("prReadiness", "s", 8))
	if strings.Contains(string(r.Data), "checkList") {
		t.Fatalf("unbound check list sent: %s", r.Data)
	}
	if strings.Join(branches, ",") != "acme/app@release,acme/app@main" {
		t.Fatalf("required checks asked for %v", branches)
	}
	if out := publicCheckList([]scm.ReadinessCheck{{Name: "test"}}, true, []string{"test", "e2e"}); len(out) != 1 {
		t.Fatalf("truncated list grew an Expected entry: %+v", out)
	}
}
