package linear

import (
	"context"
	"testing"
)

func TestAgentSessionOwnerOrder(t *testing.T) {
	const app = "app"
	for _, tc := range []struct {
		name string
		s    AgentSession
		want string
	}{
		{"human delegate wins", AgentSession{DelegateID: "d", AssigneeID: "a", CreatorID: "c"}, "d"},
		{"the app as delegate names nobody", AgentSession{DelegateID: app, AssigneeID: "a", CreatorID: "c"}, "a"},
		{"no assignee falls to the creator", AgentSession{DelegateID: app, CreatorID: "c"}, "c"},
		{"the app as creator and nothing else", AgentSession{DelegateID: app, CreatorID: app}, ""},
		{"unknown", AgentSession{}, ""},
	} {
		if got := tc.s.Owner(app); got != tc.want {
			t.Errorf("%s: owner = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// An app user is never a person: a delegate that is another agent must not hide
// the human assignee, and the mention comment text is read.
func TestAgentSessionsDropAppUsersFromOwnership(t *testing.T) {
	srv := gqlServer(t, `{"agentSessions":{"nodes":[
		{"id":"s1","status":"pending","createdAt":"2026-01-01T00:00:00Z","url":null,
		 "creator":{"id":"ben","app":false},"sourceComment":{"body":"@lola go ahead"},
		 "issue":{"id":"i1","identifier":"ENG-1","title":"t","branchName":"b","priority":2,"createdAt":"x",
		  "team":{"id":"t"},"project":null,"assignee":{"id":"ana","app":false},"delegate":{"id":"other-agent","app":true}}}]}}`, nil, nil)
	defer srv.Close()
	got, err := NewBearer(srv.URL, "t").AgentSessions(context.Background())
	if err != nil || len(got) != 1 {
		t.Fatalf("%v %+v", err, got)
	}
	s := got[0]
	if s.DelegateID != "" || s.AssigneeID != "ana" || s.CreatorID != "ben" || s.MentionBody != "@lola go ahead" {
		t.Fatalf("session = %+v", s)
	}
	if o := s.Owner(""); o != "ana" {
		t.Fatalf("owner = %q, want the assignee", o)
	}
}
