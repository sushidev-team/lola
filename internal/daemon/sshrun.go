package daemon

// The command runner. A command is an exec channel on the master, run with its own
// process group, a timeout and an output cap, on a goroutine that is never the
// observer's or a socket handler's. The block's life is the daemon's, so an agent's
// tool timing out does not stop a command that a human approved.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/sshconn"
	"github.com/sushidev-team/lola/internal/sshstore"
	"github.com/sushidev-team/lola/internal/sshterm"
)

var (
	errSSHStopped  = errors.New("stopped")
	errSSHTimedOut = errors.New("timed out")
)

// What a block says when it was never started. "May still be running" would be
// false here, and a block's status is a fact.
const (
	sshNotRunRestart = "lola restarted before this ran. It did not run."
	sshNotRunStop    = "lola stopped before this ran. It did not run."
)

func sshRunKey(session, block string) string { return session + "/" + block }

// sshDispatch starts what can start on a connection. Wheel and connection state are
// re-checked at dequeue: a block approved while the world changed is refused, not
// run. A human's own proposal does not need the wheel, it only needs a connection.
func (d *Daemon) sshDispatch(store *sshstore.Store, connID string) {
	type start struct {
		block protocol.SSHBlock
		conn  sshstore.Conn
	}
	var starts []start
	now := d.ssh.now()
	var refused []string
	d.ssh.mu.Lock()
	stopping := d.ssh.stopped
	d.ssh.mu.Unlock()
	err := store.Update(func(st *sshstore.State) error {
		c := st.Conn(connID)
		if c == nil {
			return nil
		}
		running := 0
		for _, it := range st.Items {
			if it.Kind == protocol.SSHItemBlock && it.Block.Conn == connID && it.Block.Status == protocol.SSHBlockRunning {
				running++
			}
		}
		for _, it := range st.Items {
			if it.Kind != protocol.SSHItemBlock || it.Block.Conn != connID || it.Block.Status != protocol.SSHBlockQueued {
				continue
			}
			b := it.Block
			if stopping {
				sshRefuseBlock(st, it, protocol.SSHRefuseNotConnected, sshNotRunStop, now)
				refused = append(refused, b.ID)
				continue
			}
			if c.State != protocol.SSHConnConnected {
				if c.State == protocol.SSHConnAuthenticating || c.State == protocol.SSHConnConnecting {
					continue // a reconnect is under way; re-checked when it lands
				}
				sshRefuseBlock(st, it, protocol.SSHRefuseNotConnected, "The connection was not up.", now)
				refused = append(refused, b.ID)
				continue
			}
			if c.Wheel == protocol.SSHWheelHuman && b.Actor == protocol.SSHActorAgent {
				sshRefuseBlock(st, it, protocol.SSHRefuseHumanControl, "You had the wheel.", now)
				refused = append(refused, b.ID)
				continue
			}
			if running >= sshMaxRunning {
				break
			}
			running++
			b.Status = protocol.SSHBlockRunning
			b.StartedAt = now
			b.ApprovalExpiresAt = time.Time{}
			st.Touch(it)
			c.LastUsed = now
			starts = append(starts, start{*b, *c})
		}
		return nil
	})
	if err != nil {
		d.logf("", "ssh: dispatch %s: %v", connID, err)
	}
	session := ""
	store.View(func(st *sshstore.State) { session = st.Session })
	why := "the connection or the wheel changed first"
	if stopping {
		why = "lola stopped first"
	}
	for _, id := range refused {
		d.sshNudgeIfGone(store, session, "[lola] Your command "+id+" did not run: "+why+".")
	}
	for _, s := range starts {
		d.sshStartBlock(store, s.conn, s.block)
	}
}

func sshRefuseBlock(st *sshstore.State, it *protocol.SSHTimelineItem, code, detail string, now time.Time) {
	b := it.Block
	b.Status, b.RefusalCode, b.StatusDetail, b.EndedAt = protocol.SSHBlockRefused, code, detail, now
	b.ApprovalExpiresAt = time.Time{}
	b.Approval.Granted = false
	st.Touch(it)
}

