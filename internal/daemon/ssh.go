package daemon

// Agent-assisted ssh: the daemon half. docs/ssh-sessions.md is the contract and
// this file group implements it.
//
//	ssh.go          state, seams, dispatch, lifecycle, the sweep
//	sshagent.go     the agent side: hosts, request, run, poll, cancel, post
//	sshhuman.go     the human side: go, deny, approve, wheel, close, say, proposals
//	sshconnect.go   bringing a connection up, its terminal tab, the monitor
//	sshrun.go       the command runner
//	sshqueue.go     text waiting for the agent
//	sshopen.go      a direct session, and the glance on cmd=sessions
//
// The shape in one paragraph. The agent stays local and holds nothing: its verbs
// reach this daemon, which owns a real ssh master in a tmux tab where the human
// authenticated. A command is an exec channel on that master, never keystrokes, so
// it has a real exit code and nothing parses a pane. What ran is recorded as FACTS
// in internal/sshstore, and what the agent says about it stays a CLAIM that never
// reaches a state machine.
//
// Lock order: d.sessions, then a store. A store's lock never takes anything else,
// d.ssh.mu may take a store's read lock, and no lock is ever held across an exec.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/notify"
	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/sshconn"
	"github.com/sushidev-team/lola/internal/sshelement"
	"github.com/sushidev-team/lola/internal/sshstore"
	"github.com/sushidev-team/lola/internal/tmux"
)

const (
	sshConnectTick   = time.Second      // the monitor's cadence while a master comes up
	sshConnectedTick = 15 * time.Second // and once one is up: one -O check, no network traffic of note
	sshAuthAfter     = 4 * time.Second  // connecting becomes authenticating
	sshSweepEvery    = 2 * time.Second
	sshPruneEvery    = time.Hour

	sshMaxRunning = 4  // per connection; the rest wait in queued
	sshMaxOpen    = 16 // pending + queued per connection

	sshRunBurst  = 30 // per session, per minute
	sshPostBurst = 60 // per session, per minute

	sshNudgeAfter  = 15 * time.Second // no agent poll for this long: it stopped waiting
	sshPreviewTick = time.Second
	sshTabExec     = 10 * time.Second
	sshWhyRunes    = 500
	sshReasonRunes = 500
	sshUndetermMax = 3 // unanswered checks before a master counts as gone
)

// sshTmux is the slice of the tmux client the ssh paths use, so tests can drive
// them with no tmux server. *tmux.Client satisfies it.
type sshTmux interface {
	Available() bool
	ListSessions(ctx context.Context) ([]tmux.Session, error)
	DeadPanes(ctx context.Context) (map[string]bool, error)
	Has(ctx context.Context, name string) bool
	NewSession(ctx context.Context, name, dir, command string) error
	KeepDeadPane(ctx context.Context, name string) error
	KillSessionTree(ctx context.Context, name string) error
	HistorySize(ctx context.Context, name string) (int, int, error)
	CaptureFrom(ctx context.Context, name string, back int) (string, error)
}

// sshRunningBlock is one command in flight.
// sshTapper is the part of tmux only the shell capture needs, kept out of sshTmux so a
// fake without it simply has no capture.
type sshTapper interface {
	PipePane(ctx context.Context, name, command string) error
	SendText(ctx context.Context, name, text string, enter bool) error
}

// sshPrompter is the part of tmux that answers a shell's question, kept out of sshTmux for
// the same reason: a fake without it simply never shows a card.
type sshPrompter interface {
	PaneInput(ctx context.Context, name string) (tmux.PaneInput, error)
	PasteSecret(ctx context.Context, name, secret string) error
	SendInterrupt(ctx context.Context, name string) error
}

type sshRunningBlock struct {
	cancel context.CancelCauseFunc
	done   chan struct{}
}

// sshState is everything the ssh paths keep outside the stores. Its zero-time
// fields and seams are set by newSSHState and overridden by tests.
type sshState struct {
	mu       sync.Mutex
	stores   map[string]*sshstore.Store
	conns    map[string]string // connection id -> session id
	running  map[string]*sshRunningBlock
	monitors map[string]chan struct{} // connection id -> kick
	taps     map[string]*sshTap       // connection id -> the reader of its shell
	lastPoll map[string]time.Time     // session id -> last agent poll
	prompts  sshPrompts
	stopped  bool

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	client   sshconn.Client
	tmuxFn   func() sshTmux
	lookPath func(string) (string, error)
	now      func() time.Time

	connectTick   time.Duration
	connectedTick time.Duration
	sweepEvery    time.Duration

	runLimit  *slidingLimiter
	postLimit *slidingLimiter
}

