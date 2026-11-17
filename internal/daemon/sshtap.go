package daemon

// The reader of a human's long-running shell. tmux pipe-pane sends the pane's raw bytes
// to a FIFO, internal/sshterm finds the markers the shell's hooks print, and each command
// line becomes a block. A FIFO rather than a file, because `yes` in a terminal must not
// fill a disk: output past the cap is read and thrown away.
//
// Reading an ssh tab is the human's choice for that host (`terminal_capture`), and a
// block made here is private until they share it. Everything in it is display material:
// a program on the host could learn the nonce and print a marker, so nothing here may
// reach a decision. The agent gets the shared directory and nothing else.

import (
	"context"
	"errors"
	"os"
	"path"
	"path/filepath"
	"syscall"
	"time"

	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/sshconn"
	"github.com/sushidev-team/lola/internal/sshelement"
	"github.com/sushidev-team/lola/internal/sshstore"
	"github.com/sushidev-team/lola/internal/sshterm"
)

type sshTap struct {
	tab  string
	stop context.CancelFunc
	done chan struct{}
}

// sshShellArgs starts the terminal with the hooks, when this host wants them and tmux
// can pipe a pane. Without either, the shell is an ordinary one.
func (d *Daemon) sshShellArgs(store *sshstore.Store, connID string, h config.SSHHost, tm sshTmux, args []string) []string {
	if _, ok := tm.(sshTapper); !ok || !h.CapturesTerminal() {
		return args
	}
	nonce := ""
	err := store.Update(func(st *sshstore.State) error {
		c := st.Conn(connID)
		if c == nil {
			return errors.New("gone")
		}
		if c.TermNonce == "" {
			n, err := sshterm.NewNonce()
			if err != nil {
				return err
			}
			c.TermNonce = n
		}
		nonce = c.TermNonce
		return nil
	})
	if err != nil || nonce == "" {
		return args
	}
	return sshconn.WithShell(args, sshterm.Bootstrap(nonce))
}

// sshEnsureTap reads a connection's tab, if it is not being read already. It is called
// after a tab opens and on every monitor tick, which is what re-arms a tab that outlived
// a daemon restart. Every failure leaves a working, unrecorded terminal.
func (d *Daemon) sshEnsureTap(store *sshstore.Store, connID string) {
	c, ok := sshConnCopy(store, connID)
	if !ok || c.TermNonce == "" || c.Tab == "" || c.Terminal() {
		return
	}
	m := d.ssh
	m.mu.Lock()
	cur := m.taps[connID]
	m.mu.Unlock()
	if cur != nil {
		select {
		case <-cur.done:
		default:
			if cur.tab == c.Tab {
				return
			}
		}
		d.sshStopTap(connID)
	}
	tm := d.sshTmuxClient()
	tapper, ok := tm.(sshTapper)
	if !ok {
		return
	}
	dir := filepath.Join(sshstore.Root(d.home), c.Session)
	fifo := filepath.Join(dir, "tap-"+filepath.Base(connID))
	_ = os.Remove(fifo)
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		d.logf("", "ssh: %s: no capture: %v", connID, err)
		return
	}
	// Read and write, so opening never waits for a writer and the reader never sees EOF
	// between writers. Blocking on purpose: Go cannot poll a FIFO on darwin, where a
	// non-blocking read just fails at once, so stopping wakes the read with a byte.
	f, err := os.OpenFile(fifo, os.O_RDWR, 0)
	if err != nil {
		d.logf("", "ssh: %s: no capture: %v", connID, err)
		return
	}
	ectx, cancel := context.WithTimeout(m.ctx, sshTabExec)
	err = tapper.PipePane(ectx, c.Tab, "cat > "+sshconn.ShQuote(fifo))
	cancel()
	if err != nil {
		_ = f.Close()
		d.logf("", "ssh: %s: no capture: %v", connID, err)
		return
	}
	ctx, stop := context.WithCancel(m.ctx)
	tap := &sshTap{tab: c.Tab, stop: stop, done: make(chan struct{})}
	r := &sshTapReader{d: d, store: store, conn: c, ctx: ctx}
	started := d.sshSpawn("shell capture "+connID, func() {
		defer close(tap.done)
		defer func() {
			_ = f.Close()
			_ = os.Remove(fifo)
		}()
		go func() {
			<-ctx.Done()
			_, _ = f.Write([]byte{0})
		}()
		parser := sshterm.NewParser(c.TermNonce, sshterm.Events{Start: r.start, Output: r.output, End: r.end})
		buf := make([]byte, 32*1024)
		for {
			n, rerr := f.Read(buf)
			if n > 0 {
				parser.Feed(buf[:n])
			}
			if rerr != nil || ctx.Err() != nil {
				r.abandon()
				return
			}
		}
	})
	if !started {
		stop()
		_ = f.Close()
		return
	}
	m.mu.Lock()
	if m.taps == nil {
		m.taps = map[string]*sshTap{}
	}
	m.taps[connID] = tap
	m.mu.Unlock()
	// Nothing is known about a shell until it next prints a prompt.
	_ = store.Update(func(st *sshstore.State) error {
		if p := st.Conn(connID); p != nil {
			p.TermAtPrompt = false
			st.TouchConn()
		}
		return nil
	})
}