func (d *Daemon) sshStartBlock(store *sshstore.Store, c sshstore.Conn, b protocol.SSHBlock) {
	timeout := time.Duration(b.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 2 * time.Minute
	}
	ctx, cancel := context.WithCancelCause(d.ssh.ctx)
	run := &sshRunningBlock{cancel: cancel, done: make(chan struct{})}
	key := sshRunKey(b.Session, b.ID)
	d.ssh.mu.Lock()
	d.ssh.running[key] = run
	d.ssh.mu.Unlock()
	cleanup := func() {
		d.ssh.mu.Lock()
		delete(d.ssh.running, key)
		d.ssh.mu.Unlock()
		cancel(nil)
		close(run.done)
	}
	started := d.sshSpawn("command "+b.ID, func() {
		defer cleanup()
		tctx, tcancel := context.WithTimeoutCause(ctx, timeout, errSSHTimedOut)
		defer tcancel()
		d.sshRunBlock(tctx, store, c, b)
	})
	if !started {
		cleanup()
		d.sshNeverStarted(store, b.ID)
	}
}

// sshNeverStarted undoes a dispatch that lost the race with shutdown. Nothing ran,
// so the block reads refused, not lost, and StartedAt goes with it: CommandsRun
// counts that field.
func (d *Daemon) sshNeverStarted(store *sshstore.Store, id string) {
	now := d.ssh.now()
	err := store.Update(func(st *sshstore.State) error {
		if it := st.Item(id); it != nil && it.Block != nil {
			sshRefuseBlock(st, it, protocol.SSHRefuseNotConnected, sshNotRunStop, now)
			it.Block.StartedAt = time.Time{}
		}
		return nil
	})
	if err != nil {
		d.logf("", "ssh: record that %s never started: %v", id, err)
	}
}

func (d *Daemon) sshRunBlock(ctx context.Context, store *sshstore.Store, c sshstore.Conn, b protocol.SSHBlock) {
	capBytes := int64(0)
	if h := d.sshConfig().SSHHostByName(c.Host); h != nil {
		capBytes = h.OutputCapBytes()
	}
	if capBytes <= 0 {
		capBytes = 1 << 20
	}
	stdout := store.NewSink(b.ID, protocol.SSHStreamStdout, capBytes)
	stderr := store.NewSink(b.ID, protocol.SSHStreamStderr, capBytes)

	stop := make(chan struct{})
	go d.sshPreviewLoop(store, b.ID, stdout, stderr, stop)

	d.logf("", "ssh: %s: %s started on %s", b.Session, b.ID, c.ID)
	// The command reports the directory it ended in, which is where the next one starts.
	remote := sshconn.RemoteCommand(b.Cwd, b.Command)
	var cwdOut io.Writer = stderr
	var filter *sshterm.CwdFilter
	if nonce, nerr := sshterm.NewNonce(); nerr == nil {
		filter = sshterm.NewCwdFilter(nonce, stderr)
		cwdOut, remote = filter, sshconn.RemoteCommandTracked(b.Cwd, b.Command, nonce)
	}
	exit, err := d.ssh.client.Run(ctx, sshTarget(c), remote, stdout, cwdOut)
	close(stop)
	if filter != nil {
		d.sshSetCwd(store, c.ID, filter.Close())
	}
	stdout.Close()
	stderr.Close()
	cause := error(nil)
	if ctx.Err() != nil {
		cause = context.Cause(ctx)
	}
	if err != nil && cause == nil {
		cause = err
	}
	// Exit 255 is ssh's own failure, or a remote command that chose it. Only the
	// master can tell them apart, and a remote 255 with a healthy master is real.
	if cause == nil && exit == 255 {
		cctx, cancel := context.WithTimeout(d.ssh.ctx, sshconn.ControlTimeout)
		defer cancel()
		if cerr := d.ssh.client.Check(cctx, sshTarget(c)); cerr != nil {
			cause = errors.New("the connection went away")
		}
	}
	d.sshFinish(store, b, exit, cause, stdout, stderr)
	d.logf("", "ssh: %s: %s finished on %s", b.Session, b.ID, c.ID)
	d.sshDispatch(store, c.ID)
}

