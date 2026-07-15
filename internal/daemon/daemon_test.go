package daemon

import (
	"bytes"
	"context"
	"errors"
	"log"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// warnPreMigrationSessions logs ONE best-effort warning naming lola sessions
// still on the user's default tmux server (seamed so no real tmux runs), and
// stays silent when there are none or the scan errors.
func TestWarnPreMigrationSessions(t *testing.T) {
	prev := defaultServerSessions
	t.Cleanup(func() { defaultServerSessions = prev })

	var buf bytes.Buffer
	d := &Daemon{log: log.New(&buf, "", 0)}

	// Orphans present → one warning naming them + the manual cleanup hint.
	defaultServerSessions = func(context.Context, string, string) ([]string, error) {
		return []string{"lola-web-eng-1", "lola-api-eng-2"}, nil
	}
	d.warnPreMigrationSessions(context.Background())
	out := buf.String()
	for _, want := range []string{"migration", "lola-web-eng-1", "lola-api-eng-2", "tmux kill-session"} {
		if !strings.Contains(out, want) {
			t.Errorf("warning = %q, want it to include %q", out, want)
		}
	}
	if n := strings.Count(out, "migration:"); n != 1 {
		t.Errorf("want exactly one migration warning line, got %d:\n%s", n, out)
	}

	// No orphans → silent.
	buf.Reset()
	defaultServerSessions = func(context.Context, string, string) ([]string, error) { return nil, nil }
	d.warnPreMigrationSessions(context.Background())
	if buf.Len() != 0 {
		t.Errorf("no orphans must log nothing, got %q", buf.String())
	}

	// Scan error → best-effort silent.
	buf.Reset()
	defaultServerSessions = func(context.Context, string, string) ([]string, error) {
		return nil, errors.New("tmux missing")
	}
	d.warnPreMigrationSessions(context.Background())
	if buf.Len() != 0 {
		t.Errorf("a scan error must be best-effort silent, got %q", buf.String())
	}
}

// shortSockDir returns a directory whose paths stay under the unix socket
// path limit (~104 bytes on darwin); long TMPDIRs fall back to /tmp.
func shortSockDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if len(dir) < 80 {
		return dir
	}
	for _, base := range []string{"/tmp", "/tmp/claude"} {
		if d, err := os.MkdirTemp(base, "lola"); err == nil {
			t.Cleanup(func() { os.RemoveAll(d) })
			return d
		}
	}
	t.Skip("no short tmp dir available for unix sockets")
	return ""
}

func TestClaimSocketRefusesLiveDaemonAndReclaimsStale(t *testing.T) {
	sock := filepath.Join(shortSockDir(t), "lola.sock")

	// A live listener on the socket: a second daemon must refuse to start
	// instead of stealing the path (two daemons would double-spawn issues).
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Skipf("cannot listen on unix socket here: %v", err)
	}
	ln.(*net.UnixListener).SetUnlinkOnClose(false)
	if _, err := claimSocket(sock); err == nil {
		t.Fatal("claimSocket must refuse when a live daemon already serves the socket")
	}

	// Close the listener but leave the socket FILE behind (stale socket
	// after a crash): claiming must now succeed.
	ln.Close()
	if _, err := os.Stat(sock); err != nil {
		t.Fatalf("test setup: stale socket file missing: %v", err)
	}
	ln2, err := claimSocket(sock)
	if err != nil {
		t.Fatalf("claimSocket must reclaim a stale socket file: %v", err)
	}
	defer ln2.Close()
	fi, err := os.Stat(sock)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("socket mode = %v, want 0600", fi.Mode().Perm())
	}
}
