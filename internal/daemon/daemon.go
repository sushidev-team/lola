// Package daemon is the heart of lola: it polls Linear on per-poll tickers,
// dispatches matching issues into native runner sessions (git worktree +
// tmux + Claude Code), reconciles orphans, and serves the unix-socket
// protocol for the TUI/CLI.
package daemon

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"reflect"
	"sync"
	"syscall"
	"time"

	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/linear"
	"github.com/sushidev-team/lola/internal/runtime"
	"github.com/sushidev-team/lola/internal/scm"
	"github.com/sushidev-team/lola/internal/secrets"
	"github.com/sushidev-team/lola/internal/session"
	"github.com/sushidev-team/lola/internal/tmux"
	"github.com/sushidev-team/lola/internal/worktree"
)

// NativeAPI is the daemon's seam over the native runtime (runtime.Native) so
// dispatch, observation, adoption, and kills are testable with fakes. It
// mirrors runtime.Native's exported lifecycle surface.
type NativeAPI interface {
	Spawn(ctx context.Context, p config.Project, issue linear.Issue) (session.Session, error)
	Adopt(ctx context.Context) ([]session.Session, error)
	Kill(ctx context.Context, s session.Session, removeWorktree, force bool) error
	Alive(ctx context.Context, s session.Session) bool
}

var _ NativeAPI = (*runtime.Native)(nil)

// worker is one running poll goroutine. poll/interval are snapshots taken at
// start time and used only for reload diffing; ticks always read the live
// config.
type worker struct {
	poll     config.Poll
	interval time.Duration
	stop     chan struct{}
	done     chan struct{}
}

type Daemon struct {
	log  *log.Logger
	home string

	mu       sync.Mutex
	cfg      *config.Config
	cfgErr   string     // non-empty = cfg failed validation; polls are held
	lin      linear.API // nil until the Linear API key resolves
	linOK    bool
	viewerID string
	// Native runtime (PLAN P2): lola's own worktree+tmux+claude spawner.
	// nil until Run wires the real one; tests inject fakes directly.
	native     NativeAPI
	realNative bool   // native is a *runtime.Native we own (recreate on reload when projects change)
	lolaBin    string // this executable; Claude Code hooks call back via `<lolaBin> hook <event>`
	workers    map[string]*worker

	tickMuMu sync.Mutex
	tickMus  map[string]*sync.Mutex // per-poll tick mutual exclusion

	inflight *inflightSet
	seen     *seenStore
	status   *statusTracker

	// Session observability (PLAN P1): the observer loop's snapshot store.
	sessions *session.Store

	ghWarn sync.Once // "gh not on PATH" is logged once per daemon lifetime

	// hookWarned dedupes "unknown session" hookEvent log lines: hooks fire
	// after every agent turn/tool call, so an untracked session would
	// otherwise flood the daemon log.
	hookWarnMu sync.Mutex
	hookWarned map[string]bool

	cancel context.CancelFunc
	wg     sync.WaitGroup

	// openPR reports whether branch has an open PR in repo ("owner/name",
	// the poll's `repo` config); the error means "could not determine" and
	// callers must fail CLOSED (skip the revert). Overridable in tests;
	// defaults to the gh-based check.
	openPR func(ctx context.Context, repo, branch string) (bool, error)

	// prForBranch returns full PR state for branch in repo ("owner/name") or
	// (nil, nil) when the branch has no PR; the observer's seam over
	// scm.Client. Overridable in tests.
	prForBranch func(ctx context.Context, repo, branch string) (*scm.PR, error)

	// runtimeHealth is the tick precheck seam (SPEC step 1 successor): it
	// reports whether the native runtime's external tools — tmux, git,
	// claude — are all resolvable, returning an error naming the first
	// missing one. Checked once per tick and by cmd=status; a failing check
	// skips the tick WITHOUT mutating seen/labels (the same discipline as
	// the old AO-down rule). Overridable in tests.
	runtimeHealth func() error

	// Socket-initiated tick work (pollOnce) is tracked separately from the
	// worker/reconcile goroutines so graceful shutdown can drain it too.
	shutMu   sync.Mutex
	draining bool
	connWg   sync.WaitGroup
}

