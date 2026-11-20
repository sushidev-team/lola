package linear

import "testing"

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
