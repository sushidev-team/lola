package daemon

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sushidev-team/lola/internal/agent"
	"github.com/sushidev-team/lola/internal/agentfeed"
	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/quota"
	"github.com/sushidev-team/lola/internal/runtime"
	"github.com/sushidev-team/lola/internal/session"
	"github.com/sushidev-team/lola/internal/sshstore"
	"github.com/sushidev-team/lola/internal/usage"
)

// This file is the daemon's one door to the agent's log. Everything that comes through it
// is a CLAIM and goes to a client's display and nowhere else: no axis, Attention, slot,
// reaction, write-back, gate or agent ever reads it, and agent liveness never derives
// from it. TestControlLoopNeverImportsAgentfeed keeps it that way.
//
// It is pull-driven or watch-driven: talkState moves a viewed session's reader a little
// every second, agentFeed moves it by wall clock, and the push prober (watchprobe.go)
// moves it for a session a phone watches. Nothing runs for a session no client is
// looking at or watching. Its one consumer outside a display is the push path's diff
// tick (an edit/run item pokes diff:<id>), which is itself display-only.

const (
	// feedSessions bounds how many sessions keep a reader in memory.
	feedSessions = 8
	// feedEchoWindow is how far apart a send and its log record may be stamped.
	feedEchoWindow = 5 * time.Minute
)

// agentFeed advances by wall clock, because talkState's 2 MB budget would need a minute
// to reach the tail of a large transcript. These are variables so a test can watch a scan
// in progress.
var (
	feedWallClock  = time.Second
	feedChunkBytes = int64(8 << 20)
)

// feedSend is one thing lola knows it typed.
type feedSend struct {
	Hash string
	At   time.Time
}

func (s feedSend) key() string { return s.Hash + "@" + s.At.UTC().Format(time.RFC3339Nano) }

// feedEntry is one session's reader. mu serializes everything that touches feed; the two
// atomics let talkState answer without waiting for an agentFeed that is mid-scan.
type feedEntry struct {
	mu   sync.Mutex
	id   string
	kind agent.Kind
	slug string
	root string // codex: where its log was found, which moves when a private home appears
	feed *agentfeed.Feed

	lastUsed int64
	seq      atomic.Int64
	health   atomic.Value
	epoch    atomic.Value // the reader's epoch, for talkState

	pending map[string]bool // user items no send has claimed yet
	claimed map[string]bool // sends that have matched a record
	gen     int64

	// What the health judgement compares: hooks and feed counts since the read first
	// reached the end of the log. Before that the feed is catching up on history that
	// no hook counter saw, and a comparison would mean nothing.
	baseSet    bool
	baseHooks  hookTally
	baseCounts agentfeed.Counts
}

type feedHub struct {
	mu      sync.Mutex
	entries map[string]*feedEntry
	// pins counts the watches (talk or feed) holding a session's reader. They live on the
	// hub, not the entry, so an entry replaced on an agent switch keeps its pin. A pinned
	// reader is never evicted; the watch hub caps pinned sessions at feedSessions.
	pins  map[string]int
	clock int64
}

// pin keeps a session's reader out of LRU eviction until a matching unpin.
func (h *feedHub) pin(id string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.pins == nil {
		h.pins = map[string]int{}
	}
	h.pins[id]++
}

// unpin releases one pin. The count never goes below zero.
func (h *feedHub) unpin(id string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.pins[id] <= 1 {
		delete(h.pins, id)
		return
	}
	h.pins[id]--
}

// feedHubs holds each daemon's hub. The daemon has one, a test has many.
var feedHubs sync.Map // map[*Daemon]*feedHub

func (d *Daemon) feedHub() *feedHub {
	h, _ := feedHubs.LoadOrStore(d, &feedHub{entries: map[string]*feedEntry{}})
	return h.(*feedHub)
}

// feedTarget is a session as the reader needs it, live or ended.
type feedTarget struct {
	id    string
	kind  agent.Kind
	slug  string
	cwd   string
	root  string // codex: the sessions directory of the CODEX_HOME this session ran with
	known bool
}

