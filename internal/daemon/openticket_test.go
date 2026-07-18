package daemon

import (
	"context"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/linear"
	"github.com/sushidev-team/lola/internal/protocol"
	"github.com/sushidev-team/lola/internal/session"
)

// openTicket for a POLLING project must reproduce the dispatch dedup ordering:
// in-flight + seen persisted BEFORE the spawn, session upserted with the poll
// name, and the label flip fired — so a running poll can't double-dispatch it.
func TestOpenTicketPollingDedupsLikeTick(t *testing.T) {
	is := testIssue("FE-9", 1, "2024-01-01T00:00:00Z")
	fake := &linear.Fake{LabelIDsByIssue: map[string][]string{is.ID: {"lbl-trigger"}}}
	nat := &fakeNative{}
	d := newTestDaemon(t, testConfig(labelPoll("p1")), fake, nat)

	nat.onSpawn = func(_ config.Project, _ linear.Issue) {
		if !d.inflight.Has(is.ID) {
			t.Error("in-flight must be claimed before the spawn")
		}
		data, err := os.ReadFile(seenPath(d, "p1"))
		if err != nil || !strings.Contains(string(data), is.ID) {
			t.Errorf("seen must be persisted before the spawn (err=%v data=%s)", err, data)
		}
	}

	data, err := d.handleOpenTicket(context.Background(), protocol.OpenTicketArgs{Project: "p1", Identifier: is.Identifier, UUID: is.ID, Title: is.Title})
	if err != nil {
		t.Fatalf("handleOpenTicket: %v", err)
	}
	if len(nat.spawnCalls()) != 1 {
		t.Fatalf("want one spawn, got %v", nat.spawnCalls())
	}
	s, ok := d.sessions.Get(data.SessionID)
	if !ok || !s.LinearBound() || s.PollName != "p1" {
		t.Fatalf("session must be a poll-named linear session: %+v", s)
	}
	if !slices.Contains(fake.CallNames(), "SetIssueLabels") {
		t.Errorf("label-mode project must flip labels, calls = %v", fake.CallNames())
	}
}

// An issue already in-flight (claimed by a tick or a re-open) is refused without
// a spawn.
func TestOpenTicketRefusesWhenInFlight(t *testing.T) {
	is := testIssue("FE-9", 1, "2024-01-01T00:00:00Z")
	nat := &fakeNative{}
	d := newTestDaemon(t, testConfig(labelPoll("p1")), &linear.Fake{}, nat)
	d.inflight.Add(is.ID, is.Identifier) // as a concurrent tick would

	if _, err := d.handleOpenTicket(context.Background(), protocol.OpenTicketArgs{Project: "p1", Identifier: is.Identifier, UUID: is.ID}); err == nil {
		t.Fatal("must refuse an issue already in-flight")
	}
	if len(nat.spawnCalls()) != 0 {
		t.Error("must not spawn when already in-flight")
	}
}

// A NON-polling project (no team) starts a ticket with only the in-flight claim
// as the guard — no seen file, no poll name.
func TestOpenTicketNonPollingProject(t *testing.T) {
	nat := &fakeNative{}
	cfg := &config.Config{
		Defaults: config.Defaults{ConcurrencyCap: 10, GlobalCap: 10},
		Projects: []config.Project{{Name: "manual", Path: "/tmp/manual", Repo: "acme/m", DefaultBranch: "main"}},
	}
	d := newTestDaemon(t, cfg, &linear.Fake{}, nat)

	data, err := d.handleOpenTicket(context.Background(), protocol.OpenTicketArgs{Project: "manual", Identifier: "X-1", UUID: "uuid-x1"})
	if err != nil {
		t.Fatalf("handleOpenTicket: %v", err)
	}
	if len(nat.spawnCalls()) != 1 {
		t.Fatalf("want one spawn, got %v", nat.spawnCalls())
	}
	if _, serr := os.Stat(seenPath(d, "manual")); !os.IsNotExist(serr) {
		t.Errorf("a non-polling project must not write a seen file (err=%v)", serr)
	}
	if s, _ := d.sessions.Get(data.SessionID); s.PollName != "" {
		t.Errorf("a non-polling ticket has no owning poll, got PollName=%q", s.PollName)
	}
}

// A spawn failure rolls back the in-flight claim (and the seen entry) so the
// issue can be retried.
func TestOpenTicketSpawnFailureRollsBack(t *testing.T) {
	is := testIssue("FE-9", 1, "2024-01-01T00:00:00Z")
	nat := &fakeNative{spawnErr: errors.New("boom")}
	d := newTestDaemon(t, testConfig(seenPoll("p1")), &linear.Fake{}, nat)

	if _, err := d.handleOpenTicket(context.Background(), protocol.OpenTicketArgs{Project: "p1", Identifier: is.Identifier, UUID: is.ID}); err == nil {
		t.Fatal("spawn failure must surface")
	}
	if d.inflight.Has(is.ID) {
		t.Error("in-flight must be released on spawn failure")
	}
	// seen mode: the entry must be removed so the issue is not dropped forever.
	if seen, _ := d.seen.load("p1"); len(seen) != 0 {
		t.Errorf("seen entry must be removed on spawn failure, got %v", seen)
	}
}

func TestOpenTicketUnknownProject(t *testing.T) {
	d := newTestDaemon(t, testConfig(labelPoll("p1")), &linear.Fake{}, &fakeNative{})
	if _, err := d.handleOpenTicket(context.Background(), protocol.OpenTicketArgs{Project: "ghost", Identifier: "X-1", UUID: "u"}); err == nil {
		t.Fatal("unknown project must error")
	}
}

// cmd=tickets lists a project's team issues; a project with no team is a clear
// error, not an empty list.
func TestHandleTickets(t *testing.T) {
	fake := &linear.Fake{Issues: []linear.Issue{
		{ID: "u1", Identifier: "FE-1", Title: "one", Priority: 1},
		{ID: "u2", Identifier: "FE-2", Title: "two", Priority: 3},
	}}
	d := newTestDaemon(t, testConfig(labelPoll("p1")), fake, &fakeNative{})
	// A live session on FE-1 marks it already-live.
	d.sessions.Upsert(session.Session{ID: "s1", Source: "native", Kind: session.KindLinear, Project: "p1", IssueUUID: "u1", Status: "working"})

	data, err := d.handleTickets(context.Background(), protocol.TicketsArgs{Project: "p1", Scope: "team"})
	if err != nil {
		t.Fatalf("handleTickets: %v", err)
	}
	if len(data.Issues) != 2 {
		t.Fatalf("want 2 issues, got %d", len(data.Issues))
	}
	var fe1 protocol.TicketRow
	for _, r := range data.Issues {
		if r.Identifier == "FE-1" {
			fe1 = r
		}
	}
	if !fe1.AlreadyLive {
		t.Error("FE-1 has a live session and must be flagged AlreadyLive")
	}

	// A project without a team is a distinct error.
	d2 := newTestDaemon(t, &config.Config{
		Defaults: config.Defaults{GlobalCap: 10, ConcurrencyCap: 10},
		Projects: []config.Project{{Name: "manual", Path: "/tmp/manual"}},
	}, &linear.Fake{}, &fakeNative{})
	if _, err := d2.handleTickets(context.Background(), protocol.TicketsArgs{Project: "manual"}); err == nil {
		t.Fatal("a project with no team must error, not return an empty list")
	}
}