// sshStopTap stops reading a tab and waits for the reader to finish its open block.
func (d *Daemon) sshStopTap(connID string) {
	m := d.ssh
	m.mu.Lock()
	tap := m.taps[connID]
	delete(m.taps, connID)
	m.mu.Unlock()
	if tap == nil {
		return
	}
	tap.stop()
	select {
	case <-tap.done:
	case <-time.After(sshTabExec):
	}
}

// sshSetCwd records where the next command starts. It takes only something that could be
// a directory, because the text came from the remote side.
func (d *Daemon) sshSetCwd(store *sshstore.Store, connID, cwd string) {
	if !path.IsAbs(cwd) || sshelement.ValidateCwd(cwd) != nil {
		return
	}
	_ = store.Update(func(st *sshstore.State) error {
		if c := st.Conn(connID); c != nil && c.Cwd != cwd {
			c.Cwd = cwd
			st.TouchConn()
		}
		return nil
	})
}

// sshRunInShell sends a human's command into their long-running shell, when it is at a
// prompt, so `export` and `sudo` stick. The block comes back through the reader like any
// other command typed there. It reports false when the shell cannot take it, and the
// caller runs the command over an exec channel.
func (d *Daemon) sshRunInShell(ctx context.Context, store *sshstore.Store, connID, command string) (bool, error) {
	tm := d.sshTmuxClient()
	tapper, ok := tm.(sshTapper)
	if !ok {
		return false, nil
	}
	tab := ""
	err := store.Update(func(st *sshstore.State) error {
		c := st.Conn(connID)
		if c != nil && c.TermTracked && c.TermAtPrompt && c.TabAlive && c.TermElevated == "" {
			tab, c.TermAtPrompt = c.Tab, false
			st.TouchConn()
		}
		return nil
	})
	if err != nil || tab == "" {
		return false, err
	}
	sctx, cancel := context.WithTimeout(ctx, sshTabExec)
	defer cancel()
	if err := tapper.SendText(sctx, tab, command, true); err != nil {
		return true, err
	}
	return true, nil
}

// handleSSHTerminalCd is the human's "Go to" button: their shell follows the shared
// folder. At a known prompt it runs, with a leading space so it stays out of the timeline
// and their history. Anywhere else the line is only typed, for them to look at first.
func (d *Daemon) handleSSHTerminalCd(ctx context.Context, a protocol.SSHConnArgs) (protocol.SSHConnInfo, error) {
	store, err := d.sshConnStore(a.Conn)
	if err != nil {
		return protocol.SSHConnInfo{}, err
	}
	c, ok := sshConnCopy(store, a.Conn)
	if !ok {
		return protocol.SSHConnInfo{}, errors.New("unknown connection")
	}
	if c.Cwd == "" || c.Tab == "" || !c.TabAlive {
		return protocol.SSHConnInfo{}, errors.New("there is no shell to move")
	}
	if c.TermElevated != "" {
		return protocol.SSHConnInfo{}, errors.New("your shell is inside " + c.TermElevated)
	}
	tapper, ok := d.sshTmuxClient().(sshTapper)
	if !ok {
		return protocol.SSHConnInfo{}, errors.New("tmux is not available")
	}
	run := c.TermTracked && c.TermAtPrompt
	if c.TermTracked && !c.TermAtPrompt {
		return protocol.SSHConnInfo{}, errors.New("your shell is busy")
	}
	sctx, cancel := context.WithTimeout(ctx, sshTabExec)
	defer cancel()
	if err := tapper.SendText(sctx, c.Tab, " cd -- "+sshconn.ShQuote(c.Cwd), run); err != nil {
		return protocol.SSHConnInfo{}, err
	}
	return sshConnInfo(store, a.Conn), nil
}

// sshTapReader turns one shell's events into blocks. Its methods all run on the one
// goroutine reading that FIFO.
type sshTapReader struct {
	d     *Daemon
	store *sshstore.Store
	conn  sshstore.Conn
	ctx   context.Context

	block          protocol.SSHBlock
	stdout, stderr *sshstore.Sink
	stopPreview    chan struct{}

	prompt sshTapPrompt
}

