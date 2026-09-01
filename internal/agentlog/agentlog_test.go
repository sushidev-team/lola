package agentlog

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// --- fixtures ---------------------------------------------------------------
//
// Every line below is the real shape claude-code writes, trimmed to the fields
// this package decodes. The bookkeeping types are verbatim from live
// transcripts in ~/.claude/projects — they are the reason the scan has to walk
// BACKWARD past records rather than read "the last line".

const (
	recToolUse   = `{"type":"assistant","isSidechain":false,"message":{"role":"assistant","stop_reason":"tool_use","content":[{"type":"tool_use","name":"Bash","input":{"command":"make test"}}]}}`
	recEndTurn   = `{"type":"assistant","isSidechain":false,"message":{"role":"assistant","stop_reason":"end_turn","content":[{"type":"text","text":"Done."}]}}`
	recStopSeq   = `{"type":"assistant","isSidechain":false,"message":{"role":"assistant","stop_reason":"stop_sequence","content":[{"type":"text","text":"Done."}]}}`
	recToolRes   = `{"type":"user","isSidechain":false,"message":{"role":"user","content":[{"type":"tool_result","content":"ok"}]}}`
	recPrompt    = `{"type":"user","isSidechain":false,"message":{"role":"user","content":"please fix the build"}}`
	recSideEnd   = `{"type":"assistant","isSidechain":true,"message":{"role":"assistant","stop_reason":"end_turn","content":[{"type":"text","text":"subagent done"}]}}`
	recNoStopTU  = `{"type":"assistant","isSidechain":false,"message":{"role":"assistant","stop_reason":null,"content":[{"type":"tool_use","name":"Read"}]}}`
	recNoStopThk = `{"type":"assistant","isSidechain":false,"message":{"role":"assistant","stop_reason":null,"content":[{"type":"thinking","thinking":"hmm"}]}}`

	// Bookkeeping. None of these is evidence about the turn.
	recSystem   = `{"type":"system","subtype":"hook","content":"PostToolUse"}`
	recLastProm = `{"type":"last-prompt","lastPrompt":"go on","leafUuid":"x"}`
	recQueueOp  = `{"type":"queue-operation","op":"add"}`
	recAttach   = `{"type":"attachment","attachment":{"type":"file"}}`
	recFileHist = `{"type":"file-history-snapshot","messageId":"x"}`
	recFuture   = `{"type":"some-future-record-type","payload":42}`
)

// write drops lines into a fresh transcript and stamps its mtime, so age is a
// test input rather than a race with the clock.
func write(t *testing.T, name string, age time.Duration, lines ...string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	body := ""
	for _, l := range lines {
		body += l + "\n"
	}
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatalf("write transcript: %v", err)
	}
	mod := time.Now().Add(-age)
	if err := os.Chtimes(p, mod, mod); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
	return p
}

func verdict(t *testing.T, path string) Verdict {
	t.Helper()
	return NewReader().Verdict(path, time.Now())
}

// --- the turn-structure verdicts --------------------------------------------

// The headline case: a dispatched tool writes NOTHING further until it returns,
// so an hours-long build looks identical to a dead session on the pane. The
// transcript still says a tool is in flight — for as long as the claim is
// allowed to stand (see TestWorkingClaimExpires).
func TestToolUseIsWorking(t *testing.T) {
	if got := verdict(t, write(t, "a.jsonl", 5*time.Minute, recPrompt, recToolUse)); got != Working {
		t.Fatalf("verdict = %v, want working (a tool is dispatched and has not returned)", got)
	}
}

// The other half: the model stopped for a reason that is not a tool call. This
// is the fact the always-drawn composer can no longer establish from the pane.
func TestEndTurnIsIdle(t *testing.T) {
	for _, rec := range []struct {
		name string
		line string
	}{
		{"end_turn", recEndTurn},
		{"stop_sequence", recStopSeq},
	} {
		t.Run(rec.name, func(t *testing.T) {
			if got := verdict(t, write(t, "a.jsonl", 5*time.Minute, recToolUse, recToolRes, rec.line)); got != Idle {
				t.Fatalf("verdict = %v, want idle", got)
			}
		})
	}
}

