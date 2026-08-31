package daemon

import (
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
