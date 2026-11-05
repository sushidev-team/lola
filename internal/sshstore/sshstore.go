// Package sshstore is the record of what an agent-assisted ssh session did:
// connections, wheels, grants, blocks, elements, system events and the human's
// words, per session, under ~/.lola/state/ssh/<session>/. It is deliberately NOT
// in the session snapshot: an audit trail of thousands of commands does not
// belong in a file rewritten on every observer cycle.
//
// Files are 0600 in 0700 directories, written temp + rename. One mutex per store,
// never held across an exec. The store knows nothing about ssh or tmux. Callers
// mutate State inside Update, and the invariants that keep the timeline honest
// (Order fixed at creation, Seq moving on every change, the retention caps) live
// here so no caller can forget them.
package sshstore

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/sshelement"
	"github.com/sushidev-team/lola/internal/sshterm"
)

// Retention caps. See docs/ssh-sessions.md section 5.
const (
	MaxBlocks      = 2000
	MaxEvents      = 1000
	MaxOutputBytes = 256 << 20
	// RetainAfterGone is how long a store outlives its session, as the audit trail
	// of what ran on a host.
	RetainAfterGone = 14 * 24 * time.Hour
	// MaxQueueBytes bounds the text waiting for the agent, so a human who keeps
	// talking to an agent that never reads cannot grow a state file without limit.
	MaxQueueBytes = 64 << 10
)

// Conn is one connection as stored: the wire form, plus what only the daemon
// needs. Sock never reaches the agent, and on the wire only the id is used.
type Conn struct {
	protocol.SSHConnInfo

	// Sock is the control socket's path, set at the first go.
	Sock string `json:"sock,omitempty"`
	// Connection overrides are pinned to the master, including across reload/restart.
	Port         int    `json:"port,omitempty"`
	IdentityFile string `json:"identityFile,omitempty"`
	// DenyReason is the human's reason, returned to the agent.
	DenyReason string `json:"denyReason,omitempty"`
	// ConnectingSince times the connecting to authenticating label.
	ConnectingSince time.Time `json:"connectingSince,omitzero"`
	// LastUsed is the last moment anything was known to be using the master, which
	// is how a lost connection is told apart from an idle timeout.
	LastUsed time.Time `json:"lastUsed,omitzero"`
	// WheelMark is the tab's history size when the human took the wheel, the start
	// of a handback transcript. WheelMarkTab names which tab it was read from.
	WheelMark    int    `json:"wheelMark,omitempty"`
	WheelMarkTab string `json:"wheelMarkTab,omitempty"`
	// TermNonce is the secret in the shell's markers. Empty means no capture.
	TermNonce string `json:"termNonce,omitempty"`
	// AnswerEchoTab names a tab whose pane echoed an answer to a prompt, which only a fake
	// or a badly behaved prompt does. Its transcript is not offered at a handback.
	AnswerEchoTab string `json:"answerEchoTab,omitempty"`
	// TabN is the highest tab number this connection has used.
	TabN int `json:"tabN,omitempty"`
	// AuthNotified records that the authentication notification went out for this
	// connection attempt, so it fires once.
	AuthNotified bool `json:"authNotified,omitempty"`
}

// Terminal reports whether the connection can no longer change.
func (c *Conn) Terminal() bool {
	return c.State == protocol.SSHConnDenied || c.State == protocol.SSHConnClosed
}

// QueueEntry is one piece of text waiting for the agent: a human's words (Item
// names the timeline item) or one of lola's nudges (Item empty). Text is framed.
// Claimed means a send through the prompt is in flight, so the reply path must not
// also hand it over.
type QueueEntry struct {
	ID      int    `json:"id"`
	Item    string `json:"item,omitempty"`
	Text    string `json:"text"`
	Claimed bool   `json:"claimed,omitempty"`
}

// State is everything a session's store holds. Exported for the JSON file and for
// Update's callback. A caller must only touch it inside Update or View.
type State struct {
	Session string `json:"session"`
	Seq     int64  `json:"seq"`
	Order   int64  `json:"order"`

	NextBlock   int `json:"nextBlock"`
	NextElement int `json:"nextElement"`
	NextEvent   int `json:"nextEvent"`
	NextHuman   int `json:"nextHuman"`
	NextQueue   int `json:"nextQueue"`

	Conns []*Conn                     `json:"conns"`
	Items []*protocol.SSHTimelineItem `json:"items"`
	Queue []QueueEntry                `json:"queue,omitempty"`

	// TalkConn is the tab the human last spoke to the agent from. With two tabs on one
	// host it decides which one the agent's next command goes to, and the agent never
	// has to name a tab.
	TalkConn string `json:"talkConn,omitempty"`

	// WidgetReports remembers what the agent was told about each widget, by element id,
	// so a reload or a restart of the app does not tell it again.
	WidgetReports map[string]WidgetReport `json:"widgetReports,omitempty"`

	// OutputBytes counts stored output, for the per-session cap.
	OutputBytes int64 `json:"outputBytes,omitempty"`
}

