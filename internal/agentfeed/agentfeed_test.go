package agentfeed

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/sushidev-team/lola/internal/protocol"
)

var update = flag.Bool("update", false, "rewrite the golden files")

func TestPreviewKeepsReadableContextAndStaysBounded(t *testing.T) {
	var source strings.Builder
	for i := 0; i < 8; i++ {
		fmt.Fprintf(&source, "%d\t                    const value%d = \"readable context\";\n", 1150+i, i)
	}
	want := strings.TrimSpace(source.String())
	if got := preview("\x1b[31m" + source.String() + "\x1b[0m"); got != want {
		t.Fatalf("lost source context: %q", got)
	}
	got := preview(strings.Repeat("界", 10_000))
	if !utf8.ValidString(got) || utf8.RuneCountInString(got) > maxPreviewRunes || !strings.HasSuffix(got, "…") {
		t.Fatalf("preview must stay bounded, UTF-8 safe and visibly truncated: %q", got)
	}
}

const (
	segA = "aaaaaaaa-0000-4000-8000-000000000001.jsonl"
	segB = "bbbbbbbb-0000-4000-8000-000000000002.jsonl"
)

// slugRig is a projects root with one session's slug directory in it.
func slugRig(t *testing.T) (root, slug, dir string) {
	t.Helper()
	root = t.TempDir()
	slug = "-w-s1"
	dir = filepath.Join(root, slug)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	return root, slug, dir
}

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func writeFile(t *testing.T, path string, b []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
}

func appendFile(t *testing.T, path string, b []byte) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.Write(b); err != nil {
		t.Fatal(err)
	}
}

// drain advances a Feed to the end of its log and answers the last health.
func drain(t *testing.T, f *Feed) Health {
	t.Helper()
	var h Health
	for i := 0; i < 10000; i++ {
		_, h = f.Advance(context.Background(), 1<<20)
		if !f.Scanning() {
			return h
		}
	}
	t.Fatal("the scan never reached the end of the log")
	return h
}

func newClaudeFeed(root, slug string, segs ...string) *Feed {
	return NewFeed(func() Source { return NewClaude(root, slug, func() []string { return segs }) })
}

// shown is the stable, readable form of an item the golden files pin.
type shown struct {
	ID        string                  `json:"id"`
	Kind      string                  `json:"kind"`
	Order     int64                   `json:"order"`
	Turn      string                  `json:"turn,omitempty"`
	Parent    string                  `json:"parent,omitempty"`
	Text      string                  `json:"text,omitempty"`
	Truncated bool                    `json:"truncated,omitempty"`
	Origin    string                  `json:"origin,omitempty"`
	At        string                  `json:"at,omitempty"`
	Tool      *protocol.AgentFeedTool `json:"tool,omitempty"`
}

func golden(t *testing.T, name string, items []Item) {
	t.Helper()
	rows := make([]shown, 0, len(items))
	for _, it := range items {
		s := shown{ID: it.ID, Kind: it.Kind, Order: it.Order, Turn: it.Turn, Parent: it.Parent, Text: it.Text, Truncated: it.Truncated, Origin: it.Origin, Tool: it.Tool}
		if !it.At.IsZero() {
			s.At = it.At.UTC().Format(time.RFC3339)
		}
		rows = append(rows, s)
	}
	got, err := json.MarshalIndent(rows, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	got = append(got, '\n')
	path := filepath.Join("testdata", "golden", name+".json")
	if *update {
		writeFile(t, path, got)
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v: run with -update and read the result", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("%s differs from its golden file:\n%s", name, got)
	}
}

func TestClaudeGolden(t *testing.T) {
	cases := []struct {
		name  string
		files map[string]string // segment -> fixture, written in this order below
		segs  []string
	}{
		{"basic", map[string]string{segA: "claude/basic.jsonl"}, []string{segA}},
		{"parallel", map[string]string{segA: "claude/parallel.jsonl"}, []string{segA}},
		{"interrupt", map[string]string{segA: "claude/interrupt.jsonl"}, []string{segA}},
		{"shapes", map[string]string{segA: "claude/shapes.jsonl"}, []string{segA}},
		{"reports", map[string]string{segA: "claude/reports.jsonl"}, []string{segA}},
		{"queued", map[string]string{segA: "claude/queued.jsonl"}, []string{segA}},
		{"branch", map[string]string{segA: "claude/branch-a.jsonl", segB: "claude/branch-b.jsonl"}, []string{segA, segB}},
		{"clear", map[string]string{segA: "claude/branch-a.jsonl", segB: "claude/clear-b.jsonl"}, []string{segA, segB}},
		{"subagent-parent", map[string]string{segA: "claude/subagent-parent.jsonl"}, []string{segA}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root, slug, dir := slugRig(t)
			for seg, fx := range c.files {
				writeFile(t, filepath.Join(dir, seg), fixture(t, fx))
			}
			f := newClaudeFeed(root, slug, c.segs...)
			if h := drain(t, f); h != protocol.FeedOK {
				t.Errorf("health = %q", h)
			}
			golden(t, c.name, f.All())
		})
	}
}

