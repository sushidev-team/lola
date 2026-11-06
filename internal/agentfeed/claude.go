package agentfeed

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/sshelement"
)

// segmentRe is the only shape of transcript name lola will read.
var segmentRe = regexp.MustCompile(`^[0-9a-f-]{36}\.jsonl$`)

// agentIDRe bounds a subagent id, which reaches a file name.
var agentIDRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// ValidSegment says whether name may be read as a transcript.
func ValidSegment(name string) bool { return segmentRe.MatchString(name) }

// Prefix skips for the bulkiest bookkeeping records: about 40% of a real transcript. An
// escaped copy of these bytes inside tool output cannot match at the start of a line, and
// the same goes for the allowlist below, whose unescaped quotes cannot occur inside a
// JSON string.
var (
	skipPrefixes = [][]byte{[]byte(`{"type":"api-request`), []byte(`{"type":"attachment`), []byte(`{"type":"file-history`)}
	typeUser     = []byte(`"type":"user"`)
	typeAsst     = []byte(`"type":"assistant"`)
	typeSystem   = []byte(`"type":"system"`)
	typeQueued   = []byte(`"type":"queued_command"`)
)

var (
	commandNameRe = regexp.MustCompile(`(?s)<command-name>(.*?)</command-name>`)
	commandArgsRe = regexp.MustCompile(`(?s)<command-args>(.*?)</command-args>`)
	localOutRe    = regexp.MustCompile(`(?s)<local-command-std(?:out|err)>(.*?)</local-command-std(?:out|err)>`)
	bashInputRe   = regexp.MustCompile(`(?s)<bash-input>(.*?)</bash-input>`)
	bashOutRe     = regexp.MustCompile(`(?s)<bash-std(?:out|err)>(.*?)</bash-std(?:out|err)>`)
	taskSummaryRe = regexp.MustCompile(`(?s)<summary>(.*?)</summary>`)
	// teammateRe opens a record another agent sent. The optional lead is the sentence
	// claude-code writes above the tag. Anchored, so a person quoting the tag mid-message
	// still reads as what they typed.
	teammateRe    = regexp.MustCompile(`^(?:[^\n<]*sent a message:\s*)?<teammate-message\b`)
	teammateTagRe = regexp.MustCompile(`<teammate-message\b([^>]*)>`)
	teammateIDRe  = regexp.MustCompile(`\bteammate_id="([^"]*)"`)
	teammateSumRe = regexp.MustCompile(`\bsummary="([^"]*)"`)
	imageTokenRe  = regexp.MustCompile(`\[Image #\d+\]`)
	leadingCdRe   = regexp.MustCompile(`^cd\s+(?:"[^"\n]*"|'[^'\n]*'|[^\s&;|]+)\s*(?:&&|;)\s*`)
	heredocOpenRe = regexp.MustCompile(`<<-?\s*['"]?[A-Za-z_][A-Za-z0-9_]*['"]?\s*$`)
	// toolUseIDRe reads an oversized result line's head. The unescaped quotes cannot
	// occur inside a JSON string, so tool output cannot forge it.
	toolUseIDRe = regexp.MustCompile(`"tool_use_id":"([^"\\]{1,128})"`)
)

// imageMark stands in for an image block. Never the base64.
const imageMark = "[Image]"

// userWantsNoneText opens the result claude-code writes when a call is interrupted or a
// permission prompt is answered No.
const userWantsNoneText = "The user doesn't want to proceed"

