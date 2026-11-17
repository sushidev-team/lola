package daemon

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/sushidev-team/lola/internal/linear"
	"github.com/sushidev-team/lola/internal/review"
	"github.com/sushidev-team/lola/internal/scm"
	"github.com/sushidev-team/lola/internal/session"
	"github.com/sushidev-team/lola/internal/state"
)

type statusCall struct{ repo, sha, state, ctx, desc string }

type fakeStatus struct {
	mu    sync.Mutex
	calls []statusCall
}

func (f *fakeStatus) install(d *Daemon) {
	d.setCommitStatus = func(_ context.Context, repo, sha, st, c, desc, _ string) error {
		f.mu.Lock()
		f.calls = append(f.calls, statusCall{repo, sha, st, c, desc})
		f.mu.Unlock()
		return nil
	}
}

func (f *fakeStatus) states() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, c := range f.calls {
		out = append(out, c.sha[:4]+":"+c.state)
	}
	return out
}

const (
	headA = "aaaa111111111111111111111111111111111111"
	headB = "bbbb222222222222222222222222222222222222"
	headC = "cccc333333333333333333333333333333333333"
	headD = "dddd444444444444444444444444444444444444"
)

func gatedDaemon(t *testing.T) (*Daemon, *fakeReview, *fakeStatus, session.Session) {
	t.Helper()
	d := newTestDaemon(t, reviewTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeNative{})
	p := claudeDesc()
	p.Status = true
	setProviders(d, p)
	(&fakeReactSeams{}).install(d)
	fr := &fakeReview{findings: "**[blocker]** `a.go:1` — nil deref\n"}
	fr.installKind(d, kindClaudeSession)
	fs := &fakeStatus{}
	fs.install(d)
	d.verifyReviewHead = func(context.Context, string, string) error { return nil }
	pr := openPR(7, "MERGEABLE", "", "pass")
	pr.HeadSHA = headA
	s := reactSess("FE-1", "review_pending", pr)
	s.AgentState = state.AgentIdle
	d.sessions.Upsert(s)
	return d, fr, fs, s
}

func moveHead(t *testing.T, d *Daemon, id, sha string, a state.AgentState) session.Session {
	t.Helper()
	d.sessions.Update(id, func(cur *session.Session) bool {
		cur.PR.HeadSHA = sha
		cur.AgentState = a
		return true
	})
	s, _ := d.sessions.Get(id)
	return s
}

func TestReviewGatePostsPendingThenVerdictAndReReviewsNewHeads(t *testing.T) {
	d, fr, fs, s := gatedDaemon(t)
	ctx := context.Background()

	d.runReviewProviders(ctx, s)
	if got := strings.Join(fs.states(), " "); got != "aaaa:pending aaaa:failure" {
		t.Fatalf("statuses = %q", got)
	}
	if fs.calls[0].ctx != "lola/review/claude-session" || fs.calls[0].repo != "acme/widgets" {
		t.Fatalf("status call = %+v", fs.calls[0])
	}
	got, _ := d.sessions.Get(s.ID)
	if g := got.ReviewGates["claude-session"]; g.PR != 7 || g.Head != headA || g.Rounds != 1 || g.State != scm.StatusFailure {
		t.Fatalf("gate = %+v", g)
	}

	// Same head again: nothing new.
	d.runReviewProviders(ctx, got)
	if fr.callCount() != 1 {
		t.Fatalf("same head must not re-review, got %d execs", fr.callCount())
	}

	// The worker pushes a fix but is still mid-turn: wait.
	s = moveHead(t, d, s.ID, headB, state.AgentWorking)
	d.runReviewProviders(ctx, s)
	if fr.callCount() != 1 {
		t.Fatalf("a mid-turn head must not be reviewed, got %d execs", fr.callCount())
	}

	// Settled: round 2, now clean.
	fr.mu.Lock()
	fr.findings = ""
	fr.mu.Unlock()
	s = moveHead(t, d, s.ID, headB, state.AgentIdle)
	d.runReviewProviders(ctx, s)
	if fr.callCount() != 2 {
		t.Fatalf("a settled new head must be re-reviewed, got %d execs", fr.callCount())
	}
	got, _ = d.sessions.Get(s.ID)
	if g := got.ReviewGates["claude-session"]; g.Rounds != 2 || g.State != scm.StatusSuccess || g.Posted != headB {
		t.Fatalf("gate after round 2 = %+v", g)
	}
	if !d.reviewGatesOpen(got) {
		t.Fatal("a success on the current head must open the merge-queue gate")
	}

	// Round 3, then the cap: the 4th head gets an error status and no exec.
	s = moveHead(t, d, s.ID, headC, state.AgentIdle)
	d.runReviewProviders(ctx, s)
	s = moveHead(t, d, s.ID, headD, state.AgentIdle)
	if d.reviewGatesOpen(s) {
		t.Fatal("an unreviewed head must hold the merge-queue gate")
	}
	d.runReviewProviders(ctx, s)
	s, _ = d.sessions.Get(s.ID)
	d.runReviewProviders(ctx, s)
	if fr.callCount() != 3 {
		t.Fatalf("rounds must stop at %d, got %d execs", reviewGateRounds, fr.callCount())
	}
	all := fs.states()
	if last := all[len(all)-1]; last != "dddd:error" || strings.Count(strings.Join(all, " "), "dddd:") != 1 {
		t.Fatalf("cap must post one error on the new head, got %v", all)
	}

	// A human forcing the review still works past the cap.
	if _, err := d.handleReviewProvider(ctx, s.ID, "claude-session"); err != nil {
		t.Fatal(err)
	}
	if all := fs.states(); all[len(all)-1] != "dddd:success" {
		t.Fatalf("forced review must post its verdict, got %v", all)
	}
}

