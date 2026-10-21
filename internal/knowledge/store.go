package knowledge

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"sync"
	"time"
)

type snapshot struct {
	History    map[string][]Lesson    `json:"history"`
	Version    int                    `json:"version"`
	Lessons    map[string]Lesson      `json:"lessons"`
	Deliveries map[string][]Reference `json:"deliveries"`
}

type Store struct {
	mu   sync.Mutex
	path string
	data snapshot
}

func Open(path string) (*Store, error) {
	s := &Store{path: path, data: snapshot{Version: 1, History: map[string][]Lesson{}, Lessons: map[string]Lesson{}, Deliveries: map[string][]Reference{}}}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	var loaded snapshot
	if err := json.Unmarshal(b, &loaded); err != nil {
		return nil, err
	}
	if loaded.Version != 1 || loaded.Lessons == nil || loaded.Deliveries == nil || loaded.History == nil {
		return nil, fmt.Errorf("invalid knowledge snapshot")
	}
	for id, l := range loaded.Lessons {
		if id != l.ID || l.Version < 1 {
			return nil, fmt.Errorf("invalid knowledge identity")
		}
		if err := Validate(l.Scope, l.Proposal, l.Source); err != nil {
			return nil, err
		}
		switch l.Status {
		case Candidate, Active, Disputed, Superseded:
		default:
			return nil, fmt.Errorf("unknown lesson status")
		}
		if l.Status == Active && (l.Verification == "" || l.VerifiedAt.IsZero() || l.Policy) {
			return nil, fmt.Errorf("active lesson has no valid verification")
		}
	}
	s.data = loaded
	return s, nil
}

func (s *Store) copy() snapshot {
	b, _ := json.Marshal(s.data)
	var next snapshot
	_ = json.Unmarshal(b, &next)
	return next
}

func (s *Store) commit(next snapshot) error {
	b, err := json.MarshalIndent(next, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".knowledge-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(append(b, '\n')); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err := os.Rename(f.Name(), s.path); err != nil {
		return err
	}
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	err = d.Sync()
	d.Close()
	if err != nil {
		return err
	}
	s.data = next
	return nil
}