func newDaemon(cfg *config.Config, lin linear.API, logger *log.Logger, home string) *Daemon {
	d := &Daemon{
		log:      logger,
		home:     home,
		cfg:      cfg,
		lin:      lin,
		linOK:    lin != nil,
		workers:  map[string]*worker{},
		tickMus:  map[string]*sync.Mutex{},
		inflight: newInflightSet(),
		seen:     newSeenStore(filepath.Join(home, "state")),
		status:   newStatusTracker(),
		sessions: session.NewStore(filepath.Join(home, "state")),

		hookWarned: map[string]bool{},
	}
	d.openPR = d.ghOpenPR
	scmc := &scm.Client{}
	d.prForBranch = scmc.PRForBranch
	d.runtimeHealth = checkRuntimeHealth
	return d
}

// checkRuntimeHealth is the production runtimeHealth: the native runtime is
// healthy when tmux is available, git resolves, and claude is on PATH. Only
// LookPath probes — nothing is exec'd.
func checkRuntimeHealth() error {
	if !(&tmux.Client{Bin: "tmux"}).Available() {
		return errors.New("missing tmux")
	}
	if _, err := exec.LookPath("git"); err != nil {
		return errors.New("missing git")
	}
	if _, err := exec.LookPath("claude"); err != nil {
		return errors.New("missing claude")
	}
	return nil
}

