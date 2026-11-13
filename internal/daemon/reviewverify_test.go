package daemon

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/sushidev-team/lola/internal/goalpolicy"
	"github.com/sushidev-team/lola/internal/linear"
	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/remote"
	"github.com/sushidev-team/lola/internal/reviewpack"
	"github.com/sushidev-team/lola/internal/reviewverify"
)

func TestReviewVerificationReadbackAndAuthority(t *testing.T) {
	d := newTestDaemon(t, nativeTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeNative{})
	s := nativeSess("FE-1", "working")
	s.Worktree = t.TempDir()
	s.Repo = "acme/app"
	d.sessions.Upsert(s)
	p, err := reviewpack.Publish(d.home, s.ID, s.Worktree, s.Repo, 1, reviewpack.Manifest{Version: 1, Title: "Review", Body: "Changes", Base: strings.Repeat("a", 40), Head: strings.Repeat("b", 40)})
	if err != nil {
		t.Fatal(err)
	}
	r := reviewverify.Runner{Home: d.home}
	v, _, err := r.Reserve(p, strings.Repeat("1", 32), goalpolicy.Check{ID: "test", Command: "true", Image: "sha256:" + strings.Repeat("c", 64), TimeoutSeconds: 10})
	if err != nil {
		t.Fatal(err)
	}
	a, _ := json.Marshal(protocol.ReviewVerifyArgs{Package: p.ID, ID: v.ID, Spec: v.Spec})
	resp := d.handle(context.Background(), protocol.Request{Cmd: protocol.CmdReviewChecks, Session: s.ID, Args: a})
	var read protocol.ReviewChecksData
	if err := json.Unmarshal(resp.Data, &read); err != nil || len(read.Checks) != 1 || read.Checks[0].State != "interrupted" {
		t.Fatalf("read %+v %v", resp, err)
	}
	// A lost response or restart cannot repeat an existing request.
	resp = d.handle(context.Background(), protocol.Request{Cmd: protocol.CmdReviewVerify, Session: s.ID, Args: a})
	if !resp.OK {
		t.Fatal(resp.Error)
	}
	if len(d.reviewJobs) != 0 {
		t.Fatal("restarted a reserved check")
	}
	resp = d.handle(context.Background(), protocol.Request{Cmd: protocol.CmdReviewRecover, Session: s.ID, Args: a})
	if !resp.OK {
		t.Fatal(resp.Error)
	}
	for _, cmd := range []string{protocol.CmdReviewVerify, protocol.CmdReviewChecks, protocol.CmdReviewRecover, protocol.CmdReviewFeedback} {
		if hookBridgeCmds[cmd] || !remote.CommandDenied(cmd) {
			t.Fatalf("human command escaped boundary: %s", cmd)
		}
		if resp := d.handle(context.Background(), protocol.Request{Cmd: cmd, Session: "foreign", Args: a}); resp.OK {
			t.Fatalf("cross-session %s", cmd)
		}
	}
	// Retained reads do not require a live session/config or execute anything.
	if resp := (&Daemon{home: d.home}).handle(context.Background(), protocol.Request{Cmd: protocol.CmdReviewChecks, Session: s.ID, Args: a}); !resp.OK {
		t.Fatal(resp.Error)
	}
}

func TestReviewCompletionAndFeedbackAcrossAgents(t *testing.T) {
	for _, kind := range []string{"claude", "codex", "opencode"} {
		t.Run(kind, func(t *testing.T) {
			d := newTestDaemon(t, conflictConfig(), &linear.Fake{}, &fakeNative{})
			seams := &fakeReactSeams{}
			seams.install(d)
			s := nativeSess("FE-3", "working")
			s.Agent = kind
			s.Worktree = t.TempDir()
			s.AtPrompt = false
			d.sessions.Upsert(s)
			s, _ = d.sessions.Get(s.ID)
			manifest, _ := json.Marshal(reviewpack.Manifest{Version: 1, Title: "Finished", Body: "Observed results", Base: strings.Repeat("a", 40), Head: strings.Repeat("b", 40)})
			a, _ := json.Marshal(protocol.ReviewPublishArgs{Manifest: manifest})
			response := d.handle(context.Background(), protocol.Request{Cmd: protocol.CmdReviewComplete, Session: s.ID, Args: a})
			if !response.OK {
				t.Fatal(response.Error)
			}
			var p reviewpack.Package
			json.Unmarshal(response.Data, &p)
			after, _ := d.sessions.Get(s.ID)
			if after.Board.Phase != "done" || after.AgentState != s.AgentState || after.Delivery != s.Delivery || after.AtPrompt != s.AtPrompt {
				t.Fatalf("completion changed authority: %+v", after)
			}
			a, _ = json.Marshal(protocol.ReviewFeedbackArgs{Package: p.ID, Section: "Settings screenshot", Note: "Please retain keyboard focus."})
			response = d.handle(context.Background(), protocol.Request{Cmd: protocol.CmdReviewFeedback, Session: s.ID, Args: a})
			if !response.OK {
				t.Fatal(response.Error)
			}
			after, _ = d.sessions.Get(s.ID)
			for _, want := range []string{p.ID, p.Head, "Settings screenshot", "retain keyboard focus"} {
				if !strings.Contains(after.PendingFeedback, want) {
					t.Fatalf("missing %s from %s", want, after.PendingFeedback)
				}
			}
			if len(seams.sendCalls()) != 0 {
				t.Fatal("typed while working")
			}
			if !hookBridgeCmds[protocol.CmdReviewComplete] || !remote.CommandDenied(protocol.CmdReviewComplete) {
				t.Fatal("completion boundary")
			}
		})
	}
}

