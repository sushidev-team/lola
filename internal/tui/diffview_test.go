package tui

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/sushidev-team/lola/internal/protocol"
)

var cannedDiff = protocol.DiffData{
	Session: "s1", Base: "origin/main", MergeBase: "abc",
	Files: []protocol.DiffFile{{
		Path: "internal/foo/foo.go", Status: "modified", Additions: 1, Deletions: 1,
		Patch: "@@ -1,3 +1,3 @@\n package foo\n-var x = 1\n+var x = 2\n // end\n",
	}},
}

// openTestDiff selects s1 and opens the overlay, feeding back the canned diff.
func openTestDiff(t *testing.T) *rootModel {
	t.Helper()
	m := newTestRoot(t)
	m.Update(sessionsMsg{data: cannedSessions()})
	for i, s := range m.sessions.data.Sessions {
		if s.ID == "s1" {
			m.sessions.cursor = i
		}
	}
	_, cmd := m.Update(keyMsg("f"))
	if !m.diff.open || cmd == nil {
		t.Fatalf("f must open the diff overlay and fetch (open=%v)", m.diff.open)
	}
	m.Update(diffLoadedMsg{session: "s1", data: &cannedDiff})
	return m
}

func TestDiffOverlayRendersTheChanges(t *testing.T) {
	m := openTestDiff(t)
	v := m.viewString()
	for _, want := range []string{"against origin/main", "internal/foo/foo.go", "-var x = 1", "+var x = 2"} {
		if !strings.Contains(v, want) {
			t.Errorf("diff view missing %q:\n%q", want, v)
		}
	}
	m.Update(keyMsg("esc"))
	if m.diff.open {
		t.Error("esc must close the overlay")
	}
}

// The acceptance path in the TUI: move to a line, type a comment, send — the
// daemon gets path:line context.
func TestDiffOverlaySendsALineComment(t *testing.T) {
	m := openTestDiff(t)
	// rows: header, hunk, ctx(1), del(old 2), add(new 2), ctx
	for range 4 {
		m.Update(keyMsg("j"))
	}
	m.Update(keyMsg("c"))
	for _, r := range "why 2?" {
		m.Update(keyMsg(string(r)))
	}
	m.Update(keyMsg("enter"))
	if n := len(m.diff.drafts["s1"]); n != 1 {
		t.Fatalf("want one queued comment, got %d", n)
	}

	var reqs []protocol.Request
	fakeRequest(t, &reqs, mustData(t, protocol.FeedbackData{Delivered: true}), nil)
	_, cmd := m.Update(keyMsg("s"))
	if cmd == nil {
		t.Fatal("s must send")
	}
	m.Update(cmd())
	if len(reqs) != 1 || reqs[0].Cmd != "feedback" {
		t.Fatalf("requests = %+v", reqs)
	}
	var args protocol.FeedbackArgs
	if err := json.Unmarshal(reqs[0].Args, &args); err != nil {
		t.Fatal(err)
	}
	c := args.Comments[0]
	if args.Session != "s1" || c.Path != "internal/foo/foo.go" || c.Line != 2 || c.Side != "new" || c.Body != "why 2?" || c.Quote != "var x = 2" {
		t.Errorf("args = %+v", args)
	}
	if len(m.diff.drafts["s1"]) != 0 {
		t.Error("an accepted batch must clear the drafts")
	}
}

// A note alone is enough to send, and a refusal keeps it.
func TestDiffOverlayKeepsDraftsOnRefusal(t *testing.T) {
	m := openTestDiff(t)
	m.Update(keyMsg("n"))
	for _, r := range "add tests" {
		m.Update(keyMsg(string(r)))
	}
	m.Update(keyMsg("enter"))
	fakeRequest(t, nil, &protocol.Response{OK: false, Error: "unknown session"}, nil)
	_, cmd := m.Update(keyMsg("s"))
	m.Update(cmd())
	if m.diff.notes["s1"] != "add tests" {
		t.Errorf("a refused send must keep the note, got %q", m.diff.notes["s1"])
	}
	if !strings.Contains(m.diff.flash, "unknown session") {
		t.Errorf("flash = %q", m.diff.flash)
	}
}

func TestDiffOverlayRefusesCommentOnAHeader(t *testing.T) {
	m := openTestDiff(t)
	m.Update(keyMsg("c")) // cursor is on the file header
	if m.diff.input != diffInputNone {
		t.Error("a file header is not a commentable line")
	}
}
