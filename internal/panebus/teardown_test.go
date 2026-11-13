package panebus

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

// The tests in this file are regressions for teardown and drop-recovery
// defects. Each of them fails against the code as it stood before its fix, and
// none of them asserts an implementation detail: the subjects are "shutdown
// always completes", "a repaired subscriber is not sent what its repair
// already shows" and "a pane is subscribable again the moment its bus closes".

// TestPaneExitRacingASubscriberCloseAlwaysCompletesTeardown drives the exact
// pair of ordinary events that used to strand a bus forever: a pane dying at
// the same moment a connection tears its subscription down.
//
// bus.shutdown empties bus.subs and only then offers each subscriber its final
// frame, so the membership invariant that makes every other send safe does not
// cover offer. With the mutex released between offer's closed-check and its
// send, a concurrent Sub.Close closed the channel in the gap and the send
// panicked — and the panic was worse than a crash, because bus.guard recovered
// it and the teardown it interrupted never ran: b.terminated stayed open, so
// Registry.Close (and therefore the daemon's stopRemote) blocked forever while
// the tmux attach child and its PTY leaked.
//
// The assertion is deliberately about the OUTCOME rather than about the panic:
// Close must return, and the pane must actually have been closed.
func TestPaneExitRacingASubscriberCloseAlwaysCompletesTeardown(t *testing.T) {
	for i := 0; i < 60; i++ {
		f := NewFake()
		r := f.Registry()

		subs := make([]*Sub, 0, 3)
		for j := 0; j < 3; j++ {
			s, err := r.Subscribe(ctx(t), "lola-fe-42")
			if err != nil {
				t.Fatalf("subscribe: %v", err)
			}
			subs = append(subs, s)
		}
		pane := f.Pane("lola-fe-42")
		if pane == nil {
			t.Fatal("no pane attached")
		}
		// Something buffered, so shutdown has a final output frame to offer as
		// well as the exit frame: two chances to hit the window per subscriber.
		pane.Emit([]byte("tail"))

		start := make(chan struct{})
		done := make(chan struct{})
		go func() {
			defer close(done)
			<-start
			for _, s := range subs {
				_ = s.Close()
			}
		}()
		close(start)
		pane.Exit()
		<-done

		closed := make(chan struct{})
		go func() {
			defer close(closed)
			_ = r.Close()
		}()
		select {
		case <-closed:
		case <-time.After(5 * time.Second):
			t.Fatalf("iteration %d: Registry.Close did not return; a bus teardown was stranded", i)
		}
		if !pane.Closed() {
			t.Fatalf("iteration %d: the tmux attach was never closed", i)
		}
	}
}

// TestRepairDoesNotDeliverBytesItsOwnResyncAlreadyShows pins the flush that
// makes drop-recovery correct rather than merely prompt.
//
// flushLoop drops b.mu before calling repair, and repair then waits for the
// pane's tapMu — the width of one emulator write on a busy pane, which is
// exactly the pane that desyncs. Bytes landing in b.pending in that window are
// RENDERED into the repairing resync and were then still delivered by the next
// flush, so the repaired subscriber applied them twice. Replaying a byte range
// is not idempotent (a newline scrolls twice), so the client recovered into a
// corrupted screen with no Seq gap to tell it anything had gone wrong.
//
// The interleaving is driven directly rather than raced for, so the test is
// deterministic: the subscriber is desynced, bytes arrive, repair runs, and
// then the next flush tick is simulated.
func TestRepairDoesNotDeliverBytesItsOwnResyncAlreadyShows(t *testing.T) {
	f := NewFake()
	r := slowRegistry(f) // no flush or size ticks; this test drives both by hand
	defer r.Close()

	s, err := r.Subscribe(ctx(t), "lola-fe-42")
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	if got := next(t, s); got.Kind != KindResync {
		t.Fatalf("first frame is %v, want a resync", got.Kind)
	}

	b := r.buses["lola-fe-42"]
	if b == nil {
		t.Fatal("no bus registered")
	}
	pane := f.Pane("lola-fe-42")

	// The subscriber has fallen behind: output is being withheld from it and a
	// resync is owed. This is the state enqueueLocked leaves after a drop.
	s.markDesynced()

	// Output arrives while the repair is on its way to the pane's lock. Emit
	// puts it on the modelled SCREEN and hands it to the tap, so the repairing
	// resync below necessarily renders it.
	pane.Emit([]byte("GAP"))

	b.repair()

	// The next flush tick, which is where the duplicate used to be delivered.
	b.mu.Lock()
	b.flushLocked()
	b.mu.Unlock()

	if s.Desynced() {
		t.Fatal("the subscriber was never repaired")
	}
	repaired := next(t, s)
	if repaired.Kind != KindResync {
		t.Fatalf("repair frame is %v, want a resync", repaired.Kind)
	}
	if repaired.Screen == nil || !strings.Contains(strings.Join(repaired.Screen.Lines, "\n"), "GAP") {
		t.Fatalf("the repairing resync does not show the bytes it raced: %+v", repaired.Screen)
	}
	select {
	case f := <-s.Frames():
		if f.Kind == KindOutput && bytes.Contains(f.Data, []byte("GAP")) {
			t.Fatalf("bytes the repairing resync already rendered were delivered again: %q", f.Data)
		}
		t.Fatalf("unexpected frame after the repair: %v", f.Kind)
	case <-time.After(50 * time.Millisecond):
	}
}

