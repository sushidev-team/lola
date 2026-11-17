package daemon

// Bringing a connection up. The human authenticates against real ssh in a tmux tab, in
// that terminal or through a prompt card. `ssh -O check` exiting 0 is the only thing that
// proves a connection, and "authenticating" is a label from elapsed time. The pane is read
// during this stage for one purpose, to show its question on a card (sshprompt.go), and
// what it says never moves a state. A dropped connection never reconnects by itself,
// because reconnecting may need a human's MFA.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/notify"
	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/sshconn"
	"github.com/sushidev-team/lola/internal/sshstore"
	"github.com/sushidev-team/lola/internal/sshtab"
)

func sshNewConnID() (string, error) { return sshconn.NewConnID() }

// sshConnect moves a connection to connecting and opens its terminal. It is the only
// way into connecting, and only a human's go reaches it. The claim comes first, so
// two clicks cannot open two tabs. What fails after that is the connection's state,
// shown to the human, and an error only when lola itself could not try.
func (d *Daemon) sshConnect(ctx context.Context, store *sshstore.Store, connID string, h config.SSHHost) (protocol.SSHConnInfo, error) {
	now := d.ssh.now()
	wait := time.Duration(h.EffectiveConnectWait()) * time.Second
	claimed := false
	err := store.Update(func(st *sshstore.State) error {
		c := st.Conn(connID)
		if c == nil {
			return fmt.Errorf("unknown connection %s", connID)
		}
		switch c.State {
		case protocol.SSHConnConnecting, protocol.SSHConnAuthenticating, protocol.SSHConnConnected:
			return nil // the go already landed
		case protocol.SSHConnClosed, protocol.SSHConnDenied:
			return fmt.Errorf("connection %s is %s. Start a new one", connID, c.State)
		}
		c.State, c.StateDetail = protocol.SSHConnConnecting, ""
		c.ConnectingSince, c.ConnectDeadline = now, now.Add(wait)
		c.AuthNotified = false
		st.AddEvent(protocol.SSHSystemEvent{Kind: protocol.SSHEventGo, Text: "You gave the go. Opening a terminal.", Conn: connID}, now)
		claimed = true
		return nil
	})
	if err != nil {
		return protocol.SSHConnInfo{}, err
	}
	if !claimed {
		return sshConnInfo(store, connID), nil
	}
	fail := func(detail string, cause error) (protocol.SSHConnInfo, error) {
		d.sshMarkFailed(store, connID, detail)
		return sshConnInfo(store, connID), cause
	}
	if _, err := d.ssh.lookPath(d.ssh.client.Binary()); err != nil {
		detail := "ssh was not found on this Mac. Lola needs it to connect."
		return fail(detail, errors.New(detail))
	}
	tm := d.sshTmuxClient()
	if tm == nil || !tm.Available() {
		detail := "tmux is not available, so there is no terminal to sign in from."
		return fail(detail, errors.New(detail))
	}
	sock, err := sshconn.SocketPath(d.home, connID)
	if err != nil {
		detail := "lola could not make a safe control socket: " + err.Error()
		return fail(detail, errors.New(detail))
	}
	c, _ := sshConnCopy(store, connID)
	target := sshconn.Target{Sock: sock, Alias: h.Alias, User: h.User, Port: h.Port, IdentityFile: h.IdentityFile}
	if err := target.Valid(); err != nil {
		return fail(err.Error(), err)
	}
	// Another tab is already signed in to this destination, so this one uses its login. It
	// gets its own shell, folder and timeline, and asks nobody for a password.
	if shared := d.sshSharedMaster(ctx, store, connID, target); shared != "" {
		sock, target.Sock = shared, shared
	}

	// What this alias means to the local ssh config. It never connects, and a
	// failure blocks nothing.
	resolved, rerr := d.ssh.client.ResolveTarget(ctx, target)
	if rerr != nil {
		d.logf("", "ssh: %s: ssh -G: %v", connID, rerr)
	}

	// A master that outlived its record, or a reconnect that found one, needs no
	// second authentication. Its terminal is an ordinary mux client.
	args := sshconn.MasterArgs(target, h.EffectiveControlPersist())
	if d.ssh.client.Check(ctx, target) == nil {
		args = sshconn.ReopenArgs(target)
	}
	args = d.sshShellArgs(store, connID, h, tm, args)
	tab, err := d.sshOpenTab(ctx, tm, c, args)
	if err != nil {
		return fail("lola could not open a terminal: "+err.Error(), err)
	}
	err = store.Update(func(st *sshstore.State) error {
		p := st.Conn(connID)
		if p == nil {
			return nil
		}
		p.Sock, p.Tab, p.TabAlive, p.TabN = sock, tab.name, true, tab.n
		// What this master was dialed with, which a config edit since the request
		// may have changed. Every later exec and every block's user reads the record.
		p.Alias, p.User = target.Alias, target.User
		p.Port, p.IdentityFile = target.Port, target.IdentityFile
		p.ResolvedUser, p.ResolvedHostname = resolved.User, resolved.Hostname
		st.TouchConn()
		return nil
	})
	if err != nil {
		d.logf("", "ssh: record %s: %v", connID, err)
	}
	d.sshEnsureTap(store, connID)
	d.sshStartMonitor(store, connID)
	return sshConnInfo(store, connID), nil
}

