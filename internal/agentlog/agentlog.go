// Package agentlog reads a coding agent's OWN transcript file to answer the one
// question lola's pane classifier answers most fragilely: is a turn in flight,
// or has the agent yielded?
//
// Why it exists: internal/attention.Classify is the only corroborator of the
// AGENT axis, and it is a MIRROR OF CLAUDE-CODE'S RENDERING. Every cue in it
// carries its own "Fragility:" note, and two of them have already cost a
// debugging session apiece (see CLAUDE.md): the composer caret padded with
// U+00A0 rather than a space, which made ActivityWaiting unreachable and
// silently disabled every send-keys gate; and the composer being drawn at ALL
// times with no "esc to interrupt" in the frame, which made a resting caret stop
// meaning "the turn ended". Both were invisible — a wording change disables a
// load-bearing gate without a single error line.
//
// The transcript is a RENDERING-INDEPENDENT second opinion on the same
// question. Claude Code appends one JSON object per line to a JSONL file and
// hands its path to every lifecycle hook, so lola already records it on
// Session.TranscriptPath. That file's SHAPE — which record types appear, and
// whether the last turn-structure record says a tool was dispatched or the turn
// stopped — is a fact about the agent's own conversation state, not about how
// it happens to paint a terminal this month.
//
// # What is read, and what is not
//
// ONLY structural metadata: a record's "type", the assistant message's
// "stop_reason", the "isSidechain" flag, and the "type" field of each content
// BLOCK. Never a block's text, never a tool's arguments, never a tool's output.
// The file is model output plus tool output — i.e. attacker-influenceable text
// on exactly the same footing as pane text — so nothing read here is rendered,
// logged, executed, or fed to any agent. The only thing that leaves this
// package is a three-valued Verdict.
//
// # Fail toward the status quo
//
// Every unknown is Unknown, and Unknown means "change nothing, use the existing
// pane-driven behavior". An absent path, a missing/unreadable file, a truncated
// record, malformed JSON, a record type this package has never seen, another
// agent's format — all of them return Unknown rather than a confident wrong
// answer. This package can only ever make the daemon MORE right than the pane
// alone; it is never the sole basis for a state change (see the precedence
// documented on internal/daemon.agentReconcile).
//
// # Only claude writes this file
//
// Codex and opencode keep no such transcript, and Session.TranscriptPath is
// populated exclusively from Claude Code's hook payloads. Callers must gate on
// the session's agent kind so a codex/opencode session costs not even a stat;
// that gate lives at the call site because this package is a stdlib-only leaf
// and does not import internal/agent.
package agentlog

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"sync"
	"time"
)

// Verdict is what the transcript says about the agent's turn.
type Verdict int

const (
	// Unknown means the transcript answered nothing: no path, no readable file,
	// no recognizable record in the bounded tail, or a claim too old to still be
	// evidence. It is the SAFE default — callers must fall back to whatever they
	// would have done without a transcript.
	Unknown Verdict = iota
	// Working means a turn is in flight: a tool has been dispatched and its
	// result has not come back, a tool result HAS come back and the model is
	// being called again, a prompt was just submitted, a subagent is running, or
	// — when no record could be parsed — bytes were appended to the file within
	// the last freshWindow.
	Working
	// Idle means the agent yielded the turn: the last turn-structure record is
	// an assistant message that stopped for a reason other than a tool call.
	// Unlike Working this verdict does not expire, because "the turn ended" does
	// not become less true with time; only a WRITE to the file can revise it.
	Idle
)

// String renders the verdict for logs and test failures.
func (v Verdict) String() string {
	switch v {
	case Working:
		return "working"
	case Idle:
		return "idle"
	default:
		return "unknown"
	}
}

