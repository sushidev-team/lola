package main

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/creack/pty"
	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/devtab"
	"github.com/sushidev-team/lola/internal/lolaenv"
	"github.com/sushidev-team/lola/internal/tmux"
)

// TermService bridges lola's isolated tmux server (tmux -L lola) to the webview.
// It serves the sessions overview two ways, matching the WebGL-context ceiling
// (~16 live GPU contexts per page): read-only *snapshots* for the many grid
// tiles (cheap capture-pane, no tmux client, no resize), and a live interactive
// *PTY attach* for the one focused terminal (streamed as coalesced base64 chunks
// the frontend decodes straight into an xterm WebGL instance).
//
// Snapshots never attach a client, so they can never resize or disturb a running
// agent. Attaching does create a tmux client; we size it to the caller's xterm
// and rely on tmux's window-size=latest so a second client (e.g. the TUI embed)
// doesn't thrash the pane.
type TermService struct {
	app     *application.App
	tmuxBin string

	mu      sync.Mutex
	streams map[string]*ptyStream
}

// NewTermService resolves the tmux binary once (PATH is already augmented in
// main) and returns a ready service. A missing tmux is not fatal here — Capture
// and Attach surface it as an error so the UI can show a health hint.
func NewTermService() *TermService {
	bin, _ := exec.LookPath("tmux")
	return &TermService{tmuxBin: bin, streams: map[string]*ptyStream{}}
}

// SetApp injects the Wails emitter. Called once from main before Run.
func (t *TermService) SetApp(app *application.App) { t.app = app }

// tmux returns the resolved tmux binary, resolving it lazily on first use. It is
// called concurrently (CaptureMany fans out), so t.mu guards the cached path; no
// caller holds t.mu when calling in, so this can't re-enter.
func (t *TermService) tmux() (string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.tmuxBin == "" {
		if bin, err := exec.LookPath("tmux"); err == nil {
			t.tmuxBin = bin
		} else {
			return "", errors.New("tmux not found on PATH")
		}
	}
	return t.tmuxBin, nil
}

// childEnv is the environment every tmux child inherits: a real TERM so the
// remote programs render correctly into xterm.js, UTF-8 locale, plus the
// process env (which already carries the augmented PATH).
func childEnv() []string {
	env := os.Environ()
	env = append(env, "TERM=xterm-256color")
	if os.Getenv("LANG") == "" {
		env = append(env, "LANG=en_US.UTF-8")
	}
	return env
}

// --- snapshots (grid tiles) -------------------------------------------------

// Capture returns a read-only snapshot of a session's tmux pane with ANSI
// escapes preserved (capture-pane -p -e), the last `lines` rows (0 → 200). The
// tmux session name is the lola session ID (SessionInfo.TmuxName). This never
// attaches a client, so it cannot resize or disturb the agent.
func (t *TermService) Capture(name string, lines int) (string, error) {
	bin, err := t.tmux()
	if err != nil {
		return "", err
	}
	if name == "" {
		return "", errors.New("empty session name")
	}
	if lines <= 0 {
		lines = 200
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	// "=name:" — exact session match, active pane (see internal/tmux.paneTarget).
	target := "=" + name + ":"
	cmd := exec.CommandContext(ctx, bin, "-L", "lola", "capture-pane", "-p", "-e",
		"-t", target, "-S", "-"+strconv.Itoa(lines))
	cmd.Env = childEnv()
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("capture %s: %w", name, err)
	}
	return string(out), nil
}

// CaptureMany snapshots several panes in one call so the grid refresh is a single
// round-trip from the frontend. Panes are captured concurrently, so the batch is
// bounded by the slowest capture rather than their sum. A pane that fails to
// capture (session gone) is simply omitted from the map rather than failing the
// whole batch.
func (t *TermService) CaptureMany(names []string, lines int) map[string]string {
	out := make(map[string]string, len(names))
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, n := range names {
		wg.Add(1)
		go func(n string) {
			defer wg.Done()
			if s, err := t.Capture(n, lines); err == nil {
				mu.Lock()
				out[n] = s
				mu.Unlock()
			}
		}(n)
	}
	wg.Wait()
	return out
}

// --- auxiliary shell sessions ------------------------------------------------

// shellMarker must appear in every shell tmux name. A shell is a SECOND session
// on the lola server, rooted in the agent's worktree (not a window of the agent's
// session, so the agent pane is never disturbed and the grid keeps showing it).
// The frontend names them "<sessionId>-shell-<n>" — one lola session can have
// any number. This marker is also a guard: Shell / CloseShell refuse a name
// without it, so neither can ever create or kill an agent session by mistake.
const shellMarker = "-shell"