func newSSHState() *sshState {
	ctx, cancel := context.WithCancel(context.Background())
	return &sshState{
		stores:        map[string]*sshstore.Store{},
		conns:         map[string]string{},
		running:       map[string]*sshRunningBlock{},
		monitors:      map[string]chan struct{}{},
		lastPoll:      map[string]time.Time{},
		ctx:           ctx,
		cancel:        cancel,
		lookPath:      exec.LookPath,
		now:           time.Now,
		connectTick:   sshConnectTick,
		connectedTick: sshConnectedTick,
		sweepEvery:    sshSweepEvery,
		runLimit:      &slidingLimiter{burst: sshRunBurst, window: time.Minute},
		postLimit:     &slidingLimiter{burst: sshPostBurst, window: time.Minute},
	}
}

// spawn runs f on a tracked goroutine, unless the daemon is stopping. A panic stays
// in its goroutine, because one bad command must not take the daemon with it.
func (d *Daemon) sshSpawn(what string, f func()) bool {
	m := d.ssh
	m.mu.Lock()
	if m.stopped {
		m.mu.Unlock()
		return false
	}
	m.wg.Add(1)
	m.mu.Unlock()
	go func() {
		defer m.wg.Done()
		defer func() {
			if r := recover(); r != nil {
				d.logf("", "ssh: %s panicked: %v", what, r)
			}
		}()
		f()
	}()
	return true
}

// slidingLimiter is a per-key sliding-window counter, the shape of reportLimiter.
type slidingLimiter struct {
	mu     sync.Mutex
	burst  int
	window time.Duration
	hits   map[string][]time.Time
}

func (l *slidingLimiter) allow(id string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.hits == nil {
		l.hits = map[string][]time.Time{}
	}
	cut := now.Add(-l.window)
	h := l.hits[id]
	i := 0
	for i < len(h) && h[i].Before(cut) {
		i++
	}
	h = h[i:]
	if len(h) >= l.burst {
		l.hits[id] = h
		return false
	}
	l.hits[id] = append(h, now)
	return true
}

func (d *Daemon) sshTmuxClient() sshTmux {
	if d.ssh.tmuxFn != nil {
		return d.ssh.tmuxFn()
	}
	return d.tmuxClient()
}

// sshConfig is the current config. A *Config is replaced on reload, never mutated,
// so it can be read after the lock is gone.
func (d *Daemon) sshConfig() *config.Config {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.cfg
}

// sshStore returns a session's store from memory, then disk. With create it makes
// one. nil when there is none.
func (d *Daemon) sshStore(session string, create bool) (*sshstore.Store, error) {
	m := d.ssh
	m.mu.Lock()
	defer m.mu.Unlock()
	if s := m.stores[session]; s != nil {
		return s, nil
	}
	s, err := sshstore.Open(d.home, session, create, func(f string, a ...any) { d.logf("", f, a...) })
	if err != nil || s == nil {
		return nil, err
	}
	m.stores[session] = s
	s.View(func(st *sshstore.State) {
		for _, c := range st.Conns {
			m.conns[c.ID] = session
		}
	})
	return s, nil
}

// sshConnStore finds the store holding a connection. A human names a connection
// by id alone.
func (d *Daemon) sshConnStore(conn string) (*sshstore.Store, error) {
	m := d.ssh
	m.mu.Lock()
	session := m.conns[conn]
	m.mu.Unlock()
	if session == "" {
		return nil, fmt.Errorf("unknown connection %s", conn)
	}
	return d.sshStore(session, false)
}

func (d *Daemon) sshIndexConn(conn, session string) {
	d.ssh.mu.Lock()
	d.ssh.conns[conn] = session
	d.ssh.mu.Unlock()
}

func sshAll(m map[string]*sshstore.Store) []*sshstore.Store {
	out := make([]*sshstore.Store, 0, len(m))
	for _, s := range m {
		out = append(out, s)
	}
	return out
}