// sshSharedMaster is the socket of a running master this connection can join, "" when
// there is none. ssh has the last word: a record is not proof that a master answers.
func (d *Daemon) sshSharedMaster(ctx context.Context, store *sshstore.Store, connID string, t sshconn.Target) string {
	var found sshconn.Target
	store.View(func(st *sshstore.State) {
		if o := st.MasterFor(connID, t.Alias, t.User, t.Port, t.IdentityFile); o != nil {
			found = sshTarget(*o)
		}
	})
	if found.Sock == "" || d.ssh.client.Check(ctx, found) != nil {
		return ""
	}
	return found.Sock
}

type sshTab struct {
	name string
	n    int
}

// sshOpenTab starts a connection's one terminal. A dead tab's error stays readable
// until the human asks for the next one, and only then is it replaced, so dead tabs
// do not pile up.
//
// The pause keeps the option race closed: remain-on-exit is set after tmux has
// forked the pane, and ssh failing at once would otherwise take the session and its
// error message with it, leaving "it failed" with nothing to say why.
func (d *Daemon) sshOpenTab(ctx context.Context, tm sshTmux, c sshstore.Conn, args []string) (sshTab, error) {
	ectx, cancel := context.WithTimeout(ctx, sshTabExec)
	defer cancel()
	if c.Tab != "" && tm.Has(ectx, c.Tab) {
		_ = tm.KillSessionTree(ectx, c.Tab)
	}
	n := c.TabN
	var name string
	for {
		n++
		name = sshtab.Name(c.Session, n)
		if !tm.Has(ectx, name) {
			break
		}
	}
	line := "exec sh -c " + sshconn.ShQuote("sleep 0.2; exec "+sshconn.ShellLine(d.ssh.client.Binary(), args))
	if err := tm.NewSession(ectx, name, d.home, line); err != nil {
		return sshTab{}, err
	}
	if err := tm.KeepDeadPane(ectx, name); err != nil {
		d.logf("", "ssh: %s could not keep the dead pane of %s: %v", c.Session, name, err)
	}
	return sshTab{name, n}, nil
}

func (d *Daemon) sshMarkFailed(store *sshstore.Store, connID, detail string) {
	d.ssh.prompts.forget(connID)
	now := d.ssh.now()
	err := store.Update(func(st *sshstore.State) error {
		c := st.Conn(connID)
		if c == nil || c.Terminal() || c.State == protocol.SSHConnFailed {
			return nil // a cancel that raced the monitor keeps its own words
		}
		c.State, c.StateDetail = protocol.SSHConnFailed, detail
		c.ConnectDeadline = time.Time{}
		st.AddEvent(protocol.SSHSystemEvent{Kind: protocol.SSHEventFailed, Text: detail, Conn: connID}, now)
		return nil
	})
	if err != nil {
		d.logf("", "ssh: record failure of %s: %v", connID, err)
	}
}