// feedResolve finds a session in the store, or what is left of a gone one.
func (d *Daemon) feedResolve(id string) (feedTarget, bool, error) {
	id = session.BaseID(id)
	if s, ok := d.sessions.Get(id); ok {
		if s.IsAgentless() {
			return feedTarget{}, false, nil
		}
		cwd := sessionAgentCWD(d.home, s)
		t := feedTarget{id: id, kind: agent.Parse(s.Agent), slug: filepath.Base(usage.SlugDir(usage.ProjectsRoot(), cwd)), cwd: cwd}
		if t.kind == agent.Codex {
			t.root = d.codexFeedRoot(id, s.Project, cwd)
		}
		return t, true, nil
	}
	if ended := sshstore.ReadEnded(d.home, id); ended != nil {
		t := feedTarget{id: id, kind: agent.Parse(ended.Agent), slug: ended.Slug}
		if t.kind == agent.Codex {
			// A rollout is found by the directory it ran in, which an ended session no longer
			// records: it is the kept directory, or where lola puts a session's worktree.
			t.cwd = ended.AgentCWD
			if t.cwd == "" {
				t.cwd = ended.KeptWorktree
			}
			if t.cwd == "" && ended.Project != "" {
				t.cwd = filepath.Join(d.home, "worktrees", ended.Project, id)
			}
			t.root = d.codexFeedRoot(id, ended.Project, t.cwd)
		}
		return t, true, nil
	}
	return feedTarget{}, false, errors.New("unknown session " + id)
}

// feedSegments names the transcript files of a session. A live session that predates the
// field, and that fires no hook while it rests, is seeded from the base name of the path
// it last reported. Only that base name is used: the path itself came from a hook.
func (d *Daemon) feedSegments(id string) []string {
	if s, ok := d.sessions.Get(id); ok {
		if len(s.TranscriptSegments) > 0 {
			return s.TranscriptSegments
		}
		if s.TranscriptPath != "" {
			if base := filepath.Base(filepath.Clean(s.TranscriptPath)); agentfeed.ValidSegment(base) {
				return []string{base}
			}
		}
		return nil
	}
	if ended := sshstore.ReadEnded(d.home, id); ended != nil {
		return ended.Segments
	}
	return nil
}

// codexFeedRoot is the sessions directory of the CODEX_HOME a session's codex writes to,
// which is not always the user's: a sandboxed one keeps a private home under lola's
// (~/.lola/sandbox/<id>/codex), an older one kept it in the worktree, and a project may
// name its own. It is derived from lola's own layout and the config, never read from the
// session's env file, which the agent can write. The private homes are the agent's to
// write as well, so they count only while every level is a plain directory.
func (d *Daemon) codexFeedRoot(id, project, cwd string) string {
	private := []string{filepath.Join(d.home, "sandbox", id, "codex")}
	if cwd != "" {
		private = append(private, filepath.Join(cwd, ".lola", "codex"))
	}
	for _, home := range private {
		if sessions := filepath.Join(home, "sessions"); plainDirs(filepath.Dir(home), home, sessions) {
			return sessions
		}
	}
	d.mu.Lock()
	home := ""
	if d.cfg != nil {
		if p := d.cfg.ProjectByName(project); p != nil {
			home = p.Env["CODEX_HOME"]
		}
	}
	d.mu.Unlock()
	if home == "" {
		home = quota.CodexHome()
	}
	return filepath.Join(home, "sessions")
}

// plainDirs says every path is a directory and none of them a symlink.
func plainDirs(paths ...string) bool {
	for _, p := range paths {
		if li, err := os.Lstat(p); err != nil || !li.IsDir() {
			return false
		}
	}
	return true
}

func (d *Daemon) newFeedSource(t feedTarget) agentfeed.Source {
	switch t.kind {
	case agent.Codex:
		return agentfeed.NewCodex(t.root, t.cwd)
	case agent.OpenCode:
		return agentfeed.NewOpencode(t.cwd)
	}
	id := t.id
	return agentfeed.NewClaude(usage.ProjectsRoot(), t.slug, func() []string { return d.feedSegments(id) })
}

