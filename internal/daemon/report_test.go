package daemon

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/sushidev-team/lola/internal/linear"
	"github.com/sushidev-team/lola/internal/protocol"
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
