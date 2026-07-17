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
)

// listFormat is the `tmux ls -F` format: tab-separated name, creation epoch
// seconds, and attached-client count.
const listFormat = "#{session_name}\t#{session_created}\t#{session_attached}"

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
}

// Client shells out to tmux. Bin is an absolute path or "tmux"; a bare name
// is resolved via exec.LookPath (launchd contexts should pass an absolute
// path, see SPEC). SocketName selects the isolated tmux server via "-L"; an
// empty value defaults to "lola" so callers get isolation for free.
type Client struct {
	Bin        string
	SocketName string
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
		if len(fields) != 3 {
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
		sessions = append(sessions, Session{
			Name:     fields[0],
			Created:  time.Unix(created, 0),
			Attached: attached > 0,
		})
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

// NewSession creates a detached session named name running command in dir.
// An empty command starts the default shell.
func (c *Client) NewSession(ctx context.Context, name, dir, command string) error {
	args := []string{"new-session", "-d", "-s", name, "-c", dir}
	if command != "" {
		args = append(args, command)
	}
	_, _, err := c.run(ctx, args...)
	return err
}

// SessionChrome describes the status-bar branding applied by ConfigureSession.
// Brand is the product mark (defaults to "LOLA"); Label identifies the
// issue/session; StatusRight is free-form text shown on the right (e.g. the
// derived agent status). DetachKey, when non-empty (e.g. "F12"), binds a
// single-key detach on the lola server and is surfaced in the status hint;
// empty leaves the default "C-b d". Mouse toggles the session's mouse mode.
type SessionChrome struct {
	Brand       string
	Label       string
	StatusRight string
	DetachKey   string
	Mouse       bool
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
	cmds := [][]string{
		{"set-option", "-t", target, "status", "on"},
		// Defaults truncate to 10 chars; widen so the chrome is not cut off.
		{"set-option", "-t", target, "status-left-length", "80"},
		{"set-option", "-t", target, "status-right-length", "80"},
		{"set-option", "-t", target, "status-left", chromeStatusLeft(opts)},
		{"set-option", "-t", target, "status-right", chromeStatusRight(opts)},
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
