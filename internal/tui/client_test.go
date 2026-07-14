package tui

import (
	"bufio"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/you/aop/internal/linear"
	"github.com/you/aop/internal/protocol"
)

// fakeDaemon serves one connection per accept loop iteration, replying with
// resp to any request line.
func fakeDaemon(t *testing.T, home string, resp protocol.Response) {
	t.Helper()
	ln, err := net.Listen("unix", filepath.Join(home, "aop.sock"))
	if errors.Is(err, syscall.EPERM) {
		t.Skip("sandbox forbids unix socket bind")
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				if _, err := bufio.NewReader(c).ReadString('\n'); err != nil {
					return
				}
				out, _ := json.Marshal(resp)
				c.Write(append(out, '\n'))
			}(conn)
		}
	}()
}

func TestSendStatusOK(t *testing.T) {
	home := t.TempDir()
	t.Setenv("AOP_HOME", home)

	data, _ := json.Marshal(protocol.StatusData{Polls: []protocol.PollStatus{{Name: "fe", Enabled: true}}})
	fakeDaemon(t, home, protocol.Response{OK: true, Data: data})

	if err := Send(`{"cmd":"status"}`); err != nil {
		t.Fatalf("Send(status) = %v, want nil", err)
	}
}

func TestSendErrorResponse(t *testing.T) {
	home := t.TempDir()
	t.Setenv("AOP_HOME", home)
	fakeDaemon(t, home, protocol.Response{OK: false, Error: "no such poll"})

	err := Send(`{"cmd":"enable","poll":"nope"}`)
	if err == nil || err.Error() != "no such poll" {
		t.Fatalf("Send = %v, want daemon error surfaced", err)
	}
}

func TestSendDaemonDown(t *testing.T) {
	t.Setenv("AOP_HOME", t.TempDir())
	if err := Send(`{"cmd":"status"}`); !errors.Is(err, errDaemonDown) {
		t.Fatalf("Send with no socket = %v, want errDaemonDown", err)
	}
}

func TestLogsFilter(t *testing.T) {
	home := t.TempDir()
	t.Setenv("AOP_HOME", home)
	log := "2026-01-01 [fe] matched 2\n2026-01-01 [be] matched 0\n"
	if err := os.WriteFile(filepath.Join(home, "daemon.log"), []byte(log), 0o600); err != nil {
		t.Fatal(err)
	}
	// Only asserts it runs without error; output filtering is visual.
	if err := Logs("fe", false); err != nil {
		t.Fatalf("Logs = %v", err)
	}
	if err := Logs("", false); err != nil {
		t.Fatalf("Logs(all) = %v", err)
	}
}

func TestTeamCacheRoundTrip(t *testing.T) {
	t.Setenv("AOP_HOME", t.TempDir())
	in := &teamMeta{Teams: []linear.Team{{ID: "t1", Key: "FE", Name: "Frontend"}}}
	if err := saveTeamCache("t1", in); err != nil {
		t.Fatal(err)
	}
	out, err := loadTeamCache("t1")
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Teams) != 1 || out.Teams[0].Key != "FE" {
		t.Fatalf("cache round-trip mismatch: %+v", out.Teams)
	}
}
