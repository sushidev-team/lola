package daemon

import (
	"context"
	"errors"
	"math"
	"time"

	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/hub"
)

// The hub's Linear doorbell (hub README, "Linear webhook doorbell"). A laptop
// has no public address for Linear's webhook; the hub does. It verifies the
// delivery and bumps a per-org counter, and this loop holds a signed long-poll
// on that counter: a change means "poll Linear now" (wakeLinearAgent) — the
// same doorbell the local webhook rings, and just as unable to carry anything
// else. The counter's value is compared with the previous one and never
// interpreted, so a compromised hub can only make the agent loop poll early.
//
// It runs only while [hub] is enabled AND [linear_agent] is enabled, and every
// failure is quiet: the 15s poll is the floor, this only shortens it.

// hubDoorbell is the optional capability of a hub client (the interface hubAPI
// is deliberately not widened: every test fake would have to grow it).
type hubDoorbell interface {
	Doorbell(ctx context.Context, since uint64) (uint64, error)
}

const (
	doorbellIdle       = 30 * time.Second // waiting for the config to turn it on
	doorbellBackoffMin = 10 * time.Second
	doorbellBackoffMax = 2 * time.Minute
	doorbellNoHub      = 10 * time.Minute // a hub without the endpoint (404)
	// since value that never matches a real counter: the first answer is
	// immediate and only seeds the baseline.
	doorbellSeed = math.MaxUint64
)

func (d *Daemon) hubDoorbellLoop(ctx context.Context) {
	defer d.wg.Done()
	defer func() {
		if r := recover(); r != nil {
			d.logf("", "hub doorbell panic (daemon keeps running): %v", r)
		}
	}()
	since := uint64(doorbellSeed)
	backoff := doorbellBackoffMin
	for {
		wait := d.hubDoorbellOnce(ctx, &since, &backoff)
		if wait <= 0 {
			continue
		}
		t := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-t.C:
		}
	}
}

// hubDoorbellOnce makes one long-poll and returns how long to wait before the
// next (0 = go again at once).
func (d *Daemon) hubDoorbellOnce(ctx context.Context, since *uint64, backoff *time.Duration) time.Duration {
	if ctx.Err() != nil {
		return time.Second
	}
	d.mu.Lock()
	on := d.cfg != nil && d.cfg.Hub.Enabled && d.cfg.LinearAgent.Enabled && d.cfgErr == ""
	var hubCfg config.HubConfig
	if d.cfg != nil {
		hubCfg = d.cfg.Hub
	}
	d.mu.Unlock()
	if !on {
		*since = doorbellSeed // a later enable re-seeds instead of ringing for old news
		return doorbellIdle
	}
	api, _, err := d.hub.newClient(hubCfg.URL, d.hubDir())
	if err != nil {
		return doorbellIdle // not joined (hubConnect reports it)
	}
	bell, ok := api.(hubDoorbell)
	if !ok {
		return doorbellNoHub
	}
	n, err := bell.Doorbell(ctx, *since)
	if err != nil {
		if ctx.Err() != nil {
			return time.Second
		}
		if hubStatusIs(err, 404) {
			return doorbellNoHub
		}
		if errors.Is(err, hub.ErrNotJoined) {
			return doorbellIdle
		}
		w := *backoff
		*backoff = min(*backoff*2, doorbellBackoffMax)
		return w
	}
	*backoff = doorbellBackoffMin
	if *since != doorbellSeed && n != *since {
		d.logf("", "hub doorbell: Linear event, polling the agent loop now")
		d.wakeLinearAgent()
	}
	*since = n
	// A floor, so a hub that answers at once forever cannot spin this loop.
	return 250 * time.Millisecond
}