// TestSubscribeRecoversWhileAClosedBusIsStillRegistered covers the window after
// every linger expiry.
//
// A bus removes itself from the registry at the very END of its teardown, after
// waiting for its own goroutines — which can include an in-flight size probe
// bounded by ExecTimeout. Until then the map still holds a closed bus whose
// attached flag is set, so ensureAttached did nothing and both of Subscribe's
// attempts got ErrClosed from the same corpse. For a second or two after every
// teardown the pane was simply unsubscribable, which is precisely where a phone
// reconnecting around the linger boundary lands.
func TestSubscribeRecoversWhileAClosedBusIsStillRegistered(t *testing.T) {
	f := NewFake()
	r := slowRegistry(f)
	defer r.Close()

	s, err := r.Subscribe(ctx(t), "lola-fe-42")
	if err != nil {
		t.Fatalf("first subscribe: %v", err)
	}

	// Close the bus without letting its teardown finish, which is what leaves a
	// closed bus registered. The pending drop is then explicitly undone so the
	// map is in exactly the state the linger race produces.
	b := r.buses["lola-fe-42"]
	if b == nil {
		t.Fatal("no bus registered")
	}
	_ = s.Close()
	b.shutdown("test", false)
	b.wait()
	r.mu.Lock()
	r.buses["lola-fe-42"] = b
	r.mu.Unlock()

	s2, err := r.Subscribe(ctx(t), "lola-fe-42")
	if err != nil {
		t.Fatalf("subscribe while a closed bus was still registered: %v", err)
	}
	defer s2.Close()
	if got := next(t, s2); got.Kind != KindResync {
		t.Fatalf("recovered subscription opened with %v, want a resync", got.Kind)
	}
	if r.buses["lola-fe-42"] == b {
		t.Fatal("the closed bus is still registered; the retry did not build a fresh one")
	}
}

// TestOversizedNameIsRefusedWithoutCarryingItselfIntoTheError bounds what an
// untrusted name can put in the daemon's log.
//
// The length check lives inside ValidName, so the names this package REJECTS
// are the ones with no bound on them. A pane name arrives from a remote peer
// capped only by the frame size — a megabyte — and the layer above logs the
// returned error verbatim into a log that is append-only, unrotated and
// mirrored to stderr.
func TestOversizedNameIsRefusedWithoutCarryingItselfIntoTheError(t *testing.T) {
	f := NewFake()
	r := slowRegistry(f)
	defer r.Close()

	huge := "lola-" + strings.Repeat("a", 400*1024)
	_, err := r.Subscribe(ctx(t), huge)
	if err == nil {
		t.Fatal("an oversized name was accepted")
	}
	if n := len(err.Error()); n > MaxPaneNameLen+128 {
		t.Errorf("the refusal carries %d bytes of the peer's own string; it must be clipped", n)
	}
	if f.CallCount() != 0 {
		t.Errorf("an oversized name reached %d seam calls, want none", f.CallCount())
	}
}
