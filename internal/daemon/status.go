package daemon

import (
	"context"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/sushidev-team/lola/internal/agent"
	"github.com/sushidev-team/lola/internal/protocol"
)

// statusTracker holds live per-poll status for the status command.
type statusTracker struct {
	mu sync.Mutex
	m  map[string]*protocol.PollStatus
	// onChange is called after a mutation, with mu released. It must not block: it is a
	// watch-hub poke.
	onChange func()
}

// setOnChange installs the change hook; nil clears it.
func (t *statusTracker) setOnChange(fn func()) {
	t.mu.Lock()
	t.onChange = fn
	t.mu.Unlock()
}

// bindOnChange installs fn only when no hook is set, so a repeat call is harmless.
func (t *statusTracker) bindOnChange(fn func()) {
	t.mu.Lock()
	if t.onChange == nil {
		t.onChange = fn
	}
	t.mu.Unlock()
}

// changed runs the hook after the lock is released.
func (t *statusTracker) changed() {
	t.mu.Lock()
	fn := t.onChange
	t.mu.Unlock()
	if fn != nil {
		fn()
	}
}

// pokeStatusOnChange makes every tracker mutation a status/projects poke. Idempotent.
func (d *Daemon) pokeStatusOnChange() {
	if d.status == nil {
		return
	}
	d.status.bindOnChange(func() {
		if d.watch != nil {
			d.watch.PokeGlobal(pokeStatus | pokeProjects)
		}
	})
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
	t.ensure(name).Running = true
	t.mu.Unlock()
	t.changed()
}

func (t *statusTracker) end(name string, ranAt time.Time) {
	t.mu.Lock()
	ps := t.ensure(name)
	ps.Running = false
	ps.LastRun = ranAt
	t.mu.Unlock()
	t.changed()
}

func (t *statusTracker) setError(name, msg string) {
	t.mu.Lock()
	t.ensure(name).LastError = msg
	t.mu.Unlock()
	t.changed()
}

func (t *statusTracker) setLastSpawn(name string, at time.Time) {
	t.mu.Lock()
	t.ensure(name).LastSpawn = at
	t.mu.Unlock()
	t.changed()
}

func (t *statusTracker) get(name string) protocol.PollStatus {
	t.mu.Lock()
	defer t.mu.Unlock()
	return *t.ensure(name)
}

// statusData builds the reply for cmd=status: runtimeOk reflects the native
// runtime health check NOW (tmux/git resolvable and the default coding-agent
// binary on PATH), linearOk is the last known auth state.
func (d *Daemon) statusData(ctx context.Context) protocol.StatusData {
	// newDaemon binds the tracker's poke; binding again here (idempotent) covers a Daemon
	// assembled without newDaemon, which still pushes once anything has asked for status.
	d.pokeStatusOnChange()
	d.mu.Lock()
	linOK := d.linOK
	cfgErr := d.cfgErr
	health := d.runtimeHealth
	// cmd=status is not poll-scoped, so probe the DEFAULT agent's binary
	// ([defaults].agent → "claude"); a per-poll override is checked at that
	// poll's own tick.
	agentBin := agent.Parse(d.cfg.AgentForProject("")).Binary()
	type pollInfo struct {
		name    string
		enabled bool
	}
	polling := d.cfg.PollingProjects()
	polls := make([]pollInfo, 0, len(polling))
	for _, p := range polling {
		polls = append(polls, pollInfo{p.Name, p.Enabled})
	}
	d.mu.Unlock()

	runtimeErr := ""
	if health != nil {
		if err := health(agentBin); err != nil {
			runtimeErr = err.Error()
		}
	}
	sd := protocol.StatusData{
		RuntimeOK:  runtimeErr == "",
		RuntimeErr: runtimeErr,
		LinearOK:   linOK,
		Polls:      make([]protocol.PollStatus, 0, len(polls)),
		Host:       machineName(),
		Usage:      d.usageStatus(time.Now()),
	}
	sd.LinearAgent = d.linearAgentStatus()
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

// machineName is what a client calls this Mac.
//
// The ".local" suffix is trimmed because it is mDNS plumbing rather than part
// of the name anyone gave the machine, and a UI that says "connected to
// marvin" reads better than one saying "marvin.local". An unavailable or empty
// hostname answers "" and every caller falls back to naming the address, which
// is what they did before this field existed.
func machineName() string {
	host, err := os.Hostname()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(host), ".local"))
}