// The body of a Write must not stay in memory or on the wire in any form.
func TestWriteContentIsNeverKept(t *testing.T) {
	root, slug, dir := slugRig(t)
	writeFile(t, filepath.Join(dir, segA), fixture(t, "claude/basic.jsonl"))
	f := newClaudeFeed(root, slug, segA)
	drain(t, f)
	b, _ := json.Marshal(f.All())
	if bytes.Contains(b, []byte("SECRET-BODY")) {
		t.Error("a Write's content reached an item")
	}
	if bytes.Contains(b, []byte("BASE64")) {
		t.Error("an image's base64 reached an item")
	}
}

func TestPartialLineWaitsForItsNewline(t *testing.T) {
	root, slug, dir := slugRig(t)
	whole := `{"type":"user","message":{"content":"hello"},"uuid":"u-1","timestamp":"2026-11-06T10:00:00Z"}` + "\n"
	path := filepath.Join(dir, segA)
	writeFile(t, path, []byte(whole[:30]))
	f := newClaudeFeed(root, slug, segA)
	drain(t, f)
	if len(f.All()) != 0 {
		t.Fatalf("a partial line produced %d items", len(f.All()))
	}
	appendFile(t, path, []byte(whole[30:]))
	drain(t, f)
	if all := f.All(); len(all) != 1 || all[0].Text != "hello" {
		t.Fatalf("after the line finished: %+v", all)
	}
}

func TestOversizedLineIsSkippedAndItsCallSaysNoResult(t *testing.T) {
	root, slug, dir := slugRig(t)
	var b bytes.Buffer
	b.WriteString(`{"type":"user","message":{"content":"go"},"uuid":"u-1","timestamp":"2026-11-06T10:00:00Z"}` + "\n")
	b.WriteString(`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"t-big","name":"Read","input":{"file_path":"huge.log"}}]},"uuid":"a-1","timestamp":"2026-11-06T10:00:01Z"}` + "\n")
	b.WriteString(`{"type":"user","message":{"content":[{"tool_use_id":"t-big","type":"tool_result","content":"`)
	b.Write(bytes.Repeat([]byte("x"), maxLineBytes+chunkBytes))
	b.WriteString(`"}]},"uuid":"u-2"}` + "\n")
	b.WriteString(`{"type":"assistant","message":{"content":[{"type":"text","text":"after"}]},"uuid":"a-2","timestamp":"2026-11-06T10:00:02Z"}` + "\n")
	writeFile(t, filepath.Join(dir, segA), b.Bytes())

	f := newClaudeFeed(root, slug, segA)
	drain(t, f)
	all := f.All()
	if len(all) != 3 {
		t.Fatalf("items = %d: %+v", len(all), all)
	}
	if all[1].Tool == nil || all[1].Tool.Status != "no_result" {
		t.Errorf("the call whose result was too long to read: %+v", all[1].Tool)
	}
	if all[2].Text != "after" {
		t.Errorf("the line after the oversized one was lost: %+v", all[2])
	}
}

