package runtime

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestAttemptIdentitySurvivesCleanupAndRuntimeRestart(t *testing.T) {
	f := newFixture(t, "", "")
	for i := 1; i <= maxSpawnAttempts+2; i++ {
		// Every probe sees no worktree or local branch, as after successful cleanup.
		// A newly constructed runtime must still remember every previous claim.
		n := *f.n
		id, branch, err := n.freeSessionSlot(context.Background(), f.p, "lola-nori-eng-42", "lola/eng-42")
		if err != nil {
			t.Fatal(err)
		}
		suffix := ""
		if i > 1 {
			suffix = "-r" + strconv.Itoa(i)
		}
		if id != "lola-nori-eng-42"+suffix || branch != "lola/eng-42"+suffix {
			t.Fatalf("attempt %d reused an identity: %s %s", i, id, branch)
		}
	}
}

func TestAttemptReservesLegacyHistoryAndFailsClosed(t *testing.T) {
	f := newFixture(t, "", "")
	f.n.SessionUsed = func(id string) (bool, error) { return id == "lola-nori-eng-42", nil }
	id, _, err := f.n.freeSessionSlot(context.Background(), f.p, "lola-nori-eng-42", "lola/eng-42")
	if err != nil || id != "lola-nori-eng-42-r2" {
		t.Fatalf("legacy attempt reused: %s %v", id, err)
	}
	// History can expire without making either claimed identity reusable.
	f.n.SessionUsed = nil
	id, _, err = f.n.freeSessionSlot(context.Background(), f.p, "lola-nori-eng-42", "lola/eng-42")
	if err != nil || id != "lola-nori-eng-42-r3" {
		t.Fatalf("expired history reused: %s %v", id, err)
	}
	f.n.SessionUsed = func(string) (bool, error) { return false, errors.New("unreadable history") }
	if _, _, err = f.n.freeSessionSlot(context.Background(), f.p, "lola-nori-eng-42", "lola/eng-42"); err == nil {
		t.Fatal("unreadable history must stop launch")
	}
}

func TestAttemptCannotLaunchWithoutDurableReservation(t *testing.T) {
	f := newFixture(t, "", "")
	if err := os.MkdirAll(filepath.Join(f.n.Home, "state"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.n.Home, "state", "attempts"), []byte("blocked"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := f.n.Spawn(context.Background(), f.p, issueENG42(), ""); err == nil {
		t.Fatal("must fail closed when the reservation cannot be persisted")
	}
	if loggedArgs(t, f.gitLog) != "" || loggedArgs(t, f.tmuxLog) != "" {
		t.Fatal("launch touched git or tmux before reserving identity")
	}
}

func TestFreshIssuePromptIncludesPreviousWork(t *testing.T) {
	f := newFixture(t, "", "")
	f.n.PreviousWork = func(project, issue string) string {
		if project != f.p.Name || issue != "ENG-42" {
			t.Fatalf("wrong history scope: %s %s", project, issue)
		}
		return "- PR #8 merged: https://github.com/acme/nori/pull/8\n"
	}
	before := time.Now()
	s, err := f.n.Spawn(context.Background(), f.p, issueENG42(), "")
	if err != nil {
		t.Fatal(err)
	}
	if s.FirstSeen.Before(before) || s.FirstSeen.After(time.Now()) {
		t.Fatalf("missing launch boundary: %s", s.FirstSeen)
	}
	b, err := os.ReadFile(filepath.Join(s.Worktree, lolaDir, "prompt.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"latest Linear comments", "outstanding requests", "https://github.com/acme/nori/pull/8", "open a new PR", "no shared notes are available"} {
		if !strings.Contains(string(b), want) {
			t.Errorf("prompt missing %q", want)
		}
	}
}

func TestConcurrentLaunchesReserveDifferentIdentities(t *testing.T) {
	f := newFixture(t, "", "")
	var wg sync.WaitGroup
	ids := make(chan string, 3)
	for range 3 {
		wg.Go(func() {
			n := *f.n
			id, _, err := n.freeSessionSlot(context.Background(), f.p, "lola-nori-eng-42", "lola/eng-42")
			if err != nil {
				t.Error(err)
				return
			}
			ids <- id
		})
	}
	wg.Wait()
	close(ids)
	seen := map[string]bool{}
	for id := range ids {
		if seen[id] {
			t.Fatalf("concurrent launches shared %s", id)
		}
		seen[id] = true
	}
	if len(seen) != 3 {
		t.Fatalf("only reserved %v", seen)
	}
}

func TestStackFreshAttemptAfterCleanTeardown(t *testing.T) {
	f := newStackFixture(t)
	f.n.PreviousWork = func(string, string) string { return "- Previous PR: https://github.com/acme/docs/pull/8\n" }
	first, err := f.n.Spawn(context.Background(), f.p, issueENG42(), "")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.n.Kill(context.Background(), first, true, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(first.Worktree); !os.IsNotExist(err) {
		t.Fatalf("first worktree should be gone: %v", err)
	}
	nextRuntime := *f.n
	next, err := nextRuntime.Spawn(context.Background(), f.p, issueENG42(), "")
	if err != nil {
		t.Fatal(err)
	}
	if next.ID != first.ID+"-r2" || next.Branch != first.Branch+"-r2" {
		t.Fatalf("stack reused cleaned identity: %+v", next)
	}
	for _, app := range next.Apps {
		if app.Branch != next.Branch {
			t.Fatalf("app did not use fresh branch: %+v", app)
		}
	}
	b, err := os.ReadFile(filepath.Join(next.Worktree, lolaDir, "prompt.md"))
	if err != nil || !strings.Contains(string(b), "https://github.com/acme/docs/pull/8") {
		t.Fatalf("stack missing prior work: %v", err)
	}
}