// entry finds or makes a session's reader, evicting the least recently used.
func (h *feedHub) entry(d *Daemon, t feedTarget) *feedEntry {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.clock++
	e := h.entries[t.id]
	if e == nil || e.kind != t.kind || e.slug != t.slug || e.root != t.root {
		e = &feedEntry{id: t.id, kind: t.kind, slug: t.slug, root: t.root, pending: map[string]bool{}, claimed: map[string]bool{}}
		e.feed = agentfeed.NewFeed(func() agentfeed.Source { return d.newFeedSource(t) })
		e.gen = e.feed.Generation()
		h.entries[t.id] = e
	}
	e.lastUsed = h.clock
	for len(h.entries) > feedSessions {
		var oldest *feedEntry
		for _, c := range h.entries {
			if c != e && h.pins[c.id] == 0 && (oldest == nil || c.lastUsed < oldest.lastUsed) {
				oldest = c
			}
		}
		if oldest == nil {
			break
		}
		delete(h.entries, oldest.id)
	}
	return e
}

// advance moves the reader once and marks what changed. The caller holds e.mu.
func (e *feedEntry) advance(ctx context.Context, d *Daemon, budget int64) agentfeed.Health {
	changed, h := e.feed.Advance(ctx, budget)
	if g := e.feed.Generation(); g != e.gen {
		e.gen = g
		e.pending, e.claimed = map[string]bool{}, map[string]bool{}
		e.baseSet = false
	}
	for _, it := range changed {
		if it.Kind == protocol.FeedItemUser && it.Parent == "" && !it.Echo && it.Origin != "task" {
			e.pending[it.ID] = true
		}
	}
	e.mark(d)
	if len(changed) > 0 && d != nil && d.watch != nil {
		// The one choke point every reader goes through: a desktop pull that moves the
		// reader also pushes to a phone watching feed:<id>. A poke takes only the hub's
		// leaf lock, so it is safe under e.mu.
		d.watch.PokeSession(e.id, pokeFeed)
	}
	return h
}

// mark decides, for each user record not yet claimed, whether it is something lola typed.
// A send is recorded just after it is typed, so a record can be read first. An unmatched
// record therefore reads as typed in the terminal and stays pending for the window, and it
// turns into an echo if its send shows up.
func (e *feedEntry) mark(d *Daemon) {
	if len(e.pending) == 0 {
		return
	}
	sends, since := d.feedSends(e.id)
	now := time.Now()
	// In log order, so the first of two identical records is the one that claims a send.
	waiting := make([]protocol.AgentFeedItem, 0, len(e.pending))
	for id := range e.pending {
		if it, ok := e.feed.Item(id); ok {
			waiting = append(waiting, it)
		} else {
			delete(e.pending, id)
		}
	}
	sort.Slice(waiting, func(i, j int) bool { return waiting[i].Order < waiting[j].Order })
	for _, it := range waiting {
		id := it.ID
		// lola's own launch line would otherwise open every worktree's story as something
		// typed in the terminal. A forged copy of it can only hide itself.
		if runtime.IsLaunchPrompt(e.id, it.Text) {
			e.feed.Update(id, func(i *protocol.AgentFeedItem) { i.Echo, i.Origin = true, "" })
			delete(e.pending, id)
			continue
		}
		if send, ok := matchSend(sends, e.claimed, e.feed.TextHash(id), it.At); ok {
			e.claimed[send.key()] = true
			e.feed.Update(id, func(i *protocol.AgentFeedItem) { i.Echo, i.Origin = true, "" })
			delete(e.pending, id)
			continue
		}
		if beforeSendHistory(it.At, since) {
			e.feed.Update(id, func(i *protocol.AgentFeedItem) { i.Echo, i.Origin = true, "" })
			delete(e.pending, id)
			continue
		}
		if it.Origin == "" {
			e.feed.Update(id, func(i *protocol.AgentFeedItem) { i.Origin = "terminal" })
		}
		if it.At.IsZero() || now.Sub(it.At) > feedEchoWindow {
			delete(e.pending, id)
		}
	}
}

