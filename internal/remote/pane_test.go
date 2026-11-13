package remote

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/sushidev-team/lola/internal/protocol"
)

func (r *rig) sub(id, pane string, cols, rows int) {
	r.t.Helper()
	f := protocol.Frame{V: protocol.FrameVersionCurrent, Type: protocol.FrameSub, ID: id, Pane: pane}
	if err := f.SetPayload(protocol.SubPayload{Cols: cols, Rows: rows}); err != nil {
		r.t.Fatalf("payload: %v", err)
	}
	r.send(f)
}

func (r *rig) pty(pane string, in protocol.PTYInputPayload) {
	r.t.Helper()
	f := protocol.Frame{V: protocol.FrameVersionCurrent, Type: protocol.FramePTY, Pane: pane}
	if err := f.SetPayload(in); err != nil {
		r.t.Fatalf("payload: %v", err)
	}
	r.send(f)
}

func (r *rig) wantResync(pane string) (protocol.Frame, protocol.ResyncPayload) {
	r.t.Helper()
	f := r.next()
	if f.Type != protocol.FrameResync {
		r.t.Fatalf("got type %q, want %q", f.Type, protocol.FrameResync)
	}
	if f.Pane != pane {
		r.t.Fatalf("got pane %q, want %q", f.Pane, pane)
	}
	var p protocol.ResyncPayload
	if err := json.Unmarshal(f.Payload, &p); err != nil {
		r.t.Fatalf("decode resync: %v", err)
	}
	return f, p
}

// TestSubscribeReachesTheBusAndIsAcknowledgedByResync pins the contract: a sub
// is acknowledged by the FIRST resync carrying the same id, and the resync
// reports the tmux window's size rather than the phone's viewport, because the
// phone pans client-side and must never shrink the developer's window.
func TestSubscribeReachesTheBusAndIsAcknowledgedByResync(t *testing.T) {
	r := newRig(t)
	r.sub("s3", "lola-fe-42", 55, 34)

	f, p := r.wantResync("lola-fe-42")
	if f.ID != "s3" {
		t.Errorf("got id %q, want the sub frame's id; the resync IS the acknowledgement", f.ID)
	}
	if f.Seq != 1 {
		t.Errorf("got seq %d, want 1", f.Seq)
	}
	if p.Cols != 200 || p.Rows != 50 {
		t.Errorf("got %dx%d, want the window size 200x50 rather than the subscriber's 55x34", p.Cols, p.Rows)
	}
	if p.CursorX != 2 || p.CursorY != 2 || !p.AltScreen {
		t.Errorf("cursor/altScreen lost: %+v", p)
	}
	if len(p.Lines) != 3 {
		t.Errorf("got %d lines, want the emulator's 3", len(p.Lines))
	}
	if subs, _, _ := r.bus.snapshot(); !reflect.DeepEqual(subs, []string{"lola-fe-42"}) {
		t.Errorf("bus saw %v, want one subscribe for lola-fe-42", subs)
	}
}

// TestPaneOutputCarriesIncrementingSeqAndNoID: Seq is what lets a client detect
// a gap and re-subscribe, and a stream of output is nobody's reply, so it
// carries no correlation id.
func TestPaneOutputCarriesIncrementingSeqAndNoID(t *testing.T) {
	r := newRig(t)
	r.sub("s3", "lola-fe-42", 55, 34)
	r.wantResync("lola-fe-42")

	st := r.bus.stream(0)
	st.push(PaneFrame{Kind: PaneOutput, Data: []byte("hello")})
	st.push(PaneFrame{Kind: PaneOutput, Data: []byte("world")})

	for i, want := range []string{"hello", "world"} {
		f := r.next()
		if f.Type != protocol.FramePTY {
			t.Fatalf("frame %d: got type %q, want %q", i, f.Type, protocol.FramePTY)
		}
		if f.ID != "" {
			t.Errorf("frame %d: pty output carried id %q", i, f.ID)
		}
		if f.Seq != uint64(i+2) {
			t.Errorf("frame %d: got seq %d, want %d", i, f.Seq, i+2)
		}
		var p protocol.PTYOutputPayload
		if err := json.Unmarshal(f.Payload, &p); err != nil {
			t.Fatalf("decode pty: %v", err)
		}
		if string(p.Data) != want {
			t.Errorf("frame %d: got %q, want %q", i, p.Data, want)
		}
	}
}

