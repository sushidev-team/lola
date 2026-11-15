package daemon

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/sushidev-team/lola/internal/linear"
	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/session"
)

func ticketCalls(fake *linear.Fake) int { return countCalls(fake.CallNames(), "MatchingIssues") }

func TestTicketsCachedPerProjectAndScope(t *testing.T) {
	fake := &linear.Fake{Issues: []linear.Issue{{ID: "u1", Identifier: "FE-1", Title: "one"}}}
	d := newTestDaemon(t, testConfig(labelPoll("p1")), fake, &fakeNative{})
	ctx := context.Background()
	get := func(scope string) protocol.TicketsData {
		t.Helper()
		data, err := d.handleTickets(ctx, protocol.TicketsArgs{Project: "p1", Scope: scope})
		if err != nil {
			t.Fatal(err)
		}
		return data
	}

	// A burst shares one Linear query per scope.
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); get("team") }()
	}
	wg.Wait()
	if n := ticketCalls(fake); n != 1 {
		t.Fatalf("8 concurrent team reads ran %d queries, want 1", n)
	}
	get("team")
	if n := ticketCalls(fake); n != 1 {
		t.Fatalf("a warm read queried again: %d", n)
	}

	// The other scope is its own entry.
	get("mine")
	if n := ticketCalls(fake); n != 2 {
		t.Fatalf("scope mine must fetch separately: %d", n)
	}
	get("")
	if n := ticketCalls(fake); n != 2 {
		t.Fatalf("empty scope is mine and is cached: %d", n)
	}

	// AlreadyLive is derived per request, never cached.
	if get("team").Issues[0].AlreadyLive {
		t.Fatal("fixture: nothing is live yet")
	}
	d.sessions.Upsert(session.Session{ID: "s1", Source: "native", Kind: session.KindLinear, Project: "p1", IssueUUID: "u1", Status: "working"})
	if !get("team").Issues[0].AlreadyLive {
		t.Error("a session opened inside the TTL must show on the next read")
	}
	if n := ticketCalls(fake); n != 2 {
		t.Fatalf("liveness must not cost a query: %d", n)
	}

	// Past the TTL it refetches.
	e := d.ticketsCache().entry(ticketsKey{"p1", ticketsScopeTeam})
	e.mu.Lock()
	e.fetchedAt = time.Now().Add(-ticketsTTL - time.Second)
	e.mu.Unlock()
	get("team")
	if n := ticketCalls(fake); n != 3 {
		t.Fatalf("an expired entry must refetch: %d", n)
	}
}

// A failed fetch is not cached: the next call asks again.
func TestTicketsFailureIsNotCached(t *testing.T) {
	fake := &linear.Fake{Issues: []linear.Issue{{ID: "u1", Identifier: "FE-1"}}, Errs: map[string]error{"MatchingIssues": context.DeadlineExceeded}}
	d := newTestDaemon(t, testConfig(labelPoll("p1")), fake, &fakeNative{})
	args := protocol.TicketsArgs{Project: "p1", Scope: "team"}
	if _, err := d.handleTickets(context.Background(), args); err == nil {
		t.Fatal("want the Linear error")
	}
	fake.Errs = nil
	data, err := d.handleTickets(context.Background(), args)
	if err != nil || len(data.Issues) != 1 {
		t.Fatalf("retry after failure: %+v %v", data, err)
	}
}