// matchSend finds the oldest unclaimed send of this text within the window. Claiming is
// what makes a forged copy of a real message read as typed in the terminal.
func matchSend(sends []feedSend, claimed map[string]bool, hash string, at time.Time) (feedSend, bool) {
	if hash == "" || at.IsZero() {
		return feedSend{}, false
	}
	for _, s := range sends {
		if s.Hash != hash || claimed[s.key()] {
			continue
		}
		diff := at.Sub(s.At)
		if diff < 0 {
			diff = -diff
		}
		if diff <= feedEchoWindow {
			return s, true
		}
	}
	return feedSend{}, false
}

// feedSends lists what lola typed into a session, and the time that record reaches back to,
// which is zero while it still holds everything. The whole log is matched against this after
// a restart, an eviction or on paging older history, so anything older than the record cannot
// be judged (see beforeSendHistory).
func (d *Daemon) feedSends(id string) ([]feedSend, time.Time) {
	store, err := d.sshStore(id, false)
	if err != nil || store == nil {
		return nil, time.Time{}
	}
	var out []feedSend
	var since time.Time
	store.View(func(st *sshstore.State) {
		for _, b := range st.Sent {
			out = append(out, feedSend{Hash: b.Hash, At: b.At})
		}
		if len(st.Sent) >= sshstore.MaxSent {
			since = st.Sent[0].At
		}
	})
	return out, since
}

// beforeSendHistory says no record of sends reaches back to this log record. It might be a CI
// reaction, a review hand-off or a joined batch lola typed, each of which would otherwise read
// as a row typed in the terminal and split its turn. So it is hidden: a person's own message
// from then still has its store row, and what is lost is only the rare terminal row that old.
func beforeSendHistory(at, since time.Time) bool {
	return !since.IsZero() && !at.IsZero() && at.Before(since)
}

// judge turns the reader's own view of a log into the feed's health. Health is not judged
// while scanning. Past that, the file alone can only detect total failure, so partial
// comes from comparing what the hooks counted with what the reader found: stops with no
// text, tools with too few results, or prompts with no user records.
func (e *feedEntry) judge(d *Daemon, h agentfeed.Health) agentfeed.Health {
	if e.feed.Scanning() {
		return protocol.FeedOK
	}
	hooks, counts := d.hookTallyFor(e.id), e.feed.Counts()
	if !e.baseSet {
		e.baseSet, e.baseHooks, e.baseCounts = true, hooks, counts
	}
	if h != protocol.FeedOK {
		return h
	}
	var (
		stops   = hooks.Stop - e.baseHooks.Stop
		tools   = hooks.ToolUse - e.baseHooks.ToolUse
		prompts = hooks.UserPrompt - e.baseHooks.UserPrompt
		text    = counts.Text - e.baseCounts.Text
		results = counts.Results - e.baseCounts.Results
		users   = counts.User - e.baseCounts.User
		// A subagent's tools fire hooks in this session, while their results sit in the
		// subagent's own file. Once one has run, the tool comparison proves nothing.
		subagents = counts.AgentCalls - e.baseCounts.AgentCalls
	)
	switch {
	case stops >= 3 && text == 0,
		prompts >= 3 && users == 0,
		tools >= 20 && subagents == 0 && float64(results) < 0.8*float64(tools):
		return protocol.FeedPartial
	}
	return protocol.FeedOK
}

func (d *Daemon) handleAgentFeed(ctx context.Context, a protocol.AgentFeedArgs) (protocol.AgentFeedData, error) {
	id := session.BaseID(a.Session)
	t, ok, err := d.feedResolve(id)
	if err != nil {
		return protocol.AgentFeedData{}, err
	}
	if !ok || !agent.Caps(t.kind).Feed {
		return protocol.AgentFeedData{Session: id, Health: protocol.FeedUnsupported, Items: []protocol.AgentFeedItem{}}, nil
	}
	e := d.feedHub().entry(d, t)
	e.mu.Lock()
	defer e.mu.Unlock()

	var h agentfeed.Health
	deadline := time.Now().Add(feedWallClock)
	for {
		h = e.advance(ctx, d, feedChunkBytes)
		if !e.feed.Scanning() || !time.Now().Before(deadline) || ctx.Err() != nil {
			break
		}
	}
	health := e.judge(d, h)
	e.seq.Store(e.feed.LastSeq())
	e.health.Store(health)

	if a.Parent != "" {
		items, more, err := e.feed.Thread(ctx, a.Parent, a.Limit)
		if err != nil {
			return protocol.AgentFeedData{}, err
		}
		return protocol.AgentFeedData{Session: id, Epoch: e.feed.Epoch(), Health: health, More: more, Items: items}, nil
	}
	a.Session = id
	data := e.feed.Page(ctx, a)
	data.Health = health
	if a.Before != 0 {
		e.markHistory(d, data.Items)
	}
	return data, nil
}

