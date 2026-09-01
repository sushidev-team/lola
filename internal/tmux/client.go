// Package tmux is a thin adapter over the tmux CLI. The tmux server — not
// lola — owns the sessions, so they survive lola restarts by design; lola
// only observes them (P1) and later controls them (P2/P3) through this
// client. Session targets always use the "=" prefix so tmux matches names
// exactly instead of by prefix.
//
// Isolation: every command runs against a dedicated tmux server addressed by
// "-L <socket>" (default "lola"), so lola never touches the user's default
// tmux server — they can keep using tmux themselves, and any per-server tweaks
// lola makes (custom key bindings via ConfigureSession) stay on the lola
// socket. One consequence of moving to "-L lola": sessions that predate this
// change live on the OLD default server and are invisible here — this is a
// one-time migration, and adoption (ListSessions) only ever scans the lola
// server.
package tmux

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/sushidev-team/lola/internal/proctree"
)

// listFormat is the `tmux ls -F` format: tab-separated name, creation epoch
// seconds, attached-client count, and last-activity epoch seconds
// (#{session_activity} — tmux's own "when did this session's pane last emit
// bytes" stamp, a zero-cost activity signal the observer feeds the
// anti-false-working guard).
const listFormat = "#{session_name}\t#{session_created}\t#{session_attached}\t#{session_activity}"

// OrphanSessionPrefix is the tmux session-name prefix lola gives every session
// it spawns ("lola-<project>-<identifier>"). The migration guards (daemon +
// doctor) pass it to DefaultServerSessions to find pre-"-L lola" orphans still
// running on the user's DEFAULT tmux server.
const OrphanSessionPrefix = "lola-"

// Session is one line of `tmux ls`.
type Session struct {
	Name     string
	Created  time.Time
	Attached bool
	// Activity is tmux's #{session_activity}: the last time any pane of the
	// session produced output. Zero when the server predates the format field
	// (a mid-upgrade mixed state) — consumers must treat zero as "unknown".
	Activity time.Time
}

// DefaultScrollback is the pane history (tmux `history-limit`) lola gives every
// session on its own server when nothing else is configured. tmux's own default
// is 2000 lines, which an agent burns through in a couple of tool calls — by the
// time anyone scrolls back to read what happened, the interesting part is gone.
const DefaultScrollback = 10000

// Client shells out to tmux. Bin is an absolute path or "tmux"; a bare name
// is resolved via exec.LookPath (launchd contexts should pass an absolute
// path, see SPEC). SocketName selects the isolated tmux server via "-L"; an
// empty value defaults to "lola" so callers get isolation for free.
type Client struct {
	Bin        string
	SocketName string
	// Scrollback is the pane history size ConfigureServer applies to lola's own
	// tmux server; 0 means DefaultScrollback. It is a SERVER-wide default rather
	// than a per-session option because tmux reads history-limit when a pane is
	// CREATED — setting it on a session that already exists changes nothing.
	Scrollback int
	// Mouse mirrors [tmux].mouse: ConfigureServer writes tmux's mouse mode for
	// the whole lola server from it, on OR off, so every session (agent, shell,
	// dev tab, review pane) treats mouse events the same way and the machine's
	// own tmux config cannot decide it.
	Mouse bool
	// Dir is the working directory every tmux command runs from. It matters
	// only for the command that first starts the tmux server, because that
	// process's cwd becomes the SERVER's cwd for its whole lifetime — and the
	// server is long-lived (it outlives daemon restarts). If that cwd is later
	// deleted (e.g. a project/worktree dir that gets removed), every process
	// the server spawns inherits the now-dangling cwd; a Bun-based agent like
	// Claude Code then fails its early-init getcwd() with a bare
	// "ENOENT: Bun could not find a file" and exits before drawing anything,
	// so the tmux session dies the instant it is created. Pin Dir to a stable,
	// always-present directory (lola's Home) so the server can never inherit a
	// doomed cwd. Empty falls back to the user's home, then "/".
	Dir string
}

func (c *Client) bin() string {
	if c.Bin == "" {
		return "tmux"
	}
	return c.Bin
}

// socket is the "-L" server name; empty defaults to "lola" so lola always
// lives on its own tmux server, never the user's default.
func (c *Client) socket() string {
	if c.SocketName == "" {
		return "lola"
	}
	return c.SocketName
}

// dir is the working directory tmux commands run from (see the Dir field). A
// deleted cwd is the specific failure this guards against, so the fallbacks are
// ordered by how certain they are to exist: the configured Dir, then the user's
// home, then "/". os.UserHomeDir never touches the filesystem (it reads $HOME),
// so a dangling process cwd cannot make this fail.
func (c *Client) dir() string {
	if c.Dir != "" {
		return c.Dir
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return home
	}
	return "/"
}

// Available reports whether the tmux binary can be resolved to an
// executable.
func (c *Client) Available() bool {
	_, err := exec.LookPath(c.bin())
	return err == nil
}