func (d *Daemon) sshStores() []*sshstore.Store {
	d.ssh.mu.Lock()
	defer d.ssh.mu.Unlock()
	return sshAll(d.ssh.stores)
}

// sshConnCopy reads a connection out of a store. ok is false when it is gone.
func sshConnCopy(store *sshstore.Store, conn string) (c sshstore.Conn, ok bool) {
	store.View(func(st *sshstore.State) {
		if p := st.Conn(conn); p != nil {
			c, ok = *p, true
		}
	})
	return c, ok
}

func sshConnInfo(store *sshstore.Store, conn string) protocol.SSHConnInfo {
	var info protocol.SSHConnInfo
	store.View(func(st *sshstore.State) {
		if p := st.Conn(conn); p != nil {
			info = st.ConnInfo(p)
		}
	})
	return info
}

func sshTarget(c sshstore.Conn) sshconn.Target {
	return sshconn.Target{Sock: c.Sock, Alias: c.Alias, User: c.User, Port: c.Port, IdentityFile: c.IdentityFile}
}

func sshDisplay(c *sshstore.Conn) string {
	if c.Label != "" {
		return c.Label
	}
	return c.Host
}

// sshNotify tells a human without holding up the caller: a notifier can take 15s.
// Only lola's own sentences and clipped facts go in here.
func (d *Daemon) sshNotify(prio notify.Priority, title, body string) {
	d.mu.Lock()
	n := d.notifier
	d.mu.Unlock()
	if n == nil || !d.beginConnWork() {
		return
	}
	go func() {
		defer d.connWg.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		n.Notify(ctx, notify.Note{Title: title, Body: body, Priority: prio})
	}()
}

func sshDecode[T any](req protocol.Request) (T, error) {
	var a T
	if len(req.Args) > 0 {
		if err := json.Unmarshal(req.Args, &a); err != nil {
			return a, fmt.Errorf("%s: bad args: %w", req.Cmd, err)
		}
	}
	return a, nil
}

func sshReply[T any](data T, err error) protocol.Response {
	if err != nil {
		return protocol.Response{OK: false, Error: err.Error()}
	}
	return dataResponse(data)
}

// sshRoute decodes args and runs the handler, as one generic step of the switch.
func sshRoute[A, R any](req protocol.Request, h func(A) (R, error)) protocol.Response {
	a, err := sshDecode[A](req)
	if err != nil {
		return protocol.Response{OK: false, Error: err.Error()}
	}
	return sshReply(h(a))
}

