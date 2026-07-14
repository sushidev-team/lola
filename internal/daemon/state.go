package daemon

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// seenStore persists per-poll seen maps at <dir>/<poll>.seen as one JSON
// object {uuid: firstSeenRFC3339}. Loads are lazy and cached; saves are
// atomic (temp file in the same dir + rename). Boundedness is the caller's
// job: label mode prunes entries older than SeenTTL, seen mode prunes
// non-matching IDs on every tick.
type seenStore struct {
	mu    sync.Mutex
	dir   string
	cache map[string]map[string]time.Time
}

func newSeenStore(dir string) *seenStore {
	return &seenStore{dir: dir, cache: map[string]map[string]time.Time{}}
}

func (s *seenStore) path(poll string) string {
	return filepath.Join(s.dir, poll+".seen")
}

// load returns a copy of the poll's seen map (callers mutate freely and
// persist via save).
func (s *seenStore) load(poll string) (map[string]time.Time, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if m, ok := s.cache[poll]; ok {
		return copySeen(m), nil
	}
	m := map[string]time.Time{}
	data, err := os.ReadFile(s.path(poll))
	switch {
	case errors.Is(err, fs.ErrNotExist):
		// first run for this poll
	case err != nil:
		return nil, err
	default:
		var raw map[string]string
		if err := json.Unmarshal(data, &raw); err != nil {
			return nil, err
		}
		for id, ts := range raw {
			t, err := time.Parse(time.RFC3339, ts)
			if err != nil {
				continue // drop unparsable entries rather than fail the tick
			}
			m[id] = t
		}
	}
	s.cache[poll] = m
	return copySeen(m), nil
}

// save atomically persists the poll's seen map and updates the cache.
func (s *seenStore) save(poll string, m map[string]time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	raw := make(map[string]string, len(m))
	for id, t := range m {
		raw[id] = t.UTC().Format(time.RFC3339)
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(s.dir, "."+poll+"-*.seen")
	if err != nil {
		return err
	}
	defer func() {
		if tmp != nil {
			tmp.Close()
			os.Remove(tmp.Name())
		}
	}()
	if err := tmp.Chmod(0o600); err != nil {
		return err
	}
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	name := tmp.Name()
	tmp = nil // disarm cleanup
	if err := os.Rename(name, s.path(poll)); err != nil {
		os.Remove(name)
		return err
	}
	s.cache[poll] = copySeen(m)
	return nil
}

func copySeen(m map[string]time.Time) map[string]time.Time {
	out := make(map[string]time.Time, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
