//go:build lola_insecure

package daemon

import (
	"context"
	"encoding/json"
	"net"
	"strings"
	"testing"

	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/panebus"
	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/remote"
)

const pairBeginTestKey = "pairbegin-test-key-not-a-real-secret"

// startPairBeginListener brings a real M1 listener up on a free loopback port,
// which is what makes these tests worth writing: pairBegin's whole reason for
// existing is that it answers about the listener that is RUNNING rather than
// about a file or a config value, so a test that stubbed the server would be
// testing the opposite of the claim.
func startPairBeginListener(t *testing.T) *Daemon {
	t.Helper()
	t.Setenv(remote.InsecureKeyEnv, pairBeginTestKey)

	d := newRemoteTestDaemon(t)
	d.cfg.Remote = config.RemoteConfig{Enabled: true, Bind: "localhost", Port: freePort(t)}
	fake := panebus.NewFake()
	d.paneRegistry = func() *panebus.Registry { return fake.Registry() }

	d.startRemote(context.Background())
	t.Cleanup(d.stopRemote)
	if d.remote == nil {
		t.Fatal("the M1 build should have started a listener")
	}
	return d
}

func TestPairBeginDescribesTheRunningListener(t *testing.T) {
	d := startPairBeginListener(t)

	got, err := d.handlePairBegin(context.Background())
	if err != nil {
		t.Fatalf("pairBegin: %v", err)
	}
	if got.Problem != "" {
		t.Fatalf("unexpected problem: %s", got.Problem)
	}
	if !got.Insecure {
		t.Error("an M1 code must say it carries a shared bearer key")
	}

	// The facts must come from the listener, not from the configuration: the
	// pin is the identity this process loaded and the port is the one actually
	// bound.
	if got.Pin != d.remote.SPKIPin() {
		t.Errorf("pin %q is not the listener's %q", got.Pin, d.remote.SPKIPin())
	}
	if got.Port != d.cfg.Remote.ListenPort() {
		t.Errorf("port %d, want the bound %d", got.Port, d.cfg.Remote.ListenPort())
	}
	if got.Key != pairBeginTestKey {
		t.Error("the key must be the one this listener authenticates with")
	}

	// Every host is loopback, because the M1 build forces the bind there.
	if len(got.Hosts) == 0 {
		t.Fatal("no hosts")
	}
	for _, h := range got.Hosts {
		if h != "127.0.0.1" && h != "::1" {
			t.Errorf("an M1 code must only offer loopback, got %q", h)
		}
	}

	// And the code decodes back to exactly those facts, through the same codec
	// the phone runs.
	c, err := remote.DecodeConnectCode(got.Code)
	if err != nil {
		t.Fatalf("the code the daemon produced does not decode: %v", err)
	}
	if len(c.Addrs) == 0 || c.Addrs[0] != got.Hosts[0] || c.Port != got.Port || c.Pin != got.Pin || c.Key != got.Key {
		t.Errorf("the code and the loose fields disagree: %+v vs %+v", c, got)
	}
}

// TestPairBeginOffersOnlyLoopbackEvenWhenConfiguredForTheLAN pins the rail that
// makes M1 survivable from the connect-code side. remote.Listen overrides a
// "lan" bind, so a code that advertised a LAN address would be advertising one
// the daemon never bound — a scan that fails with no named reason.
func TestPairBeginOffersOnlyLoopbackEvenWhenConfiguredForTheLAN(t *testing.T) {
	t.Setenv(remote.InsecureKeyEnv, pairBeginTestKey)
	d := newRemoteTestDaemon(t)
	d.cfg.Remote = config.RemoteConfig{Enabled: true, Bind: "lan", Port: freePort(t)}
	fake := panebus.NewFake()
	d.paneRegistry = func() *panebus.Registry { return fake.Registry() }
	d.startRemote(context.Background())
	t.Cleanup(d.stopRemote)

	got, err := d.handlePairBegin(context.Background())
	if err != nil {
		t.Fatalf("pairBegin: %v", err)
	}
	for _, h := range got.Hosts {
		if h != "127.0.0.1" && h != "::1" {
			t.Fatalf("bind=lan still produced a routable host %q in the connect code", h)
		}
	}
}

// TestPairBeginIsARenderedStateWhenNothingIsListening: the caller is a human at
// a button, and "the listener is off" is something they can act on, whereas a
// failed call with no payload is not.
func TestPairBeginIsARenderedStateWhenNothingIsListening(t *testing.T) {
	d := newRemoteTestDaemon(t)
	d.cfg.Remote = config.RemoteConfig{} // disabled

	got, err := d.handlePairBegin(context.Background())
	if err != nil {
		t.Fatalf("a disabled listener must not be an error: %v", err)
	}
	if got.Problem == "" {
		t.Fatal("expected a problem naming the disabled listener")
	}
	if got.Code != "" || got.Key != "" {
		t.Fatal("no listener means no code and no key")
	}
	if !strings.Contains(got.Problem, "[remote]") {
		t.Errorf("the problem should name what to enable, got %q", got.Problem)
	}
}