func TestReviewCompletionIsScopedToStackMember(t *testing.T) {
	d := newTestDaemon(t, conflictConfig(), &linear.Fake{}, &fakeNative{})
	root := stackSess("FE-4")
	root.Worktree = t.TempDir()
	root.Apps[1].Worktree = t.TempDir()
	d.sessions.Upsert(root)
	manifest, _ := json.Marshal(reviewpack.Manifest{Version: 1, Title: "Web done", Body: "Changes", Base: strings.Repeat("a", 40), Head: strings.Repeat("b", 40)})
	a, _ := json.Marshal(protocol.ReviewPublishArgs{Manifest: manifest, App: "web"})
	r := d.handle(context.Background(), protocol.Request{Cmd: protocol.CmdReviewComplete, Session: root.ID, Args: a})
	if !r.OK {
		t.Fatal(r.Error)
	}
	parent, _ := d.sessions.Get(root.ID)
	var p reviewpack.Package
	if err := json.Unmarshal(r.Data, &p); err != nil || p.Session != root.ID+"~web" {
		t.Fatalf("completion not scoped to member: %+v %v", p, err)
	}
	if parent.Board.Phase != "" {
		t.Fatal("member completion changed the parent")
	}
}

func TestReviewVerificationSchedulesOnceAndReadsCompletion(t *testing.T) {
	d := newTestDaemon(t, nativeTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeNative{})
	s := nativeSess("FE-5", "working")
	s.Worktree, s.Repo = t.TempDir(), "acme/app"
	goalFixtureGit(t, s.Worktree, "init", "-b", "main")
	goalFixtureGit(t, s.Worktree, "commit", "--allow-empty", "-m", "initial")
	head := goalFixtureGit(t, s.Worktree, "rev-parse", "HEAD")
	d.sessions.Upsert(s)
	p, err := reviewpack.Publish(d.home, s.ID, s.Worktree, s.Repo, 1, reviewpack.Manifest{Version: 1, Title: "Review", Body: "Changes", Base: head, Head: head})
	if err != nil {
		t.Fatal(err)
	}
	started, release := make(chan struct{}, 2), make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	defer func() { cancel(); d.connWg.Wait() }()
	d.reviewCheckRun = func(ctx context.Context, _ string, args []string) (string, error) {
		if args[0] == "rm" {
			return "", nil
		}
		started <- struct{}{}
		select {
		case <-release:
			return "observed pass", nil
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	a, _ := json.Marshal(protocol.ReviewVerifyArgs{Package: p.ID, ID: strings.Repeat("2", 32), Spec: goalpolicy.Check{ID: "unit", Command: "true", Image: "sha256:" + strings.Repeat("c", 64), TimeoutSeconds: 10}})
	req := protocol.Request{Cmd: protocol.CmdReviewVerify, Session: s.ID, Args: a}
	if r := d.handle(ctx, req); !r.OK {
		t.Fatal(r.Error)
	}
	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("check was not scheduled")
	}
	if r := d.handle(ctx, req); !r.OK {
		t.Fatal(r.Error)
	}
	close(release)
	d.connWg.Wait()
	select {
	case <-started:
		t.Fatal("replayed check launched twice")
	default:
	}
	r := d.handle(ctx, protocol.Request{Cmd: protocol.CmdReviewChecks, Session: s.ID, Args: a})
	var data protocol.ReviewChecksData
	if err := json.Unmarshal(r.Data, &data); err != nil || !r.OK || len(data.Checks) != 1 {
		t.Fatalf("readback %+v %v", r, err)
	}
	if v := data.Checks[0]; v.State != "pass" || v.Head != head || v.Output != "observed pass" || !v.Stopped {
		t.Fatalf("completion %+v", v)
	}
}
