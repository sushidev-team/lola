package goalplan

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

	"github.com/sushidev-team/lola/internal/goal"
	"github.com/sushidev-team/lola/internal/usage"
)

type Record struct {
	ModelOutput *Output            `json:"model_output,omitempty"` // preserved original when an operator edits the graph
	Edits       []Edit             `json:"edits,omitempty"`
	Usage       *usage.Observation `json:"usage,omitempty"`
	ID          string             `json:"id"`
	GoalID      string             `json:"goal_id"` // reserved before the model call; acceptance is retry-safe
	Revision    uint64             `json:"revision"`
	State       string             `json:"state"` // running, proposed, failed, interrupted, accepted
	Agent       string             `json:"agent"`
	Image       string             `json:"image"`
	Input       Input              `json:"input"`
	InputSHA    string             `json:"input_sha256"`
	Output      *Output            `json:"output,omitempty"`
	Error       string             `json:"error,omitempty"`
	StartedAt   time.Time          `json:"started_at"`
	FinishedAt  time.Time          `json:"finished_at,omitempty"`
}
type disk struct {
	Version int               `json:"version"`
	Records map[string]Record `json:"records"`
}
type Store struct {
	mu    sync.Mutex
	path  string
	data  disk
	write func(string, []byte) error
}

func Open(path string) (*Store, error) {
	s := &Store{path: path, data: disk{Version: 1, Records: map[string]Record{}}, write: atomicWrite}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if err = json.Unmarshal(b, &s.data); err != nil {
		return nil, err
	}
	if (s.data.Version != 1 && s.data.Version != 2) || s.data.Records == nil {
		return nil, errors.New("unsupported goal proposal store")
	}
	recovered := false
	for id, r := range s.data.Records {
		if id != r.ID || r.ID != r.Input.Request.ID || !identity.MatchString(id) || !identity.MatchString(r.GoalID) || r.Revision == 0 || r.InputSHA != Digest(r.Input) || r.StartedAt.IsZero() {
			return nil, errors.New("invalid goal proposal record")
		}
		if err := r.Input.Request.Normalize(); err != nil {
			return nil, err
		}
		if r.Usage != nil {
			if err := goal.ValidateUsageObservation(*r.Usage); err != nil {
				return nil, err
			}
			if r.Usage.Agent != r.Agent || r.Usage.At.Before(r.StartedAt) {
				return nil, errors.New("proposal usage ownership mismatch")
			}
		}
		switch r.State {
		case "running":
			// Never infer that an interrupted paid call did not run. The inner
			// container timeout bounds survivors; a new ID is an explicit new run.
			r.State = "interrupted"
			r.Error = "daemon restarted during planning; inspect this record and use a new proposal ID to request another run"
			r.Revision++
			r.FinishedAt = time.Now().UTC()
			recovered = true
		case "proposed", "accepted":
			if r.Output == nil {
				return nil, errors.New("proposal result missing")
			}
			if err := ValidateOutput(*r.Output, r.Input); err != nil {
				return nil, err
			}
		case "failed", "interrupted":
		default:
			return nil, errors.New("unknown proposal state")
		}
		if err := validateEdits(r, s.data.Version); err != nil {
			return nil, err
		}
		s.data.Records[id] = r
	}
	if recovered {
		if err := s.persist(s.data); err != nil {
			return nil, err
		}
	}
	return s, nil
}
func clone(r Record) Record {
	b, _ := json.Marshal(r)
	var c Record
	_ = json.Unmarshal(b, &c)
	return c
}
func (s *Store) Get(id string) (Record, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.data.Records[id]
	return clone(r), ok
}

// Summary leaves bounded source snapshots out of the proposal picker. Execution
// replans belong to their owning goal and must not appear as new goal proposals.
type Summary struct {
	ID        string    `json:"id"`
	Project   string    `json:"project"`
	Title     string    `json:"title"`
	State     string    `json:"state"`
	StartedAt time.Time `json:"started_at"`
}

func (s *Store) List(project string) []Summary {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []Summary{}
	for _, r := range s.data.Records {
		if r.Input.Repair == nil && (project == "" || r.Input.Request.Project == project) {
			out = append(out, Summary{r.ID, r.Input.Request.Project, r.Input.Request.Title, r.State, r.StartedAt})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].StartedAt.Equal(out[j].StartedAt) {
			return out[i].ID < out[j].ID
		}
		return out[i].StartedAt.After(out[j].StartedAt)
	})
	return out
}

