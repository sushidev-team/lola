// Package claimaudit checks what a coding agent SAYS it verified against what
// its own transcript shows it ran.
//
// An agent publishes `lola report check tests pass` (internal/board), and that
// is a CLAIM: the board is display-only precisely because the agent's context
// is attacker-influenceable and because agents simply say things that are not
// so ("all tests pass" with no test run in sight). This package is the
// deterministic second opinion: it reads the Bash commands recorded in Claude
// Code's JSONL transcript, recognises test / lint / build runners, pairs each
// with its tool result's error flag, and answers per claimed check whether a
// matching command ran before the claim and how it ended.
//
// # What is read, and where it goes
//
// The transcript is model output plus tool output. From it this package reads
// a tool_use block's name-independent `command` / `run_in_background` input and
// a tool_result block's `is_error` flag + id — and NOTHING leaves the package
// but a closed Verdict vocabulary and a category word. No command text, no tool
// output, is ever rendered, logged or sent to an agent. The audit result is
// DISPLAY-ONLY, on exactly the same footing as the board it audits: it reaches
// SessionInfo.board and no control-loop reader (axes, slots, reactions,
// write-back, send-keys gates, dispatch).
//
// # Fail toward silence
//
// A warning is only raised on positive evidence of a mismatch over a FULLY read
// transcript. An unknown agent kind, a missing file, a scan still catching up,
// a check name that names no known category — all of them audit to nothing, so
// this package can add a warning a human should look at but never a false alarm
// from not knowing.
package claimaudit

import (
	"regexp"
	"slices"
	"strings"
)

// Category is what kind of verification a check or a command is.
type Category string

const (
	Test  Category = "test"
	Lint  Category = "lint"
	Build Category = "build"
)

// categoryWords maps a check-name token to its category. Checked Test → Lint →
// Build, so "unit-build" still reads as a test claim (the stronger one).
var categoryWords = []struct {
	cat   Category
	words []string
}{
	{Test, []string{"test", "tests", "testing", "unit", "e2e", "spec", "specs", "integration",
		"phpunit", "pest", "vitest", "jest", "pytest", "rspec", "playwright", "cypress"}},
	{Lint, []string{"lint", "linter", "linting", "vet", "eslint", "fmt", "format", "formatting",
		"typecheck", "types", "tsc", "phpstan", "pint", "clippy", "staticcheck", "ruff", "mypy",
		"svelte-check", "prettier", "biome", "rubocop", "shellcheck"}},
	{Build, []string{"build", "builds", "compile", "compiles", "bundle"}},
}

// CategoryForCheck maps a board check name ("tests", "go-test", "lint") to the
// category it claims, "" when the name names none — such a check is not
// audited at all.
func CategoryForCheck(name string) Category {
	name = strings.ToLower(strings.TrimSpace(name))
	toks := strings.FieldsFunc(name, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9')
	})
	toks = append(toks, name) // "svelte-check" as a whole
	for _, cw := range categoryWords {
		for _, t := range toks {
			if slices.Contains(cw.words, t) {
				return cw.cat
			}
		}
	}
	return ""
}

// Hit is one verification command found in a shell line.
type Hit struct {
	Cat Category
	// Trusted reports whether the shell line's exit status is the command's
	// own: it is followed by nothing or only by `&&` links. Behind a pipe, a
	// `;`, an `||` or a `&` the line's status belongs to something else (`go
	// test ./... | tail` exits with tail's status), so the run counts as RAN
	// but neither as passed nor as failed.
	Trusted bool
}

// segment is one simple command of a shell line plus the operator after it.
type segment struct {
	text string
	sep  string // "&&", "||", "|", ";", "&", "\n" or "" (end)
}