// markHistory marks a page of older items, which a rescan just read and which the live
// feed no longer holds, so nothing is remembered about them.
func (e *feedEntry) markHistory(d *Daemon, items []protocol.AgentFeedItem) {
	sends, since := d.feedSends(e.id)
	for i := range items {
		it := &items[i]
		if it.Kind != protocol.FeedItemUser || it.Echo || it.Origin == "task" || it.Origin == "system" {
			continue
		}
		_, sent := matchSend(sends, map[string]bool{}, e.feed.TextHash(it.ID), it.At)
		if sent || runtime.IsLaunchPrompt(e.id, it.Text) || beforeSendHistory(it.At, since) {
			it.Echo, it.Origin = true, ""
		} else if it.Origin == "" {
			it.Origin = "terminal"
		}
	}
}

func (d *Daemon) handleAgentFeedOutput(ctx context.Context, a protocol.AgentFeedOutputArgs) (protocol.AgentFeedOutputData, error) {
	t, ok, err := d.feedResolve(a.Session)
	if err != nil {
		return protocol.AgentFeedOutputData{}, err
	}
	if !ok || !agent.Caps(t.kind).Feed {
		return protocol.AgentFeedOutputData{}, errors.New("no conversation reader for this agent")
	}
	e := d.feedHub().entry(d, t)
	e.mu.Lock()
	defer e.mu.Unlock()
	text, cut, err := e.feed.Output(ctx, a.Item)
	if err != nil {
		// The log's own error may name a path under the user's home.
		if errors.Is(err, agentfeed.ErrNoOutput) {
			return protocol.AgentFeedOutputData{}, agentfeed.ErrNoOutput
		}
		return protocol.AgentFeedOutputData{}, errors.New("that output can no longer be read")
	}
	return protocol.AgentFeedOutputData{Text: text, Truncated: cut}, nil
}

// advanceFeed is the default feedAdvance seam: it moves a session's scanner by at most
// budget bytes and answers the newest sequence and the feed's health. It never waits on an
// agentFeed that is mid-scan, because talkState runs every second and has a pane to read.
func (d *Daemon) advanceFeed(ctx context.Context, s session.Session, budget int64) (int64, string) {
	t, ok, err := d.feedResolve(s.ID)
	if err != nil || !ok || !agent.Caps(t.kind).Feed {
		return 0, protocol.FeedUnsupported
	}
	e := d.feedHub().entry(d, t)
	if !e.mu.TryLock() {
		health, _ := e.health.Load().(string)
		if health == "" {
			health = protocol.FeedOK
		}
		return e.seq.Load(), health
	}
	defer e.mu.Unlock()
	health := e.judge(d, e.advance(ctx, d, budget))
	e.seq.Store(e.feed.LastSeq())
	e.epoch.Store(e.feed.Epoch())
	e.health.Store(health)
	return e.feed.LastSeq(), health
}

// feedEpochOf is the epoch of a session's reader, "" when it has none. A reader rebuilt by a
// restart, an eviction or an agent switch starts its sequence over and can come back lower, so
// a client that only watches the sequence for growth would stop asking until it caught up.
func (d *Daemon) feedEpochOf(id string) string {
	h := d.feedHub()
	h.mu.Lock()
	e := h.entries[id]
	h.mu.Unlock()
	if e == nil {
		return ""
	}
	epoch, _ := e.epoch.Load().(string)
	return epoch
}