// TestPaneExitIsDistinguishableFromADetach: a death and a detach must not look
// the same, so the terminal frame is an Exited resync rather than a silently
// closed stream.
func TestPaneExitIsDistinguishableFromADetach(t *testing.T) {
	r := newRig(t)
	r.sub("s3", "lola-fe-42", 55, 34)
	r.wantResync("lola-fe-42")

	r.bus.stream(0).push(PaneFrame{Kind: PaneExit})
	_, p := r.wantResync("lola-fe-42")
	if !p.Exited {
		t.Error("the terminal frame did not report Exited")
	}

	// The subscription is gone, so a write is refused rather than forwarded.
	r.pty("lola-fe-42", protocol.PTYInputPayload{Action: protocol.PTYActionWrite, Data: []byte("x")})
	r.wantErr(protocol.CodeUnknownPane)
}

// TestPTYWriteReachesTheBusInOrder. Ordering is the whole reason pane frames
// run on one worker: keystrokes must arrive as they were typed.
func TestPTYWriteReachesTheBusInOrder(t *testing.T) {
	r := newRig(t)
	r.sub("s3", "lola-fe-42", 55, 34)
	r.wantResync("lola-fe-42")

	for _, s := range []string{"a", "b", "c", "\x1b[Z"} {
		r.pty("lola-fe-42", protocol.PTYInputPayload{Action: protocol.PTYActionWrite, Data: []byte(s)})
	}
	// A round trip on the same connection proves the queue has drained.
	r.wantOpen()

	_, writes, _ := r.bus.snapshot()
	want := []string{"lola-fe-42:a", "lola-fe-42:b", "lola-fe-42:c", "lola-fe-42:\x1b[Z"}
	if !reflect.DeepEqual(writes, want) {
		t.Errorf("bus saw %q, want %q", writes, want)
	}
}

// TestPTYWriteWithoutASubscriptionIsRefused: a pty write is authorized by the
// SUBSCRIPTION, so acting on one without it would bypass the check entirely.
func TestPTYWriteWithoutASubscriptionIsRefused(t *testing.T) {
	r := newRig(t)
	r.pty("lola-fe-42", protocol.PTYInputPayload{Action: protocol.PTYActionWrite, Data: []byte("rm -rf /")})
	r.wantErr(protocol.CodeUnknownPane)

	if _, writes, _ := r.bus.snapshot(); len(writes) != 0 {
		t.Errorf("bus saw %q, want nothing written", writes)
	}
}

// TestScrollDelegatesToTheBus. The client must never synthesize wheel bytes: on
// an alternate screen the program owns the wheel and copy mode moves nothing,
// and only the bus knows which of the two applies.
func TestScrollDelegatesToTheBus(t *testing.T) {
	r := newRig(t)
	r.sub("s3", "lola-fe-42", 55, 34)
	r.wantResync("lola-fe-42")

	r.pty("lola-fe-42", protocol.PTYInputPayload{Action: protocol.PTYActionScroll, Lines: -12})
	r.wantOpen()

	if _, _, scrolls := r.bus.snapshot(); !reflect.DeepEqual(scrolls, []int{-12}) {
		t.Errorf("bus saw %v, want [-12]", scrolls)
	}
}

// TestUnknownPTYActionIsRefused: guessing here would type bytes into a live
// agent, so an action this build does not know is refused rather than
// approximated.
func TestUnknownPTYActionIsRefused(t *testing.T) {
	r := newRig(t)
	r.sub("s3", "lola-fe-42", 55, 34)
	r.wantResync("lola-fe-42")

	r.pty("lola-fe-42", protocol.PTYInputPayload{Action: "paste", Data: []byte("x")})
	r.wantErr(protocol.CodeUnknownType)
	if _, writes, _ := r.bus.snapshot(); len(writes) != 0 {
		t.Errorf("bus saw %q, want nothing written", writes)
	}
}

// TestResizeIsRecordedAndIgnored: the bus attaches at the tmux window size, so
// honouring a phone's resize would shrink the developer's window. It must not
// reach the bus and must not break the stream.
func TestResizeIsRecordedAndIgnored(t *testing.T) {
	r := newRig(t)
	r.sub("s3", "lola-fe-42", 55, 34)
	r.wantResync("lola-fe-42")

	r.pty("lola-fe-42", protocol.PTYInputPayload{Action: protocol.PTYActionResize, Cols: 40, Rows: 20})
	r.bus.stream(0).push(PaneFrame{Kind: PaneOutput, Data: []byte("still here")})

	f := r.next()
	if f.Type != protocol.FramePTY {
		t.Fatalf("got type %q after a resize, want the stream to continue", f.Type)
	}
	if _, writes, scrolls := r.bus.snapshot(); len(writes) != 0 || len(scrolls) != 0 {
		t.Errorf("a resize reached the bus: writes=%q scrolls=%v", writes, scrolls)
	}
}

