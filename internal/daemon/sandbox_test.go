package daemon

// Tests for SUSHI-628's daemon half: the sandbox hook bridge (hookbridge.go)
// and the sandbox gates. Stack sessions are in stack_test.go.

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"log"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/gitguard"
	"github.com/sushidev-team/lola/internal/hook"
	"github.com/sushidev-team/lola/internal/linear"
	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/session"
	"github.com/sushidev-team/lola/internal/state"
)

// bridgeRoundTrip sends one request over a bridge connection and returns the
// reply.
func bridgeRoundTrip(t *testing.T, d *Daemon, req protocol.Request) protocol.Response {
	t.Helper()
	client, server := net.Pipe()
	defer client.Close()
	go d.handleBridgeConn(context.Background(), server)
	raw, _ := json.Marshal(req)
	_ = client.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := client.Write(append(raw, '\n')); err != nil {
		t.Fatal(err)
	}
	line, err := bufio.NewReader(client).ReadString('\n')
	if err != nil {
		t.Fatalf("no reply: %v", err)
	}
	var resp protocol.Response
	if err := json.Unmarshal([]byte(line), &resp); err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestHookBridgeAdmitsOnlyTokenedHooksForTheirOwnSession(t *testing.T) {
	d := newTestDaemon(t, nativeTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeNative{})
	s := nativeSess("FE-1", "working")
	s.SetAgentState(state.AgentWorking, "", time.Now())
	d.sessions.Upsert(s)
	other := nativeSess("FE-2", "working")
	d.sessions.Upsert(other)

	tok := d.hookToken(s.ID)
	if tok == "" {
		t.Fatal("no token minted")
	}
	stop := protocol.Request{Cmd: "hookEvent", Session: s.ID, Event: "stop", HookToken: tok}
	if resp := bridgeRoundTrip(t, d, stop); !resp.OK {
		t.Fatalf("valid hook refused: %s", resp.Error)
	}
	if cur, _ := d.sessions.Get(s.ID); !cur.AtPrompt {
		t.Fatalf("stop hook over the bridge did not reach the session: %+v", cur)
	}

	for name, req := range map[string]protocol.Request{
		"no token":          {Cmd: "hookEvent", Session: s.ID, Event: "stop"},
		"forged token":      {Cmd: "hookEvent", Session: s.ID, Event: "stop", HookToken: strings.Repeat("0", 64)},
		"another session":   {Cmd: "hookEvent", Session: other.ID, Event: "stop", HookToken: tok},
		"command not hooks": {Cmd: "kill", Session: s.ID, HookToken: tok},
		"config reload":     {Cmd: "reload", HookToken: tok},
	} {
		if resp := bridgeRoundTrip(t, d, req); resp.OK {
			t.Errorf("%s: admitted", name)
		}
	}
	if cur, _ := d.sessions.Get(other.ID); cur.AtPrompt {
		t.Error("a token for one session moved another")
	}
}

// The plan gate reaches a sandboxed agent too: its PreToolUse check and its
// submit pass the bridge for the token's own session, while a human's verdict
// (planDecide) never does.
func TestHookBridgeCarriesThePlanGate(t *testing.T) {
	d := newTestDaemon(t, nativeTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeNative{})
	s := nativeSess("FE-1", "working")
	s.PlanGate = session.PlanPlanning
	d.sessions.Upsert(s)
	tok := d.hookToken(s.ID)

	resp := bridgeRoundTrip(t, d, protocol.Request{Cmd: "planGate", Session: s.ID, HookToken: tok})
	var gate protocol.PlanGateData
	if !resp.OK || json.Unmarshal(resp.Data, &gate) != nil || !gate.Blocked {
		t.Fatalf("planGate over the bridge = %+v / %+v, want a denial", resp, gate)
	}
	args, _ := json.Marshal(protocol.PlanSubmitArgs{Plan: "## Approach\nx"})
	if resp := bridgeRoundTrip(t, d, protocol.Request{Cmd: "planSubmit", Session: s.ID, Args: args, HookToken: tok}); !resp.OK {
		t.Fatalf("planSubmit over the bridge refused: %s", resp.Error)
	}
	if cur, _ := d.sessions.Get(s.ID); cur.PlanGate != session.PlanSubmitted {
		t.Fatalf("PlanGate = %q, want submitted", cur.PlanGate)
	}
	verdict, _ := json.Marshal(protocol.PlanDecideArgs{Approve: true})
	if resp := bridgeRoundTrip(t, d, protocol.Request{Cmd: "planDecide", Session: s.ID, Args: verdict, HookToken: tok}); resp.OK {
		t.Fatal("planDecide admitted on the bridge: an agent must not approve its own plan")
	}
}

func TestHookBridgeOpensOnlyWhileAProjectIsSandboxed(t *testing.T) {
	cfg := nativeTestConfig(nativePoll("p1"))
	d := newTestDaemon(t, cfg, &linear.Fake{}, &fakeNative{})
	free, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := free.Addr().(*net.TCPAddr).Port
	free.Close()
	cfg.Sandbox = config.SandboxConfig{HookPort: port}
	addr := "127.0.0.1:" + strconv.Itoa(port)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	defer d.stopHookBridge()

	d.syncHookBridge(ctx)
	if c, err := net.DialTimeout("tcp", addr, time.Second); err == nil {
		c.Close()
		t.Fatal("bridge listening with no sandboxed project")
	}

	d.mu.Lock()
	d.cfg.Projects[0].Sandbox = config.SandboxDocker
	d.mu.Unlock()
	d.syncHookBridge(ctx)
	c, err := net.DialTimeout("tcp", addr, time.Second)
	if err != nil {
		t.Fatalf("bridge not listening for a sandboxed project: %v", err)
	}
	c.Close()

	d.mu.Lock()
	d.cfg.Projects[0].Sandbox = ""
	d.mu.Unlock()
	d.syncHookBridge(ctx)
	if c, err := net.DialTimeout("tcp", addr, time.Second); err == nil {
		c.Close()
		t.Fatal("bridge still listening after the last sandboxed project went")
	}
}

func TestHookTokenIsStableAcrossRestarts(t *testing.T) {
	d := newTestDaemon(t, nativeTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeNative{})
	tok := d.hookToken("lola-p1-fe-1")
	// A fresh daemon on the same home reads the same secret.
	d2 := newDaemon(d.cfg, &linear.Fake{}, log.New(io.Discard, "", 0), d.home)
	if got := d2.hookToken("lola-p1-fe-1"); got != tok {
		t.Fatalf("token changed across restart: %q vs %q", got, tok)
	}
	secret, err := hook.LoadOrCreateSecret(hookSecretPath(d.home))
	if err != nil || !hook.ValidToken(secret, "lola-p1-fe-1", tok) {
		t.Fatalf("token does not verify: %v", err)
	}
}

func TestSandboxedProjectGatesOnTheDockerCLI(t *testing.T) {
	cfg := nativeTestConfig(nativePoll("p1"))
	if got := sandboxedBin(cfg, "p1", "claude"); got != "claude" {
		t.Fatalf("host project gate = %q", got)
	}
	cfg.Projects[0].Sandbox = config.SandboxDocker
	cfg.Sandbox.Docker = "/opt/orbstack/docker"
	if got := sandboxedBin(cfg, "p1", "claude"); got != "/opt/orbstack/docker" {
		t.Fatalf("sandboxed project gate = %q", got)
	}
}

// A sandboxed session's agent lives in the image: revive gates on the docker
// CLI, like spawn, switch and fork.
func TestReviveGatesASandboxedSessionOnDocker(t *testing.T) {
	cfg := nativeTestConfig(nativePoll("p1"))
	cfg.Projects[0].Sandbox = config.SandboxDocker
	d := newTestDaemon(t, cfg, &linear.Fake{}, &fakeNative{})
	var asked []string
	d.runtimeHealth = func(bin string) error { asked = append(asked, bin); return nil }
	s := nativeSess("FE-1", "dead")
	s.Agent = "codex"
	d.sessions.Upsert(s)
	if _, err := d.handleRevive(context.Background(), s.ID); err != nil {
		t.Fatalf("handleRevive: %v", err)
	}
	if len(asked) != 1 || asked[0] != "docker" {
		t.Fatalf("revive health asked for %v, want [docker]", asked)
	}
}

// After a restart every sandboxed worktree is pinned again from its record;
// one whose record lost the pin is DENIED host git rather than trusted.
func TestSyncGitPinsPinsOrDeniesSandboxedWorktrees(t *testing.T) {
	d := newTestDaemon(t, nativeTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeNative{})
	pinned := nativeSess("FE-1", "working")
	pinned.Sandbox = config.SandboxDocker
	pinned.Worktree = "/wt/p1/" + pinned.ID
	pinned.GitPin = &gitguard.Pin{GitDir: "/r/.git/worktrees/x", CommonDir: "/r/.git"}
	stack := nativeSess("FE-3", "working")
	stack.Sandbox = config.SandboxDocker
	stack.Stack = true
	stack.Worktree = "/wt/p1/" + stack.ID
	stack.Apps = []session.App{
		{Name: "api", Worktree: stack.Worktree + "/api", GitPin: &gitguard.Pin{GitDir: "/a/.git/worktrees/y", CommonDir: "/a/.git"}},
		{Name: "web", Worktree: stack.Worktree + "/web"}, // rebuilt by adoption: no pin
	}
	host := nativeSess("FE-2", "working")
	host.Worktree = "/wt/p1/" + host.ID
	d.sessions.Upsert(pinned)
	d.sessions.Upsert(stack)
	d.sessions.Upsert(host)
	defer func() {
		gitguard.Clear(pinned.Worktree)
		for _, a := range stack.Apps {
			gitguard.Clear(a.Worktree)
		}
	}()

	d.syncGitPins()
	if _, p, ok := gitguard.Lookup(pinned.Worktree); !ok || p != *pinned.GitPin {
		t.Fatalf("pinned worktree = %+v %v", p, ok)
	}
	if _, p, ok := gitguard.Lookup(stack.Apps[0].Worktree); !ok || p != *stack.Apps[0].GitPin {
		t.Fatalf("stack app = %+v %v", p, ok)
	}
	if at, p, ok := gitguard.Lookup(stack.Apps[1].Worktree); !ok || at != stack.Apps[1].Worktree || p.Valid() {
		t.Fatalf("pin-less stack app not denied: %q %+v %v", at, p, ok)
	}
	if gitguard.Pinned(stack.Worktree) {
		t.Fatal("a stack session's folder is not a repository and must not be pinned")
	}
	if gitguard.Pinned(host.Worktree) {
		t.Fatal("a host session was pinned")
	}
}