func TestReviewGateKeepsPendingWhileRetrying(t *testing.T) {
	d, fr, fs, s := gatedDaemon(t)
	fr.err = errTimeoutForTest()
	d.runReviewProviders(context.Background(), s)
	if got := strings.Join(fs.states(), " "); got != "aaaa:pending aaaa:pending" {
		t.Fatalf("a retried pass must stay pending, got %q", got)
	}
}

func TestReviewGateOffPostsNothing(t *testing.T) {
	d, _, fs, s := gatedDaemon(t)
	setProviders(d, claudeDesc())
	d.runReviewProviders(context.Background(), s)
	if len(fs.states()) != 0 {
		t.Fatalf("github_status off must post nothing, got %v", fs.states())
	}
}

func TestReviewGateRefusesMismatchedCheckout(t *testing.T) {
	for _, changeDuringPass := range []bool{false, true} {
		t.Run(fmt.Sprint(changeDuringPass), func(t *testing.T) {
			d, fr, fs, s := gatedDaemon(t)
			fr.findings = ""
			checks := 0
			d.verifyReviewHead = func(_ context.Context, _, sha string) error {
				checks++
				if sha != headA {
					t.Fatalf("verified %q, want PR head", sha)
				}
				if !changeDuringPass || checks > 1 {
					return errors.New("checkout moved")
				}
				return nil
			}
			d.runReviewProviders(context.Background(), s)
			if strings.Contains(strings.Join(fs.states(), " "), "success") {
				t.Fatal("a mismatched checkout received a success status")
			}
			wantCalls := 0
			if changeDuringPass {
				wantCalls = 1
			}
			if fr.callCount() != wantCalls {
				t.Fatalf("review calls = %d, want %d", fr.callCount(), wantCalls)
			}
			cur, _ := d.sessions.Get(s.ID)
			if cur.ReviewedPRs["claude-session"] != 0 {
				t.Fatal("mismatched checkout must permit a later retry")
			}
		})
	}
}

func TestReviewGateWaitsForFirstHeadToSettle(t *testing.T) {
	d, fr, fs, s := gatedDaemon(t)
	s.AgentState = state.AgentWorking
	d.runReviewProviders(context.Background(), s)
	if fr.callCount() != 0 || len(fs.states()) != 0 {
		t.Fatal("first gated pass must wait for the worker to settle")
	}
}

