package usage

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// DayFormat is the ledger's day key: a LOCAL calendar date, because "today's
// budget" means the user's today, not UTC's.
const DayFormat = "2006-01-02"

// maxDepth bounds the directory walk. claude-code writes a session's subagent
// transcripts one level down (<session-id>/subagents/agent-*.jsonl); anything
// deeper is not a transcript lola needs to sum.
const maxDepth = 3

// ProjectsRoot is where claude-code keeps transcripts: $CLAUDE_CONFIG_DIR/projects
// when that is set, else ~/.claude/projects. "" when no home dir resolves.
func ProjectsRoot() string {
	if d := os.Getenv("CLAUDE_CONFIG_DIR"); d != "" {
		return filepath.Join(d, "projects")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".claude", "projects")
}

// SlugDir is the transcript directory claude-code uses for a working directory:
// every character outside [A-Za-z0-9] becomes '-'. It is the fallback when no
// hook has reported a transcript path yet (a session that has not finished a
// turn); a reported path is always preferred, since it is claude-code's own
// answer rather than a re-derivation of its rule.
func SlugDir(root, cwd string) string {
	if root == "" || cwd == "" {
		return ""
	}
	var b strings.Builder
	for _, r := range cwd {
		if r < 128 && (r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteByte('-')
		}
	}
	return filepath.Join(root, b.String())
}

// Scanner sums transcript directories INCREMENTALLY: each file's read offset is
// remembered, so a pass over a directory whose transcripts did not grow costs a
// walk and a stat per file, and a growing one reads only the appended bytes.
// That is the whole cost model — a multi-megabyte transcript is parsed once,
// not every minute. Safe for concurrent use.
type Scanner struct {
	mu   sync.Mutex
	dirs map[string]*dirState
}

type dirState struct {
	files map[string]*fileState
	// seen dedups messages ACROSS files: claude-code writes one line per
	// content block, each carrying the same usage, and a resumed or forked
	// session copies history into a new file with the original ids.
	seen map[string]struct{}
	days map[string]Totals
}

type fileState struct {
	offset int64
	size   int64
}

// NewScanner returns an empty Scanner.
func NewScanner() *Scanner { return &Scanner{dirs: map[string]*dirState{}} }

// ScanDir returns dir's spend bucketed by local day (DayFormat). A missing
// directory is not an error: it answers nothing, which is what a session that
// has not started a turn, or runs another agent, has spent as far as lola can
// tell. The map is a copy the caller owns.
func (s *Scanner) ScanDir(dir string) (map[string]Totals, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if dir == "" {
		return nil, nil
	}
	st := s.dirs[dir]
	if st == nil {
		st = newDirState()
		s.dirs[dir] = st
	}
	err := s.walk(dir, st)
	return maps.Clone(st.days), err
}

func newDirState() *dirState {
	return &dirState{files: map[string]*fileState{}, seen: map[string]struct{}{}, days: map[string]Totals{}}
}

func (s *Scanner) walk(dir string, st *dirState) error {
	root := filepath.Clean(dir)
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if path == root {
				return err
			}
			return nil // an unreadable subdirectory costs only its own files
		}
		if d.IsDir() {
			if path != root && strings.Count(strings.TrimPrefix(path, root), string(filepath.Separator)) >= maxDepth {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() || !strings.HasSuffix(d.Name(), ".jsonl") {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		fst := st.files[path]
		if fst != nil && info.Size() < fst.size {
			// A transcript only ever grows. One that shrank was rewritten, and
			// its old contribution can no longer be told apart from the rest:
			// start the whole directory over (the next walk re-reads it).
			*st = *newDirState()
			fst = nil
		}
		if fst == nil {
			fst = &fileState{}
			st.files[path] = fst
		}
		fst.size = info.Size()
		if info.Size() == fst.offset {
			return nil
		}
		fst.offset = readFrom(path, fst.offset, info.ModTime(), st)
		return nil
	})
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

// record is the ONLY shape decoded from a transcript line.
type record struct {
	Type      string `json:"type"`
	Timestamp string `json:"timestamp"`
	RequestID string `json:"requestId"`
	Message   *struct {
		ID    string `json:"id"`
		Model string `json:"model"`
		Usage *struct {
			Input      int64 `json:"input_tokens"`
			Output     int64 `json:"output_tokens"`
			CacheWrite int64 `json:"cache_creation_input_tokens"`
			CacheRead  int64 `json:"cache_read_input_tokens"`
			// CacheCreation splits the write by TTL: an hour-long cache entry
			// is billed at 2× input, a 5-minute one at 1.25×.
			CacheCreation *struct {
				Hour int64 `json:"ephemeral_1h_input_tokens"`
			} `json:"cache_creation"`
			Speed string `json:"speed"`
		} `json:"usage"`
	} `json:"message"`
}

var usageKey = []byte(`"usage"`)

// readFrom consumes complete lines from offset and returns the new offset. A
// trailing partial line (the agent is mid-write) is left for the next pass.
func readFrom(path string, offset int64, mod time.Time, st *dirState) int64 {
	f, err := os.Open(path)
	if err != nil {
		return offset
	}
	defer f.Close()
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return offset
	}
	r := bufio.NewReaderSize(f, 64<<10)
	for {
		line, err := r.ReadBytes('\n')
		if err != nil {
			return offset // EOF on a partial (or empty) tail: re-read it next time
		}
		offset += int64(len(line))
		// Most lines are tool output with no usage at all; skip the decode.
		if !bytes.Contains(line, usageKey) {
			continue
		}
		var rec record
		if json.Unmarshal(line, &rec) != nil || rec.Type != "assistant" || rec.Message == nil || rec.Message.Usage == nil {
			continue
		}
		if key := rec.Message.ID + ":" + rec.RequestID; key != ":" {
			if _, dup := st.seen[key]; dup {
				continue
			}
			st.seen[key] = struct{}{}
		}
		u := rec.Message.Usage
		t := Totals{Input: u.Input, Output: u.Output, CacheWrite: u.CacheWrite, CacheRead: u.CacheRead}
		var hour int64
		if u.CacheCreation != nil {
			hour = min(u.CacheCreation.Hour, u.CacheWrite)
		}
		t.CostUSD = cost(rec.Message.Model, t, hour, u.Speed == "fast")
		day := mod.Local().Format(DayFormat)
		if ts, err := time.Parse(time.RFC3339Nano, rec.Timestamp); err == nil {
			day = ts.Local().Format(DayFormat)
		}
		cur := st.days[day]
		cur.Add(t)
		st.days[day] = cur
	}
}

// Retain forgets every directory not in keep, so the scanner's memory follows
// the live session set rather than every session the daemon ever saw.
func (s *Scanner) Retain(keep map[string]bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for d := range s.dirs {
		if !keep[d] {
			delete(s.dirs, d)
		}
	}
}

// Sum folds a by-day map into one total.
func Sum(days map[string]Totals) Totals {
	var t Totals
	for _, d := range days {
		t.Add(d)
	}
	return t
}
