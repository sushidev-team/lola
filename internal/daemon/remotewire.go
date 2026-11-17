package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"slices"
	"strings"
	"sync"

	"github.com/sushidev-team/lola/internal/mdns"
	"github.com/sushidev-team/lola/internal/panebus"
	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/remote"
	"github.com/sushidev-team/lola/internal/sshstore"
)

// The remote phone listener's daemon-side wiring (docs/mobile/01-pairing-and-tiers.md §5).
//
// This file is the seam between three packages that deliberately do not know
// about each other. internal/remote owns the TLS socket, the frame codec and
// the authorization boundary, and declares a PaneBus interface rather than
// importing a pane layer. internal/panebus owns one tmux attach per pane and
// fans its bytes out, and declares a Resolve seam rather than importing a
// session store. The daemon is the only package that holds all three facts —
// the dispatcher, the pane bus and the session store — so it is where the
// adapter and the identity gate live.
//
// One thing is deliberately NOT here: nothing this file starts registers with
// connWg. That group's drain is unbounded and was built for bounded socket
// work, whereas a phone holds a pane stream open for hours. remote.Server owns
// its own WaitGroup and Close is bounded and synchronous, which is what lets
// shutdown take it down FIRST and only then observe an empty drain group.

// errUnknownPane is the identity gate's refusal. It says nothing about which
// panes exist: internal/remote answers every subscribe failure with the same
// code precisely so a refusal cannot be used to enumerate sessions, and an
// error that named the near misses would put the enumeration back.
var errUnknownPane = errors.New("no such pane")

// paneAuxSuffixRe strips an AUXILIARY session's suffix to name its parent.
//
// A pane name is a tmux session name, and lola's auxiliary surfaces are
// separate tmux sessions of their own: "<id>-shell-N" (the embedded shell
// tabs), "<id>-review" (a visible review pass) and "<id>-dev-N"
// (internal/devtab). The session store holds only the PARENT, so resolving one
// of these means asking about the parent — which is why this cannot simply be
// a store lookup.
//
// The vocabulary is runtime.IsAuxSession's, and that function stays the
// authority; it is re-expressed here rather than exported from there because
// what this needs is the parent, not a yes/no. Both directions of a mistake are
// bounded: a suffix this pattern does not know costs a refused pane, and a
// suffix it wrongly claims can still only ever name a session that must ALSO
// be in the store, so it never widens access past some live session's own
// namespace. An exact match on the full name is tried FIRST, so a real session
// whose id happens to end in "-review" resolves as itself.
var paneAuxSuffixRe = regexp.MustCompile(`-(?:shell-\d+|review|dev-\d+)$`)

// paneAuxParent returns the parent session's tmux name for an auxiliary pane,
// or "" when the name carries no auxiliary suffix.
func paneAuxParent(name string) string {
	loc := paneAuxSuffixRe.FindStringIndex(name)
	if loc == nil {
		return ""
	}
	return name[:loc[0]]
}

// resolvePaneName is panebus.Registry.Resolve: the IDENTITY gate, asked before
// any tmux process is spawned. panebus checks the SHAPE of a name (it owns the
// argv); only the daemon can say whether a well-formed name is a session that
// actually exists, because the session store is the authority and it lives
// here.
//
// It fails closed in every direction: an empty name, a name in no session's
// namespace, or a store that answers nothing all refuse. It asks only whether
// the pane belongs to a session lola knows about; the device's capability tier
// is checked in internal/remote before a subscribe or a pty frame reaches the
// bus, never here.
func (d *Daemon) resolvePaneName(_ context.Context, name string) error {
	if name == "" {
		return errUnknownPane
	}
	if d.paneNameKnown(name) {
		return nil
	}
	if parent := paneAuxParent(name); parent != "" && d.paneNameKnown(parent) {
		return nil
	}
	return fmt.Errorf("%w: %s", errUnknownPane, name)
}

// paneNameKnown reports whether a tmux session name is one lola's own session
// store records. It reads the snapshot rather than tmux: the store is the
// authority on what lola owns, and asking tmux would make the identity gate
// exec once per subscribe.
func (d *Daemon) paneNameKnown(name string) bool {
	for _, s := range d.sessions.Snapshot() {
		if paneTarget(s) == name {
			return true
		}
	}
	return false
}

// newPaneRegistry builds the production pane registry: real tmux attaches, real
// vtterm shadow emulators, and the daemon's log. Resolve is deliberately left
// for startRemote to install.
func (d *Daemon) newPaneRegistry() *panebus.Registry {
	r := panebus.NewRegistry(d.tmuxClient())
	r.Logf = d.remoteLogf
	return r
}