// A line longer than the per-pass budget is not an oversized line: talkState's 2 MB budget
// must not cost a 3 MB result its row.
func TestLineLongerThanBudgetIsStillRead(t *testing.T) {
	root, slug, dir := slugRig(t)
	var b bytes.Buffer
	b.WriteString(`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"t-1","name":"Read","input":{"file_path":"a"}}]},"uuid":"a-1","timestamp":"2026-11-06T10:00:01Z"}` + "\n")
	b.WriteString(`{"type":"user","message":{"content":[{"tool_use_id":"t-1","type":"tool_result","content":"`)
	b.Write(bytes.Repeat([]byte("y"), 3<<20))
	b.WriteString(`"}]},"uuid":"u-2","timestamp":"2026-11-06T10:00:02Z"}` + "\n")
	writeFile(t, filepath.Join(dir, segA), b.Bytes())

	f := newClaudeFeed(root, slug, segA)
	for i := 0; f.Scanning() && i < 100; i++ {
		f.Advance(context.Background(), 1<<10)
	}
	all := f.All()
	if len(all) != 1 || all[0].Tool.Status != "ok" || len(all[0].Tool.Preview) > maxPreviewRunes*4 {
		t.Fatalf("items = %+v", all)
	}
}

func TestTextIsCappedAndMarked(t *testing.T) {
	root, slug, dir := slugRig(t)
	long := strings.Repeat("word ", maxTextRunes)
	line, _ := json.Marshal(map[string]any{"type": "assistant", "uuid": "a-1", "timestamp": "2026-11-06T10:00:00Z",
		"message": map[string]any{"content": []map[string]any{{"type": "text", "text": long}}}})
	writeFile(t, filepath.Join(dir, segA), append(line, '\n'))
	f := newClaudeFeed(root, slug, segA)
	drain(t, f)
	it := f.All()[0]
	if !it.Truncated || len([]rune(it.Text)) > maxTextRunes {
		t.Errorf("truncated=%v runes=%d", it.Truncated, len([]rune(it.Text)))
	}
}

// A newer segment appearing later is read after the ones already known.
func TestSegmentsAreAskedOnEveryPass(t *testing.T) {
	root, slug, dir := slugRig(t)
	writeFile(t, filepath.Join(dir, segA), fixture(t, "claude/branch-a.jsonl"))
	segs := []string{segA}
	f := NewFeed(func() Source { return NewClaude(root, slug, func() []string { return segs }) })
	drain(t, f)
	writeFile(t, filepath.Join(dir, segB), fixture(t, "claude/clear-b.jsonl"))
	segs = []string{segA, segB}
	drain(t, f)
	all := f.All()
	if len(all) != 4 || all[2].Kind != protocol.FeedItemDivider || all[2].Text != "Context cleared" {
		t.Fatalf("items = %+v", all)
	}
}

func TestSubagentOnDemand(t *testing.T) {
	root, slug, dir := slugRig(t)
	writeFile(t, filepath.Join(dir, segA), fixture(t, "claude/subagent-parent.jsonl"))
	child := filepath.Join(dir, strings.TrimSuffix(segA, ".jsonl"), "subagents", "agent-abc123.jsonl")
	writeFile(t, child, fixture(t, "claude/subagent-child.jsonl"))

	f := newClaudeFeed(root, slug, segA)
	drain(t, f)
	for _, it := range f.All() {
		if it.Parent != "" {
			t.Errorf("a subagent's item appeared without being asked for: %+v", it)
		}
	}
	items, _, err := f.Thread(context.Background(), "abc123", 0)
	if err != nil {
		t.Fatal(err)
	}
	golden(t, "subagent-thread", items)

	text, _, err := f.Output(context.Background(), "agent-abc123/c-2:0")
	if err != nil || text != "login.go:3" {
		t.Errorf("a subagent's tool output = %q, %v", text, err)
	}
	if _, _, err := f.Thread(context.Background(), "../../etc", 0); err == nil {
		t.Error("a traversal in the subagent id was accepted")
	}
}