// run executes tmux with args, returning stdout and stderr separately so
// callers can inspect stderr (ListSessions' no-server detection) alongside
// the error, which already wraps the trimmed stderr text.
func (c *Client) run(ctx context.Context, args ...string) (stdout, stderr string, err error) {
	// -L keeps every command on lola's isolated tmux server. It is a server
	// flag, so it must precede the tmux subcommand; args[0] (the subcommand)
	// stays intact for the error message below.
	full := append([]string{"-L", c.socket()}, args...)
	cmd := exec.CommandContext(ctx, c.bin(), full...)
	// Pin cwd so the tmux server never inherits (and outlives) a deleted
	// directory — see the Dir field comment.
	cmd.Dir = c.dir()
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	err = cmd.Run()
	if err != nil {
		if msg := strings.TrimSpace(errb.String()); msg != "" {
			err = fmt.Errorf("tmux %s: %w: %s", args[0], err, msg)
		} else {
			err = fmt.Errorf("tmux %s: %w", args[0], err)
		}
	}
	return out.String(), errb.String(), err
}

// ListSessions returns all sessions known to the tmux server. A tmux server
// that is not running (`tmux ls` exits 1 with "no server ..." on stderr) is
// not an error: it means zero sessions, so an empty slice and nil error are
// returned.
func (c *Client) ListSessions(ctx context.Context) ([]Session, error) {
	out, stderr, err := c.run(ctx, "ls", "-F", listFormat)
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && ee.ExitCode() == 1 && strings.Contains(stderr, "no server") {
			return []Session{}, nil
		}
		return nil, err
	}
	sessions := []Session{}
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if line == "" {
			continue
		}
		fields := strings.Split(line, "\t")
		// 3 fields tolerated: an older tmux (or a test fake) without the
		// trailing #{session_activity} column parses with Activity zero.
		if len(fields) != 3 && len(fields) != 4 {
			return nil, fmt.Errorf("tmux ls: unexpected line %q", line)
		}
		created, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("tmux ls: bad session_created in %q: %w", line, err)
		}
		attached, err := strconv.Atoi(fields[2])
		if err != nil {
			return nil, fmt.Errorf("tmux ls: bad session_attached in %q: %w", line, err)
		}
		s := Session{
			Name:     fields[0],
			Created:  time.Unix(created, 0),
			Attached: attached > 0,
		}
		if len(fields) == 4 && fields[3] != "" {
			// Best-effort: a malformed activity stamp degrades to zero
			// ("unknown"), never fails the whole listing.
			if act, err := strconv.ParseInt(fields[3], 10, 64); err == nil && act > 0 {
				s.Activity = time.Unix(act, 0)
			}
		}
		sessions = append(sessions, s)
	}
	return sessions, nil
}

// Has reports whether a session named exactly name exists. It runs through
// c.run, so it carries "-L <socket>" and probes lola's isolated server — the
// TUI's attach pre-check relies on this to confirm a live pane before execing
// a doomed attach.
func (c *Client) Has(ctx context.Context, name string) bool {
	_, _, err := c.run(ctx, "has-session", "-t", "="+name)
	return err == nil
}

// DefaultServerSessions lists sessions on the user's DEFAULT tmux server (NO
// "-L" flag) whose names start with prefix. It is a package function, not a
// *Client method, precisely so it never inherits the SocketName "lola" default
// and can reach the default server the migration guard needs to scan.
//
// This finds pre-"-L lola" orphans: sessions named e.g. "lola-*" still running
// on the default server, invisible to the lola-scoped daemon. A default server
// that is not running (`tmux ls` exits 1 with "no server ..." on stderr) is the
// common healthy case, not an error: empty slice, nil error. bin is the tmux
// binary (empty defaults to "tmux").
func DefaultServerSessions(ctx context.Context, bin, prefix string) ([]string, error) {
	if bin == "" {
		bin = "tmux"
	}
	// Deliberately NO "-L": this targets the user's default tmux server.
	cmd := exec.CommandContext(ctx, bin, "list-sessions", "-F", "#{session_name}")
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	err := cmd.Run()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && ee.ExitCode() == 1 && strings.Contains(errb.String(), "no server") {
			return []string{}, nil
		}
		if msg := strings.TrimSpace(errb.String()); msg != "" {
			return nil, fmt.Errorf("tmux list-sessions: %w: %s", err, msg)
		}
		return nil, fmt.Errorf("tmux list-sessions: %w", err)
	}
	names := []string{}
	for _, line := range strings.Split(strings.TrimRight(out.String(), "\n"), "\n") {
		if line != "" && strings.HasPrefix(line, prefix) {
			names = append(names, line)
		}
	}
	return names, nil
}

// paneTarget builds a target-PANE spec for the session named exactly name: the
// "=" keeps the exact-match safety (no prefix collision), and the trailing ":"
// resolves to the session's active window+pane. capture-pane and send-keys take
// a target-PANE, and a bare "=name" is NOT a valid pane target on tmux (it fails
// with "can't find pane") — the ":" is required. Session-target commands
// (has-session, kill-session) use "=name" without the colon.
func paneTarget(name string) string { return "=" + name + ":" }

// CapturePane returns the rendered screen of the session's active pane,
// including ANSI escape sequences (-e), covering the last lines rows of
// scrollback plus the visible screen.
func (c *Client) CapturePane(ctx context.Context, name string, lines int) (string, error) {
	out, _, err := c.run(ctx, "capture-pane", "-p", "-e", "-t", paneTarget(name), "-S", fmt.Sprintf("-%d", lines))
	if err != nil {
		return "", err
	}
	return out, nil
}