// remoteLogf carries internal/remote's and internal/panebus's lines into the
// daemon log with no poll prefix. Neither package ever hands it pane bytes or a
// resync frame, at any level.
func (d *Daemon) remoteLogf(format string, args ...any) {
	d.logf("", format, args...)
}

// startRemote brings the phone server up, if the configuration asks for one: a
// listener (RemoteConfig.Listens) or a relay-only server (a relay turned on
// and a hub joined; see RemoteConfig.Serves).
//
// ctx is the daemon's CANCELLABLE run context, the same posture as the
// interpreter and review workers and for the same reason: everything the
// listener does is read-only fan-out or a request already in flight, so
// aborting one costs a repaint rather than state. It must NOT be a socket
// command's context — a reload's ctx dies when its reply is written, which
// would tear the listener down moments after rebinding it. reloadRemote passes
// d.shutdownCtx for exactly that reason.
//
// A failure is logged and never fatal: a daemon that will not start because a
// port is taken is strictly worse than one that polls Linear without a phone
// attached.
func (d *Daemon) startRemote(ctx context.Context) {
	d.mu.Lock()
	rc := d.cfg.Remote
	hubCfg := d.cfg.Hub
	d.mu.Unlock()
	// Computed with no lock held: the joined check reads the hub identity from
	// disk.
	serves := rc.Serves(d.hubRelayJoined(hubCfg))
	d.remoteServes.Store(serves)
	if !serves {
		return
	}

	if ctx.Err() != nil {
		// The daemon is already shutting down. Checked here rather than left to
		// the listener's own cancel watcher because a reload racing shutdown
		// would otherwise bind a socket AFTER stopRemote had run, and nothing
		// would ever wait for it: handleReload runs on a socket goroutine that
		// is not in any drain group.
		return
	}

	// Registered before remoteMu is taken, so it runs after the unlock (defers
	// are last in, first out) and the relay loop never sees the lock held.
	defer d.hubRelayNudge()

	d.remoteMu.Lock()
	defer d.remoteMu.Unlock()
	if d.remote != nil {
		return // already serving; reloadRemote stops before it starts
	}

	if err := d.devices.Unsaved(); err != nil {
		// A revocation that lives only in memory must not be forgotten by a
		// restart that would accept the revoked phone again, so nothing is
		// served until a later save succeeds. Kept as remoteErr so pairBegin
		// reports the real reason; reconcileRemoteBind retries.
		d.logf("", "remote: not serving: %v", err)
		d.remoteErr = err.Error()
		d.remoteUnsaved = true
		return
	}
	d.remoteUnsaved = false

	build := d.paneRegistry
	if build == nil {
		build = d.newPaneRegistry
	}
	reg := build()
	// The identity gate is installed HERE and never by the seam above: the
	// session store is the only authority on which panes exist, and a registry
	// that arrived with its own Resolve would answer that question somewhere
	// this daemon cannot see.
	reg.Resolve = d.resolvePaneName

	host, _ := os.Hostname()
	opts := remote.Options{
		Bind:      rc.BindMode(),
		Port:      rc.ListenPort(),
		Dir:       d.home,
		Handle:    d.handleRemote,
		Panes:     remotePanes{reg: reg, logf: d.remoteLogf},
		Devices:   d.devices,
		Pairer:    d.pairer,
		Host:      host,
		Logf:      d.remoteLogf,
		RelayOnly: !rc.Listens(),
	}
	if d.watch != nil {
		// Assigned only when set: a nil *watchHub in the interface would not be
		// nil, and the server answers a nil Events with "unavailable".
		opts.Events = d.watch
	}
	srv, err := d.listenRemote(ctx, opts)
	if err != nil {
		// The refusal itself was logged where it was decided (an unsaved
		// registry, an unbindable address); this line says the daemon is
		// carrying on without a listener, which is the part an operator
		// reading the log for "why can't my phone connect" needs.
		d.logf("", "remote: not listening: %v", err)
		// KEPT, so cmd=pairBegin reports the real reason instead of sending a
		// human to the log. Safe to put in front of an operator:
		// internal/remote is careful that no key ever reaches an error.
		d.remoteErr = err.Error()
		_ = reg.Close()
		return
	}
	d.remote = srv
	d.remoteErr = ""
	d.panes = reg

	if len(srv.Addrs()) == 0 {
		// Relay-only: there is no socket to name and no port to advertise.
		d.logf("", "remote: relay-only server up (SPKI pin %s)", srv.SPKIPin())
		return
	}
	var where []string
	for _, ba := range srv.Addrs() {
		where = append(where, ba.Addr)
	}
	d.logf("", "remote: phone listener up on %s (SPKI pin %s)", strings.Join(where, ", "), srv.SPKIPin())
	d.startAdvertiserLocked(srv)
}