// sshMarkConnected records a master that answered, from any live state.
func (d *Daemon) sshMarkConnected(store *sshstore.Store, connID string) {
	d.sshNoteSignInEcho(store, connID)
	d.ssh.prompts.forgetStage(connID, protocol.SSHPromptSignIn)
	now := d.ssh.now()
	var session, host string
	changed := false
	err := store.Update(func(st *sshstore.State) error {
		c := st.Conn(connID)
		if c == nil || c.Terminal() || c.State == protocol.SSHConnConnected {
			return nil
		}
		c.State, c.StateDetail = protocol.SSHConnConnected, ""
		c.ConnectDeadline, c.ConnectedAt, c.LastUsed = time.Time{}, now, now
		st.AddEvent(protocol.SSHSystemEvent{Kind: protocol.SSHEventConnected, Text: "Connected to " + sshDisplay(c) + ".", Conn: connID}, now)
		session, host, changed = st.Session, c.Host, true
		return nil
	})
	if err != nil {
		d.logf("", "ssh: record connection %s: %v", connID, err)
	}
	if changed {
		d.logf("", "ssh: %s connected (%s)", connID, session)
		d.sshNudgeIfGone(store, session, "[lola] The connection to "+host+" is up. Go on with: lola ssh run "+host+" --why ... -- ...")
		d.sshDispatch(store, connID)
	}
}

// sshMarkDisconnected records a master that stopped answering, as well as lola can
// say why. It tells a human only when something was running or waiting: an idle
// timeout overnight must not sit as an alarm.
func (d *Daemon) sshMarkDisconnected(store *sshstore.Store, connID string) {
	d.ssh.prompts.forget(connID)
	now := d.ssh.now()
	var busy int
	var label string
	var refuse []string
	err := store.Update(func(st *sshstore.State) error {
		c := st.Conn(connID)
		if c == nil || c.Terminal() {
			return nil
		}
		info := st.ConnInfo(c)
		queued := 0
		for _, it := range st.Items {
			if it.Kind == protocol.SSHItemBlock && it.Block.Conn == connID && it.Block.Status == protocol.SSHBlockQueued {
				queued++
				refuse = append(refuse, it.Block.ID)
			}
		}
		busy = info.RunningBlocks + info.PendingApprovals + queued
		text := "Connection lost."
		idle := time.Duration(c.IdleLingerSeconds) * time.Second
		if busy == 0 && !c.TabAlive && idle > 0 && !c.LastUsed.IsZero() && now.Sub(c.LastUsed) >= idle {
			text = fmt.Sprintf("Connection closed after %s idle.", sshDuration(idle))
		}
		c.State, c.StateDetail = protocol.SSHConnDisconnected, text
		label = sshDisplay(c)
		st.AddEvent(protocol.SSHSystemEvent{Kind: protocol.SSHEventDisconnected, Text: text, Conn: connID}, now)
		return nil
	})
	if err != nil {
		d.logf("", "ssh: record disconnect of %s: %v", connID, err)
	}
	if busy > 0 {
		d.sshNotify(notify.Action, "Connection lost: "+label, fmt.Sprintf("%d commands were running or waiting.", busy))
	}
	if len(refuse) > 0 {
		d.sshDispatch(store, connID)
	}
}

// sshIdleExpired reports a connection with no shell, nothing running or waiting, and no
// use for longer than its linger.
func (d *Daemon) sshIdleExpired(store *sshstore.Store, connID string, now time.Time) bool {
	expired := false
	store.View(func(st *sshstore.State) {
		c := st.Conn(connID)
		if c == nil || c.LastUsed.IsZero() || c.IdleLingerSeconds <= 0 {
			return
		}
		idle := func(o *sshstore.Conn) bool {
			info := st.ConnInfo(o)
			return info.RunningBlocks == 0 && info.PendingApprovals == 0 && !o.LastUsed.IsZero() &&
				now.Sub(o.LastUsed) >= time.Duration(c.IdleLingerSeconds)*time.Second
		}
		if !idle(c) {
			return
		}
		// A master two tabs share closes when both are idle, or it would drop the other.
		for _, o := range st.LiveConns() {
			if o.ID != c.ID && o.Sock == c.Sock && !idle(o) {
				return
			}
		}
		expired = true
	})
	return expired
}

