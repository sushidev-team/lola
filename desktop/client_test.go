package main

import (
	"bufio"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/sushidev-team/lola/internal/protocol"
)

// fakeDaemon listens on $LOLA_HOME/lola.sock and answers each request with the
// handler's reply, mimicking the real daemon's newline-delimited JSON framing.
type fakeDaemon struct {
	t       *testing.T
	ln      net.Listener
	handler func(protocol.Request) protocol.Response
	mu      sync.Mutex
	got     []protocol.Request
}

func startFakeDaemon(t *testing.T, h func(protocol.Request) protocol.Response) *fakeDaemon {
	t.Helper()
	home := t.TempDir()
	t.Setenv("LOLA_HOME", home)
	ln, err := net.Listen("unix", filepath.Join(home, "lola.sock"))
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	f := &fakeDaemon{t: t, ln: ln, handler: h}
	go f.serve()
	t.Cleanup(func() { _ = ln.Close() })
	return f
}

func (f *fakeDaemon) serve() {
	for {
		conn, err := f.ln.Accept()
		if err != nil {
			return
		}
		go func() {
			defer conn.Close()
			sc := bufio.NewScanner(conn)
			for sc.Scan() {
				var req protocol.Request
				if err := json.Unmarshal(sc.Bytes(), &req); err != nil {
					continue
				}
				f.mu.Lock()
				f.got = append(f.got, req)
				f.mu.Unlock()
				resp := f.handler(req)
				line, _ := json.Marshal(resp)
				_, _ = conn.Write(append(line, '\n'))
			}
		}()
	}
}

func (f *fakeDaemon) requests() []protocol.Request {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]protocol.Request(nil), f.got...)
}

func TestSocketPathHonorsLolaHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("LOLA_HOME", home)
	got, err := socketPath()
	if err != nil {
		t.Fatalf("socketPath: %v", err)
	}
	if want := filepath.Join(home, "lola.sock"); got != want {
		t.Fatalf("socketPath = %q, want %q", got, want)
	}
}

func TestRoundTripDecodesData(t *testing.T) {
	startFakeDaemon(t, func(req protocol.Request) protocol.Response {
		if req.Cmd != "sessions" {
			return protocol.Response{OK: false, Error: "unexpected cmd"}
		}
		data, _ := json.Marshal(protocol.SessionsData{
			Sessions: []protocol.SessionInfo{{ID: "lola-x", Status: "working"}},
		})
		return protocol.Response{OK: true, Data: data}
	})

	var out protocol.SessionsData
	if err := call(protocol.Request{Cmd: "sessions"}, time.Second, &out); err != nil {
		t.Fatalf("call: %v", err)
	}
	if len(out.Sessions) != 1 || out.Sessions[0].ID != "lola-x" {
		t.Fatalf("decoded %+v", out.Sessions)
	}
}

func TestCallSurfacesDaemonError(t *testing.T) {
	startFakeDaemon(t, func(protocol.Request) protocol.Response {
		return protocol.Response{OK: false, Error: "boom"}
	})
	err := call(protocol.Request{Cmd: "kill", Session: "x"}, time.Second, nil)
	if err == nil || err.Error() != "boom" {
		t.Fatalf("want error 'boom', got %v", err)
	}
}

func TestCallDaemonDown(t *testing.T) {
	// A LOLA_HOME with no listener → dial fails → errDaemonDown.
	home := t.TempDir()
	t.Setenv("LOLA_HOME", home)
	// Ensure no stale socket file resolves.
	_ = os.Remove(filepath.Join(home, "lola.sock"))
	if daemonAlive() {
		t.Fatal("daemonAlive true with no listener")
	}
	err := call(protocol.Request{Cmd: "status"}, time.Second, nil)
	if err != errDaemonDown {
		t.Fatalf("want errDaemonDown, got %v", err)
	}
}

func TestDaemonServicePassesArgs(t *testing.T) {
	f := startFakeDaemon(t, func(req protocol.Request) protocol.Response {
		data, _ := json.Marshal(protocol.PrsData{Repo: "o/r"})
		return protocol.Response{OK: true, Data: data}
	})
	d := &DaemonService{}
	if _, err := d.PRs("proj", true); err != nil {
		t.Fatalf("PRs: %v", err)
	}
	reqs := f.requests()
	if len(reqs) != 1 || reqs[0].Cmd != "prs" {
		t.Fatalf("requests: %+v", reqs)
	}
	var args protocol.PrsArgs
	if err := json.Unmarshal(reqs[0].Args, &args); err != nil {
		t.Fatalf("args: %v", err)
	}
	if args.Project != "proj" || !args.Refresh {
		t.Fatalf("args = %+v", args)
	}
}
