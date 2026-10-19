package session

import (
	"testing"
	"time"

	"github.com/sushidev-team/lola/internal/scm"
	"github.com/sushidev-team/lola/internal/state"
)

func stackFixture() Session {
	return Session{
		ID: "lola-nori-eng-42", Project: "nori", Stack: true, Worktree: "/w/nori/lola-nori-eng-42",
		AgentState: state.AgentIdle, AtPrompt: true, Delivery: state.DeliveryNone,
		Apps: []App{
			{Name: "nori-app", Repo: "acme/app", Worktree: "/w/nori/lola-nori-eng-42/nori-app", Branch: "lola/eng-42"},
			{Name: "nori-website", Repo: "acme/web", Worktree: "/w/nori/lola-nori-eng-42/nori-website", Branch: "lola/eng-42"},
		},
	}
}

func TestLaneIDRoundTrip(t *testing.T) {
	id := LaneID("lola-nori-eng-42", "nori-website")
	if b, a := SplitLaneID(id); b != "lola-nori-eng-42" || a != "nori-website" {
		t.Fatalf("split = %q %q", b, a)
	}
	if b, a := SplitLaneID("lola-nori-eng-42"); b != "lola-nori-eng-42" || a != "" {
		t.Fatalf("plain id split = %q %q", b, a)
	}
}

func TestLaneUpdateWritesTheAppAndSharesTheGate(t *testing.T) {
	st := NewStore(t.TempDir())
	st.Upsert(stackFixture())
	lane := LaneID("lola-nori-eng-42", "nori-website")

	v, ok := st.Get(lane)
	if !ok || v.Repo != "acme/web" || v.Worktree != "/w/nori/lola-nori-eng-42/nori-website" || v.PR != nil {
		t.Fatalf("lane view = %+v %v", v, ok)
	}
	// A reaction on the website lane: CI failed, gate consumed, guard stamped.
	now := time.Now()
	got, ok := st.Update(lane, func(cur *Session) bool {
		cur.PR = &scm.PR{Number: 8, State: "OPEN", ChecksState: "FAILURE"}
		cur.SetDelivery(state.DeliveryCIFailed, now)
		cur.AtPrompt = false
		cur.LastReactedStatus = "ci_failed"
		cur.ReviewedPRs = map[string]int{"claude-session": 8}
		return true
	})
	if !ok || got.Lane != "nori-website" || got.PR.Number != 8 {
		t.Fatalf("returned view = %+v", got)
	}
	s, _ := st.Get("lola-nori-eng-42")
	if s.AtPrompt {
		t.Error("the gate is the session's: consuming it on one lane closes it")
	}
	if s.LastReactedStatus != "" || s.ReviewedPRs != nil {
		t.Errorf("lane guards leaked onto the session: %q %v", s.LastReactedStatus, s.ReviewedPRs)
	}
	web := s.Apps[s.AppIndex("nori-website")]
	if web.LastReactedStatus != "ci_failed" || web.ReviewedPRs["claude-session"] != 8 || web.Delivery != state.DeliveryCIFailed {
		t.Errorf("website lane = %+v", web)
	}
	if s.Delivery != state.DeliveryCIFailed || s.PR == nil || s.PR.Number != 8 || s.Repo != "acme/web" || s.FocusApp() != "nori-website" {
		t.Errorf("aggregate = %s %+v %q", s.Delivery, s.PR, s.Repo)
	}
	if s.Worktree != "/w/nori/lola-nori-eng-42" {
		t.Errorf("the session folder was overwritten: %q", s.Worktree)
	}
	// The app lane is untouched by the website's state.
	if a, _ := st.Get(LaneID("lola-nori-eng-42", "nori-app")); a.LastReactedStatus != "" || a.PR != nil {
		t.Errorf("app lane = %+v", a)
	}
}

func TestReaggregateIgnoresUntouchedApps(t *testing.T) {
	s := stackFixture()
	s.Apps[0].PR = &scm.PR{Number: 3, State: "MERGED"}
	s.Apps[0].Delivery = state.DeliveryMerged
	s.Reaggregate(time.Now())
	if s.Delivery != state.DeliveryMerged {
		t.Fatalf("an app the issue never touched kept the stack open: %s", s.Delivery)
	}
	s.Apps[1].Touched = true
	s.Reaggregate(time.Now())
	if s.Delivery != state.DeliveryNone {
		t.Fatalf("an app with unshipped changes must keep the stack pending: %s", s.Delivery)
	}
}

func TestLaneIDsAreNeverInsertedOrDeleted(t *testing.T) {
	st := NewStore(t.TempDir())
	st.Upsert(stackFixture())
	if st.Delete(LaneID("lola-nori-eng-42", "nori-app")) {
		t.Error("deleting a lane must not delete anything")
	}
	if _, ok := st.Apply(LaneID("lola-nori-eng-42", "nope"), func(*Session, bool) bool { return true }); ok {
		t.Error("an unknown app's lane must not be created")
	}
	if n := len(st.Snapshot()); n != 1 {
		t.Errorf("snapshot has %d records, want the one session", n)
	}
}
