package goal

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestProgressCoalescesFactsAndPreservesIntent(t *testing.T) {
	g := executable(t)
	now := time.Now().UTC()
	summary := g.ProgressSummary()
	g.Execution.ObservedTokens = 42
	g.Execution.Reason = "sensitive raw tool output"
	if g.ProgressSummary() != summary {
		t.Fatal("heartbeat, consumption or raw output causes narration")
	}
	if ok, err := g.ReserveProgress(NewID(), now); !ok || err != nil {
		t.Fatal(ok, err)
	}
	s, _ := Open(filepath.Join(t.TempDir(), "goals.json"))
	g, err := s.Save(g)
	if err != nil {
		t.Fatal(err)
	}
	id := g.ProgressUpdates[0].ID
	g.State = "paused"
	if ok, err := g.ReserveProgress(NewID(), now.Add(3*time.Minute)); ok || err != nil {
		t.Fatal("replaced unresolved send", err)
	}
	g.ProgressUpdates[0].ConfirmedAt = now
	g, err = s.Save(g)
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := g.ReserveProgress(NewID(), now.Add(time.Second)); ok || err != nil {
		t.Fatal("failed to coalesce", err)
	}
	if ok, err := g.ReserveProgress(NewID(), now.Add(2*time.Minute)); !ok || err != nil {
		t.Fatal("material transition not reserved", err)
	}
	g, err = s.Save(g)
	if err != nil {
		t.Fatal(err)
	}
	g.ProgressUpdates[0].Summary = "rewritten"
	if _, err = s.Save(g); err == nil {
		t.Fatal("history overwritten")
	}
	stored, _ := s.Get(g.ID)
	if stored.ProgressUpdates[0].ID != id || strings.Contains(stored.ProgressUpdates[0].Body(g.ID), "sensitive") {
		t.Fatal("identity or privacy boundary changed")
	}
}
func TestProgressDoesNotInventDeliveryOrVerification(t *testing.T) {
	g := executable(t)
	text := g.ProgressSummary()
	if !strings.Contains(text, "not yet established") || !strings.Contains(text, "no target-branch merge") {
		t.Fatal(text)
	}
	g.PendingBrief = "proposal"
	if !strings.Contains(g.ProgressSummary(), "Decision needed") {
		t.Fatal("human edit not visible")
	}
	if safeProgressURL("https://github.com/owner/repo/pull/1)\n@everyone") || safeProgressURL("https://foreign.example/pull/1") || !safeProgressURL("https://github.com/owner/repo/pull/1") {
		t.Fatal("unsafe progress link")
	}
}

func TestProgressDismissalRetainsUncertainty(t *testing.T) {
	g := executable(t)
	g.Revision = 1
	now := time.Now().UTC()
	_, err := g.ReserveProgress(NewID(), now)
	if err != nil {
		t.Fatal(err)
	}
	u := g.ProgressUpdates[0]
	if err = g.DismissProgress(ProgressResolution{ID: g.ID, Revision: 2, UpdateID: u.ID, Reason: "reviewed"}, now); err == nil {
		t.Fatal("stale dismissal accepted")
	}
	if err = g.DismissProgress(ProgressResolution{ID: g.ID, Revision: 1, UpdateID: u.ID, Reason: "Human removed the comment; preserve that deletion"}, now); err != nil {
		t.Fatal(err)
	}
	if !g.ProgressUpdates[0].ConfirmedAt.IsZero() || g.ProgressUpdates[0].DismissedAt.IsZero() {
		t.Fatal("dismissal invented delivery")
	}
	if ok, err := g.ReserveProgress(NewID(), now.Add(3*time.Minute)); ok || err != nil {
		t.Fatal("dismissed content reposted", err)
	}
	g.State = "paused"
	if ok, err := g.ReserveProgress(NewID(), now.Add(3*time.Minute)); !ok || err != nil {
		t.Fatal("later material update suppressed", err)
	}
	if err = g.validateProgress(); err != nil {
		t.Fatal(err)
	}
}
