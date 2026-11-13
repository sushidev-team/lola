package agentfeed

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/sshelement"
)

// This reader is written from the rollout format as a structural survey recorded it, and
// tested on synthetic fixtures only. internal/agent's Caps keeps codex's Feed off until a
// real capture has been checked in, so production does not reach it yet. Expect the first
// real rollout to correct some field names here.
//
// Codex keeps its logs by date, so a session's files are found by the working directory
// in each one's first line. The same directory also holds lola's codex review passes
// (originator codex_exec) and guardian subagent threads, which are not this conversation.

// codexRolloutRe is the only file name read.
var codexRolloutRe = regexp.MustCompile(`^rollout-[A-Za-z0-9_.:-]{1,128}\.jsonl$`)

const (
	codexRediscover  = 5 * time.Second
	codexDayDirs     = 16 // how many of the newest day directories are searched
	maxCodexMetaLine = 4 << 20
)

type codexSource struct {
	collector
	root     string
	cwd      string
	files    []*codexFile
	byPath   map[string]*codexFile
	verdict  map[string]bool
	lastWalk time.Time
	seen     map[string]struct{}
	refs     map[string]codexRef
	turn     string
	eof      bool
	anyFile  bool
}

type codexFile struct {
	tail    tail
	started bool
}

type codexRef struct {
	file   *codexFile
	offset int64
	length int64
}

// NewCodex reads the rollouts under root whose session_meta names exactly cwd.
func NewCodex(root, cwd string) Source {
	s := &codexSource{root: root, cwd: filepath.Clean(cwd)}
	s.reset()
	return s
}

func (s *codexSource) reset() {
	s.files, s.byPath = nil, map[string]*codexFile{}
	s.verdict = map[string]bool{}
	s.seen, s.refs = map[string]struct{}{}, map[string]codexRef{}
	s.turn, s.eof, s.anyFile = "", false, false
	s.lastWalk = time.Time{}
}

type codexMeta struct {
	Type    string `json:"type"`
	Payload struct {
		Cwd            string `json:"cwd"`
		Originator     string `json:"originator"`
		Source         any    `json:"source"`
		ThreadSource   string `json:"thread_source"`
		ParentThreadID string `json:"parent_thread_id"`
	} `json:"payload"`
}

// belongs decides from a rollout's first line. Anything that is not plainly this session's
// own interactive thread is left out.
func (s *codexSource) belongs(path string) bool {
	f, _, err := openRegular(path)
	if err != nil {
		return false
	}
	defer f.Close()
	// The first line carries the base instructions and is large, but never this large.
	line, err := bufio.NewReaderSize(io.LimitReader(f, maxCodexMetaLine), 64<<10).ReadBytes('\n')
	if err != nil {
		return false
	}
	var m codexMeta
	if json.Unmarshal(line, &m) != nil || m.Type != "session_meta" {
		return false
	}
	p := m.Payload
	if p.Cwd == "" || filepath.Clean(p.Cwd) != s.cwd || p.ParentThreadID != "" {
		return false
	}
	source := strings.ToLower(fmt.Sprint(p.Source))
	for _, v := range []string{strings.ToLower(p.Originator), source, strings.ToLower(p.ThreadSource)} {
		if strings.Contains(v, "codex_exec") || v == "exec" || strings.Contains(v, "guardian") || strings.Contains(v, "subagent") {
			return false
		}
	}
	return true
}

func (s *codexSource) discover() {
	if time.Since(s.lastWalk) < codexRediscover && !s.lastWalk.IsZero() {
		return
	}
	s.lastWalk = time.Now()
	days, _ := filepath.Glob(filepath.Join(s.root, "*", "*", "*"))
	sort.Strings(days)
	if len(days) > codexDayDirs {
		days = days[len(days)-codexDayDirs:]
	}
	var found []string
	for _, day := range days {
		entries, err := os.ReadDir(day)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.Type().IsRegular() || !codexRolloutRe.MatchString(e.Name()) {
				continue
			}
			p := filepath.Join(day, e.Name())
			ok, known := s.verdict[p]
			if !known {
				ok = s.belongs(p)
				s.verdict[p] = ok
			}
			if ok {
				found = append(found, p)
			}
		}
	}
	sort.Strings(found)
	for _, p := range found {
		if s.byPath[p] == nil {
			f := &codexFile{tail: tail{path: p}}
			s.byPath[p] = f
			s.files = append(s.files, f)
		}
	}
}