func (s *Store) List() []Lesson {
	s.mu.Lock()
	defer s.mu.Unlock()
	copy := s.copy()
	out := make([]Lesson, 0, len(copy.Lessons))
	for _, l := range copy.Lessons {
		out = append(out, l)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Capture deduplicates repeated delivery of the same event. A worker cannot
// submit a status or a verification flag. Only an explicit human correction may
// activate immediately; conflicting or policy-changing corrections stay pending.
func (s *Store) Capture(scope Scope, p Proposal, source Source, correction bool) (Lesson, error) {
	if err := Validate(scope, p, source); err != nil {
		return Lesson{}, err
	}
	if correction && source.Kind != "correction" {
		return Lesson{}, fmt.Errorf("automatic activation requires an explicit correction")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	p.Paths, p.Tags, p.Evidence = slices.Clone(p.Paths), slices.Clone(p.Tags), slices.Clone(p.Evidence)
	id := identity(scope, p, source)
	if l, ok := s.copy().Lessons[id]; ok {
		return l, nil
	}
	if len(s.data.Lessons) >= 10000 {
		return Lesson{}, fmt.Errorf("knowledge capacity reached; curate existing lessons before adding more")
	}
	next := s.copy()
	now := time.Now().UTC()
	scope.Paths = slices.Clone(p.Paths)
	l := Lesson{ID: id, Version: 1, Scope: scope, Proposal: p, Source: source, Status: Candidate, CreatedAt: now}
	for _, existing := range next.Lessons {
		if existing.Scope.Project == scope.Project && existing.Scope.GoalID == scope.GoalID &&
			existing.Topic == p.Topic && slices.Equal(existing.Paths, p.Paths) && existing.Summary != p.Summary && existing.Status == Active {
			l.Conflicts = append(l.Conflicts, existing.ID)
		}
	}
	slices.Sort(l.Conflicts)
	if len(l.Conflicts) > 0 {
		l.Status = Disputed
	} else if correction && !p.Policy {
		l.Status, l.VerifiedAt, l.Verification = Active, now, "explicit human correction: "+source.ID
	}
	next.Lessons[id] = l
	if err := s.commit(next); err != nil {
		return Lesson{}, err
	}
	// Do not return slices shared with the canonical snapshot.
	return s.copy().Lessons[id], nil
}

// Accept is called only for a human decision or a verifier-owned receipt, never
// for a worker's self-report. Conflicts must be resolved explicitly and policy
// proposals can only be exported for normal code review, never activated here.
func (s *Store) Accept(id string, version int, evidence string, supersedes []string, expires time.Time) (Lesson, error) {
	if len(evidence) < 8 || len(evidence) > 4000 || credential.MatchString(evidence) {
		return Lesson{}, fmt.Errorf("acceptance requires bounded, non-secret verification evidence")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	next := s.copy()
	l, ok := next.Lessons[id]
	if !ok || l.Version != version {
		return Lesson{}, fmt.Errorf("lesson changed; refresh before accepting")
	}
	if l.Policy || l.Status == Superseded {
		return Lesson{}, fmt.Errorf("policy proposals and superseded lessons cannot be activated")
	}
	// Recompute conflicts: another candidate may have become active since this
	// one was captured. Its old conflict list is not an authorization to replace it.
	var conflicts []string
	for otherID, other := range next.Lessons {
		if otherID != id && other.Status == Active && other.Scope.Project == l.Scope.Project && other.Scope.GoalID == l.Scope.GoalID && other.Topic == l.Topic && slices.Equal(other.Paths, l.Paths) && other.Summary != l.Summary {
			conflicts = append(conflicts, otherID)
		}
	}
	want, got := conflicts, slices.Clone(supersedes)
	slices.Sort(want)
	slices.Sort(got)
	if !slices.Equal(want, got) {
		return Lesson{}, fmt.Errorf("resolve all conflicts explicitly before activation")
	}
	for _, conflict := range want {
		other := next.Lessons[conflict]
		next.History[conflict] = append(next.History[conflict], other)
		other.Status, other.Version = Superseded, other.Version+1
		next.Lessons[conflict] = other
	}
	next.History[id] = append(next.History[id], l)
	l.Status, l.Version, l.VerifiedAt, l.Verification, l.ExpiresAt = Active, l.Version+1, time.Now().UTC(), evidence, expires
	l.Conflicts = nil
	next.Lessons[id] = l
	if err := s.commit(next); err != nil {
		return Lesson{}, err
	}
	return s.copy().Lessons[id], nil
}

func (s *Store) Dispute(id string, version int, reason string) error {
	if len(reason) < 8 || len(reason) > 2000 || credential.MatchString(reason) {
		return fmt.Errorf("bounded, non-secret dispute reason required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	next := s.copy()
	l, ok := next.Lessons[id]
	if !ok || l.Version != version || l.Status == Superseded {
		return fmt.Errorf("lesson changed or superseded")
	}
	next.History[id] = append(next.History[id], l)
	l.Status, l.Version, l.Verification = Disputed, l.Version+1, reason
	next.Lessons[id] = l
	return s.commit(next)
}

// RecordDelivery preserves exactly the versions supplied at launch. A resumed
// session retains its original bundle rather than silently acquiring new rules.
func (s *Store) RecordDelivery(session string, refs []Reference) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if session == "" {
		return fmt.Errorf("session required")
	}
	next := s.copy()
	if previous, exists := next.Deliveries[session]; exists {
		if !slices.Equal(previous, refs) {
			return fmt.Errorf("session already has a different recorded knowledge bundle")
		}
		return nil
	}
	next.Deliveries[session] = slices.Clone(refs)
	return s.commit(next)
}

func (s *Store) Delivery(session string) []Reference {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.data.Deliveries[session])
}

// Version returns the immutable content of a supplied lesson version.
func (s *Store) Version(ref Reference) (Lesson, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := s.copy()
	if l, ok := next.Lessons[ref.ID]; ok && l.Version == ref.Version {
		return l, true
	}
	for _, l := range next.History[ref.ID] {
		if l.Version == ref.Version {
			return l, true
		}
	}
	return Lesson{}, false
}