const (
	// tailBytes bounds the read. Measured against 400 real transcripts in
	// ~/.claude/projects, the trailing records are p50 1.7KB, p90 12KB, p99 30KB,
	// max 352KB — a single `user` record carrying a large tool_result is what
	// makes the long tail. 64KB therefore contains a COMPLETE final line for
	// well over 99% of files while staying a page-cache-sized read, and the rare
	// file whose last record alone exceeds it degrades exactly as designed: no
	// complete line is parsed, the verdict falls through to the mtime check, and
	// a record that large was by definition just written, so the freshness path
	// answers Working anyway.
	tailBytes = 64 << 10

	// maxRecords bounds how many trailing lines are parsed while looking for the
	// last turn-structure record. The tail is mostly BOOKKEEPING — real files
	// interleave `system`, `attachment`, `last-prompt`, `atis-latch`,
	// `queue-operation`, `file-history-delta`, `file-history-snapshot`, `mode`
	// and `permission-mode` records around the `assistant`/`user` ones — so the
	// scan has to walk past them rather than read "the last record". The cap
	// stops a tail of thousands of tiny bookkeeping lines from costing thousands
	// of json.Unmarshal calls; hitting it yields Unknown, which is free.
	maxRecords = 200

	// freshWindow is how recently the file must have been APPENDED TO for the
	// mtime alone — with no parse at all — to count as evidence a turn is in
	// flight. It is only consulted when the tail parsed to nothing, so it is the
	// last line of defence rather than the main signal. Sized at two observer
	// cycles so an append anywhere in the previous cycle still reads as live;
	// erring long here is cheap, because the caller only ever uses Working to
	// SUSTAIN or promote a working axis that the (equally patient)
	// staleWorkingThreshold guard would otherwise take another cycle to drop.
	freshWindow = 60 * time.Second

	// clockSkewSlack tolerates an mtime slightly in the future (a clock step, a
	// filesystem with coarse timestamps). A file stamped FURTHER ahead than this
	// is nonsense rather than freshness, and reads as not-fresh — i.e. Unknown,
	// which changes nothing.
	clockSkewSlack = 5 * time.Second

	// workingClaimMaxAge expires a parsed Working verdict. A dispatched tool
	// writes NOTHING to the transcript until it returns, so an `assistant`
	// record with stop_reason "tool_use" is the correct answer for as long as
	// that tool runs — which for a full test suite or a container build is
	// legitimately many minutes, and is precisely the case where the pane shows
	// nothing recognizable and the staleWorkingThreshold guard used to give up.
	// But it is also the shape left behind FOREVER by an agent that died mid-tool
	// or is sitting on an unanswered permission prompt, so the claim cannot be
	// eternal: past this age the transcript stops asserting anything and the
	// pane-driven behavior takes over again. Generous on purpose — a false
	// "still working" for half an hour costs a delayed status, while a false
	// "idle" on a session mid-build costs a wrong reaction.
	workingClaimMaxAge = 30 * time.Minute

	// maxCacheEntries caps the per-path cache. Overflow clears it wholesale
	// rather than evicting precisely: an entry is a stat stamp plus an int, the
	// live population is a handful of sessions, and a cache miss costs one
	// bounded read — so exact eviction would buy nothing but code.
	maxCacheEntries = 512
)

// Reader answers Verdict for transcript paths, remembering the stat stamp it
// last parsed so a file that has not changed costs ONE stat and no read at all.
// That is the whole cost model: an idle session stats forever and never reads;
// an actively working one reads at most tailBytes per observe cycle; a session
// blocked in a long tool call reverts to stat-only the moment its file goes
// quiet. Safe for concurrent use.
type Reader struct {
	mu    sync.Mutex
	cache map[string]entry
}

// entry caches the PARSED verdict for one (path, size, mtime). Freshness is
// deliberately NOT cached: it decays, so it is re-derived from the stat on
// every call.
type entry struct {
	size int64
	mod  time.Time
	v    Verdict
}

// NewReader returns an empty Reader.
func NewReader() *Reader { return &Reader{cache: map[string]entry{}} }

// Reset drops the cache. Only tests need it — a stale entry is otherwise
// impossible, since every entry is keyed by the file's own size and mtime.
func (r *Reader) Reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cache = map[string]entry{}
}

// Len reports how many transcripts the reader is tracking. It exists so a
// caller's "this session must not touch a file at all" gate — the agent-kind
// check every caller owes this package, and the dead-pane check the observer
// adds — can be PROVEN rather than assumed: a session that was correctly
// skipped leaves no entry behind. A stat that failed leaves none either
// (Verdict forgets the path), so a test comparing kinds must point both at a
// file that really exists.
func (r *Reader) Len() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.cache)
}

// Verdict reports what the transcript at path says about the agent's turn at
// time now. It performs exactly one os.Stat, and reads at most tailBytes — only
// when the file's size or mtime differs from the last read.
//
// The caller must have established that this session's agent actually writes a
// transcript (claude) and that its pane is still alive; Verdict itself does not
// know either fact. An empty path is free.
func (r *Reader) Verdict(path string, now time.Time) Verdict {
	if path == "" {
		return Unknown
	}
	fi, err := os.Stat(path)
	if err != nil || !fi.Mode().IsRegular() || fi.Size() == 0 {
		// No file, a directory, a device, an empty file: the agent has not
		// written a transcript we can read. Drop any cached answer — it
		// described a file that is no longer there.
		r.forget(path)
		return Unknown
	}

	size, mod := fi.Size(), fi.ModTime()
	parsed, cached := r.lookup(path, size, mod)
	if !cached {
		parsed = parseTail(path, size)
		r.store(path, size, mod, parsed)
	}

	age := now.Sub(mod)
	switch parsed {
	case Working:
		if age > workingClaimMaxAge {
			return Unknown // the claim has outlived its evidence
		}
		return Working
	case Idle:
		return Idle
	}

	// The tail said nothing (a single record larger than the window, a shape
	// this package does not recognize, an unparsable line). The stat is still a
	// fact, and it is the one signal with no text parsing in it whatsoever:
	// bytes appended a moment ago mean SOMETHING is happening in there.
	if age <= freshWindow && age >= -clockSkewSlack {
		return Working
	}
	return Unknown
}

func (r *Reader) lookup(path string, size int64, mod time.Time) (Verdict, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.cache[path]
	if !ok || e.size != size || !e.mod.Equal(mod) {
		return Unknown, false
	}
	return e.v, true
}

