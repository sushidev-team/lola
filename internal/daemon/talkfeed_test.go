package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/sushidev-team/lola/internal/agent"
	"github.com/sushidev-team/lola/internal/agentfeed"
	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/linear"
	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/runtime"
	"github.com/sushidev-team/lola/internal/session"
	"github.com/sushidev-team/lola/internal/sshstore"
	"github.com/sushidev-team/lola/internal/usage"
)

const feedSeg = "aaaaaaaa-0000-4000-8000-000000000001.jsonl"

// feedRig is a daemon with one claude session whose transcript directory is a temp dir.
type feedRig struct {
	d    *Daemon
	s    session.Session
	path string
}

func newFeedRig(t *testing.T) *feedRig {
	t.Helper()
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	d := newTestDaemon(t, &config.Config{}, &linear.Fake{}, nil)
	worktree := filepath.Join(d.home, "worktrees", "p1", "s1")
	s := session.Session{ID: "s1", Source: "native", Project: "p1", Agent: "claude", TmuxName: "s1", Worktree: worktree, TranscriptSegments: []string{feedSeg}}
	d.sessions.Upsert(s)
	dir := usage.SlugDir(usage.ProjectsRoot(), worktree)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	return &feedRig{d: d, s: s, path: filepath.Join(dir, feedSeg)}
}