// TestResubscribeReplacesTheStreamAndRestartsSeq. A re-subscribe is the
// client's defined recovery from a Seq gap, so it must work rather than be
// refused as a duplicate.
func TestResubscribeReplacesTheStreamAndRestartsSeq(t *testing.T) {
	r := newRig(t)
	r.sub("s1", "lola-fe-42", 55, 34)
	f, _ := r.wantResync("lola-fe-42")
	if f.Seq != 1 {
		t.Fatalf("first resync seq %d, want 1", f.Seq)
	}
	r.bus.stream(0).push(PaneFrame{Kind: PaneOutput, Data: []byte("x")})
	r.next()

	r.sub("s2", "lola-fe-42", 55, 34)
	f2, _ := r.wantResync("lola-fe-42")
	if f2.ID != "s2" {
		t.Errorf("got id %q, want the new subscription's id", f2.ID)
	}
	if f2.Seq != 1 {
		t.Errorf("got seq %d after a re-subscribe, want the sequence to restart at 1", f2.Seq)
	}
	if !r.bus.stream(0).closed.Load() {
		t.Error("the replaced stream was not closed; a re-subscribe would leak a tmux client")
	}
}

// TestSubscribeFailsClosedOnAnUnavailablePane: an unresolvable name and a name
// the device may not see must look identical, or the refusal enumerates panes.
func TestSubscribeFailsClosedOnAnUnavailablePane(t *testing.T) {
	r := newRig(t, func(r *rig, _ *Options) {
		r.bus.refusePane("lola-other-1", errors.New("no such session lola-other-1"))
	})
	r.sub("s3", "lola-other-1", 55, 34)

	p := r.wantErr(protocol.CodeUnknownPane)
	if strings.Contains(p.Message, "lola-other-1") || strings.Contains(p.Message, "no such session") {
		t.Errorf("the refusal %q repeats the bus's reason back to the peer", p.Message)
	}
	r.wantOpen()
}

// TestSubscribeIsRefusedWithoutAPaneBus: a daemon built with no bus still
// serves the read commands, and refuses subscriptions rather than panicking.
func TestSubscribeIsRefusedWithoutAPaneBus(t *testing.T) {
	r := newRig(t, func(_ *rig, o *Options) { o.Panes = nil })
	r.sub("s3", "lola-fe-42", 55, 34)
	r.wantErr(protocol.CodeUnknownPane)
	r.wantOpen()
}

// TestUnsubscribeClosesTheStream. The bus refcounts on this, so a leaked
// subscription is a leaked tmux client and a pane that never tears down.
func TestUnsubscribeClosesTheStream(t *testing.T) {
	r := newRig(t)
	r.sub("s3", "lola-fe-42", 55, 34)
	r.wantResync("lola-fe-42")

	r.send(protocol.Frame{V: protocol.FrameVersionCurrent, Type: protocol.FrameUnsub, Pane: "lola-fe-42"})
	r.wantOpen()

	if !r.bus.stream(0).closed.Load() {
		t.Error("unsubscribe did not close the stream")
	}
	r.pty("lola-fe-42", protocol.PTYInputPayload{Action: protocol.PTYActionWrite, Data: []byte("x")})
	r.wantErr(protocol.CodeUnknownPane)
}

// TestPaneFrameWithNoPaneNameIsRefused: the name reaches a tmux argv, so an
// empty one is refused here rather than resolved downstream.
func TestPaneFrameWithNoPaneNameIsRefused(t *testing.T) {
	r := newRig(t)
	r.sub("s3", "", 55, 34)
	r.wantErr(protocol.CodeUnknownPane)
	if subs, _, _ := r.bus.snapshot(); len(subs) != 0 {
		t.Errorf("bus saw %v, want no subscribe", subs)
	}
	r.wantOpen()
}

// TestShutdownClosesEverySubscription proves the teardown ordering: closing the
// connection releases its streams, which is what lets Server.Close finish
// without waiting on a phone.
func TestShutdownClosesEverySubscription(t *testing.T) {
	r := newRig(t)
	r.sub("s1", "lola-fe-42", 55, 34)
	r.wantResync("lola-fe-42")
	r.sub("s2", "lola-fe-43", 55, 34)
	r.wantResync("lola-fe-43")

	r.srv.closeConns()
	r.wantClosed()

	for i := 0; i < 2; i++ {
		if st := r.bus.stream(i); st == nil || !st.closed.Load() {
			t.Errorf("stream %d was left open after the connection closed", i)
		}
	}
}

