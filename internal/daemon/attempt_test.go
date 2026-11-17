package daemon

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sushidev-team/lola/internal/linear"
	"github.com/sushidev-team/lola/internal/scm"
	"github.com/sushidev-team/lola/internal/session"
	"github.com/sushidev-team/lola/internal/sshstore"
	"github.com/sushidev-team/lola/internal/state"
)

func TestObserveFreshAttemptIgnoresPreviousMerge(t *testing.T) {
	for _, stack := range []bool{false, true} {
		name := "single repo"
		if stack {
			name = "stack"
		}
		t.Run(name, func(t *testing.T) {
			s := nativeSess("CE-127", "working")
			if stack {
				s = stackSess("CE-127")
			}
			s.Kind = session.KindLinear
			s.FirstSeen = time.Now().Add(-time.Minute)
			nat := &fakeNative{alive: map[string]bool{s.ID: true}}
			d := newTestDaemon(t, reactTestConfig(nativePoll("p1")), &linear.Fake{}, nat)
			(&fakeReactSeams{}).install(d)
			d.changedFiles = func(context.Context, string, string) ([]string, error) { return nil, nil }
			mergedAt := s.FirstSeen.Add(-time.Hour)
			d.prForBranch = func(context.Context, string, string) (*scm.PR, error) {
				return &scm.PR{Number: 8, State: "MERGED", MergedAt: mergedAt}, nil
			}
			d.sessions.Upsert(s)
			for i := 0; i < 2; i++ {
				d.observe(context.Background())
			}
			cur, ok := d.sessions.Get(s.ID)
			if !ok || cur.PR != nil || cur.Delivery != state.DeliveryNone || len(nat.killCalls()) != 0 {
				t.Fatalf("old merge ended fresh work: present=%v session=%+v kills=%v", ok, cur, nat.killCalls())
			}
			for _, app := range cur.Apps {
				if app.PR != nil || app.Delivery != state.DeliveryNone {
					t.Fatalf("old PR reached app delivery: %+v", app)
				}
			}
			// A PR completed during THIS attempt must still clean up, even if
			// it was first observed after merging (no prior OPEN observation).
			mergedAt = s.FirstSeen.Add(30 * time.Second)
			for i := 0; i < 2; i++ {
				d.observe(context.Background())
			}
			if _, ok := d.sessions.Get(s.ID); ok || len(nat.killCalls()) != 1 {
				t.Fatalf("current merge did not clean up once: present=%v kills=%v", ok, nat.killCalls())
			}
		})
	}
}

func TestAttemptPRCompletionBoundaryAndResume(t *testing.T) {
	start := time.Now()
	for _, tc := range []struct {
		name string
		kind session.Kind
		pr   *scm.PR
		keep bool
	}{
		{"previous merge", session.KindLinear, &scm.PR{State: "MERGED", MergedAt: start.Add(-time.Hour)}, false},
		{"previous close", session.KindLinear, &scm.PR{State: "CLOSED", ClosedAt: start.Add(-time.Hour)}, false},
		{"merge closedAt fallback", session.KindLinear, &scm.PR{State: "MERGED", ClosedAt: start.Add(-time.Hour)}, false},
		{"current merge", session.KindLinear, &scm.PR{State: "MERGED", MergedAt: start.Add(time.Second)}, true},
		{"current close", session.KindLinear, &scm.PR{State: "CLOSED", ClosedAt: start.Add(time.Second)}, true},
		{"reopened PR", session.KindLinear, &scm.PR{State: "OPEN", ClosedAt: start.Add(-time.Hour)}, true},
		{"explicit PR resume", session.KindPR, &scm.PR{State: "MERGED", MergedAt: start.Add(-time.Hour)}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := prForAttempt(session.Session{Kind: tc.kind, FirstSeen: start}, tc.pr)
			if (got != nil) != tc.keep {
				t.Fatalf("got %+v, keep=%v", got, tc.keep)
			}
		})
	}
}

func TestEndedAttemptPreservesFollowupContextAndWorktree(t *testing.T) {
	d, _ := talkFakeDaemon(t)
	s := stackSess("CE-127")
	s.Worktree = t.TempDir()
	s.Apps[0].PR = &scm.PR{Number: 8, URL: "https://github.com/acme/app/pull/8", State: "MERGED"}
	s.Apps[1].PR = &scm.PR{Number: 101, URL: "https://github.com/acme/web/pull/101", State: "MERGED"}
	s.PR = s.Apps[0].PR
	s.SetDelivery(state.DeliveryMerged, time.Now())
	d.sessions.Upsert(s)
	d.dropSession(s)
	info := sshstore.ReadEnded(d.home, s.ID)
	if info == nil || info.KeptWorktree != s.Worktree || len(info.PRURLs) != 2 {
		t.Fatalf("lost retained checkout or per-app history: %+v", info)
	}
	if used, err := sessionPreviouslyUsed(d.home, s.ID); err != nil || !used {
		t.Fatalf("legacy session must reserve its identity: %v %v", used, err)
	}
	previous := previousIssueWork(d.home, s.Project, s.Issue)
	for _, want := range []string{s.ID, "PR #8 merged", s.Apps[0].PR.URL, s.Apps[1].PR.URL} {
		if !strings.Contains(previous, want) {
			t.Errorf("history missing %q: %s", want, previous)
		}
	}
	if got := previousIssueWork(d.home, "other-project", s.Issue); got != "" {
		t.Fatalf("cross-project history: %s", got)
	}
	if got := previousIssueWork(d.home, s.Project, "CE-128"); got != "" {
		t.Fatalf("cross-issue history: %s", got)
	}
	// Unknown/removed worktrees must not be advertised as retained.
	s.ID += "-r2"
	s.Worktree = filepath.Join(t.TempDir(), "removed")
	d.recordEnded(s)
	if info := sshstore.ReadEnded(d.home, s.ID); info == nil || info.KeptWorktree != "" {
		t.Fatalf("removed checkout claimed as retained: %+v", info)
	}
	// An unreadable history root cannot mean a free session ID.
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "state"), []byte("blocked"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := sessionPreviouslyUsed(home, s.ID); err == nil {
		t.Fatal("invalid history path must not be treated as unused")
	}
}
