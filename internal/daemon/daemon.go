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
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/sushidev-team/lola/internal/brain"
	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/linear"
	"github.com/sushidev-team/lola/internal/notify"
	"github.com/sushidev-team/lola/internal/review"
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
	Open(ctx context.Context, p config.Project, sessionID, ref, branch string) (session.Session, error)
	Adopt(ctx context.Context) ([]session.Session, error)
	Kill(ctx context.Context, s session.Session, removeWorktree, force bool) error
	Alive(ctx context.Context, s session.Session) bool
	Revive(ctx context.Context, s session.Session) (session.Session, error)
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

	// events is the in-memory activity feed (recent notable status
	// transitions) served to the TUI. Fed via sessions.OnTransition + the
	// spawn site; see events.go.
	events *eventLog

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

	// Reaction-engine seams (PLAN P3.16–19). notifier is rebuilt from cfg in
	// Run and on reload (under d.mu); the rest default to the real tmux/gh
	// clients in newDaemon and are overridden by tests. sendKeys is the ONE way
	// the engine types into a live agent (asserts AtPrompt at the call site);
	// failingChecks / reviewComments fetch the size-bounded reaction content the
	// engine hands the agent.
	notifier       notify.Notifier
	sendKeys       func(ctx context.Context, tmuxName, text string) error
	failingChecks  func(ctx context.Context, repo string, pr int) (string, error)
	reviewComments func(ctx context.Context, repo string, pr int) (string, error)

	// coderabbitComments is the [coderabbit] PR-comment WATCH fetch seam: it
	// returns the reviewer-bot feedback on a PR newer than `since` plus the new
	// watermark (see scm.CodeRabbitComments). Set once in newDaemon (like
	// prForBranch); overridden by tests. Always non-nil — the watch itself is
	// gated by [coderabbit].enabled, not by this seam.
	coderabbitComments func(ctx context.Context, repo string, pr int, since time.Time, author string) (string, time.Time, error)

	// Orchestrator brain (PLAN P5.25): the OPT-IN, bounded, headless-claude
	// SUMMARIZER wired into the EXISTING escalation notify/comment and approved
	// notify paths — it adds no transitions and never enters the control loop.
	// brain is nil when [brain].enabled is false or claude is unavailable;
	// brainSummarize is its exec seam and is nil in exactly the same case, so
	// every call site treats "nil seam" as "use the generic template" — zero
	// behavior change when the brain is off. Rebuilt on reload (setBrainLocked).
	// Tests install a fake brainSummarize directly. paneTail / prDiff are the
	// read-only context-gathering seams whose (attacker-influenceable) output is
	// fed to claude and then shown to a human only — never to send-keys.
	brain          *brain.Client
	brainSummarize func(ctx context.Context, instruction, contextText string) (string, error)
	paneTail       func(ctx context.Context, tmuxName string, lines int) (string, error)
	prDiff         func(ctx context.Context, repo string, pr int) (string, error)

	// QA review buddy (PLAN P9): the OPT-IN, EVENT-TRIGGERED CodeRabbit review
	// pass wired into the observer's PR-open transition (and the manual `lola
	// review` command). It adds NO persistent agent and NO new observed
	// transition — on PR-open it runs one bounded `coderabbit review` and hands
	// the (UNTRUSTED, diff-derived) findings to the human (notify + optional
	// Linear comment) and, sanitized-and-idle-gated, to the worker. review is nil
	// when [review].enabled is false OR coderabbit is unavailable (checked ONCE at
	// startup); reviewRun is its exec seam and is nil in exactly the same case, so
	// every call site treats "nil seam" as "review off" — zero behavior change
	// when the buddy is disabled. Rebuilt on reload (setReviewLocked). Tests
	// install a fake reviewRun directly.
	review    *review.Client
	reviewRun func(ctx context.Context, worktreeDir, baseBranch string) (string, error)

	// reviewCycleCtx is the CURRENT observe cycle's shared review budget: one
	// review timeout for the WHOLE cycle (not per session), derived from
	// shutdownCtx so a slow/hung `coderabbit review` is capped for the cycle and
	// abortable at shutdown (it is read-only and safe to abort). observeNative
	// sets it at cycle start and clears it at the end; runReviewPass reads it
	// (falling back to its own ctx when nil, e.g. the manual command). Guarded by
	// reviewMu.
	reviewMu       sync.Mutex
	reviewCycleCtx context.Context

	// escSummaries carries a brain escalation summary from react's Urgent notify
	// (reactCIFailed) to the P4 blocked Linear comment (writeBackEscalation) in
	// the SAME observe cycle, so one claude call feeds both. Guarded by brainMu;
	// each entry is consumed (deleted) by writeBackEscalation.
	//
	// brainCycleCtx is the CURRENT observe cycle's shared brain budget: one
	// brainTimeout for the WHOLE cycle (not per session), derived from
	// shutdownCtx so a hung `claude -p` is capped for the cycle and abortable at
	// shutdown. observeNative sets it at cycle start and clears it at the end;
	// the summary helpers read it (falling back to their own ctx when nil, e.g.
	// react called directly in tests). Also guarded by brainMu.
	brainMu       sync.Mutex
	escSummaries  map[string]string
	brainCycleCtx context.Context

	// shutdownCtx is the daemon's root context, cancelled on signal/stop. The
	// observe/tick cycles run on WithoutCancel derivatives so their read/write
	// execs FINISH rather than abort; the OPT-IN brain summaries are the one
	// exception — they are read-only and safe to abort, so they derive their
	// bounded context from THIS one (via brainCycleCtx) so a hung claude is
	// killed at shutdown instead of delaying it up to the brain timeout. nil
	// until Run sets it (and in tests) → brain calls fall back to their own ctx.
	shutdownCtx context.Context

	// runtimeHealth is the tick precheck seam (SPEC step 1 successor): it
	// reports whether the native runtime's external tools — tmux, git, and
	// the dispatching context's coding-agent binary (claude|codex|opencode,
	// resolved by the caller and passed in) — are all resolvable, returning
	// an error naming the first missing one. Checked once per tick and by
	// cmd=status; a failing check skips the tick WITHOUT mutating seen/labels
	// (the same discipline as the old AO-down rule). Overridable in tests.
	runtimeHealth func(binary string) error

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
		events:   newEventLog(eventLogCap),

		hookWarned: map[string]bool{},
	}
	// Feed the activity ring from every status transition the store commits
	// (the spawn birth is recorded separately at the dispatch site).
	d.sessions.OnTransition(d.recordSessionEvent)
	d.openPR = d.ghOpenPR
	scmc := &scm.Client{}
	d.prForBranch = scmc.PRForBranch
	d.failingChecks = scmc.FailingChecks
	d.reviewComments = scmc.ReviewComments
	d.coderabbitComments = scmc.CodeRabbitComments
	d.sendKeys = func(ctx context.Context, tmuxName, text string) error {
		return d.tmuxClient().SendKeys(ctx, tmuxName, text)
	}
	// Brain context-gathering seams (P5.25). brain / brainSummarize stay nil
	// here: the brain is built by Run/handleReload from [brain] and left off in
	// tests unless they install a fake seam — so newDaemon alone is a no-brain,
	// generic-template daemon.
	d.paneTail = func(ctx context.Context, tmuxName string, lines int) (string, error) {
		return d.tmuxClient().CapturePane(ctx, tmuxName, lines)
	}
	d.prDiff = scmc.PRDiff
	// A no-op notifier until Run/reload resolves the [notify] config; keeps the
	// engine free of nil checks. notify.New always returns a non-nil Notifier.
	d.notifier = notify.New(notify.NotifyConfig{})
	d.runtimeHealth = checkRuntimeHealth
	return d
}