// handleSSH serves every ssh command. The bridge's allowlist and internal/remote's
// denial both run before this, and nothing here relaxes them.
func (d *Daemon) handleSSH(ctx context.Context, req protocol.Request) protocol.Response {
	switch req.Cmd {
	case protocol.CmdSSHAgentHosts:
		return sshRoute(req, func(a protocol.SSHAgentHostsArgs) (protocol.SSHAgentHostsData, error) {
			return d.handleSSHAgentHosts(req.Session, a)
		})
	case protocol.CmdSSHAgentRequest:
		return sshRoute(req, func(a protocol.SSHAgentRequestArgs) (protocol.SSHAgentRequestData, error) {
			return d.handleSSHAgentRequest(req.Session, a)
		})
	case protocol.CmdSSHAgentRun:
		return sshRoute(req, func(a protocol.SSHAgentRunArgs) (protocol.SSHAgentRunData, error) {
			return d.handleSSHAgentRun(req.Session, a)
		})
	case protocol.CmdSSHAgentPoll:
		return sshRoute(req, func(a protocol.SSHAgentPollArgs) (protocol.SSHAgentPollData, error) {
			return d.handleSSHAgentPoll(req.Session, a)
		})
	case protocol.CmdSSHAgentCancel:
		return sshRoute(req, func(a protocol.SSHAgentCancelArgs) (protocol.SSHBlock, error) {
			return d.handleSSHAgentCancel(req.Session, a)
		})
	case protocol.CmdSSHAgentPost:
		return sshRoute(req, func(a protocol.SSHAgentPostArgs) (protocol.SSHAgentPostData, error) {
			return d.handleSSHAgentPost(req.Session, a)
		})

	case protocol.CmdSSHHosts:
		return dataResponse(d.handleSSHHosts())
	case protocol.CmdSSHOpen:
		return sshRoute(req, func(a protocol.SSHOpenArgs) (protocol.SSHOpenData, error) { return d.handleSSHOpen(ctx, a) })
	case protocol.CmdSSHConnect:
		return sshRoute(req, func(a protocol.SSHConnectArgs) (protocol.SSHConnectData, error) { return d.handleSSHConnect(ctx, a) })
	case protocol.CmdSSHHumanRun:
		return sshRoute(req, func(a protocol.SSHHumanRunArgs) (protocol.SSHBlock, error) { return d.handleSSHHumanRun(ctx, a) })
	case protocol.CmdSSHShareBlock:
		return sshRoute(req, func(a protocol.SSHShareBlockArgs) (protocol.SSHBlock, error) { return d.handleSSHShareBlock(ctx, a) })
	case protocol.CmdSSHTerminalCd:
		return sshRoute(req, func(a protocol.SSHConnArgs) (protocol.SSHConnInfo, error) { return d.handleSSHTerminalCd(ctx, a) })
	case protocol.CmdSSHGo:
		return sshRoute(req, func(a protocol.SSHGoArgs) (protocol.SSHConnInfo, error) { return d.handleSSHGo(ctx, a) })
	case protocol.CmdSSHDeny:
		return sshRoute(req, func(a protocol.SSHDenyArgs) (protocol.SSHConnInfo, error) { return d.handleSSHDeny(a) })
	case protocol.CmdSSHApprove:
		return sshRoute(req, func(a protocol.SSHApproveArgs) (protocol.SSHBlock, error) { return d.handleSSHApprove(a) })
	case protocol.CmdSSHRevokeGrant:
		return sshRoute(req, func(a protocol.SSHConnArgs) (protocol.SSHConnInfo, error) { return d.handleSSHRevokeGrant(a) })
	case protocol.CmdSSHTakeWheel:
		return sshRoute(req, func(a protocol.SSHTakeWheelArgs) (protocol.SSHTakeWheelData, error) {
			return d.handleSSHTakeWheel(ctx, a)
		})
	case protocol.CmdSSHHandbackPreview:
		return sshRoute(req, func(a protocol.SSHConnArgs) (protocol.SSHHandbackPreviewData, error) {
			return d.handleSSHHandbackPreview(ctx, a)
		})
	case protocol.CmdSSHHandback:
		return sshRoute(req, func(a protocol.SSHHandbackArgs) (protocol.SSHHandbackData, error) { return d.handleSSHHandback(ctx, a) })
	case protocol.CmdSSHStop:
		return sshRoute(req, func(a protocol.SSHStopArgs) (protocol.SSHStopData, error) { return d.handleSSHStop(a) })
	case protocol.CmdSSHClose:
		return sshRoute(req, func(a protocol.SSHConnArgs) (protocol.SSHCloseData, error) { return d.handleSSHClose(ctx, a) })
	case protocol.CmdSSHTrash:
		return sshRoute(req, func(a protocol.SSHConnArgs) (protocol.SSHCloseData, error) { return d.handleSSHTrash(ctx, a) })
	case protocol.CmdSSHTerminal:
		return sshRoute(req, func(a protocol.SSHConnArgs) (protocol.SSHConnInfo, error) { return d.handleSSHTerminal(ctx, a) })
	case protocol.CmdSSHTimeline:
		return sshRoute(req, func(a protocol.SSHTimelineArgs) (protocol.SSHTimelineData, error) { return d.handleSSHTimeline(a) })
	case protocol.CmdSSHOutput:
		return sshRoute(req, func(a protocol.SSHOutputArgs) (protocol.SSHOutputData, error) { return d.handleSSHOutput(a) })
	case protocol.CmdSSHSay:
		return sshRoute(req, func(a protocol.SSHSayArgs) (protocol.SSHSayData, error) { return d.handleSSHSay(ctx, a) })
	case protocol.CmdSSHAnswer:
		return sshRoute(req, func(a protocol.SSHAnswerArgs) (protocol.SSHSayData, error) { return d.handleSSHAnswer(ctx, a) })
	case protocol.CmdSSHProposal:
		return sshRoute(req, func(a protocol.SSHProposalArgs) (protocol.SSHProposalData, error) { return d.handleSSHProposal(ctx, a) })
	case protocol.CmdSSHPromptAnswer:
		return sshRoute(req, func(a protocol.SSHPromptAnswerArgs) (protocol.SSHPromptAnswerData, error) {
			return d.handleSSHPromptAnswer(ctx, a)
		})
	case protocol.CmdSSHWidgetStatus:
		return sshRoute(req, func(a protocol.SSHWidgetStatusArgs) (protocol.SSHWidgetStatusData, error) {
			return d.handleSSHWidgetStatus(ctx, a)
		})
	case protocol.CmdSSHRecall:
		return sshRoute(req, func(a protocol.SSHRecallArgs) (protocol.SSHRecallData, error) { return d.handleSSHRecall(a) })
	case protocol.CmdSSHResolve:
		return sshRoute(req, func(a protocol.SSHResolveArgs) (protocol.SSHResolveData, error) {
			return d.handleSSHResolve(ctx, a), nil
		})
	}
	return protocol.Response{OK: false, Error: "unknown ssh command " + req.Cmd}
}