// Advance implements Source.
func (s *codexSource) Advance(ctx context.Context, budget int64) ([]Item, Health) {
	if budget <= 0 {
		budget = chunkBytes
	}
	s.discover()
	for _, f := range s.files {
		if budget <= 0 {
			break
		}
		n, st := f.tail.advance(ctx, budget,
			func(line []byte, off int64) { s.line(f, line, off) },
			func([]byte) {})
		if st == tailReplaced {
			s.restart()
			s.reset()
			return nil, protocol.FeedOK
		}
		s.counts.Bytes += n
		budget -= n
	}
	s.eof, s.anyFile = true, false
	for _, f := range s.files {
		s.eof = s.eof && f.tail.atEOF
		s.anyFile = s.anyFile || f.tail.info != nil
	}
	return s.take(), s.health()
}

func (s *codexSource) health() Health {
	switch {
	case !s.anyFile:
		return protocol.FeedEmpty
	case s.counts.Items > 0:
		return protocol.FeedOK
	case s.counts.Bytes > unreadableBytes:
		return protocol.FeedUnreadable
	}
	return protocol.FeedEmpty
}

// AtEOF implements Source.
func (s *codexSource) AtEOF() bool { return s.eof }

type codexLine struct {
	Timestamp string `json:"timestamp"`
	Type      string `json:"type"`
	Payload   struct {
		Type   string          `json:"type"`
		Reason string          `json:"reason"`
		TurnID string          `json:"turn_id"`
		Item   json.RawMessage `json:"item"`
	} `json:"payload"`
}

type codexItem struct {
	Type             string          `json:"type"`
	ID               string          `json:"id"`
	Content          json.RawMessage `json:"content"`
	Command          json.RawMessage `json:"command"`
	AggregatedOutput string          `json:"aggregated_output"`
	ExitCode         *int            `json:"exit_code"`
	Status           string          `json:"status"`
	DurationMs       int64           `json:"duration_ms"`
	Changes          json.RawMessage `json:"changes"`
	Server           string          `json:"server"`
	Tool             string          `json:"tool"`
	Path             string          `json:"path"`
}

func (s *codexSource) line(f *codexFile, line []byte, off int64) {
	if !bytes.Contains(line, []byte(`"event_msg"`)) && !bytes.Contains(line, []byte(`"compacted"`)) {
		return
	}
	var l codexLine
	err := json.Unmarshal(line, &l)
	var mismatch *json.UnmarshalTypeError
	if err != nil && !errors.As(err, &mismatch) {
		return
	}
	at := parseAt(l.Timestamp)
	s.startFile(f, at)
	if l.Type == "compacted" {
		s.emit(Item{ID: fmt.Sprintf("compacted@%s@%d", filepath.Base(f.tail.path), off), Kind: protocol.FeedItemDivider, At: at, Text: "Compacted"})
		return
	}
	switch l.Payload.Type {
	case "turn_aborted":
		if l.Payload.Reason == "interrupted" {
			s.emit(Item{ID: fmt.Sprintf("aborted@%s@%d", filepath.Base(f.tail.path), off), Kind: protocol.FeedItemNotice, At: at, Text: "Interrupted"})
		}
	case "item_completed":
		var it codexItem
		if len(l.Payload.Item) == 0 {
			return
		}
		err := json.Unmarshal(l.Payload.Item, &it)
		if (err != nil && !errors.As(err, &mismatch)) || it.ID == "" {
			return
		}
		if _, dup := s.seen[it.ID]; dup {
			return
		}
		s.seen[it.ID] = struct{}{}
		s.item(f, &it, at, off, int64(len(line)))
	}
}

// startFile divides one rollout from the one before it.
func (s *codexSource) startFile(f *codexFile, at time.Time) {
	if f.started {
		return
	}
	f.started = true
	if len(s.files) > 0 && f != s.files[0] {
		s.emit(Item{ID: "segment:" + filepath.Base(f.tail.path), Kind: protocol.FeedItemDivider, At: at, Text: "New conversation"})
	}
}

func (s *codexSource) emit(it Item) {
	if it.Turn == "" {
		it.Turn = s.turn
	}
	s.counts.Items++
	s.put(it)
}

// codexText reads a content field, which is a string or a list of blocks carrying text.
func codexText(raw json.RawMessage) string {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return ""
	}
	if raw[0] == '"' {
		var t string
		_ = json.Unmarshal(raw, &t)
		return t
	}
	var blocks []struct {
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &blocks) != nil {
		return ""
	}
	parts := make([]string, 0, len(blocks))
	for _, b := range blocks {
		if b.Text != "" {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, "\n")
}

// codexCommand reads a command, which is a string or an argv whose last word is the script.
func codexCommand(raw json.RawMessage) string {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return ""
	}
	if raw[0] == '"' {
		var c string
		_ = json.Unmarshal(raw, &c)
		return c
	}
	var argv []string
	if json.Unmarshal(raw, &argv) != nil || len(argv) == 0 {
		return ""
	}
	return argv[len(argv)-1]
}

