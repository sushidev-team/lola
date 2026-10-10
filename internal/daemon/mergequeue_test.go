package daemon

// Tests for the local merge queue (mergequeue.go): one merge per repository
// per pass, the sync request through the send-keys gate, the one-shot guards,
// and failing CLOSED on every fact the queue cannot judge.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/linear"
	"github.com/sushidev-team/lola/internal/scm"
	"github.com/sushidev-team/lola/internal/session"
	"github.com/sushidev-team/lola/internal/state"
)

// fakeGitHub is the queue's view of GitHub: PR facts per branch, how far each
// head is behind the base, and every merge issued.
type fakeGitHub struct {
	mu        sync.Mutex
	prs       map[string]*scm.PR // by branch
	behind    map[string]int     // by head sha
	behindErr error
	mergeErr  error
	merges    []int
	compares  int
}

func (g *fakeGitHub) install(d *Daemon) {
	d.prForBranch = func(_ context.Context, _, branch string) (*scm.PR, error) {
		g.mu.Lock()
		defer g.mu.Unlock()
		if pr := g.prs[branch]; pr != nil {
			cp := *pr
			return &cp, nil
		}
		return nil, nil
	}
	d.behindBy = func(_ context.Context, _, _, sha string) (int, error) {
		g.mu.Lock()
		defer g.mu.Unlock()
		g.compares++
		return g.behind[sha], g.behindErr
	}
	d.mergePR = func(_ context.Context, _ string, pr int, method, sha string) error {
		g.mu.Lock()
		defer g.mu.Unlock()
		if g.mergeErr != nil {
			return g.mergeErr
		}
		for _, p := range g.prs {
			if p.Number == pr {
				if p.HeadSHA != sha {
					return errors.New("head moved")
				}
				p.State = "MERGED"
			}
		}
		g.merges = append(g.merges, pr)
		return nil
	}
}

func (g *fakeGitHub) merged() []int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]int(nil), g.merges...)
}

func sha(n int) string { return fmt.Sprintf("%040x", n) }

func greenPR(number int, head string) *scm.PR {
	pr := openPR(number, "MERGEABLE", "APPROVED", "pass")
	pr.HeadSHA, pr.BaseRef = head, "main"
	return pr
}

func queueDaemon(t *testing.T) (*Daemon, *fakeGitHub, *fakeReactSeams) {
	t.Helper()
	cfg := nativeTestConfig(nativePoll("p1"))
	cfg.MergeQueue = config.MergeQueueConfig{Enabled: true}
	d := newTestDaemon(t, cfg, &linear.Fake{}, &fakeNative{})
	seams := &fakeReactSeams{}
	seams.install(d)
	gh := &fakeGitHub{prs: map[string]*scm.PR{}, behind: map[string]int{}}
	gh.install(d)
	return d, gh, seams
}

// queueSess seeds an approved session resting at its prompt, and mirrors its
// PR into the fake GitHub.
func queueSess(d *Daemon, gh *fakeGitHub, ident string, pr *scm.PR) string {
	s := reactSess(ident, "approved", pr)
	s.AtPrompt = true
	s.SetDelivery(state.DeriveDelivery(pr, ""), time.Now())
	d.sessions.Upsert(s)
	cp := *pr
	gh.prs[s.Branch] = &cp
	return s.ID
}

func posture(t *testing.T, d *Daemon, id string) (int, string) {
	t.Helper()
	s, ok := d.sessions.Get(id)
	if !ok {
		t.Fatalf("session %s gone", id)
	}
	return s.MergeQueuePos, s.MergeQueueStep
}

func TestMergeQueueOffDoesNothing(t *testing.T) {
	d, gh, _ := queueDaemon(t)
	d.cfg.MergeQueue.Enabled = false
	id := queueSess(d, gh, "FE-1", greenPR(5, sha(5)))
	d.runMergeQueue(context.Background())
	if len(gh.merged()) != 0 || gh.compares != 0 {
		t.Fatalf("a disabled queue must not touch GitHub: merges=%v compares=%d", gh.merged(), gh.compares)
	}
	if pos, step := posture(t, d, id); pos != 0 || step != "" {
		t.Errorf("posture = %d %q, want none", pos, step)
	}
}

// Only the oldest PR lands in a pass; the rest wait their turn.
func TestMergeQueueMergesOnlyTheHead(t *testing.T) {
	d, gh, _ := queueDaemon(t)
	a := queueSess(d, gh, "FE-1", greenPR(5, sha(5)))
	b := queueSess(d, gh, "FE-2", greenPR(7, sha(7)))
	d.runMergeQueue(context.Background())
	if got := gh.merged(); len(got) != 1 || got[0] != 5 {
		t.Fatalf("merges = %v, want [5]", got)
	}
	if pos, step := posture(t, d, a); pos != 1 || step != mqMerging {
		t.Errorf("head posture = %d %q", pos, step)
	}
	if pos, step := posture(t, d, b); pos != 2 || step != mqQueued {
		t.Errorf("second posture = %d %q", pos, step)
	}
	// The next pass must not re-issue the merge for the same head.
	d.runMergeQueue(context.Background())
	if got := gh.merged(); len(got) != 1 {
		t.Errorf("merge re-issued: %v", got)
	}
}