// SendKeys types text into the session literally (-l: no key-name
// interpretation) and then presses Enter.
// submitSettleDelay is the pause between typing a MULTI-LINE payload and the
// separate submit Enter. A large multi-line message (relayed CodeRabbit / review
// findings, a multi-line reaction template) can still be settling in the agent's
// TUI when a back-to-back Enter arrives, so the Enter is swallowed and the text
// sits in the input UNSENT. A short window lets the paste finish rendering before
// the submit. Single-line sends skip it (they submit reliably and stay snappy).
const submitSettleDelay = 600 * time.Millisecond

func (c *Client) SendKeys(ctx context.Context, name, text string) error {
	// Leave copy mode first. A human scrolling the pane back (the app's wheel, or
	// tmux's own wheel binding with [tmux].mouse on) puts it in copy mode, where
	// keys are read as copy-mode COMMANDS instead of reaching the agent — so a
	// reaction or a review hand-off typed into a scrolled-back pane is silently
	// mangled. It is a no-op on a pane that has no mode.
	_ = c.LeaveCopyMode(ctx, name)
	if _, _, err := c.run(ctx, "send-keys", "-t", paneTarget(name), "-l", text); err != nil {
		return err
	}
	if strings.Contains(text, "\n") {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(submitSettleDelay):
		}
	}
	_, _, err := c.run(ctx, "send-keys", "-t", paneTarget(name), "Enter")
	return err
}

// ScrollTransport says how ScrollPane moved a pane's view, because the two
// halves are not interchangeable to the caller: only ScrollCopyMode leaves the
// pane in a mode a later keystroke has to cancel.
type ScrollTransport int

const (
	// ScrollApp handed the wheel to the program running in the pane.
	ScrollApp ScrollTransport = iota
	// ScrollCopyMode moved tmux's own copy-mode view over the pane's history.
	ScrollCopyMode
)

// MaxScrollLines caps one ScrollPane request. Callers coalesce a wheel burst
// into a single call and the count can originate in a UI, so it is bounded here
// rather than trusted.
const MaxScrollLines = 500

// ScrollPane scrolls a session's active pane by lines: positive scrolls BACK
// (up, into history), negative forward again, zero is a no-op. It is the one
// scroll path for both surfaces, and it exists because NEITHER surface has a
// mouse event to hand tmux — the app intercepts the wheel in the webview, the
// TUI reads it as a bubbletea message.
//
// There are two histories, and picking the wrong one is the whole bug this
// replaces. A full-screen program (Claude Code, vim, less) runs on the
// ALTERNATE screen, where tmux keeps NO scrollback at all: copy mode there opens
// on an empty history and reads "[0/0]" — nothing to scroll, whatever the
// history-limit says. Those programs keep their own transcript and ask for the
// wheel themselves (mouse_any_flag), so the scroll has to reach THEM. A plain
// shell asks for nothing and its history is exactly tmux's, so there copy mode
// is the only answer. Which is why the pane is asked first — the same test
// tmux's own wheel binding makes:
//
//	if -F '#{?pane_in_mode,1,#{mouse_any_flag}}' 'send -M' 'copy-mode -e'
//
// The app half writes the SGR wheel sequence straight to the PANE, not through
// tmux's mouse handling, so it works with [tmux].mouse off — that option only
// decides whether tmux itself consumes the events of a real mouse.
func (c *Client) ScrollPane(ctx context.Context, name string, lines int) (ScrollTransport, error) {
	if name == "" {
		return ScrollApp, errors.New("tmux: scroll: empty session name")
	}
	if lines == 0 {
		return ScrollApp, nil
	}
	up := lines > 0
	n := lines
	if !up {
		n = -n
	}
	if n > MaxScrollLines {
		n = MaxScrollLines
	}
	target := paneTarget(name)
	inMode, wantsMouse, w, h := c.paneScrollFacts(ctx, target)
	if !inMode && wantsMouse {
		// One send-keys with the sequence repeated: -l is literal, so tmux passes
		// the bytes to the program untouched instead of parsing them as key names.
		if _, _, err := c.run(ctx, "send-keys", "-l", "-t", target, wheelSeq(up, n, w, h)); err != nil {
			return ScrollApp, err
		}
		return ScrollApp, nil
	}
	verb := "scroll-up"
	if !up {
		verb = "scroll-down"
	}
	// "-e" leaves copy mode again as soon as the view reaches the bottom, so
	// scrolling forward returns the pane to normal with no state to track.
	if _, _, err := c.run(ctx, "copy-mode", "-e", "-t", target, ";",
		"send-keys", "-X", "-N", strconv.Itoa(n), "-t", target, verb); err != nil {
		return ScrollCopyMode, err
	}
	return ScrollCopyMode, nil
}