// sshFinish records a command's end. The status is a fact, and when the result is
// unknown ExitCode stays nil rather than a guess.
func (d *Daemon) sshFinish(store *sshstore.Store, b protocol.SSHBlock, exit int, cause error, stdout, stderr *sshstore.Sink) {
	now := d.ssh.now()
	var status, detail string
	switch {
	case errors.Is(cause, errSSHStopped):
		status, detail = protocol.SSHBlockStopped, protocol.SSHRemoteOutlives
	case errors.Is(cause, errSSHTimedOut):
		status, detail = protocol.SSHBlockTimedOut, fmt.Sprintf("Ran past its %d second timeout. %s", b.TimeoutSeconds, protocol.SSHRemoteOutlives)
	case cause != nil && d.ssh.ctx.Err() != nil:
		status, detail = protocol.SSHBlockLost, "lola restarted while this ran. The remote process may still be running."
	case cause != nil:
		status, detail = protocol.SSHBlockLost, "The connection went away while this ran. The remote process may still be running."
	case exit == 0:
		status = protocol.SSHBlockSucceeded
	default:
		status = protocol.SSHBlockFailed
	}
	var so, se sshstore.Stats
	if stdout != nil {
		so, se = stdout.Stats(), stderr.Stats()
	}
	var final protocol.SSHBlock
	err := store.Update(func(st *sshstore.State) error {
		it := st.Item(b.ID)
		if it == nil || it.Block == nil {
			return nil
		}
		k := it.Block
		k.Status, k.StatusDetail, k.EndedAt = status, detail, now
		if !k.StartedAt.IsZero() {
			k.DurationMs = now.Sub(k.StartedAt).Milliseconds()
		}
		if status == protocol.SSHBlockSucceeded || status == protocol.SSHBlockFailed {
			code := exit
			k.ExitCode = &code
		}
		if stdout != nil {
			sshApplyStats(k, so, se, status)
			if so.Failed || se.Failed {
				k.StatusDetail = strings.TrimSpace(k.StatusDetail + " Some output could not be saved.")
			}
			st.OutputBytes += so.Stored + se.Stored
		}
		st.Touch(it)
		if c := st.Conn(k.Conn); c != nil {
			c.LastUsed = now
		}
		store.Prune(st)
		final = *k
		return nil
	})
	if err != nil {
		d.logf("", "ssh: record the end of %s: %v", b.ID, err)
	}
	d.sshAfterBlock(store, final)
}

// sshApplyStats copies a stream's progress onto its block.
func sshApplyStats(k *protocol.SSHBlock, so, se sshstore.Stats, status string) {
	k.StdoutBytes, k.StderrBytes = so.Stored, se.Stored
	k.DroppedBytes = so.Dropped + se.Dropped
	k.Binary = so.Binary || se.Binary
	k.Preview, k.PreviewStream = so.Recent, protocol.SSHStreamStdout
	useErr := status == protocol.SSHBlockFailed || status == protocol.SSHBlockTimedOut || status == protocol.SSHBlockLost || so.Recent == ""
	if useErr && se.Recent != "" {
		k.Preview, k.PreviewStream = se.Recent, protocol.SSHStreamStderr
	}
	if k.Preview == "" {
		k.PreviewStream = ""
	}
}

