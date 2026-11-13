package daemon

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/linear"
)

func TestRefProblems(t *testing.T) {
	p := config.Project{TeamID: "team-1", MatchMode: "all", MatchLabels: []string{"dead", "lbl-app"}, StateIDs: []string{"st-todo"}, OnSentSetLabel: "lbl-sent"}
	live := []linear.Ref{{ID: "lbl-app", Name: "app", TeamID: "team-1"}, {ID: "lbl-sent", Name: "agent/sent"}}
	states := []linear.Ref{{ID: "st-todo", Name: "Todo", TeamID: "team-1"}}

	got := refProblems(p, live, states)
	if len(got) != 1 || !strings.Contains(got[0], "match label dead no longer exists") {
		t.Fatalf("match_mode=all with one dead label = %q", got)
	}

	// match_mode=any still matches through the live label: not an error.
	p.MatchMode = "any"
	if got := refProblems(p, live, states); len(got) != 0 {
		t.Fatalf("any-mode with a live label reported %q", got)
	}

	// A label of ANOTHER team can never be on this team's issues.
	p.MatchMode, p.MatchLabels = "all", []string{"lbl-other"}
	other := append(live, linear.Ref{ID: "lbl-other", Name: "agent-ready", TeamID: "team-2"})
	if got := refProblems(p, other, states); len(got) != 1 || !strings.Contains(got[0], `"agent-ready" belongs to another team`) {
		t.Fatalf("foreign team label = %q", got)
	}

	// Every state gone, and a dead on-sent label.
	p.MatchLabels, p.OnSentSetLabel = []string{"lbl-app"}, "sent-dead"
	if got := refProblems(p, live, nil); len(got) != 2 || !strings.Contains(got[0], "state st-todo") || !strings.Contains(got[1], "on-sent label") {
		t.Fatalf("dead state + on-sent = %q", got)
	}
}

// A zero-match tick whose filter names a dead label reports it as the poll's
// LastError (the field every surface already renders) and in the dry-run
// result; a lookup failure reports nothing (fail open).
func TestTickReportsUnmatchableFilter(t *testing.T) {
	p := labelPoll("p1")
	p.MatchMode = "all"
	fake := &linear.Fake{RefsFunc: func(l, s []string) ([]linear.Ref, []linear.Ref, error) {
		return []linear.Ref{{ID: "lbl-sent", Name: "sent"}}, nil, nil // lbl-trigger is gone
	}}
	d := newTestDaemon(t, testConfig(p), fake, &fakeNative{})

	res, err := d.tick(context.Background(), "p1", true)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Problems) != 1 {
		t.Fatalf("dry-run problems = %q", res.Problems)
	}
	if _, err := d.tick(context.Background(), "p1", false); err != nil {
		t.Fatal(err)
	}
	if got := d.status.get("p1").LastError; !strings.HasPrefix(got, "filter can never match: match label lbl-trig") {
		t.Fatalf("LastError = %q", got)
	}

	// Fixed config (new cache key) + lookup failure: fail open, error cleared.
	d.mu.Lock()
	d.cfg.Projects[0].MatchLabels = []string{"lbl-new"}
	d.mu.Unlock()
	fake.RefsFunc = func(l, s []string) ([]linear.Ref, []linear.Ref, error) { return nil, nil, errors.New("502") }
	if _, err := d.tick(context.Background(), "p1", false); err != nil {
		t.Fatal(err)
	}
	if got := d.status.get("p1").LastError; got != "" {
		t.Fatalf("a failed lookup must not paint the poll red: %q", got)
	}
}

func TestRefCacheHonoursTTL(t *testing.T) {
	calls := 0
	fake := &linear.Fake{RefsFunc: func(l, s []string) ([]linear.Ref, []linear.Ref, error) {
		calls++
		return nil, nil, nil
	}}
	d := newTestDaemon(t, testConfig(labelPoll("p1")), fake, &fakeNative{})
	p := labelPoll("p1")
	now := time.Now()
	d.filterRefProblems(context.Background(), fake, "p1", p, now)
	d.filterRefProblems(context.Background(), fake, "p1", p, now.Add(time.Minute))
	if calls != 1 {
		t.Fatalf("cached verdict re-queried: %d calls", calls)
	}
	d.filterRefProblems(context.Background(), fake, "p1", p, now.Add(refTTL+time.Second))
	if calls != 2 {
		t.Fatalf("TTL never expires: %d calls", calls)
	}
}