func (s *codexSource) item(f *codexFile, it *codexItem, at time.Time, off, length int64) {
	switch strings.ToLower(it.Type) {
	case "usermessage":
		raw := codexText(it.Content)
		if strings.TrimSpace(raw) == "" {
			return
		}
		s.noteUser(it.ID, raw)
		clean, cut := capText(displayPaste(raw), maxTextRunes)
		s.turn = it.ID
		s.counts.User++
		s.emit(Item{ID: it.ID, Kind: protocol.FeedItemUser, At: at, Turn: it.ID, Text: clean, Truncated: cut})
	case "agentmessage":
		clean, cut := capText(codexText(it.Content), maxTextRunes)
		if clean == "" {
			return
		}
		s.counts.Text++
		s.emit(Item{ID: it.ID, Kind: protocol.FeedItemText, At: at, Text: clean, Truncated: cut})
	case "commandexecution":
		cmd := codexCommand(it.Command)
		s.counts.Results++
		if isReportCall(cmd) {
			return
		}
		status := "ok"
		if it.ExitCode != nil && *it.ExitCode != 0 {
			status = "error"
		}
		tool := &protocol.AgentFeedTool{Name: "Bash", Category: "run", Summary: sshelement.Line(runSummary(&toolInput{Command: cmd}), maxSummaryRunes),
			Status: status, DurationMs: it.DurationMs, Preview: preview(it.AggregatedOutput), HasOutput: strings.TrimSpace(it.AggregatedOutput) != ""}
		if full := sshelement.Line(firstLine(cmd), 4096); full != tool.Summary {
			tool.Detail = full
		}
		if tool.HasOutput {
			s.refs[it.ID] = codexRef{file: f, offset: off, length: length}
		}
		s.emit(Item{ID: it.ID, Kind: protocol.FeedItemTool, At: at, Tool: tool})
	case "filechange":
		s.counts.Results++
		s.emit(Item{ID: it.ID, Kind: protocol.FeedItemTool, At: at, Tool: &protocol.AgentFeedTool{
			Name: "Edit", Category: "edit", Summary: sshelement.Line(codexChangedPaths(it.Changes), maxSummaryRunes), Status: "ok"}})
	case "mcptoolcall":
		s.counts.Results++
		s.emit(Item{ID: it.ID, Kind: protocol.FeedItemTool, At: at, Tool: &protocol.AgentFeedTool{
			Name: sshelement.Line(it.Tool, 80), Category: "other", Summary: sshelement.Line(it.Server, maxSummaryRunes), Status: "ok"}})
	case "imageview":
		s.counts.Results++
		s.emit(Item{ID: it.ID, Kind: protocol.FeedItemTool, At: at, Tool: &protocol.AgentFeedTool{
			Name: "View", Category: "read", Summary: sshelement.Line(it.Path, maxSummaryRunes), Status: "ok"}})
	}
}

// codexChangedPaths names the files of a change, which is a map or a list keyed by path.
func codexChangedPaths(raw json.RawMessage) string {
	var byPath map[string]json.RawMessage
	if json.Unmarshal(raw, &byPath) == nil {
		paths := make([]string, 0, len(byPath))
		for p := range byPath {
			paths = append(paths, p)
		}
		sort.Strings(paths)
		return strings.Join(paths, ", ")
	}
	var list []struct {
		Path string `json:"path"`
	}
	if json.Unmarshal(raw, &list) == nil {
		paths := make([]string, 0, len(list))
		for _, c := range list {
			paths = append(paths, c.Path)
		}
		return strings.Join(paths, ", ")
	}
	return ""
}

// Output implements Source.
func (s *codexSource) Output(_ context.Context, id string) (string, bool, error) {
	ref, ok := s.refs[id]
	if !ok {
		return "", false, ErrNoOutput
	}
	line, err := readRange(ref.file.tail.path, ref.file.tail.info, ref.offset, ref.length)
	if err != nil {
		return "", false, err
	}
	var l codexLine
	if json.Unmarshal(line, &l) != nil {
		return "", false, errors.New("the log changed")
	}
	var it codexItem
	if json.Unmarshal(l.Payload.Item, &it) != nil || it.ID != id {
		return "", false, errors.New("the log changed")
	}
	return cleanOutput(it.AggregatedOutput)
}

// Thread implements Source. Guardian threads are filtered out, and codex has no fold to
// open.
func (s *codexSource) Thread(context.Context, string) ([]Item, error) {
	return nil, errors.New("no subagent threads for this agent")
}