// startAdvertiserLocked publishes the listener on the local network, when the
// operator has asked for it.
//
// WHAT IT BUYS. The pinned daemon identity and the phone's own key identify
// the pair, not an address, so a paired phone already has everything it needs
// on any network — except where to dial. The pairing code carries the
// addresses this machine had at pairing time, which is why a phone paired at
// home finds nothing at the office. A phone that can BROWSE finds the daemon
// wherever it is, with the same credentials and no re-pairing.
//
// OFF UNLESS ASKED FOR ([remote].advertise), because it is a DISCLOSURE.
// `_lola._tcp` announces "this machine runs autonomous coding agents and
// accepts remote control" to every peer on the network, which is a thing to
// opt into on purpose rather than a convenience to switch on for everyone.
//
// WHAT IT PUBLISHES: the port, and a protocol version. Nothing else — no pin,
// no hostname, no session names. The instance name is not derived from anything
// identifying either. See internal/mdns's header: an SPKI pin or a hostname in
// a TXT record is a stable cross-network correlator for one laptop, which is
// exactly what a phone roaming between networks must not broadcast.
//
// DISCOVERY IS A HINT, NOT AN AUTHORITY. Anyone on the network can advertise
// this service type; the pinned TLS handshake is what decides trust, exactly as
// it does for a stored address. So this failing costs nothing that matters, and
// the advertiser is built to fail quietly (no dns-sd, a refused registration)
// rather than take a working listener down with it.
//
// Caller holds remoteMu; the advertiser's lifetime is the listener's, and
// stopRemote takes it down with the same lock held.
func (d *Daemon) startAdvertiserLocked(srv *remote.Server) {
	d.mu.Lock()
	on := d.cfg.Remote.Advertise
	d.mu.Unlock()
	if !on {
		return
	}
	if d.advertiser == nil {
		d.advertiser = mdns.New("", d.mdnsStart, func(f string, v ...any) { d.logf("", f, v...) })
	}
	if err := d.advertiser.Start(mdns.Service{
		Instance: mdns.DefaultInstance,
		Port:     srv.Port(),
		TXT:      map[string]string{mdns.TXTVersion: mdns.Version},
	}); err != nil {
		d.logf("", "remote: not advertising on the local network: %v", err)
		return
	}
	d.logf("", "remote: advertising %s on the local network", mdns.ServiceType)
}

// stopAdvertiserLocked withdraws the registration. Caller holds remoteMu.
func (d *Daemon) stopAdvertiserLocked() {
	if d.advertiser == nil {
		return
	}
	d.advertiser.Stop()
}

// reconcileRemoteBind rebinds the listener when the machine's addresses have
// moved out from under it.
//
// A laptop changes networks, and the listener does not notice: it holds sockets
// on addresses that no longer exist, nothing errors, nothing logs, and the
// phone that connected yesterday cannot find the daemon today. `lola reload`
// does not fix it either — handleReload only calls reloadRemote when [remote]
// itself changed, and nothing about the config changed. The daemon has to
// observe the drift, because it is the only thing that can.
//
// Called once per observe cycle. The check is one interface enumeration and a
// set comparison, and it does nothing at all for the two common binds:
// "localhost" resolves to the same loopback forever, and a wildcard never
// drifts. Only "lan" moves, which is exactly the mode a phone needs.
//
// It reuses reloadRemote, so the rebind is the same stop-then-start every other
// caller gets, on the shutdown context rather than an observe cycle's.
func (d *Daemon) reconcileRemoteBind() {
	d.remoteMu.Lock()
	srv := d.remote
	held := d.remoteUnsaved && d.devices.Unsaved() == nil
	if held {
		d.remoteUnsaved = false
	}
	d.remoteMu.Unlock()
	if srv == nil {
		// A server held off by an unsaved revocation comes back once a later
		// save succeeded.
		if held {
			d.logf("", "remote: the device registry saved again; starting the phone server")
			d.reloadRemote()
		}
		return
	}
	if !srv.BindDrifted() {
		return
	}

	// Say what happened before doing it: a rebind drops every live connection,
	// and an operator watching a phone disconnect deserves the reason in the
	// log rather than a mystery.
	d.logf("", "remote: the addresses this machine listens on have changed; rebinding the phone listener")
	d.reloadRemote()
}