// TestBusSequenceIsForwardedVerbatimIncludingAGap. The bus counts per PANE
// across every frame it produced, dropped ones included, so renumbering here
// would make a drop invisible — which is the one thing Seq exists to expose.
func TestBusSequenceIsForwardedVerbatimIncludingAGap(t *testing.T) {
	r := newRig(t)
	r.sub("s1", "lola-fe-42", 55, 34)

	// The fake's opening resync is unnumbered, so the connection numbers it 1;
	// everything after arrives with the bus's own numbers, including a jump
	// from 7 to 12 standing for five frames dropped for a slow subscriber.
	if f, _ := r.wantResync("lola-fe-42"); f.Seq != 1 {
		t.Fatalf("opening resync seq %d, want the connection's own 1", f.Seq)
	}
	st := r.bus.stream(0)
	st.push(PaneFrame{Kind: PaneOutput, Data: []byte("a"), Seq: 7})
	st.push(PaneFrame{Kind: PaneResync, Seq: 12, Screen: &PaneScreen{Cols: 200, Rows: 50}})
	st.push(PaneFrame{Kind: PaneOutput, Data: []byte("b"), Seq: 13})

	for i, want := range []uint64{7, 12, 13} {
		f := r.next()
		if f.Seq != want {
			t.Errorf("frame %d: got seq %d, want the bus's %d", i, f.Seq, want)
		}
	}
}

// TestPaneKindZeroValueIsResync mirrors internal/panebus, where a subscription
// always opens with one. A mis-mapped zero must degrade into a redundant
// repaint, never into raw bytes rendered as a screen.
func TestPaneKindZeroValueIsResync(t *testing.T) {
	var k PaneKind
	if k != PaneResync {
		t.Fatalf("the zero PaneKind is %d, want PaneResync", k)
	}
}

// TestAPaneWriteFailureIsReportedWithoutClosing. A tmux that refuses one write
// is not a protocol violation: the client is told, on the pane it named, and
// the stream carries on.
func TestAPaneWriteFailureIsReportedWithoutClosing(t *testing.T) {
	r := newRig(t, func(r *rig, _ *Options) { r.bus.failWrites(errors.New("tmux: no such pane")) })
	r.sub("s1", "lola-fe-42", 55, 34)
	r.wantResync("lola-fe-42")

	r.pty("lola-fe-42", protocol.PTYInputPayload{Action: protocol.PTYActionWrite, Data: []byte("x")})
	p := r.wantErr(protocol.CodeInternal)
	if strings.Contains(p.Message, "no such pane") {
		t.Errorf("the refusal repeated the bus's error to the peer: %q", p.Message)
	}
	r.wantOpen()
}

// TestOversizedPaneNameIsRefusedRatherThanAnswerlessly pins that a refusal is
// always REPRESENTABLE.
//
// refusePane echoes the peer's own pane string back into the err frame, so a
// name near the frame cap made the encoded refusal exceed that cap: the writer
// returned ErrFrameTooLarge and wrote nothing at all, and the peer saw silence
// where the protocol promises a coded refusal — indistinguishable from a hang.
// The same bound stops a megabyte of peer-supplied string reaching the pane
// layer and, through its error, the daemon's unrotated log.
func TestOversizedPaneNameIsRefusedRatherThanAnswerlessly(t *testing.T) {
	r := newRig(t)
	huge := "lola-" + strings.Repeat("a", 600*1024)
	r.sub("s1", huge, 80, 24)

	p := r.wantErr(protocol.CodeUnknownPane)
	if p.Code != protocol.CodeUnknownPane {
		t.Fatalf("got %q", p.Code)
	}
	subs, _, _ := r.bus.snapshot()
	if len(subs) != 0 {
		t.Errorf("an oversized name reached the pane bus: %q", subs)
	}
	if n := len(r.log.all()); n > 4096 {
		t.Errorf("the refusal wrote %d bytes to the log; a peer must not be able to drive that", n)
	}
	r.wantOpen()
}

