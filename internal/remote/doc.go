// Package remote is the daemon's PHONE LISTENER: a TLS socket speaking the
// versioned envelope in internal/protocol, so a paired mobile client can read
// the session list, answer a parked agent and attach to a pane.
//
// It is deliberately NOT the unix socket with TLS bolted on. internal/daemon's
// handleConn speaks unauthenticated newline JSON with no envelope, no peer
// identity and no capability filter, and it is strictly serial per connection —
// handing it a network conn would be a straight line from the LAN to stop,
// kill --force and renameProject, and would let one slow command block every
// other request the phone has in flight. So this package takes the daemon's
// dispatcher as a plain func seam (Options.Handle, bound to d.handle in
// production) and reaches it only through frames it has already versioned,
// direction-checked and authorized. The raw socket vocabulary is unreachable
// from TCP by construction rather than by review.
//
// # What is in this package and what is not
//
// Framing, dispatch, the Handle seam, the authorizer interface and the whole
// pane path are TAG-FREE: they compile and are unit-tested in an ordinary
// `make test` run. Exactly two symbols are split by //go:build lola_insecure —
// newAuthorizer and Listen — and they are the two that can actually open a
// port. M1 has no cryptography at all, and its stand-in for pairing is a bearer
// key read from LOLA_REMOTE_INSECURE_KEY; "deleted, not disabled, in M2" is a
// promise enforced by memory, so it is enforced by the compiler instead. In an
// untagged build newAuthorizer refuses, Listen therefore binds nothing, and one
// log line names both the reason and the way out — a silently dead port is the
// failure this split exists to avoid. While the tag IS active two extra rails
// hold: the bind mode is forced to loopback whatever config says, because a
// shared bearer secret must never reach a network interface, and every accept
// logs a warning, because a daemon running this path should be impossible to
// forget about.
//
// Cryptography, the device registry, capability tiers, the per-command Request
// rebuild and the pairing frames are M2 and are not here. What IS here from the
// security model, because it is a property of the transport rather than of the
// policy layer above it: the unconditional command denials (policy.go), which
// no authorizer can grant, and the envelope's own fail-closed rules.
//
// # Fail closed, and what each refusal costs
//
// An unknown envelope version, an unknown frame type, a frame travelling the
// wrong direction, an oversized or unframable message, and a denied command all
// REFUSE, LOG and CLOSE the connection, mutating nothing. Closing rather than
// skipping is the point: a stream whose framing or whose caller cannot be
// trusted cannot be resynchronized, and a client that asked for a denied
// command is either broken or hostile. A malformed PAYLOAD on a well-formed
// envelope is the one recoverable case — the refusal goes back on that frame's
// id and the connection survives, because the framing is still intact.
//
// # Multiplexing
//
// One reader goroutine per connection decodes each frame and routes it without
// ever waiting on the work it dispatched:
//
//   - req frames run concurrently behind a semaphore of reqConcurrency, so a
//     five-minute openTicket cannot delay a sessions refresh on the same
//     connection. A request arriving with the semaphore full is refused with
//     CodeRateLimited rather than queued, because queueing it in the reader is
//     exactly the head-of-line block this design exists to remove.
//   - sub, unsub and pty frames go to ONE ordered per-connection worker.
//     Keystrokes must arrive in the order they were typed, and a pty write may
//     have to cancel copy mode first, which execs tmux — so they are serialized
//     with each other and with the subscribe that precedes them, but never with
//     a request.
//
// Payloads are decoded on the reader goroutine before anything is handed off:
// protocol.FrameReader reuses its body buffer, so a payload that crossed a
// goroutine boundary undecoded would be read after the next frame overwrote it.
//
// # Shutdown
//
// Server.Close is bounded and synchronous: it closes every listener (so Accept
// returns net.ErrClosed exactly as the daemon's unix path does), then closes
// every live connection, which unblocks its reader and its writers, and only
// then waits. Nothing here may be registered with the daemon's connWg, whose
// drain is unbounded and was built for bounded socket work — a stream a phone
// holds open for six hours would hang graceful shutdown until the phone
// happened to disconnect. The guarantee that this package drains promptly comes
// from the server closing its own streams, not from the peer behaving.
//
// # Daemon-side wiring
//
// internal/daemon/remotewire.go holds the fields and the two functions; nothing
// in this package imports internal/daemon. startRemote is called from Run right
// after the review worker and before `go d.serve(ctx, ln)`, and stopRemote is
// the FIRST statement of the shutdown block, ahead of stopAllWorkers, wg.Wait
// and drainConnWork:
//
//	// daemon.go, in Run:
//	d.startRemote(ctx)
//
//	// daemon.go, in the shutdown block, BEFORE d.stopAllWorkers():
//	d.stopRemote()
//
//	// remotewire.go:
//	func (d *Daemon) startRemote(ctx context.Context) {
//	    d.mu.Lock()
//	    rc := d.cfg.Remote
//	    d.mu.Unlock()
//	    if !rc.Listens() {
//	        return
//	    }
//	    home, err := config.Home()
//	    if err != nil {
//	        d.logf("", "remote: %v", err)
//	        return
//	    }
//	    srv, err := remote.Listen(ctx, remote.Options{
//	        Bind:   rc.BindMode(),
//	        Port:   rc.ListenPort(),
//	        Dir:    home,
//	        Handle: d.handle,
//	        Panes:  d.panes,   // a remote.PaneBus; see the adapter note below
//	        Now:    time.Now,
//	        Logf:   d.remoteLogf,
//	    })
//	    if err != nil {
//	        d.logf("", "remote: not listening: %v", err) // never fatal to Run
//	        return
//	    }
//	    d.remote = srv
//	}
//
//	func (d *Daemon) stopRemote() {
//	    if d.remote == nil {
//	        return
//	    }
//	    d.remote.Close() // bounded: closes listeners, closes streams, waits
//	    d.remote = nil
//	}
//
// handleReload compares old.Remote against the new config under d.mu and calls
// stopRemote then startRemote after the unlock, the pattern by which d.native is
// recreated. config.RemoteConfig is comparable, so the diff is `!=`.
//
// Options.Panes is a PaneBus (see panebus.go), declared HERE rather than taken
// as an internal/panebus type. That is not only about compiling the two
// packages in parallel: a channel type is invariant in Go, so no foreign frame
// type can ever structurally satisfy a Frames() <-chan PaneFrame, and the
// daemon — which owns pane-name resolution against the session store anyway —
// is the right place for the adapter. Write and Scroll already match
// internal/panebus method for method; Subscribe and the frame type do not, so
// remotewire.go carries this:
//
//	// remotePanes adapts *panebus.Registry to remote.PaneBus. The registry
//	// numbers frames per PANE, drops included, so Seq is forwarded verbatim:
//	// renumbering it would hide the drops it exists to expose.
//	type remotePanes struct{ reg *panebus.Registry }
//
//	func (p remotePanes) Write(pane string, b []byte) error { return p.reg.Write(pane, b) }
//
//	func (p remotePanes) Scroll(ctx context.Context, pane string, lines int) error {
//	    return p.reg.Scroll(ctx, pane, lines)
//	}
//
//	// cols and rows are advisory: the registry attaches at the tmux WINDOW
//	// size and the phone pans client-side, so they are deliberately dropped.
//	func (p remotePanes) Subscribe(ctx context.Context, pane string, _, _ int) (remote.PaneStream, error) {
//	    sub, err := p.reg.Subscribe(ctx, pane)
//	    if err != nil {
//	        return nil, err
//	    }
//	    return &remoteStream{sub: sub, out: make(chan remote.PaneFrame, 64)}, nil // pump below
//	}
//
// The stream wrapper is a goroutine copying panebus.Frame to remote.PaneFrame
// (KindResync/KindOutput/KindExit to PaneResync/PaneOutput/PaneExit, Screen's
// W/H to Cols/Rows, Seq straight across) and closing its output channel when
// the registry's channel closes. Close closes the panebus.Sub; the copier
// then ends on its own.
//
// The registry's Resolve seam is where the daemon's OWN name check goes — the
// session-store lookup that decides whether this pane belongs to a session the
// device may see. panebus re-checks the anchored shape as defence in depth, but
// only the daemon can answer the identity question, and an unresolvable name
// must fail closed there rather than reach a tmux argv.
package remote
