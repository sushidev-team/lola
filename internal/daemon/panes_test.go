package daemon

import (
	"strings"
	"testing"

	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/session"
)

// shellIndex is anchored at BOTH ends, for the reason the teardown invariant
// gives: "lola-fe-42" is a prefix of "lola-fe-420-shell-1", so a loose prefix
// test would claim another session's tab as this one's — and the tab strip
// would offer a pane belonging to a different worktree.
func TestShellIndex(t *testing.T) {
	cases := []struct {
		parent, name string
		want         int
	}{
		{"lola-fe-42", "lola-fe-42-shell-1", 1},
		{"lola-fe-42", "lola-fe-42-shell-11", 11},
		{"lola-fe-42", "lola-fe-42", 0},
		{"lola-fe-42", "lola-fe-42-review", 0},
		{"lola-fe-42", "lola-fe-42-dev-1", 0},
		// The one that matters: a DIFFERENT session whose id starts the same.
		{"lola-fe-42", "lola-fe-420-shell-1", 0},
		{"lola-fe-42", "lola-fe-42-shell-", 0},
		{"lola-fe-42", "lola-fe-42-shell-x", 0},
		{"lola-fe-42", "lola-fe-42-shell-0", 0},
		{"lola-fe-42", "lola-fe-42-shell--1", 0},
	}
	for _, tc := range cases {
		if got := shellIndex(tc.parent, tc.name); got != tc.want {
			t.Errorf("shellIndex(%q, %q) = %d, want %d", tc.parent, tc.name, got, tc.want)
		}
	}
}

// The strip is drawn in the order this returns, so the order is a contract:
// agent first, then shells and dev tabs by index, then review.
func TestSortByIndexOrdersTabs(t *testing.T) {
	p := []protocol.PaneInfo{{Index: 3}, {Index: 1}, {Index: 2}}
	sortByIndex(p)
	for i, want := range []int{1, 2, 3} {
		if p[i].Index != want {
			t.Fatalf("order = %v, want ascending", p)
		}
	}
}

// An unknown session is an error rather than an empty strip: a client asking
// about a session that does not exist has a bug, and answering "no panes"
// would hide it behind a plausible-looking empty tab bar.
func TestPanesRefusesAnUnknownSession(t *testing.T) {
	d := newRemoteTestDaemon(t)
	if _, err := d.handlePanes(t.Context(), "nope"); err == nil {
		t.Fatal("an unknown session was accepted")
	}
	if _, err := d.handleShellCreate(t.Context(), "nope"); err == nil {
		t.Fatal("a shell was created for an unknown session")
	}
}

// A session whose worktree is gone has nowhere to root a shell. Refusing names
// the reason; the alternative is a shell in whatever directory the daemon
// happens to be in, which is the operator's own repository.
func TestShellCreateRefusesWithoutAWorktree(t *testing.T) {
	d := newRemoteTestDaemon(t)
	s := session.Session{ID: "acc-1", Source: "native", TmuxName: "acc-1", Worktree: ""}
	d.sessions.Upsert(s)

	_, err := d.handleShellCreate(t.Context(), "acc-1")
	if err == nil {
		t.Fatal("a shell was created for a session with no worktree")
	}
	if got := err.Error(); got == "" {
		t.Error("the refusal said nothing")
	}
}

// And one whose worktree no longer exists on disk, which is the same failure
// arriving a different way — the record outlives the checkout.
func TestShellCreateRefusesAMissingWorktree(t *testing.T) {
	d := newRemoteTestDaemon(t)
	s := session.Session{ID: "acc-2", Source: "native", TmuxName: "acc-2", Worktree: t.TempDir() + "/gone"}
	d.sessions.Upsert(s)

	if _, err := d.handleShellCreate(t.Context(), "acc-2"); err == nil {
		t.Fatal("a shell was created in a worktree that does not exist")
	}
}

// The agent pane IS the session. Closing it would end the work and leave a
// record pointing at nothing, so a tab strip must not be able to offer it as a
// tidy-up — that is cmd=kill, which takes the worktree and branch too.
func TestPaneCloseRefusesTheAgentPane(t *testing.T) {
	d := newRemoteTestDaemon(t)
	d.sessions.Upsert(session.Session{ID: "acc-3", Source: "native", TmuxName: "acc-3"})

	_, err := d.handlePaneClose(t.Context(), protocol.PaneCloseArgs{Session: "acc-3", Pane: "acc-3"})
	if err == nil {
		t.Fatal("the agent pane was closed")
	}
	if !strings.Contains(err.Error(), "kill") {
		t.Errorf("the refusal should point at the command that does end a session, got %q", err)
	}
}

// One session must not be able to close another's tab by naming it. The
// subscribe path already has an identity gate; this path would reopen the hole.
func TestPaneCloseRefusesAnotherSessionsPane(t *testing.T) {
	d := newRemoteTestDaemon(t)
	d.sessions.Upsert(session.Session{ID: "lola-fe-42", Source: "native", TmuxName: "lola-fe-42"})

	// The prefix case that motivates the anchoring: lola-fe-42 is a prefix of
	// lola-fe-420, so a bare prefix test would accept this.
	_, err := d.handlePaneClose(t.Context(), protocol.PaneCloseArgs{
		Session: "lola-fe-42", Pane: "lola-fe-420-shell-1",
	})
	if err == nil {
		t.Fatal("a pane belonging to another session was closed")
	}
}

func TestPaneBelongsTo(t *testing.T) {
	d := newRemoteTestDaemon(t)
	cases := []struct {
		pane string
		want bool
	}{
		{"lola-fe-42-shell-1", true},
		{"lola-fe-42-dev-2", true},
		{"lola-fe-42-review", true},
		{"lola-fe-42", false},          // the agent pane is not auxiliary
		{"lola-fe-420-shell-1", false}, // a different session
		{"lola-fe-42-shell-x", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := d.paneBelongsTo("lola-fe-42", tc.pane); got != tc.want {
			t.Errorf("paneBelongsTo(%q) = %v, want %v", tc.pane, got, tc.want)
		}
	}
}

// A resize is bounded. A window told it is 10000 columns wide is a wedged agent
// rather than a wide one, since the TUI redraws against whatever it is given.
func TestPaneResizeRejectsAbsurdDimensions(t *testing.T) {
	d := newRemoteTestDaemon(t)
	d.sessions.Upsert(session.Session{ID: "acc-4", Source: "native", TmuxName: "acc-4"})

	_, err := d.handlePaneResize(t.Context(), protocol.PaneResizeArgs{
		Session: "acc-4", Pane: "acc-4", Cols: 10000, Rows: 40,
	})
	if err == nil {
		t.Fatal("an out-of-range size was accepted")
	}
}

// The agent pane IS resizable — that is the entire point of the feature, and it
// is the one place close and resize deliberately disagree.
func TestPaneResizeAllowsTheAgentPane(t *testing.T) {
	d := newRemoteTestDaemon(t)
	d.sessions.Upsert(session.Session{ID: "acc-5", Source: "native", TmuxName: "acc-5"})

	// Reaching tmux is expected to fail in a test environment; what matters is
	// that it was not refused on identity grounds before getting there.
	_, err := d.handlePaneResize(t.Context(), protocol.PaneResizeArgs{
		Session: "acc-5", Pane: "acc-5", Cols: 55, Rows: 34,
	})
	if err != nil && strings.Contains(err.Error(), "does not belong") {
		t.Fatalf("the agent pane was refused as foreign: %v", err)
	}
}