// TestExitIsAnnouncedOnlyAfterTheSubscriptionIsDropped is the ordering that
// made TestPaneExitIsDistinguishableFromADetach flaky, stated directly.
//
// c.subs is what authorizes a pty frame, and a client is entitled to react to
// the exit frame the instant it arrives. With the write first, the reply's
// keystrokes found the subscription still registered and were forwarded to a
// pane that no longer exists — the peer got "write failed" where the protocol
// promises unknown_pane, and a tmux session reusing the name (session ids are
// reused, e.g. across revive) could have taken those keystrokes for real.
func TestExitIsAnnouncedOnlyAfterTheSubscriptionIsDropped(t *testing.T) {
	for i := 0; i < 40; i++ {
		func() {
			r := newRig(t)
			r.sub("s1", "lola-fe-42", 80, 24)
			r.wantResync("lola-fe-42")

			st := r.bus.stream(0)
			if st == nil {
				t.Fatal("no stream")
			}
			st.push(PaneFrame{Kind: PaneExit})

			if _, p := r.wantResync("lola-fe-42"); !p.Exited {
				t.Fatal("the terminal frame does not report an exit")
			}
			// The reply a client is allowed to send the moment it sees the exit.
			r.pty("lola-fe-42", protocol.PTYInputPayload{Action: protocol.PTYActionWrite, Data: []byte("y\r")})
			r.wantErr(protocol.CodeUnknownPane)

			_, writes, _ := r.bus.snapshot()
			if len(writes) != 0 {
				t.Fatalf("iteration %d: a write was forwarded to a dead pane: %q", i, writes)
			}
		}()
	}
}

// TestDropSubIfOnlyRemovesTheSubscriptionItWasGiven pins the identity check on
// the background drop — the same check panebus.Registry.drop makes, for the
// same reason.
//
// A pump noticing its pane died can be arbitrarily far behind a client that has
// already re-subscribed (a re-subscribe is the client's DEFINED recovery from a
// Seq gap, so it is a routine event, not an exotic one). It may have taken the
// exit frame off the channel and then been descheduled while the replacement
// took the map entry. An unconditional delete-by-name there would unregister
// and then close the REPLACEMENT, leaving the client believing it is attached
// to a pane the daemon no longer streams — and every keystroke it sends
// refused. dropSub keeps the by-name form because an explicit unsub and a
// re-subscribe both mean "whatever is attached for this pane".
func TestDropSubIfOnlyRemovesTheSubscriptionItWasGiven(t *testing.T) {
	r := newRig(t)
	r.sub("s1", "lola-fe-42", 80, 24)
	r.wantResync("lola-fe-42")

	c := r.serverConn()
	corpse := c.getSub("lola-fe-42")
	if corpse == nil {
		t.Fatal("no subscription registered")
	}

	// The replacement a re-subscribe would install.
	live := &subscription{pane: "lola-fe-42", id: "s2"}
	c.mu.Lock()
	c.subs["lola-fe-42"] = live
	c.mu.Unlock()

	if got := c.dropSubIf("lola-fe-42", corpse); got != nil {
		t.Error("a stale pump removed a subscription it does not own")
	}
	if c.getSub("lola-fe-42") != live {
		t.Fatal("the replacement was unregistered by a late exit on the previous stream")
	}
	if got := c.dropSubIf("lola-fe-42", live); got != live {
		t.Error("the owning pump could not drop its own subscription")
	}
	if c.getSub("lola-fe-42") != nil {
		t.Error("the subscription survived its owner's drop")
	}
}

// TestResyncStatesCursorVisibilityInTheNegative pins the one inversion on the
// way to the wire.
//
// The pane layer mirrors the emulator, which models DECTCEM as "enabled"; the
// wire mirrors what is cheap and safe to omit. A visible caret is the
// overwhelmingly common case AND the safe one to assume, so omitting it costs
// nothing and — the load-bearing half — a client that reads the field against a
// daemon too old to write it degrades to "visible" rather than painting no
// caret at all on every pane it attaches to.
func TestResyncStatesCursorVisibilityInTheNegative(t *testing.T) {
	visible := resyncPayload(PaneFrame{Kind: PaneResync, Screen: &PaneScreen{Cols: 80, Rows: 24, CursorVisible: true}})
	if visible.CursorHidden {
		t.Error("a visible caret was marked hidden")
	}
	hidden := resyncPayload(PaneFrame{Kind: PaneResync, Screen: &PaneScreen{Cols: 80, Rows: 24, CursorVisible: false}})
	if !hidden.CursorHidden {
		t.Error("a hidden caret was not marked hidden")
	}

	// The omitted field decodes to "visible", which is the direction a wire
	// default has to fail in.
	body, err := json.Marshal(visible)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(body), "cursorHidden") {
		t.Errorf("the common case is on the wire: %s", body)
	}
	var back protocol.ResyncPayload
	if err := json.Unmarshal(body, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back.CursorHidden {
		t.Error("an absent cursorHidden decoded to hidden; the default must be visible")
	}
}