// stopRemote takes the listener down and is idempotent.
//
// The ORDER inside is the invariant. remote.Server.Close closes its listeners,
// then every live connection — which closes every pane subscription — and only
// then waits for its own goroutines. The registry is closed AFTER that, so no
// subscriber is ever reading a bus that has already been torn out from under
// it; closing the registry first would race every pump against a vanishing
// pane.
//
// It is called BEFORE stopAllWorkers and before the unbounded drainConnWork,
// because a phone holding a pane stream open is the opposite of the bounded
// socket work that group was built for: waiting first would hang until the
// phone happened to disconnect.
func (d *Daemon) stopRemote() {
	d.remoteMu.Lock()
	srv, reg := d.remote, d.panes
	d.remote, d.panes = nil, nil
	// Withdrawn BEFORE the listener closes: an advertisement outliving its
	// socket points every browsing phone at a port that refuses.
	d.stopAdvertiserLocked()
	d.remoteMu.Unlock()

	if srv != nil {
		_ = srv.Close()
	}
	if reg != nil {
		_ = reg.Close()
	}
	// A relay that was serving through this server must stop accepting.
	d.hubRelayNudge()
}

// reloadRemote applies a changed [remote] table by tearing the listener down
// and building a new one.
//
// A REBIND rather than an in-place mutation, and that is a decision rather than
// an accident. Every value in the table is consumed at bind time: the interface
// set is resolved once, the port is in the socket, and the enabled flag decides
// whether there is a socket at all — internal/remote offers nothing to change
// afterwards, and inventing one would mean a listener whose live connections
// were admitted under a configuration that no longer exists. That is precisely
// the state an operator disabling [remote], or narrowing bind from "lan" to
// "localhost", is trying to end. So the connections go with it.
//
// The cost is one reconnect for an attached phone, and it is paid only when the
// table ACTUALLY changed: config.RemoteConfig is comparable and handleReload
// compares it exactly, so an unrelated reload costs nothing at all.
//
// It runs AFTER handleReload releases d.mu. Binding a socket and loading the
// device identity are I/O, and doing them under the lock every tick and every
// socket command takes would stall the daemon for the length of a TLS
// listener's teardown.
func (d *Daemon) reloadRemote() {
	d.stopRemote()
	ctx := d.shutdownCtx
	if ctx == nil {
		// Reachable only from a hand-built Daemon (a test) that never ran Run.
		// Background is the honest answer: there is no shutdown to cancel on.
		ctx = context.Background()
	}
	d.startRemote(ctx)
}

// remotePanes adapts *panebus.Registry to remote.PaneBus.
//
// It exists because Go channel types are INVARIANT: panebus.Sub hands out a
// <-chan panebus.Frame and remote wants a <-chan remote.PaneFrame, and no
// amount of structural similarity makes one satisfy the other. One of the two
// packages would otherwise have to import the other's frame type, and neither
// should — remote must be testable without a tmux attach, and panebus must be
// usable by something that is not a TLS listener.
//
// Write and Scroll match method for method and pass straight through. Only
// Subscribe adapts, and it drops the advisory cols/rows: the bus attaches once
// per pane at the tmux WINDOW size and fans the full untruncated stream out, so
// a phone pans client-side rather than reflowing the developer's window. They
// are recorded by the connection layer, which is the only place they mean
// anything.
type remotePanes struct {
	reg  *panebus.Registry
	logf func(string, ...any)
}

func (p remotePanes) Subscribe(ctx context.Context, pane string, _, _ int) (remote.PaneStream, error) {
	sub, err := p.reg.Subscribe(ctx, pane)
	if err != nil {
		return nil, err
	}
	return newPaneStream(sub, p.logf), nil
}

func (p remotePanes) Write(pane string, b []byte) error { return p.reg.Write(pane, b) }

func (p remotePanes) Scroll(ctx context.Context, pane string, lines int) error {
	return p.reg.Scroll(ctx, pane, lines)
}