// claudeRecord is the only shape decoded from a line. Every field tolerates being absent,
// and a field of the wrong type leaves the rest of the record usable.
type claudeRecord struct {
	Type             string `json:"type"`
	Subtype          string `json:"subtype"`
	UUID             string `json:"uuid"`
	Timestamp        string `json:"timestamp"`
	Cwd              string `json:"cwd"`
	IsMeta           bool   `json:"isMeta"`
	IsSidechain      bool   `json:"isSidechain"`
	IsCompactSummary bool   `json:"isCompactSummary"`
	IsAPIError       bool   `json:"isApiErrorMessage"`
	PromptSource     string `json:"promptSource"`
	Origin           struct {
		Kind string `json:"kind"`
	} `json:"origin"`
	Content       json.RawMessage `json:"content"`
	Attachment    json.RawMessage `json:"attachment"`
	ToolUseResult json.RawMessage `json:"toolUseResult"`
	Message       struct {
		Model   string          `json:"model"`
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

type claudeBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	IsError   bool            `json:"is_error"`
	Content   json.RawMessage `json:"content"`
}

// queuedCommand is an attachment claude-code writes for something that arrived mid-turn: a
// person typing while the agent works, or a background task reporting. In most cases no user
// record follows, so this is the only trace of it. Prompt is a string, or a list of blocks when
// an image came along.
type queuedCommand struct {
	Type   string          `json:"type"`
	Prompt json.RawMessage `json:"prompt"`
	Origin struct {
		Kind string `json:"kind"`
	} `json:"origin"`
}

// queuedDedupWindow is how long a queued message can claim a user record of the same words.
// Past it, the same words again are someone saying them again.
const queuedDedupWindow = 10 * time.Minute

// decodeRecord accepts a type mismatch in a single field, which encoding/json reports
// only after finishing the rest of the record.
func decodeRecord(line []byte, r *claudeRecord) bool {
	err := json.Unmarshal(line, r)
	var mismatch *json.UnmarshalTypeError
	return err == nil || errors.As(err, &mismatch)
}

// outputRef is where a tool's output lives: lola keeps this instead of the body, because
// a single result can be over a megabyte and most are never opened.
type outputRef struct {
	seg    *claudeSegment
	offset int64
	length int64
	useID  string
}

type openTool struct {
	item    Item
	started time.Time
}

type claudeSegment struct {
	base    string
	tail    tail
	dup     bool // it re-contains records already seen: a /branch copy
	started bool
}

// maxRefs bounds remembered output locations. A Feed holds at most 5,000 items, so
// anything older is reached through a rescan, which keeps every ref.
const maxRefs = 6000

type claudeSource struct {
	collector
	dir      string
	segments func() []string
	parent   string // non-empty: one subagent's thread, read from a fixed path
	keepRefs bool

	segs     []*claudeSegment
	byBase   map[string]*claudeSegment
	seen     map[string]struct{}
	tools    map[string]*openTool
	dropped  map[string]struct{}
	refs     map[string]outputRef
	refOrder []string
	turn     string
	cwd      string
	lastCmd  string
	// queued is what mid-turn attachments already showed and when, so that a user record of
	// the same words does not show it twice.
	queued  map[string]time.Time
	eof     bool
	anyFile bool
}

// NewClaude reads the transcripts named by segments, in that order, directly under
// root/slug. segments is asked on every pass, and a name that does not look like a
// transcript is ignored.
func NewClaude(root, slug string, segments func() []string) Source {
	s := &claudeSource{dir: filepath.Join(root, slug), segments: segments}
	if slug == "" || slug != filepath.Base(slug) {
		s.dir = ""
	}
	s.reset()
	return s
}

func (s *claudeSource) reset() {
	s.segs, s.byBase = nil, map[string]*claudeSegment{}
	s.seen = map[string]struct{}{}
	s.tools = map[string]*openTool{}
	s.dropped = map[string]struct{}{}
	s.refs, s.refOrder = map[string]outputRef{}, nil
	s.turn, s.cwd, s.lastCmd = "", "", ""
	s.queued = map[string]time.Time{}
	s.eof, s.anyFile = false, false
}

// sync adds segments newly named by the caller. Existing ones keep their position.
func (s *claudeSource) sync() {
	if s.segments == nil {
		return
	}
	for _, base := range s.segments() {
		if !s.acceptable(base) || s.byBase[base] != nil {
			continue
		}
		seg := &claudeSegment{base: base, tail: tail{path: join(s.dir, base)}}
		s.byBase[base] = seg
		s.segs = append(s.segs, seg)
	}
}

func (s *claudeSource) acceptable(base string) bool {
	return s.parent != "" || ValidSegment(base)
}

// Advance implements Source.
func (s *claudeSource) Advance(ctx context.Context, budget int64) ([]Item, Health) {
	if budget <= 0 {
		budget = chunkBytes
	}
	s.sync()
	if s.dir == "" || (s.parent == "" && !plainDir(s.dir)) {
		s.eof = true
		return nil, protocol.FeedEmpty
	}
	s.anyFile = false
	for _, seg := range s.segs {
		if budget <= 0 {
			break
		}
		n, st := seg.tail.advance(ctx, budget,
			func(line []byte, off int64) { s.line(seg, line, off) },
			func(head []byte) { s.oversize(head) })
		switch st {
		case tailReplaced:
			// A replaced file invalidates every order and id handed out so far.
			s.restart()
			s.reset()
			return nil, protocol.FeedOK
		case tailOK:
			s.anyFile = true
		}
		s.counts.Bytes += n
		budget -= n
	}
	s.eof = true
	for _, seg := range s.segs {
		s.eof = s.eof && seg.tail.atEOF
		s.anyFile = s.anyFile || seg.tail.info != nil
	}
	return s.take(), s.health()
}

func (s *claudeSource) health() Health {
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
func (s *claudeSource) AtEOF() bool { return s.eof }

func (s *claudeSource) retainRefs() { s.keepRefs = true }

func (s *claudeSource) id(raw string) string {
	if s.parent != "" {
		return "agent-" + s.parent + "/" + raw
	}
	return raw
}

func (s *claudeSource) line(seg *claudeSegment, line []byte, off int64) {
	queued := bytes.Contains(line, typeQueued)
	for _, p := range skipPrefixes {
		if !queued && bytes.HasPrefix(line, p) {
			return
		}
	}
	if !queued && !bytes.Contains(line, typeUser) && !bytes.Contains(line, typeAsst) && !bytes.Contains(line, typeSystem) {
		return // an unknown or uninteresting record type is tolerated, never an error
	}
	var r claudeRecord
	if !decodeRecord(line, &r) {
		return
	}
	if r.UUID != "" {
		if _, dup := s.seen[r.UUID]; dup {
			seg.dup = true
			return
		}
		s.seen[r.UUID] = struct{}{}
	} else {
		r.UUID = fmt.Sprintf("%s@%d", seg.base, off)
	}
	if r.IsSidechain && s.parent == "" {
		return
	}
	if r.Cwd != "" {
		s.cwd = r.Cwd
	}
	switch r.Type {
	case "user":
		s.user(seg, &r, off, int64(len(line)))
	case "assistant":
		s.assistant(seg, &r)
	case "system":
		s.system(seg, &r)
	case "attachment":
		s.attachment(seg, &r)
	}
}

// attachment reads the one kind of attachment that says something: a queued command from a
// person or a background task. It goes through the same sorting as a user record, so a task's
// notice reads as one and a slash command stays a slash command. Another agent's message is
// not shown, as elsewhere.
func (s *claudeSource) attachment(seg *claudeSegment, r *claudeRecord) {
	var q queuedCommand
	if json.Unmarshal(r.Attachment, &q) != nil || q.Type != "queued_command" {
		return
	}
	if q.Origin.Kind != "human" && q.Origin.Kind != "task-notification" {
		return
	}
	text := queuedText(q.Prompt)
	if strings.TrimSpace(text) == "" {
		return
	}
	r.Origin = q.Origin
	if q.Origin.Kind == "human" {
		s.queued[strings.TrimSpace(text)] = parseAt(r.Timestamp)
	}
	s.userText(seg, r, text)
}

// queuedText reads a queued prompt in either of its shapes. An image is named, never carried.
func queuedText(prompt json.RawMessage) string {
	prompt = bytes.TrimSpace(prompt)
	if len(prompt) == 0 {
		return ""
	}
	if prompt[0] == '"' {
		var text string
		_ = json.Unmarshal(prompt, &text)
		return text
	}
	var blocks []claudeBlock
	if json.Unmarshal(prompt, &blocks) != nil {
		return ""
	}
	var texts []string
	for _, b := range blocks {
		switch b.Type {
		case "text":
			texts = append(texts, b.Text)
		case "image":
			texts = append(texts, "[Image]")
		}
	}
	return strings.Join(texts, "\n")
}

// shownAsQueued says a user record repeats a queued message already on screen.
func (s *claudeSource) shownAsQueued(r *claudeRecord, text string) bool {
	key := strings.TrimSpace(text)
	at, ok := s.queued[key]
	if !ok {
		return false
	}
	delete(s.queued, key)
	diff := parseAt(r.Timestamp).Sub(at)
	return diff >= 0 && diff <= queuedDedupWindow
}

func (s *claudeSource) emit(seg *claudeSegment, it Item) {
	s.startSegment(seg, it.At)
	it.Parent = s.parent
	if it.Turn == "" {
		it.Turn = s.turn
	}
	s.counts.Items++
	s.put(it)
}

// startSegment puts a divider before the first new thing a later segment says. It waits
// for that first new record so a /branch copy, whose history is all duplicates, divides
// where the conversation actually moved on.
func (s *claudeSource) startSegment(seg *claudeSegment, at time.Time) {
	if seg.started {
		return
	}
	seg.started = true
	if len(s.segs) == 0 || seg == s.segs[0] || s.parent != "" {
		return
	}
	text := "Context cleared"
	if seg.dup {
		text = "Conversation branched"
	}
	s.counts.Items++
	s.put(Item{ID: s.id("segment:" + seg.base), Kind: protocol.FeedItemDivider, At: at, Text: text, Parent: s.parent, Turn: s.turn})
}

func parseAt(ts string) time.Time {
	t, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		return time.Time{}
	}
	return t
}

func (s *claudeSource) system(seg *claudeSegment, r *claudeRecord) {
	at := parseAt(r.Timestamp)
	switch r.Subtype {
	case "compact_boundary":
		s.emit(seg, Item{ID: s.id(r.UUID + ":0"), Kind: protocol.FeedItemDivider, At: at, Text: "Compacted"})
	case "local_command":
		var text string
		if json.Unmarshal(r.Content, &text) == nil {
			s.userText(seg, r, text)
		}
	}
}

func (s *claudeSource) user(seg *claudeSegment, r *claudeRecord, off, length int64) {
	content := bytes.TrimSpace(r.Message.Content)
	if len(content) == 0 {
		return
	}
	if content[0] == '"' {
		var text string
		if json.Unmarshal(content, &text) == nil {
			s.userText(seg, r, text)
		}
		return
	}
	var blocks []claudeBlock
	if json.Unmarshal(content, &blocks) != nil {
		return
	}
	var texts []string
	for _, b := range blocks {
		switch b.Type {
		case "tool_result":
			s.result(seg, r, &b, off, length)
		case "text":
			texts = append(texts, b.Text)
		case "image":
			texts = append(texts, imageMark)
		}
	}
	if len(texts) > 0 {
		s.userText(seg, r, strings.Join(dropImageMark(texts), "\n"))
	}
}

// dropImageMark removes the reader's own [Image] when the text already carries
// claude-code's [Image #n], or the row announces the same picture twice.
func dropImageMark(texts []string) []string {
	if !imageTokenRe.MatchString(strings.Join(texts, "\n")) {
		return texts
	}
	kept := texts[:0:0]
	for _, t := range texts {
		if t != imageMark {
			kept = append(kept, t)
		}
	}
	return kept
}

// teammateLine is the one line a record from another agent reads as. Its markup and any JSON
// inside stay out of the story: the sender's own summary if it wrote one, else just who.
func teammateLine(text string) string {
	from, summary, count := "", "", 0
	for _, m := range teammateTagRe.FindAllStringSubmatch(text, -1) {
		count++
		if id := teammateIDRe.FindStringSubmatch(m[1]); id != nil && from == "" {
			from = id[1]
		}
		if sum := teammateSumRe.FindStringSubmatch(m[1]); sum != nil && summary == "" {
			summary = sum[1]
		}
	}
	line := "Message from another agent"
	if from != "" {
		line = "Message from " + from
	}
	if summary != "" {
		line += " · " + summary
	}
	if count > 1 {
		line += fmt.Sprintf(" · %d messages", count)
	}
	return sshelement.Line(line, maxSummaryRunes)
}

// userText sorts what a user record says. A record of this type is often not a person:
// claude-code writes command output, background-task notices and interrupts the same way.
func (s *claudeSource) userText(seg *claudeSegment, r *claudeRecord, text string) {
	at := parseAt(r.Timestamp)
	id := s.id(r.UUID + ":0")
	trimmed := strings.TrimSpace(text)
	switch {
	case strings.HasPrefix(trimmed, "<task-notification>") || r.Origin.Kind == "task-notification":
		summary := ""
		if m := taskSummaryRe.FindStringSubmatch(trimmed); m != nil {
			summary = sshelement.Line(m[1], maxSummaryRunes)
		}
		s.turn = id
		s.counts.User++
		s.emit(seg, Item{ID: id, Kind: protocol.FeedItemUser, At: at, Turn: id, Text: summary, Origin: "task"})
		return
	case r.IsMeta || r.IsCompactSummary || trimmed == "":
		return
	case strings.HasPrefix(trimmed, "[Request interrupted"):
		s.interrupt()
		s.emit(seg, Item{ID: id, Kind: protocol.FeedItemNotice, At: at, Text: "Interrupted"})
		return
	case strings.HasPrefix(trimmed, "<local-command-caveat>"):
		return
	}
	if m := commandNameRe.FindStringSubmatch(trimmed); m != nil {
		cmd := strings.TrimSpace(m[1])
		if !strings.HasPrefix(cmd, "/") {
			cmd = "/" + cmd
		}
		if a := commandArgsRe.FindStringSubmatch(trimmed); a != nil && strings.TrimSpace(a[1]) != "" {
			cmd += " " + strings.TrimSpace(a[1])
		}
		// A slash command is written twice in some versions, as a system record and a
		// user record, one straight after the other.
		if s.lastCmd == cmd {
			return
		}
		s.lastCmd = cmd
		s.userItem(seg, id, at, cmd, r)
		return
	}
	s.lastCmd = ""
	if m := localOutRe.FindStringSubmatch(trimmed); m != nil {
		s.notice(seg, id, at, m[1])
		return
	}
	if teammateRe.MatchString(trimmed) {
		// Another agent, not a person, so this must not wear "In terminal". It still opens a
		// turn, since the agent answers it, and it is not a user prompt to the health check.
		s.turn = id
		s.emit(seg, Item{ID: id, Kind: protocol.FeedItemUser, At: at, Turn: id, Text: teammateLine(trimmed), Origin: "teammate"})
		return
	}
	if m := bashInputRe.FindStringSubmatch(trimmed); m != nil {
		s.userItem(seg, id, at, "!"+strings.TrimSpace(m[1]), r)
		return
	}
	if ms := bashOutRe.FindAllStringSubmatch(trimmed, -1); ms != nil {
		var out []string
		for _, m := range ms {
			out = append(out, m[1])
		}
		s.notice(seg, id, at, strings.Join(out, "\n"))
		return
	}
	s.userItem(seg, id, at, text, r)
}

func (s *claudeSource) notice(seg *claudeSegment, id string, at time.Time, text string) {
	clean, cut := capText(text, maxNoticeRunes)
	if clean == "" {
		return
	}
	s.emit(seg, Item{ID: id, Kind: protocol.FeedItemNotice, At: at, Text: clean, Truncated: cut})
}

func (s *claudeSource) userItem(seg *claudeSegment, id string, at time.Time, raw string, r *claudeRecord) {
	if r.Type == "user" && s.shownAsQueued(r, raw) {
		return
	}
	// Hash before the sanitizer and the cap, so a long hand-off still matches its send.
	s.noteUser(id, raw)
	clean, cut := capText(displayPaste(raw), maxTextRunes)
	origin := ""
	if r.PromptSource == "system" || r.Origin.Kind == "auto-continuation" {
		origin = "system"
	}
	s.turn = id
	s.counts.User++
	s.emit(seg, Item{ID: id, Kind: protocol.FeedItemUser, At: at, Turn: id, Text: clean, Truncated: cut, Origin: origin})
}

// interrupt closes every call still open. Esc writes no result for one that had not
// reached its end, and a row must not read "running" past the interrupt.
func (s *claudeSource) interrupt() {
	ids := make([]string, 0, len(s.tools))
	for id := range s.tools {
		ids = append(ids, id)
	}
	sort.Strings(ids) // a rescan must hand out the same changes in the same order
	for _, id := range ids {
		t := s.tools[id]
		tool := *t.item.Tool
		tool.Status = "interrupted"
		t.item.Tool = &tool
		s.put(t.item)
		delete(s.tools, id)
	}
}

func (s *claudeSource) assistant(seg *claudeSegment, r *claudeRecord) {
	at := parseAt(r.Timestamp)
	if !bytes.HasPrefix(bytes.TrimSpace(r.Message.Content), []byte("[")) {
		return
	}
	var blocks []claudeBlock
	if json.Unmarshal(r.Message.Content, &blocks) != nil {
		return
	}
	s.lastCmd = ""
	for i, b := range blocks {
		id := s.id(fmt.Sprintf("%s:%d", r.UUID, i))
		switch b.Type {
		case "text":
			if strings.TrimSpace(b.Text) == "" {
				continue
			}
			if r.IsAPIError {
				s.notice(seg, id, at, b.Text)
				continue
			}
			if r.Message.Model == "<synthetic>" {
				continue
			}
			clean, cut := capText(b.Text, maxTextRunes)
			s.counts.Text++
			s.emit(seg, Item{ID: id, Kind: protocol.FeedItemText, At: at, Text: clean, Truncated: cut})
		case "tool_use":
			s.toolUse(seg, id, at, &b)
		}
		// A thinking block yields nothing: nearly all are empty in a real log.
	}
}

// toolInput is the part of a call's input a row can summarize. A Write's content is
// decoded only so its lines can be counted, and is never kept.
type toolInput struct {
	Command      string `json:"command"`
	FilePath     string `json:"file_path"`
	NotebookPath string `json:"notebook_path"`
	Path         string `json:"path"`
	Pattern      string `json:"pattern"`
	Query        string `json:"query"`
	URL          string `json:"url"`
	Description  string `json:"description"`
	Content      string `json:"content"`
	Skill        string `json:"skill"`
	To           string `json:"to"`
	Questions    []struct {
		Question string `json:"question"`
	} `json:"questions"`
}

// runSummary says what a command actually does in one line. A leading `cd <dir> &&` pushed the
// real command out of the clip, and a first line that only opens a here-doc or a quote names
// nothing, so it reads as the tool's own description or says that more follows.
var leadingAssignRe = regexp.MustCompile(`^(?:[A-Za-z_]\w*=(?:"[^"\n]*"|'[^'\n]*'|[^\s;&|]+)\s*(?:;|&&)?\s*)+`)

func runSummary(in *toolInput) string {
	command := leadingCdRe.ReplaceAllString(strings.TrimSpace(in.Command), "")
	command = leadingAssignRe.ReplaceAllString(command, "")
	line := firstLine(command)
	if line == "" {
		line = firstLine(in.Command)
	}
	if heredocOpenRe.MatchString(line) || strings.Count(line, `"`)%2 == 1 || strings.Count(line, "'")%2 == 1 {
		if in.Description != "" {
			return in.Description
		}
		return line + " …"
	}
	return line
}

// firstShortString is the first short, one-line string among a tool's inputs, in the order the
// tool wrote them, for the many tools whose inputs lola has no name for. A blank row says nothing,
// and repeated calls to one tool would collapse into its name. `content` stays out: a Write's body
// is never kept.
func firstShortString(raw json.RawMessage) string {
	dec := json.NewDecoder(bytes.NewReader(raw))
	if t, err := dec.Token(); err != nil || t != json.Delim('{') {
		return ""
	}
	for dec.More() {
		key, err := dec.Token()
		if err != nil {
			return ""
		}
		var v json.RawMessage
		if dec.Decode(&v) != nil {
			return ""
		}
		var text string
		if key == "content" || json.Unmarshal(v, &text) != nil {
			continue
		}
		if text = strings.TrimSpace(text); text != "" && !strings.Contains(text, "\n") && utf8.RuneCountInString(text) <= maxSummaryRunes {
			return text
		}
	}
	return ""
}

func (s *claudeSource) relative(p string) string {
	if s.cwd != "" {
		if rel, ok := strings.CutPrefix(p, strings.TrimSuffix(s.cwd, "/")+"/"); ok {
			return rel
		}
	}
	return p
}

func (s *claudeSource) toolSummary(name string, in *toolInput, raw json.RawMessage) string {
	var v string
	switch toolCategory(name) {
	case "run":
		v = runSummary(in)
	case "agent":
		v = in.Description
	default:
		question := ""
		if len(in.Questions) > 0 {
			question = in.Questions[0].Question
		}
		for _, c := range []string{in.FilePath, in.NotebookPath, in.Pattern, in.Query, in.URL, in.Path, in.Description, in.Skill, in.To, question, firstLine(in.Command)} {
			if c != "" {
				v = c
				break
			}
		}
		if v == "" {
			v = firstShortString(raw)
		}
		if v != "" && (v == in.FilePath || v == in.NotebookPath) {
			v = s.relative(v)
		}
	}
	return sshelement.Line(v, maxSummaryRunes)
}

func (s *claudeSource) toolUse(seg *claudeSegment, id string, at time.Time, b *claudeBlock) {
	var in toolInput
	_ = json.Unmarshal(b.Input, &in) // a mismatch leaves a blank summary, not a lost row
	if toolCategory(b.Name) == "run" && isReportCall(in.Command) {
		s.dropped[b.ID] = struct{}{}
		return
	}
	tool := &protocol.AgentFeedTool{
		Name:     sshelement.Line(b.Name, 80),
		Category: toolCategory(b.Name),
		Summary:  s.toolSummary(b.Name, &in, b.Input),
		Status:   "running",
	}
	// The row's tooltip names the whole line when the summary shortened or replaced it.
	if tool.Category == "run" {
		if full := sshelement.Line(firstLine(in.Command), 4096); full != tool.Summary {
			tool.Detail = full
		}
	}
	if tool.Category == "agent" {
		s.counts.AgentCalls++
	}
	if b.Name == "Write" && in.Content != "" {
		tool.Added = strings.Count(strings.TrimRight(in.Content, "\n"), "\n") + 1
	}
	it := Item{ID: id, Kind: protocol.FeedItemTool, At: at, Tool: tool}
	s.emit(seg, it)
	it = s.changed[s.at[id]]
	s.tools[b.ID] = &openTool{item: it, started: at}
}

// resultDetails is the structured half of a result. It is decoded only for the calls
// that use it, since a Read's copy of the file sits in the same field.
type resultDetails struct {
	AgentID         string `json:"agentId"`
	Interrupted     bool   `json:"interrupted"`
	StructuredPatch []struct {
		Lines    []string `json:"lines"`
		OldStart int      `json:"oldStart"`
		OldLines int      `json:"oldLines"`
		NewStart int      `json:"newStart"`
		NewLines int      `json:"newLines"`
	} `json:"structuredPatch"`
}

func (s *claudeSource) result(seg *claudeSegment, r *claudeRecord, b *claudeBlock, off, length int64) {
	s.counts.Results++
	if _, gone := s.dropped[b.ToolUseID]; gone {
		delete(s.dropped, b.ToolUseID)
		return
	}
	open := s.tools[b.ToolUseID]
	if open == nil {
		return
	}
	delete(s.tools, b.ToolUseID)
	at := parseAt(r.Timestamp)
	tool := *open.item.Tool
	text := resultText(b.Content)
	tool.Status = "ok"
	switch {
	case strings.HasPrefix(strings.TrimSpace(text), userWantsNoneText):
		tool.Status = "interrupted"
	case b.IsError:
		tool.Status = "error"
	}
	if !at.IsZero() && !open.started.IsZero() && at.After(open.started) {
		tool.DurationMs = at.Sub(open.started).Milliseconds()
	}
	tool.Preview = preview(text)
	tool.HasOutput = strings.TrimSpace(text) != ""
	if tool.Category == "edit" || tool.Category == "agent" {
		var d resultDetails
		_ = json.Unmarshal(r.ToolUseResult, &d)
		if d.Interrupted {
			tool.Status = "interrupted"
		}
		if agentIDRe.MatchString(d.AgentID) {
			tool.Subagent = d.AgentID
		}
		if patch := structuredPatchText(r.ToolUseResult); patch != "" {
			tool.Preview = preview(patch)
			tool.HasOutput = true
		}
		for _, h := range d.StructuredPatch {
			for _, l := range h.Lines {
				switch {
				case strings.HasPrefix(l, "+"):
					tool.Added++
				case strings.HasPrefix(l, "-"):
					tool.Removed++
				}
			}
		}
	}
	it := open.item
	it.Tool = &tool
	s.counts.Items++
	s.put(it)
	if tool.HasOutput {
		s.remember(it.ID, outputRef{seg: seg, offset: off, length: length, useID: b.ToolUseID})
	}
}

func (s *claudeSource) remember(id string, ref outputRef) {
	s.refs[id] = ref
	if s.keepRefs {
		return
	}
	s.refOrder = append(s.refOrder, id)
	if len(s.refOrder) > maxRefs {
		delete(s.refs, s.refOrder[0])
		s.refOrder = s.refOrder[1:]
	}
}

// oversize handles a line too long to keep. Its head still names the call it answers, and
// that call must not read "running" forever, but nothing about its result was read, so it
// says no_result rather than claiming success.
func (s *claudeSource) oversize(head []byte) {
	m := toolUseIDRe.FindSubmatch(head)
	if m == nil {
		return
	}
	id := string(m[1])
	s.counts.Results++
	if open := s.tools[id]; open != nil {
		delete(s.tools, id)
		tool := *open.item.Tool
		tool.Status = "no_result"
		it := open.item
		it.Tool = &tool
		s.put(it)
	}
	delete(s.dropped, id)
}

// resultText is a tool_result's text, which is a string or a list of blocks. An image in
// it stays behind: base64 is not text.
func resultText(raw json.RawMessage) string {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return ""
	}
	if raw[0] == '"' {
		var s string
		_ = json.Unmarshal(raw, &s)
		return s
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &blocks) != nil {
		return ""
	}
	var parts []string
	for _, b := range blocks {
		if b.Type == "text" {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, "\n")
}

// structuredPatchText shows the edit recorded by the tool, not the file as it is now.
func structuredPatchText(raw json.RawMessage) string {
	var d resultDetails
	if json.Unmarshal(raw, &d) != nil || len(d.StructuredPatch) == 0 {
		return ""
	}
	var out strings.Builder
	for _, h := range d.StructuredPatch {
		fmt.Fprintf(&out, "@@ -%d,%d +%d,%d @@\n", h.OldStart, h.OldLines, h.NewStart, h.NewLines)
		for _, line := range h.Lines {
			out.WriteString(line)
			out.WriteByte('\n')
		}
	}
	return out.String()
}

// Output implements Source.
func (s *claudeSource) Output(ctx context.Context, id string) (string, bool, error) {
	if rest, ok := strings.CutPrefix(id, "agent-"); ok && s.parent == "" {
		parent, _, found := strings.Cut(rest, "/")
		if !found {
			return "", false, errors.New("no such item")
		}
		thread, err := s.threadSource(parent)
		if err != nil {
			return "", false, err
		}
		thread.drain(ctx, func([]Item) {})
		return thread.Output(ctx, id)
	}
	ref, ok := s.refs[id]
	if !ok {
		return "", false, ErrNoOutput
	}
	line, err := readRange(ref.seg.tail.path, ref.seg.tail.info, ref.offset, ref.length)
	if err != nil {
		return "", false, err
	}
	var r claudeRecord
	if !decodeRecord(line, &r) {
		return "", false, errors.New("the log changed")
	}
	var blocks []claudeBlock
	if json.Unmarshal(r.Message.Content, &blocks) != nil {
		return "", false, errors.New("the log changed")
	}
	for _, b := range blocks {
		if b.Type == "tool_result" && b.ToolUseID == ref.useID {
			if patch := structuredPatchText(r.ToolUseResult); patch != "" {
				return cleanOutput(patch)
			}
			return cleanOutput(resultText(b.Content))
		}
	}
	return "", false, errors.New("the log changed")
}

// ErrNoOutput says a Source no longer remembers where an item's output is, or that it has
// none. A Feed answers it by rescanning once.
var ErrNoOutput = errors.New("no output for this item")

func cleanOutput(text string) (string, bool, error) {
	var c sshelement.Cleaner
	clean := string(append(c.Write([]byte(text)), c.Flush()...))
	if !utf8.ValidString(clean) {
		clean = strings.ToValidUTF8(clean, "�")
	}
	out, cut := clipUTF8(clean, maxOutputBytes)
	return out, cut, nil
}

// maxThreadBytes bounds reading one subagent file on demand.
const maxThreadBytes = 64 << 20

// subagentPath finds a subagent's file below this session's segments. It walks without
// following anything: WalkDir reports a symlink as itself, and openRegular refuses one.
func (s *claudeSource) subagentPath(parent string) (string, error) {
	if !agentIDRe.MatchString(parent) {
		return "", errors.New("no such subagent")
	}
	want := "agent-" + parent + ".jsonl"
	for _, seg := range s.segs {
		root := join(join(s.dir, strings.TrimSuffix(seg.base, ".jsonl")), "subagents")
		if !plainDir(s.dir) || !plainDir(filepath.Dir(root)) || !plainDir(root) {
			continue
		}
		found := ""
		_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.Type().IsRegular() && d.Name() == want {
				found = p
				return filepath.SkipAll
			}
			return nil
		})
		if found != "" {
			return found, nil
		}
	}
	return "", errors.New("no such subagent")
}

// threadSource prepares a reader for one subagent's file. Its items carry Parent, and
// their ids name the thread, so a request for one item's output can find its way back.
func (s *claudeSource) threadSource(parent string) (*claudeSource, error) {
	path, err := s.subagentPath(parent)
	if err != nil {
		return nil, err
	}
	t := &claudeSource{dir: filepath.Dir(path), parent: parent, keepRefs: true}
	t.reset()
	seg := &claudeSegment{base: filepath.Base(path), tail: tail{path: path}}
	t.segs, t.byBase[seg.base] = []*claudeSegment{seg}, seg
	return t, nil
}

// drain reads a thread to its end, within the bound.
func (s *claudeSource) drain(ctx context.Context, each func([]Item)) {
	for ctx.Err() == nil && s.counts.Bytes <= maxThreadBytes {
		changed, _ := s.Advance(ctx, 8<<20)
		each(changed)
		if s.eof {
			return
		}
	}
}

// Thread implements Source.
func (s *claudeSource) Thread(ctx context.Context, parent string) ([]Item, error) {
	t, err := s.threadSource(parent)
	if err != nil {
		return nil, err
	}
	feed := newFeedOf(t, 0)
	t.drain(ctx, feed.apply)
	return feed.All(), nil
}