// TestPairBeginNeverLogsTheKey is the discipline test. d.logf reaches
// ~/.lola/daemon.log AND stderr, and the response carries a bearer key; a
// single well-meaning "handing out a connect code for 127.0.0.1" line that
// interpolated the wrong variable would persist the secret to disk.
func TestPairBeginNeverLogsTheKey(t *testing.T) {
	t.Setenv(remote.InsecureKeyEnv, pairBeginTestKey)
	d, logBuf := newLoggingRemoteDaemon(t)
	d.cfg.Remote = config.RemoteConfig{Enabled: true, Bind: "localhost", Port: freePort(t)}
	fake := panebus.NewFake()
	d.paneRegistry = func() *panebus.Registry { return fake.Registry() }
	d.startRemote(context.Background())
	t.Cleanup(d.stopRemote)

	before := logBuf.Len()
	got, err := d.handlePairBegin(context.Background())
	if err != nil {
		t.Fatalf("pairBegin: %v", err)
	}
	written := logBuf.String()[before:]
	if strings.Contains(written, pairBeginTestKey) {
		t.Fatal("the daemon log carries the bearer key")
	}
	if strings.Contains(written, got.Code) && got.Code != "" {
		t.Fatal("the daemon log carries the connect code, which contains the key")
	}
}

// TestPairBeginOverTheSocketDispatcher proves the case is wired into d.handle,
// so a client gets PairBeginData rather than `unknown cmd`.
func TestPairBeginOverTheSocketDispatcher(t *testing.T) {
	d := startPairBeginListener(t)

	resp := d.handle(context.Background(), protocol.Request{Cmd: "pairBegin"})
	if !resp.OK {
		t.Fatalf("pairBegin over the dispatcher: %s", resp.Error)
	}
	var data protocol.PairBeginData
	if err := json.Unmarshal(resp.Data, &data); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if data.Code == "" {
		t.Fatal("the dispatcher returned no code")
	}
	if _, err := remote.DecodeConnectCode(data.Code); err != nil {
		t.Fatalf("the dispatched code does not decode: %v", err)
	}
}

// TestPairBeginIsDeniedForEveryRemotePeer. Adding a case to d.handle must grant
// a phone nothing, and pairBegin is the case where that matters most: it hands
// out the credential a device would need to enrol a second one.
func TestPairBeginIsDeniedForEveryRemotePeer(t *testing.T) {
	if !remote.CommandDenied("pairBegin") {
		t.Fatal("pairBegin must be refused for every remote peer")
	}
}

func TestListenerDialSkipsWhatItCannotSplit(t *testing.T) {
	hosts, port := listenerDial([]remote.BindAddr{
		{Addr: "not-a-host-port"},
		{Addr: "127.0.0.1:7717"},
		{Addr: "[::1]:7717", Iface: "lo0"},
		{Addr: "127.0.0.1:7717"}, // a duplicate host is offered once
		{Addr: "10.0.0.5:notaport"},
	})
	if strings.Join(hosts, ",") != "127.0.0.1,::1" {
		t.Fatalf("hosts = %v", hosts)
	}
	if port != 7717 {
		t.Fatalf("port = %d", port)
	}
}

// The listener refusing and the listener failing to BIND are different
// failures, and the app used to report the second whichever had happened. The
// first time it mattered the cause was a missing bearer key — nothing had been
// attempted against an address at all — and an operator was sent to the log to
// look for a bind error that did not exist. So the recorded reason is reported.
func TestPairBeginReportsWhyTheListenerRefused(t *testing.T) {
	d := newRemoteTestDaemon(t)
	d.cfg.Remote = config.RemoteConfig{Enabled: true, Bind: "localhost", Port: freePort(t)}
	d.remoteErr = "no authorizer in this build: something specific went wrong"

	got, err := d.handlePairBegin(context.Background())
	if err != nil {
		t.Fatalf("a refused listener must not be an error: %v", err)
	}
	if !strings.Contains(got.Problem, "no authorizer") {
		t.Errorf("the recorded reason was not reported, got %q", got.Problem)
	}
	if strings.Contains(got.Problem, "could not bind") {
		t.Errorf("the problem still guesses at a bind failure: %q", got.Problem)
	}
	if got.Code != "" || got.Key != "" {
		t.Fatal("no listener means no code and no key")
	}
}