func (r *Reader) store(path string, size int64, mod time.Time, v Verdict) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cache == nil || len(r.cache) >= maxCacheEntries {
		r.cache = map[string]entry{}
	}
	r.cache[path] = entry{size: size, mod: mod, v: v}
}

func (r *Reader) forget(path string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.cache, path)
}

// record is the ONLY shape decoded out of a transcript line: structural
// metadata and nothing else. Content is kept as RawMessage so a block's text,
// a tool's arguments and a tool's output are never materialized — only the
// "type" of each block is ever looked at, and only when stop_reason is absent.
type record struct {
	Type        string `json:"type"`
	IsSidechain bool   `json:"isSidechain"`
	Message     struct {
		// StopReason is the API's own turn-boundary marker and the primary
		// discriminator. A JSON null decodes to "" without error, which is
		// exactly the "fall through to the content blocks" case below.
		StopReason string          `json:"stop_reason"`
		Content    json.RawMessage `json:"content"`
	} `json:"message"`
}

// parseTail reads the last tailBytes of path and walks its COMPLETE lines
// backward, returning the first recognizable turn-structure verdict. Anything
// it cannot answer is Unknown.
func parseTail(path string, size int64) Verdict {
	f, err := os.Open(path)
	if err != nil {
		return Unknown
	}
	defer f.Close()

	n := int64(tailBytes)
	if size < n {
		n = size
	}
	off := size - n
	buf := make([]byte, n)
	// ReadAt is the right call rather than Seek+Read: it reports a short read as
	// an error, so a file truncated between the stat and the read cannot be
	// parsed as if the remaining bytes were the tail.
	got, err := f.ReadAt(buf, off)
	if err != nil && err != io.EOF {
		return Unknown
	}
	b := buf[:got]

	// Discard the leading fragment: unless the window covers the whole file, it
	// starts mid-record. Discard the trailing fragment too — a large record is
	// not written atomically, so the bytes after the last newline may be half a
	// line the agent is still flushing.
	if off > 0 {
		i := bytes.IndexByte(b, '\n')
		if i < 0 {
			return Unknown // one record larger than the whole window
		}
		b = b[i+1:]
	}
	i := bytes.LastIndexByte(b, '\n')
	if i < 0 {
		return Unknown
	}
	b = b[:i]

	lines := bytes.Split(b, []byte{'\n'})
	scanned := 0
	for j := len(lines) - 1; j >= 0 && scanned < maxRecords; j-- {
		line := bytes.TrimSpace(lines[j])
		if len(line) == 0 {
			continue
		}
		scanned++
		var rec record
		if json.Unmarshal(line, &rec) != nil {
			continue // another program's file: a line we cannot read is not a fact
		}
		if v := classify(rec); v != Unknown {
			return v
		}
	}
	return Unknown
}

// classify maps ONE record to a verdict. Unknown means "this record says
// nothing about the turn" — the caller keeps walking backward, which is what
// lets the scan step over the bookkeeping records interleaved into the tail.
func classify(rec record) Verdict {
	switch rec.Type {
	case "assistant":
		// A SIDECHAIN record belongs to a subagent, and a subagent only ever
		// runs inside a parent turn — so its own stop_reason says nothing about
		// whether the session is busy, and reading a finished subagent's
		// end_turn as "the agent yielded" would be a false idle in the middle of
		// real work.
		if rec.IsSidechain {
			return Working
		}
		switch rec.Message.StopReason {
		case "tool_use":
			// A tool was dispatched and its result has not come back. This is the
			// verdict that carries the whole feature: it stays true for the entire
			// duration of a long build, with nothing appended to the file and
			// nothing recognizable on screen.
			return Working
		case "end_turn", "stop_sequence", "max_tokens", "refusal":
			return Idle
		}
		// stop_reason absent (a partial record — measured at ~14% of assistant
		// records, none of them ever last in a file) or a value this package does
		// not know. A dispatched tool is still unambiguous from the blocks; a
		// text/thinking-only record is not, because a response is split across
		// one record PER content block and a thinking block is followed by more
		// of the same response. So: only the positive case answers here.
		if hasToolUseBlock(rec.Message.Content) {
			return Working
		}
		return Unknown
	case "user":
		// Either a tool_result coming back or a human prompt going in. Both mean
		// the model is the next to speak, so a turn is in flight.
		return Working
	}
	// Everything else is bookkeeping (system, attachment, last-prompt,
	// atis-latch, queue-operation, file-history-*, mode, permission-mode,
	// summary, …) or a record type from a future build. Neither is evidence.
	return Unknown
}

// hasToolUseBlock reports whether the message content array carries a tool_use
// block. Only each block's "type" is decoded — never its input, name or text.
// A content field that is a bare string (the older single-text shape) or
// anything else simply fails to decode and reports false.
func hasToolUseBlock(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return false
	}
	var blocks []struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(raw, &blocks) != nil {
		return false
	}
	for _, b := range blocks {
		if b.Type == "tool_use" {
			return true
		}
	}
	return false
}