// reviewMarker is the suffix of a session's REVIEW pane ("<id>-review"), the
// tmux session a visible review pass runs in (internal/daemon/reviewvisible.go).
// The daemon owns its lifetime — it opens the pane, and it holds it open after
// the pass so the findings stay readable — so the app never CREATES one; it only
// lists it as a tab beside the shells and may close it like any other tab.
const reviewMarker = "-review"

// Shell ensures the named shell tmux session exists, rooted in worktree, and
// returns its name so the frontend can Attach to it exactly like the agent pane.
// The desktop equivalent of the TUI's shell. The frontend owns the name (and its
// uniqueness); Shell only validates it carries the shell marker. Idempotent: an
// already-running session of that name is reused, so a re-open re-attaches.
func (t *TermService) Shell(shell, worktree string) (string, error) {
	bin, err := t.tmux()
	if err != nil {
		return "", err
	}
	if !strings.Contains(shell, shellMarker) {
		return "", fmt.Errorf("not a shell session name: %q", shell)
	}
	if worktree == "" {
		return "", errors.New("session has no worktree")
	}
	if fi, err := os.Stat(worktree); err != nil || !fi.IsDir() {
		return "", fmt.Errorf("worktree unavailable: %s", worktree)
	}
	if t.hasSession(bin, shell) {
		return shell, nil // reuse — re-attach, don't respawn
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// The scroll defaults FIRST, exactly as the CLI's (*tmux.Client).NewSession
	// applies them (see its doc comment): tmux reads history-limit when a pane is
	// created, and a shell tab can be the session that starts a cold server — a
	// session whose agent pane died takes the whole server with it, and its shell
	// tab would then be born with tmux's 2000-line default.
	coldServer := t.configureServer(ctx, bin) != nil
	// -d creates it detached (the frontend attaches via Attach); the trailing
	// command starts the user's default shell with the worktree's .lola/env
	// exported, the same line the agent pane and the TUI's shell tabs use — a
	// bare login shell would silently lack [[project]].env. No per-session
	// set-option: the shell inherits the same server defaults (mouse, etc.) the
	// agent panes use, so both tabs scroll identically.
	if out, err := exec.CommandContext(ctx, bin, "-L", "lola", "new-session", "-d",
		"-s", shell, "-c", worktree, lolaenv.ShellCommand).CombinedOutput(); err != nil {
		return "", fmt.Errorf("new shell %s: %w: %s", shell, err, out)
	}
	if coldServer {
		_ = t.configureServer(ctx, bin) // nothing existed to configure before; there is now
	}
	return shell, nil
}

// configureServer applies lola's scroll defaults to the lola tmux server, the
// app-side twin of (*tmux.Client).ConfigureServer — same two options, same
// source: [tmux].scrollback and [tmux].mouse, so both surfaces agree and mouse
// mode is decided by config rather than by the machine's ~/.tmux.conf. An
// unreadable config falls back to lola's own defaults rather than leaving
// tmux's. Best-effort — the error is returned only so the caller can tell a COLD
// server (nothing to set an option on yet) from a configured one.
func (t *TermService) configureServer(ctx context.Context, bin string) error {
	lines, mouse := tmux.DefaultScrollback, "off"
	if path, err := config.DefaultPath(); err == nil {
		if cfg, err := config.Load(path); err == nil {
			lines = cfg.Tmux.ScrollbackLines()
			if cfg.Tmux.Mouse {
				mouse = "on"
			}
		}
	}
	return exec.CommandContext(ctx, bin, "-L", "lola",
		"set-option", "-g", "history-limit", strconv.Itoa(lines), ";",
		"set-option", "-g", "mouse", mouse).Run()
}

// hasSession reports whether the lola tmux server already has an exactly-named
// session, so Shell can reuse one instead of spawning a duplicate.
func (t *TermService) hasSession(bin, name string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, bin, "-L", "lola", "has-session", "-t", "="+name).Run() == nil
}