// With no reason on record the answer says so, rather than inventing one. A
// daemon still starting is the honest reading, and it is what the sentence says.
func TestPairBeginSaysWhenItHasNoReason(t *testing.T) {
	d := newRemoteTestDaemon(t)
	d.cfg.Remote = config.RemoteConfig{Enabled: true, Bind: "localhost", Port: freePort(t)}

	got, err := d.handlePairBegin(context.Background())
	if err != nil {
		t.Fatalf("pairBegin: %v", err)
	}
	if got.Problem == "" {
		t.Fatal("expected a problem")
	}
	if strings.Contains(got.Problem, "could not bind") {
		t.Errorf("guessed at a bind failure with nothing on record: %q", got.Problem)
	}
}

// A listener that comes up must CLEAR any earlier reason, or the next failure
// would be described with a stale one — worse than no reason at all.
func TestStartRemoteClearsAPreviousReason(t *testing.T) {
	t.Setenv(remote.InsecureKeyEnv, pairBeginTestKey)
	d := newRemoteTestDaemon(t)
	d.cfg.Remote = config.RemoteConfig{Enabled: true, Bind: "localhost", Port: freePort(t)}
	d.remoteErr = "a stale reason from an earlier attempt"

	fake := panebus.NewFake()
	d.paneRegistry = func() *panebus.Registry { return fake.Registry() }
	d.startRemote(context.Background())
	t.Cleanup(d.stopRemote)

	d.remoteMu.Lock()
	left := d.remoteErr
	d.remoteMu.Unlock()
	if left != "" {
		t.Errorf("a started listener left %q on record", left)
	}
}

// Rolling the key is M1's only revocation, so the key on disk must actually
// change and the listener must come back up around the new one — a command
// that rolled the file and left the old key working would report a revocation
// that had not happened.
func TestRegenerateRemoteKeyRollsTheKeyAndRebuildsTheListener(t *testing.T) {
	d := newRemoteTestDaemon(t)
	d.cfg.Remote = config.RemoteConfig{Enabled: true, Bind: "localhost", Port: freePort(t)}
	fake := panebus.NewFake()
	d.paneRegistry = func() *panebus.Registry { return fake.Registry() }
	d.startRemote(context.Background())
	t.Cleanup(d.stopRemote)

	before, err := d.handlePairBegin(context.Background())
	if err != nil || before.Key == "" {
		t.Fatalf("pairBegin before: %v (key %q)", err, before.Key)
	}

	if err := d.handleRegenerateRemoteKey(context.Background()); err != nil {
		t.Fatalf("regenerate: %v", err)
	}

	after, err := d.handlePairBegin(context.Background())
	if err != nil {
		t.Fatalf("pairBegin after: %v", err)
	}
	if after.Key == "" {
		t.Fatal("the listener did not come back up around a new key")
	}
	if after.Key == before.Key {
		t.Fatal("the key did not change, so no phone was disconnected")
	}
}

// A wildcard bind reports 0.0.0.0 / [::], and neither can be dialled. Handing
// one to a phone produces a code that scans perfectly and then times out — the
// worst failure shape available, because it looks like every other network
// problem. So the unspecified address is replaced by addresses that exist.
func TestListenerDialReplacesAWildcardWithReachableAddresses(t *testing.T) {
	for _, wildcard := range []string{"0.0.0.0:7717", "[::]:7717"} {
		t.Run(wildcard, func(t *testing.T) {
			hosts, port := listenerDial([]remote.BindAddr{{Addr: wildcard}})
			if port != 7717 {
				t.Fatalf("port = %d, want 7717", port)
			}
			if len(hosts) == 0 {
				t.Fatal("a wildcard produced no dialable host at all")
			}
			for _, h := range hosts {
				ip := net.ParseIP(h)
				if ip == nil {
					t.Errorf("host %q does not parse as an address", h)
					continue
				}
				if ip.IsUnspecified() {
					t.Errorf("host %q is still a wildcard", h)
				}
				if strings.Contains(h, "%") {
					t.Errorf("host %q carries a zone, which names an interface on THIS machine", h)
				}
			}
			// Loopback is always there, because a Simulator shares the Mac's
			// loopback and is the one client for which it is correct.
			found := false
			for _, h := range hosts {
				if h == "127.0.0.1" {
					found = true
				}
			}
			if !found {
				t.Error("loopback was not offered, so a Simulator has nothing to dial")
			}
		})
	}
}

// An explicit address is passed through untouched: the substitution is only for
// the case where the bound address names nothing.
func TestListenerDialLeavesExplicitAddressesAlone(t *testing.T) {
	hosts, port := listenerDial([]remote.BindAddr{
		{Addr: "192.168.20.3:7717"},
		{Addr: "127.0.0.1:7717"},
	})
	if port != 7717 {
		t.Fatalf("port = %d", port)
	}
	want := []string{"192.168.20.3", "127.0.0.1"}
	if len(hosts) != len(want) {
		t.Fatalf("hosts = %v, want %v", hosts, want)
	}
	for i := range want {
		if hosts[i] != want[i] {
			t.Errorf("hosts = %v, want %v (bind order is preserved)", hosts, want)
		}
	}
}
