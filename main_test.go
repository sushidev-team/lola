package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeBin writes an executable no-op script named tool into dir.
func fakeBin(t *testing.T, dir, tool string) {
	t.Helper()
	body := "#!/bin/sh\nexit 0\n"
	if tool == "claude" {
		body = "#!/bin/sh\necho '1.2.3 (Claude Code)'\n"
	}
	if err := os.WriteFile(filepath.Join(dir, tool), []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}

// runDoctor returns true (exit 0) only when every critical check passes; the
// doctorCmd wrapper turns a false into os.Exit(1).
func TestRunDoctorExitCode(t *testing.T) {
	t.Setenv("LOLA_HOME", t.TempDir()) // no config, no socket

	// All runtime tools present on a clean PATH -> critical checks pass.
	present := t.TempDir()
	for _, tool := range []string{"tmux", "git", "claude", "gh"} {
		fakeBin(t, present, tool)
	}
	t.Setenv("PATH", present)
	var out strings.Builder
	if ok := runDoctor(context.Background(), &out); !ok {
		t.Errorf("runDoctor with all tools present = false, want true\n%s", out.String())
	}
	if !strings.Contains(out.String(), "ok,") {
		t.Errorf("report missing summary line:\n%s", out.String())
	}

	// Empty PATH -> tmux/git/claude missing (critical) -> exit non-zero.
	t.Setenv("PATH", t.TempDir())
	out.Reset()
	if ok := runDoctor(context.Background(), &out); ok {
		t.Errorf("runDoctor with tools missing = true, want false\n%s", out.String())
	}
}

// The report must never render the Linear API key value.
func TestRunDoctorNeverPrintsKey(t *testing.T) {
	home := t.TempDir()
	t.Setenv("LOLA_HOME", home)
	dir := t.TempDir()
	for _, tool := range []string{"tmux", "git", "claude", "gh"} {
		fakeBin(t, dir, tool)
	}
	t.Setenv("PATH", dir)

	const secret = "lin_api_SUPERSECRET_never_render"
	t.Setenv("LINEAR_API_KEY", secret)
	cfg := "[linear]\napi_key_env = \"LINEAR_API_KEY\"\n[defaults]\nglobal_cap = 1\n"
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}

	var out strings.Builder
	runDoctor(context.Background(), &out)
	if strings.Contains(out.String(), secret) {
		t.Fatalf("doctor report leaked the API key:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "found in env LINEAR_API_KEY") {
		t.Errorf("report should attribute the key source:\n%s", out.String())
	}
}

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
