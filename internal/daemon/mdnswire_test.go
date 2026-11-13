//go:build lola_insecure

package daemon

// The local-network advertisement, which only exists where a listener does —
// hence the build tag, as with every other test that starts one.

import (
	"context"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/mdns"
	"github.com/sushidev-team/lola/internal/panebus"
	"github.com/sushidev-team/lola/internal/remote"
)

// The advertisement carries the port and a version, and NOTHING that identifies
// this machine.
//
// mobile/PLAN.md argues the restraint: `_lola._tcp` already announces "this
// machine runs autonomous coding agents and accepts remote control" to every
// peer on the network, and an SPKI pin or a hostname in a TXT record is a
// stable cross-network correlator for one operator's laptop. The bearer key is
// a separate and absolute rule — a TXT record is readable by everything.
func TestStartRemoteAdvertisesTheListener(t *testing.T) {
	t.Setenv(remote.InsecureKeyEnv, pairBeginTestKey)

	var mu sync.Mutex
	var launched [][]string
	d := newRemoteTestDaemon(t)
	d.mdnsStart = func(ctx context.Context, bin string, args []string) (mdns.Process, error) {
		mu.Lock()
		launched = append(launched, append([]string(nil), args...))
		mu.Unlock()
		return stubMDNS{ctx: ctx}, nil
	}
	port := freePort(t)
	d.cfg.Remote = config.RemoteConfig{Enabled: true, Bind: "localhost", Port: port, Advertise: true}
	fake := panebus.NewFake()
	d.paneRegistry = func() *panebus.Registry { return fake.Registry() }

	d.startRemote(context.Background())
	t.Cleanup(d.stopRemote)
	if d.remote == nil {
		t.Fatal("no listener")
	}

	var args []string
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		if len(launched) > 0 {
			args = launched[0]
		}
		mu.Unlock()
		if args != nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if args == nil {
		t.Fatal("the listener was never advertised")
	}

	joined := strings.Join(args, " ")
	if !strings.Contains(joined, mdns.ServiceType) {
		t.Errorf("service type missing from %q", joined)
	}
	if !strings.Contains(joined, strconv.Itoa(port)) {
		t.Errorf("the bound port is not advertised: %q", joined)
	}
	if !strings.Contains(joined, mdns.TXTVersion+"="+mdns.Version) {
		t.Errorf("the protocol version is not advertised: %q", joined)
	}

	// THE THINGS THAT MUST NEVER BE THERE.
	if strings.Contains(joined, pairBeginTestKey) {
		t.Fatal("the bearer key reached a network advertisement")
	}
	if strings.Contains(joined, d.remote.SPKIPin()) {
		t.Error("the SPKI pin reached a network advertisement: a cross-network correlator")
	}
	if host, err := os.Hostname(); err == nil && host != "" {
		base := strings.TrimSuffix(host, ".local")
		if base != "" && strings.Contains(joined, base) {
			t.Errorf("the hostname reached a network advertisement: %q", joined)
		}
	}
}

// Advertising is OPT-IN. `_lola._tcp` on a shared network is a disclosure about
// what this machine is, so a daemon that was not asked to advertise must be
// silent — and a listener still comes up either way.
func TestStartRemoteDoesNotAdvertiseUnlessAsked(t *testing.T) {
	t.Setenv(remote.InsecureKeyEnv, pairBeginTestKey)

	started := make(chan struct{}, 4)
	d := newRemoteTestDaemon(t)
	d.mdnsStart = func(ctx context.Context, _ string, _ []string) (mdns.Process, error) {
		started <- struct{}{}
		return stubMDNS{ctx: ctx}, nil
	}
	d.cfg.Remote = config.RemoteConfig{Enabled: true, Bind: "localhost", Port: freePort(t)}
	fake := panebus.NewFake()
	d.paneRegistry = func() *panebus.Registry { return fake.Registry() }

	d.startRemote(context.Background())
	t.Cleanup(d.stopRemote)
	if d.remote == nil {
		t.Fatal("the listener must come up whether or not it is advertised")
	}

	select {
	case <-started:
		t.Fatal("a daemon that was not asked to advertise did so anyway")
	case <-time.After(200 * time.Millisecond):
	}
}

// The advertisement is withdrawn with the listener. One that outlived its
// socket would point every browsing phone at a port that refuses.
func TestStopRemoteWithdrawsTheAdvertisement(t *testing.T) {
	t.Setenv(remote.InsecureKeyEnv, pairBeginTestKey)

	ended := make(chan struct{}, 4)
	d := newRemoteTestDaemon(t)
	d.mdnsStart = func(ctx context.Context, _ string, _ []string) (mdns.Process, error) {
		go func() {
			<-ctx.Done()
			ended <- struct{}{}
		}()
		return stubMDNS{ctx: ctx}, nil
	}
	d.cfg.Remote = config.RemoteConfig{
		Enabled: true, Bind: "localhost", Port: freePort(t), Advertise: true,
	}
	fake := panebus.NewFake()
	d.paneRegistry = func() *panebus.Registry { return fake.Registry() }

	d.startRemote(context.Background())
	d.stopRemote()

	select {
	case <-ended:
	case <-time.After(2 * time.Second):
		t.Fatal("the advertisement outlived the listener")
	}
}
