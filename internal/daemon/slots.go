package daemon

import (
	"sync"

	"github.com/sushidev-team/lola/internal/session"
	"github.com/sushidev-team/lola/internal/state"
)

// noProjectCap tells reserveSlots to apply global_cap only: a launch a human
// asked for (best-of) or a goal attempt is not bound by the project's
// concurrency_cap, but it still must not overshoot the machine-wide one.
const noProjectCap = -1

// slotLedger holds the slots of spawns still in flight. A spawn upserts its
// session only when it returns, and that can take minutes (worktree +
// post_create, see nativeSpawnTimeout). Every poll ticks on its own goroutine
// under its own tick mutex, so without this ledger two polls reading the store
// in that window each saw the same free slots and together spawned past
// global_cap. A reservation is taken and checked under one lock, and released
// once the spawn returned (its session, if any, is in the store by then).
type slotLedger struct {
	mu        sync.Mutex
	total     int
	byProject map[string]int
}

// liveSlots counts the slot-holding native sessions of snap (state.HoldsSlot,
// the one slot classification): all of them, and those of project.
func liveSlots(snap []session.Session, project string) (global, own int) {
	for _, s := range snap {
		if s.Source != "native" {
			continue
		}
		s.EnsureAxes()
		if !state.HoldsSlot(s.AgentState, s.Delivery) {
			continue
		}
		global++
		if s.Project == project {
			own++
		}
	}
	return global, own
}

// slotCounts returns liveSlots plus the reservations still in flight.
func (d *Daemon) slotCounts(project string) (global, own int) {
	d.slots.mu.Lock()
	defer d.slots.mu.Unlock()
	return d.slotCountsLocked(project)
}

func (d *Daemon) slotCountsLocked(project string) (global, own int) {
	global, own = liveSlots(d.sessions.Snapshot(), project)
	return global + d.slots.total, own + d.slots.byProject[project]
}

// reserveSlots claims cost slots for project when they fit under both caps
// (projectCap = noProjectCap skips the project's), counting live sessions AND
// every other spawn still in flight. ok=false reserves nothing. The caller
// must call release exactly once, after the spawn returned.
func (d *Daemon) reserveSlots(project string, cost, projectCap, globalCap int) (release func(), ok bool) {
	d.slots.mu.Lock()
	defer d.slots.mu.Unlock()
	global, own := d.slotCountsLocked(project)
	if global+cost > globalCap || (projectCap != noProjectCap && own+cost > projectCap) {
		return func() {}, false
	}
	if d.slots.byProject == nil {
		d.slots.byProject = map[string]int{}
	}
	d.slots.total += cost
	d.slots.byProject[project] += cost
	var once sync.Once
	return func() {
		once.Do(func() {
			d.slots.mu.Lock()
			defer d.slots.mu.Unlock()
			d.slots.total -= cost
			if d.slots.byProject[project] -= cost; d.slots.byProject[project] <= 0 {
				delete(d.slots.byProject, project)
			}
		})
	}, true
}
