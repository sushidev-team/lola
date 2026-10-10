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

// Hit is one verification command found in a shell line, with where it sits
// in the line's control structure. A line's exit status is only evidence about
// a command when the structure says so: `go test ./... | tail` exits with
// tail's status, `go test ./... && false` fails after a passing test run, and
// `true || go test ./...` never runs the tests at all. Resolve turns a hit plus
// the tool result into an outcome, or into no evidence.
type Hit struct {
	Cat Category
	// gated: an `&&` link before it in its and-or list, so it ran only if
	// that predecessor succeeded (a bare `cd` does not count).
	gated bool
	// own: it is the last command of its pipeline, so the pipeline's status
	// is its own.
	own bool
	// tail: its pipeline is the last of its and-or list.
	tail bool
	// orAfter: an `||` follows it in its list, which can mask its failure.
	orAfter bool
	// final: its list is the line's last one and not backgrounded, so the
	// line's exit status is that list's.
	final bool
}

// outcome is what one hit proves given the line's error flag; ok is false
// when it proves nothing — not even that the command ran.
func (h Hit) outcome(isErr, background bool) (o Outcome, ok bool) {
	switch {
	case background || !h.final || h.orAfter:
		// The line's status is not this list's (or `||` can swallow it):
		// it ran if nothing gated it, result unknown.
		return Unknown, !h.gated
	case !isErr:
		// A zero status from a pure `&&` list: every link ran and succeeded.
		if h.own {
			return Passed, true
		}
		return Unknown, true
	case h.own && h.tail && !h.gated:
		// Nothing before it could have failed instead, nothing after it ran.
		return Failed, true
	default:
		// Some link failed, and it may not be this one — or this one may
		// never have started.
		return Unknown, !h.gated
	}
}

// Result of one line for one category.
type CatOutcome struct {
	Cat     Category
	Outcome Outcome
}

// Resolve turns a line's hits and its tool result into one outcome per
// category it proves anything about. When a category appears more than once,
// a failure outranks a pass, which outranks a bare run.
func Resolve(hits []Hit, isErr, background bool) []CatOutcome {
	rank := map[Outcome]int{Unknown: 0, Passed: 1, Failed: 2}
	var out []CatOutcome
	idx := map[Category]int{}
	for _, h := range hits {
		o, ok := h.outcome(isErr, background)
		if !ok {
			continue
		}
		if i, seen := idx[h.Cat]; seen {
			if rank[o] > rank[out[i].Outcome] {
				out[i].Outcome = o
			}
			continue
		}
		idx[h.Cat] = len(out)
		out = append(out, CatOutcome{h.Cat, o})
	}
	return out
}

// segment is one simple command of a shell line plus the operator after it.
type segment struct {
	text string
	sep  string // "&&", "||", "|", ";", "&", "\n" or "" (end)
}

// splitShell cuts a shell line into simple commands at top-level control
// operators, honouring quotes and backslash escapes. A here-doc's BODY is
// skipped: it is text handed to a command, not commands (`cat <<EOF` with
// `go test ./...` inside runs no tests). It is a scanner, not a shell parser:
// subshells and command substitutions are read as plain text.
func splitShell(line string) []segment {
	var out []segment
	var b strings.Builder
	var quote rune
	esc := false
	rs := []rune(line)
	type heredoc struct {
		delim string
		dash  bool // <<- strips leading tabs from the closing line
	}
	var docs []heredoc
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
		case r == '<' && next == '<' && (i+2 >= len(rs) || rs[i+2] != '<'):
			// A here-doc operator: record its delimiter, keep the text.
			j := i + 2
			d := heredoc{}
			if j < len(rs) && rs[j] == '-' {
				d.dash = true
				j++
			}
			for j < len(rs) && (rs[j] == ' ' || rs[j] == '\t') {
				j++
			}
			var w strings.Builder
			for j < len(rs) && !strings.ContainsRune(" \t\n;&|<>()", rs[j]) {
				if rs[j] != '\'' && rs[j] != '"' && rs[j] != '\\' {
					w.WriteRune(rs[j])
				}
				j++
			}
			b.WriteString(string(rs[i:j]))
			i = j - 1
			if w.Len() > 0 {
				d.delim = w.String()
				docs = append(docs, d)
			}
		case r == '\n' && len(docs) > 0:
			flush("\n")
			// Skip each pending body through its closing line.
			for _, d := range docs {
				for i+1 < len(rs) {
					end := i + 1
					for end < len(rs) && rs[end] != '\n' {
						end++
					}
					ln := string(rs[i+1 : end])
					i = end
					if d.dash {
						ln = strings.TrimLeft(ln, "\t")
					}
					if ln == d.delim {
						break
					}
				}
			}
			docs = nil
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

// Classify reports every verification command in one shell line, with its
// place in the line's structure (see Hit). A command after an `||` in its
// and-or list is dropped outright: whether it ran depends on a status nothing
// records.
func Classify(line string) []Hit {
	type pipe []string
	type list struct {
		pipes []pipe
		ops   []string // ops[i] links pipes[i] and pipes[i+1]: "&&" or "||"
		bg    bool
	}
	var lists []list
	var cur list
	var p pipe
	for _, sg := range splitShell(line) {
		if sg.text != "" {
			p = append(p, sg.text)
		}
		switch sg.sep {
		case "|":
			continue
		case "&&", "||":
			if len(p) > 0 {
				cur.pipes = append(cur.pipes, p)
				cur.ops = append(cur.ops, sg.sep)
			}
		default: // ";", "\n", "&", end of line
			if len(p) > 0 {
				cur.pipes = append(cur.pipes, p)
			}
			if len(cur.pipes) > 0 {
				// A dangling operator (`a &&` then newline) links nothing.
				cur.ops = cur.ops[:len(cur.pipes)-1]
				cur.bg = sg.sep == "&"
				lists = append(lists, cur)
			}
			cur = list{}
		}
		p = nil
	}

	var out []Hit
	for li, l := range lists {
		for pi, pp := range l.pipes {
			if slices.Contains(l.ops[:pi], "||") {
				continue
			}
			gated := false
			for _, prev := range l.pipes[:pi] {
				gated = gated || !isCD(prev)
			}
			for ci, cmd := range pp {
				for _, c := range classifySimple(normalize(cmd)) {
					out = append(out, Hit{
						Cat:     c,
						gated:   gated,
						own:     ci == len(pp)-1,
						tail:    pi == len(l.pipes)-1,
						orAfter: slices.Contains(l.ops[pi:], "||"),
						final:   li == len(lists)-1 && !l.bg,
					})
				}
			}
		}
	}
	return out
}

// isCD reports whether a pipeline only changes directory. `cd dir && go test`
// is the usual way to run a check in a subdirectory, and a cd that fails is
// rare enough not to cost every such run its outcome.
func isCD(p []string) bool {
	if len(p) != 1 {
		return false
	}
	f := strings.Fields(normalize(p[0]))
	return len(f) > 0 && (f[0] == "cd" || f[0] == "pushd")
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
