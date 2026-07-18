package daemon

import (
	"sync"
	"time"
)

// inflightEntry is a daemon-global dispatch claim on one issue.
type inflightEntry struct {
	Identifier string // Linear identifier (FE-231), for matching AO sessions
	AddedAt    time.Time
}

// inflightSet is the daemon-global in-flight set for cross-poll dedup,
// keyed by issue UUID. Entries are removed on spawn failure and by the
// reconcile pass once the issue has no counted AO session anymore.
type inflightSet struct {
	mu sync.Mutex
	m  map[string]inflightEntry
}

func newInflightSet() *inflightSet {
	return &inflightSet{m: map[string]inflightEntry{}}
}

// Add claims uuid, always overriding an existing entry.
func (s *inflightSet) Add(uuid, identifier string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[uuid] = inflightEntry{Identifier: identifier, AddedAt: time.Now()}
}

// Claim atomically claims uuid only if it is not already in-flight, returning
// true when it took the claim. Used by cmd=openTicket to dedup against a
// concurrent dispatch tick without a check-then-Add race.
func (s *inflightSet) Claim(uuid, identifier string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.m[uuid]; ok {
		return false
	}
	s.m[uuid] = inflightEntry{Identifier: identifier, AddedAt: time.Now()}
	return true
}

func (s *inflightSet) Has(uuid string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.m[uuid]
	return ok
}

func (s *inflightSet) Remove(uuid string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m, uuid)
}

// Entries returns a copy for iteration (used by reconcile).
func (s *inflightSet) Entries() map[string]inflightEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]inflightEntry, len(s.m))
	for k, v := range s.m {
		out[k] = v
	}
	return out
}