// sshTabFacts reports whether a tab's pane process runs. known is false when tmux
// could not answer, and then nothing may change: a false "dead" would mark a
// connection failed that is only slow.
func (d *Daemon) sshTabFacts(ctx context.Context, name string) (alive, known bool) {
	if name == "" {
		return false, true
	}
	tm := d.sshTmuxClient()
	if tm == nil {
		return false, false
	}
	cctx, cancel := context.WithTimeout(ctx, sshTabExec)
	defer cancel()
	live, err := tm.ListSessions(cctx)
	if err != nil {
		return false, false
	}
	found := false
	for _, s := range live {
		if s.Name == name {
			found = true
			break
		}
	}
	if !found {
		return false, true
	}
	dead, err := tm.DeadPanes(cctx)
	if err != nil {
		return false, false
	}
	return !dead[name], true
}

// sshStartMonitor starts a connection's watcher, unless one runs. It lives on its own
// goroutine with per-exec deadlines, never on the observer.
func (d *Daemon) sshStartMonitor(store *sshstore.Store, connID string) {
	m := d.ssh
	m.mu.Lock()
	if kick, ok := m.monitors[connID]; ok {
		m.mu.Unlock()
		select {
		case kick <- struct{}{}:
		default:
		}
		return
	}
	kick := make(chan struct{}, 1)
	m.monitors[connID] = kick
	m.mu.Unlock()
	started := d.sshSpawn("monitor "+connID, func() { d.sshMonitor(store, connID, kick) })
	if !started {
		m.mu.Lock()
		delete(m.monitors, connID)
		m.mu.Unlock()
	}
}

func (d *Daemon) sshMonitor(store *sshstore.Store, connID string, kick chan struct{}) {
	m := d.ssh
	// A panic must not leave a dead entry that blocks every later monitor.
	defer func() {
		m.mu.Lock()
		if m.monitors[connID] == kick {
			delete(m.monitors, connID)
		}
		m.mu.Unlock()
	}()
	undetermined := 0
	for {
		wait, done := d.sshMonitorOnce(store, connID, &undetermined)
		if done {
			// Re-read under the monitors lock, so a go that landed since the pass
			// above is not left without a watcher.
			m.mu.Lock()
			c, ok := sshConnCopy(store, connID)
			if !ok || sshMonitorDone(c) {
				delete(m.monitors, connID)
				m.mu.Unlock()
				return
			}
			m.mu.Unlock()
			wait = 0
		}
		select {
		case <-m.ctx.Done():
			return
		case <-kick:
		case <-time.After(wait):
		}
	}
}

// sshMonitorDone reports whether there is nothing left to watch. A failed or dropped
// connection whose tab still lives is watched, because the human may yet finish
// signing in there, and then -O check is what says so.
func sshMonitorDone(c sshstore.Conn) bool {
	switch c.State {
	case protocol.SSHConnRequested, protocol.SSHConnDenied, protocol.SSHConnClosed:
		return true
	case protocol.SSHConnFailed, protocol.SSHConnDisconnected:
		return !c.TabAlive
	}
	return false
}

