package reviewprofile

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

type Store struct {
	mu       sync.Mutex
	path     string
	records  map[string]Record
	findings map[string]FindingReport
}

type disk struct {
	Findings map[string]FindingReport `json:"findings,omitempty"`
	Version  int                      `json:"version"`
	Records  map[string]Record        `json:"records"`
}

func Open(path string) (*Store, error) {
	s := &Store{path: path, records: map[string]Record{}, findings: map[string]FindingReport{}}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	var data disk
	if err := json.Unmarshal(b, &data); err != nil {
		return nil, err
	}
	if (data.Version != 1 && data.Version != 2) || data.Records == nil {
		return nil, fmt.Errorf("invalid specialist review store")
	}
	for key, r := range data.Records {
		if err := r.Inputs.Invocation.Validate(); err != nil {
			return nil, err
		}
		if key != r.Key || r.Profile == "" || r.Attempt < 1 {
			return nil, fmt.Errorf("invalid specialist review record")
		}
		switch r.State {
		case "running", "pass", "findings", "incomplete", "error", "not_applicable", "stale":
		default:
			return nil, fmt.Errorf("unknown specialist record state")
		}
		if r.State == "pass" && (!r.Result.Complete || len(r.Result.Evidence) == 0) {
			return nil, fmt.Errorf("specialist pass has no evidence")
		}
		if r.State == "running" {
			r.State, r.Error = "error", "review interrupted by daemon restart; result unknown"
			data.Records[key] = r
		}
	}
	if data.Findings == nil {
		data.Findings = map[string]FindingReport{}
	}
	for id, finding := range data.Findings {
		if id != finding.ID {
			return nil, fmt.Errorf("invalid finding map identity")
		}
		if err := validateFindingReport(finding); err != nil {
			return nil, err
		}
	}
	// Old snapshots have no finding registry. Import only completed claims;
	// interrupted and incomplete attempts never gain an assessed finding.
	for _, record := range data.Records {
		registerFindings(data.Findings, record)
	}
	if err := s.saveWithFindings(data.Records, data.Findings); err != nil {
		return nil, err
	}
	return s, nil
}

func clone(records map[string]Record) map[string]Record {
	b, _ := json.Marshal(records)
	var out map[string]Record
	_ = json.Unmarshal(b, &out)
	return out
}

func (s *Store) save(next map[string]Record) error {
	return s.saveWithFindings(next, s.findings)
}

func (s *Store) saveWithFindings(next map[string]Record, findings map[string]FindingReport) error {
	// Version 2 prevents older writers from silently dropping invocation evidence.
	// Version 1 remains readable; migration retains every review and finding.
	b, err := json.MarshalIndent(disk{Version: 2, Records: next, Findings: findings}, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".profiles-*")
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
	s.records = clone(next)
	s.findings = cloneFindings(findings)
	return nil
}

func (s *Store) List() []Record {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Record
	for _, r := range clone(s.records) {
		out = append(out, r)
	}
	slices.SortFunc(out, func(a, b Record) int { return strings.Compare(a.Key, b.Key) })
	return out
}

func (s *Store) Get(key string) (Record, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := clone(s.records)[key]
	return r, ok
}

func (s *Store) Begin(p Profile, in Inputs, retry bool) (Record, error) {
	if err := in.Invocation.Validate(); err != nil {
		return Record{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := p.Key(in)
	old, exists := s.records[key]
	if exists && ((!retry && old.State != "stale") || old.State == "running" || old.State == "pass" || old.Attempt >= p.Rounds()) {
		return old, fmt.Errorf("specialist result already recorded or attempt budget exhausted")
	}
	r := Record{Key: key, Profile: p.ID, Provider: p.Provider, Version: p.Version, Inputs: in, State: "running", Attempt: old.Attempt + 1, UpdatedAt: time.Now().UTC()}
	next := clone(s.records)
	next[key] = r
	if err := s.save(next); err != nil {
		return Record{}, err
	}
	return clone(next)[key], nil
}

func (s *Store) Finish(r Record) error {
	if err := r.Inputs.Invocation.Validate(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	old, exists := s.records[r.Key]
	if !exists || old.State != "running" || old.Attempt != r.Attempt {
		return fmt.Errorf("specialist attempt superseded")
	}
	if (old.Inputs.Invocation == nil && r.Inputs.Invocation != nil) || (old.Inputs.Invocation != nil && !old.Inputs.Invocation.Matches(r.Inputs.Invocation)) {
		return fmt.Errorf("specialist invocation differs from its reservation")
	}
	switch r.State {
	case "pass", "findings", "incomplete", "error", "not_applicable":
	default:
		return fmt.Errorf("invalid specialist completion")
	}
	if r.State == "pass" && (!r.Result.Complete || len(r.Result.Evidence) == 0) {
		return fmt.Errorf("cannot certify an incomplete specialist review")
	}
	r.UpdatedAt = time.Now().UTC()
	next := clone(s.records)
	next[r.Key] = r
	findings := cloneFindings(s.findings)
	registerFindings(findings, r)
	return s.saveWithFindings(next, findings)
}

func (s *Store) Invalidate(key, reason string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := clone(s.records)
	r, ok := next[key]
	if !ok {
		return fmt.Errorf("unknown specialist result")
	}
	r.State, r.Error, r.UpdatedAt = "stale", reason, time.Now().UTC()
	next[key] = r
	return s.save(next)
}
