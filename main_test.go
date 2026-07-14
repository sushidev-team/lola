package main

import (
	"strings"
	"testing"
)

// The hook subcommand must exit 0 no matter what breaks — here both the
// session env and the daemon socket are missing — because a failing hook
// command would surface as an error inside the agent's turn.
func TestHookCmdExitsCleanOnFailure(t *testing.T) {
	t.Setenv("LOLA_HOME", t.TempDir()) // no daemon socket
	t.Setenv("LOLA_SESSION", "")       // not a lola session

	cmd := hookCmd()
	cmd.SetIn(strings.NewReader(`{"session_id":"abc","stop_reason":"end_turn"}`))
	var stderr strings.Builder
	cmd.SetErr(&stderr)

	if err := cmd.RunE(cmd, []string{"stop"}); err != nil {
		t.Fatalf("hook RunE = %v, want nil (hook must always exit 0)", err)
	}
	if !strings.Contains(stderr.String(), "not a lola session") {
		t.Errorf("stderr = %q, want the failure diagnosed there", stderr.String())
	}
}

func TestHookCmdNoEventStillExitsClean(t *testing.T) {
	t.Setenv("LOLA_HOME", t.TempDir())
	t.Setenv("LOLA_SESSION", "")

	cmd := hookCmd()
	cmd.SetIn(strings.NewReader(""))
	cmd.SetErr(&strings.Builder{})
	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatalf("hook RunE with no args = %v, want nil", err)
	}
}

func TestHookDetail(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{"notification", `{"notification_type":"permission_request","message":"..."}`, "permission_request"},
		{"stop", `{"session_id":"s","cwd":"/w","stop_reason":"end_turn"}`, "end_turn"},
		{"session end", `{"end_reason":"exit"}`, "exit"},
		{"no known field", `{"session_id":"s"}`, ""},
		{"invalid json", `garbage{`, ""},
		{"empty", ``, ""},
	}
	for _, tc := range cases {
		if got := hookDetail(strings.NewReader(tc.in)); got != tc.want {
			t.Errorf("%s: hookDetail(%q) = %q, want %q", tc.name, tc.in, got, tc.want)
		}
	}
}