func (d *Daemon) sshMonitorOnce(store *sshstore.Store, connID string, undetermined *int) (wait time.Duration, done bool) {
	m := d.ssh
	c, ok := sshConnCopy(store, connID)
	if !ok || sshMonitorDone(c) {
		return 0, true
	}
	alive, known := d.sshTabFacts(m.ctx, c.Tab)
	if known && alive != c.TabAlive {
		c.TabAlive = alive
		_ = store.Update(func(st *sshstore.State) error {
			if p := st.Conn(connID); p != nil {
				p.TabAlive = alive
				if !alive {
					p.LastUsed = m.now()
				}
				st.TouchConn()
			}
			return nil
		})
	}
	if known && !alive {
		d.sshStopTap(connID)
	} else if known {
		d.sshEnsureTap(store, connID) // after a restart, or a tab replaced
	}
	now := m.now()
	switch c.State {
	case protocol.SSHConnConnecting, protocol.SSHConnAuthenticating:
		switch err := m.client.Check(m.ctx, sshTarget(c)); {
		case err == nil:
			d.sshMarkConnected(store, connID)
			return m.connectedTick, false
		case known && !alive:
			d.sshMarkFailed(store, connID, "ssh exited before connecting. Its message is in the terminal.")
			return m.connectedTick, false
		}
		if !c.ConnectDeadline.IsZero() && now.After(c.ConnectDeadline) {
			wait := c.ConnectDeadline.Sub(c.ConnectingSince)
			d.sshMarkFailed(store, connID, "Nobody finished signing in within "+sshDuration(wait)+".")
			return m.connectedTick, false
		}
		if c.State == protocol.SSHConnConnecting && now.Sub(c.ConnectingSince) >= sshAuthAfter {
			d.sshMarkAuthenticating(store, connID)
		}
		d.sshReadSignInPrompt(connID, c.Tab, known && alive)
		return m.connectTick, false
	case protocol.SSHConnConnected:
		switch err := m.client.Check(m.ctx, sshTarget(c)); {
		case err == nil:
			*undetermined = 0
			if alive {
				_ = store.Update(func(st *sshstore.State) error {
					if p := st.Conn(connID); p != nil {
						p.LastUsed = now
					}
					return nil
				})
			} else if known && d.sshIdleExpired(store, connID, now) {
				// This very check resets ssh's own ControlPersist timer, measured against
				// a real master: left to ssh, a monitored connection would never close.
				// So lola enforces the promise itself, and ControlPersist stays as the
				// backstop for a daemon that is gone.
				cctx, cancel := context.WithTimeout(m.ctx, sshconn.ControlTimeout)
				_ = m.client.Exit(cctx, sshTarget(c))
				cancel()
				d.sshMarkDisconnected(store, connID)
			}
		case errors.Is(err, sshconn.ErrUndetermined):
			if m.ctx.Err() != nil {
				return m.connectedTick, false
			}
			// A slow answer is not an answer. Give it a few before believing it.
			*undetermined++
			if *undetermined >= sshUndetermMax {
				*undetermined = 0
				d.sshMarkDisconnected(store, connID)
			}
		default:
			if m.ctx.Err() != nil {
				return m.connectedTick, false
			}
			d.sshMarkDisconnected(store, connID)
		}
		return m.connectedTick, false
	case protocol.SSHConnFailed:
		if alive && m.client.Check(m.ctx, sshTarget(c)) == nil {
			d.sshMarkConnected(store, connID)
		}
		return m.connectedTick, false
	}
	return m.connectedTick, false
}

func (d *Daemon) sshMarkAuthenticating(store *sshstore.Store, connID string) {
	var label string
	var notifyNow bool
	err := store.Update(func(st *sshstore.State) error {
		c := st.Conn(connID)
		if c == nil || c.State != protocol.SSHConnConnecting {
			return nil
		}
		c.State = protocol.SSHConnAuthenticating
		st.TouchConn()
		if !c.AuthNotified {
			c.AuthNotified, notifyNow, label = true, true, sshDisplay(c)
		}
		return nil
	})
	if err != nil {
		d.logf("", "ssh: record %s: %v", connID, err)
	}
	if notifyNow {
		d.sshNotify(notify.Urgent, "Sign in: "+label, "Waiting for you to sign in.")
	}
}

// sshCloseSession closes every connection of a session. Every teardown calls it
// first, because a master is not in any pane's tree and nothing else reaches it.
func (d *Daemon) sshCloseSession(ctx context.Context, id string) {
	d.ssh.mu.Lock()
	store := d.ssh.stores[id]
	d.ssh.mu.Unlock()
	if store == nil {
		return
	}
	var ids []string
	store.View(func(st *sshstore.State) {
		for _, c := range st.LiveConns() {
			ids = append(ids, c.ID)
		}
	})
	for _, c := range ids {
		d.sshCloseConn(ctx, store, c, "")
	}
}