func TestSubagentSymlinkRefused(t *testing.T) {
	root, slug, dir := slugRig(t)
	writeFile(t, filepath.Join(dir, segA), fixture(t, "claude/subagent-parent.jsonl"))
	secret := filepath.Join(t.TempDir(), "secret.jsonl")
	writeFile(t, secret, fixture(t, "claude/subagent-child.jsonl"))
	subs := filepath.Join(dir, strings.TrimSuffix(segA, ".jsonl"), "subagents")
	if err := os.MkdirAll(subs, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(subs, "agent-abc123.jsonl")); err != nil {
		t.Fatal(err)
	}
	f := newClaudeFeed(root, slug, segA)
	drain(t, f)
	if _, _, err := f.Thread(context.Background(), "abc123", 0); err == nil {
		t.Error("a symlinked subagent file was read")
	}
}

func TestForgedPathRefused(t *testing.T) {
	root, slug, dir := slugRig(t)
	outside := filepath.Join(root, "other-slug")
	writeFile(t, filepath.Join(outside, segA), fixture(t, "claude/branch-a.jsonl"))
	writeFile(t, filepath.Join(dir, "notes.jsonl"), fixture(t, "claude/branch-a.jsonl"))
	writeFile(t, filepath.Join(root, "passwd"), fixture(t, "claude/branch-a.jsonl"))

	for _, forged := range []string{
		"../other-slug/" + segA,
		"/etc/passwd",
		filepath.Join(outside, segA),
		"notes.jsonl",
		"../passwd",
		segA + "/../" + segA,
		"AAAAAAAA-0000-4000-8000-000000000001.jsonl",
	} {
		f := newClaudeFeed(root, slug, forged)
		if h := drain(t, f); h != protocol.FeedEmpty || len(f.All()) != 0 {
			t.Errorf("segment %q: health %q, %d items", forged, h, len(f.All()))
		}
	}
	// The slug itself is not a way out either.
	for _, forgedSlug := range []string{"../" + filepath.Base(root), "..", "", "a/b"} {
		f := newClaudeFeed(root, forgedSlug, segA)
		if h := drain(t, f); h != protocol.FeedEmpty || len(f.All()) != 0 {
			t.Errorf("slug %q: health %q, %d items", forgedSlug, h, len(f.All()))
		}
	}
	if ValidSegment("../x.jsonl") || !ValidSegment(segA) {
		t.Error("ValidSegment disagrees with the rule")
	}
}

func TestSymlinkRefused(t *testing.T) {
	root, slug, dir := slugRig(t)
	secret := filepath.Join(t.TempDir(), "secret")
	writeFile(t, secret, fixture(t, "claude/branch-a.jsonl"))
	if err := os.Symlink(secret, filepath.Join(dir, segA)); err != nil {
		t.Fatal(err)
	}
	f := newClaudeFeed(root, slug, segA)
	if h := drain(t, f); h != protocol.FeedEmpty || len(f.All()) != 0 {
		t.Errorf("a symlinked transcript: health %q, %d items", h, len(f.All()))
	}

	// A slug directory that is a link is refused as well.
	target := t.TempDir()
	writeFile(t, filepath.Join(target, segB), fixture(t, "claude/branch-a.jsonl"))
	if err := os.Symlink(target, filepath.Join(root, "-linked")); err != nil {
		t.Fatal(err)
	}
	f = newClaudeFeed(root, "-linked", segB)
	if h := drain(t, f); h != protocol.FeedEmpty || len(f.All()) != 0 {
		t.Errorf("a symlinked slug directory: health %q, %d items", h, len(f.All()))
	}
}