// paneStream re-types one panebus.Sub's frames as remote.PaneFrames.
//
// The output channel is UNBUFFERED on purpose. panebus.Sub's own queue is the
// one that knows how to recover from a slow consumer — it drops the frame,
// marks the subscription desynced and has the bus repair it with a fresh resync
// — so a buffer here would only create a second queue with none of that
// machinery, and pressure would build in the half that cannot repair itself.
// Unbuffered puts every drop back where the repair lives, and the cost is one
// goroutine handoff per frame on a stream already coalesced to one animation
// frame.
type paneStream struct {
	sub  *panebus.Sub
	ch   chan remote.PaneFrame
	done chan struct{}
	logf func(string, ...any)

	// closeOnce makes Close idempotent on its own rather than by grace of its
	// caller. The one caller today wraps it in a sync.Once of its own, so a
	// check-then-close on done was safe in practice — but the doc comment
	// promised idempotence unconditionally, and two concurrent Closes would
	// both have taken the default branch and panicked on close of a closed
	// channel. Every other one-shot in this codebase is a sync.Once; so is this.
	closeOnce sync.Once
}

func newPaneStream(sub *panebus.Sub, logf func(string, ...any)) *paneStream {
	s := &paneStream{
		sub:  sub,
		ch:   make(chan remote.PaneFrame),
		done: make(chan struct{}),
		logf: logf,
	}
	go s.pump()
	return s
}

func (s *paneStream) log(format string, args ...any) {
	if s.logf != nil {
		s.logf(format, args...)
	}
}

// pump copies until the source closes or Close is called. The send SELECTS on
// done, because the consumer is allowed to stop reading — remote's own pump
// returns the moment its connection is torn down — and a copier blocked on a
// send nobody will take would never close ch, which is the signal that
// consumer waits on.
func (s *paneStream) pump() {
	defer close(s.ch)
	// Panic-guarded like every other long-lived goroutine this change adds
	// (bus.guard, Server.acceptLoop, serveConn, conn.paneLoop, conn.pump): a
	// bug in the frame mapping below must cost one pane stream, never the
	// daemon. The deferred close above still runs, so the consumer sees the
	// stream end rather than hanging.
	defer func() {
		if r := recover(); r != nil {
			s.log("remote: pane stream %s panicked: %v", s.sub.Pane(), r)
		}
	}()
	for f := range s.sub.Frames() {
		rf, ok := toRemoteFrame(f)
		if !ok {
			// A kind neither package's constants cover. Dropped rather than
			// mapped onto the zero value, which is a resync: rendering raw
			// bytes as a screen (or the reverse) is worse than a missing
			// frame, and the Seq gap it leaves is exactly what tells the
			// client to re-subscribe.
			continue
		}
		select {
		case s.ch <- rf:
		case <-s.done:
			return
		}
	}
}

func (s *paneStream) Frames() <-chan remote.PaneFrame { return s.ch }

// Close releases the subscription. It is idempotent and never blocks on a
// reader: done is closed first, so a pump parked on a send it will never
// complete is released before the panebus side is asked to tear anything down.
func (s *paneStream) Close() error {
	s.closeOnce.Do(func() { close(s.done) })
	return s.sub.Close()
}

// toRemoteFrame maps one panebus.Frame onto the wire-facing shape.
//
// The switch is written out rather than relying on the two kind enums having
// the same numeric order, because they are declared in packages that must not
// import each other and nothing but a test can hold them together. That test is
// TestPaneFrameKindsMapAcrossThePackageBoundary; if a constant is reordered in
// either package it fails there instead of silently rendering keystrokes as a
// screen.
func toRemoteFrame(f panebus.Frame) (remote.PaneFrame, bool) {
	var kind remote.PaneKind
	switch f.Kind {
	case panebus.KindResync:
		kind = remote.PaneResync
	case panebus.KindOutput:
		kind = remote.PaneOutput
	case panebus.KindExit:
		kind = remote.PaneExit
	default:
		return remote.PaneFrame{}, false
	}
	return remote.PaneFrame{
		Kind:   kind,
		Data:   f.Data,
		Screen: toRemoteScreen(f.Screen),
		Seq:    f.Seq,
	}, true
}

// toRemoteScreen re-types a coherent screen reading. Lines are shared, not
// copied: panebus hands out an immutable rendering and remote only marshals it.
func toRemoteScreen(sc *panebus.Screen) *remote.PaneScreen {
	if sc == nil {
		return nil
	}
	return &remote.PaneScreen{
		Cols:          sc.W,
		Rows:          sc.H,
		Lines:         sc.Lines,
		CursorX:       sc.CursorX,
		CursorY:       sc.CursorY,
		CursorVisible: sc.CursorVisible,
		AltScreen:     sc.AltScreen,
	}
}

