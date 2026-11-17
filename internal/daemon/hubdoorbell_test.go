package daemon

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/hub"
)

// doorbellHub is a hub client whose long-poll answers from a script.
type doorbellHub struct {
	*fakeHub
	mu     sync.Mutex
	values []uint64
	errs   []error
	asked  []uint64
}

func (h *doorbellHub) Doorbell(_ context.Context, since uint64) (uint64, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.asked = append(h.asked, since)
	if len(h.errs) > 0 {
		e := h.errs[0]
		h.errs = h.errs[1:]
		if e != nil {
			return 0, e
		}
	}
	v := h.values[0]
	if len(h.values) > 1 {
		h.values = h.values[1:]
	}
	return v, nil
}

func doorbellDaemon(t *testing.T, bell *doorbellHub) *Daemon {
	t.Helper()
	d, _, _ := hubFixture(t)
	d.cfg.LinearAgent = config.LinearAgentConfig{Enabled: true}
	d.hub.newClient = func(url, _ string) (hubAPI, hub.Identity, error) {
		return bell, hub.Identity{URL: url, DeviceID: "dev", Org: "acme"}, nil
	}
	d.agent = newLinearAgentRuntime()
	return d
}

func rung(d *Daemon) bool {
	select {
	case <-d.agent.wake:
		return true
	default:
		return false
	}
}

// The first answer only seeds the baseline; a later change rings the agent loop;
// an unchanged answer does not.
func TestHubDoorbellRingsOnlyOnAChange(t *testing.T) {
	bell := &doorbellHub{fakeHub: &fakeHub{}, values: []uint64{7, 7, 8}}
	d := doorbellDaemon(t, bell)
	since, backoff := uint64(doorbellSeed), doorbellBackoffMin

	d.hubDoorbellOnce(context.Background(), &since, &backoff)
	if rung(d) || since != 7 {
		t.Fatalf("the seed must not ring: since=%d", since)
	}
	d.hubDoorbellOnce(context.Background(), &since, &backoff)
	if rung(d) {
		t.Fatal("an unchanged counter must not ring")
	}
	d.hubDoorbellOnce(context.Background(), &since, &backoff)
	if !rung(d) || since != 8 {
		t.Fatalf("a changed counter must ring: since=%d", since)
	}
	if bell.asked[0] != doorbellSeed || bell.asked[1] != 7 || bell.asked[2] != 7 {
		t.Fatalf("each poll asks with the last value: %v", bell.asked)
	}
}

// With the Linear agent (or the hub) off nothing is asked, and re-enabling does
// not ring for old news.
func TestHubDoorbellIdlesWhenDisabledAndReseeds(t *testing.T) {
	bell := &doorbellHub{fakeHub: &fakeHub{}, values: []uint64{3, 9}}
	d := doorbellDaemon(t, bell)
	since, backoff := uint64(5), doorbellBackoffMin

	d.cfg.LinearAgent.Enabled = false
	if wait := d.hubDoorbellOnce(context.Background(), &since, &backoff); wait != doorbellIdle || len(bell.asked) != 0 || since != doorbellSeed {
		t.Fatalf("disabled: wait=%v asked=%v since=%d", wait, bell.asked, since)
	}
	d.cfg.LinearAgent.Enabled = true
	d.hubDoorbellOnce(context.Background(), &since, &backoff)
	if rung(d) {
		t.Fatal("enabling must re-seed, not ring")
	}
}

// Errors back off and never ring; a hub without the endpoint is left alone.
func TestHubDoorbellBacksOffOnErrors(t *testing.T) {
	bell := &doorbellHub{fakeHub: &fakeHub{}, values: []uint64{1}, errs: []error{errors.New("down"), errors.New("down"), &hub.APIError{Status: 404}}}
	d := doorbellDaemon(t, bell)
	since, backoff := uint64(doorbellSeed), doorbellBackoffMin

	if w := d.hubDoorbellOnce(context.Background(), &since, &backoff); w != doorbellBackoffMin {
		t.Fatalf("first failure waits %v", w)
	}
	if w := d.hubDoorbellOnce(context.Background(), &since, &backoff); w != 2*doorbellBackoffMin {
		t.Fatalf("second failure waits %v", w)
	}
	if w := d.hubDoorbellOnce(context.Background(), &since, &backoff); w != doorbellNoHub {
		t.Fatalf("a 404 hub waits %v", w)
	}
	if rung(d) {
		t.Fatal("errors must not ring")
	}
	_ = time.Second
}
