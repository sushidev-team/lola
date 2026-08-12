package lolaenv

import "strings"

import "testing"

// The command is the contract three call sites share (agent pane, TUI shell
// tab, desktop terminal). Pin its shape: a wrapper that drops the env file, or
// stops exec'ing, silently changes what every pane inherits.
func TestShellCommandExportsTheEnvFileAndExecsTheLoginShell(t *testing.T) {
	got := ShellCommand

	for _, want := range []string{
		"set -a",                   // exported, not just set
		"[ -f ./.lola/env ]",       // a worktree without one still gets a shell
		". ./.lola/env",            // sourced, so no secret reaches argv
		"set +a",                   // stop exporting before the shell starts
		`exec "${SHELL:-/bin/sh}"`, // the pane's process IS the login shell
	} {
		if !strings.Contains(got, want) {
			t.Errorf("ShellCommand missing %q:\n%s", want, got)
		}
	}

	if !strings.HasPrefix(got, "exec sh -c ") {
		t.Errorf("ShellCommand must hand a POSIX sh the posix-only line: %s", got)
	}
	if File != ".lola/env" || Dir != ".lola" {
		t.Errorf("paths drifted: Dir=%q File=%q", Dir, File)
	}
}