// handleRemote is the daemon's request handler as a paired device sees it.
//
// internal/remote has already tiered, field-rebuilt and rate-limited the
// request; this is defence in depth behind that gate. A local-only command or
// a command with no tier reaching it means the gate was bypassed, which is a
// bug in internal/remote, so it is logged rather than answered quietly.
func (d *Daemon) handleRemote(ctx context.Context, req protocol.Request) protocol.Response {
	if slices.Contains(protocol.LocalOnlyCommands, req.Cmd) || remote.TierFor(req.Cmd) == protocol.TierNever {
		d.logf("", "remote: handleRemote refused %s (tier gate bypassed?)", logSafeCmd(req.Cmd))
		return protocol.Response{OK: false, Error: "not available remotely"}
	}
	if req.Cmd == protocol.CmdSSHAnswer && !d.remoteMayAnswer(req) {
		return protocol.Response{OK: false, Error: errSSHAnswerOnMac.Error()}
	}
	ctx = context.WithValue(ctx, remoteCallKey{}, true)
	if req.Cmd != "sessions" {
		return d.handle(ctx, req)
	}
	// A session's ssh glance carries the exact command waiting for approval and
	// the agent's latest summary, and every ssh command is denied remotely, so
	// a phone could read those details without being able to act on them. It
	// gets the session list without them.
	data := d.sessionsData()
	for i := range data.Sessions {
		data.Sessions[i] = remoteSessionInfo(data.Sessions[i])
	}
	return dataResponse(data)
}

// logSafeCmd bounds and flattens a command name taken from the wire before it
// reaches a log line.
func logSafeCmd(cmd string) string {
	if len(cmd) > 40 {
		cmd = cmd[:40]
	}
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return '?'
		}
		return r
	}, cmd)
}

// errSSHAnswerOnMac refuses a phone's answer to a question scoped to an SSH
// connection: that is an SSH human act and stays on the desktop.
var errSSHAnswerOnMac = errors.New("this question is about an SSH host; answer it on the Mac")

// remoteCallKey marks a context whose request came through handleRemote, so a
// handler can apply a phone-only rule inside its own critical section.
type remoteCallKey struct{}

// isRemoteCall reports whether ctx carries a request from a paired device.
func isRemoteCall(ctx context.Context) bool {
	v, _ := ctx.Value(remoteCallKey{}).(bool)
	return v
}

// remoteMayAnswer is the early refusal of a phone's sshAnswer: only an element
// that exists, is at the revision the phone read, and is not scoped to an SSH
// connection; anything unreadable (no store, unknown element, bad args) answers
// false. It is a courtesy, not the gate: an agent update may land between this
// read and the handler's, so handleSSHAnswer re-checks the scope (isRemoteCall)
// in the same store.Update that pins the revision.
func (d *Daemon) remoteMayAnswer(req protocol.Request) bool {
	var a protocol.SSHAnswerArgs
	if err := json.Unmarshal(req.Args, &a); err != nil || a.Session == "" || a.Element == "" {
		return false
	}
	store, err := d.sshStore(a.Session, false)
	if err != nil || store == nil {
		return false
	}
	ok := false
	store.View(func(st *sshstore.State) {
		it := st.Item(a.Element)
		ok = it != nil && it.Element != nil && it.Element.Conn == "" && it.Element.Revision == a.Revision
	})
	return ok
}

// devicesChanged is the registry subscriber: the rendezvous set a relay
// registers with the hub derives from the registry, so any change nudges it.
func (d *Daemon) devicesChanged() { d.hubRelayDirty() }

// dropRemoteDevice closes every live connection of a device (a revoke, a tier
// change) and reports how many it closed. It must be called after the registry
// change, so a reconnect cannot succeed in between.
//
// When the registry could not persist a revocation, the server is stopped as
// well: a restart would forget the revocation and accept the phone again, so
// nothing is served until a later save succeeds (reconcileRemoteBind brings it
// back).
func (d *Daemon) dropRemoteDevice(id string) int {
	d.remoteMu.Lock()
	srv := d.remote
	d.remoteMu.Unlock()
	n := 0
	if srv != nil {
		n = srv.DropDevice(id)
	}
	if err := d.devices.Unsaved(); err != nil {
		d.logf("", "remote: stopping the phone server: %v", err)
		d.stopRemote()
		d.remoteMu.Lock()
		d.remoteErr = err.Error()
		d.remoteUnsaved = true
		d.remoteMu.Unlock()
	}
	return n
}
