package daemon

import (
	"context"
	"sync"
	"time"

	"github.com/you/aop/internal/protocol"
)

// statusTracker holds live per-poll status for the status command.
type statusTracker struct {
	mu sync.Mutex
	m  map[string]*protocol.PollStatus
}

func newStatusTracker() *statusTracker {
	return &statusTracker{m: map[string]*protocol.PollStatus{}}
}

// ensure returns the entry for name; caller must hold t.mu.
func (t *statusTracker) ensure(name string) *protocol.PollStatus {
	ps, ok := t.m[name]
	if !ok {
		ps = &protocol.PollStatus{Name: name}
		t.m[name] = ps
	}
	return ps
}

func (t *statusTracker) begin(name string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.ensure(name).Running = true
}

func (t *statusTracker) end(name string, ranAt time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	ps := t.ensure(name)
	ps.Running = false
	ps.LastRun = ranAt
}

func (t *statusTracker) setError(name, msg string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.ensure(name).LastError = msg
}

func (t *statusTracker) setLastSpawn(name string, at time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.ensure(name).LastSpawn = at
}

func (t *statusTracker) get(name string) protocol.PollStatus {
	t.mu.Lock()
	defer t.mu.Unlock()
	return *t.ensure(name)
}

// statusData builds the reply for cmd=status: aoRunning is probed NOW,
// linearOk is the last known auth state.
func (d *Daemon) statusData(ctx context.Context) protocol.StatusData {
	d.mu.Lock()
	linOK := d.linOK
	cfgErr := d.cfgErr
	aoc := d.aoc // snapshot under d.mu: reload may swap the client concurrently
	type pollInfo struct {
		name    string
		enabled bool
	}
	polls := make([]pollInfo, 0, len(d.cfg.Polls))
	for _, p := range d.cfg.Polls {
		polls = append(polls, pollInfo{p.Name, p.Enabled})
	}
	d.mu.Unlock()

	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	sd := protocol.StatusData{
		AORunning: aoc.Reachable(cctx),
		LinearOK:  linOK,
		Polls:     make([]protocol.PollStatus, 0, len(polls)),
	}
	for _, p := range polls {
		ps := d.status.get(p.name)
		ps.Name = p.name
		ps.Enabled = p.enabled
		if cfgErr != "" {
			// Polls are held while the config is invalid; never fail silently.
			ps.LastError = cfgErr
		}
		sd.Polls = append(sd.Polls, ps)
	}
	return sd
}