func (r *sshTapReader) start(raw string) {
	if r.stdout != nil { // the last command never reported its end
		r.finish(0, errors.New("the shell moved on"))
	}
	cmd := sshelement.Para(raw, sshelement.MaxCommandBytes)
	if cmd == "" || sshelement.ValidateCommand(cmd) != nil {
		cmd = sshClean(raw, 512)
	}
	if cmd == "" {
		return
	}
	d, now := r.d, r.d.ssh.now()
	wheel := ""
	var b protocol.SSHBlock
	err := r.store.Update(func(st *sshstore.State) error {
		c := st.Conn(r.conn.ID)
		if c == nil {
			return errors.New("gone")
		}
		wheel = c.Wheel
		c.TermAtPrompt = false
		if sshterm.OpensShell(cmd) {
			c.TermElevated = cmd
		}
		k := st.AddBlock(protocol.SSHBlock{
			ID: st.NewBlockID(), Conn: c.ID, Session: st.Session, Host: c.Host, Tier: c.Tier, User: sshBlockUser(c),
			Command: cmd, Cwd: c.TermCwd, Actor: protocol.SSHActorHuman, Private: true, Origin: protocol.SSHOriginTerminal,
			Status:    protocol.SSHBlockRunning,
			Approval:  protocol.SSHApprovalInfo{Mode: protocol.SSHApprovalHuman, Granted: true, At: now},
			CreatedAt: now, StartedAt: now,
		}, now)
		b = *k
		c.LastUsed = now
		st.TouchConn()
		return nil
	})
	if err != nil {
		return
	}
	// A human using their shell has started driving, wherever they typed it.
	if wheel != protocol.SSHWheelHuman {
		if _, err := d.handleSSHTakeWheel(r.ctx, protocol.SSHTakeWheelArgs{Conn: r.conn.ID}); err != nil {
			d.logf("", "ssh: %s: take the wheel: %v", r.conn.ID, err)
		}
	}
	capBytes := int64(1 << 20)
	if h := d.sshConfig().SSHHostByName(r.conn.Host); h != nil && h.OutputCapBytes() > 0 {
		capBytes = h.OutputCapBytes()
	}
	r.block = b
	r.stdout = r.store.NewSink(b.ID, protocol.SSHStreamStdout, capBytes)
	r.stderr = r.store.NewSink(b.ID, protocol.SSHStreamStderr, capBytes)
	r.stopPreview = make(chan struct{})
	go d.sshPreviewLoop(r.store, b.ID, r.stdout, r.stderr, r.stopPreview)
	r.promptStart(b.ID, cmd)
}

func (r *sshTapReader) output(p []byte) {
	if r.stdout == nil {
		return
	}
	if p = r.dropAnswerLine(p); len(p) > 0 {
		_, _ = r.stdout.Write(p)
		r.promptOutput(p)
	}
}

func (r *sshTapReader) end(exit int, cwd string, command, fullScreen bool) {
	if r.stdout != nil && command {
		id := r.block.ID
		r.finish(exit, nil)
		if fullScreen {
			_ = r.store.Update(func(st *sshstore.State) error {
				if it := st.Item(id); it != nil && it.Block != nil {
					it.Block.StatusDetail = "A full-screen program. Its screen is not recorded."
					st.Touch(it)
				}
				return nil
			})
		}
	}
	valid := path.IsAbs(cwd) && sshelement.ValidateCwd(cwd) == nil
	_ = r.store.Update(func(st *sshstore.State) error {
		c := st.Conn(r.conn.ID)
		if c == nil {
			return nil
		}
		c.TermTracked, c.TermAtPrompt, c.TermElevated = true, true, ""
		if valid {
			// A move in this shell moves the shared folder. The first prompt of a new
			// shell does not, because nobody chose to go there.
			if c.TermCwd != "" && c.TermCwd != cwd {
				c.Cwd = cwd
			}
			if c.Cwd == "" {
				c.Cwd = cwd
			}
			c.TermCwd = cwd
		}
		st.TouchConn()
		return nil
	})
}

func (r *sshTapReader) finish(exit int, cause error) {
	r.promptEnd()
	close(r.stopPreview)
	r.stdout.Close()
	r.stderr.Close()
	r.d.sshFinish(r.store, r.block, exit, cause, r.stdout, r.stderr)
	r.stdout, r.stderr, r.stopPreview = nil, nil, nil
}

// abandon closes the record when reading stops: the tab went away or the daemon stopped.
func (r *sshTapReader) abandon() {
	if r.stdout != nil {
		r.finish(0, errors.New("the shell went away"))
	}
	_ = r.store.Update(func(st *sshstore.State) error {
		if c := st.Conn(r.conn.ID); c != nil {
			c.TermAtPrompt, c.TermElevated = false, ""
			st.TouchConn()
		}
		return nil
	})
}