// paneScrollFacts asks the pane the one question ScrollPane needs, in a single
// exec. It FAILS TOWARD COPY MODE: a pane that cannot be described is most
// likely gone, and copy mode is the half that cannot type anything into a
// program by mistake.
func (c *Client) paneScrollFacts(ctx context.Context, target string) (inMode, wantsMouse bool, width, height int) {
	out, _, err := c.run(ctx, "display-message", "-p", "-t", target,
		"#{pane_in_mode} #{mouse_any_flag} #{pane_width} #{pane_height}")
	if err != nil {
		return false, false, 0, 0
	}
	f := strings.Fields(strings.TrimSpace(out))
	if len(f) < 4 {
		return false, false, 0, 0
	}
	width, _ = strconv.Atoi(f[2])
	height, _ = strconv.Atoi(f[3])
	return f[0] == "1", f[1] == "1", width, height
}

// wheelSeq builds n SGR wheel events (button 64 up / 65 down) aimed at the
// middle of a w×h pane. The position is what a program uses to decide WHICH of
// its regions scrolls, so the centre is the one point that is always inside the
// content; a zero size (the pane could not be measured) falls back to 1;1.
func wheelSeq(up bool, n, w, h int) string {
	btn := 65
	if up {
		btn = 64
	}
	col, row := 1, 1
	if w > 1 {
		col = w / 2
	}
	if h > 1 {
		row = h / 2
	}
	one := fmt.Sprintf("\x1b[<%d;%d;%dM", btn, col, row)
	return strings.Repeat(one, n)
}

// LeaveCopyMode cancels any mode on the session's active pane, returning it to
// the live view. A no-op on a pane that has no mode, so callers do not have to
// ask first.
func (c *Client) LeaveCopyMode(ctx context.Context, name string) error {
	_, _, err := c.run(ctx, "copy-mode", "-q", "-t", paneTarget(name))
	return err
}

// KillSession terminates the session named exactly name. Killing a session
// that does not exist is an error (tmux exits non-zero); callers that want
// idempotence check Has first.
func (c *Client) KillSession(ctx context.Context, name string) error {
	_, _, err := c.run(ctx, "kill-session", "-t", "="+name)
	return err
}

// AttachArgs returns the argv for attaching to the session; the caller (the
// TUI via tea.ExecProcess) execs it itself so tmux takes over the terminal.
// It carries "-L" so the attach targets lola's isolated server, matching every
// other command.
func (c *Client) AttachArgs(name string) []string {
	return []string{c.bin(), "-L", c.socket(), "attach-session", "-t", "=" + name}
}

// scrollback is the resolved pane history size; 0 (unset) means the lola
// default rather than tmux's, so a Client built without config still gets a
// usable amount of history.
func (c *Client) scrollback() int {
	if c.Scrollback > 0 {
		return c.Scrollback
	}
	return DefaultScrollback
}

