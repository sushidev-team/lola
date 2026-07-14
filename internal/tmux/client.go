// Package tmux is a thin adapter over the tmux CLI. The tmux server — not
// lola — owns the sessions, so they survive lola restarts by design; lola
// only observes them (P1) and later controls them (P2/P3) through this
// client. Session targets always use the "=" prefix so tmux matches names
// exactly instead of by prefix.
package tmux

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// listFormat is the `tmux ls -F` format: tab-separated name, creation epoch
// seconds, and attached-client count.
const listFormat = "#{session_name}\t#{session_created}\t#{session_attached}"

// Session is one line of `tmux ls`.
type Session struct {
	Name     string
	Created  time.Time
	Attached bool
}

// Client shells out to tmux. Bin is an absolute path or "tmux"; a bare name
// is resolved via exec.LookPath (launchd contexts should pass an absolute
// path, see SPEC).
type Client struct{ Bin string }

func (c *Client) bin() string {
	if c.Bin == "" {
		return "tmux"
	}
	return c.Bin
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
	cmd := exec.CommandContext(ctx, c.bin(), args...)
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

// Has reports whether a session named exactly name exists.
func (c *Client) Has(ctx context.Context, name string) bool {
	_, _, err := c.run(ctx, "has-session", "-t", "="+name)
	return err == nil
}

// CapturePane returns the rendered screen of the session's active pane,
// including ANSI escape sequences (-e), covering the last lines rows of
// scrollback plus the visible screen.
func (c *Client) CapturePane(ctx context.Context, name string, lines int) (string, error) {
	out, _, err := c.run(ctx, "capture-pane", "-p", "-e", "-t", "="+name, "-S", fmt.Sprintf("-%d", lines))
	if err != nil {
		return "", err
	}
	return out, nil
}

// SendKeys types text into the session literally (-l: no key-name
// interpretation) and then presses Enter.
func (c *Client) SendKeys(ctx context.Context, name, text string) error {
	if _, _, err := c.run(ctx, "send-keys", "-t", "="+name, "-l", text); err != nil {
		return err
	}
	_, _, err := c.run(ctx, "send-keys", "-t", "="+name, "Enter")
	return err
}

// AttachArgs returns the argv for attaching to the session; the caller (the
// TUI via tea.ExecProcess) execs it itself so tmux takes over the terminal.
func (c *Client) AttachArgs(name string) []string {
	return []string{c.bin(), "attach-session", "-t", "=" + name}
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