// sshCloseConn is the one teardown, used by cmd=sshClose, every runtime.Kill path,
// host removal and session removal. The order is the contract: closing the master
// before the tab means a human in the shell sees the connection close rather than a
// tab vanish. A failure is logged and listed, and the idle timeout then bounds how
// long the master can linger.
func (d *Daemon) sshCloseConn(ctx context.Context, store *sshstore.Store, connID, why string) []string {
	d.sshStopTap(connID)
	d.ssh.prompts.forget(connID)
	now := d.ssh.now()
	var c sshstore.Conn
	var session string
	var stop []string
	var commandsRan, sharedMaster bool
	closing := false
	// 1. Closed, so nothing new starts. Open blocks are refused here too.
	err := store.Update(func(st *sshstore.State) error {
		p := st.Conn(connID)
		if p == nil || p.State == protocol.SSHConnClosed {
			return nil
		}
		session = st.Session
		info := st.ConnInfo(p)
		commandsRan = info.CommandsRun > 0
		c = *p
		closing = true
		sharedMaster = st.SharesMaster(p)
		p.State, p.StateDetail, p.ClosedAt, p.TabAlive = protocol.SSHConnClosed, why, now, false
		p.ConnectDeadline = time.Time{}
		sshEndGrant(st, p, "connection closed", now)
		// 2. Refuse what waits. What runs is stopped below, outside the lock.
		for _, it := range st.Items {
			if it.Kind != protocol.SSHItemBlock || it.Block.Conn != connID {
				continue
			}
			switch it.Block.Status {
			case protocol.SSHBlockPendingApproval, protocol.SSHBlockQueued:
				sshRefuseBlock(st, it, protocol.SSHRefuseNotConnected, "The connection was not up.", now)
			case protocol.SSHBlockRunning:
				stop = append(stop, it.Block.ID)
			}
		}
		st.AddEvent(protocol.SSHSystemEvent{Kind: protocol.SSHEventClosed, Text: "Connection closed.", Note: why, Conn: connID}, now)
		return nil
	})
	if err != nil {
		d.logf("", "ssh: record close of %s: %v", connID, err)
	}
	if !closing {
		return nil
	}
	for _, b := range stop {
		d.sshStopBlock(store, b)
	}
	var warnings []string
	if commandsRan || len(stop) > 0 {
		warnings = append(warnings, protocol.SSHRemoteOutlives)
	}
	// 3. The master. Never one that did not get as far as a socket, and never one another
	// tab is still using: its tab goes below, and the last tab to leave closes the master.
	if c.Sock != "" && !sharedMaster {
		if err := d.ssh.client.Exit(ctx, sshTarget(c)); err != nil {
			if cerr := d.ssh.client.Check(ctx, sshTarget(c)); cerr == nil {
				msg := fmt.Sprintf("The master did not close. It ends by itself after %s with nothing using it.", sshDuration(time.Duration(c.IdleLingerSeconds)*time.Second))
				warnings = append(warnings, msg)
				d.logf("", "ssh: %s: -O exit: %v", connID, err)
			}
		}
	}
	// 4. The terminal.
	if tm := d.sshTmuxClient(); tm != nil && c.Tab != "" {
		tctx, cancel := context.WithTimeout(ctx, sshTabExec)
		if tm.Has(tctx, c.Tab) {
			if err := tm.KillSessionTree(tctx, c.Tab); err != nil {
				warnings = append(warnings, "The terminal tab could not be closed.")
				d.logf("", "ssh: %s: kill tab %s: %v", connID, c.Tab, err)
			}
		}
		cancel()
	}
	// 5. The socket file, if ssh left it.
	if c.Sock != "" && !sharedMaster {
		if err := os.Remove(c.Sock); err != nil && !errors.Is(err, os.ErrNotExist) {
			d.logf("", "ssh: %s: remove socket: %v", connID, err)
		}
	}
	d.logf("", "ssh: %s closed (%s)", connID, session)
	return warnings
}