// checkRuntimeHealth is the production runtimeHealth: the native runtime is
// healthy when tmux is available, git resolves, and the dispatching context's
// coding-agent binary is on PATH. The caller resolves that binary from the
// agent kind (claude|codex|opencode); an empty binary falls back to "claude"
// so a probe with no resolved agent behaves exactly as before. Only LookPath
// probes — nothing is exec'd.
func checkRuntimeHealth(binary string) error {
	if binary == "" {
		binary = "claude"
	}
	if !(&tmux.Client{Bin: "tmux"}).Available() {
		return errors.New("missing tmux")
	}
	if _, err := exec.LookPath("git"); err != nil {
		return errors.New("missing git")
	}
	if _, err := exec.LookPath(binary); err != nil {
		return fmt.Errorf("missing %s", binary)
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
	d.native = newNativeRuntime(cfg, home, lolaBin, d.linearKey, d.nativeLogf)
	d.realNative = true
	// Reaction notifier (PLAN P3.20): resolve the [notify] table into a live
	// desktop/Slack fan-out. Rebuilt on reload (handleReload). The Slack webhook
	// URL is resolved from its env-var name here and never logged.
	d.notifier = notify.New(cfg.ResolveNotify())
	// Orchestrator brain (PLAN P5.25): build the OPT-IN summarizer from [brain].
	// Disabled by default (nil ⇒ generic templates). Check availability ONCE at
	// startup: an operator who enabled the brain but has no claude on PATH gets a
	// single log line and the generic behavior, never a per-cycle error.
	d.mu.Lock()
	d.setBrainLocked(cfg.Brain)
	brainEnabledButMissing := cfg.Brain.Enabled && d.brain == nil
	// QA review buddy (PLAN P9): build the OPT-IN CodeRabbit review client from
	// [review]. Disabled by default (nil ⇒ review off). Check availability ONCE at
	// startup: an operator who enabled the buddy but has no coderabbit on PATH
	// gets a single log line and the pass simply never fires, never a per-cycle
	// error.
	d.setReviewLocked(cfg.Review)
	reviewEnabledButMissing := cfg.Review.Enabled && d.review == nil
	d.mu.Unlock()
	if brainEnabledButMissing {
		logger.Printf("brain: [brain].enabled is true but claude is not available on PATH — using generic notify/comment templates")
	}
	if reviewEnabledButMissing {
		logger.Printf("review: [review].enabled is true but coderabbit is not available on PATH (run: coderabbit auth login) — QA review pass disabled")
	}
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
	// Root context for the OPT-IN brain summaries: unlike the shielded
	// observe/tick execs, a hung `claude -p` is read-only and safe to abort, so
	// it hangs off this shutdown-cancellable ctx (see brainCycleCtx). Set before
	// the observe loop starts so it is always visible to a brain call.
	d.shutdownCtx = ctx
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

	// Migration guard (one-time): warn about lola sessions still running on the
	// user's DEFAULT tmux server from before the "-L lola" isolation change —
	// they are orphaned, invisible to this daemon, and `lola kill` cannot reach
	// them.
	d.warnPreMigrationSessions(ctx)

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

// tmuxClient builds a tmux client on the configured isolated server socket
// (default "lola"), so every daemon-side tmux op — SendKeys, CapturePane — targets
// the same server the sessions actually live on. The socket name is read under
// d.mu because handleReload swaps d.cfg under the same lock; an unlocked read
// races that write (caught by -race). Reading it live also keeps this client on
// the SAME server as the native runtime, which handleReload rebuilds whenever the
// socket name changes — otherwise a socket-only reload would split the observer
// (native runtime) and send-keys/capture (this client) onto two servers. Never
// called while d.mu is held (only via the sendKeys / paneTail seams), so the
// lock cannot deadlock.
func (d *Daemon) tmuxClient() *tmux.Client {
	d.mu.Lock()
	sock := d.cfg.TmuxSocketName()
	d.mu.Unlock()
	return &tmux.Client{Bin: "tmux", SocketName: sock, Dir: d.home}
}

// newNativeRuntime assembles the production native runtime for cfg: worktrees
// under <home>/worktrees, tmux (on cfg's isolated socket) and claude resolved
// via PATH, and Claude Code lifecycle hooks calling back through lolaBin.
// linearKey is the provider forwarding the daemon's currently-resolved Linear
// API key into each spawned session's 0600 .lola/env file (never argv). It is
// re-read per spawn, so a rotated key is picked up next spawn; it returns ""
// when no key is available so spawning is never blocked on it. logf carries
// best-effort styling advisories (status-bar chrome) into the daemon log.
func newNativeRuntime(cfg *config.Config, home, lolaBin string, linearKey func() string, logf func(string, ...any)) *runtime.Native {
	return &runtime.Native{
		Cfg:       cfg,
		WT:        &worktree.Manager{Root: filepath.Join(home, "worktrees")},
		Tmux:      &tmux.Client{Bin: "tmux", SocketName: cfg.TmuxSocketName(), Dir: home},
		LolaBin:   lolaBin,
		Home:      home,
		LinearKey: linearKey,
		Logf:      logf,
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
			// The persisted record is authoritative for the launch discriminator
			// (Kind/Agentless) — Adopt only re-derives it from the ID shape as a
			// store-loss backstop. Carry it forward. An agent-less session
			// (`lola open`, the manual-shell flow) must additionally stay out of the
			// control loop across a restart: keep its "no Linear issue" identity and
			// coerce a scanned "working" back to "shell".
			if prev.Kind != "" {
				s.Kind = prev.Kind
			}
			if prev.Agentless || prev.Manual {
				s.Agentless = true
				s.Manual = prev.Manual
				s.Issue = ""
				if s.Status == "working" {
					s.Status = "shell"
				}
			}
			if s.Branch == "" {
				s.Branch = prev.Branch
			}
			if s.Worktree == "" {
				s.Worktree = prev.Worktree
			}
			if s.Repo == "" {
				s.Repo = prev.Repo
			}
			if s.Issue == "" {
				s.Issue = prev.Issue
			}
			if s.Title == "" {
				s.Title = prev.Title // adopt scans tmux only; the title lives in the persisted record
			}
			if s.IssueUUID == "" {
				s.IssueUUID = prev.IssueUUID
			}
			if s.PollName == "" {
				s.PollName = prev.PollName // preserve the P4 write-back owner
			}
			// Carry forward the P4 write-back one-shot guards so a restart never
			// re-comments a transition already narrated to Linear.
			s.WBSpawnDone = s.WBSpawnDone || prev.WBSpawnDone
			s.WBPRDone = s.WBPRDone || prev.WBPRDone
			s.WBMergedDone = s.WBMergedDone || prev.WBMergedDone
			s.WBBlockedDone = s.WBBlockedDone || prev.WBBlockedDone
			s.PR = prev.PR
			// Carry forward the hook-driven / one-shot state a tmux scan cannot
			// recover. Without this a restart resets AtPrompt to false — and since
			// only a fresh Stop hook re-opens the send-keys gate (which an
			// already-idle adopted agent never fires), every DEFERRED hand-off
			// (reaction / review / coderabbit) would wedge un-delivered — and
			// re-fires the reaction/review/coderabbit one-shots. Preserve the gate,
			// the reaction guards, the review + coderabbit guards, and any stashed
			// (deferred) hand-off + watermark.
			s.AtPrompt = prev.AtPrompt
			s.LastActivityAt = prev.LastActivityAt
			s.LastReactedStatus = prev.LastReactedStatus
			s.CIRetries = prev.CIRetries
			s.Escalated = prev.Escalated
			s.PendingReaction = prev.PendingReaction
			s.ReviewedPR = prev.ReviewedPR
			s.PendingReviewFindings = prev.PendingReviewFindings
			s.LastCodeRabbitAt = prev.LastCodeRabbitAt
			s.PendingCodeRabbit = prev.PendingCodeRabbit
			if len(s.RemovedLabels) == 0 {
				s.RemovedLabels = prev.RemovedLabels
			}
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

// defaultServerSessions is the seam over tmux.DefaultServerSessions so the
// migration warning is testable without a real default tmux server.
var defaultServerSessions = tmux.DefaultServerSessions

// warnPreMigrationSessions logs ONE startup warning naming any "lola-*"
// sessions still running on the user's DEFAULT tmux server (from before the
// "-L lola" isolation change): they are orphaned, invisible to this daemon, and
// `lola kill` cannot reach them. Best-effort — a tmux error (no default server,
// tmux missing) just skips.
func (d *Daemon) warnPreMigrationSessions(ctx context.Context) {
	names, err := defaultServerSessions(ctx, "tmux", tmux.OrphanSessionPrefix)
	if err != nil || len(names) == 0 {
		return
	}
	d.logf("", "migration: %d lola session(s) still running on the DEFAULT tmux server (%s) — these predate the isolated tmux server and are invisible to lola; stop them with: tmux kill-session -t <name>", len(names), strings.Join(names, ", "))
}

// logf prefixes log lines with the poll name when applicable.
func (d *Daemon) logf(poll, format string, args ...any) {
	if poll != "" {
		format = "[" + poll + "] " + format
	}
	d.log.Printf(format, args...)
}

// nativeLogf adapts logf to the runtime.Native.Logf signature (no poll prefix)
// so best-effort spawn advisories — tmux status-bar styling failures — land in
// the daemon log without failing the spawn.
func (d *Daemon) nativeLogf(format string, args ...any) {
	d.logf("", format, args...)
}