// A user record is either a returned tool_result or a submitted prompt. Either
// way the model is next to speak.
func TestUserRecordIsWorking(t *testing.T) {
	for name, line := range map[string]string{"tool_result": recToolRes, "prompt": recPrompt} {
		t.Run(name, func(t *testing.T) {
			if got := verdict(t, write(t, "a.jsonl", 5*time.Minute, recEndTurn, line)); got != Working {
				t.Fatalf("verdict = %v, want working", got)
			}
		})
	}
}

// A SIDECHAIN record belongs to a subagent, which only ever runs inside a
// parent turn. Reading its end_turn as "the agent yielded" would be a false
// idle in the middle of real work — and Task/Explore subagents write plenty of
// them.
func TestSidechainEndTurnIsWorking(t *testing.T) {
	if got := verdict(t, write(t, "a.jsonl", 5*time.Minute, recToolUse, recSideEnd)); got != Working {
		t.Fatalf("verdict = %v, want working (a subagent finishing is not the turn ending)", got)
	}
}

// ~14% of real assistant records carry a null stop_reason (measured over 400
// transcripts). A dispatched tool is still unambiguous from the content blocks;
// a thinking-only record is NOT, because one response is split across a record
// per block, so it must fall through and let the scan keep walking back.
func TestMissingStopReason(t *testing.T) {
	t.Run("tool_use block still answers", func(t *testing.T) {
		if got := verdict(t, write(t, "a.jsonl", 5*time.Minute, recEndTurn, recNoStopTU)); got != Working {
			t.Fatalf("verdict = %v, want working", got)
		}
	})
	t.Run("thinking-only defers to the record behind it", func(t *testing.T) {
		if got := verdict(t, write(t, "a.jsonl", 5*time.Minute, recToolRes, recNoStopThk)); got != Working {
			t.Fatalf("verdict = %v, want working (the tool_result behind it is the answer)", got)
		}
	})
}

// The scan must step over the bookkeeping records claude-code interleaves. Real
// files routinely END on one of these, so "read the last record" would answer
// Unknown for a session that has plainly finished its turn.
func TestScanWalksPastBookkeeping(t *testing.T) {
	p := write(t, "a.jsonl", 5*time.Minute,
		recToolUse, recToolRes, recEndTurn,
		recSystem, recAttach, recFileHist, recLastProm, recQueueOp, recFuture)
	if got := verdict(t, p); got != Idle {
		t.Fatalf("verdict = %v, want idle (bookkeeping is not evidence)", got)
	}
}

// --- fail-toward-unknown ----------------------------------------------------

