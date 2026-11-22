package daemon

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/linear"
	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/runtime"
	"github.com/sushidev-team/lola/internal/session"
	"github.com/sushidev-team/lola/internal/usage"
)

func TestAgentTabsShareCheckoutAndCloseIndependently(t *testing.T) {
	nat := &fakeNative{}
	d := newTestDaemon(t, nativeTestConfig(nativePoll("p1")), &linear.Fake{}, nat)
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	owner := session.Session{ID: "original", Project: "p1", Source: "native", Kind: session.KindPR, Agent: "codex", Worktree: dir}
	d.sessions.Upsert(owner)
	args := protocol.OpenManualArgs{ParentSession: owner.ID, Location: runtime.LocationAgentTab}
	ctx := context.Background()
	first, err := d.handleOpenManual(ctx, args)
	if err != nil {
		t.Fatal(err)
	}
	second, err := d.handleOpenManual(ctx, args)
	if err != nil {
		t.Fatal(err)
	}
	if first.SessionID == second.SessionID || first.Worktree != dir || second.Worktree != dir {
		t.Fatal(first, second)
	}
	for _, id := range []string{first.SessionID, second.SessionID} {
		tab, ok := d.sessions.Get(id)
		if !ok || tab.Agent != owner.Agent || tab.AgentTabParent != owner.ID || tab.TmuxName != id || tab.OwnsBranch() {
			t.Fatal(tab)
		}
	}
	if _, err := d.handleKill(ctx, owner.ID, true, false); !errors.Is(err, errAgentTabsOpen) || len(nat.kills) != 0 {
		t.Fatalf("owner removal: %v, kills %v", err, nat.kills)
	}
	if _, err := d.handleKill(ctx, first.SessionID, false, false); err != nil {
		t.Fatal(err)
	}
	if _, ok := d.sessions.Get(owner.ID); !ok {
		t.Fatal("owner removed")
	}
	if _, ok := d.sessions.Get(second.SessionID); !ok {
		t.Fatal("sibling removed")
	}
	d.cfg.ProjectByName(owner.Project).Sandbox = config.SandboxDocker
	if _, err := d.handleOpenManual(ctx, args); err == nil {
		t.Fatal("opened a tab without verifying the original host launch")
	}
	nat.alive = map[string]bool{owner.ID: true}
	d.syncGitPins()
	var binary string
	d.runtimeHealth = func(bin string) error { binary = bin; return nil }
	inherited, err := d.handleOpenManual(ctx, args)
	if err != nil {
		t.Fatal(err)
	}
	tab, ok := d.sessions.Get(inherited.SessionID)
	if !ok || tab.Sandbox != "" || tab.Worktree != dir || binary != owner.Agent {
		t.Fatalf("expected host agent %s in %s, got binary %s, tab %+v", owner.Agent, dir, binary, tab)
	}
}

func TestAgentTabReadsOnlyItsConversation(t *testing.T) {
	r := newFeedRig(t)
	tab := r.s
	tab.ID, tab.TmuxName, tab.AgentTabParent = "second", "second", r.s.ID
	tab.AgentCWD = filepath.Join(r.d.home, "local-sessions", tab.ID, "work")
	r.d.sessions.Upsert(tab)
	child := feedRig{d: r.d, s: tab, path: filepath.Join(usage.SlugDir(usage.ProjectsRoot(), tab.AgentCWD), feedSeg)}
	if err := os.MkdirAll(filepath.Dir(child.path), 0700); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	r.write(t, feedText("one", "primary only", now))
	child.write(t, feedText("two", "second only", now))
	for _, test := range []struct {
		rig  *feedRig
		text string
	}{{r, "primary only"}, {&child, "second only"}} {
		items := test.rig.feed(t).Items
		if len(items) != 1 || items[0].Text != test.text {
			t.Fatalf("wrong transcript for %s: %+v", test.rig.s.ID, items)
		}
	}
}