// Shells lists a lola session's auxiliary tmux sessions on the lola server: its
// dev tabs ("<id>-dev-N", the project's dev_commands — the daemon owns their
// lifetime, see internal/daemon/dev.go), then its shells ("<id>-shell-N", sorted
// by their trailing index), then its review pane ("<id>-review") when a visible
// review pass has opened one. Both the app
// and the TUI discover the SAME sessions, so a shell opened in either shows up
// as a tab in the other — and a review the daemon started shows up in both
// without either having to be told. An empty result (or a tmux error) simply
// means no auxiliary sessions.
func (t *TermService) Shells(sessionID string) []string {
	bin, err := t.tmux()
	if err != nil || sessionID == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, "-L", "lola", "list-sessions", "-F", "#{session_name}").Output()
	if err != nil {
		return nil // no server / no sessions
	}
	prefix := sessionID + shellMarker + "-" // "<id>-shell-"
	review := sessionID + reviewMarker      // "<id>-review"
	var names, devs []string
	var reviewPane string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if strings.HasPrefix(line, prefix) {
			names = append(names, line)
			continue
		}
		if devtab.Index(sessionID, line) > 0 {
			devs = append(devs, line)
			continue
		}
		if line == review {
			reviewPane = line
		}
	}
	sort.Slice(names, func(i, j int) bool {
		return shellSessionIndex(sessionID, names[i]) < shellSessionIndex(sessionID, names[j])
	})
	sort.Slice(devs, func(i, j int) bool {
		return devtab.Index(sessionID, devs[i]) < devtab.Index(sessionID, devs[j])
	})
	// Dev tabs sort FIRST (right after the agent): they belong to the project
	// rather than to this session, so they hold their place while shells come
	// and go — and the shell numbering above them never shifts because it is
	// computed among shells alone.
	names = append(devs, names...)
	// The review pane sorts LAST so the shell numbering above it never shifts
	// when a review starts or ends.
	if reviewPane != "" {
		names = append(names, reviewPane)
	}
	return names
}

// shellSessionIndex parses the trailing N from "<id>-shell-N" (0 if absent), so
// shells sort and number stably — the mirror of the TUI's shellIndex.
func shellSessionIndex(id, name string) int {
	n, _ := strconv.Atoi(strings.TrimPrefix(name, id+shellMarker+"-"))
	return n
}

// CloseSessionShells kills every shell tmux session for a lola session — called
// when the session is killed so its shells (rooted in the now-removed worktree)
// don't linger as orphan tabs in either surface. Best-effort.
func (t *TermService) CloseSessionShells(sessionID string) {
	for _, name := range t.Shells(sessionID) {
		_ = t.CloseShell(name)
	}
}

// CloseShell tears down one shell: detach any live stream, then kill its tmux
// session so it doesn't linger after the tab is closed. Idempotent — killing an
// absent session is a no-op. shell is the full "-shell" tmux name; the marker
// guard keeps this from ever killing an agent session.
func (t *TermService) CloseShell(shell string) error {
	bin, err := t.tmux()
	if err != nil {
		return err
	}
	if !strings.Contains(shell, shellMarker) && !strings.HasSuffix(shell, reviewMarker) && !devtab.Is(shell) {
		return fmt.Errorf("not a shell session name: %q", shell)
	}
	_ = t.Detach(shell)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = exec.CommandContext(ctx, bin, "-L", "lola", "kill-session", "-t", "="+shell).Run()
	return nil
}

// --- live PTY attach (focused terminal) -------------------------------------

type ptyStream struct {
	f      *os.File
	cmd    *exec.Cmd
	cancel context.CancelFunc

	mu       sync.Mutex
	pending  []byte
	closed   bool
	detached bool // teardown was frontend-initiated (Detach), so no exit event fires

	// scrollMu serializes everything that moves the pane in or out of tmux copy
	// mode: the enter in Scroll, the flag below, and the cancel in Write/Detach.
	// It is NOT mu (which guards the byte buffer on the read path) because it is
	// held across a tmux exec, and it has to be held that long: Wails dispatches
	// each webview call on its own goroutine, so a keystroke arriving between
	// "Scroll marked the pane scrolled" and "Scroll actually entered copy mode"
	// would consume the flag before there was a mode to leave — and the pane
	// would then sit in copy mode with nothing left to cancel it, swallowing
	// every later keystroke.
	scrollMu sync.Mutex
	// scrolled records that Scroll put this pane into tmux copy mode, so the next
	// keystroke can leave it first (see Write). It is only ever an over-estimate:
	// copy mode can also end on its own (`copy-mode -e` exits at the bottom), and
	// the cancel that clears it is a no-op on a pane that has no mode.
	scrolled bool
}

