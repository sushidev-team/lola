package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sushidev-team/lola/internal/linear"
	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/session"
)

func reportReq(t *testing.T, id string, argv ...string) protocol.Request {
	t.Helper()
	args, err := json.Marshal(protocol.ReportArgs{Argv: argv})
	if err != nil {
		t.Fatal(err)
	}
	return protocol.Request{Cmd: "agentReport", Session: id, Args: args}
}

// A report lands on the board and ships on the wire, and touches NOTHING else:
// the board is the agent's claim about itself, so no axis, gate, freshness stamp
// or rollup may move because of one.
func TestAgentReportIsDisplayOnly(t *testing.T) {
	d := newTestDaemon(t, nativeTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeNative{})
	s := nativeSess("FE-1", "working")
	d.sessions.Upsert(s)
	before, _ := d.sessions.Get(s.ID)

	for _, argv := range [][]string{
		{"todo", "set", "read", "build", "test"},
		{"todo", "done", "1"},
		{"phase", "implementing"},
		{"blocked", "need", "a", "key"},
		{"check", "tests", "pass", "12/12"},
	} {
		if resp := d.handle(context.Background(), reportReq(t, s.ID, argv...)); !resp.OK {
			t.Fatalf("report %q: %s", argv, resp.Error)
		}
	}

	after, _ := d.sessions.Get(s.ID)
	if after.AgentState != before.AgentState || after.Delivery != before.Delivery || after.Status != before.Status ||
		after.AtPrompt != before.AtPrompt || !after.LastActivityAt.Equal(before.LastActivityAt) ||
		!after.AgentStateSince.Equal(before.AgentStateSince) {
		t.Fatalf("a report moved control state:\nbefore %+v\nafter  %+v", before, after)
	}

	var bi *protocol.BoardInfo
	for _, si := range d.sessionsData().Sessions {
		if si.ID == s.ID {
			bi = si.Board
		}
	}
	if bi == nil {
		t.Fatal("board missing from the wire")
	}
	if bi.Phase != "implementing" || bi.Current != "build" || bi.Done != 1 || bi.Total != 3 ||
		!bi.HasProgress || !bi.ProgressDerived || bi.Percent != 33 || bi.Blocked != "need a key" ||
		len(bi.Checks) != 1 || bi.Checks[0].State != "pass" {
		t.Fatalf("board on the wire: %+v", bi)
	}
}

func TestAgentReportRejections(t *testing.T) {
	d := newTestDaemon(t, nativeTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeNative{})
	s := nativeSess("FE-1", "working")
	d.sessions.Upsert(s)

	if resp := d.handle(context.Background(), reportReq(t, s.ID, "phase", "napping")); resp.OK || !strings.Contains(resp.Error, "unknown phase") {
		t.Fatalf("bad phase: %+v", resp)
	}
	if resp := d.handle(context.Background(), reportReq(t, "nope", "phase", "testing")); resp.OK {
		t.Fatal("unknown session accepted")
	}
	if resp := d.handle(context.Background(), reportReq(t, "", "phase", "testing")); resp.OK {
		t.Fatal("sessionless report accepted")
	}
	if got, _ := d.sessions.Get(s.ID); !got.Board.Empty() {
		t.Fatalf("a rejected report wrote the board: %+v", got.Board)
	}
	for _, si := range d.sessionsData().Sessions {
		if si.ID == s.ID && si.Board != nil {
			t.Fatal("an empty board must not ship")
		}
	}
}

func TestReportLimiter(t *testing.T) {
	var l reportLimiter
	now := time.Now()
	for i := range reportBurst {
		if !l.allow("a", now) {
			t.Fatalf("hit %d refused inside the burst", i)
		}
	}
	if l.allow("a", now) {
		t.Fatal("burst not enforced")
	}
	if !l.allow("b", now) {
		t.Fatal("limit leaked across sessions")
	}
	if !l.allow("a", now.Add(reportWindow+time.Second)) {
		t.Fatal("window never slides")
	}
}

// SUSHI-622 acceptance: a session that reports passing tests without having run
// any shows a warning on the wire — and the audit, like the board, moves no
// control state.
func TestClaimAuditFlagsTestsClaimedWithoutARun(t *testing.T) {
	d := newTestDaemon(t, nativeTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeNative{})
	path := filepath.Join(t.TempDir(), "transcript.jsonl")
	// One Bash call, and it is not a test run.
	lines := `{"type":"assistant","timestamp":"2026-10-09T12:00:00Z","message":{"content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"git status"}}]}}
{"type":"user","timestamp":"2026-10-09T12:00:01Z","message":{"content":[{"type":"tool_result","tool_use_id":"t1","is_error":false,"content":"clean"}]}}
`
	if err := os.WriteFile(path, []byte(lines), 0o600); err != nil {
		t.Fatal(err)
	}
	s := nativeSess("FE-2", "working")
	s.TranscriptPath = path
	d.sessions.Upsert(s)
	if resp := d.handle(context.Background(), reportReq(t, s.ID, "check", "tests", "pass", "all", "green")); !resp.OK {
		t.Fatalf("report: %s", resp.Error)
	}
	before, _ := d.sessions.Get(s.ID)

	board := func() *protocol.BoardInfo {
		for _, si := range d.sessionsData().Sessions {
			if si.ID == s.ID {
				return si.Board
			}
		}
		return nil
	}
	// Not yet scanned: nothing is known, so nothing is claimed to be wrong.
	if bi := board(); len(bi.Mismatches) != 0 || bi.Checks[0].Evidence != "" {
		t.Fatalf("unscanned transcript must audit to nothing: %+v", bi)
	}

	scanClaimEvidence(d.sessions.Snapshot())
	bi := board()
	if len(bi.Mismatches) != 1 || !strings.Contains(bi.Mismatches[0], "no test command ran") ||
		bi.Checks[0].Evidence != "unverified" {
		t.Fatalf("claim without a run must be flagged: %+v", bi)
	}
	after, _ := d.sessions.Get(s.ID)
	if after.AgentState != before.AgentState || after.Delivery != before.Delivery || after.Status != before.Status ||
		after.AtPrompt != before.AtPrompt {
		t.Fatalf("the audit moved control state:\nbefore %+v\nafter  %+v", before, after)
	}

	// A codex session's transcript path (none is ever recorded, but be sure)
	// is not read: the audit stays silent rather than guessing.
	d.sessions.Update(s.ID, func(sess *session.Session) bool { sess.Agent = "codex"; return true })
	if bi := board(); len(bi.Mismatches) != 0 {
		t.Fatalf("non-claude session must not be audited from a transcript: %+v", bi)
	}
}

// The limiter never keeps one entry per session id forever: unknown ids are
// refused before it, and idle windows are swept once the map grows.
func TestReportLimiterIsBounded(t *testing.T) {
	var l reportLimiter
	now := time.Now()
	for i := 0; i < reportSweepAt+50; i++ {
		l.allow(fmt.Sprintf("s%d", i), now)
	}
	l.allow("fresh", now.Add(2*reportWindow))
	if len(l.hits) > 2 {
		t.Fatalf("idle sessions not swept: %d entries", len(l.hits))
	}

	d := newTestDaemon(t, testConfig(labelPoll("p1")), &linear.Fake{}, &fakeNative{})
	if r := d.handleAgentReport(protocol.Request{Session: "nope", Args: json.RawMessage(`{"argv":["note","x"]}`)}); r.OK {
		t.Fatal("an unknown session must be refused")
	}
	if len(d.reports.hits) != 0 {
		t.Fatalf("an unknown session got a limiter entry: %v", d.reports.hits)
	}
}
