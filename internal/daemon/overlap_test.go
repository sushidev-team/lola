package daemon

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/sushidev-team/lola/internal/linear"
	"github.com/sushidev-team/lola/internal/session"
	"github.com/sushidev-team/lola/internal/state"
)

// overlapDaemon seeds sessions with real worktree directories (the pass skips a
// checkout that is gone) and a fake changed-files seam keyed by worktree.
func overlapDaemon(t *testing.T, files map[string][]string, idents ...string) (*Daemon, map[string]string) {
	t.Helper()
	d := newTestDaemon(t, nativeTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeNative{})
	ids := map[string]string{}
	byDir := map[string][]string{}
	for _, ident := range idents {
		s := nativeSess(ident, "working")
		s.Worktree = filepath.Join(t.TempDir(), ident)
		if err := os.MkdirAll(s.Worktree, 0o755); err != nil {
			t.Fatal(err)
		}
		d.sessions.Upsert(s)
		ids[ident] = s.ID
		byDir[s.Worktree] = files[ident]
	}
	d.changedFiles = func(_ context.Context, dir, base string) ([]string, error) {
		if base != "main" {
			t.Errorf("base = %q, want the project's default branch", base)
		}
		if f, ok := byDir[dir]; ok && f != nil {
			return f, nil
		}
		return nil, errors.New("no merge-base")
	}
	return d, ids
}

// The acceptance case: two sessions editing the same file are flagged — on
// both sides, naming the other — while a third that touches nothing shared is
// not.
func TestOverlapFlagsSessionsEditingTheSameFile(t *testing.T) {
	d, ids := overlapDaemon(t, map[string][]string{
		"FE-1": {"app/user.go", "README.md"},
		"FE-2": {"app/user.go", "app/order.go"},
		"FE-3": {"docs/x.md"},
	}, "FE-1", "FE-2", "FE-3")
	d.reconcileOverlaps(context.Background())

	a, _ := d.sessions.Get(ids["FE-1"])
	if len(a.Overlaps) != 1 || a.Overlaps[0].Session != ids["FE-2"] || a.Overlaps[0].Issue != "FE-2" ||
		len(a.Overlaps[0].Files) != 1 || a.Overlaps[0].Files[0] != "app/user.go" {
		t.Errorf("FE-1 overlaps = %+v", a.Overlaps)
	}
	b, _ := d.sessions.Get(ids["FE-2"])
	if len(b.Overlaps) != 1 || b.Overlaps[0].Session != ids["FE-1"] {
		t.Errorf("FE-2 overlaps = %+v", b.Overlaps)
	}
	c, _ := d.sessions.Get(ids["FE-3"])
	if len(c.Overlaps) != 0 {
		t.Errorf("FE-3 overlaps = %+v, want none", c.Overlaps)
	}

	// On the wire.
	for _, si := range d.sessionsData().Sessions {
		if si.ID == ids["FE-1"] && (len(si.Overlaps) != 1 || si.Overlaps[0].Files[0] != "app/user.go") {
			t.Errorf("wire overlaps = %+v", si.Overlaps)
		}
	}
}

// Derived, not remembered: once the shared edit is gone the flag clears.
func TestOverlapClearsWhenTheCollisionIsGone(t *testing.T) {
	files := map[string][]string{"FE-1": {"a.go"}, "FE-2": {"a.go"}}
	d, ids := overlapDaemon(t, files, "FE-1", "FE-2")
	d.reconcileOverlaps(context.Background())
	if s, _ := d.sessions.Get(ids["FE-1"]); len(s.Overlaps) != 1 {
		t.Fatalf("want an overlap first, got %+v", s.Overlaps)
	}
	// FE-2's PR merged: it no longer takes part.
	d.sessions.Update(ids["FE-2"], func(cur *session.Session) bool {
		cur.SetDelivery(state.DeliveryMerged, cur.LastSeen)
		return true
	})
	d.reconcileOverlaps(context.Background())
	if s, _ := d.sessions.Get(ids["FE-1"]); len(s.Overlaps) != 0 {
		t.Errorf("overlap must clear once the other PR merged, got %+v", s.Overlaps)
	}
}

// A worktree git cannot read contributes nothing — no flag on a guess.
func TestOverlapFailsOpenTowardSilence(t *testing.T) {
	d, ids := overlapDaemon(t, map[string][]string{"FE-1": {"a.go"}}, "FE-1", "FE-2")
	d.reconcileOverlaps(context.Background())
	d.reconcileOverlaps(context.Background())
	if s, _ := d.sessions.Get(ids["FE-1"]); len(s.Overlaps) != 0 {
		t.Errorf("overlaps = %+v, want none", s.Overlaps)
	}
	if d.overlapWarned[ids["FE-2"]] == "" {
		t.Error("the unreadable worktree should be remembered so it logs once")
	}
}

func TestOverlapCapsTheFileList(t *testing.T) {
	var many []string
	for i := 0; i < overlapMaxFiles+5; i++ {
		many = append(many, "f"+itoa(100+i))
	}
	d, ids := overlapDaemon(t, map[string][]string{"FE-1": many, "FE-2": many}, "FE-1", "FE-2")
	d.reconcileOverlaps(context.Background())
	s, _ := d.sessions.Get(ids["FE-1"])
	if len(s.Overlaps) != 1 || len(s.Overlaps[0].Files) != overlapMaxFiles || s.Overlaps[0].More != 5 {
		t.Errorf("overlap = %+v", s.Overlaps)
	}
}
