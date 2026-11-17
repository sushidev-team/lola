package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/reviewprofile"
	"github.com/sushidev-team/lola/internal/scm"
	"github.com/sushidev-team/lola/internal/session"
)

func mergeRequest(id, method string) protocol.Request {
	raw, _ := json.Marshal(protocol.PRReadinessArgs{Number: 42, Repo: "acme/app", Head: headA, Method: method})
	return protocol.Request{Cmd: "mergePR", Session: id, Args: raw}
}

func readyToMerge() scm.PRReadiness {
	return scm.PRReadiness{Number: 42, State: "OPEN", Head: headA, Base: headB, Checks: "pass", Review: "APPROVED", Mergeable: "MERGEABLE", MergeState: "CLEAN"}
}

func TestManualMergePinsLaneHeadAndSelectedMethod(t *testing.T) {
	for _, method := range []string{"squash", "merge", "rebase"} {
		t.Run(method, func(t *testing.T) {
			d := prsDaemon(t)
			d.cfg.MergeQueue.Method = "squash" // choosing another method does not change the default
			d.sessions.Upsert(session.Session{ID: "s", Stack: true, Apps: []session.App{{Name: "app", Repo: "acme/app", PR: &scm.PR{Number: 42}}}})
			d.prReadiness = func(_ context.Context, repo string, n int) (scm.PRReadiness, error) {
				if repo != "acme/app" || n != 42 {
					t.Fatalf("wrong target: %s#%d", repo, n)
				}
				pr := readyToMerge()
				pr.Review, pr.Checks = "", "none" // manual confirmation permits an unprotected repo
				return pr, nil
			}
			calls := 0
			d.mergePR = func(_ context.Context, repo string, n int, m, head string) error {
				calls++
				if repo != "acme/app" || n != 42 || m != method || head != headA {
					t.Fatalf("wrong merge: %s %d %s %s", repo, n, m, head)
				}
				return nil
			}
			if r := d.handle(context.Background(), mergeRequest("s", method)); r.OK {
				t.Fatal("accepted aggregate")
			}
			r := d.handle(context.Background(), feedbackReq("prReadiness", "s~app", 42))
			var got protocol.PRReadinessData
			if err := json.Unmarshal(r.Data, &got); err != nil || got.MergeMethod != "squash" || got.MergeBlocked != "" {
				t.Fatalf("readiness: %s %v", r.Data, err)
			}
			if r := d.handle(context.Background(), mergeRequest("s~app", method)); !r.OK {
				t.Fatal(r.Error)
			}
			if calls != 1 {
				t.Fatalf("merge calls: %d", calls)
			}
			s, _ := d.sessions.Get("s~app")
			if s.PR.State == "MERGED" {
				t.Fatal("invented merged state before observer")
			}
		})
	}
}

