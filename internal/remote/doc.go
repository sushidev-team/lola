// Package remote is the daemon's PHONE LISTENER: a mutual-TLS socket speaking the
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
// direction checked, authorized and rebuilt. The raw socket vocabulary is
// unreachable from TCP by construction rather than by review.
//
// # Who may connect
//
// Every peer presents a client certificate, and TLS refuses it inside the
// handshake unless its SPKI is in the device registry (internal/devices) or a
// pairing window is open: an unpaired laptop on the same WiFi gets a
// bad_certificate alert and never reaches the JSON parser. TLS 1.3 reports
// success to the client before the server checks that certificate, so the
// daemon writes one ready frame first on every authenticated connection and a
// client counts a path as connected only when it arrives. A pairing candidate
// is the one exception: it talks in band (pairing.go) and never authenticates.
//
// There is one Listen and no build tag. A release binary listens when
// [remote] is enabled; the registry decides who gets in.
//
// # What a paired device may do
//
// Capability tiers are a closed allowlist (tiers.go): a command in no tier is
// tier never and unreachable from a phone whatever the registry says, so adding
// a case to d.handle grants a phone nothing until a human writes it into the
// table. fields.go then rebuilds each request from that command's own field
// spec, so a handler that starts reading a new field reads nothing a phone
// chose. Tiers are read live from the registry on every frame, which makes a
// downgrade behave like a revocation: it applies on the next frame, and the
// daemon also drops the device's connections (Server.DropDevice).
//
// # Fail closed, and what each refusal costs
//
// A framing fault — an unknown envelope version, an unknown frame type, a frame
// travelling the wrong direction, an oversized or unframable message — REFUSES,
// LOGS and CLOSES the connection, because a stream whose framing cannot be
// trusted cannot be resynchronized. A refusal of a well-formed frame (tier
// never, a tier the device lacks, a field the rebuild rejects, a rate limit) is
// answered on that frame's id and the connection SURVIVES; a per-connection
// strike counter ends one that only ever probes. A malformed PAYLOAD on a
// well-formed envelope is likewise recoverable.
//
// Three pools bound what an unauthenticated peer can hold: a LAN pre-auth pool
// taken at accept (global and per source IP), a relay pre-auth pool, and an
// authed pool bounded per device, where the oldest connection of a device is
// the one evicted. Established phones are never displaced by strangers.
//
// Every non-read request leaves one audit line (audit.go) naming the device,
// tier, command and outcome, and never a payload.
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
//	        Devices: d.devices, // owned by the daemon; survives a listener rebuild
//	        Pairer:  d.pairer,
//	        Host:    host,
//	        Now:     time.Now,
//	        Logf:    d.remoteLogf,
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
