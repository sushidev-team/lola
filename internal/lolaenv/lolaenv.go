// Package lolaenv holds the one contract shared by everything that starts an
// interactive shell in a worktree: where the per-session env file lives, and
// the tmux command that exports it.
//
// It exists so the agent pane, the TUI's shell tabs and the desktop app's
// terminal cannot drift apart. A shell that skips the env file silently loses
// [[project]].env — the vars the project's own commands are configured with —
// which is invisible until something misbehaves at a distance.
package lolaenv

import "strings"

// Dir is the per-worktree directory lola keeps its session artifacts in,
// relative to the worktree root. Git-excluded, mode 0700.
const Dir = ".lola"

// File is the per-worktree env file, relative to the worktree root. Mode 0600:
// it can carry LINEAR_API_KEY.
const File = Dir + "/env"

// ShellCommand is the single command argument for `tmux new-session` that
// starts the user's interactive login shell with File exported.
//
// tmux hands that trailing argument to the user's LOGIN shell, which may be
// fish/csh/tcsh rather than a POSIX sh — so the POSIX-only part (`set -a`, `.`,
// `set +a`) is wrapped in an explicit `sh -c`, and the login shell only has to
// exec `sh`. The file is sourced conditionally, so a worktree without one (a
// plain `git worktree`, an older session) still gets a working shell instead of
// an error. `exec` twice over so the pane's process IS the interactive shell:
// no wrapper lingers to swallow signals or skew tmux's dead-pane detection.
//
// Every byte here is a literal — nothing user-supplied is interpolated, which
// is why this can be a constant rather than something quoted at run time.
const ShellCommand = `exec sh -c 'set -a; [ -f ./` + File + ` ] && . ./` + File + `; set +a; exec "${SHELL:-/bin/sh}"'`

// exportPrelude is the POSIX-only part ShellCommand and CommandLine share: the
// env file exported into the process that follows it.
const exportPrelude = `set -a; [ -f ./` + File + ` ] && . ./` + File + `; set +a; `

// CommandLine is the single command argument for `tmux new-session` that runs
// ONE command line with File exported — the dev tabs' equivalent of
// ShellCommand (internal/daemon/dev.go runs a project's dev_commands with it).
//
// command is a user-authored shell line from config, so it is deliberately
// INTERPRETED by sh (pipes, &&, env prefixes all work, exactly as
// [[project]].post_create is run) — it is quoted only so it survives the trip
// through the login shell intact, not to neuter it.
//
// A SIMPLE command runs under `exec`, so the pane's process IS the command:
// tmux's dead-pane detection then reports the command's own exit, which is what
// makes a crashed dev server visible instead of a wrapper shell sitting there
// looking alive. Anything else — a pipeline, an `&&` chain, a leading `cd` —
// runs WITHOUT it, because `exec` takes a single command and prefixing it onto
// a compound line does not merely lose the optimization, it silently eats the
// line: `exec cd desktop && npm run dev` binds `exec` to `cd` alone, and bash
// then leaves the pane dead at exit 0 with the actual command never started.
// Dropping `exec` costs only the wrapper shell, which waits for the command and
// exits with it, so the dead pane still appears at the right moment.
func CommandLine(command string) string {
	line := strings.TrimSpace(command)
	if isSimpleCommand(line) {
		line = "exec " + line
	}
	return "exec sh -c " + shQuote(exportPrelude+line)
}

// commandSeparators are the characters that can make a line more than one
// command (or make its first word something other than the command that ends up
// running). Deliberately conservative: a false "not simple" only drops the
// `exec` optimization, a false "simple" breaks the whole command line.
const commandSeparators = "\n;&|<>()`{}"

// shellWordBreakers are the builtins and keywords that cannot be `exec`ed —
// `exec` runs an external command, so `exec cd …` is at best an error and at
// worst (bash 3.2, macOS's /bin/sh) a silent no-op that swallows the rest.
var shellWordBreakers = map[string]bool{
	"cd": true, ".": true, "source": true, "export": true, "set": true,
	"unset": true, "eval": true, "exec": true, "exit": true, "shift": true,
	"trap": true, "umask": true, "wait": true, "alias": true, "read": true,
	"local": true, "return": true, "times": true, "ulimit": true,
	"if": true, "for": true, "while": true, "until": true, "case": true,
	"function": true, "time": true, "!": true, "{": true, "(": true,
}

// isSimpleCommand reports whether line is one plain command that `exec` can
// take over — no separators, no builtin/keyword head. An env-assignment prefix
// (`PORT=3000 npm run dev`) stays simple: both bash and dash apply the
// assignment to the exec'd command.
func isSimpleCommand(line string) bool {
	if line == "" {
		return false
	}
	if strings.ContainsAny(line, commandSeparators) {
		return false
	}
	head := line
	if i := strings.IndexAny(head, " \t"); i >= 0 {
		head = head[:i]
	}
	return !shellWordBreakers[head]
}

// shQuote single-quotes s for a POSIX shell. An embedded quote goes through the
// usual close-escape-reopen dance:
//
//	'\''
//
// which is written as an indented code block on purpose. gofmt rewrites a pair
// of adjacent apostrophes in doc-comment PROSE into a typographic close-quote,
// so spelling the idiom out inline made this file permanently gofmt-unclean and
// mangled the one thing the comment exists to show. A code block is left
// verbatim.
//
// Kept here rather than imported so this package stays stdlib-free and the one
// contract lives in one file.
func shQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
