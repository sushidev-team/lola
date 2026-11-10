package daemon

import (
	"context"
	"testing"

	"github.com/sushidev-team/lola/internal/linear"
)

// concurrency_cap bounds how many sessions a project RUNS. It used to bound
// only how many one tick started, so a project at its cap kept spawning up to
// the cap again every tick until global_cap stopped it.
func TestTickProjectCapCountsItsOwnLiveSessions(t *testing.T) {
	is := testIssue("FE-1", 1, "2024-01-01T00:00:00Z")
	fake := &linear.Fake{Issues: []linear.Issue{is}}
	nat := &fakeNative{}
	p := seenPoll("p1")
	p.ConcurrencyCap = 2
	cfg := nativeTestConfig(p)
	cfg.Defaults.GlobalCap = 10
	d := newTestDaemon(t, cfg, fake, nat)
	d.sessions.Upsert(nativeSess("FE-90", "working"))
	d.sessions.Upsert(nativeSess("FE-91", "ci_pending"))

	res, err := d.tick(context.Background(), "p1", false)
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	if len(nat.spawnCalls()) != 0 {
		t.Errorf("project at its cap must not spawn, got %v", nat.spawnCalls())
	}
	if m := findMatch(t, res, "FE-1"); m.Action != "skipped" || m.Reason != "capped" {
		t.Errorf("match = %+v, want skipped/capped", m)
	}
}

// A spawn still in flight on another poll has no store record yet; its
// reserved slot must count, or two polls overshoot global_cap together.
func TestTickCountsSpawnsInFlightOnOtherPolls(t *testing.T) {
	is := testIssue("FE-1", 1, "2024-01-01T00:00:00Z")
	fake := &linear.Fake{Issues: []linear.Issue{is}}
	nat := &fakeNative{}
	cfg := nativeTestConfig(seenPoll("p1"))
	cfg.Defaults.GlobalCap = 2
	d := newTestDaemon(t, cfg, fake, nat)
	d.sessions.Upsert(nativeSess("FE-90", "working"))
	release, ok := d.reserveSlots("other", 1, noProjectCap, 2)
	if !ok {
		t.Fatal("reserve: want ok")
	}

	if _, err := d.tick(context.Background(), "p1", false); err != nil {
		t.Fatalf("tick: %v", err)
	}
	if len(nat.spawnCalls()) != 0 {
		t.Fatalf("in-flight reservation must fill global_cap, got %v", nat.spawnCalls())
	}

	release()
	release() // idempotent
	if g, _ := d.slotCounts("other"); g != 1 {
		t.Fatalf("after release global = %d, want 1 (the live session only)", g)
	}
	if _, err := d.tick(context.Background(), "p1", false); err != nil {
		t.Fatalf("tick: %v", err)
	}
	if len(nat.spawnCalls()) != 1 {
		t.Fatalf("spawns after release = %v, want 1", nat.spawnCalls())
	}
	if g, own := d.slotCounts("p1"); d.slots.total != 0 || g != 2 || own != 2 {
		t.Fatalf("after spawn: reserved=%d global=%d own=%d, want 0/2/2", d.slots.total, g, own)
	}
}
