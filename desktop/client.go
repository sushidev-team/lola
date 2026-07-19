package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"net"
	"path/filepath"
	"time"

	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/protocol"
)

// errDaemonDown is returned (wrapped) when the unix socket can't be dialed. The
// frontend keys its "daemon down" banner off this so it can offer to start the
// daemon rather than showing a raw dial error.
var errDaemonDown = errors.New("daemon not running (start with: lola run)")

// socketPath resolves ~/.lola/lola.sock (honoring $LOLA_HOME) exactly like the
// TUI client, so the desktop app talks to the same daemon the TUI does.
func socketPath() (string, error) {
	home, err := config.Home()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "lola.sock"), nil
}

// roundTrip performs one request/response over the daemon socket: dial, write a
// single newline-terminated JSON line, read one reply line, decode. It mirrors
// the framing the daemon speaks (internal/daemon/server.go) and the TUI client's
// tolerance for a reply the daemon closes without a trailing newline.
//
// timeout bounds the whole exchange. Cheap cached reads (sessions/projects/
// status) use a short timeout; commands that do real work (pollOnce, the open*
// family) need a generous one because the daemon runs them synchronously.
func roundTrip(req protocol.Request, timeout time.Duration) (protocol.Response, error) {
	sock, err := socketPath()
	if err != nil {
		return protocol.Response{}, err
	}
	conn, err := net.DialTimeout("unix", sock, 2*time.Second)
	if err != nil {
		return protocol.Response{}, errDaemonDown
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(timeout))

	line, err := json.Marshal(req)
	if err != nil {
		return protocol.Response{}, err
	}
	if _, err := conn.Write(append(line, '\n')); err != nil {
		return protocol.Response{}, err
	}

	r := bufio.NewReaderSize(conn, 1<<20)
	respLine, err := r.ReadBytes('\n')
	if err != nil && !(errors.Is(err, io.EOF) && len(respLine) > 0) {
		return protocol.Response{}, err
	}
	var resp protocol.Response
	if err := json.Unmarshal(respLine, &resp); err != nil {
		return protocol.Response{}, err
	}
	return resp, nil
}

// call is the typed helper: it runs a command and decodes Response.Data into out
// (out may be nil for commands that return only {ok:true}). A non-OK response is
// surfaced as an error carrying the daemon's message verbatim.
func call(req protocol.Request, timeout time.Duration, out any) error {
	resp, err := roundTrip(req, timeout)
	if err != nil {
		return err
	}
	if !resp.OK {
		if resp.Error == "" {
			return errors.New("daemon returned not-ok with no error")
		}
		return errors.New(resp.Error)
	}
	if out != nil && len(resp.Data) > 0 {
		return json.Unmarshal(resp.Data, out)
	}
	return nil
}

// daemonAlive dial-probes the socket; a successful connect means the daemon is
// up (mirrors daemonctl.daemonAlive).
func daemonAlive() bool {
	sock, err := socketPath()
	if err != nil {
		return false
	}
	conn, err := net.DialTimeout("unix", sock, time.Second)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// Timeouts for the two request classes.
const (
	shortTimeout = 10 * time.Second       // cached reads + cheap mutations
	longTimeout  = 5 * time.Minute        // pollOnce + open* (daemon runs them synchronously)
)
