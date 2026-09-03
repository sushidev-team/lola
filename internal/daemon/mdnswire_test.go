//go:build lola_insecure

package daemon

// The local-network advertisement, which only exists where a listener does —
// hence the build tag, as with every other test that starts one.

import (
	"context"
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

// The listener is advertised on the local network, and the advertisement says
// exactly two things a phone needs and nothing it must not learn.
//
// A phone's credentials — the bearer key and the SPKI pin — name the DAEMON,
// not an address, so the only thing that goes stale when a laptop changes
// network is where to dial. Discovery closes that gap. The bearer key must
// never be in it: a TXT record is readable by everything on the network.
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
	d.cfg.Remote = config.RemoteConfig{Enabled: true, Bind: "localhost", Port: port}
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
	if !strings.Contains(joined, mdns.TXTPin+"="+d.remote.SPKIPin()) {
		t.Errorf("the SPKI pin is not advertised: %q", joined)
	}
	// THE ONE THING THAT MUST NEVER BE THERE.
	if strings.Contains(joined, pairBeginTestKey) {
		t.Fatal("the bearer key reached a network advertisement")
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
	d.cfg.Remote = config.RemoteConfig{Enabled: true, Bind: "localhost", Port: freePort(t)}
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