// Attach starts an interactive `tmux attach` to the named session in a PTY sized
// cols×rows and streams its output to the frontend as base64 chunks on the event
// `pty:<name>`, coalesced to one emit per ~16ms so a chatty agent can't saturate
// the webview. Re-attaching an already-live stream tears the old one down first.
// Returns the stream ID (the session name) the frontend uses for Write/Resize/
// Detach and the event subscription.
func (t *TermService) Attach(name string, cols, rows int) (string, error) {
	bin, err := t.tmux()
	if err != nil {
		return "", err
	}
	if name == "" {
		return "", errors.New("empty session name")
	}
	if t.app == nil {
		return "", errors.New("terminal service not wired to app")
	}
	_ = t.Detach(name) // idempotent re-attach

	if cols <= 0 {
		cols = 80
	}
	if rows <= 0 {
		rows = 24
	}

	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, bin, "-L", "lola", "attach-session", "-t", "="+name)
	cmd.Env = childEnv()
	f, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: clampWinDim(cols), Rows: clampWinDim(rows)})
	if err != nil {
		cancel()
		return "", fmt.Errorf("attach %s: %w", name, err)
	}

	s := &ptyStream{f: f, cmd: cmd, cancel: cancel}
	t.mu.Lock()
	t.streams[name] = s
	t.mu.Unlock()

	go t.readLoop(name, s)
	go t.flushLoop(name, s)
	return name, nil
}

// readLoop pumps PTY bytes into the stream's pending buffer. Buffering raw bytes
// (not a decoded string) keeps multibyte runes intact across read boundaries; the
// flush loop base64-encodes them so the JSON event transport can't corrupt them.
func (t *TermService) readLoop(name string, s *ptyStream) {
	buf := make([]byte, 32*1024)
	for {
		n, err := s.f.Read(buf)
		if n > 0 {
			s.mu.Lock()
			s.pending = append(s.pending, buf[:n]...)
			s.mu.Unlock()
		}
		if err != nil {
			t.endStream(name, s)
			return
		}
	}
}

// flushLoop emits at most one event per ~16ms with everything buffered since the
// last tick — one animation frame's worth of terminal output per pane.
func (t *TermService) flushLoop(name string, s *ptyStream) {
	tick := time.NewTicker(16 * time.Millisecond)
	defer tick.Stop()
	evt := "pty:" + name
	for range tick.C {
		s.mu.Lock()
		if s.closed {
			s.mu.Unlock()
			return
		}
		if len(s.pending) == 0 {
			s.mu.Unlock()
			continue
		}
		chunk := s.pending
		s.pending = nil
		s.mu.Unlock()
		t.app.Event.Emit(evt, base64.StdEncoding.EncodeToString(chunk))
	}
}

// Write forwards keystrokes (xterm onData, always valid UTF-8) to the PTY. A
// write to a stream that has already ended (the terminal exited, tearing itself
// down a frame before xterm's last onData) is a silent no-op, NOT an error: the
// keystroke has nowhere to go and surfacing it would spam the log on every exit.
func (t *TermService) Write(name, data string) error {
	t.mu.Lock()
	s := t.streams[name]
	t.mu.Unlock()
	if s == nil {
		return nil
	}
	// Typing snaps back to the live view, the way every terminal behaves. Without
	// this, a keystroke after a wheel-scroll is swallowed by tmux's copy mode key
	// table (where "q" quits, "/" searches and prose does nothing) instead of
	// reaching the agent — the pane would look wedged. One extra exec, and only
	// for the FIRST key after a scroll: the flag is cleared here, so a normal
	// typing burst costs nothing.
	//
	// The lock is held ACROSS the write, not just the flag: it both makes the
	// cancel and the keystroke one step (a concurrent Scroll cannot slip copy
	// mode in between them) and orders concurrent writes, since each one arrives
	// on its own Wails goroutine.
	s.scrollMu.Lock()
	defer s.scrollMu.Unlock()
	if s.scrolled {
		s.scrolled = false
		t.cancelCopyMode(name)
	}
	_, err := s.f.Write([]byte(data))
	return err
}

// cancelCopyMode leaves copy mode on the pane (a no-op when it has no mode).
// Best-effort: a failure here costs a keystroke's worth of confusion, never the
// keystroke itself, so the caller writes regardless.
func (t *TermService) cancelCopyMode(name string) {
	bin, err := t.tmux()
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = t.scrollClient(bin).LeaveCopyMode(ctx, name)
}