func TestManualMergeRefusesBlockersAndDrift(t *testing.T) {
	for _, mode := range []string{"offline", "closed", "draft", "threads", "changes", "review", "ci", "pending", "unknown-checks", "conflict", "behind", "blocked", "unknown", "new-head", "wrong-number", "new-repo", "new-pr", "invalid-method", "goal", "goal-branch", "goal-store", "profile", "missing-session"} {
		t.Run(mode, func(t *testing.T) {
			d := prsDaemon(t)
			s := session.Session{ID: "s", Repo: "acme/app", PR: &scm.PR{Number: 42}}
			if mode == "goal" {
				s.GoalID = "goal"
			}
			if mode == "goal-branch" {
				s.Branch = "goal/test"
			}
			if mode == "goal-store" {
				d.goalsErr = errors.New("unavailable")
			}
			if mode == "profile" {
				d.cfg.ReviewProfiles = []reviewprofile.Profile{{ID: "required", Enabled: true, Required: true}}
			}
			d.sessions.Upsert(s)
			d.prReadiness = func(context.Context, string, int) (scm.PRReadiness, error) {
				pr := readyToMerge()
				switch mode {
				case "offline":
					return scm.PRReadiness{}, errors.New("offline")
				case "closed":
					pr.State = "MERGED"
				case "draft":
					pr.Draft = true
				case "threads":
					pr.Threads = []scm.ReviewThread{{ID: "t"}}
				case "changes":
					pr.Review = "CHANGES_REQUESTED"
				case "review":
					pr.Review = "REVIEW_REQUIRED"
				case "ci":
					pr.Checks = "fail"
				case "pending":
					pr.Checks = "pending"
				case "unknown-checks":
					pr.Checks = "unknown"
				case "conflict":
					pr.Mergeable = "CONFLICTING"
				case "behind":
					pr.MergeState = "BEHIND"
				case "blocked":
					pr.MergeState = "BLOCKED"
				case "unknown":
					pr.Mergeable = "UNKNOWN"
				case "new-head":
					pr.Head = headB
				case "wrong-number":
					pr.Number = 43
				case "new-repo":
					d.sessions.Update("s", func(s *session.Session) bool { s.Repo = "other/repo"; return true })
				case "new-pr":
					d.sessions.Update("s", func(s *session.Session) bool { s.PR = &scm.PR{Number: 43}; return true })
				}
				return pr, nil
			}
			d.mergePR = func(context.Context, string, int, string, string) error { t.Fatal("unsafe merge"); return nil }
			req := mergeRequest("s", "squash")
			if mode == "invalid-method" {
				req = mergeRequest("s", "--admin")
			}
			if mode == "missing-session" {
				req.Session = "missing"
			}
			if r := d.handle(context.Background(), req); r.OK || r.Error == "" {
				t.Fatalf("accepted: %+v", r)
			}
		})
	}
}

func TestManualMergeReturnsGitHubRefusal(t *testing.T) {
	d := prsDaemon(t)
	d.sessions.Upsert(session.Session{ID: "s", Repo: "acme/app", PR: &scm.PR{Number: 42}})
	d.prReadiness = func(context.Context, string, int) (scm.PRReadiness, error) { return readyToMerge(), nil }
	d.mergePR = func(context.Context, string, int, string, string) error {
		return errors.New("required status check missing")
	}
	if r := d.handle(context.Background(), mergeRequest("s", "squash")); r.OK || r.Error != "required status check missing" {
		t.Fatalf("%+v", r)
	}
}

func TestManualMergeUnreadyNeedsConfirmationAndKeepsPolicyGates(t *testing.T) {
	for _, mode := range []string{"pending", "failed", "threads", "goal", "profile", "closed", "changed-head"} {
		t.Run(mode, func(t *testing.T) {
			d := prsDaemon(t)
			s := session.Session{ID: "s", Repo: "acme/app", PR: &scm.PR{Number: 42}}
			pr := readyToMerge()
			pr.Checks, pr.MergeState = "pending", "BLOCKED"
			switch mode {
			case "failed":
				pr.Checks = "fail"
			case "threads":
				pr.Threads = []scm.ReviewThread{{ID: "t"}}
			case "goal":
				s.GoalID = "goal"
			case "profile":
				d.cfg.ReviewProfiles = []reviewprofile.Profile{{ID: "required", Enabled: true, Required: true}}
			case "closed":
				pr.State = "CLOSED"
			case "changed-head":
				pr.Head = headB
			}
			d.sessions.Upsert(s)
			d.prReadiness = func(context.Context, string, int) (scm.PRReadiness, error) { return pr, nil }
			calls := 0
			d.mergePR = func(context.Context, string, int, string, string) error { calls++; return nil }
			if r := d.handle(context.Background(), mergeRequest("s", "squash")); r.OK || calls != 0 {
				t.Fatalf("merged without confirmation: %+v", r)
			}
			req := mergeRequest("s", "squash")
			var args protocol.PRReadinessArgs
			_ = json.Unmarshal(req.Args, &args)
			args.ConfirmUnready = true
			req.Args, _ = json.Marshal(args)
			r := d.handle(context.Background(), req)
			allowed := mode == "pending" || mode == "failed" || mode == "threads"
			if r.OK != allowed || (calls == 1) != allowed {
				t.Fatalf("confirmed merge: %+v calls=%d", r, calls)
			}
		})
	}
}