// Run starts the daemon: loads config, starts per-poll goroutines and the
// unix socket server, and blocks until SIGTERM/SIGINT or a stop command.
func Run(ctx context.Context) error {
	home, err := config.Home()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(home, 0o700); err != nil {
		return err
	}

	logFile, err := os.OpenFile(filepath.Join(home, "daemon.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer logFile.Close()
	logger := log.New(io.MultiWriter(os.Stderr, logFile), "", log.LstdFlags)

	cfgPath, err := config.DefaultPath()
	if err != nil {
		return err
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		logger.Printf("config load failed: %v", err)
		return err
	}
	d := newDaemon(cfg, nil, logger, home)

	// Native runtime (PLAN P2): lola's own worktree+tmux+claude spawner. The
	// generated per-session Claude Code settings wire the lifecycle hooks to
	// `<lolaBin> hook <event>`, so LolaBin must be THIS executable.
	lolaBin, err := os.Executable()
	if err != nil {
		logger.Printf("resolve lola binary (session hooks fall back to PATH lookup): %v", err)
		lolaBin = "lola"
	}
	d.lolaBin = lolaBin
	d.native = newNativeRuntime(cfg, home, lolaBin, d.linearKey)
	d.realNative = true
	if err := cfg.Validate(); err != nil {
		// Not fatal: the daemon stays up so status/reload can surface and
		// fix it — but polls are HELD (never ticked). Reload rejects the
		// same config, and running e.g. a label-mode poll whose flip labels
		// were hand-deleted would re-spawn issues hourly.
		logger.Printf("config invalid, polls held until a valid config is reloaded: %v", err)
		d.cfgErr = "config invalid: " + err.Error()
	}

	if _, err := d.ensureLinear(); err != nil {
		// Keep the daemon alive; status reports linearOk=false. Never log
		// the key itself — secrets errors only name the sources tried.
		logger.Printf("linear API key unavailable: %v", err)
	}

	sock := filepath.Join(home, "lola.sock")
	ln, err := claimSocket(sock)
	if err != nil {
		logger.Printf("%v", err)
		return err
	}

	ctx, cancel := context.WithCancel(ctx)
	d.cancel = cancel
	defer cancel()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT)
	defer signal.Stop(sig)
	go func() {
		select {
		case s := <-sig:
			logger.Printf("signal %v: shutting down", s)
			cancel()
		case <-ctx.Done():
		}
	}()

	// Session adoption (PLAN P2.15): re-pair surviving tmux sessions and
	// worktrees from a previous daemon into the store BEFORE the first tick,
	// so adopted sessions count against the cap right away.
	d.adoptNativeSessions(ctx)

	d.syncWorkers(ctx)

	d.wg.Add(1)
	go d.reconcileLoop(ctx)

	d.wg.Add(1)
	go d.observeLoop(ctx)

	go d.serve(ctx, ln)

	logger.Printf("daemon started (socket %s)", sock)
	<-ctx.Done()

	// Graceful shutdown: stop tickers, wait for in-flight ticks (worker,
	// reconcile AND socket-initiated pollOnce) to finish, close the socket,
	// remove the socket file, exit nil. Ticks run on a context shielded from
	// this cancellation (see safeTick), so they finish rather than abort.
	// Native tmux sessions are deliberately NOT killed here: the tmux server
	// owns them and they survive lola restarts by design — the next daemon
	// re-adopts them (adoptNativeSessions).
	d.stopAllWorkers()
	d.wg.Wait()
	d.drainConnWork()
	ln.Close()
	os.Remove(sock)
	logger.Printf("daemon stopped")
	return nil
}

// claimSocket binds the daemon's unix socket. If another live daemon is
// already serving it, starting a second instance is refused — silently
// stealing the socket would leave two daemons polling (and double-spawning)
// concurrently. Only a stale socket file (nothing accepting) is removed.
func claimSocket(sock string) (net.Listener, error) {
	if conn, err := net.DialTimeout("unix", sock, time.Second); err == nil {
		conn.Close()
		return nil, fmt.Errorf("another lola daemon is already serving %s (stop it first)", sock)
	}
	_ = os.Remove(sock) // stale socket from a previous run
	ln, err := net.Listen("unix", sock)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(sock, 0o600); err != nil {
		ln.Close()
		os.Remove(sock)
		return nil, err
	}
	return ln, nil
}

// beginConnWork registers socket-initiated tick work so shutdown waits for
// it; returns false once draining has begun (the work must be refused).
func (d *Daemon) beginConnWork() bool {
	d.shutMu.Lock()
	defer d.shutMu.Unlock()
	if d.draining {
		return false
	}
	d.connWg.Add(1)
	return true
}

func (d *Daemon) drainConnWork() {
	d.shutMu.Lock()
	d.draining = true
	d.shutMu.Unlock()
	d.connWg.Wait()
}

// ensureLinear returns the Linear client, resolving the API key on demand so
// the daemon recovers once a missing key is provisioned.
func (d *Daemon) ensureLinear() (linear.API, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.lin != nil {
		return d.lin, nil
	}
	key, err := secrets.LinearAPIKey(d.cfg.Linear.APIKeyKeychain, d.cfg.Linear.APIKeyEnv)
	if err != nil {
		d.linOK = false
		return nil, err
	}
	d.lin = linear.New(d.cfg.Linear.Endpoint, key)
	d.linOK = true
	return d.lin, nil
}

// linearKey resolves the current Linear API key for forwarding into a native
// session's 0600 .lola/env file. It re-reads the configured source (Keychain >
// env) on every call so a rotated key is picked up on the next spawn, and
// returns "" on any error so spawning is NEVER blocked on secret resolution —
// the session then simply gets no LINEAR_API_KEY. The key is only ever a return
// value here; it is never cached on the native runtime as a plain string.
func (d *Daemon) linearKey() string {
	d.mu.Lock()
	kc, env := d.cfg.Linear.APIKeyKeychain, d.cfg.Linear.APIKeyEnv
	d.mu.Unlock()
	key, err := secrets.LinearAPIKey(kc, env)
	if err != nil {
		return ""
	}
	return key
}

func (d *Daemon) setLinearOK(ok bool) {
	d.mu.Lock()
	d.linOK = ok
	d.mu.Unlock()
}

// invalidateLinear drops the cached client on an auth failure so the next
// ensureLinear re-resolves the API key (Keychain > env). Without this a
// rotated/revoked key would be reused until a full daemon restart.
func (d *Daemon) invalidateLinear() {
	d.mu.Lock()
	d.lin = nil
	d.linOK = false
	d.mu.Unlock()
}

// viewer resolves and caches viewer.id (needed for assignee_mode=me).
func (d *Daemon) viewer(ctx context.Context, api linear.API) (string, error) {
	d.mu.Lock()
	v := d.viewerID
	d.mu.Unlock()
	if v != "" {
		return v, nil
	}
	u, err := api.Viewer(ctx)
	if err != nil {
		return "", err
	}
	d.mu.Lock()
	d.viewerID = u.ID
	d.mu.Unlock()
	return u.ID, nil
}

// intervalLocked returns the effective poll interval; caller holds d.mu.
// config.Load already clamps, but reload/enable paths re-enforce the 30s floor.
func (d *Daemon) intervalLocked() time.Duration {
	iv := d.cfg.Defaults.PollInterval
	if iv == 0 {
		iv = config.DefaultPollInterval
	}
	if iv < config.MinPollInterval {
		iv = config.MinPollInterval
	}
	return iv
}

// syncWorkers reconciles running poll goroutines with the current config:
// starts new/newly-enabled polls, stops removed/disabled ones, restarts
// changed ones (or all, when the interval changed), leaves the rest alone.
func (d *Daemon) syncWorkers(ctx context.Context) {
	d.mu.Lock()
	iv := d.intervalLocked()
	desired := map[string]config.Poll{}
	if d.cfgErr == "" { // invalid config: hold ALL polls (status surfaces why)
		for _, p := range d.cfg.Polls {
			if p.Enabled {
				desired[p.Name] = p
			}
		}
	}
	var toStop []*worker
	for name, w := range d.workers {
		p, ok := desired[name]
		if ok && w.interval == iv && reflect.DeepEqual(w.poll, p) {
			delete(desired, name) // unchanged: leave running
			continue
		}
		toStop = append(toStop, w)
		delete(d.workers, name)
	}
	d.mu.Unlock()

	// Wait for stopped workers outside d.mu: a mid-tick worker briefly
	// takes d.mu for its config snapshot.
	for _, w := range toStop {
		close(w.stop)
		<-w.done
	}

	d.mu.Lock()
	for name, p := range desired {
		if _, exists := d.workers[name]; exists {
			continue
		}
		d.startWorkerLocked(ctx, name, p, iv)
	}
	d.mu.Unlock()
}

func (d *Daemon) startWorkerLocked(ctx context.Context, name string, p config.Poll, iv time.Duration) {
	w := &worker{poll: p, interval: iv, stop: make(chan struct{}), done: make(chan struct{})}
	d.workers[name] = w
	d.wg.Add(1)
	go d.pollLoop(ctx, name, w)
}

func (d *Daemon) stopAllWorkers() {
	d.mu.Lock()
	ws := d.workers
	d.workers = map[string]*worker{}
	d.mu.Unlock()
	for _, w := range ws {
		close(w.stop)
		<-w.done
	}
}

func (d *Daemon) pollLoop(ctx context.Context, name string, w *worker) {
	defer d.wg.Done()
	defer close(w.done)
	t := time.NewTicker(w.interval)
	defer t.Stop()
	d.safeTick(ctx, name) // immediate first tick
	for {
		select {
		case <-w.stop:
			return
		case <-ctx.Done():
			return
		case <-t.C:
			d.safeTick(ctx, name)
		}
	}
}

// safeTick runs one real tick under the poll's mutex; a poll error or panic
// never crashes the daemon — it surfaces via status and the log.
func (d *Daemon) safeTick(ctx context.Context, name string) {
	defer func() {
		if r := recover(); r != nil {
			d.logf(name, "tick panic (daemon keeps running): %v", r)
			d.status.setError(name, fmt.Sprintf("tick panic: %v", r))
		}
	}()
	mu := d.tickMutex(name)
	mu.Lock()
	defer mu.Unlock()
	// Shutdown cancels ctx to stop the poll loops, but an in-flight tick
	// must FINISH, not abort (agent-rules "Daemon"): a cancelled context
	// would SIGKILL a running native spawn and abort the post-spawn label
	// flip, corrupting dedup state. Run waits for us via d.wg.
	_, _ = d.tick(context.WithoutCancel(ctx), name, false) // tick logs and records its own errors
}

// tickMutex returns the per-poll mutex ensuring a tick never runs twice
// concurrently for one poll (ticker vs pollOnce).
func (d *Daemon) tickMutex(name string) *sync.Mutex {
	d.tickMuMu.Lock()
	defer d.tickMuMu.Unlock()
	m, ok := d.tickMus[name]
	if !ok {
		m = &sync.Mutex{}
		d.tickMus[name] = m
	}
	return m
}

// newNativeRuntime assembles the production native runtime for cfg: worktrees
// under <home>/worktrees, tmux and claude resolved via PATH, and Claude Code
// lifecycle hooks calling back through lolaBin.
// linearKey is the provider forwarding the daemon's currently-resolved Linear
// API key into each spawned session's 0600 .lola/env file (never argv). It is
// re-read per spawn, so a rotated key is picked up next spawn; it returns ""
// when no key is available so spawning is never blocked on it.
func newNativeRuntime(cfg *config.Config, home, lolaBin string, linearKey func() string) *runtime.Native {
	return &runtime.Native{
		Cfg:       cfg,
		WT:        &worktree.Manager{Root: filepath.Join(home, "worktrees")},
		Tmux:      &tmux.Client{Bin: "tmux"},
		LolaBin:   lolaBin,
		Home:      home,
		LinearKey: linearKey,
	}
}

// adoptNativeSessions is the restart-recovery scan (PLAN P2.15): every finding
// from runtime.Adopt is upserted into the session store — live pairs as
// "working", worktrees without a pane as "dead", panes without a worktree as
// "orphaned" — and anomalies are reported to the log. Facts observation cannot
// recover (branch, repo, issue UUID, PR state) are preserved from a previously
// persisted record. Adoption never kills sessions or removes worktrees; acting
// on dead/orphaned candidates is the reconcile pass's decision.
func (d *Daemon) adoptNativeSessions(ctx context.Context) {
	d.mu.Lock()
	nat := d.native
	d.mu.Unlock()
	if nat == nil {
		return
	}
	found, err := nat.Adopt(ctx)
	if err != nil {
		d.logf("", "adopt: native session scan failed: %v", err)
		return
	}
	for _, s := range found {
		if prev, ok := d.sessions.Get(s.ID); ok {
			if s.Branch == "" {
				s.Branch = prev.Branch
			}
			if s.Repo == "" {
				s.Repo = prev.Repo
			}
			if s.Issue == "" {
				s.Issue = prev.Issue
			}
			if s.IssueUUID == "" {
				s.IssueUUID = prev.IssueUUID
			}
			s.PR = prev.PR
			if s.Status == "dead" && prev.Status == "merged" {
				// A merged session's pane going away is the expected end of
				// life, not an anomaly worth resurrecting as "dead".
				s.Status = "merged"
			}
		}
		if s.TmuxName == "" {
			s.TmuxName = s.ID // native sessions ARE tmux sessions
		}
		switch s.Status {
		case "dead":
			d.logf("", "adopt: %s has a worktree but no tmux session (dead; reconcile may revert its issue)", s.ID)
		case "orphaned":
			d.logf("", "adopt: %s is a lola tmux session without a worktree (orphaned; kill candidate)", s.ID)
		}
		d.sessions.Upsert(s)
	}
	if len(found) == 0 {
		return
	}
	d.logf("", "adopt: %d native session(s) recorded", len(found))
	if err := d.sessions.Save(); err != nil {
		d.logf("", "adopt: persist sessions: %v", err)
	}
}

// logf prefixes log lines with the poll name when applicable.
func (d *Daemon) logf(poll, format string, args ...any) {
	if poll != "" {
		format = "[" + poll + "] " + format
	}
	d.log.Printf(format, args...)
}
