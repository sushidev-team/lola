// Package portlease hands each session its OWN block of TCP ports, so several
// worktrees of one project can run their dev stacks side by side.
//
// Without it the only safe arrangement is one dev stack per project at a time:
// every worktree's `composer dev` wants the same port, and the second one either
// dies with "address already in use" or silently talks to the first one's
// checkout. A project that configures `port_range` instead gets a LEASE per
// session — ports_per_session consecutive ports out of that range — exported
// into the worktree as LOLA_PORT / LOLA_PORT_1..N, so the dev command binds
// what it was handed rather than what every other worktree also wants.
//
// The registry is the ONE source of truth for who holds what. It is persisted
// (`~/.lola/state/ports.json`) so a daemon restart does not hand a live
// session's ports to the next spawn, and keyed by session ID so a revive, an
// agent switch or a teardown hook finds the same ports again. It is a stdlib
// leaf: the runtime acquires and releases, the daemon prunes and reports, and
// neither may import the other.
//
// Allocation is best-effort about the MACHINE and exact about lola: a block is
// never handed out twice while leased, and a port something else is already
// listening on is skipped (InUse), but nothing stops an unrelated process from
// binding a leased port later. That is the clash detection's job, not this one.
package portlease

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"sync"
	"time"
)

// Lease is one session's block of ports.
type Lease struct {
	Session string    `json:"session"`
	Project string    `json:"project,omitempty"`
	Ports   []int     `json:"ports"`
	Created time.Time `json:"created"`
}

// Port is the lease's first (primary) port, 0 for an empty lease.
func (l Lease) Port() int {
	if len(l.Ports) == 0 {
		return 0
	}
	return l.Ports[0]
}

// ErrExhausted reports that no free block of the requested size is left in
// the range.
var ErrExhausted = errors.New("port range exhausted")

// Registry is the persisted set of leases. The zero value is not usable; build
// one with Open. Safe for concurrent use within one process — the daemon is the
// only writer.
type Registry struct {
	path string

	// InUse reports whether something on this machine already listens on port.
	// nil uses ListeningOn. Tests replace it so allocation never touches the
	// network.
	InUse func(port int) bool
	// Now is the clock stamped on new leases; nil uses time.Now.
	Now func() time.Time

	mu     sync.Mutex
	leases map[string]Lease
}

// Open loads the registry at path. A missing file is an empty registry; an
// unreadable or corrupt one is ALSO returned as an empty registry, with the
// error, so the caller can log it and keep spawning — a lost lease file costs
// at worst one collision, never a session.
func Open(path string) (*Registry, error) {
	r := &Registry{path: path, leases: map[string]Lease{}}
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return r, nil
	}
	if err != nil {
		return r, fmt.Errorf("portlease: read %s: %w", path, err)
	}
	var file struct {
		Leases []Lease `json:"leases"`
	}
	if err := json.Unmarshal(data, &file); err != nil {
		return r, fmt.Errorf("portlease: parse %s: %w", path, err)
	}
	for _, l := range file.Leases {
		if l.Session != "" {
			r.leases[l.Session] = l
		}
	}
	return r, nil
}

// Get returns the session's lease, if it holds one.
func (r *Registry) Get(session string) (Lease, bool) {
	if r == nil {
		return Lease{}, false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	l, ok := r.leases[session]
	return l, ok
}

// All returns every lease, sorted by session ID.
func (r *Registry) All() []Lease {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Lease, 0, len(r.leases))
	for _, l := range r.leases {
		out = append(out, l)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Session < out[j].Session })
	return out
}