// --- Lifecycle --------------------------------------------------------------

// startSSH reads what a previous daemon left and starts the background work. A
// local ssh child does not survive its daemon, so a block that was running reads
// lost. A master does survive, on purpose, and each one gets a look.
func (d *Daemon) startSSH(_ context.Context) {
	d.sshResume()
	d.sshSpawn("sweep", d.sshSweepLoop)
}

// stopSSH ends the runner and the monitors. It closes no master: those persist
// across a restart, bounded by their idle timeout.
func (d *Daemon) stopSSH() {
	m := d.ssh
	m.mu.Lock()
	m.stopped = true
	m.mu.Unlock()
	m.cancel()
	m.wg.Wait()
	for _, s := range d.sshStores() {
		if err := s.Flush(); err != nil {
			d.logf("", "ssh: flush at shutdown: %v", err)
		}
	}
}

func (d *Daemon) sshResume() {
	entries, err := os.ReadDir(sshstore.Root(d.home))
	if err != nil {
		return
	}
	for _, e := range entries {
		if !e.IsDir() || !sshstore.ValidSession(e.Name()) {
			continue
		}
		store, err := d.sshStore(e.Name(), false)
		if err != nil || store == nil {
			if err != nil {
				d.logf("", "ssh: store %s: %v", e.Name(), err)
			}
			continue
		}
		d.sshResumeStore(store)
	}
}

func (d *Daemon) sshResumeStore(store *sshstore.Store) {
	now := d.ssh.now()
	var unrun []string
	session := ""
	err := store.Update(func(st *sshstore.State) error {
		session = st.Session
		for _, it := range st.Items {
			if it.Kind != protocol.SSHItemBlock {
				continue
			}
			// An approval is for now, not for whenever the next dispatch happens to
			// come. A crash must not turn it into a command that runs unannounced.
			if it.Block.Status == protocol.SSHBlockQueued {
				sshRefuseBlock(st, it, protocol.SSHRefuseNotConnected, sshNotRunRestart, now)
				unrun = append(unrun, it.Block.ID)
				continue
			}
			if it.Block.Status != protocol.SSHBlockRunning {
				continue
			}
			b := it.Block
			_, so, _ := store.ReadRange(b.ID, protocol.SSHStreamStdout, 0, 1)
			_, se, _ := store.ReadRange(b.ID, protocol.SSHStreamStderr, 0, 1)
			b.StdoutBytes, b.StderrBytes = so, se
			b.Status = protocol.SSHBlockLost
			b.StatusDetail = "lola restarted while this ran. The remote process may still be running."
			b.EndedAt = now
			st.Touch(it)
		}
		return nil
	})
	if err != nil {
		d.logf("", "ssh: resume: %v", err)
	}
	for _, id := range unrun {
		d.sshEnqueueLola(store, session, "[lola] Your command "+id+" did not run: lola restarted first.")
	}
	var conns []sshstore.Conn
	store.View(func(st *sshstore.State) {
		for _, c := range st.Conns {
			if !c.Terminal() && c.State != protocol.SSHConnRequested {
				conns = append(conns, *c)
			}
		}
	})
	for _, c := range conns {
		d.sshSpawn("resume check", func() { d.sshResumeConn(store, c) })
	}
}