// Scroll moves the named pane's view back through its history: lines > 0 scrolls
// BACK (up), lines < 0 forward again, zero is a no-op. It is the whole reason a
// lola terminal scrolls in the app: `tmux attach` runs on the alternate screen,
// so xterm.js has no scrollback of its own and its fallback for that case turns
// the wheel into cursor keys — which walks the AGENT's input history instead of
// scrolling anything.
//
// The work is (*tmux.Client).ScrollPane's, shared with the TUI, and it is what
// decides WHICH history to move: the program's own (an agent on the alternate
// screen keeps its transcript itself and asks for the wheel) or tmux's copy mode
// (a plain shell). All this needs to know is which one happened, because only
// copy mode leaves a mode behind for the next keystroke to cancel.
func (t *TermService) Scroll(name string, lines int) error {
	bin, err := t.tmux()
	if err != nil {
		return err
	}
	if name == "" {
		return errors.New("empty session name")
	}
	if lines == 0 {
		return nil
	}
	t.mu.Lock()
	s := t.streams[name]
	t.mu.Unlock()
	if s != nil {
		// Held across the exec below, so a keystroke can never consume the flag
		// before the pane is actually in copy mode (see ptyStream.scrollMu).
		s.scrollMu.Lock()
		defer s.scrollMu.Unlock()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	how, err := t.scrollClient(bin).ScrollPane(ctx, name, lines)
	if s != nil {
		s.scrolled = how == tmux.ScrollCopyMode
	}
	if err != nil {
		return fmt.Errorf("scroll %s: %w", name, err)
	}
	return nil
}

// scrollClient is the tmux client the scroll path runs through. The socket is
// the hardcoded "lola" every other exec in this file uses, NOT [tmux].socket_name
// — the app attaches to that server, so a scroll must address the same one.
func (t *TermService) scrollClient(bin string) *tmux.Client {
	return &tmux.Client{Bin: bin, SocketName: "lola"}
}

// Resize propagates an xterm resize to the PTY so the remote app reflows. Like
// Write, a resize aimed at an already-gone stream is a no-op (the ResizeObserver
// can fire once more as the terminal tears down) rather than an error.
func (t *TermService) Resize(name string, cols, rows int) error {
	t.mu.Lock()
	s := t.streams[name]
	t.mu.Unlock()
	if s == nil || cols <= 0 || rows <= 0 {
		return nil
	}
	return pty.Setsize(s.f, &pty.Winsize{Cols: clampWinDim(cols), Rows: clampWinDim(rows)})
}

// clampWinDim narrows a positive terminal dimension to the uint16 pty.Winsize
// uses, capping (not wrapping) anything past the max so a bogus huge cols/rows
// from the frontend can't fold back to a tiny size. Callers guarantee n > 0.
func clampWinDim(n int) uint16 {
	if n > math.MaxUint16 {
		return math.MaxUint16
	}
	return uint16(n)
}

// Detach closes the PTY: the tmux client detaches (the agent keeps running,
// untouched) and the stream's goroutines wind down. Idempotent. Marks the stream
// detached FIRST so the read loop's teardown (unblocked by the close) knows this
// was intentional and suppresses the exit event — detaching a shell tab must NOT
// look like the shell exiting, or switching tabs would kill the shell.
func (t *TermService) Detach(name string) error {
	t.mu.Lock()
	s := t.streams[name]
	delete(t.streams, name)
	t.mu.Unlock()
	if s == nil {
		return nil
	}
	s.mu.Lock()
	s.detached = true
	s.mu.Unlock()
	// Hand the pane back live. Copy mode is a property of the PANE, not of this
	// client, so a terminal closed while scrolled back would leave the agent's
	// output piling up behind a frozen view for whoever looks next.
	s.scrollMu.Lock()
	if s.scrolled {
		s.scrolled = false
		t.cancelCopyMode(name)
	}
	s.scrollMu.Unlock()
	t.closeStream(s)
	return nil
}

// endStream is the read-loop's teardown path (PTY EOF/error): drop the registry
// entry, close, and — unless a Detach set this in motion — emit `pty:<name>:exit`
// so the frontend can retire a tab whose shell exited on its own (e.g. the user
// typed `exit`). The agent pane subscribes to nothing here, so its exit is inert.
func (t *TermService) endStream(name string, s *ptyStream) {
	t.mu.Lock()
	if t.streams[name] == s {
		delete(t.streams, name)
	}
	t.mu.Unlock()
	s.mu.Lock()
	intentional := s.detached
	s.mu.Unlock()
	t.closeStream(s)
	if !intentional && t.app != nil {
		t.app.Event.Emit("pty:"+name+":exit", "")
	}
}

func (t *TermService) closeStream(s *ptyStream) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	s.mu.Unlock()
	s.cancel()
	_ = s.f.Close()
}