// sshPreviewLoop refreshes a running block's sizes and preview at most once a second,
// which is what lets a timeline show a live command without a fetch per row and
// without a Seq per output chunk. In memory only: the next Update carries it.
func (d *Daemon) sshPreviewLoop(store *sshstore.Store, block string, stdout, stderr *sshstore.Sink, stop <-chan struct{}) {
	t := time.NewTicker(sshPreviewTick)
	defer t.Stop()
	last := int64(-1)
	for {
		select {
		case <-stop:
			return
		case <-t.C:
		}
		so, se := stdout.Stats(), stderr.Stats()
		seen := so.Stored + so.Dropped + se.Stored + se.Dropped
		if seen == last {
			continue
		}
		last = seen
		store.Volatile(func(st *sshstore.State) bool {
			it := st.Item(block)
			if it == nil || it.Block == nil || it.Block.Status != protocol.SSHBlockRunning {
				return false
			}
			sshApplyStats(it.Block, so, se, protocol.SSHBlockRunning)
			st.Touch(it)
			return true
		})
	}
}

// sshAfterBlock tells an agent that gave up waiting. The line is lola's own text and
// carries no remote text. A human's run of a proposal always tells it, since nothing
// was polling for that block at all.
func (d *Daemon) sshAfterBlock(store *sshstore.Store, b protocol.SSHBlock) {
	if b.ID == "" || b.Private {
		return
	}
	line := fmt.Sprintf("[lola] Your command %s %s. Read it with: lola ssh output %s.", b.ID, sshOutcomePhrase(b), b.ID)
	if b.Actor == protocol.SSHActorHuman {
		text := fmt.Sprintf("[lola] The human ran your proposal as %s. It %s. Read it with: lola ssh output %s.", b.ID, sshOutcomePhrase(b), b.ID)
		d.sshEnqueueLola(store, b.Session, text)
		return
	}
	d.sshNudgeIfGone(store, b.Session, line)
}

func sshOutcomePhrase(b protocol.SSHBlock) string {
	switch b.Status {
	case protocol.SSHBlockSucceeded:
		return "finished with exit 0"
	case protocol.SSHBlockFailed:
		if b.ExitCode != nil {
			return fmt.Sprintf("finished with exit %d", *b.ExitCode)
		}
		return "failed"
	case protocol.SSHBlockTimedOut:
		return "timed out"
	case protocol.SSHBlockStopped:
		return "was stopped"
	}
	return "ended with its result unknown"
}

// sshStopBlock stops a block, whoever asked. A waiting one simply never runs. A
// running one has its local process group killed, and the reply waits briefly so it
// shows the settled state.
func (d *Daemon) sshStopBlock(store *sshstore.Store, id string) protocol.SSHBlock {
	now := d.ssh.now()
	var session string
	var wasRunning bool
	_ = store.Update(func(st *sshstore.State) error {
		session = st.Session
		it := st.Item(id)
		if it == nil || it.Block == nil {
			return nil
		}
		switch it.Block.Status {
		case protocol.SSHBlockPendingApproval, protocol.SSHBlockQueued:
			it.Block.Status, it.Block.EndedAt = protocol.SSHBlockStopped, now
			it.Block.ApprovalExpiresAt = time.Time{}
			it.Block.StatusDetail = "Stopped before it ran."
			st.Touch(it)
		case protocol.SSHBlockRunning:
			wasRunning = true
		}
		return nil
	})
	if wasRunning {
		// The dispatcher marks a block running a moment before registering it, so a
		// stop landing in that gap has to wait for the entry or it would do nothing.
		var run *sshRunningBlock
		for i := 0; i < 50 && run == nil; i++ {
			d.ssh.mu.Lock()
			run = d.ssh.running[sshRunKey(session, id)]
			d.ssh.mu.Unlock()
			if run == nil {
				time.Sleep(20 * time.Millisecond)
			}
		}
		if run != nil {
			run.cancel(errSSHStopped)
			select {
			case <-run.done:
			case <-time.After(5 * time.Second):
			}
		}
	}
	if b := sshBlockCopy(store, id); b != nil {
		return *b
	}
	return protocol.SSHBlock{ID: id}
}
