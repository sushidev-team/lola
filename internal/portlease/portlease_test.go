package portlease

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

func newReg(t *testing.T) (*Registry, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "state", "ports.json")
	r, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	r.InUse = func(int) bool { return false }
	return r, path
}

func TestAcquireAllocatesAlignedBlocksAndIsIdempotent(t *testing.T) {
	r, _ := newReg(t)
	a, err := r.Acquire("s-a", "app", 4100, 4199, 2)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(a.Ports, []int{4100, 4101}) || a.Port() != 4100 {
		t.Fatalf("first lease = %v", a.Ports)
	}
	b, err := r.Acquire("s-b", "app", 4100, 4199, 2)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(b.Ports, []int{4102, 4103}) {
		t.Fatalf("second lease = %v, want the next aligned block", b.Ports)
	}
	again, err := r.Acquire("s-a", "app", 4100, 4199, 2)
	if err != nil || !slices.Equal(again.Ports, a.Ports) {
		t.Fatalf("re-acquire = %v, %v; want the same ports back", again.Ports, err)
	}
}

func TestAcquireReplacesALeaseThatNoLongerFits(t *testing.T) {
	r, _ := newReg(t)
	if _, err := r.Acquire("s-a", "app", 4100, 4199, 1); err != nil {
		t.Fatal(err)
	}
	l, err := r.Acquire("s-a", "app", 5000, 5099, 3)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(l.Ports, []int{5000, 5001, 5002}) {
		t.Fatalf("resized lease = %v", l.Ports)
	}
}

func TestAcquireSkipsPortsInUseAndOtherProjectsLeases(t *testing.T) {
	r, _ := newReg(t)
	r.InUse = func(p int) bool { return p == 4101 }
	if _, err := r.Acquire("other", "web", 4102, 4103, 2); err != nil {
		t.Fatal(err)
	}
	l, err := r.Acquire("s-a", "app", 4100, 4199, 2)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(l.Ports, []int{4104, 4105}) {
		t.Fatalf("lease = %v, want 4104-4105 (4100-01 busy, 4102-03 leased by another project)", l.Ports)
	}
}

func TestAcquireExhausted(t *testing.T) {
	r, _ := newReg(t)
	if _, err := r.Acquire("s-a", "app", 4100, 4101, 2); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Acquire("s-b", "app", 4100, 4101, 2); !errors.Is(err, ErrExhausted) {
		t.Fatalf("err = %v, want ErrExhausted", err)
	}
	if _, err := r.Acquire("s-c", "app", 4100, 4100, 2); err == nil {
		t.Fatal("a range smaller than the block must be refused")
	}
}

func TestPersistsAcrossOpenAndReleases(t *testing.T) {
	r, path := newReg(t)
	if _, err := r.Acquire("s-a", "app", 4100, 4199, 1); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v, want 0600", fi.Mode().Perm())
	}
	r2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	r2.InUse = func(int) bool { return false }
	if l, ok := r2.Get("s-a"); !ok || l.Port() != 4100 || l.Project != "app" {
		t.Fatalf("reloaded lease = %+v, %v", l, ok)
	}
	// A restarted daemon must not hand the live session's port out again.
	if l, _ := r2.Acquire("s-b", "app", 4100, 4199, 1); l.Port() != 4101 {
		t.Fatalf("after reload the next lease = %v, want 4101", l.Ports)
	}
	if err := r2.Release("s-a"); err != nil {
		t.Fatal(err)
	}
	if err := r2.Release("never-leased"); err != nil {
		t.Fatal(err)
	}
	r3, _ := Open(path)
	if _, ok := r3.Get("s-a"); ok {
		t.Fatal("released lease came back after reload")
	}
}

func TestOpenCorruptFileIsEmptyWithError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ports.json")
	if err := os.WriteFile(path, []byte("{nope"), 0o600); err != nil {
		t.Fatal(err)
	}
	r, err := Open(path)
	if err == nil {
		t.Fatal("want a parse error")
	}
	if r == nil || len(r.All()) != 0 {
		t.Fatal("a corrupt file must still yield a usable empty registry")
	}
}

func TestPruneHonoursKeepAndGrace(t *testing.T) {
	r, _ := newReg(t)
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	r.Now = func() time.Time { return now }
	for _, s := range []string{"live", "gone", "fresh"} {
		if s == "fresh" {
			now = now.Add(time.Hour)
		}
		if _, err := r.Acquire(s, "app", 4100, 4199, 1); err != nil {
			t.Fatal(err)
		}
	}
	dropped, err := r.Prune(func(id string) bool { return id == "live" }, 10*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(dropped, []string{"gone"}) {
		t.Fatalf("dropped = %v, want only the old unkept lease", dropped)
	}
	if _, ok := r.Get("fresh"); !ok {
		t.Fatal("a lease inside the grace window (spawn in flight) was pruned")
	}
}

func TestNilRegistryIsInert(t *testing.T) {
	var r *Registry
	if _, ok := r.Get("x"); ok {
		t.Fatal("nil registry has no leases")
	}
	if err := r.Release("x"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Acquire("x", "p", 1, 2, 1); err == nil {
		t.Fatal("nil registry cannot lease")
	}
}
