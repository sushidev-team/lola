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

// CommandLine is what every dev tab runs. Pin the two properties the daemon
// relies on: the env file is exported (so a dev server sees [[project]].env)
// and the command is exec'd (so tmux's dead-pane detection reports the COMMAND's
// exit, not a wrapper shell's — which is what makes a crashed dev server show as
// inactive instead of alive).
func TestCommandLineExportsTheEnvFileAndExecsTheCommand(t *testing.T) {
	got := CommandLine("composer dev")

	for _, want := range []string{
		"set -a",
		"[ -f ./.lola/env ]",
		". ./.lola/env",
		"set +a",
		"exec composer dev",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("CommandLine missing %q:\n%s", want, got)
		}
	}
	if !strings.HasPrefix(got, "exec sh -c ") {
		t.Errorf("CommandLine must hand a POSIX sh the posix-only line: %s", got)
	}
}

// The bug this guards: `exec` takes ONE command, so prefixing it onto a
// compound line binds it to the first word only. `exec cd desktop && wails3 dev`
// runs under macOS's /bin/sh (bash 3.2) as a silent no-op that exits 0 — the tab
// dies instantly and the dev server never starts. A compound line must therefore
// reach sh unprefixed, whole and in order.
func TestCommandLineDoesNotExecCompoundLines(t *testing.T) {
	for _, cmd := range []string{
		"cd desktop && wails3 dev",
		"npm run build | tee build.log",
		"cd desktop; npm run dev",
		"php artisan serve > serve.log 2>&1",
		"cd desktop",
	} {
		got := CommandLine(cmd)
		if strings.Contains(got, "exec "+cmd) {
			t.Errorf("CommandLine(%q) still execs a compound line: %s", cmd, got)
		}
		if !strings.Contains(got, exportPrelude+cmd) {
			t.Errorf("CommandLine(%q) must run the line verbatim after the prelude: %s", cmd, got)
		}
		if strings.Count(got, "exec sh -c ") != 1 {
			t.Errorf("CommandLine(%q) want exactly one wrapper: %s", cmd, got)
		}
	}
}

// The exec is kept wherever it is safe, because it is what makes tmux report
// the COMMAND's exit rather than a wrapper shell's. An env-assignment prefix
// stays simple: both bash and dash apply it to the exec'd command.
func TestCommandLineStillExecsSimpleCommands(t *testing.T) {
	for _, cmd := range []string{
		"composer dev",
		"npm run dev",
		"PORT=3000 npm run dev",
		"  wails3 dev  ",
	} {
		got := CommandLine(cmd)
		want := "exec " + strings.TrimSpace(cmd)
		if !strings.Contains(got, want) {
			t.Errorf("CommandLine(%q) dropped the exec: %s", cmd, got)
		}
	}
}

// A command carrying a single quote must survive the trip through the login
// shell intact — the quoting exists for that, not to neuter the command (which
// is user-authored config and is meant to be interpreted by sh, pipes and all).
func TestCommandLineQuotesEmbeddedSingleQuotes(t *testing.T) {
	got := CommandLine(`php -r 'echo 1;'`)
	if !strings.Contains(got, `'\''echo 1;'\''`) {
		t.Errorf("single quotes not escaped for the outer sh -c: %s", got)
	}
	if strings.Count(got, "exec sh -c ") != 1 {
		t.Errorf("want exactly one wrapper: %s", got)
	}
}