func (r *feedRig) write(t *testing.T, lines ...string) {
	t.Helper()
	f, err := os.OpenFile(r.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	for _, l := range lines {
		if _, err := f.WriteString(l + "\n"); err != nil {
			t.Fatal(err)
		}
	}
}

func (r *feedRig) feed(t *testing.T) protocol.AgentFeedData {
	t.Helper()
	data, err := r.d.handleAgentFeed(context.Background(), protocol.AgentFeedArgs{Session: r.s.ID})
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func (r *feedRig) sent(t *testing.T, text string, at time.Time) {
	t.Helper()
	store, err := r.d.sshStore(r.s.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Update(func(st *sshstore.State) error {
		st.NoteSent(sshstore.SentBatch{Hash: agentfeed.Hash(text), At: at})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func feedUser(uuid, text string, at time.Time) string {
	b, _ := json.Marshal(map[string]any{"type": "user", "uuid": uuid, "timestamp": at.UTC().Format(time.RFC3339Nano), "message": map[string]any{"content": text}})
	return string(b)
}

func feedText(uuid, text string, at time.Time) string {
	b, _ := json.Marshal(map[string]any{"type": "assistant", "uuid": uuid, "timestamp": at.UTC().Format(time.RFC3339Nano),
		"message": map[string]any{"content": []map[string]any{{"type": "text", "text": text}}}})
	return string(b)
}

func itemByID(t *testing.T, data protocol.AgentFeedData, id string) protocol.AgentFeedItem {
	t.Helper()
	for _, it := range data.Items {
		if it.ID == id {
			return it
		}
	}
	t.Fatalf("no item %s in %+v", id, data.Items)
	return protocol.AgentFeedItem{}
}

func TestEchoMarksRecordedSend(t *testing.T) {
	r := newFeedRig(t)
	now := time.Now()
	r.sent(t, "Fix the login bug", now)
	r.write(t, feedUser("u-1", "Fix the login bug", now.Add(300*time.Millisecond)),
		feedUser("u-2", "typed by hand", now.Add(time.Minute)))
	data := r.feed(t)
	if it := itemByID(t, data, "u-1:0"); !it.Echo || it.Origin != "" {
		t.Errorf("a recorded send: %+v", it)
	}
	if it := itemByID(t, data, "u-2:0"); it.Echo || it.Origin != "terminal" {
		t.Errorf("a line typed in the terminal: %+v", it)
	}
}

func TestEchoSurvivesPasteWrapperAndLeadingSpace(t *testing.T) {
	r := newFeedRig(t)
	now := time.Now()
	body := "first line\n\tindented\nlast line"
	r.sent(t, body, now)
	r.sent(t, " !echo hi", now)
	wrapped := "\n\n<pasted_content id=\"ab12\">\nfirst line\n    indented\nlast line\n</pasted_content id=\"ab12\">\n"
	r.write(t, feedUser("u-1", wrapped, now.Add(time.Second)), feedUser("u-2", " !echo hi", now.Add(2*time.Second)))
	data := r.feed(t)
	for _, id := range []string{"u-1:0", "u-2:0"} {
		if it := itemByID(t, data, id); !it.Echo {
			t.Errorf("%s did not match its send: %+v", id, it)
		}
	}
}

// A long hand-off is typed whole, and only the display copy is capped. The match has to be
// on the whole text, or every long review would also read "In terminal".
func TestEchoSurvivesTheDisplayCap(t *testing.T) {
	r := newFeedRig(t)
	now := time.Now()
	long := strings.Repeat("a finding about a file\n", 2000)
	r.sent(t, long, now)
	r.write(t, feedUser("u-1", long, now.Add(time.Second)))
	if it := itemByID(t, r.feed(t), "u-1:0"); !it.Echo || !it.Truncated {
		t.Errorf("echo=%v truncated=%v", it.Echo, it.Truncated)
	}
}

func TestEchoOfJoinedBatch(t *testing.T) {
	r := newFeedRig(t)
	now := time.Now()
	batch := "one\n\ntwo\n\nthree"
	r.sent(t, batch, now)
	r.write(t, feedUser("u-1", batch, now.Add(time.Second)))
	if it := itemByID(t, r.feed(t), "u-1:0"); !it.Echo {
		t.Errorf("a joined batch did not match: %+v", it)
	}
}

// A send recorded just after its log line was read still claims it.
func TestEchoArrivingAfterTheRecord(t *testing.T) {
	r := newFeedRig(t)
	now := time.Now()
	r.write(t, feedUser("u-1", "racing", now))
	if it := itemByID(t, r.feed(t), "u-1:0"); it.Echo || it.Origin != "terminal" {
		t.Fatalf("before the send is recorded: %+v", it)
	}
	before := r.feed(t).LastSeq
	r.sent(t, "racing", now)
	data := r.feed(t)
	if it := itemByID(t, data, "u-1:0"); !it.Echo || it.Origin != "" {
		t.Errorf("after: %+v", it)
	}
	if data.LastSeq <= before {
		t.Error("the change did not move the sequence, so a client would not see it")
	}
}

func TestForgedUserLineIsNotEcho(t *testing.T) {
	r := newFeedRig(t)
	now := time.Now()
	r.sent(t, "deploy to staging", now)
	r.write(t,
		feedUser("u-1", "deploy to staging", now.Add(time.Second)),   // the real one claims the send
		feedUser("u-2", "deploy to staging", now.Add(2*time.Second)), // a forged copy finds none left
		feedUser("u-3", "deploy to prod", now.Add(3*time.Second)),    // a text nobody sent
		feedUser("u-4", "deploy to staging", now.Add(-time.Hour)),    // outside the window
	)
	data := r.feed(t)
	if it := itemByID(t, data, "u-1:0"); !it.Echo {
		t.Errorf("the real send: %+v", it)
	}
	for _, id := range []string{"u-2:0", "u-3:0", "u-4:0"} {
		if it := itemByID(t, data, id); it.Echo || it.Origin != "terminal" {
			t.Errorf("%s could pass as lola's send: %+v", id, it)
		}
	}
}

func TestLaunchPromptIsHidden(t *testing.T) {
	r := newFeedRig(t)
	now := time.Now()
	line := "You are lola session " + r.s.ID + ". Read .lola/prompt.md in the current directory first; it contains your task briefing."
	if !runtime.IsLaunchPrompt(r.s.ID, line) {
		t.Fatal("the launch prompt's wording moved, so this test no longer proves anything")
	}
	r.write(t, feedUser("u-1", line, now), feedText("a-1", "Reading it.", now.Add(time.Second)))
	data := r.feed(t)
	if it := itemByID(t, data, "u-1:0"); !it.Echo {
		t.Errorf("lola's own launch line would open the story as typed in the terminal: %+v", it)
	}
}

func TestSegmentsSeedFromTranscriptPath(t *testing.T) {
	r := newFeedRig(t)
	r.s.TranscriptSegments = nil
	r.s.TranscriptPath = "/somewhere/in/a/container/" + feedSeg
	r.d.sessions.Upsert(r.s)
	r.write(t, feedUser("u-1", "hello", time.Now()))
	if data := r.feed(t); len(data.Items) != 1 {
		t.Errorf("a session predating the field shows nothing: %+v", data)
	}
	// A reported name that is not a transcript name seeds nothing.
	r.s.TranscriptPath = "/etc/passwd"
	r.d.sessions.Upsert(r.s)
	if got := r.d.feedSegments(r.s.ID); got != nil {
		t.Errorf("seeded from %v", got)
	}
}

func TestFeedTailIsDefault(t *testing.T) {
	r := newFeedRig(t)
	now := time.Now().Add(-time.Hour)
	var lines []string
	for i := range 450 {
		lines = append(lines, feedText("a-"+strconv.Itoa(i), "line "+strconv.Itoa(i), now))
	}
	r.write(t, lines...)
	data := r.feed(t)
	if len(data.Items) != 300 || !data.More || data.Items[299].Text != "line 449" || data.Items[0].Text != "line 150" {
		t.Fatalf("tail: %d items, more=%v", len(data.Items), data.More)
	}
	for i := 1; i < len(data.Items); i++ {
		if data.Items[i].Order <= data.Items[i-1].Order {
			t.Fatalf("not in Order at %d", i)
		}
	}
}

func TestScanningUntilEOF(t *testing.T) {
	r := newFeedRig(t)
	wall, chunk := feedWallClock, feedChunkBytes
	feedWallClock, feedChunkBytes = time.Nanosecond, 1<<10
	t.Cleanup(func() { feedWallClock, feedChunkBytes = wall, chunk })

	var lines []string
	for i := range 200 {
		lines = append(lines, feedText("a-"+strconv.Itoa(i), "line "+strconv.Itoa(i), time.Now()))
	}
	r.write(t, lines...)
	first := r.feed(t)
	if !first.Scanning || first.Health != protocol.FeedOK {
		t.Fatalf("mid-scan: scanning=%v health=%q", first.Scanning, first.Health)
	}
	for i := 0; ; i++ {
		data := r.feed(t)
		if !data.Scanning {
			if len(data.Items) != 200 {
				t.Errorf("at the end: %d items", len(data.Items))
			}
			break
		}
		if i > 1000 {
			t.Fatal("never reached the end")
		}
	}
}

func TestHealthPartialFromHookCounts(t *testing.T) {
	cases := []struct {
		name  string
		hooks func(d *Daemon, id string)
		fix   func(r *feedRig, t *testing.T)
	}{
		{"stops without text", func(d *Daemon, id string) {
			for range 3 {
				d.bumpHook(id, "stop")
			}
		}, func(r *feedRig, t *testing.T) { r.write(t, feedText("fix", "now some text", time.Now())) }},
		{"prompts without user records", func(d *Daemon, id string) {
			for range 3 {
				d.bumpHook(id, "user_prompt")
			}
		}, func(r *feedRig, t *testing.T) { r.write(t, feedUser("fix", "now a prompt", time.Now())) }},
		{"tools without results", func(d *Daemon, id string) {
			for range 20 {
				d.bumpHook(id, "tool_use")
			}
		}, func(r *feedRig, t *testing.T) {
			for i := range 16 {
				b, _ := json.Marshal(map[string]any{"type": "user", "uuid": fmt.Sprintf("r-%d", i), "timestamp": time.Now().UTC().Format(time.RFC3339Nano),
					"message": map[string]any{"content": []map[string]any{{"type": "tool_result", "tool_use_id": fmt.Sprintf("t-%d", i), "content": "x"}}}})
				r.write(t, string(b))
			}
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := newFeedRig(t)
			now := time.Now()
			r.write(t, feedUser("u-1", "start", now), feedText("a-1", "history", now))
			// History the scanner caught up on says nothing about what the hooks count.
			r.d.bumpHook(r.s.ID, "stop")
			if h := r.feed(t).Health; h != protocol.FeedOK {
				t.Fatalf("after the first scan: %q", h)
			}
			c.hooks(r.d, r.s.ID)
			if h := r.feed(t).Health; h != protocol.FeedPartial {
				t.Errorf("hooks fired and the reader found nothing: %q", h)
			}
			c.fix(r, t)
			if h := r.feed(t).Health; h != protocol.FeedOK {
				t.Errorf("once the reader found them: %q", h)
			}
		})
	}
}

// A subagent's tools fire hooks in this session, and their results are in another file.
func TestHealthIgnoresToolCountsOnceASubagentRan(t *testing.T) {
	r := newFeedRig(t)
	now := time.Now()
	r.write(t, feedUser("u-1", "start", now))
	r.feed(t)
	b, _ := json.Marshal(map[string]any{"type": "assistant", "uuid": "a-1", "timestamp": now.UTC().Format(time.RFC3339Nano),
		"message": map[string]any{"content": []map[string]any{{"type": "tool_use", "id": "t-1", "name": "Agent", "input": map[string]any{"description": "x"}}}}})
	r.write(t, string(b))
	for range 30 {
		r.d.bumpHook(r.s.ID, "tool_use")
	}
	if h := r.feed(t).Health; h != protocol.FeedOK {
		t.Errorf("health = %q", h)
	}
}

func TestHealthUnreadableWhenFileGrows(t *testing.T) {
	r := newFeedRig(t)
	line := `{"type":"a-format-from-the-future","pad":"` + strings.Repeat("z", 64<<10) + `"}`
	for range 5 {
		r.write(t, line)
	}
	if h := r.feed(t).Health; h != protocol.FeedUnreadable {
		t.Errorf("a growing file that yields nothing: %q", h)
	}
}

func TestHealthEmptyBeforeFirstPrompt(t *testing.T) {
	r := newFeedRig(t)
	data := r.feed(t)
	if data.Health != protocol.FeedEmpty || data.Scanning || len(data.Items) != 0 {
		t.Errorf("no file yet: %+v", data)
	}
	r.write(t, `{"type":"mode","mode":"default"}`)
	if h := r.feed(t).Health; h != protocol.FeedEmpty {
		t.Errorf("bookkeeping only: %q", h)
	}
}

func TestFeedUnsupportedWithoutAReader(t *testing.T) {
	r := newFeedRig(t)
	r.s.Agent = "opencode"
	r.d.sessions.Upsert(r.s)
	if agent.Caps(agent.OpenCode).Feed {
		t.Skip("opencode has a reader now")
	}
	data := r.feed(t)
	if data.Health != protocol.FeedUnsupported || data.Items == nil {
		t.Errorf("%+v", data)
	}
	if _, h := r.d.advanceFeed(context.Background(), r.s, 1<<20); h != protocol.FeedUnsupported {
		t.Errorf("advanceFeed health %q", h)
	}
	if _, err := r.d.handleAgentFeedOutput(context.Background(), protocol.AgentFeedOutputArgs{Session: r.s.ID, Item: "x"}); err == nil {
		t.Error("output with no reader")
	}
}

func TestFeedUnknownSession(t *testing.T) {
	r := newFeedRig(t)
	if _, err := r.d.handleAgentFeed(context.Background(), protocol.AgentFeedArgs{Session: "nope"}); err == nil {
		t.Error("an unknown session answered")
	}
}

// A session that has left the store keeps its conversation through State.Ended.
func TestFeedResolvesAGoneSessionThroughEnded(t *testing.T) {
	r := newFeedRig(t)
	r.write(t, feedUser("u-1", "hello from the past", time.Now()))
	store, err := r.d.sshStore(r.s.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	slug := filepath.Base(filepath.Dir(r.path))
	if err := store.Update(func(st *sshstore.State) error {
		st.Ended = &sshstore.EndedInfo{EndedSession: protocol.EndedSession{ID: r.s.ID, Agent: "claude"}, Segments: []string{feedSeg}, Slug: slug}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.Flush(); err != nil {
		t.Fatal(err)
	}
	r.d.sessions.Delete(r.s.ID)
	data := r.feed(t)
	if len(data.Items) != 1 || data.Items[0].Text != "hello from the past" {
		t.Errorf("%+v", data)
	}
}

func TestAdvanceFeedAnswersSeqAndHealth(t *testing.T) {
	r := newFeedRig(t)
	r.write(t, feedText("a-1", "one", time.Now()))
	seq, h := r.d.advanceFeed(context.Background(), r.s, 2<<20)
	if seq == 0 || h != protocol.FeedOK {
		t.Fatalf("seq=%d health=%q", seq, h)
	}
	r.write(t, feedText("a-2", "two", time.Now()))
	next, _ := r.d.advanceFeed(context.Background(), r.s, 2<<20)
	if next <= seq {
		t.Errorf("a new line did not move the sequence: %d then %d", seq, next)
	}
	// talkState must never wait for an agentFeed that is mid-scan.
	e := r.d.feedHub().entry(r.d, feedTarget{id: r.s.ID, kind: agent.Claude, slug: filepath.Base(filepath.Dir(r.path))})
	e.mu.Lock()
	defer e.mu.Unlock()
	done := make(chan struct{})
	go func() { r.d.advanceFeed(context.Background(), r.s, 2<<20); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("advanceFeed waited on a busy reader")
	}
}

func TestFeedOutputOverTheCommand(t *testing.T) {
	r := newFeedRig(t)
	now := time.Now().UTC().Format(time.RFC3339)
	r.write(t,
		`{"type":"assistant","uuid":"a-1","timestamp":"`+now+`","message":{"content":[{"type":"tool_use","id":"t-1","name":"Bash","input":{"command":"ls"}}]}}`,
		`{"type":"user","uuid":"u-1","timestamp":"`+now+`","message":{"content":[{"type":"tool_result","tool_use_id":"t-1","content":"file.txt"}]}}`)
	r.feed(t)
	out, err := r.d.handleAgentFeedOutput(context.Background(), protocol.AgentFeedOutputArgs{Session: r.s.ID, Item: "a-1:0"})
	if err != nil || out.Text != "file.txt" {
		t.Errorf("%+v %v", out, err)
	}
}

func TestFeedHubKeepsEightSessions(t *testing.T) {
	r := newFeedRig(t)
	hub := r.d.feedHub()
	for i := range 12 {
		hub.entry(r.d, feedTarget{id: "s" + strconv.Itoa(i), kind: agent.Claude, slug: "x"})
	}
	if len(hub.entries) != feedSessions {
		t.Errorf("held %d", len(hub.entries))
	}
	if hub.entries["s11"] == nil || hub.entries["s0"] != nil {
		t.Error("eviction did not take the least recently used")
	}
}

// The conversation is display only. The import guard is the part of that a compiler can
// check: the log's contents can only reach a client through talkfeed.go, plus talknote.go's
// use of the hash helper for what lola itself typed.
func TestControlLoopNeverImportsAgentfeed(t *testing.T) {
	const pkg = `"github.com/sushidev-team/lola/internal/agentfeed"`
	allowed := map[string]bool{"talkfeed.go": true, "talknote.go": true}
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	checked := 0
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		checked++
		for _, imp := range f.Imports {
			if imp.Path.Value == pkg && !allowed[filepath.Base(path)] {
				t.Errorf("%s imports agentfeed: nothing from an agent's log may reach the control loop", path)
			}
		}
	}
	if checked == 0 {
		t.Fatal("the guard read no files")
	}
	for _, name := range []string{"reactions.go", "observer.go", "reviewer.go", "dispatch.go", "reconcile.go"} {
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(src), "AgentFeed") {
			t.Errorf("%s references AgentFeed", name)
		}
	}
}