// A head that is behind the default branch is asked — once — to merge it in.
func TestMergeQueueSyncsABehindHeadOnce(t *testing.T) {
	d, gh, seams := queueDaemon(t)
	id := queueSess(d, gh, "FE-1", greenPR(5, sha(5)))
	gh.behind[sha(5)] = 2

	d.runMergeQueue(context.Background())
	if len(gh.merged()) != 0 {
		t.Fatal("a behind head must not be merged")
	}
	calls := seams.sendCalls()
	if len(calls) != 1 || !strings.Contains(calls[0].text, "git merge origin/main") ||
		!strings.Contains(calls[0].text, "Do not merge the PR yourself") {
		t.Fatalf("want one sync request naming the default branch, got %+v", calls)
	}
	if _, step := posture(t, d, id); step != mqSyncing {
		t.Errorf("step = %q, want syncing", step)
	}
	got, _ := d.sessions.Get(id)
	if got.MergeQueueGuard == nil || got.MergeQueueGuard.Action != mqActSync || got.MergeQueueGuard.HeadSHA != sha(5) {
		t.Errorf("guard = %+v", got.MergeQueueGuard)
	}

	// Back at its prompt but the head has not moved: no second request.
	d.sessions.Update(id, func(cur *session.Session) bool { cur.AtPrompt = true; return true })
	d.runMergeQueue(context.Background())
	if n := len(seams.sendCalls()); n != 1 {
		t.Errorf("sync re-sent for the same head: %d sends", n)
	}
}

// A mid-turn agent is not typed into; the request waits for a later pass and
// nothing is stamped meanwhile.
func TestMergeQueueSyncWaitsForAnIdleAgent(t *testing.T) {
	d, gh, seams := queueDaemon(t)
	d.paneTail = func(context.Context, string, int) (string, error) { return paneWorking, nil }
	id := queueSess(d, gh, "FE-1", greenPR(5, sha(5)))
	gh.behind[sha(5)] = 1

	d.runMergeQueue(context.Background())
	if n := len(seams.sendCalls()); n != 0 {
		t.Fatalf("typed into a mid-turn agent: %d sends", n)
	}
	got, _ := d.sessions.Get(id)
	if got.MergeQueueGuard != nil || got.MergeQueueStep != mqWaiting {
		t.Errorf("guard = %+v step = %q, want unstamped and waiting", got.MergeQueueGuard, got.MergeQueueStep)
	}
}

// Every fact the queue cannot judge holds it; none of them merges.
func TestMergeQueueFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(pr *scm.PR, s *session.Session, gh *fakeGitHub)
		step   string
	}{
		{"behind check fails", func(_ *scm.PR, _ *session.Session, gh *fakeGitHub) { gh.behindErr = errors.New("502") }, mqBlocked},
		{"mergeability unknown", func(pr *scm.PR, _ *session.Session, _ *fakeGitHub) { pr.Mergeable = "UNKNOWN" }, mqWaiting},
		{"checks pending", func(pr *scm.PR, _ *session.Session, _ *fakeGitHub) { pr.ChecksState = "pending" }, mqWaiting},
		{"no checks at all", func(pr *scm.PR, _ *session.Session, _ *fakeGitHub) { pr.ChecksState = "none" }, mqBlocked},
		{"other base branch", func(pr *scm.PR, _ *session.Session, _ *fakeGitHub) { pr.BaseRef = "release" }, mqBlocked},
		{"head sha unknown", func(pr *scm.PR, _ *session.Session, _ *fakeGitHub) { pr.HeadSHA = "" }, mqBlocked},
		{"stale PR facts", func(_ *scm.PR, s *session.Session, _ *fakeGitHub) { s.PRFetchFailures = 1 }, mqBlocked},
		{"conflicting", func(pr *scm.PR, _ *session.Session, _ *fakeGitHub) { pr.Mergeable = "CONFLICTING" }, mqConflict},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d, gh, seams := queueDaemon(t)
			id := queueSess(d, gh, "FE-1", greenPR(5, sha(5)))
			d.sessions.Update(id, func(cur *session.Session) bool {
				tc.mutate(cur.PR, cur, gh)
				return true
			})
			d.runMergeQueue(context.Background())
			if got := gh.merged(); len(got) != 0 {
				t.Errorf("merged %v", got)
			}
			if n := len(seams.sendCalls()); n != 0 {
				t.Errorf("sent %d messages", n)
			}
			if _, step := posture(t, d, id); step != tc.step {
				t.Errorf("step = %q, want %q", step, tc.step)
			}
		})
	}
}

