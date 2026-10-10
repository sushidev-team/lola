package claimaudit

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"sync"
	"time"
)

// Outcome is how one verification run ended.
type Outcome uint8

const (
	// Unknown: the run happened, but the line's exit status was not its own
	// (piped, backgrounded, followed by `;`) — evidence it RAN, not of a result.
	Unknown Outcome = iota
	Passed
	Failed
)

// Run is one completed verification command, stamped with the time its tool
// result was recorded.
type Run struct {
	At      time.Time
	Outcome Outcome
}

// Ledger is what a transcript shows was run, per category, oldest first.
// Complete is true once the whole file has been read; an incomplete ledger
// must not be used to claim that something did NOT run.
type Ledger struct {
	Runs     map[Category][]Run
	Complete bool
}

// Bounds. maxRunsPerCat keeps the newest runs only (the audit asks for the
// latest run before a claim, and claims are recent); maxScanBytes bounds one
// Scan so the first read of a large transcript after a restart is spread over a
// few observe cycles instead of stalling one; maxPending bounds tool calls
// awaiting their result.
const (
	maxRunsPerCat = 32
	maxScanBytes  = 8 << 20
	maxPending    = 256
)

type pendingRun struct {
	hits       []Hit
	background bool
}

type fileState struct {
	info     os.FileInfo // identity: a replaced file at the same path starts over
	offset   int64
	skipping bool // inside a line longer than maxScanBytes; discard to its newline
	pending  map[string]pendingRun
	runs     map[Category][]Run
	complete bool
}

// Scanner reads Claude Code transcripts INCREMENTALLY: each Scan consumes only
// the bytes appended since the last one. It is a pure cache keyed by absolute
// path with its own mutex — the same shape as internal/agentlog.Reader.
type Scanner struct {
	mu    sync.Mutex
	files map[string]*fileState
}

func NewScanner() *Scanner { return &Scanner{files: map[string]*fileState{}} }

// Scan reads what was appended to path since the last call (at most
// maxScanBytes). Every failure leaves the ledger incomplete, never wrong: a
// missing file forgets the path, and a shrunk or replaced file starts over.
func (s *Scanner) Scan(path string) {
	if path == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := os.Open(path)
	if err != nil {
		delete(s.files, path)
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		delete(s.files, path)
		return
	}
	st := s.files[path]
	if st == nil || !os.SameFile(st.info, info) || info.Size() < st.offset {
		st = &fileState{pending: map[string]pendingRun{}, runs: map[Category][]Run{}}
		s.files[path] = st
	}
	st.info = info
	if st.offset == info.Size() {
		st.complete = !st.skipping
		return
	}
	n := min(info.Size()-st.offset, maxScanBytes)
	buf := make([]byte, n)
	if _, err := f.ReadAt(buf, st.offset); err != nil && err != io.EOF {
		st.complete = false
		return
	}
	consumed := int64(0)
	for {
		nl := bytes.IndexByte(buf[consumed:], '\n')
		if nl < 0 {
			break
		}
		line := buf[consumed : consumed+int64(nl)]
		consumed += int64(nl) + 1
		if st.skipping {
			st.skipping = false
			continue
		}
		st.line(line)
	}
	if consumed == 0 && n == maxScanBytes {
		// One line longer than the whole read budget (a huge tool output):
		// skip past it rather than re-reading it forever.
		st.skipping = true
		consumed = n
	}
	st.offset += consumed
	// A trailing partial line is still being written; it is read next time.
	st.complete = st.offset == info.Size() && !st.skipping
}

// Ledger returns a copy of what path's transcript shows; ok is false when the
// path was never scanned.
func (s *Scanner) Ledger(path string) (Ledger, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.files[path]
	if st == nil {
		return Ledger{}, false
	}
	l := Ledger{Runs: make(map[Category][]Run, len(st.runs)), Complete: st.complete}
	for c, rs := range st.runs {
		l.Runs[c] = append([]Run(nil), rs...)
	}
	return l, true
}

// Retain forgets every path not in keep, so ended sessions do not pin memory.
func (s *Scanner) Retain(keep map[string]bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for p := range s.files {
		if !keep[p] {
			delete(s.files, p)
		}
	}
}

// Len reports how many transcripts are tracked (tests).
func (s *Scanner) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.files)
}

// record is the only shape decoded from a transcript line.
type record struct {
	Type      string    `json:"type"`
	Timestamp time.Time `json:"timestamp"`
	Message   struct {
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

type block struct {
	Type      string          `json:"type"`
	ID        string          `json:"id"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	IsError   bool            `json:"is_error"`
}

// shellInput is the part of a tool's input that makes it a shell call. Any
// tool with a string `command` counts (Bash, and MCP shell wrappers alike).
type shellInput struct {
	Command         string `json:"command"`
	RunInBackground bool   `json:"run_in_background"`
}

var toolUseMarker = []byte(`"tool_use`) // matches both "tool_use" and "tool_use_id"

func (st *fileState) line(line []byte) {
	if !bytes.Contains(line, toolUseMarker) {
		return // the cheap filter: most lines are prose, summaries, metadata
	}
	var rec record
	if json.Unmarshal(line, &rec) != nil || len(rec.Message.Content) == 0 || rec.Message.Content[0] != '[' {
		return
	}
	var blocks []block
	if json.Unmarshal(rec.Message.Content, &blocks) != nil {
		return
	}
	for _, b := range blocks {
		switch {
		case rec.Type == "assistant" && b.Type == "tool_use" && b.ID != "":
			var in shellInput
			if json.Unmarshal(b.Input, &in) != nil || in.Command == "" {
				continue
			}
			hits := Classify(in.Command)
			if len(hits) == 0 || len(st.pending) >= maxPending {
				continue
			}
			st.pending[b.ID] = pendingRun{hits: hits, background: in.RunInBackground}
		case rec.Type == "user" && b.Type == "tool_result" && b.ToolUseID != "":
			p, ok := st.pending[b.ToolUseID]
			if !ok {
				continue
			}
			delete(st.pending, b.ToolUseID)
			at := rec.Timestamp
			for _, h := range p.hits {
				o := Unknown
				if h.Trusted && !p.background {
					o = Passed
					if b.IsError {
						o = Failed
					}
				}
				rs := append(st.runs[h.Cat], Run{At: at, Outcome: o})
				if len(rs) > maxRunsPerCat {
					rs = rs[len(rs)-maxRunsPerCat:]
				}
				st.runs[h.Cat] = rs
			}
		}
	}
}
