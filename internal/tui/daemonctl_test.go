package tui

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// shortHome makes a temp LOLA_HOME with a short path. t.TempDir() embeds the
// (long) test name, which can push the unix socket path over the darwin
// sun_path limit (~104 bytes) and fail bind with EINVAL.
func shortHome(t *testing.T) string {
	t.Helper()
	d, err := os.MkdirTemp("", "lola")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(d) })
	t.Setenv("LOLA_HOME", d)
	return d
}

// bindSock binds the daemon socket and accepts+closes connections, so
// daemonAlive() reports true without a real daemon. It replies to nothing —
// only the dial-and-close liveness probe is exercised by callers that use it.
func bindSock(t *testing.T, home string) net.Listener {
	t.Helper()
	ln, err := net.Listen("unix", filepath.Join(home, "lola.sock"))
	if errors.Is(err, syscall.EPERM) {
		t.Skip("sandbox forbids unix socket bind")
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()
	return ln
}

func TestDaemonAlive(t *testing.T) {
	home := shortHome(t)
	if daemonAlive() {
		t.Fatal("no socket bound, daemonAlive should be false")
	}
	bindSock(t, home)
	if !daemonAlive() {
		t.Fatal("socket bound, daemonAlive should be true")
	}
}

func TestEnsureDaemonSpawnsWhenDown(t *testing.T) {
	home := shortHome(t)

	spawned := false
	orig := spawnDaemonFn
	t.Cleanup(func() { spawnDaemonFn = orig })
	spawnDaemonFn = func() error {
		spawned = true
		bindSock(t, home) // make the socket live so waitDaemon(true) returns fast
		return nil
	}

	msg, ok := ensureDaemonCmd().(daemonOpMsg)
	if !ok {
		t.Fatalf("ensureDaemonCmd returned %T, want daemonOpMsg", ensureDaemonCmd())
	}
	if !spawned {
		t.Error("down daemon should have been spawned")
	}
	if msg.op != "start" || msg.err != nil {
		t.Errorf("msg = %+v, want {start <nil>}", msg)
	}
}

func TestEnsureDaemonSkipsWhenAlive(t *testing.T) {
	home := shortHome(t)
	bindSock(t, home)

	orig := spawnDaemonFn
	t.Cleanup(func() { spawnDaemonFn = orig })
	spawnDaemonFn = func() error {
		t.Fatal("must not spawn when a daemon is already alive")
		return nil
	}

	msg := ensureDaemonCmd().(daemonOpMsg)
	if msg.op != "start" || msg.err != nil {
		t.Errorf("msg = %+v, want {start <nil>}", msg)
	}
}

func TestStopDaemonCmdWhenDown(t *testing.T) {
	shortHome(t)
	// No socket: stop is a no-op and the socket is already quiet, so this
	// returns promptly without hitting the stop timeout.
	msg := stopDaemonCmd().(daemonOpMsg)
	if msg.op != "stop" || msg.err != nil {
		t.Errorf("msg = %+v, want {stop <nil>}", msg)
	}
}

func TestRestartDaemonCmdSpawnsWhenDown(t *testing.T) {
	home := shortHome(t)

	spawned := false
	orig := spawnDaemonFn
	t.Cleanup(func() { spawnDaemonFn = orig })
	spawnDaemonFn = func() error {
		spawned = true
		bindSock(t, home)
		return nil
	}

	msg := restartDaemonCmd().(daemonOpMsg)
	if !spawned {
		t.Error("restart on a down daemon should spawn a fresh one")
	}
	if msg.op != "restart" || msg.err != nil {
		t.Errorf("msg = %+v, want {restart <nil>}", msg)
	}
}