// sshResumeConn gives one surviving connection its one -O check. A connection
// mid-authentication stays that way, because a human may be typing an MFA code into
// a tab that outlived the daemon.
func (d *Daemon) sshResumeConn(store *sshstore.Store, c sshstore.Conn) {
	if c.Sock != "" {
		err := d.ssh.client.Check(d.ssh.ctx, sshTarget(c))
		switch {
		case err == nil:
			d.sshMarkConnected(store, c.ID)
		case errors.Is(err, sshconn.ErrUndetermined):
			// Nobody knows. The monitor asks again.
		case c.State == protocol.SSHConnConnected:
			d.sshMarkDisconnected(store, c.ID)
		}
	}
	d.sshStartMonitor(store, c.ID)
}

// sshSweepLoop is the time-driven half: expiring approvals and grants, and closing
// what config or the session store no longer knows. Memory only, except a close.
func (d *Daemon) sshSweepLoop() {
	t := time.NewTicker(d.ssh.sweepEvery)
	defer t.Stop()
	lastPrune := d.ssh.now()
	for {
		select {
		case <-d.ssh.ctx.Done():
			return
		case <-t.C:
		}
		d.sshSweep(d.ssh.ctx)
		if now := d.ssh.now(); now.Sub(lastPrune) >= sshPruneEvery {
			lastPrune = now
			d.sshPruneStores(now)
		}
	}
}

// sshSweep runs one pass. Exported to tests through its name.
func (d *Daemon) sshSweep(ctx context.Context) {
	now := d.ssh.now()
	cfg := d.sshConfig()
	for _, store := range d.sshStores() {
		var session string
		var expired bool
		var closeIDs []string
		var closeWhy = map[string]string{}
		store.View(func(st *sshstore.State) {
			session = st.Session
			for _, it := range st.Items {
				if it.Kind == protocol.SSHItemBlock && it.Block.Status == protocol.SSHBlockPendingApproval &&
					!it.Block.ApprovalExpiresAt.IsZero() && now.After(it.Block.ApprovalExpiresAt) {
					expired = true
				}
			}
			for _, c := range st.Conns {
				if c.Grant != nil && now.After(c.Grant.ExpiresAt) {
					expired = true
				}
				if c.Terminal() {
					continue
				}
				if cfg.SSHHostByName(c.Host) == nil {
					closeIDs = append(closeIDs, c.ID)
					closeWhy[c.ID] = "This host was removed from the config."
				}
			}
		})
		if _, ok := d.sessions.Get(session); !ok {
			store.View(func(st *sshstore.State) {
				for _, c := range st.LiveConns() {
					if closeWhy[c.ID] == "" {
						closeIDs = append(closeIDs, c.ID)
						closeWhy[c.ID] = "Its session is gone."
					}
				}
			})
		}
		if expired {
			d.sshExpire(store, now)
		}
		for _, id := range closeIDs {
			d.sshCloseConn(ctx, store, id, closeWhy[id])
		}
	}
}

// sshExpire moves what ran out of time. A block nobody answered expires unrun, and
// a grant past its time ends with a recorded fact.
func (d *Daemon) sshExpire(store *sshstore.Store, now time.Time) {
	var ended []string
	err := store.Update(func(st *sshstore.State) error {
		for _, it := range st.Items {
			if it.Kind != protocol.SSHItemBlock || it.Block.Status != protocol.SSHBlockPendingApproval {
				continue
			}
			b := it.Block
			if b.ApprovalExpiresAt.IsZero() || !now.After(b.ApprovalExpiresAt) {
				continue
			}
			b.Status = protocol.SSHBlockExpired
			b.StatusDetail = "Nobody answered in time, so this did not run."
			b.EndedAt = now
			st.Touch(it)
			ended = append(ended, b.ID)
		}
		for _, c := range st.Conns {
			if c.Grant != nil && now.After(c.Grant.ExpiresAt) {
				sshEndGrant(st, c, "expired", now)
			}
		}
		return nil
	})
	if err != nil {
		d.logf("", "ssh: expire: %v", err)
	}
	if len(ended) > 0 {
		var session string
		store.View(func(st *sshstore.State) { session = st.Session })
		for _, id := range ended {
			d.sshNudgeIfGone(store, session, "[lola] Your command "+id+" expired: nobody approved it in time. It did not run.")
		}
	}
}