// splitShell cuts a shell line into simple commands at top-level control
// operators, honouring quotes and backslash escapes. It is a scanner, not a
// shell parser: subshells and here-docs are read as plain text, which can only
// cost a missed hit (no warning) — never a hit that was not typed.
func splitShell(line string) []segment {
	var out []segment
	var b strings.Builder
	var quote rune
	esc := false
	rs := []rune(line)
	flush := func(sep string) {
		out = append(out, segment{text: strings.TrimSpace(b.String()), sep: sep})
		b.Reset()
	}
	for i := 0; i < len(rs); i++ {
		r := rs[i]
		switch {
		case esc:
			esc = false
			b.WriteRune(r)
			continue
		case r == '\\' && quote != '\'':
			esc = true
			b.WriteRune(r)
			continue
		case quote != 0:
			if r == quote {
				quote = 0
			}
			b.WriteRune(r)
			continue
		case r == '\'' || r == '"':
			quote = r
			b.WriteRune(r)
			continue
		}
		next := rune(0)
		if i+1 < len(rs) {
			next = rs[i+1]
		}
		switch {
		case r == '&' && next == '&':
			flush("&&")
			i++
		case r == '|' && next == '|':
			flush("||")
			i++
		case r == '|':
			flush("|")
		case r == ';' || r == '\n':
			flush(string(r))
		case r == '&' && next == '>':
			b.WriteRune(r) // &> redirect
		case r == '&' && i > 0 && (rs[i-1] == '>' || rs[i-1] == '<'):
			b.WriteRune(r) // 2>&1
		case r == '&':
			flush("&")
		default:
			b.WriteRune(r)
		}
	}
	flush("")
	return out
}

var (
	envAssignRe = regexp.MustCompile(`^(?:[A-Za-z_][A-Za-z0-9_]*=(?:'[^']*'|"[^"]*"|\S*)\s+)+`)
	// wrapperRe strips a launcher that runs the real command as its argument.
	wrapperRe = regexp.MustCompile(`^(?:time|env|command|exec|nice|nohup|npx|bunx|pnpm\s+exec|pnpm\s+dlx|yarn\s+dlx|uv\s+run|poetry\s+run|pipenv\s+run|bundle\s+exec|timeout\s+\S+|gtimeout\s+\S+)\s+`)
	groupRe   = regexp.MustCompile(`^[({]\s*`)
)

// normalize peels a subshell/group opener, env assignments and launchers off a
// simple command so the runner is the first word.
func normalize(s string) string {
	s = groupRe.ReplaceAllString(s, "")
	for range 8 {
		before := s
		s = envAssignRe.ReplaceAllString(s, "")
		s = wrapperRe.ReplaceAllString(s, "")
		s = strings.TrimSpace(s)
		if s == before {
			break
		}
	}
	return s
}

// runnerRe is one runner pattern per category, anchored at the normalized
// command's first word. A Make-style target runner is matched separately
// (targetRunners) because its target can follow flags.
var runnerRe = map[Category]*regexp.Regexp{
	Test: regexp.MustCompile(`^(?:` + strings.Join([]string{
		`go\s+test`, `gotestsum`,
		`(?:npm|pnpm|yarn|bun)\s+(?:run\s+)?(?:test|t)(?::\S+)?`,
		`(?:bun|deno|dart|flutter|swift|cargo|mix|dotnet|zig\s+build|rails)\s+test`,
		`cargo\s+nextest`, `vitest`, `jest`, `mocha`, `ava`, `playwright\s+test`, `cypress\s+run`,
		`(?:python3?\s+-m\s+)?pytest`, `python3?\s+-m\s+unittest`, `tox`, `nox`,
		`php\s+artisan\s+test`, `(?:\./)?(?:vendor/bin/)?(?:phpunit|pest|paratest)`,
		`composer\s+(?:run(?:-script)?\s+)?test(?::\S+)?`,
		`rspec`, `rake\s+(?:test|spec)`, `ctest`,
		`(?:\./)?gradlew?\s+(?:\S+\s+)*?(?:test|check)`, `mvn\s+(?:\S+\s+)*?(?:test|verify)`,
		`xcodebuild\s+(?:\S+\s+)*?test`,
	}, "|") + `)(?:\s|$)`),
	Lint: regexp.MustCompile(`^(?:` + strings.Join([]string{
		`go\s+vet`, `gofmt\s+-l`, `golangci-lint`, `staticcheck`,
		`(?:npm|pnpm|yarn|bun)\s+(?:run\s+)?(?:lint|check|typecheck|type-check|svelte-check|format:check|fmt:check)(?::\S+)?`,
		`eslint`, `tsc`, `vue-tsc`, `svelte-check`, `prettier\s+(?:\S+\s+)*?--check`, `biome\s+(?:check|lint|ci)`,
		`ruff`, `flake8`, `mypy`, `pyright`, `pylint`, `black\s+(?:\S+\s+)*?--check`,
		`(?:\./)?(?:vendor/bin/)?(?:phpstan|pint|php-cs-fixer|psalm)`, `composer\s+(?:run(?:-script)?\s+)?(?:lint|analyse|stan)`,
		`cargo\s+(?:clippy|check|fmt\s+(?:\S+\s+)*?--check)`, `rubocop`, `swiftlint`, `shellcheck`,
	}, "|") + `)(?:\s|$)`),
	Build: regexp.MustCompile(`^(?:` + strings.Join([]string{
		`go\s+(?:build|install)`,
		`(?:npm|pnpm|yarn|bun)\s+(?:run\s+)?build(?::\S+)?`,
		`(?:cargo|swift|dotnet|zig)\s+build`, `vite\s+build`, `tsc`, `xcodebuild`,
		`wails3?\s+(?:build|package|task\s+(?:build|package))`,
		`(?:\./)?gradlew?\s+(?:\S+\s+)*?(?:build|assemble)`, `mvn\s+(?:\S+\s+)*?(?:package|compile|install)`,
		`docker\s+(?:compose\s+)?build`,
	}, "|") + `)(?:\s|$)`),
}