// WidgetReport holds a fingerprint and never the page's words, which go to the agent and
// nowhere else.
type WidgetReport struct {
	Revision int    `json:"revision"`
	Mark     string `json:"mark"`
	// Page fingerprints the HTML the budget below counts for, and Since and Sent are that
	// budget: how many lines this page has cost since when.
	Page  string    `json:"page,omitempty"`
	Since time.Time `json:"since,omitzero"`
	Sent  int       `json:"sent,omitempty"`
}

// Store is one session's record.
type Store struct {
	mu   sync.RWMutex
	dir  string
	st   State
	dirt bool
}

func statePath(dir string) string { return filepath.Join(dir, "state.json") }

// ValidSession refuses a session id that could not be a single path segment. The
// id comes from a request, so it never reaches a path unchecked.
func ValidSession(id string) bool {
	return id != "" && id != "." && id != ".." && filepath.Base(id) == id && !strings.ContainsAny(id, `/\`) && !strings.HasPrefix(id, ".")
}

// Root is the directory holding every session's store.
func Root(home string) string { return filepath.Join(home, "state", "ssh") }

// Open loads a session's store, creating its directory when create is set. It
// returns nil, nil when the store does not exist and create is false.
func Open(home, session string, create bool, logf func(string, ...any)) (*Store, error) {
	if !ValidSession(session) {
		return nil, fmt.Errorf("session id %q is not valid", session)
	}
	if logf == nil {
		logf = func(string, ...any) {}
	}
	dir := filepath.Join(Root(home), session)
	s := &Store{dir: dir, st: State{Session: session}}
	raw, err := os.ReadFile(statePath(dir))
	switch {
	case err == nil:
		var st State
		if jerr := json.Unmarshal(raw, &st); jerr != nil {
			// Never overwrite a record that cannot be read: it is the audit trail.
			// Set it aside and start clean so the feature still works.
			aside := statePath(dir) + ".corrupt-" + strconv.FormatInt(time.Now().Unix(), 10)
			_ = os.Rename(statePath(dir), aside)
			logf("ssh store %s: unreadable state set aside as %s: %v", session, aside, jerr)
			return s, nil
		}
		st.Session = session
		s.st = st
		return s, nil
	case !errors.Is(err, os.ErrNotExist):
		return nil, err
	}
	if !create {
		if _, serr := os.Stat(dir); serr != nil {
			return nil, nil
		}
		return s, nil
	}
	if err := os.MkdirAll(filepath.Join(dir, "out"), 0o700); err != nil {
		return nil, err
	}
	return s, nil
}

// Update runs fn under the lock, then persists. fn must not mutate before
// returning an error. A persist failure is returned, the change stays in memory,
// and the caller decides what an unrecorded fact means.
func (s *Store) Update(fn func(*State) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := fn(&s.st); err != nil {
		return err
	}
	return s.persistLocked()
}

// Volatile runs fn under the lock without persisting: the once-a-second progress
// of a running block. The next Update carries it to disk. fn reports whether it
// changed anything.
func (s *Store) Volatile(fn func(*State) bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if fn(&s.st) {
		s.dirt = true
	}
}

// View runs fn under the read lock.
func (s *Store) View(fn func(*State)) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	fn(&s.st)
}

// Flush persists volatile changes.
func (s *Store) Flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.dirt {
		return nil
	}
	return s.persistLocked()
}

func (s *Store) persistLocked() error {
	raw, err := json.Marshal(&s.st)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(s.dir, "out"), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(s.dir, ".state-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), statePath(s.dir)); err != nil {
		return err
	}
	s.dirt = false
	return nil
}

// Dir is the store's directory.
func (s *Store) Dir() string { return s.dir }

// --- State helpers. All of these assume the caller holds the lock via Update,
// Volatile or View.

// Conn finds a connection, nil when there is none.
func (st *State) Conn(id string) *Conn {
	for _, c := range st.Conns {
		if c.ID == id {
			return c
		}
	}
	return nil
}

// ConnForHost is the connection a command on host would use: the tab the human last
// spoke from when it is on this host, else the newest one that is not closed. A closed
// connection is not a standing answer, so a host whose connections have all closed has
// none. nil when there is none.
func (st *State) ConnForHost(host string) *Conn {
	if c := st.Conn(st.TalkConn); c != nil && c.Host == host && !c.Terminal() {
		return c
	}
	for _, c := range slices.Backward(st.Conns) {
		if c.Host == host && c.State != protocol.SSHConnClosed {
			return c
		}
	}
	return nil
}

// SharesMaster reports whether another live connection runs on c's master. Such a master
// is not c's to close, whoever opened it.
func (st *State) SharesMaster(c *Conn) bool {
	if c.Sock == "" {
		return false
	}
	for _, o := range st.Conns {
		if o.ID != c.ID && o.Sock == c.Sock && sshMasterInUse(o) {
			return true
		}
	}
	return false
}

func sshMasterInUse(c *Conn) bool {
	switch c.State {
	case protocol.SSHConnConnecting, protocol.SSHConnAuthenticating, protocol.SSHConnConnected:
		return true
	}
	return false
}

// MasterFor finds a running connection with the same destination, whose login a new tab
// can reuse. The caller still asks ssh whether that master answers.
func (st *State) MasterFor(self, alias, user string, port int, identity string) *Conn {
	for _, o := range slices.Backward(st.Conns) {
		if o.ID != self && o.Sock != "" && o.State == protocol.SSHConnConnected &&
			o.Alias == alias && o.User == user && o.Port == port && o.IdentityFile == identity {
			return o
		}
	}
	return nil
}

// Trash removes a connection and everything recorded under it, stored output included,
// for good. The caller closes the connection first. The last connection takes what no
// connection owned with it, or a session with no tabs would keep a timeline nothing shows.
func (s *Store) Trash(st *State, conn string) {
	st.Conns = slices.DeleteFunc(st.Conns, func(c *Conn) bool { return c.ID == conn })
	last := len(st.Conns) == 0
	gone := map[string]bool{}
	st.Items = slices.DeleteFunc(st.Items, func(it *protocol.SSHTimelineItem) bool {
		ic := itemConn(it)
		if ic != conn && !(last && ic == "") {
			return false
		}
		gone[it.ID] = true
		if it.Kind == protocol.SSHItemBlock && it.Block != nil {
			if !it.Block.OutputPruned {
				st.OutputBytes -= it.Block.StdoutBytes + it.Block.StderrBytes
			}
			s.removeOutput(it.Block.ID)
		}
		return true
	})
	st.Queue = slices.DeleteFunc(st.Queue, func(q QueueEntry) bool { return q.Item != "" && gone[q.Item] })
	for id := range st.WidgetReports {
		if gone[id] {
			delete(st.WidgetReports, id)
		}
	}
	if st.OutputBytes < 0 {
		st.OutputBytes = 0
	}
	if st.TalkConn == conn {
		st.TalkConn = ""
	}
	st.Seq++
}

// Item finds a timeline item, nil when there is none.
func (st *State) Item(id string) *protocol.SSHTimelineItem {
	for _, it := range slices.Backward(st.Items) {
		if it.ID == id {
			return it
		}
	}
	return nil
}

// Block finds a block's item, nil when there is none.
func (st *State) Block(id string) *protocol.SSHBlock {
	if it := st.Item(id); it != nil {
		return it.Block
	}
	return nil
}

// Touch records that an item changed: its Seq moves, its Order does not.
func (st *State) Touch(it *protocol.SSHTimelineItem) {
	st.Seq++
	it.Seq = st.Seq
}

// TouchConn records that a connection changed, so a client polling on LastSeq
// sees it. Connections are not timeline items, but SSHTimelineData always carries
// them current, and a moved Seq is the signal to refetch.
func (st *State) TouchConn() { st.Seq++ }

func (st *State) add(it *protocol.SSHTimelineItem) *protocol.SSHTimelineItem {
	st.Order++
	st.Seq++
	it.Order, it.Seq = st.Order, st.Seq
	st.Items = append(st.Items, it)
	return it
}

// NewBlockID allocates the next block id.
func (st *State) NewBlockID() string {
	st.NextBlock++
	return "b" + strconv.Itoa(st.NextBlock)
}

// AddBlock appends a block to the timeline.
func (st *State) AddBlock(b protocol.SSHBlock, at time.Time) *protocol.SSHBlock {
	bc := b
	bc.Elevated = sshterm.Elevated(b.Command)
	it := st.add(&protocol.SSHTimelineItem{ID: b.ID, Kind: protocol.SSHItemBlock, At: at, Block: &bc})
	return it.Block
}

// NewElementID allocates the next element id.
func (st *State) NewElementID() string {
	st.NextElement++
	return "e" + strconv.Itoa(st.NextElement)
}

// PutElement adds or replaces an element, keeping its position in the timeline.
func (st *State) PutElement(e protocol.SSHElement, at time.Time) {
	if it := st.Item(e.ID); it != nil && it.Kind == protocol.SSHItemElement {
		ec := e
		it.Element = &ec
		st.Touch(it)
		return
	}
	ec := e
	st.add(&protocol.SSHTimelineItem{ID: e.ID, Kind: protocol.SSHItemElement, At: at, Element: &ec})
}

// ElementByRef finds the element a post with this kind and ref updates.
func (st *State) ElementByRef(kind, ref string) *protocol.SSHElement {
	if ref == "" {
		return nil
	}
	for _, it := range slices.Backward(st.Items) {
		if it.Kind == protocol.SSHItemElement && it.Element.Ref == ref && it.Element.Kind == kind {
			return it.Element
		}
	}
	return nil
}

// AddEvent records a system fact.
func (st *State) AddEvent(ev protocol.SSHSystemEvent, at time.Time) {
	st.NextEvent++
	evc := ev
	st.add(&protocol.SSHTimelineItem{ID: "s" + strconv.Itoa(st.NextEvent), Kind: protocol.SSHItemSystem, At: at, System: &evc})
}

// AddHuman records something the human said and returns its item.
func (st *State) AddHuman(m protocol.SSHHumanMessage, at time.Time) *protocol.SSHTimelineItem {
	st.NextHuman++
	mc := m
	return st.add(&protocol.SSHTimelineItem{ID: "h" + strconv.Itoa(st.NextHuman), Kind: protocol.SSHItemHuman, At: at, Human: &mc})
}

// QueueBytes is the size of the undelivered text.
func (st *State) QueueBytes() int {
	n := 0
	for _, q := range st.Queue {
		n += len(q.Text)
	}
	return n
}

// Enqueue adds text for the agent. item is "" for a nudge.
func (st *State) Enqueue(item, text string) {
	st.NextQueue++
	st.Queue = append(st.Queue, QueueEntry{ID: st.NextQueue, Item: item, Text: text})
}

// PendingText is the join of what is waiting and not already being sent. The
// session record's PendingSSH is exactly this.
func (st *State) PendingText() string {
	var parts []string
	for _, q := range st.Queue {
		if !q.Claimed {
			parts = append(parts, q.Text)
		}
	}
	return strings.Join(parts, "\n\n")
}

// Deliver removes the queue entries deliver selects and marks their items
// delivered. Returns their text in order.
func (st *State) Deliver(via string, at time.Time, deliver func(QueueEntry) bool) []string {
	var out []string
	kept := st.Queue[:0:0]
	for _, q := range st.Queue {
		if !deliver(q) {
			kept = append(kept, q)
			continue
		}
		out = append(out, q.Text)
		if it := st.Item(q.Item); it != nil && it.Human != nil {
			it.Human.Delivered = true
			it.Human.DeliveredAt = at
			it.Human.Via = via
			st.Touch(it)
		}
	}
	st.Queue = kept
	return out
}

// Prune applies the retention caps. Call it inside Update, after adding items.
func (s *Store) Prune(st *State) {
	var blocks, elements, events int
	for _, it := range st.Items {
		switch it.Kind {
		case protocol.SSHItemBlock:
			blocks++
		case protocol.SSHItemElement:
			elements++
		case protocol.SSHItemSystem:
			events++
		}
	}
	if blocks > MaxBlocks {
		st.Items = slices.DeleteFunc(st.Items, func(it *protocol.SSHTimelineItem) bool {
			if blocks <= MaxBlocks || it.Kind != protocol.SSHItemBlock || !protocol.SSHBlockTerminal(it.Block.Status) {
				return false
			}
			blocks--
			st.OutputBytes -= it.Block.StdoutBytes + it.Block.StderrBytes
			s.removeOutput(it.Block.ID)
			return true
		})
	}
	if events > MaxEvents {
		st.Items = slices.DeleteFunc(st.Items, func(it *protocol.SSHTimelineItem) bool {
			if events <= MaxEvents || it.Kind != protocol.SSHItemSystem {
				return false
			}
			events--
			return true
		})
	}
	// Oldest closed element first. One a human could still act on goes last.
	for _, closedOnly := range []bool{true, false} {
		if elements <= sshelement.MaxElements {
			break
		}
		st.Items = slices.DeleteFunc(st.Items, func(it *protocol.SSHTimelineItem) bool {
			if elements <= sshelement.MaxElements || it.Kind != protocol.SSHItemElement {
				return false
			}
			if closedOnly && awaitsHuman(it.Element) {
				return false
			}
			elements--
			return true
		})
	}
	// Output past the session cap: the oldest finished block's files go, its row
	// stays and says so.
	for _, it := range st.Items {
		if st.OutputBytes <= MaxOutputBytes {
			break
		}
		if it.Kind != protocol.SSHItemBlock || it.Block.OutputPruned || !protocol.SSHBlockTerminal(it.Block.Status) {
			continue
		}
		s.removeOutput(it.Block.ID)
		st.OutputBytes -= it.Block.StdoutBytes + it.Block.StderrBytes
		it.Block.OutputPruned = true
		st.Touch(it)
	}
}

func awaitsHuman(e *protocol.SSHElement) bool {
	return e.Outcome == nil && (e.Kind == string(sshelement.KindQuestion) || e.Kind == string(sshelement.KindProposal))
}

// LiveConns counts connections that are not terminal.
func (st *State) LiveConns() []*Conn {
	var out []*Conn
	for _, c := range st.Conns {
		if !c.Terminal() {
			out = append(out, c)
		}
	}
	return out
}

// ConnInfo is a connection as the wire shows it: the stored fields, with the
// counts derived from the blocks.
func (st *State) ConnInfo(c *Conn) protocol.SSHConnInfo {
	info := c.SSHConnInfo
	if c.Grant != nil {
		g := *c.Grant
		info.Grant = &g
	}
	info.PendingApprovals, info.RunningBlocks, info.CommandsRun = 0, 0, 0
	info.AgentRan = false
	for _, it := range st.Items {
		if it.Kind != protocol.SSHItemBlock || it.Block.Conn != c.ID {
			continue
		}
		if it.Block.Actor == protocol.SSHActorAgent {
			switch it.Block.Status {
			case protocol.SSHBlockPendingApproval, protocol.SSHBlockQueued, protocol.SSHBlockDenied, protocol.SSHBlockExpired, protocol.SSHBlockRefused:
			default:
				info.AgentRan = true
			}
		}
		switch it.Block.Status {
		case protocol.SSHBlockPendingApproval:
			info.PendingApprovals++
		case protocol.SSHBlockRunning:
			info.RunningBlocks++
		}
		if !it.Block.StartedAt.IsZero() {
			info.CommandsRun++
		}
	}
	return info
}

// CloneItem copies an item so it can be used outside the lock.
func CloneItem(it *protocol.SSHTimelineItem) protocol.SSHTimelineItem {
	out := *it
	if it.Block != nil {
		b := *it.Block
		out.Block = &b
	}
	if it.Element != nil {
		e := *it.Element
		out.Element = &e
	}
	if it.System != nil {
		v := *it.System
		out.System = &v
	}
	if it.Human != nil {
		h := *it.Human
		out.Human = &h
	}
	return out
}

// Timeline answers cmd=sshTimeline: items changed after after, ordered by Seq so
// paging by LastSeq loses nothing. conn narrows, and session-wide items (Conn
// empty) stay in a narrowed view because they are about the whole investigation.
func (s *Store) Timeline(conn string, after int64, limit int) protocol.SSHTimelineData {
	if limit <= 0 {
		limit = 500
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	st := &s.st
	out := protocol.SSHTimelineData{Session: st.Session, Connections: []protocol.SSHConnInfo{}, Items: []protocol.SSHTimelineItem{}, LastSeq: st.Seq}
	for _, c := range st.Conns {
		out.Connections = append(out.Connections, st.ConnInfo(c))
	}
	var picked []*protocol.SSHTimelineItem
	for _, it := range st.Items {
		if it.Seq <= after {
			continue
		}
		if conn != "" {
			if ic := itemConn(it); ic != "" && ic != conn {
				continue
			}
		}
		picked = append(picked, it)
	}
	slices.SortFunc(picked, func(a, b *protocol.SSHTimelineItem) int {
		switch {
		case a.Seq < b.Seq:
			return -1
		case a.Seq > b.Seq:
			return 1
		}
		return 0
	})
	if len(picked) > limit {
		picked = picked[:limit]
		out.More = true
		out.LastSeq = picked[limit-1].Seq
	}
	for _, it := range picked {
		out.Items = append(out.Items, CloneItem(it))
	}
	return out
}

func itemConn(it *protocol.SSHTimelineItem) string {
	switch {
	case it.Block != nil:
		return it.Block.Conn
	case it.Element != nil:
		return it.Element.Conn
	case it.System != nil:
		return it.System.Conn
	case it.Human != nil:
		return it.Human.Conn
	}
	return ""
}
