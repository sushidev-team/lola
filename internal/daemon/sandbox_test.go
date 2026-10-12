package daemon

// Tests for SUSHI-628's daemon half: the sandbox hook bridge (hookbridge.go)
// and linked-repository PR tracking (linkedprs.go + the merged-cleanup hold).

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/hook"
	"github.com/sushidev-team/lola/internal/linear"
	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/scm"
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

func TestObserveLinkedPRsKeepsLastFactsOnFailure(t *testing.T) {
	d := newTestDaemon(t, nativeTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeNative{})
	pr := &scm.PR{Number: 9, State: "OPEN", URL: "https://github.com/acme/api/pull/9"}
	d.prForBranch = func(_ context.Context, repo, branch string) (*scm.PR, error) {
		switch repo {
		case "acme/api":
			return pr, nil
		case "acme/docs":
			return nil, errors.New("gh: 502")
		}
		t.Fatalf("unexpected repo %s", repo)
		return nil, nil
	}
	s := nativeSess("FE-1", "working")
	s.Linked = []session.LinkedRepo{
		{Name: "api", Repo: "acme/api", Branch: s.Branch},
		{Name: "docs", Repo: "acme/docs", Branch: s.Branch, PR: &scm.PR{Number: 3, State: "OPEN"}},
		{Name: "norepo", Branch: s.Branch}, // nothing to ask
	}
	facts := d.observeLinkedPRs(context.Background(), s)
	if len(facts) != 1 || facts[0].name != "api" {
		t.Fatalf("facts = %+v", facts)
	}
	snapshot := s.Linked
	cur := s
	if !applyLinkedPRs(&cur, facts, time.Now()) {
		t.Fatal("a new linked PR must count as a change")
	}
	if cur.Linked[0].PR != pr || cur.Linked[1].PR.Number != 3 {
		t.Fatalf("linked after apply = %+v", cur.Linked)
	}
	if snapshot[0].PR != nil {
		t.Fatal("apply mutated a shared snapshot in place")
	}
	if applyLinkedPRs(&cur, facts, time.Now()) {
		t.Fatal("unchanged facts reported as a change")
	}
}

func TestMergedCleanupWaitsForOpenLinkedPRs(t *testing.T) {
	nat := &fakeNative{}
	d := newTestDaemon(t, reactTestConfig(nativePoll("p1")), &linear.Fake{}, nat)
	(&fakeReactSeams{}).install(d)

	pr := openPR(7, "MERGEABLE", "APPROVED", "pass")
	pr.State = "MERGED"
	s := reactSess("FE-1", "merged", pr)
	s.SetDelivery(state.DeliveryMerged, time.Now())
	s.Linked = []session.LinkedRepo{{Name: "api", Repo: "acme/api", Branch: s.Branch, PR: &scm.PR{Number: 9, State: "OPEN"}}}
	d.sessions.Upsert(s)

	d.react(context.Background(), s)
	if calls := nat.killCalls(); len(calls) != 0 {
		t.Fatalf("cleanup ran while a linked PR is open: %+v", calls)
	}

	s.Linked[0].PR = &scm.PR{Number: 9, State: "MERGED"}
	d.sessions.Upsert(s)
	d.react(context.Background(), s)
	if calls := nat.killCalls(); len(calls) != 1 {
		t.Fatalf("cleanup did not run once every linked PR settled: %+v", calls)
	}
}
