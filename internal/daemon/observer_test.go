package daemon

import (
	"context"
	"io"
	"log"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/sushidev-team/lola/internal/linear"
	"github.com/sushidev-team/lola/internal/runtime"
	"github.com/sushidev-team/lola/internal/scm"
	"github.com/sushidev-team/lola/internal/session"
)

// fakeObsSeams installs a counting fake for the observer's scm (PR) seam.
// Nothing execs gh in these tests.
type fakeObsSeams struct {
	mu      sync.Mutex
	prCalls []string // "repo|branch"
	pr      *scm.PR
	prErr   error
}

func (f *fakeObsSeams) install(d *Daemon) {
	d.prForBranch = func(ctx context.Context, repo, branch string) (*scm.PR, error) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.prCalls = append(f.prCalls, repo+"|"+branch)
		if f.prErr != nil {
			return nil, f.prErr
		}
		if f.pr == nil {
			return nil, nil
		}
		pr := *f.pr
		return &pr, nil
	}
}

// counts returns (PR lookups, 0). The second value is retained so native-merge
// tests can keep their `pr, _ := seams.counts()` call shape.
func (f *fakeObsSeams) counts() (pr, _ int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.prCalls), 0
}

func findSession(t *testing.T, snap []session.Session, id string) session.Session {
	t.Helper()
	for _, s := range snap {
		if s.ID == id {
			return s
		}
	}
	t.Fatalf("no session %q in snapshot %+v", id, snap)
	return session.Session{}
}

// --- Retention prune ----------------------------------------------------------

func TestObserveNativePrunesSessionsOlderThanRetention(t *testing.T) {
	home := t.TempDir()
	t.Setenv("LOLA_HOME", home)
	// Pre-seed the on-disk store with one stale (settled dead) and one fresh
	// (alive, working) native session before the daemon (and its store) is
	// constructed.
	staleID := runtime.SessionID("proj1", "STALE")
	freshID := runtime.SessionID("proj1", "FRESH")
	old := time.Now().Add(-25 * time.Hour).Format(time.RFC3339)
	fresh := time.Now().Add(-time.Minute).Format(time.RFC3339)
	stateDir := filepath.Join(home, "state")
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	blob := `[
	  {"id":"` + staleID + `","source":"native","project":"proj1","issue":"STALE","status":"dead","first_seen":"` + old + `","last_seen":"` + old + `"},
	  {"id":"` + freshID + `","source":"native","project":"proj1","issue":"FRESH","status":"working","first_seen":"` + fresh + `","last_seen":"` + fresh + `"}
	]`
	if err := os.WriteFile(filepath.Join(stateDir, "sessions.json"), []byte(blob), 0o600); err != nil {
		t.Fatal(err)
	}

	d := newDaemon(nativeTestConfig(nativePoll("p1")), &linear.Fake{}, log.New(io.Discard, "", 0), home)
	d.native = &fakeNative{alive: map[string]bool{freshID: true}} // stale pane gone, fresh alive
	(&fakeObsSeams{}).install(d)

	d.observe(context.Background())

	snap := d.sessions.Snapshot()
	if len(snap) != 1 || snap[0].ID != freshID {
		t.Fatalf("snapshot after prune = %+v, want only the fresh session", snap)
	}
}

// --- Shutdown stops the loop --------------------------------------------------

func TestObserveLoopStopsOnShutdown(t *testing.T) {
	d := newTestDaemon(t, nativeTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeNative{})
	(&fakeObsSeams{}).install(d)

	ctx, cancel := context.WithCancel(context.Background())
	d.wg.Add(1)
	go d.observeLoop(ctx)
	cancel()

	done := make(chan struct{})
	go func() {
		d.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("observeLoop did not stop on shutdown (goroutine leak)")
	}
}

// --- Age formatting -------------------------------------------------------------

func TestFormatAge(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{-time.Second, "0s"},
		{42 * time.Second, "42s"},
		{12 * time.Minute, "12m"},
		{3*time.Hour + 5*time.Minute, "3h05m"},
		{50 * time.Hour, "2d2h"},
	}
	for _, c := range cases {
		if got := formatAge(c.d); got != c.want {
			t.Errorf("formatAge(%v) = %q, want %q", c.d, got, c.want)
		}
	}
}