// The pre-merge re-read is the last word: a push since this cycle's fetch
// means the judged commit is no longer the head, so nothing merges.
func TestMergeQueueRereadsBeforeMerging(t *testing.T) {
	d, gh, _ := queueDaemon(t)
	queueSess(d, gh, "FE-1", greenPR(5, sha(5)))
	for _, p := range gh.prs {
		p.HeadSHA = sha(50)
		p.ChecksState = "pending"
	}
	d.runMergeQueue(context.Background())
	if got := gh.merged(); len(got) != 0 {
		t.Fatalf("merged a head that moved: %v", got)
	}
}

// A refused merge is recorded, surfaced once, and not retried for the same
// head inside the cool-down.
func TestMergeQueueRefusedMergeIsNotHammered(t *testing.T) {
	d, gh, seams := queueDaemon(t)
	id := queueSess(d, gh, "FE-1", greenPR(5, sha(5)))
	gh.mergeErr = errors.New("required status check missing")

	d.runMergeQueue(context.Background())
	d.runMergeQueue(context.Background())
	if gh.compares != 1 {
		t.Errorf("the refused head was re-judged inside the cool-down: %d compares", gh.compares)
	}
	got, _ := d.sessions.Get(id)
	if got.MergeQueueGuard == nil || got.MergeQueueGuard.Action != mqActFailed || got.MergeQueueStep != mqFailed {
		t.Errorf("guard = %+v step = %q", got.MergeQueueGuard, got.MergeQueueStep)
	}
	refused := 0
	for _, n := range seams.notes {
		if n.Title == "Merge queue: merge refused" {
			refused++
		}
	}
	if refused != 1 {
		t.Errorf("refusal notified %d times, want 1", refused)
	}
}

// Someone else's PR (a `pr` session tracks an upstream branch) is never ours to merge.
func TestMergeQueueIgnoresPRsLolaDoesNotOwn(t *testing.T) {
	d, gh, _ := queueDaemon(t)
	id := queueSess(d, gh, "FE-1", greenPR(5, sha(5)))
	d.sessions.Update(id, func(cur *session.Session) bool { cur.Kind = session.KindPR; return true })
	d.runMergeQueue(context.Background())
	if got := gh.merged(); len(got) != 0 {
		t.Fatalf("merged a PR lola does not own: %v", got)
	}
}

// The acceptance scenario: three approved PRs land one after another with no
// manual rebase. After each merge the next head is behind; its agent is asked
// to sync, "pushes" (a new head that contains main), CI goes green, and it
// lands.
func TestMergeQueueLandsSequentially(t *testing.T) {
	d, gh, seams := queueDaemon(t)
	ids := []string{
		queueSess(d, gh, "FE-1", greenPR(5, sha(5))),
		queueSess(d, gh, "FE-2", greenPR(6, sha(6))),
		queueSess(d, gh, "FE-3", greenPR(7, sha(7))),
	}
	// observe stands in for the observer's per-session refresh: copy GitHub's
	// facts onto each record, as observeNative does before the queue runs.
	observe := func() {
		for _, id := range ids {
			d.sessions.Update(id, func(cur *session.Session) bool {
				if pr := gh.prs[cur.Branch]; pr != nil {
					cp := *pr
					cur.PR = &cp
					cur.SetDelivery(state.DeriveDelivery(&cp, cur.Delivery), time.Now())
				}
				return true
			})
		}
		d.runMergeQueue(context.Background())
	}
	// agentSyncs simulates the agent answering a sync request: it merges main
	// in and pushes, CI re-runs and passes.
	agentSyncs := func(branch string, newHead string) {
		pr := gh.prs[branch]
		pr.HeadSHA = newHead
		d.sessions.Update(branch2id(d, branch), func(cur *session.Session) bool { cur.AtPrompt = true; return true })
	}

	observe() // #5 lands; #6 and #7 are now behind main
	gh.behind[sha(6)], gh.behind[sha(7)] = 1, 1
	observe() // #6 is the head: sync requested
	if n := len(seams.sendCalls()); n != 1 {
		t.Fatalf("want one sync request, got %d", n)
	}
	s6, _ := d.sessions.Get(ids[1])
	agentSyncs(s6.Branch, sha(60))
	observe() // #6 lands
	gh.behind[sha(7)] = 2
	observe() // #7 sync requested
	s7, _ := d.sessions.Get(ids[2])
	agentSyncs(s7.Branch, sha(70))
	observe() // #7 lands

	if got := gh.merged(); fmt.Sprint(got) != "[5 6 7]" {
		t.Fatalf("merges = %v, want [5 6 7] in order", got)
	}
	if n := len(seams.sendCalls()); n != 2 {
		t.Errorf("want exactly two sync requests, got %d", n)
	}
}

func branch2id(d *Daemon, branch string) string {
	for _, s := range d.sessions.Snapshot() {
		if s.Branch == branch {
			return s.ID
		}
	}
	return ""
}