// A FIFO must not hang the reader, which would hang the session's feed for good.
func TestFifoRefused(t *testing.T) {
	root, slug, dir := slugRig(t)
	if err := mkfifo(filepath.Join(dir, segA)); err != nil {
		t.Skip(err)
	}
	f := newClaudeFeed(root, slug, segA)
	done := make(chan Health, 1)
	go func() { done <- drain(t, f) }()
	select {
	case h := <-done:
		if h != protocol.FeedEmpty {
			t.Errorf("health = %q", h)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("opening a FIFO blocked the reader")
	}
}

func TestReplacedFileRestarts(t *testing.T) {
	root, slug, dir := slugRig(t)
	path := filepath.Join(dir, segA)
	writeFile(t, path, fixture(t, "claude/branch-a.jsonl"))
	f := newClaudeFeed(root, slug, segA)
	drain(t, f)
	epoch := f.Epoch()
	if len(f.All()) != 2 {
		t.Fatalf("items = %d", len(f.All()))
	}

	// A different file at the same path: rename gives it a new identity.
	repl := path + ".new"
	writeFile(t, repl, fixture(t, "claude/clear-b.jsonl"))
	if err := os.Rename(repl, path); err != nil {
		t.Fatal(err)
	}
	drain(t, f)
	all := f.All()
	if f.Epoch() == epoch {
		t.Error("a replaced file kept its epoch")
	}
	if len(all) != 1 || all[0].Text != "After clearing" || all[0].Order != 0 {
		t.Fatalf("items after the replacement = %+v", all)
	}

	// The same file shrinking is the same thing.
	epoch = f.Epoch()
	if err := os.Truncate(path, 0); err != nil {
		t.Fatal(err)
	}
	drain(t, f)
	if f.Epoch() == epoch || len(f.All()) != 0 {
		t.Errorf("a shrunk file: epoch moved %v, %d items", f.Epoch() != epoch, len(f.All()))
	}
}

// A sandboxed agent's reported path names a directory inside its container. Only the
// segment's base name is ever used, so the read still lands in the host's slug directory.
func TestSandboxUsesSlugNotContainerPath(t *testing.T) {
	root, slug, dir := slugRig(t)
	writeFile(t, filepath.Join(dir, segA), fixture(t, "claude/branch-a.jsonl"))
	containerPath := "/home/agent/.claude/projects/-workspace/" + segA
	f := newClaudeFeed(root, slug, filepath.Base(containerPath))
	if h := drain(t, f); h != protocol.FeedOK || len(f.All()) != 2 {
		t.Errorf("health %q, %d items", h, len(f.All()))
	}
	// The container path itself is refused outright.
	f = newClaudeFeed(root, slug, containerPath)
	if h := drain(t, f); h != protocol.FeedEmpty {
		t.Errorf("the container path was accepted: health %q", h)
	}
}

func TestHealthFromTheFileAlone(t *testing.T) {
	root, slug, dir := slugRig(t)
	path := filepath.Join(dir, segA)

	f := newClaudeFeed(root, slug, segA)
	if h := drain(t, f); h != protocol.FeedEmpty {
		t.Errorf("no file: %q", h)
	}
	writeFile(t, path, []byte(`{"type":"mode","mode":"default"}`+"\n"))
	if h := drain(t, f); h != protocol.FeedEmpty {
		t.Errorf("a file with bookkeeping only: %q", h)
	}
	line := []byte(`{"type":"future-shape","pad":"` + strings.Repeat("z", 64<<10) + `"}` + "\n")
	for range 5 {
		appendFile(t, path, line)
	}
	if h := drain(t, f); h != protocol.FeedUnreadable {
		t.Errorf("a file that grows and yields nothing: %q", h)
	}
	appendFile(t, path, []byte(`{"type":"user","message":{"content":"hi"},"uuid":"u","timestamp":"2026-11-06T10:00:00Z"}`+"\n"))
	if h := drain(t, f); h != protocol.FeedOK {
		t.Errorf("after an item: %q", h)
	}
}

func TestNormalize(t *testing.T) {
	cases := map[string]string{
		"plain":            "plain",
		"  !echo hi":       "!echo hi",
		"a\tb":             "a    b",
		"not submitted \\": "not submitted",
		"one\\\\":          `one\`,
		"\n\n<pasted_content id=\"x1\">\nbody\nmore\n</pasted_content id=\"x1\">\n": "body\nmore",
		"日本<pasted_content id=\"a\">語\n</pasted_content id=\"a\">":                  "日本語",
		"first\r\nsecond": "first\nsecond",
	}
	for in, want := range cases {
		if got := Normalize(in); got != want {
			t.Errorf("Normalize(%q) = %q, want %q", in, got, want)
		}
	}
	if Hash("  same\t") != Hash("same    ") {
		t.Error("Hash does not go through Normalize")
	}
}

func TestReportCalls(t *testing.T) {
	cases := map[string]bool{
		`lola report phase x`:                 true,
		`  lola ssh run --host a "ps | grep"`: true,
		`lola post widget`:                    true,
		`lola report`:                         true,
		`lola report x && go test ./...`:      false,
		`go test ./... && lola report check`:  false,
		`lola ssh x; rm -rf /tmp/y`:           false,
		`lola kill s1`:                        false,
		`lolabot report`:                      false,
	}
	for c, want := range cases {
		if got := isReportCall(c); got != want {
			t.Errorf("isReportCall(%q) = %v", c, got)
		}
	}
}

func TestOutputIsReadBackSanitizedAndBounded(t *testing.T) {
	root, slug, dir := slugRig(t)
	path := filepath.Join(dir, segA)
	writeFile(t, path, fixture(t, "claude/basic.jsonl"))
	f := newClaudeFeed(root, slug, segA)
	drain(t, f)

	text, cut, err := f.Output(context.Background(), "a-5:0")
	if err != nil || cut {
		t.Fatalf("output: %v cut=%v", err, cut)
	}
	if strings.Contains(text, "\x1b") || !strings.Contains(text, "FAIL: TestLogin") {
		t.Errorf("output = %q", text)
	}
	for _, it := range f.All() {
		if it.ID == "a-5:0" && strings.Contains(it.Tool.Preview, "\x1b") {
			t.Errorf("preview kept an escape: %q", it.Tool.Preview)
		}
	}
	if _, _, err := f.Output(context.Background(), "no-such:0"); err == nil {
		t.Error("an unknown item had output")
	}

	// A replaced file refuses rather than answering from whatever now sits at that offset.
	repl := path + ".new"
	writeFile(t, repl, bytes.Repeat([]byte("garbage line\n"), 4096))
	if err := os.Rename(repl, path); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.Output(context.Background(), "a-5:0"); err == nil {
		t.Error("output was read from a replaced file")
	}
}

func TestLongOutputIsTruncatedAtARuneBoundary(t *testing.T) {
	text, cut, err := cleanOutput(strings.Repeat("語", maxOutputBytes))
	if err != nil || !cut || len(text) > maxOutputBytes || !strings.HasSuffix(text, "語") {
		t.Errorf("cut=%v len=%d err=%v", cut, len(text), err)
	}
}

func manyUsers(n int) []byte {
	var b bytes.Buffer
	for i := range n {
		fmt.Fprintf(&b, `{"type":"user","message":{"content":"message %d"},"uuid":"u-%d","timestamp":"2026-11-06T10:00:00Z"}`+"\n", i, i)
	}
	return b.Bytes()
}

func TestPageTailIsDefault(t *testing.T) {
	root, slug, dir := slugRig(t)
	writeFile(t, filepath.Join(dir, segA), manyUsers(500))
	f := newClaudeFeed(root, slug, segA)
	drain(t, f)
	p := f.Page(context.Background(), protocol.AgentFeedArgs{})
	if len(p.Items) != DefaultLimit || !p.More || p.Items[len(p.Items)-1].Text != "message 499" {
		t.Fatalf("tail: %d items, more=%v, last=%+v", len(p.Items), p.More, p.Items[len(p.Items)-1])
	}
	if p.LastSeq != f.LastSeq() {
		t.Errorf("LastSeq = %d", p.LastSeq)
	}
	// Older history pages by Order and stays in Order.
	older := f.Page(context.Background(), protocol.AgentFeedArgs{Before: p.Items[0].Order, Limit: 50})
	if len(older.Items) != 50 || !older.More || older.Items[49].Order != p.Items[0].Order-1 {
		t.Fatalf("older: %d items more=%v", len(older.Items), older.More)
	}
	if older.LastSeq != 0 {
		t.Errorf("a page of history must not move a client's sequence: %d", older.LastSeq)
	}
	first := f.Page(context.Background(), protocol.AgentFeedArgs{Before: 5})
	if len(first.Items) != 5 || first.More {
		t.Errorf("the first page: %d items more=%v", len(first.Items), first.More)
	}
}

func TestPageSinceReturnsChangesOnly(t *testing.T) {
	root, slug, dir := slugRig(t)
	path := filepath.Join(dir, segA)
	writeFile(t, path, []byte(`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"t-1","name":"Read","input":{"file_path":"a"}}]},"uuid":"a-1","timestamp":"2026-11-06T10:00:01Z"}`+"\n"))
	f := newClaudeFeed(root, slug, segA)
	drain(t, f)
	p := f.Page(context.Background(), protocol.AgentFeedArgs{})
	if len(p.Items) != 1 || p.Items[0].Tool.Status != "running" {
		t.Fatalf("tail: %+v", p.Items)
	}
	appendFile(t, path, []byte(`{"type":"user","message":{"content":[{"tool_use_id":"t-1","type":"tool_result","content":"x"}]},"uuid":"u-1","timestamp":"2026-11-06T10:00:02Z"}`+"\n"+
		`{"type":"assistant","message":{"content":[{"type":"text","text":"done"}]},"uuid":"a-2","timestamp":"2026-11-06T10:00:03Z"}`+"\n"))
	drain(t, f)
	next := f.Page(context.Background(), protocol.AgentFeedArgs{After: p.LastSeq})
	if len(next.Items) != 2 || next.Items[0].ID != "a-1:0" || next.Items[0].Tool.Status != "ok" || next.Items[0].Order != p.Items[0].Order {
		t.Fatalf("changes: %+v", next.Items)
	}
	if again := f.Page(context.Background(), protocol.AgentFeedArgs{After: next.LastSeq}); len(again.Items) != 0 {
		t.Errorf("nothing changed yet %d items came back", len(again.Items))
	}
	// Part of a backlog: the sequence answered is where the next fetch resumes.
	part := f.Page(context.Background(), protocol.AgentFeedArgs{After: p.LastSeq, Limit: 1})
	if len(part.Items) != 1 || part.LastSeq != part.Items[0].Seq {
		t.Errorf("a partial answer: %d items, last %d", len(part.Items), part.LastSeq)
	}
	// A stale epoch is answered with the tail and the new epoch.
	stale := f.Page(context.Background(), protocol.AgentFeedArgs{Epoch: "old", After: next.LastSeq})
	if stale.Epoch != f.Epoch() || len(stale.Items) == 0 {
		t.Errorf("a stale epoch: %+v", stale)
	}
}

func TestReplyStaysUnderTheByteBudget(t *testing.T) {
	root, slug, dir := slugRig(t)
	var b bytes.Buffer
	body := strings.Repeat("x", maxTextRunes)
	for i := range 200 {
		fmt.Fprintf(&b, `{"type":"assistant","message":{"content":[{"type":"text","text":"%s"}]},"uuid":"a-%d","timestamp":"2026-11-06T10:00:00Z"}`+"\n", body, i)
	}
	writeFile(t, filepath.Join(dir, segA), b.Bytes())
	f := newClaudeFeed(root, slug, segA)
	drain(t, f)
	p := f.Page(context.Background(), protocol.AgentFeedArgs{})
	raw, _ := json.Marshal(p)
	if len(raw) > maxReplyBytes+maxTextRunes || !p.More || len(p.Items) == 0 {
		t.Errorf("reply %d bytes, %d items, more=%v", len(raw), len(p.Items), p.More)
	}
	if p.Items[len(p.Items)-1].ID != "a-199:0" {
		t.Errorf("the tail must survive the cut: %s", p.Items[len(p.Items)-1].ID)
	}
}

func TestEvictionKeepsTheTailAndARescanReachesTheRest(t *testing.T) {
	root, slug, dir := slugRig(t)
	writeFile(t, filepath.Join(dir, segA), manyUsers(MaxItems+100))
	f := newClaudeFeed(root, slug, segA)
	drain(t, f)
	all := f.All()
	if len(all) != MaxItems || all[0].Text != "message 100" {
		t.Fatalf("held %d, first %q", len(all), all[0].Text)
	}
	tail := f.Page(context.Background(), protocol.AgentFeedArgs{Limit: 10})
	if !tail.More {
		t.Error("evicted history did not read as More")
	}
	p := f.Page(context.Background(), protocol.AgentFeedArgs{Before: all[0].Order, Limit: 40})
	if len(p.Items) != 40 || p.Items[39].Text != "message 99" || p.Items[39].Order != 99 || !p.More {
		t.Fatalf("rescan page: %d items, last %+v", len(p.Items), p.Items[len(p.Items)-1])
	}
	if f.TextHash(p.Items[0].ID) != Hash(p.Items[0].Text) {
		t.Error("a rescanned page cannot be matched against sends")
	}
}

// A daemon restart rereads the log: a new epoch tells clients to reset, and the stable ids
// are what lets them keep their scroll position and what they had expanded.
func TestARescanKeepsIDsAndOrdersUnderANewEpoch(t *testing.T) {
	root, slug, dir := slugRig(t)
	writeFile(t, filepath.Join(dir, segA), fixture(t, "claude/basic.jsonl"))
	first := newClaudeFeed(root, slug, segA)
	drain(t, first)
	second := newClaudeFeed(root, slug, segA)
	drain(t, second)
	if first.Epoch() == second.Epoch() {
		t.Error("two reads share an epoch")
	}
	a, b := first.All(), second.All()
	if len(a) != len(b) || len(a) == 0 {
		t.Fatalf("%d then %d items", len(a), len(b))
	}
	for i := range a {
		if a[i].ID != b[i].ID || a[i].Order != b[i].Order {
			t.Errorf("item %d: %s/%d then %s/%d", i, a[i].ID, a[i].Order, b[i].ID, b[i].Order)
		}
	}
}

func TestParseRate(t *testing.T) {
	_, _, dir := slugRig(t)
	path := filepath.Join(dir, segA)
	writeFile(t, path, append(fixture(t, "claude/basic.jsonl"),
		[]byte(`{"type":"assistant","message":{"content":[{"type":"brand_new_block","x":1}]},"uuid":"z"}`+"\n"+
			`{"type":"user","message":{"content":{"changed":"shape"}},"uuid":"y"}`+"\n")...))
	understood, total, err := ParseRate(path)
	if err != nil {
		t.Fatal(err)
	}
	if total == 0 || understood != total-2 {
		t.Errorf("understood %d of %d", understood, total)
	}
	if _, _, err := ParseRate(filepath.Join(dir, "missing")); err == nil {
		t.Error("a missing file had a rate")
	}
}

func TestCodexGolden(t *testing.T) {
	root := t.TempDir()
	day := filepath.Join(root, "2026", "11", "06")
	writeFile(t, filepath.Join(day, "rollout-a-interactive.jsonl"), fixture(t, "codex/interactive.jsonl"))
	writeFile(t, filepath.Join(day, "rollout-b-exec.jsonl"), fixture(t, "codex/exec.jsonl"))
	writeFile(t, filepath.Join(day, "rollout-c-guardian.jsonl"), fixture(t, "codex/guardian.jsonl"))
	writeFile(t, filepath.Join(day, "rollout-d-other.jsonl"), fixture(t, "codex/other-cwd.jsonl"))
	if err := os.Symlink(filepath.Join(day, "rollout-a-interactive.jsonl"), filepath.Join(day, "rollout-e-link.jsonl")); err != nil {
		t.Fatal(err)
	}

	f := NewFeed(func() Source { return NewCodex(root, "/w/s1") })
	if h := drain(t, f); h != protocol.FeedOK {
		t.Errorf("health %q", h)
	}
	all := f.All()
	golden(t, "codex", all)
	b, _ := json.Marshal(all)
	for _, leak := range []string{"review pass", "guardian thread", "another worktree", "injected context"} {
		if bytes.Contains(b, []byte(leak)) {
			t.Errorf("%q reached the conversation", leak)
		}
	}
	text, _, err := f.Output(context.Background(), "i-4")
	if err != nil || !strings.Contains(text, "FAIL") {
		t.Errorf("output: %q %v", text, err)
	}
}

func TestOpencodeHasNoReader(t *testing.T) {
	f := NewFeed(func() Source { return NewOpencode("/w/s1") })
	if h := drain(t, f); h != protocol.FeedUnsupported {
		t.Errorf("health %q", h)
	}
}