// targetRunners run named targets that may come after flags (`make -C x test`).
var targetRunners = map[string]bool{"make": true, "gmake": true, "just": true, "task": true}

// targetCats maps a make/just/task target to what it verifies. `check` and
// `ci` are the conventional all-in-one targets (this repo's `make check` is
// build + vet + test).
var targetCats = map[string][]Category{
	"test": {Test}, "tests": {Test}, "unit": {Test}, "e2e": {Test}, "spec": {Test},
	"check": {Build, Lint, Test}, "ci": {Build, Lint, Test}, "verify": {Build, Lint, Test},
	"lint": {Lint}, "vet": {Lint}, "fmt-check": {Lint}, "typecheck": {Lint}, "analyse": {Lint},
	"build": {Build}, "all": {Build}, "compile": {Build},
}

// Classify reports every verification command in one shell line. A category
// appears at most once; when it appears in several segments the TRUSTED one
// wins, so `go test ./... && go test -race ./...` is still a trusted run.
func Classify(line string) []Hit {
	segs := splitShell(line)
	found := map[Category]bool{} // value: trusted
	var order []Category
	for i, sg := range segs {
		if sg.text == "" {
			continue
		}
		// The line's status is the runner's only while every operator from
		// here to the last real command is `&&`. A trailing `;` or newline
		// closes the line harmlessly; a trailing `&` backgrounds the runner.
		trusted := true
		for j := i; j < len(segs); j++ {
			if segs[j].sep == "&" {
				trusted = false
				break
			}
			if isLastNonEmpty(segs, j) {
				break
			}
			if segs[j].sep != "&&" {
				trusted = false
				break
			}
		}
		for _, c := range classifySimple(normalize(sg.text)) {
			if prev, ok := found[c]; !ok {
				order = append(order, c)
				found[c] = trusted
			} else if trusted && !prev {
				found[c] = true
			}
		}
	}
	out := make([]Hit, 0, len(order))
	for _, c := range order {
		out = append(out, Hit{Cat: c, Trusted: found[c]})
	}
	return out
}

// isLastNonEmpty reports whether no non-empty segment follows segs[i].
func isLastNonEmpty(segs []segment, i int) bool {
	for _, s := range segs[i+1:] {
		if s.text != "" {
			return false
		}
	}
	return true
}

func classifySimple(cmd string) []Category {
	if cmd == "" {
		return nil
	}
	fields := strings.Fields(cmd)
	if targetRunners[fields[0]] {
		var out []Category
		seen := map[Category]bool{}
		skipNext := false
		for _, f := range fields[1:] {
			if skipNext {
				skipNext = false
				continue
			}
			if strings.HasPrefix(f, "-") {
				// -C dir / -f file take a value; -j4, --foo=bar do not.
				skipNext = f == "-C" || f == "-f" || f == "--directory" || f == "--file" || f == "-d"
				continue
			}
			if strings.Contains(f, "=") {
				continue // VAR=value make argument
			}
			for _, c := range targetCats[strings.ToLower(f)] {
				if !seen[c] {
					seen[c] = true
					out = append(out, c)
				}
			}
		}
		return out
	}
	var out []Category
	for _, c := range []Category{Test, Lint, Build} {
		if runnerRe[c].MatchString(cmd) {
			out = append(out, c)
		}
	}
	return out
}
