package goal

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"sync"
	"time"
)

type snapshot struct {
	Version int             `json:"version"`
	Goals   map[string]Goal `json:"goals"`
}

// Store commits a complete snapshot before making mutations visible. Reservations
// are retained even for terminal goals; removing a goal must never release its
// issues into ordinary polling. Open fails closed on corrupt/unknown snapshots.
type Store struct {
	mu    sync.Mutex
	path  string
	data  snapshot
	write func(string, []byte) error
}

func Open(path string) (*Store, error) {
	s := &Store{path: path, data: snapshot{Version: 1, Goals: map[string]Goal{}}, write: atomicWrite}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	var loaded snapshot
	if err := json.Unmarshal(b, &loaded); err != nil {
		return nil, fmt.Errorf("goal store: %w", err)
	}
	s.data = loaded
	if err := validateSnapshot(s.data); err != nil {
		return nil, err
	}
	return s, nil
}

func validateSnapshot(data snapshot) error {
	if data.Version != 1 || data.Goals == nil {
		return fmt.Errorf("goal store: unsupported or incomplete snapshot")
	}
	ids := map[string]bool{}
	for id, g := range data.Goals {
		if id != g.ID || g.ID == "" || g.Parent.ID == "" || g.Project == "" || len(g.Tasks) == 0 {
			return fmt.Errorf("goal store: invalid goal %q", id)
		}
		for _, t := range g.Tasks {
			if t.ID == "" || t.Issue.ID == "" || len(t.Relations) != len(t.BlockedBy) {
				return fmt.Errorf("goal store: invalid task in %s", id)
			}
		}
		var drafts []TaskDraft
		for _, t := range g.Tasks {
			drafts = append(drafts, t.TaskDraft)
		}
		if err := ValidateDraft(Draft{Project: g.Project, Title: g.Title, Outcome: g.Outcome, Acceptance: g.Acceptance, NonGoals: g.NonGoals, Tasks: drafts}); err != nil {
			return fmt.Errorf("goal store: %s: %w", id, err)
		}
		if g.TeamID == "" || g.LinearProjectID == "" {
			return fmt.Errorf("goal store: missing Linear placement")
		}
		if err := g.validateWorkflow(); err != nil {
			return err
		}
		if err := g.validateProgress(); err != nil {
			return err
		}
		if err := g.validateBrief(); err != nil {
			return err
		}
		if err := g.validateExecution(); err != nil {
			return fmt.Errorf("goal store: %s: %w", id, err)
		}
		if err := g.validateAdoptions(); err != nil {
			return err
		}
		owned := []string{g.Parent.ID}
		for _, t := range g.Tasks {
			owned = append(owned, t.Issue.ID)
		}
		for _, issue := range owned {
			if ids[issue] {
				return fmt.Errorf("goal store: duplicate issue reservation %s", issue)
			}
			ids[issue] = true
		}
	}
	return nil
}

func copyGoal(g Goal) Goal {
	b, _ := json.Marshal(g)
	var c Goal
	_ = json.Unmarshal(b, &c)
	return c
}

func (s *Store) List() []Goal {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Goal, 0, len(s.data.Goals))
	for _, g := range s.data.Goals {
		out = append(out, copyGoal(g))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out
}

func (s *Store) Get(id string) (Goal, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	g, ok := s.data.Goals[id]
	return copyGoal(g), ok
}

func (s *Store) OwnsIssue(id string) bool {
	if id == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, g := range s.data.Goals {
		if g.Parent.ID == id {
			return true
		}
		for _, t := range g.Tasks {
			if t.Issue.ID == id {
				return true
			}
		}
	}
	return false
}

// Save uses revision CAS, so a stale UI or sync result cannot overwrite steering.
func (s *Store) Save(g Goal) (Goal, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	old, exists := s.data.Goals[g.ID]
	if exists {
		if err := preserveWorkflow(old, g); err != nil {
			return Goal{}, err
		}
		if err := preserveProgress(old, g); err != nil {
			return Goal{}, err
		}
		if err := preserveBrief(old, g); err != nil {
			return Goal{}, err
		}
		if err := preserveAdoptions(old, g); err != nil {
			return Goal{}, err
		}
	}
	if g.ID == "" || (exists && old.Revision != g.Revision) || (!exists && g.Revision != 0) {
		return Goal{}, fmt.Errorf("goal changed; reload before saving")
	}
	if exists && old.Execution != nil && (g.Execution == nil || old.Execution.PolicyDigest != g.Execution.PolicyDigest) {
		return Goal{}, fmt.Errorf("accepted execution policy cannot be replaced or removed")
	}
	if exists && old.PlanningID != g.PlanningID {
		return Goal{}, fmt.Errorf("accepted planning provenance cannot be replaced or removed")
	}
	if exists && old.Execution != nil {
		if err := preserveUsage(old.Execution, g.Execution); err != nil {
			return Goal{}, err
		}
		if err := preserveRefreshes(old.Execution, g.Execution); err != nil {
			return Goal{}, err
		}
		if len(g.Tasks) < len(old.Tasks) {
			return Goal{}, fmt.Errorf("accepted tasks must be retained")
		}
		for i, t := range old.Tasks {
			if g.Tasks[i].ID != t.ID || g.Tasks[i].DecomposedFrom != t.DecomposedFrom || !reflect.DeepEqual(g.Tasks[i].TaskDraft, t.TaskDraft) {
				return Goal{}, fmt.Errorf("accepted task contract cannot be silently replaced")
			}
		}
	}
	g = copyGoal(g)
	g.Revision++
	g.UpdatedAt = time.Now().UTC()
	next := snapshot{Version: 1, Goals: make(map[string]Goal, len(s.data.Goals)+1)}
	for id, value := range s.data.Goals {
		next.Goals[id] = value
	}
	next.Goals[g.ID] = g
	if err := validateSnapshot(next); err != nil {
		return Goal{}, err
	}
	b, err := json.MarshalIndent(next, "", "  ")
	if err != nil {
		return Goal{}, err
	}
	if err := s.write(s.path, append(b, '\n')); err != nil {
		return Goal{}, fmt.Errorf("persist goal before external action: %w", err)
	}
	s.data = next
	return copyGoal(g), nil
}

func atomicWrite(path string, body []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".goals-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(body); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err := os.Rename(f.Name(), path); err != nil {
		return err
	}
	// Persist the rename too, before acknowledging an external-action intent.
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
