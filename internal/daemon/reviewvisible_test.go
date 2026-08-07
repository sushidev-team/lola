package daemon

// Tests for the VISIBLE pass (reviewvisible.go): the pane's launch command, the
// hand-back protocol (the daemon reads files, never the pane), the fall-back to
// a direct exec when a pane cannot be used, and the overrun tear-down.

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/linear"
	"github.com/sushidev-team/lola/internal/reviewclaude"
	"github.com/sushidev-team/lola/internal/reviewrun"
)

func TestReviewPaneCommandQuotesEveryValue(t *testing.T) {
	cp := config.ReviewProvider{
		Provider:       "claude-session",
		Model:          "sonnet",
		TimeoutSeconds: 900,
	}
	got := reviewPaneCommand("/opt/lola bin/lola", cp, "/wt/dir", "main", "/state")

	// The whole thing is one `sh -c '<line>'` argument: tmux hands its trailing
	// argument to the user's LOGIN shell, which may be fish or csh.
	if !strings.HasPrefix(got, "sh -c '") || !strings.HasSuffix(got, "'") {
		t.Fatalf("command = %q, want a quoted `sh -c` wrapper", got)
	}
	// A path with a space must survive as ONE argument.
	if !strings.Contains(got, `'"'"'/opt/lola bin/lola'"'"'`) && !strings.Contains(got, `/opt/lola bin/lola`) {
		t.Errorf("command = %q, want the binary quoted", got)
	}
	for _, want := range []string{"review-run", "--kind claude-session", "--dir /wt/dir", "--base main", "--state /state", "--model sonnet", "--timeout-seconds 900"} {
		if !strings.Contains(got, want) {
			t.Errorf("command = %q, want it to carry %q", got, want)
		}
	}
}

// An argument that could break out of the quoted line must not.
func TestReviewPaneCommandNeutralizesQuotes(t *testing.T) {
	cp := config.ReviewProvider{Provider: "coderabbit-cli", Command: "coderabbit'; rm -rf /; echo '"}
	got := reviewPaneCommand("lola", cp, "/wt", "main", "/state")
	if strings.Contains(got, "; rm -rf /;") && !strings.Contains(got, `'\''`) {
		t.Errorf("command = %q, want the embedded quote escaped", got)
	}
}

// fakeKiller records KillSession calls for the wait loop.
type fakeKiller struct {
	mu     sync.Mutex
	killed []string
}

func (f *fakeKiller) KillSession(_ context.Context, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.killed = append(f.killed, name)
	return nil
}

func (f *fakeKiller) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.killed)
}

// The daemon reads the child's RESULT from the state directory — never from the
// pane, which wraps, scrolls and is eventually overwritten.
func TestAwaitReviewPaneReadsTheHandBackFiles(t *testing.T) {
	d := newTestDaemon(t, reviewTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeNative{})
	state := t.TempDir()
	if err := reviewrun.Write(state, "PANE-FINDING", nil); err != nil {
		t.Fatal(err)
	}

	st, findings, err := d.awaitReviewPane(context.Background(), &fakeKiller{}, "sess-review", state,
		time.Now().Add(time.Minute))
	if err != nil {
		t.Fatalf("awaitReviewPane: %v", err)
	}
	if findings != "PANE-FINDING" {
		t.Errorf("findings = %q, want PANE-FINDING", findings)
	}
	if st.Err() != nil {
		t.Errorf("status err = %v, want nil", st.Err())
	}
}

// The child's outcome class maps back onto the SAME sentinels a direct exec
// returns, so the fallback chain and the retry budget cannot tell the two apart.
func TestAwaitReviewPaneMapsTheOutcomeClass(t *testing.T) {
	d := newTestDaemon(t, reviewTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeNative{})
	state := t.TempDir()
	if err := reviewrun.Write(state, "", reviewclaude.ErrTimeout); err != nil {
		t.Fatal(err)
	}

	st, _, err := d.awaitReviewPane(context.Background(), &fakeKiller{}, "sess-review", state,
		time.Now().Add(time.Minute))
	if err != nil {
		t.Fatalf("awaitReviewPane: %v", err)
	}
	if !errors.Is(st.Err(), reviewclaude.ErrTimeout) {
		t.Errorf("status err = %v, want ErrTimeout", st.Err())
	}
	if !isFallbackErr(st.Err()) {
		t.Error("a timed-out visible pass must still advance the fallback chain")
	}
}

// A child that never reports is given up on — the pane is torn down and the
// chain gets a timeout to fall back on, rather than waiting forever.
func TestAwaitReviewPaneGivesUpAndKillsThePane(t *testing.T) {
	d := newTestDaemon(t, reviewTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeNative{})
	killer := &fakeKiller{}

	// An empty state dir plus a deadline already in the past: the first pass
	// through the loop must give up.
	st, _, err := d.awaitReviewPane(context.Background(), killer, "sess-review", t.TempDir(),
		time.Now().Add(-time.Second))
	if err != nil {
		t.Fatalf("awaitReviewPane: %v", err)
	}
	if !errors.Is(st.Err(), reviewclaude.ErrTimeout) {
		t.Errorf("status err = %v, want ErrTimeout", st.Err())
	}
	if killer.count() != 1 {
		t.Errorf("KillSession calls = %d, want the pane torn down once", killer.count())
	}
}

// A cancelled context (shutdown) leaves the pane alone: it is the user's window
// onto a run that is still going.
func TestAwaitReviewPaneLeavesThePaneOnShutdown(t *testing.T) {
	d := newTestDaemon(t, reviewTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeNative{})
	killer := &fakeKiller{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, _, err := d.awaitReviewPane(ctx, killer, "sess-review", t.TempDir(),
		time.Now().Add(time.Hour))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if killer.count() != 0 {
		t.Errorf("KillSession calls = %d, want the pane left alone at shutdown", killer.count())
	}
}

// Without a session id (or without tmux) the pass falls back to the in-process
// client: a pane that cannot be opened must never cost a review.
func TestVisibleSeamFallsBackToTheDirectExec(t *testing.T) {
	d := newTestDaemon(t, reviewTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeNative{})
	called := 0
	direct := func(context.Context, string, string, string) (string, error) {
		called++
		return "DIRECT", nil
	}
	seam := d.visibleSeam(config.ReviewProvider{Provider: "claude-session"}, direct)

	got, err := seam(context.Background(), "", "/wt", "main")
	if err != nil || got != "DIRECT" {
		t.Fatalf("seam = (%q, %v), want the direct exec's answer", got, err)
	}
	if called != 1 {
		t.Errorf("direct exec calls = %d, want 1", called)
	}
}

// visibleDeadline is the pass's own budget plus the grace window, so the daemon
// normally reads the child's own timeout status instead of imposing one.
func TestVisibleDeadlineExceedsThePassTimeout(t *testing.T) {
	got := visibleDeadline(config.ReviewProvider{TimeoutSeconds: 900})
	if want := 900*time.Second + reviewVisibleGrace; got != want {
		t.Errorf("visibleDeadline = %s, want %s", got, want)
	}
	if got := visibleDeadline(config.ReviewProvider{}); got <= 0 {
		t.Errorf("a provider without a timeout must still get a deadline, got %s", got)
	}
}

// The pane is named after its session, beside the shell tabs.
func TestReviewPaneNaming(t *testing.T) {
	if got := reviewPaneName("lola-nori-app-nor-357"); got != "lola-nori-app-nor-357-review" {
		t.Errorf("reviewPaneName = %q", got)
	}
}