// Acquire returns session's lease of n consecutive ports inside [lo, hi].
//
// It is IDEMPOTENT: a session that already holds a lease of exactly that shape
// gets it back unchanged, so a revive or an agent switch keeps the ports its
// dev stack (and whatever post_create wrote) was configured with. A lease that
// no longer fits — the range or the count changed in config — is replaced.
//
// Blocks are aligned to lo in steps of n and scanned from the bottom, so the
// lowest free block wins and a session's ports are always lo+k·n … lo+k·n+n-1.
// A block is skipped when any of its ports is leased by ANY session (two
// projects' ranges may overlap) or is already listened on. The lease is
// persisted before it is returned; a failed write releases nothing and returns
// the error, because a lease the next daemon cannot see is one it will hand out
// again.
func (r *Registry) Acquire(session, project string, lo, hi, n int) (Lease, error) {
	if r == nil {
		return Lease{}, errors.New("portlease: no registry")
	}
	if session == "" {
		return Lease{}, errors.New("portlease: session required")
	}
	if n < 1 || lo < 1 || hi > 65535 || lo > hi || hi-lo+1 < n {
		return Lease{}, fmt.Errorf("portlease: invalid request: %d port(s) in %d-%d", n, lo, hi)
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if cur, ok := r.leases[session]; ok && fits(cur, lo, hi, n) {
		return cur, nil
	}

	taken := map[int]bool{}
	for id, l := range r.leases {
		if id == session {
			continue // the caller's own stale lease is being replaced
		}
		for _, p := range l.Ports {
			taken[p] = true
		}
	}
	inUse := r.InUse
	if inUse == nil {
		inUse = ListeningOn
	}

	for base := lo; base+n-1 <= hi; base += n {
		ports := make([]int, n)
		free := true
		for i := range n {
			p := base + i
			if taken[p] || inUse(p) {
				free = false
				break
			}
			ports[i] = p
		}
		if !free {
			continue
		}
		l := Lease{Session: session, Project: project, Ports: ports, Created: r.now()}
		prev, had := r.leases[session]
		r.leases[session] = l
		if err := r.saveLocked(); err != nil {
			if had {
				r.leases[session] = prev
			} else {
				delete(r.leases, session)
			}
			return Lease{}, err
		}
		return l, nil
	}
	return Lease{}, fmt.Errorf("portlease: %d port(s) in %d-%d: %w", n, lo, hi, ErrExhausted)
}

// fits reports whether l is a lease of n consecutive ports inside [lo, hi].
func fits(l Lease, lo, hi, n int) bool {
	if len(l.Ports) != n {
		return false
	}
	for i, p := range l.Ports {
		if p < lo || p > hi || p != l.Ports[0]+i {
			return false
		}
	}
	return true
}

// Release frees session's lease. Releasing a session that holds none is not an
// error.
func (r *Registry) Release(session string) error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	prev, ok := r.leases[session]
	if !ok {
		return nil
	}
	delete(r.leases, session)
	if err := r.saveLocked(); err != nil {
		r.leases[session] = prev
		return err
	}
	return nil
}

// Prune releases every lease whose session keep rejects AND that is older than
// grace, and returns the sessions it released. The grace covers a spawn in
// flight: the runtime leases ports before the session reaches the store, and a
// prune landing in that window must not take them back.
func (r *Registry) Prune(keep func(session string) bool, grace time.Duration) ([]string, error) {
	if r == nil {
		return nil, nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	cutoff := r.now().Add(-grace)
	var dropped []string
	removed := map[string]Lease{}
	for id, l := range r.leases {
		if keep(id) || l.Created.After(cutoff) {
			continue
		}
		removed[id] = l
		delete(r.leases, id)
		dropped = append(dropped, id)
	}
	if len(dropped) == 0 {
		return nil, nil
	}
	if err := r.saveLocked(); err != nil {
		for id, l := range removed {
			r.leases[id] = l
		}
		return nil, err
	}
	slices.Sort(dropped)
	return dropped, nil
}

func (r *Registry) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

// saveLocked writes the registry atomically (temp + rename, 0600). r.mu held.
func (r *Registry) saveLocked() error {
	if r.path == "" {
		return nil // an in-memory registry (tests)
	}
	leases := make([]Lease, 0, len(r.leases))
	for _, l := range r.leases {
		leases = append(leases, l)
	}
	sort.Slice(leases, func(i, j int) bool { return leases[i].Session < leases[j].Session })
	data, err := json.MarshalIndent(struct {
		Leases []Lease `json:"leases"`
	}{leases}, "", "  ")
	if err != nil {
		return fmt.Errorf("portlease: encode: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(r.path), 0o700); err != nil {
		return fmt.Errorf("portlease: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(r.path), ".ports-*.json")
	if err != nil {
		return fmt.Errorf("portlease: %w", err)
	}
	name := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(name)
		return fmt.Errorf("portlease: write: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return fmt.Errorf("portlease: write: %w", err)
	}
	if err := os.Chmod(name, 0o600); err != nil {
		os.Remove(name)
		return fmt.Errorf("portlease: %w", err)
	}
	if err := os.Rename(name, r.path); err != nil {
		os.Remove(name)
		return fmt.Errorf("portlease: %w", err)
	}
	return nil
}

// ListeningOn reports whether something already accepts TCP connections on
// port, on loopback or on the wildcard address. It is a heuristic used to skip
// a block, never to grant one: a connect that succeeds is proof, and a bind
// that fails is treated as proof too (some servers bind only a non-loopback
// address). Both checks are local and fail fast.
func ListeningOn(port int) bool {
	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	if c, err := net.DialTimeout("tcp", addr, 150*time.Millisecond); err == nil {
		c.Close()
		return true
	}
	ln, err := net.Listen("tcp", ":"+strconv.Itoa(port))
	if err != nil {
		return true
	}
	ln.Close()
	return false
}