// Reserve returns fresh=false on an identical retry, regardless of its outcome.
// The caller must never invoke a model unless fresh=true and err=nil.
func (s *Store) Reserve(in Input, kind, image string) (r Record, fresh bool, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err = in.Request.Normalize(); err != nil {
		return
	}
	if old, ok := s.data.Records[in.Request.ID]; ok {
		if !SameRequest(old.Input.Request, in.Request) {
			return old, false, errors.New("proposal ID already belongs to a different brief")
		}
		return clone(old), false, nil
	}
	if _, err = in.Bytes(); err != nil {
		return
	}
	r = Record{ID: in.Request.ID, GoalID: goal.NewID(), Revision: 1, State: "running", Agent: kind, Image: image, Input: in, InputSHA: Digest(in), StartedAt: time.Now().UTC()}
	if in.Repair != nil {
		if !identity.MatchString(in.Repair.GoalID) {
			return r, false, errors.New("repair goal identity invalid")
		}
		r.GoalID = in.Repair.GoalID
	}
	err = s.commit(r)
	return clone(r), err == nil, err
}
func (s *Store) Finish(id string, out *Output, runErr error) (Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.data.Records[id]
	if !ok || r.State != "running" {
		return r, errors.New("proposal is not running")
	}
	if runErr == nil {
		if out == nil {
			return r, errors.New("missing proposal output")
		}
		if err := ValidateOutput(*out, r.Input); err != nil {
			return r, err
		}
		r.State = "proposed"
		r.Output = out
	} else {
		r.State = "failed"
		r.Error = runErr.Error()
		if len(r.Error) > 4000 {
			r.Error = r.Error[:4000]
		}
	}
	r.FinishedAt = time.Now().UTC()
	r.Revision++
	err := s.commit(r)
	return clone(r), err
}
func (s *Store) Accept(id string, revision uint64) (Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.data.Records[id]
	if !ok || r.Revision != revision || r.State != "proposed" {
		return r, errors.New("proposal changed; review it before accepting")
	}
	r.State = "accepted"
	r.Revision++
	err := s.commit(r)
	return clone(r), err
}
func (s *Store) commit(r Record) error {
	version := s.data.Version
	if len(r.Edits) > 0 {
		version = 2
	}
	next := disk{Version: version, Records: make(map[string]Record, len(s.data.Records)+1)}
	for id, v := range s.data.Records {
		next.Records[id] = v
	}
	next.Records[r.ID] = clone(r)
	if err := s.persist(next); err != nil {
		return err
	}
	s.data = next
	return nil
}
func (s *Store) persist(next disk) error {
	b, err := json.MarshalIndent(next, "", "  ")
	if err != nil {
		return err
	}
	return s.write(s.path, append(b, '\n'))
}
func atomicWrite(path string, b []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".goal-plans-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(b); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err = os.Rename(f.Name(), path); err != nil {
		return err
	}
	d, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("sync proposal directory: %w", err)
	}
	defer d.Close()
	return d.Sync()
}

// ObserveUsage records the CLI's numeric observation independently of whether
// its proposal is valid. Replayed callbacks cannot replace a prior observation.
func (s *Store) ObserveUsage(id string, o usage.Observation) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.data.Records[id]
	if !ok || r.State != "running" || o.Agent != r.Agent || o.At.Before(r.StartedAt) {
		return errors.New("proposal usage has no matching active reservation")
	}
	if err := goal.ValidateUsageObservation(o); err != nil {
		return err
	}
	if r.Usage != nil {
		if reflect.DeepEqual(*r.Usage, o) {
			return nil
		}
		return errors.New("proposal usage already recorded")
	}
	r.Usage = &o
	r.Revision++
	return s.commit(r)
}

// UsageRecord excludes source snapshots and model text from the minute usage
// pass. Only numeric observations and their reserved owners need copying.
type UsageRecord struct {
	ID, GoalID, Project string
	Usage               *usage.Observation
}

func (s *Store) UsageRecords() []UsageRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]UsageRecord, 0, len(s.data.Records))
	for _, r := range s.data.Records {
		if r.Usage == nil {
			continue
		}
		b, _ := json.Marshal(r.Usage)
		var o usage.Observation
		_ = json.Unmarshal(b, &o)
		out = append(out, UsageRecord{ID: r.ID, GoalID: r.GoalID, Project: r.Input.Request.Project, Usage: &o})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
