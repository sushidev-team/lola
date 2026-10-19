package daemon

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/hook"
	"github.com/sushidev-team/lola/internal/protocol"
)

// The HOOK BRIDGE: how a SANDBOXED agent's hooks reach the daemon.
//
// A containerized agent cannot reach ~/.lola/lola.sock — a host unix socket
// does not survive a bind mount into Docker Desktop's / OrbStack's VM — but it
// can reach a host LOOPBACK TCP port through host.docker.internal. So while any
// project is sandboxed, the daemon also listens on 127.0.0.1:<[sandbox].
// hook_port>, and the sandboxed session's env file names that address plus a
// per-session token (runtime.sandboxEnv).
//
// Loopback TCP is reachable by every local process, unlike the 0600 socket, so
// the bridge is deliberately NARROW, and each rail is load-bearing:
//
//   - Four commands only: hookEvent and agentReport — exactly what `lola hook`
//     and `lola report` send — plus planGate and planSubmit, the plan gate's
//     PreToolUse check and `lola plan-gate submit` (without them the gate
//     would fail open in every container). All four act only on the session
//     the token names. Everything else (kill, answer, planDecide, config…) is
//     refused before it reaches handle.
//   - Every request carries hook.TokenFor(secret, req.Session); a token names
//     ONE session, so a container can speak only for itself, and a process
//     without a token cannot speak at all. The secret is a 0600 file under
//     ~/.lola/state that no container mounts.
//   - Connections are short and bounded (a deadline per connection, the same
//     1MB line cap as the unix socket): a hook is one line and one reply.

// hookBridgeConnTimeout bounds one bridge connection. A hook posts one line
// within its own 2s budget; anything slower is not a hook.
const hookBridgeConnTimeout = 10 * time.Second

// hookBridgeCmds are the only commands the bridge forwards.
var hookBridgeCmds = map[string]bool{"hookEvent": true, "agentReport": true, "planGate": true, "planSubmit": true}

// hookBridge is the bridge listener state, guarded by its own mutex (never
// held together with d.mu).
type hookBridge struct {
	mu     sync.Mutex
	ln     net.Listener
	port   int
	secret []byte
}

// hookSecretPath is where the bridge's HMAC secret lives.
func hookSecretPath(home string) string {
	return filepath.Join(home, "state", "hook-secret")
}

// hookSecret loads (creating on first use) and caches the bridge secret.
func (d *Daemon) hookSecret() ([]byte, error) {
	d.bridge.mu.Lock()
	defer d.bridge.mu.Unlock()
	if d.bridge.secret != nil {
		return d.bridge.secret, nil
	}
	s, err := hook.LoadOrCreateSecret(hookSecretPath(d.home))
	if err != nil {
		return nil, err
	}
	d.bridge.secret = s
	return s, nil
}

// hookToken is the runtime's HookToken seam: the token a sandboxed session's
// env file carries. "" when the secret is unavailable (the session's hooks are
// then refused, which the log says).
func (d *Daemon) hookToken(session string) string {
	secret, err := d.hookSecret()
	if err != nil {
		d.logf("", "sandbox hook bridge: no secret, session %s will not be heard: %v", session, err)
		return ""
	}
	return hook.TokenFor(secret, session)
}

// syncHookBridge opens the bridge when some project is sandboxed and closes it
// when none is (or rebinds on a port change). Idempotent; a bind failure is
// logged, not fatal — the sandboxed sessions still run, they are just not heard
// until the port frees up and the next reload retries.
func (d *Daemon) syncHookBridge(ctx context.Context) {
	d.mu.Lock()
	want := d.cfg.AnySandboxed()
	port := d.cfg.Sandbox.EffectiveHookPort()
	d.mu.Unlock()

	d.bridge.mu.Lock()
	defer d.bridge.mu.Unlock()
	if d.bridge.ln != nil && (!want || d.bridge.port != port) {
		_ = d.bridge.ln.Close()
		d.bridge.ln = nil
	}
	if !want || d.bridge.ln != nil {
		return
	}
	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		d.logf("", "sandbox hook bridge: cannot listen on %s (sandboxed agents' hooks will not reach the daemon): %v", addr, err)
		return
	}
	d.bridge.ln, d.bridge.port = ln, port
	d.logf("", "sandbox hook bridge listening on %s", addr)
	go d.serveHookBridge(ctx, ln)
}

// stopHookBridge closes the bridge at shutdown.
func (d *Daemon) stopHookBridge() {
	d.bridge.mu.Lock()
	defer d.bridge.mu.Unlock()
	if d.bridge.ln != nil {
		_ = d.bridge.ln.Close()
		d.bridge.ln = nil
	}
}

func (d *Daemon) serveHookBridge(ctx context.Context, ln net.Listener) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) || ctx.Err() != nil {
				return
			}
			d.logf("", "sandbox hook bridge: accept: %v", err)
			continue
		}
		go d.handleBridgeConn(ctx, conn)
	}
}

// handleBridgeConn is handleConn with the bridge's rails: a connection
// deadline, the command allowlist and the per-session token.
func (d *Daemon) handleBridgeConn(ctx context.Context, conn net.Conn) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(hookBridgeConnTimeout))
	sc := bufio.NewScanner(conn)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	enc := json.NewEncoder(conn)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var req protocol.Request
		if err := json.Unmarshal(line, &req); err != nil {
			_ = enc.Encode(protocol.Response{OK: false, Error: "bad request"})
			return
		}
		if err := d.bridgeAdmit(req); err != nil {
			_ = enc.Encode(protocol.Response{OK: false, Error: err.Error()})
			return
		}
		if enc.Encode(d.handle(ctx, req)) != nil {
			return
		}
	}
}

// bridgeAdmit is the bridge's whole authorization decision.
func (d *Daemon) bridgeAdmit(req protocol.Request) error {
	if !hookBridgeCmds[req.Cmd] {
		return errors.New("command not allowed on the sandbox hook bridge")
	}
	secret, err := d.hookSecret()
	if err != nil {
		return errors.New("hook bridge unavailable")
	}
	if !hook.ValidToken(secret, req.Session, req.HookToken) {
		return errors.New("invalid hook token")
	}
	return nil
}

// sandboxedBin is the binary a project's spawn health gate must find: a
// sandboxed project's agent binary lives in its IMAGE, so the host needs only
// the docker CLI; any other project needs bin itself. cfg is read as given
// (callers hold d.mu or pass a snapshot).
func sandboxedBin(cfg *config.Config, project, bin string) string {
	if p := cfg.ProjectByName(project); p != nil && p.Sandboxed() {
		return cfg.Sandbox.DockerBin()
	}
	return bin
}

// launchBin is sandboxedBin under d.mu.
func (d *Daemon) launchBin(project, bin string) string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return sandboxedBin(d.cfg, project, bin)
}