// sshEndGrant ends a grant and records why. The caller holds the store.
func sshEndGrant(st *sshstore.State, c *sshstore.Conn, why string, now time.Time) {
	if c.Grant == nil {
		return
	}
	c.Grant = nil
	st.AddEvent(protocol.SSHSystemEvent{Kind: protocol.SSHEventGrantEnded, Text: "Grant ended: " + why + ".", Conn: c.ID}, now)
}

// sshPruneStores removes the record of a session gone for RetainAfterGone.
func (d *Daemon) sshPruneStores(now time.Time) {
	root := sshstore.Root(d.home)
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !e.IsDir() || !sshstore.ValidSession(e.Name()) {
			continue
		}
		if _, ok := d.sessions.Get(e.Name()); ok {
			continue
		}
		fi, err := os.Stat(filepath.Join(root, e.Name(), "state.json"))
		if err != nil || now.Sub(fi.ModTime()) < sshstore.RetainAfterGone {
			continue
		}
		if err := os.RemoveAll(filepath.Join(root, e.Name())); err != nil {
			d.logf("", "ssh: prune %s: %v", e.Name(), err)
			continue
		}
		m := d.ssh
		m.mu.Lock()
		delete(m.stores, e.Name())
		for c, s := range m.conns {
			if s == e.Name() {
				delete(m.conns, c)
			}
		}
		delete(m.lastPoll, e.Name())
		m.mu.Unlock()
	}
}

// sshOnReload applies a new config. Policy is read at each decision anyway, so this
// only covers what must change at once: a tier raised to production ends its grants
// and refuses an unsandboxed agent's waiting commands, and the display fields follow.
func (d *Daemon) sshOnReload() {
	cfg := d.sshConfig()
	now := d.ssh.now()
	for _, store := range d.sshStores() {
		// Read the session BEFORE taking the store: the order is sessions, then a
		// store (sshSyncPending holds them that way), and the reverse here wedged
		// the daemon for good.
		id := ""
		store.View(func(st *sshstore.State) { id = st.Session })
		s, known := d.sessions.Get(id)
		err := store.Update(func(st *sshstore.State) error {
			for _, c := range st.Conns {
				h := cfg.SSHHostByName(c.Host)
				if c.Terminal() || h == nil {
					continue
				}
				if known {
					if err := h.SSHAdmit(s.Sandbox != ""); err != nil {
						for _, it := range st.Items {
							if it.Kind != protocol.SSHItemBlock || it.Block.Conn != c.ID || it.Block.Actor != protocol.SSHActorAgent {
								continue
							}
							if it.Block.Status == protocol.SSHBlockPendingApproval || it.Block.Status == protocol.SSHBlockQueued {
								sshRefuseBlock(st, it, protocol.SSHRefuseUnsandboxed, sshClean(err.Error(), sshReasonRunes), now)
							}
						}
					}
				}
				c.Tier, c.Label = h.EffectiveTier(), h.DisplayName()
				// A connection nobody has said go to yet follows the config, so its
				// go card names what would actually be dialed. A live one keeps what
				// its master was started with.
				if c.State == protocol.SSHConnRequested {
					c.Alias, c.User = h.Alias, h.User
					c.Port, c.IdentityFile = h.Port, h.IdentityFile
				}
				c.GrantMaxMinutes = h.EffectiveGrantMaxMinutes()
				c.IdleLingerSeconds = h.EffectiveControlPersist()
				if c.GrantMaxMinutes == 0 && c.Grant != nil {
					sshEndGrant(st, c, "this host no longer allows grants", now)
				}
				st.TouchConn()
			}
			return nil
		})
		if err != nil {
			d.logf("", "ssh: reload: %v", err)
		}
	}
	d.sshSpawn("reload sweep", func() { d.sshSweep(d.ssh.ctx) })
}

// sshClean is the one sanitizer for agent-supplied one-liners on this path.
func sshClean(s string, n int) string { return sshelement.Line(s, n) }