// ConfigureServer applies lola's own scroll defaults to the whole "-L" server.
// It is the ONE place lola sets a GLOBAL (-g) tmux option: unlike the chrome in
// ConfigureSession, these are exactly the settings every lola session must share
// — the agent pane, its shell tabs, its dev tabs and the review pane are all
// separate sessions, and "scrolling works here but not there" is the bug this
// prevents. Isolation still holds, because the server is lola's own socket.
//
// Setting them here (rather than reading the machine's tmux config) is the
// point: a session spawned by lola scrolls the same on every machine, whatever
// ~/.tmux.conf says. tmux sources that file when the server starts, so lola's
// values land afterwards and win.
//
//   - history-limit is read when a pane is CREATED, so running this before
//     new-session is what gives the new pane its full history. Current tmux also
//     grows an existing pane's history when the option rises, so a late call is
//     not wasted — it just cannot bring back lines already trimmed.
//   - mouse is written on EVERY call, on or off, so [tmux].mouse is the whole
//     truth about it: a machine whose ~/.tmux.conf sets `mouse on` would
//     otherwise hand tmux the clicks in a lola pane (costing one-click links)
//     even though the operator never asked for it. The cost of owning it is
//     that a spawn resets what the TUI's ensureTmuxMouse turned on for its own
//     wheel forwarding — with mouse off, that embed scrolls again only after the
//     TUI re-enables it.
//
// It needs a RUNNING server and deliberately does not try to bootstrap one:
// `start-server` on a cold socket brings a server up and lets it exit again in
// the same breath (a tmux server with no sessions does not linger), taking the
// option with it. On a cold server this therefore fails, and NewSession re-runs
// it once the session exists — see there.
//
// Only options every supported tmux still has belong here (alternate-scroll,
// the obvious third candidate, was dropped by tmux 3.5 and merely logs an
// "invalid option" on every spawn now). Best-effort by contract: it joins
// failures into the returned error for the caller to log, but a server option
// must never fail a spawn.
func (c *Client) ConfigureServer(ctx context.Context) error {
	mouse := "off"
	if c.Mouse {
		mouse = "on"
	}
	cmds := [][]string{
		{"set-option", "-g", "history-limit", strconv.Itoa(c.scrollback())},
		{"set-option", "-g", "mouse", mouse},
	}
	var errs []error
	for _, a := range cmds {
		if _, _, err := c.run(ctx, a...); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// NewSession creates a detached session named name running command in dir.
// An empty command starts the default shell.
//
// The server defaults are applied first, and deliberately on EVERY create: it is
// one cheap set-option, it is the moment tmux reads history-limit for the pane
// about to exist, and the tmux server outlives the daemon — so a process that
// never creates a session cannot be relied on to have configured the server this
// one is creating in.
//
// They are applied a SECOND time when that first attempt failed, which is what
// covers a COLD server: with no tmux running there is nothing to set the option
// on (and no way to pre-start one, see ConfigureServer), so the very first
// session of the day would otherwise be born with tmux's 2000-line default and
// keep it — precisely the "cannot scroll" symptom. The retry lands once the
// session exists; current tmux grows the pane's history to match.
func (c *Client) NewSession(ctx context.Context, name, dir, command string) error {
	// Best-effort: a scroll default must never fail a spawn.
	coldServer := c.ConfigureServer(ctx) != nil
	args := []string{"new-session", "-d", "-s", name, "-c", dir}
	if command != "" {
		args = append(args, command)
	}
	if _, _, err := c.run(ctx, args...); err != nil {
		return err
	}
	if coldServer {
		_ = c.ConfigureServer(ctx)
	}
	return nil
}

// killTreeGrace is how long a pane's process group gets to exit on SIGTERM
// before the remainder is SIGKILLed. It is the window a dev server needs to
// close its listening socket — the whole point of the graceful step.
const killTreeGrace = 3 * time.Second

// PanePID returns the pid of the process running in the session's active pane.
// The target is a target-PANE ("=name:"), so it resolves the session's current
// window rather than requiring an index.
func (c *Client) PanePID(ctx context.Context, name string) (int, error) {
	out, _, err := c.run(ctx, "display-message", "-p", "-t", paneTarget(name), "#{pane_pid}")
	if err != nil {
		return 0, err
	}
	pid, err := strconv.Atoi(strings.TrimSpace(out))
	if err != nil {
		return 0, fmt.Errorf("tmux display-message: unexpected pane_pid %q: %w", strings.TrimSpace(out), err)
	}
	return pid, nil
}

// KillSessionTree kills a session AND every process below its pane, then the
// session itself.
//
// KillSession alone is NOT enough for anything that spawns children. tmux hangs
// the pane's process up (SIGHUP); a child that ignores SIGHUP — `composer dev`'s
// `php artisan serve`, and the `php -S` under it — survives as an orphan of
// pid 1 and keeps its PORT bound. The next session's dev server then quietly
// starts on 8001 instead of 8000, which is the same "find it and kill it by
// hand" problem the dev tabs exist to remove.
//
// The pane's process GROUP covers most of that tree: tmux gives each pane a
// fresh session and process group, so everything the command spawns inherits
// it. But a descendant that deliberately LEFT the group is invisible to a group
// kill, and a coding agent is exactly such a case — Claude Code's Bash tool
// puts every command it runs in its own process group, so a `php artisan serve
// --port=8000` an agent started in its worktree outlives the whole session and
// keeps the port. So the ppid tree is walked too (proctree) and every group it
// spans is signalled together, sharing one grace window.
//
// It DEGRADES to a plain KillSession at every step — an unresolvable pane pid,
// a dead pane (no process left), a `ps` that will not answer, an unsupported
// platform — so a tab is always torn down even when nothing can be signalled.
func (c *Client) KillSessionTree(ctx context.Context, name string) error {
	if pid, err := c.PanePID(ctx, name); err == nil && pid > 1 {
		var groups []int
		if pgid, gerr := proctree.GroupOf(pid); gerr == nil {
			groups = append(groups, pgid)
		}
		if tbl, terr := proctree.Read(ctx); terr == nil {
			groups = append(groups, tbl.TreeGroups(pid)...)
		}
		// Best-effort: the session teardown below runs either way, so a refused
		// signal costs a leaked child, never the tab itself.
		_ = proctree.KillGroups(groups, killTreeGrace)
	}
	// The group kill above BLOCKS through its grace window, and a pane whose
	// whole tree exits promptly takes its tmux session down with it — so the
	// trailing kill-session can race the session it is about to kill and fail
	// with "can't find session". That is success wearing an error's clothes:
	// the contract is "the session and its tree are down", and a session that
	// no longer exists satisfies it. Only an error while the session STILL
	// exists is real.
	if err := c.KillSession(ctx, name); err != nil {
		if c.Has(ctx, name) {
			return err
		}
	}
	return nil
}

// PanePIDs returns the pid of every pane on lola's server, in one exec. It is
// the daemon dev sweep's PROTECT set: a process lola is about to kill for
// squatting on a port must not be one a live pane owns (an agent, a shell tab),
// and a pid is the only thing that ties the two together.
//
// A tmux server that is not running is not an error — no panes, no pids — the
// same shape ListSessions and DeadPanes use.
func (c *Client) PanePIDs(ctx context.Context) ([]int, error) {
	out, stderr, err := c.run(ctx, "list-panes", "-a", "-F", "#{pane_pid}")
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && ee.ExitCode() == 1 && strings.Contains(stderr, "no server") {
			return nil, nil
		}
		return nil, err
	}
	var pids []int
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		pid, cerr := strconv.Atoi(strings.TrimSpace(line))
		if cerr != nil || pid <= 1 {
			continue
		}
		pids = append(pids, pid)
	}
	return pids, nil
}

