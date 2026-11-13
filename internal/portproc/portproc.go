// Package portproc answers ONE question with lsof: which processes hold a
// listening TCP port, and from which working directory.
//
// The directory is the whole point. lola cannot know what port a project's
// dev_commands bind (the port lives inside `composer dev`, not in config), so a
// squatter can never be found BY port — but it can be found by WHERE it runs.
// A process listening from inside ~/.lola/worktrees/<project>/ is serving a
// lola worktree by definition, which is what lets the dev take-over reclaim a
// port from a server nothing in tmux owns any more (see internal/daemon/dev.go).
//
// One external tool, one exec seam, stdlib otherwise — the same shape as
// internal/tmux and internal/scm. It FAILS OPEN: no lsof, an lsof that errors
// or prints something unexpected, all return nothing rather than a guess,
// because the caller's next act is killing what this reports.
package portproc

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// Listener is one process holding one listening TCP port.
type Listener struct {
	PID     int
	Command string // lsof's short command name ("php", "node")
	Port    int
	Addr    string // the listening address as lsof printed it ("127.0.0.1:8000")
	Dir     string // the process's current working directory ("" when unknown)
}

// Finder runs lsof. The zero value uses the lsof on PATH.
type Finder struct {
	Bin string
	// Exec is the exec seam. nil runs the real binary; tests inject canned
	// lsof output so the parser is tested without a machine to observe.
	Exec func(ctx context.Context, name string, args ...string) ([]byte, error)
}

func (f *Finder) bin() string {
	if f.Bin == "" {
		return "lsof"
	}
	return f.Bin
}

// Available reports whether lsof can be resolved at all. An injected Exec seam
// counts as available (tests do not need the binary).
func (f *Finder) Available() bool {
	if f.Exec != nil {
		return true
	}
	_, err := exec.LookPath(f.bin())
	return err == nil
}

func (f *Finder) run(ctx context.Context, args ...string) ([]byte, error) {
	if f.Exec != nil {
		return f.Exec(ctx, f.bin(), args...)
	}
	cmd := exec.CommandContext(ctx, f.bin(), args...)
	var out, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &stderr
	err := cmd.Run()
	// lsof exits 1 when it simply found nothing to report, which is not a
	// failure — the empty output below says the same thing.
	if err != nil && out.Len() == 0 && stderr.Len() > 0 {
		return nil, fmt.Errorf("lsof: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return out.Bytes(), nil
}

// Listeners lists every listening TCP socket with its owning process and that
// process's working directory. Two execs: one for the sockets, one for the cwds
// of just the pids that turned up.
func (f *Finder) Listeners(ctx context.Context) ([]Listener, error) {
	// -F pcn: machine-readable fields (pid, command, name). -n/-P keep addresses
	// numeric so the port is a number rather than a service name, and -w drops
	// the warnings an unreadable /proc entry would otherwise print.
	out, err := f.run(ctx, "-w", "-nP", "-iTCP", "-sTCP:LISTEN", "-Fpcn")
	if err != nil {
		return nil, err
	}
	listeners := parseListeners(string(out))
	if len(listeners) == 0 {
		return nil, nil
	}

	pids := make([]string, 0, len(listeners))
	seen := map[int]bool{}
	for _, l := range listeners {
		if !seen[l.PID] {
			seen[l.PID] = true
			pids = append(pids, strconv.Itoa(l.PID))
		}
	}
	cwdOut, err := f.run(ctx, "-w", "-a", "-d", "cwd", "-Fpn", "-p", strings.Join(pids, ","))
	if err != nil {
		// The sockets are still worth reporting; a listener with no Dir simply
		// matches no worktree, which is the safe direction.
		return listeners, nil
	}
	dirs := parseDirs(string(cwdOut))
	for i := range listeners {
		listeners[i].Dir = dirs[listeners[i].PID]
	}
	return listeners, nil
}

// parseListeners reads lsof's -F output. Records are line-oriented and stateful:
// a "p<pid>" line opens a process block, "c<command>" names it, and every
// "n<addr>" that follows is one of its sockets.
func parseListeners(out string) []Listener {
	var (
		res     []Listener
		pid     int
		command string
	)
	for _, line := range strings.Split(out, "\n") {
		if len(line) < 2 {
			continue
		}
		value := line[1:]
		switch line[0] {
		case 'p':
			n, err := strconv.Atoi(strings.TrimSpace(value))
			if err != nil {
				pid = 0
				continue
			}
			pid, command = n, ""
		case 'c':
			command = value
		case 'n':
			if pid <= 0 {
				continue
			}
			port, ok := portOf(value)
			if !ok {
				continue
			}
			res = append(res, Listener{PID: pid, Command: command, Port: port, Addr: value})
		}
	}
	return res
}

// parseDirs reads the cwd pass: "p<pid>" then the single "n<path>" for its cwd.
func parseDirs(out string) map[int]string {
	dirs := map[int]string{}
	pid := 0
	for _, line := range strings.Split(out, "\n") {
		if len(line) < 2 {
			continue
		}
		value := line[1:]
		switch line[0] {
		case 'p':
			n, err := strconv.Atoi(strings.TrimSpace(value))
			if err != nil {
				pid = 0
				continue
			}
			pid = n
		case 'n':
			if pid > 0 && dirs[pid] == "" {
				dirs[pid] = value
			}
		}
	}
	return dirs
}

// portOf extracts the port from an lsof address. The forms that matter are
// "127.0.0.1:8000", "*:3000" and IPv6's "[::1]:5175" — so the port is whatever
// follows the LAST colon, and anything else (a socket lsof described some other
// way) is dropped rather than guessed at.
func portOf(addr string) (int, bool) {
	i := strings.LastIndex(addr, ":")
	if i < 0 {
		return 0, false
	}
	port, err := strconv.Atoi(strings.TrimSpace(addr[i+1:]))
	if err != nil || port <= 0 {
		return 0, false
	}
	return port, true
}
