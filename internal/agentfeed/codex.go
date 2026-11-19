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
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/sshelement"
)

// This reader was checked against rollouts written by codex-cli 0.167.0 to 0.169.0. The
// capture beside its test (testdata/codex/capture-*.jsonl) is made of records from those
// files: keys, nesting and types are as codex wrote them, values are shortened and every
// identifier, path and text is replaced, because the repository is public.
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

// codexThreadRe is a thread id, which is also the tail of its rollout's file name.
var codexThreadRe = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

type codexSource struct {
	collector
	root     string
	cwd      string
	parent   string // set on a subagent's thread, whose one file is fixed
	agents   map[string]Item
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
	s.agents = map[string]Item{}
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

// readCodexMeta reads a rollout's first line.
func readCodexMeta(path string) (codexMeta, bool) {
	var m codexMeta
	f, _, err := openRegular(path)
	if err != nil {
		return m, false
	}
	defer f.Close()
	// The first line carries the base instructions and is large, but never this large.
	line, err := bufio.NewReaderSize(io.LimitReader(f, maxCodexMetaLine), 64<<10).ReadBytes('\n')
	if err != nil {
		return m, false
	}
	if json.Unmarshal(line, &m) != nil || m.Type != "session_meta" {
		return m, false
	}
	return m, true
}

// belongs decides from a rollout's first line. Anything that is not plainly this session's
// own interactive thread is left out.
func (s *codexSource) belongs(path string) bool {
	m, ok := readCodexMeta(path)
	if !ok {
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

// codexDays lists the newest day directories under root (year/month/day). A sandboxed
// agent can write its own CODEX_HOME, so no level is followed through a symlink: ReadDir
// reports one as itself, never as a directory.
func codexDays(root string) []string {
	levels := []string{root}
	for range 3 {
		var next []string
		for _, dir := range levels {
			entries, err := os.ReadDir(dir)
			if err != nil {
				continue
			}
			for _, e := range entries {
				if e.IsDir() {
					next = append(next, filepath.Join(dir, e.Name()))
				}
			}
		}
		levels = next
	}
	sort.Strings(levels)
	if len(levels) > codexDayDirs {
		levels = levels[len(levels)-codexDayDirs:]
	}
	return levels
}

func (s *codexSource) discover() {
	if s.parent != "" || (time.Since(s.lastWalk) < codexRediscover && !s.lastWalk.IsZero()) {
		return
	}
	s.lastWalk = time.Now()
	days := codexDays(s.root)
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
			if s.parent == "" {
				s.reset()
			}
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
		Type        string          `json:"type"`
		Reason      string          `json:"reason"`
		TurnID      string          `json:"turn_id"`
		Item        json.RawMessage `json:"item"`
		StartedMs   int64           `json:"started_at_ms"`
		CompletedMs int64           `json:"completed_at_ms"`
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
	Duration         json.RawMessage `json:"duration"`
	DurationMs       int64           `json:"duration_ms"`
	Changes          json.RawMessage `json:"changes"`
	Server           string          `json:"server"`
	Tool             string          `json:"tool"`
	AppName          string          `json:"appName"`
	Arguments        json.RawMessage `json:"arguments"`
	Result           json.RawMessage `json:"result"`
	Error            json.RawMessage `json:"error"`
	Path             string          `json:"path"`
	Kind             string          `json:"kind"`
	Query            string          `json:"query"`
	AgentThreadID    string          `json:"agent_thread_id"`
	AgentPath        string          `json:"agent_path"`
}

// codexChange is one file of a FileChange. An update carries a diff, an add or a delete the
// file's whole content.
type codexChange struct {
	Type        string `json:"type"`
	UnifiedDiff string `json:"unified_diff"`
	Content     string `json:"content"`
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
	if l.Type != "event_msg" {
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
		wall := l.Payload.CompletedMs - l.Payload.StartedMs
		s.item(f, &it, at, off, int64(len(line)), wall)
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

// id names an item. A thread's items carry their thread, so a request for one item's
// output can find its way back to the file.
func (s *codexSource) id(raw string) string {
	if s.parent != "" {
		return "agent-" + s.parent + "/" + raw
	}
	return raw
}

func (s *codexSource) emit(it Item) {
	it.ID = s.id(it.ID)
	if it.Turn == "" {
		it.Turn = s.turn
	}
	it.Parent = s.parent
	s.counts.Items++
	s.put(it)
}

// codexText reads a content field, which is a string or a list of blocks carrying text. An
// image block has no text and adds nothing.
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

// codexDuration is how long the item itself says it took: `duration` as secs and nanos, or
// the older `duration_ms`. wall, the span of the record, is the fallback, and it also
// counts the time an approval took.
func codexDuration(it *codexItem, wall int64) int64 {
	var d struct {
		Secs  int64 `json:"secs"`
		Nanos int64 `json:"nanos"`
	}
	// A command started through the unified exec reports microseconds whatever it took.
	if len(it.Duration) > 0 && json.Unmarshal(it.Duration, &d) == nil && d.Secs*1000+d.Nanos/1e6 > 0 {
		return d.Secs*1000 + d.Nanos/1e6
	}
	if it.DurationMs > 0 {
		return it.DurationMs
	}
	return max(wall, 0)
}

// relative shortens a path inside the session's directory. Codex writes some as file URLs.
func (s *codexSource) relative(p string) string {
	p = strings.TrimPrefix(p, "file://")
	if rel, ok := strings.CutPrefix(p, strings.TrimSuffix(s.cwd, "/")+"/"); ok && s.cwd != "" {
		return rel
	}
	return p
}

// codexChanges reads a FileChange: the paths in order, the lines added and removed, and
// the diff a person may open. The older list form names paths only.
func (s *codexSource) codexChanges(raw json.RawMessage) (paths []string, added, removed int, diff string) {
	var byPath map[string]codexChange
	if json.Unmarshal(raw, &byPath) != nil {
		var list []struct {
			Path string `json:"path"`
		}
		_ = json.Unmarshal(raw, &list)
		for _, c := range list {
			paths = append(paths, s.relative(c.Path))
		}
		return paths, 0, 0, ""
	}
	keys := make([]string, 0, len(byPath))
	for p := range byPath {
		keys = append(keys, p)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, p := range keys {
		c := byPath[p]
		rel := s.relative(p)
		paths = append(paths, rel)
		lines := 0
		if body := strings.TrimRight(c.Content, "\n"); body != "" {
			lines = strings.Count(body, "\n") + 1
		}
		switch {
		case c.UnifiedDiff != "":
			for l := range strings.SplitSeq(c.UnifiedDiff, "\n") {
				switch {
				case strings.HasPrefix(l, "+++") || strings.HasPrefix(l, "---"):
				case strings.HasPrefix(l, "+"):
					added++
				case strings.HasPrefix(l, "-"):
					removed++
				}
			}
			fmt.Fprintf(&b, "--- %s\n%s\n", rel, strings.TrimRight(c.UnifiedDiff, "\n"))
		case c.Type == "delete":
			removed += lines
			fmt.Fprintf(&b, "--- %s (deleted, %d lines)\n", rel, lines)
		default:
			added += lines
			fmt.Fprintf(&b, "--- %s (new, %d lines)\n", rel, lines)
		}
	}
	return paths, added, removed, b.String()
}

// codexMcpOutput is what an MCP call answered: its text blocks, or the error's message.
func codexMcpOutput(it *codexItem) (text string, failed bool) {
	var r struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	_ = json.Unmarshal(it.Result, &r)
	parts := make([]string, 0, len(r.Content))
	for _, c := range r.Content {
		if c.Text != "" {
			parts = append(parts, c.Text)
		}
	}
	var e struct {
		Message string `json:"message"`
	}
	if _ = json.Unmarshal(it.Error, &e); e.Message != "" {
		parts = append(parts, e.Message)
	}
	return strings.Join(parts, "\n"), r.IsError || e.Message != "" || it.Status == "failed"
}

// codexOutput is the text a person opens on a tool row, read from the item itself.
func (s *codexSource) codexOutput(it *codexItem) string {
	switch strings.ToLower(it.Type) {
	case "commandexecution":
		return it.AggregatedOutput
	case "filechange":
		_, _, _, diff := s.codexChanges(it.Changes)
		return diff
	case "mcptoolcall":
		text, _ := codexMcpOutput(it)
		return text
	}
	return ""
}

// tool emits one finished tool row and remembers where its output is.
func (s *codexSource) tool(f *codexFile, it *codexItem, at time.Time, off, length int64, tool *protocol.AgentFeedTool) {
	s.counts.Results++
	if out := s.codexOutput(it); strings.TrimSpace(out) != "" {
		tool.Preview, tool.HasOutput = preview(out), true
		s.refs[s.id(it.ID)] = codexRef{file: f, offset: off, length: length}
	}
	s.emit(Item{ID: it.ID, Kind: protocol.FeedItemTool, At: at, Tool: tool})
}

func (s *codexSource) item(f *codexFile, it *codexItem, at time.Time, off, length, wall int64) {
	status := "ok"
	if it.Status == "failed" || it.Status == "declined" || (it.ExitCode != nil && *it.ExitCode != 0) {
		status = "error"
	}
	switch strings.ToLower(it.Type) {
	case "usermessage":
		raw := codexText(it.Content)
		if strings.TrimSpace(raw) == "" {
			return
		}
		id := s.id(it.ID)
		s.noteUser(id, raw)
		clean, cut := capText(displayPaste(raw), maxTextRunes)
		s.turn = id
		s.counts.User++
		s.emit(Item{ID: it.ID, Kind: protocol.FeedItemUser, At: at, Turn: id, Text: clean, Truncated: cut})
	case "agentmessage":
		clean, cut := capText(codexText(it.Content), maxTextRunes)
		if clean == "" {
			return
		}
		s.counts.Text++
		s.emit(Item{ID: it.ID, Kind: protocol.FeedItemText, At: at, Text: clean, Truncated: cut})
	case "commandexecution":
		cmd := codexCommand(it.Command)
		if isReportCall(codexLolaCall(cmd)) {
			s.counts.Results++
			return
		}
		tool := &protocol.AgentFeedTool{Name: "Bash", Category: "run", Summary: sshelement.Line(runSummary(&toolInput{Command: cmd}), maxSummaryRunes),
			Status: status, DurationMs: codexDuration(it, wall)}
		if full := sshelement.Line(firstLine(cmd), 4096); full != tool.Summary {
			tool.Detail = full
		}
		s.tool(f, it, at, off, length, tool)
	case "filechange":
		paths, added, removed, _ := s.codexChanges(it.Changes)
		s.tool(f, it, at, off, length, &protocol.AgentFeedTool{Name: "Edit", Category: "edit",
			Summary: sshelement.Line(strings.Join(paths, ", "), maxSummaryRunes), Status: status, Added: added, Removed: removed})
	case "mcptoolcall":
		if _, failed := codexMcpOutput(it); failed {
			status = "error"
		}
		summary := firstShortString(it.Arguments)
		if summary == "" {
			summary = firstOf(it.AppName, it.Server)
		}
		s.tool(f, it, at, off, length, &protocol.AgentFeedTool{Name: sshelement.Line(firstOf(it.Tool, it.Server), 80), Category: "other",
			Summary: sshelement.Line(summary, maxSummaryRunes), Status: status, DurationMs: codexDuration(it, wall)})
	case "imageview":
		s.tool(f, it, at, off, length, &protocol.AgentFeedTool{Name: "View", Category: "read",
			Summary: sshelement.Line(s.relative(it.Path), maxSummaryRunes), Status: status})
	case "extension":
		// A web search or a page it opened. A clock sleep is the agent waiting, not a tool.
		if it.Kind != "web.search" || it.Query == "" {
			return
		}
		s.tool(f, it, at, off, length, &protocol.AgentFeedTool{Name: "WebSearch", Category: "read",
			Summary: sshelement.Line(it.Query, maxSummaryRunes), Status: status})
	case "subagentactivity":
		s.subagent(it, at)
	}
}

// codexLolaCall names lola the way isReportCall expects it. Codex resolves the binary and
// runs it by its path.
func codexLolaCall(cmd string) string {
	cmd = strings.TrimSpace(cmd)
	word, rest, _ := strings.Cut(cmd, " ")
	if strings.HasPrefix(word, "/") && filepath.Base(word) == "lola" {
		return strings.TrimSpace("lola " + rest)
	}
	return cmd
}

// firstOf is the first of two strings that says something.
func firstOf(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// subagent keeps one row per subagent thread: opened when it starts, closed when it
// completes. What passes between the two is in the thread, which Thread reads on demand.
func (s *codexSource) subagent(it *codexItem, at time.Time) {
	thread := it.AgentThreadID
	if !codexThreadRe.MatchString(thread) {
		return
	}
	row, open := s.agents[thread]
	switch it.Kind {
	case "started":
		if open {
			return
		}
		s.counts.AgentCalls++
		name := it.AgentPath
		if i := strings.LastIndexByte(name, '/'); i >= 0 {
			name = name[i+1:]
		}
		row = Item{ID: "subagent:" + thread, Kind: protocol.FeedItemTool, At: at, Tool: &protocol.AgentFeedTool{
			Name: "Agent", Category: "agent", Summary: sshelement.Line(name, maxSummaryRunes), Status: "running", Subagent: thread}}
		s.emit(row)
		row = s.changed[s.at[s.id(row.ID)]]
		row.ID = "subagent:" + thread // emit names it again
		s.agents[thread] = row
	case "completed":
		if !open || row.Tool.Status != "running" {
			return
		}
		tool := *row.Tool
		tool.Status = "ok"
		if !at.IsZero() && !row.At.IsZero() && at.After(row.At) {
			tool.DurationMs = at.Sub(row.At).Milliseconds()
		}
		row.Tool = &tool
		s.agents[thread] = row
		s.counts.Results++
		s.emit(row)
	}
}

// Output implements Source.
func (s *codexSource) Output(ctx context.Context, id string) (string, bool, error) {
	if rest, ok := strings.CutPrefix(id, "agent-"); ok && s.parent == "" {
		parent, _, found := strings.Cut(rest, "/")
		if !found {
			return "", false, errors.New("no such item")
		}
		thread, err := s.threadSource(parent)
		if err != nil {
			return "", false, err
		}
		thread.drain(ctx)
		return thread.Output(ctx, id)
	}
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
	var mismatch *json.UnmarshalTypeError
	if err := json.Unmarshal(l.Payload.Item, &it); (err != nil && !errors.As(err, &mismatch)) || s.id(it.ID) != id {
		return "", false, errors.New("the log changed")
	}
	return cleanOutput(s.codexOutput(&it))
}

// threadPath finds a subagent's rollout, whose file name ends in its thread id. The file
// must say it is a child thread working in this session's directory, and not a guardian.
func (s *codexSource) threadPath(thread string) (string, error) {
	if !codexThreadRe.MatchString(thread) {
		return "", errors.New("no such subagent")
	}
	days := codexDays(s.root)
	slices.Reverse(days)
	for _, day := range days {
		entries, err := os.ReadDir(day)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.Type().IsRegular() || !codexRolloutRe.MatchString(e.Name()) || !strings.HasSuffix(e.Name(), "-"+thread+".jsonl") {
				continue
			}
			p := filepath.Join(day, e.Name())
			m, ok := readCodexMeta(p)
			if !ok || m.Payload.ParentThreadID == "" || filepath.Clean(m.Payload.Cwd) != s.cwd ||
				strings.Contains(strings.ToLower(m.Payload.ThreadSource), "guardian") {
				return "", errors.New("no such subagent")
			}
			return p, nil
		}
	}
	return "", errors.New("no such subagent")
}

// threadSource prepares a reader for one subagent's rollout. Its items carry Parent, and
// their ids name the thread.
func (s *codexSource) threadSource(thread string) (*codexSource, error) {
	path, err := s.threadPath(thread)
	if err != nil {
		return nil, err
	}
	t := &codexSource{root: s.root, cwd: s.cwd}
	t.reset()
	t.parent = thread
	f := &codexFile{tail: tail{path: path}}
	t.files, t.byPath[path] = []*codexFile{f}, f
	return t, nil
}

// drain reads a thread to its end, within the bound.
func (s *codexSource) drain(ctx context.Context) []Item {
	var all []Item
	for ctx.Err() == nil && s.counts.Bytes <= maxThreadBytes {
		changed, _ := s.Advance(ctx, 8<<20)
		all = append(all, changed...)
		if s.eof {
			break
		}
	}
	return all
}

// Thread implements Source: one subagent's conversation, read on demand. Guardian threads
// are never one.
func (s *codexSource) Thread(ctx context.Context, parent string) ([]Item, error) {
	t, err := s.threadSource(parent)
	if err != nil {
		return nil, err
	}
	feed := newFeedOf(t, 0)
	feed.apply(t.drain(ctx))
	return feed.All(), nil
}