// PaneProc is one live pane: the pid running in it, and the tmux session that
// owns the pane.
type PaneProc struct {
	Session string
	PID     int
}

// PaneProcs is PanePIDs WITH each pane's owning session name, in one exec. The
// dev take-over's protect set needs the name to tell a user SHELL tab — whose
// pane leads whatever the human started inside it — from an agent pane, which
// leads nothing it must protect (see internal/daemon/dev.go's sweep).
//
// A tmux server that is not running is not an error — no panes, no pids — the
// same shape ListSessions and PanePIDs use.
func (c *Client) PaneProcs(ctx context.Context) ([]PaneProc, error) {
	out, stderr, err := c.run(ctx, "list-panes", "-a", "-F", "#{session_name}\t#{pane_pid}")
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && ee.ExitCode() == 1 && strings.Contains(stderr, "no server") {
			return nil, nil
		}
		return nil, err
	}
	var panes []PaneProc
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		// The pid is the LAST field and session names never carry a tab, so cut
		// at the right-most one rather than trusting the name's contents.
		i := strings.LastIndex(line, "\t")
		if i < 0 {
			continue
		}
		name, pidStr := line[:i], line[i+1:]
		pid, cerr := strconv.Atoi(strings.TrimSpace(pidStr))
		if cerr != nil || pid <= 1 || name == "" {
			continue
		}
		panes = append(panes, PaneProc{Session: name, PID: pid})
	}
	return panes, nil
}

// KeepDeadPane makes the named session OUTLIVE the command it runs: when the
// process exits (cleanly, on a crash, or on a signal), tmux keeps the pane and
// its output instead of destroying the session. The dev tabs use it so a `npm
// run dev` that died at 03:00 still shows why — see DeadPanes for the read side.
//
// remain-on-exit is a WINDOW option, so the target is a target-WINDOW ("=name:",
// the session's current window), not the bare session name tmux rejects with
// "no such window".
func (c *Client) KeepDeadPane(ctx context.Context, name string) error {
	_, _, err := c.run(ctx, "set-option", "-t", "="+name+":", "-w", "remain-on-exit", "on")
	return err
}

// DeadPanes reports, per session name, whether that session's panes have ALL
// exited — true only for a session kept alive by KeepDeadPane whose command is
// gone. One exec answers for the whole server (`list-panes -a`), which is what
// lets the observer decide dev-tab liveness without a per-session probe.
//
// A tmux server that is not running is not an error: nothing is dead because
// nothing is running, so an empty map and nil error come back — the same shape
// ListSessions uses. A session with several panes counts as alive while any one
// of them lives.
func (c *Client) DeadPanes(ctx context.Context) (map[string]bool, error) {
	out, stderr, err := c.run(ctx, "list-panes", "-a", "-F", "#{session_name}\t#{pane_dead}")
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && ee.ExitCode() == 1 && strings.Contains(stderr, "no server") {
			return map[string]bool{}, nil
		}
		return nil, err
	}
	dead := map[string]bool{}
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		name, flag, ok := strings.Cut(line, "\t")
		if !ok || name == "" {
			continue
		}
		if flag == "1" {
			if _, seen := dead[name]; !seen {
				dead[name] = true
			}
			continue
		}
		dead[name] = false // one live pane keeps the whole session alive
	}
	return dead, nil
}

// winTarget is a target-WINDOW spec ("=name:index"): exact session match plus an
// explicit window index. link-window/rename-window/kill-window/select-window all
// take a target-window (unlike the target-pane paneTarget builds).
func winTarget(session string, index int) string {
	return "=" + session + ":" + strconv.Itoa(index)
}

// LinkWindow links window 0 of src into dst at window index — the same window
// object shown in two sessions (not a copy), so dst becomes a live view of src's
// pane. index must be free in dst; the viewer builder assigns contiguous indices
// so it always is. Killing dst later only UNLINKS these windows (tmux destroys a
// window only when its LAST session goes), so tearing the viewer down never
// touches the agent sessions.
func (c *Client) LinkWindow(ctx context.Context, src, dst string, index int) error {
	_, _, err := c.run(ctx, "link-window", "-s", winTarget(src, 0), "-t", winTarget(dst, index))
	return err
}

// RenameWindow renames the window at session:index. On a LINKED window this also
// renames it in the source session (it is one shared object) — a cosmetic effect
// on the agent's own window name, accepted so the viewer's tabs read as the
// issue rather than the running command.
func (c *Client) RenameWindow(ctx context.Context, session string, index int, name string) error {
	_, _, err := c.run(ctx, "rename-window", "-t", winTarget(session, index), name)
	return err
}

// KillWindow removes the window at session:index (used to drop the viewer's
// placeholder shell once real windows are linked in).
func (c *Client) KillWindow(ctx context.Context, session string, index int) error {
	_, _, err := c.run(ctx, "kill-window", "-t", winTarget(session, index))
	return err
}

// SelectWindow makes session:index the active window.
func (c *Client) SelectWindow(ctx context.Context, session string, index int) error {
	_, _, err := c.run(ctx, "select-window", "-t", winTarget(session, index))
	return err
}