// Everything unreadable must change nothing. These are the cases that keep a
// broken/absent/foreign transcript from ever producing a confident wrong answer.
func TestUnreadableIsUnknown(t *testing.T) {
	dir := t.TempDir()
	empty := filepath.Join(dir, "empty.jsonl")
	if err := os.WriteFile(empty, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	cases := map[string]string{
		"no path":      "",
		"missing file": filepath.Join(dir, "nope.jsonl"),
		"a directory":  dir,
		"empty file":   empty,
	}
	for name, path := range cases {
		t.Run(name, func(t *testing.T) {
			if got := verdict(t, path); got != Unknown {
				t.Fatalf("verdict = %v, want unknown", got)
			}
		})
	}
}

// Garbage, another program's format, and record types from a future build are
// all "not evidence" — but only once the file has stopped being written to,
// since a file being appended to right now is evidence in itself (see
// TestFreshMtimeRescuesAnUnparsableTail).
func TestGarbageWithQuietFileIsUnknown(t *testing.T) {
	cases := map[string][]string{
		"malformed json": {`{"type":"assistant"`, `not json at all`},
		"unknown types":  {recFuture, recSystem, recQueueOp},
		"foreign jsonl":  {`{"level":"info","msg":"hello"}`, `{"level":"warn","msg":"bye"}`},
	}
	for name, lines := range cases {
		t.Run(name, func(t *testing.T) {
			if got := verdict(t, write(t, "a.jsonl", time.Hour, lines...)); got != Unknown {
				t.Fatalf("verdict = %v, want unknown", got)
			}
		})
	}
}

// A record larger than the whole read window leaves no complete line to parse.
// The file is quiet, so nothing rescues it and the answer is Unknown.
func TestOversizedRecordIsUnknown(t *testing.T) {
	huge := `{"type":"user","message":{"role":"user","content":[{"type":"tool_result","content":"` +
		strings.Repeat("x", tailBytes+4096) + `"}]}}`
	if got := verdict(t, write(t, "a.jsonl", time.Hour, recEndTurn, huge)); got != Unknown {
		t.Fatalf("verdict = %v, want unknown (no complete line inside the window)", got)
	}
}

// A record still being flushed has no terminating newline. Parsing that
// fragment would decode half an object; it must be discarded and the complete
// record behind it used instead.
func TestTrailingPartialLineIsDiscarded(t *testing.T) {
	p := filepath.Join(t.TempDir(), "a.jsonl")
	body := recToolUse + "\n" + recEndTurn + "\n" + `{"type":"user","messa`
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	mod := time.Now().Add(-time.Hour)
	if err := os.Chtimes(p, mod, mod); err != nil {
		t.Fatal(err)
	}
	if got := verdict(t, p); got != Idle {
		t.Fatalf("verdict = %v, want idle (the half-written line is not a record)", got)
	}
}

// --- the mtime half ---------------------------------------------------------

// The last line of defence, and the only signal with no text parsing in it at
// all: bytes appended a moment ago mean something is happening, whatever the
// tail turned out to be.
func TestFreshMtimeRescuesAnUnparsableTail(t *testing.T) {
	p := write(t, "a.jsonl", 2*time.Second, `garbage`, `{"type":"whatever"}`)
	if got := verdict(t, p); got != Working {
		t.Fatalf("verdict = %v, want working (the file was just appended to)", got)
	}
}

// Freshness must never override a PARSED answer: the final assistant record IS
// the write that made the file fresh, so a just-finished turn is idle, not
// working.
func TestFreshMtimeDoesNotOverrideAParsedIdle(t *testing.T) {
	if got := verdict(t, write(t, "a.jsonl", time.Second, recToolRes, recEndTurn)); got != Idle {
		t.Fatalf("verdict = %v, want idle (the end_turn record is what made it fresh)", got)
	}
}

// An mtime far in the FUTURE is nonsense, not freshness (a clock step, a
// coarse-timestamp filesystem). It must read as not-fresh — i.e. change nothing.
func TestFutureMtimeIsNotFreshness(t *testing.T) {
	if got := verdict(t, write(t, "a.jsonl", -2*time.Hour, `garbage`)); got != Unknown {
		t.Fatalf("verdict = %v, want unknown (a future mtime is not evidence)", got)
	}
}

// A tool_use record is the shape an agent that DIED mid-tool — or one sitting
// on an unanswered permission prompt — leaves behind forever. The claim has to
// expire, or such a session would assert "working" for the rest of its life.
func TestWorkingClaimExpires(t *testing.T) {
	p := write(t, "a.jsonl", workingClaimMaxAge+time.Minute, recPrompt, recToolUse)
	if got := verdict(t, p); got != Unknown {
		t.Fatalf("verdict = %v, want unknown (the claim outlived its evidence)", got)
	}
	// Just inside the window it still stands — a long build is the case this
	// generosity exists for.
	fresh := write(t, "b.jsonl", workingClaimMaxAge-time.Minute, recPrompt, recToolUse)
	if got := verdict(t, fresh); got != Working {
		t.Fatalf("verdict = %v, want working (a long tool call is not a dead agent)", got)
	}
}

// Idle deliberately does NOT expire: "the turn ended" does not become less true
// with time, and only a write to the file can revise it.
func TestIdleDoesNotExpire(t *testing.T) {
	if got := verdict(t, write(t, "a.jsonl", 30*24*time.Hour, recEndTurn)); got != Idle {
		t.Fatalf("verdict = %v, want idle even after a month", got)
	}
}

// --- cost model -------------------------------------------------------------

// The cache is the whole cheapness claim: a file that has not changed costs one
// stat and NO read. Proven by rewriting the body underneath the reader while
// restoring the exact size and mtime — the cached verdict must stand, which is
// only possible if the bytes were not re-read.
func TestUnchangedStatSkipsTheRead(t *testing.T) {
	p := write(t, "a.jsonl", 5*time.Minute, recPrompt, recToolUse)
	r := NewReader()
	if got := r.Verdict(p, time.Now()); got != Working {
		t.Fatalf("first verdict = %v, want working", got)
	}

	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	// A body that parses to Idle, padded to the byte to keep the stat identical.
	body := []byte(recEndTurn + "\n")
	for len(body) < int(fi.Size()) {
		body = append(body, ' ')
	}
	if err := os.WriteFile(p, body[:fi.Size()], 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(p, fi.ModTime(), fi.ModTime()); err != nil {
		t.Fatal(err)
	}

	if got := r.Verdict(p, time.Now()); got != Working {
		t.Fatalf("verdict = %v, want the cached working — an unchanged stat must not re-read", got)
	}
	// ...and a changed stat must.
	r.Reset()
	if got := r.Verdict(p, time.Now()); got != Idle {
		t.Fatalf("verdict after reset = %v, want idle (the file really does say end_turn now)", got)
	}
}

// A file that GREW is re-read: the cache keys on size and mtime precisely so a
// turn starting after an idle spell is noticed on the next cycle.
func TestGrowthInvalidatesTheCache(t *testing.T) {
	p := write(t, "a.jsonl", 5*time.Minute, recEndTurn)
	r := NewReader()
	if got := r.Verdict(p, time.Now()); got != Idle {
		t.Fatalf("first verdict = %v, want idle", got)
	}
	f, err := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(recPrompt + "\n" + recToolUse + "\n"); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if got := r.Verdict(p, time.Now()); got != Working {
		t.Fatalf("verdict = %v, want working (the file grew: a new turn started)", got)
	}
}

// A vanished file must not leave its verdict behind — the session it described
// is gone.
func TestRemovedFileForgetsItsVerdict(t *testing.T) {
	p := write(t, "a.jsonl", 5*time.Minute, recToolUse)
	r := NewReader()
	if got := r.Verdict(p, time.Now()); got != Working {
		t.Fatalf("first verdict = %v, want working", got)
	}
	if err := os.Remove(p); err != nil {
		t.Fatal(err)
	}
	if got := r.Verdict(p, time.Now()); got != Unknown {
		t.Fatalf("verdict = %v, want unknown after the file went away", got)
	}
}

// The map is bounded. Overflow clears it wholesale, which costs one bounded
// re-read and nothing else; what must NOT happen is unbounded growth.
func TestCacheIsBounded(t *testing.T) {
	dir := t.TempDir()
	r := NewReader()
	for i := 0; i < maxCacheEntries+10; i++ {
		p := filepath.Join(dir, string(rune('a'+i%26))+strings.Repeat("x", i/26)+".jsonl")
		if err := os.WriteFile(p, []byte(recToolUse+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		r.Verdict(p, time.Now())
	}
	r.mu.Lock()
	n := len(r.cache)
	r.mu.Unlock()
	if n > maxCacheEntries {
		t.Fatalf("cache holds %d entries, want at most %d", n, maxCacheEntries)
	}
}

func TestVerdictString(t *testing.T) {
	for v, want := range map[Verdict]string{Unknown: "unknown", Working: "working", Idle: "idle"} {
		if got := v.String(); got != want {
			t.Fatalf("Verdict(%d).String() = %q, want %q", v, got, want)
		}
	}
}
