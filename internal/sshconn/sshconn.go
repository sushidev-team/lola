// Package sshconn is the one place lola builds an ssh argv and runs ssh. It owns
// the exact arguments (docs/ssh-sessions.md section 4), the control socket's path
// and directory checks, safe shell quoting, and the exec seam the daemon drives.
//
// It never interprets what a remote command printed, and it never reads the
// human's authentication: the master runs in a tmux tab that this package only
// describes. The alias, user and socket come from config and the daemon. Agent
// text reaches ssh in exactly one place, inside the single quoted remote command.
package sshconn

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/sushidev-team/lola/internal/sshterm"
)

// MaxSocketPath is the longest control path lola will hand to ssh. sun_path is
// 104 bytes on macOS, and OpenSSH binds a temporary name about 17 bytes longer
// before renaming it into place, so the margin is deliberate.
const MaxSocketPath = 80

// Bounds on the short control execs.
const (
	ControlTimeout = 5 * time.Second
	stopGrace      = 2 * time.Second
	pipeGrace      = 2 * time.Second
)

// Target names one connection, as config and the daemon know it.
type Target struct {
	Sock         string
	Alias        string
	User         string
	Port         int
	IdentityFile string
}

var (
	aliasRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
	userRe  = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.-]*$`)
)

func validDestination(s string) bool {
	return (len(s) <= 253 && aliasRe.MatchString(s)) || net.ParseIP(s) != nil
}

// Valid refuses a target that could read as an ssh option or carry a user past
// the catalog. Config validation already says so, but a validation error does not
// stop a daemon, so the argv builder holds the line as well.
func (t Target) Valid() error {
	if !validDestination(t.Alias) {
		return fmt.Errorf("ssh alias %q is not valid", t.Alias)
	}
	if t.User != "" && !userRe.MatchString(t.User) {
		return fmt.Errorf("ssh user %q is not valid", t.User)
	}
	if t.Port < 0 || t.Port > 65535 {
		return fmt.Errorf("SSH port must be in 1..65535")
	}
	if t.IdentityFile != "" && (!(filepath.IsAbs(t.IdentityFile) || strings.HasPrefix(t.IdentityFile, "~/")) || strings.IndexFunc(t.IdentityFile, unicode.IsControl) >= 0) {
		return fmt.Errorf("identity file must be an absolute or ~/ path")
	}
	return nil
}

func (t Target) connectionArgs() []string {
	var a []string
	if t.User != "" {
		a = append(a, "-l", t.User)
	}
	if t.Port != 0 {
		a = append(a, "-p", strconv.Itoa(t.Port))
	}
	if t.IdentityFile != "" {
		a = append(a, "-i", t.IdentityFile)
	}
	return a
}

// MasterArgs is the master, run in the visible tab. The human authenticates
// there. ControlPersist backgrounds the master, so it outlives their shell, and a
// time value bounds a master lola fails to close.
func MasterArgs(t Target, persistSeconds int) []string {
	a := []string{
		"-M", "-S", t.Sock,
		"-o", "ControlPersist=" + strconv.Itoa(persistSeconds),
		"-o", "ForwardAgent=no", "-o", "ForwardX11=no",
		"-o", "ClearAllForwardings=yes", "-o", "PermitLocalCommand=no",
		"-o", "ServerAliveInterval=30", "-o", "ServerAliveCountMax=3",
	}
	return append(append(a, t.connectionArgs()...), t.Alias)
}

// CommandArgs is one command over an exec channel on the master.
//
// ProxyCommand=false is what makes this fail closed. With a missing socket and
// ControlMaster=no, ssh does not give up: it tries a fresh connection, and with
// keys in an agent BatchMode would not stop that. That would be a connection
// nobody gave the go for. A proxy that fails at once closes the path.
func CommandArgs(t Target, remote string) []string {
	a := []string{
		"-S", t.Sock,
		"-o", "ControlMaster=no", "-o", "BatchMode=yes", "-o", "ProxyCommand=false",
		"-o", "RemoteCommand=none", "-o", "RequestTTY=no",
		"-o", "ForwardAgent=no", "-o", "ForwardX11=no",
		"-o", "ClearAllForwardings=yes", "-o", "PermitLocalCommand=no", "-o", "LogLevel=ERROR",
	}
	a = append(a, t.connectionArgs()...)
	return append(a, "-T", t.Alias, remote)
}

// ReopenArgs is a new interactive shell on an existing master, after the human
// left their first one. It cannot open a fresh connection, for the same reason as
// CommandArgs.
func ReopenArgs(t Target) []string {
	a := []string{
		"-S", t.Sock, "-o", "ControlMaster=no", "-o", "ProxyCommand=false",
		// A mux client asks the master to open what the user's config lists, so these
		// have to be repeated or a reopen brings forwards the master refused.
		"-o", "ClearAllForwardings=yes", "-o", "ForwardAgent=no", "-o", "ForwardX11=no",
	}
	return append(append(a, t.connectionArgs()...), t.Alias)
}

// CheckArgs asks whether the master answers. Exit 0 means up.
func CheckArgs(t Target) []string { return []string{"-S", t.Sock, "-O", "check", t.Alias} }

// ExitArgs closes the master.
func ExitArgs(t Target) []string { return []string{"-S", t.Sock, "-O", "exit", t.Alias} }

// ResolveArgs is `ssh -G`, which prints the configuration and never connects.
func ResolveArgs(alias, user string) []string {
	return append(append([]string{"-G"}, Target{User: user}.connectionArgs()...), alias)
}

// ShQuote is POSIX single-quoting.
func ShQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// RemoteCommand is the single argument ssh sends to the remote side. The login
// shell may not be POSIX, so `sh -c` is explicit, and the working directory is
// quoted so it can never become part of the command. The cd is its own statement
// and ends the shell when it fails: "cd X && a; b" guards only a, and b would run in
// the login directory under an approval that named X.
func RemoteCommand(cwd, command string) string {
	body := command
	if cwd != "" {
		body = "cd -- " + ShQuote(cwd) + " || exit 1\n" + command
	}
	return "sh -c " + ShQuote(body)
}

// RemoteCommandTracked is RemoteCommand plus a report of the directory the command
// ended in, for sshterm.CwdFilter to take out of stderr.
func RemoteCommandTracked(cwd, command, nonce string) string {
	body := command
	if cwd != "" {
		body = "cd -- " + ShQuote(cwd) + " || exit 1\n" + command
	}
	return "sh -c " + ShQuote(sshterm.Tracked(nonce, body))
}

// WithShell makes an interactive argv (MasterArgs, ReopenArgs) start this script instead
// of the login shell. RemoteCommand=none is needed because ssh refuses a command when
// the user's config also sets one, and a command gets no TTY unless it is forced.
func WithShell(args []string, script string) []string {
	n := len(args) - 1
	out := append([]string{}, args[:n]...)
	out = append(out, "-o", "RemoteCommand=none", "-o", "RequestTTY=force", args[n])
	return append(out, "sh -c "+ShQuote(script))
}

// ShellLine renders bin + args as one quoted shell line for a tmux pane.
func ShellLine(bin string, args []string) string {
	var b strings.Builder
	b.WriteString(ShQuote(bin))
	for _, a := range args {
		b.WriteByte(' ')
		b.WriteString(ShQuote(a))
	}
	return b.String()
}

// NewConnID mints a connection id: "c" plus 12 hex characters, short on purpose
// because it lands in a socket path.
func NewConnID() (string, error) {
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return "c" + hex.EncodeToString(b[:]), nil
}

var connIDRe = regexp.MustCompile(`^c[0-9a-f]{12}$`)

// ValidConnID reports whether id has the shape NewConnID produces. A connection
// id is a path segment, so nothing else ever reaches SocketPath.
func ValidConnID(id string) bool { return connIDRe.MatchString(id) }

// SocketPath returns the control socket for a connection, creating and verifying
// its directory. The normal place is <home>/ssh/<conn>. A home too long for
// MaxSocketPath, or containing % (which ssh would expand as a token), falls back
// to /tmp/lola-ssh-<uid>/<conn>, and any directory that fails verification is an
// error: a socket another user could reach is a live authenticated connection,
// and ssh checks nothing about the directory itself.
func SocketPath(home, conn string) (string, error) {
	return socketPath(home, conn, fmt.Sprintf("/tmp/lola-ssh-%d", os.Getuid()))
}

func socketPath(home, conn, fallback string) (string, error) {
	if !ValidConnID(conn) {
		return "", fmt.Errorf("connection id %q is not valid", conn)
	}
	dir := filepath.Join(home, "ssh")
	if strings.Contains(dir, "%") || len(filepath.Join(dir, conn)) > MaxSocketPath {
		dir = fallback
	}
	path := filepath.Join(dir, conn)
	if len(path) > MaxSocketPath {
		return "", fmt.Errorf("control socket path %s is too long for ssh", path)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("control socket directory: %w", err)
	}
	if err := verifySocketDir(dir); err != nil {
		return "", err
	}
	return path, nil
}

func verifySocketDir(dir string) error {
	fi, err := os.Lstat(dir)
	if err != nil {
		return fmt.Errorf("control socket directory: %w", err)
	}
	if !fi.IsDir() {
		return fmt.Errorf("control socket directory %s is not a directory", dir)
	}
	if fi.Mode().Perm() != 0o700 {
		return fmt.Errorf("control socket directory %s has mode %o, it must be 700", dir, fi.Mode().Perm())
	}
	if !ownedByMe(fi) {
		return fmt.Errorf("control socket directory %s is not owned by this user", dir)
	}
	return nil
}

// ErrUndetermined means a control exec could not answer in time. It is not a
// statement that the master is down, so callers must not read it as one.
var ErrUndetermined = errors.New("ssh did not answer in time")

// Client runs ssh. Bin is the binary, "ssh" by default; tests point it at a fake.
type Client struct {
	Bin string
}

func (c Client) bin() string {
	if c.Bin != "" {
		return c.Bin
	}
	return "ssh"
}

// Binary is the ssh binary this client runs.
func (c Client) Binary() string { return c.bin() }

// Check asks the master whether it is up. nil means it is. ErrUndetermined means
// nobody knows, and any other error means it is not.
func (c Client) Check(ctx context.Context, t Target) error {
	_, err := c.control(ctx, CheckArgs(t))
	return err
}

// Exit closes the master. A master already gone also answers with an error, and
// callers treat that as advisory.
func (c Client) Exit(ctx context.Context, t Target) error {
	_, err := c.control(ctx, ExitArgs(t))
	return err
}

func (c Client) control(ctx context.Context, args []string) ([]byte, error) {
	cctx, cancel := context.WithTimeout(ctx, ControlTimeout)
	defer cancel()
	cmd := exec.Command(c.bin(), args...)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = io.Discard
	cmd.WaitDelay = pipeGrace
	setGroup(cmd)
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		return out.Bytes(), err
	case <-cctx.Done():
		killGroup(cmd, 0)
		<-done
		return nil, ErrUndetermined
	}
}

// Resolved is what the local ssh config says an alias means.
type Resolved struct {
	User      string
	Hostname  string
	Port      string
	ProxyJump string
}

// Resolve runs `ssh -G`. It never connects, and a failure blocks nothing: the
// approval dialog then shows only the configured values.
func (c Client) Resolve(ctx context.Context, alias, user string) (Resolved, error) {
	return c.ResolveTarget(ctx, Target{Alias: alias, User: user})
}

// ResolveTarget includes the same explicit overrides used to connect.
func (c Client) ResolveTarget(ctx context.Context, t Target) (Resolved, error) {
	if err := t.Valid(); err != nil {
		return Resolved{}, err
	}
	out, err := c.control(ctx, append(append([]string{"-G"}, t.connectionArgs()...), t.Alias))
	if err != nil {
		return Resolved{}, err
	}
	var r Resolved
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		k, v, ok := strings.Cut(sc.Text(), " ")
		if !ok {
			continue
		}
		switch strings.ToLower(k) {
		case "user":
			r.User = v
		case "hostname":
			r.Hostname = v
		case "port":
			r.Port = v
		case "proxyjump":
			if v != "none" {
				r.ProxyJump = v
			}
		}
	}
	return r, nil
}

// Run executes one remote command on the master and blocks until it ends. stdin
// is /dev/null. The command runs in its own process group, and when ctx ends the
// whole group is terminated (SIGTERM, grace, SIGKILL), because exec's own context
// kill reaches only the one process. The remote process may outlive that, which
// is a fact the callers must state to a human.
//
// exit is the process's exit code, -1 when it did not exit on its own. err is
// non-nil only when ssh could not be started at all or was killed.
func (c Client) Run(ctx context.Context, t Target, remote string, stdout, stderr io.Writer) (exit int, err error) {
	if err := t.Valid(); err != nil {
		return -1, err
	}
	cmd := exec.Command(c.bin(), CommandArgs(t, remote)...)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.WaitDelay = pipeGrace
	setGroup(cmd)
	if err := cmd.Start(); err != nil {
		return -1, err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case werr := <-done:
		return exitOf(cmd, werr)
	case <-ctx.Done():
		killGroup(cmd, stopGrace)
		<-done
		return -1, context.Cause(ctx)
	}
}

func exitOf(cmd *exec.Cmd, werr error) (int, error) {
	if cmd.ProcessState == nil {
		return -1, werr
	}
	if code := cmd.ProcessState.ExitCode(); code >= 0 {
		return code, nil
	}
	if werr == nil {
		werr = errors.New("ssh was killed by a signal")
	}
	return -1, werr
}