// ViewerTab names one window of a viewer session: Session is the agent session
// whose window is linked in, Name the tab label shown for it.
type ViewerTab struct {
	Session string
	Name    string
}

// BuildViewer (re)assembles a read-through "viewer" session named viewer: a
// single attachable session with one window per tab, each a LINK to that agent
// session's live window. Attaching to the viewer and switching windows tabs
// through every agent — the "attach once, see all agents" surface — while each
// agent keeps its own independent session.
//
// The viewer is rebuilt fresh each call (an existing one is killed first) so it
// always reflects the current session set. It starts as a holder with a
// placeholder shell (window 0); real windows are linked at contiguous indices
// 1..N and the placeholder is dropped once at least one linked. A tab whose
// session vanished between listing and linking is skipped, not fatal. With no
// window linkable the half-built viewer is torn down and an error returned, so a
// caller never attaches to an empty shell.
//
// The viewer name must NOT start with the "lola-" agent prefix (see
// OrphanSessionPrefix) or the daemon's Adopt scan would class it as an orphaned
// agent session.
func (c *Client) BuildViewer(ctx context.Context, viewer, dir string, tabs []ViewerTab) error {
	if len(tabs) == 0 {
		return fmt.Errorf("tmux: build viewer: no sessions to view")
	}
	if strings.HasPrefix(viewer, OrphanSessionPrefix) {
		return fmt.Errorf("tmux: build viewer: name %q must not use the %q agent prefix", viewer, OrphanSessionPrefix)
	}
	if c.Has(ctx, viewer) {
		_ = c.KillSession(ctx, viewer)
	}
	if err := c.NewSession(ctx, viewer, dir, ""); err != nil {
		return fmt.Errorf("tmux: build viewer: %w", err)
	}
	linked := 0
	for _, t := range tabs {
		idx := linked + 1 // contiguous 1..N; a skipped tab does not consume an index
		if err := c.LinkWindow(ctx, t.Session, viewer, idx); err != nil {
			continue // the agent session went away between listing and linking
		}
		if t.Name != "" {
			_ = c.RenameWindow(ctx, viewer, idx, t.Name) // cosmetic; never fatal
		}
		linked++
	}
	if linked == 0 {
		_ = c.KillSession(ctx, viewer)
		return fmt.Errorf("tmux: build viewer: no attachable sessions")
	}
	_ = c.KillWindow(ctx, viewer, 0)   // drop the placeholder shell
	_ = c.SelectWindow(ctx, viewer, 1) // open on the first real tab
	return nil
}

// SessionChrome describes the status-bar branding applied by ConfigureSession.
// Brand is the product mark (defaults to "LOLA"); Label identifies the
// issue/session; StatusRight is free-form text shown on the right (e.g. the
// derived agent status). DetachKey, when non-empty (e.g. "F12"), binds a
// single-key detach on the lola server and is surfaced in the status hint;
// empty leaves the default "C-b d". Mouse toggles the session's mouse mode.
//
// StatusBar gates the bar itself. It is OFF by default because lola's own
// surfaces already render the issue, title, status and branch in their own
// header directly above the terminal — the tmux bar restated a subset of that
// one row lower, in a second visual language, and cost a row of scrollback for
// it. Off, the terminal reaches the panel edge and reads as embedded rather than
// framed. Turn it on for attaching in a bare terminal, where nothing else names
// the session. When it is off the branding options are not sent at all: setting
// status-left on a hidden bar is dead configuration.
type SessionChrome struct {
	Brand       string
	Label       string
	StatusRight string
	DetachKey   string
	Mouse       bool
	StatusBar   bool
}