func TestReviewGateUpdatesDoNotMutateSnapshots(t *testing.T) {
	d, _, _, s := gatedDaemon(t)
	d.runReviewProviders(context.Background(), s)
	before, _ := d.sessions.Get(s.ID)
	p, _ := d.providerByKind(kindClaudeSession)
	d.postReviewStatus(context.Background(), s, p, s.Repo, headA, scm.StatusPending, "retry")
	if before.ReviewGates["claude-session"].State != scm.StatusFailure {
		t.Fatal("gate update mutated an earlier session snapshot")
	}
}

func TestReviewGateCanReviewBeforePROpens(t *testing.T) {
	d, fr, fs, s := gatedDaemon(t)
	s.PR = nil
	p, _ := d.providerByKind(kindClaudeSession)
	res := d.reviewPass(context.Background(), s, p, false)
	if !res.Ran || fr.callCount() != 1 || len(fs.states()) != 0 {
		t.Fatalf("pre-PR review = %+v, calls = %d, statuses = %v", res, fr.callCount(), fs.states())
	}
}

func TestReviewGateClosesPreviousSuccessWhenStatusPostFails(t *testing.T) {
	d, fr, _, s := gatedDaemon(t)
	fr.findings = ""
	d.runReviewProviders(context.Background(), s)
	s, _ = d.sessions.Get(s.ID)
	if !d.reviewGatesOpen(s) {
		t.Fatal("initial review should pass")
	}
	d.setCommitStatus = func(context.Context, string, string, string, string, string, string) error {
		return errors.New("offline")
	}
	p, _ := d.providerByKind(kindClaudeSession)
	d.beginReviewGate(context.Background(), s, p)
	cur, _ := d.sessions.Get(s.ID)
	if d.reviewGatesOpen(cur) {
		t.Fatal("failed pending post left the previous success open")
	}
}

func TestReviewVerdict(t *testing.T) {
	cases := []struct {
		res   reviewResult
		retry bool
		want  string
	}{
		{reviewResult{Ran: true}, false, scm.StatusSuccess},
		{reviewResult{Ran: true, Findings: "**[minor]** `a.go:1` — x\n"}, false, scm.StatusSuccess},
		{reviewResult{Ran: true, Findings: "**[major]** `a.go:1` — x\n"}, false, scm.StatusFailure},
		{reviewResult{Ran: true, Findings: "unparseable prose about a bug"}, false, scm.StatusFailure},
		{reviewResult{Err: errors.New("x")}, true, scm.StatusPending},
		{reviewResult{Err: errors.New("x")}, false, scm.StatusError},
		{reviewResult{Skipped: noProviderSkip}, false, scm.StatusError},
	}
	for i, c := range cases {
		if got, _ := reviewVerdict(c.res, c.retry); got != c.want {
			t.Errorf("case %d: got %q, want %q", i, got, c.want)
		}
	}
}

func TestRequiredChecksNarrowAndCache(t *testing.T) {
	d := newTestDaemon(t, reviewTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeNative{})
	calls := 0
	d.requiredChecks = func(_ context.Context, repo, branch string) ([]string, error) {
		calls++
		if repo != "acme/widgets" || branch != "main" {
			t.Errorf("lookup for %s@%s", repo, branch)
		}
		return []string{"test"}, nil
	}
	mk := func() *scm.PR {
		return &scm.PR{BaseRef: "main", ChecksState: "fail", Checks: []scm.Check{
			{Name: "test", Bucket: "pass"}, {Name: "vitest-storybook", Bucket: "fail"},
		}}
	}
	for i := 0; i < 3; i++ {
		pr := mk()
		d.applyRequiredChecks(context.Background(), "acme/widgets", pr)
		if pr.ChecksState != "pass" {
			t.Fatalf("ChecksState = %q, want pass", pr.ChecksState)
		}
	}
	if calls != 1 {
		t.Fatalf("ruleset lookup must be cached, got %d calls", calls)
	}

	d.required = requiredCache{}
	d.requiredChecks = func(context.Context, string, string) ([]string, error) { return nil, errors.New("gh down") }
	pr := mk()
	d.applyRequiredChecks(context.Background(), "acme/widgets", pr)
	if pr.ChecksState != "fail" {
		t.Fatalf("an unknown ruleset must keep the all-checks verdict, got %q", pr.ChecksState)
	}
}

func errTimeoutForTest() error { return review.ErrTimeout }