// ConfigureSession applies chrome to a single session on the isolated lola
// server. All set-option calls are PER-SESSION (targeted with -t, never -g),
// so they never leak to other lola sessions; the optional detach bind-key is a
// server key table entry, but because it lives on the "-L lola" socket it
// cannot touch the user's default tmux. Argv is built directly (no shell), so
// spaces in the chrome text are safe.
//
// Best-effort by contract: it attempts every command and joins any failures
// into the returned error so the caller can log it, but a styling failure must
// not fail the spawn — the caller treats a non-nil return as advisory.
func (c *Client) ConfigureSession(ctx context.Context, name string, opts SessionChrome) error {
	target := "=" + name
	// "status off" is still SENT explicitly rather than simply skipped: sessions
	// are adopted across daemon restarts and re-configured in place, so a session
	// that was spawned while the bar was on has to be told to drop it.
	cmds := [][]string{{"set-option", "-t", target, "status", "off"}}
	if opts.StatusBar {
		cmds = [][]string{
			{"set-option", "-t", target, "status", "on"},
			// Defaults truncate to 10 chars; widen so the chrome is not cut off.
			{"set-option", "-t", target, "status-left-length", "80"},
			{"set-option", "-t", target, "status-right-length", "80"},
			{"set-option", "-t", target, "status-left", chromeStatusLeft(opts)},
			{"set-option", "-t", target, "status-right", chromeStatusRight(opts)},
		}
	}
	if opts.Mouse {
		cmds = append(cmds, []string{"set-option", "-t", target, "mouse", "on"})
	}
	if opts.DetachKey != "" {
		// Root-table (-n: no prefix) binding on the lola socket only.
		cmds = append(cmds, []string{"bind-key", "-n", opts.DetachKey, "detach-client"})
	}
	var errs []error
	for _, a := range cmds {
		if _, _, err := c.run(ctx, a...); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// chromeStatusLeft renders the brand and, when present, the label, e.g.
// "LOLA | NORI-12".
func chromeStatusLeft(opts SessionChrome) string {
	brand := opts.Brand
	if brand == "" {
		brand = "LOLA"
	}
	if opts.Label == "" {
		return brand
	}
	return brand + " | " + opts.Label
}

// chromeStatusRight renders the free-form status text (when present) followed
// by the detach hint, e.g. "working | detach F12" or just "detach C-b d".
func chromeStatusRight(opts SessionChrome) string {
	key := opts.DetachKey
	if key == "" {
		key = "C-b d"
	}
	hint := "detach " + key
	if opts.StatusRight == "" {
		return hint
	}
	return opts.StatusRight + " | " + hint
}

// releaseWindowSize hands a window's size back to the clients attached to it.
//
// TWO COMMANDS, IN THIS ORDER, and both halves of that sentence were paid for:
//
//   - Unsetting `window-size` alone does nothing visible. tmux leaves the
//     window at whatever it was last told, so a phone that pinned and walked
//     away would leave the developer squashed until something else resized it.
//     `resize-window -A` is what makes tmux recompute from the clients that are
//     actually attached.
//   - `resize-window -A` SETS `window-size manual` on the window (verified on
//     tmux 3.7c). Unsetting first and recomputing second therefore ends with
//     the option pinned again — the size looked right at that instant, and the
//     window then ignored every client that attached afterwards, which is the
//     stuck-pin failure this whole path exists to prevent, arriving quietly.
//
// So: recompute, THEN unset. The option's absence is the actual release; the
// recompute is what makes it visible now.
func (c *Client) releaseWindowSize(ctx context.Context, target string) error {
	if _, _, err := c.run(ctx, "resize-window", "-t", target, "-A"); err != nil {
		return err
	}
	_, _, err := c.run(ctx, "set-option", "-w", "-t", target, "-u", "window-size")
	return err
}

// SetWindowSize pins a window to an explicit size, or releases it back to
// whatever the attached clients ask for.
//
// This exists for ONE caller: the phone, which wants the pane it is looking at
// sized to a phone while it looks at it. tmux sizes a window from its clients
// (the server runs `window-size latest`, so the most recently active client
// wins), which is why panebus attaches with `-f ignore-size` — a phone joining
// the fan-out must not silently reshape the developer's 200-column view.
// Pinning is the deliberate, temporary opposite of that, and it is scoped to
// the moment the phone has the pane in front of it.
//
// cols == 0 RELEASES, and the release is two commands rather than one because
// unsetting the option alone does nothing at all: tmux leaves the window at
// whatever it was last told, so a phone that disconnected would leave the
// desktop squashed forever. `resize-window -A` is what makes tmux recompute
// from the clients that are actually attached. Both halves are verified against
// tmux 3.7; do not drop the second one.
//
// Best-effort by design: a window that has gone away is not an error worth
// failing a UI over, and the size is cosmetic. The caller logs and continues.
func (c *Client) SetWindowSize(ctx context.Context, name string, cols, rows int) error {
	// A target-WINDOW, not the bare session name — the same trap KeepDeadPane
	// documents, and the one that made this feature do nothing at all.
	//
	// `window-size` is a window option and `resize-window` takes a window, so
	// "=name" is read as an exact WINDOW-name match rather than a session one.
	// Windows are named after the command they run ("zsh", "claude"), so every
	// call answered `no such window: =<session>` and the pin never touched a
	// window in its life — while argv-level tests, which is all a fake tmux can
	// check, passed. "=name:" is the session matched exactly, current window.
	target := "=" + name + ":"
	if cols <= 0 || rows <= 0 {
		return c.releaseWindowSize(ctx, target)
	}
	if _, _, err := c.run(ctx, "set-option", "-w", "-t", target, "window-size", "manual"); err != nil {
		return err
	}
	if _, _, err := c.run(ctx, "resize-window", "-t", target,
		"-x", strconv.Itoa(cols), "-y", strconv.Itoa(rows)); err != nil {
		// A PIN THAT FAILS HALFWAY UNDOES ITSELF, so that a caller told "the pin
		// was refused" can believe nothing is pinned.
		//
		// The two commands are not atomic: setting `window-size manual` alone
		// already stops tmux recomputing the window from its attached clients,
		// so a resize that fails after it leaves the window frozen at whatever
		// size it happened to have — pinned, with nobody holding the pin. The
		// phone's release lifecycle (mobile/src/lib/panepin.ts) reads a refusal
		// the daemon actually ANSWERED as proof that nothing landed and stops
		// tracking the pane, which is only safe because of these two lines.
		//
		// Best-effort, and the failure is deliberately not reported: the caller
		// is already being handed the error that matters, and the undo is the
		// same pair of commands the release path uses, for the same reason —
		// unsetting the option does not resize anything by itself.
		_ = c.releaseWindowSize(ctx, target)
		return err
	}
	return nil
}
