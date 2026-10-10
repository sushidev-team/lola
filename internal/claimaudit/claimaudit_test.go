package claimaudit

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sushidev-team/lola/internal/board"
)

func TestCategoryForCheck(t *testing.T) {
	cases := map[string]Category{
		"tests": Test, "go-test": Test, "Unit": Test, "e2e": Test, "phpunit": Test,
		"lint": Lint, "vet": Lint, "typecheck": Lint, "svelte-check": Lint,
		"build": Build, "compile": Build,
		"": "", "docs": "", "manual": "", "smoke": "",
	}
	for name, want := range cases {
		if got := CategoryForCheck(name); got != want {
			t.Errorf("CategoryForCheck(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestClassify(t *testing.T) {
	type hit = Hit
	cases := []struct {
		line string
		want []Hit
	}{
		{"go test ./...", []hit{{Test, true}}},
		{"GOCACHE=$PWD/.gocache GOFLAGS='-mod=mod -buildvcs=false' go test ./internal/daemon -run X -v", []hit{{Test, true}}},
		{"cd desktop/frontend && npm test", []hit{{Test, true}}},
		{"cd desktop/frontend && npx vitest run src/lib", []hit{{Test, true}}},
		{"npm run test:unit -- --reporter=dot", []hit{{Test, true}}},
		{"php artisan test --filter Foo", []hit{{Test, true}}},
		{"./vendor/bin/pest", []hit{{Test, true}}},
		{"python -m pytest -q", []hit{{Test, true}}},
		{"make test", []hit{{Test, true}}},
		{"make -C desktop test", []hit{{Test, true}}},
		{"make check", []hit{{Build, true}, {Lint, true}, {Test, true}}},
		{"make build && make vet", []hit{{Build, true}, {Lint, true}}},
		// The line's exit status is not the runner's.
		{"go test ./... | tail -20", []hit{{Test, false}}},
		{"go test ./... 2>&1 | tail -20", []hit{{Test, false}}},
		{"go test ./... ; echo done", []hit{{Test, false}}},
		{"go test ./... || true", []hit{{Test, false}}},
		{"go test ./... &", []hit{{Test, false}}},
		// A trailing ; or newline closes the line harmlessly.
		{"go test ./...;", []hit{{Test, true}}},
		{"go test ./...\n", []hit{{Test, true}}},
		// A trusted repetition wins over an untrusted one.
		{"go test ./... | tail; go test ./...", []hit{{Test, true}}},
		// Mentions are not runs.
		{`grep -rn "go test" docs`, nil},
		{`echo "make test"`, nil},
		{"git commit -m 'run go test'", nil},
		{"cat internal/claimaudit/classify.go", nil},
		{"go vet ./...", []hit{{Lint, true}}},
		{"npm run lint", []hit{{Lint, true}}},
		{"go build ./...", []hit{{Build, true}}},
		{"time go test ./...", []hit{{Test, true}}},
		{"(cd sub && go test ./...)", []hit{{Test, true}}},
	}
	for _, c := range cases {
		got := Classify(c.line)
		if fmt.Sprint(got) != fmt.Sprint(c.want) {
			t.Errorf("Classify(%q) = %v, want %v", c.line, got, c.want)
		}
	}
}

// transcript builds a Claude Code-shaped JSONL transcript.
type transcript struct {
	b  strings.Builder
	at time.Time
	n  int
}

func (tr *transcript) add(rec map[string]any) {
	tr.at = tr.at.Add(time.Second)
	rec["timestamp"] = tr.at.Format(time.RFC3339Nano)
	raw, _ := json.Marshal(rec)
	tr.b.Write(raw)
	tr.b.WriteByte('\n')
}

// bash records one Bash call and its result; it returns the result's time.
func (tr *transcript) bash(cmd string, isErr, background bool) time.Time {
	tr.n++
	id := fmt.Sprintf("toolu_%d", tr.n)
	input := map[string]any{"command": cmd}
	if background {
		input["run_in_background"] = true
	}
	tr.add(map[string]any{"type": "assistant", "message": map[string]any{"role": "assistant",
		"content": []any{map[string]any{"type": "tool_use", "id": id, "name": "Bash", "input": input}}}})
	tr.add(map[string]any{"type": "user", "message": map[string]any{"role": "user",
		"content": []any{map[string]any{"type": "tool_result", "tool_use_id": id, "is_error": isErr, "content": "out"}}}})
	return tr.at
}

func (tr *transcript) prose(text string) {
	tr.add(map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": text}})
}

func write(t *testing.T, path, data string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestScannerLedger(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t.jsonl")
	tr := &transcript{at: time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)}
	tr.prose("please run go test")
	tr.bash("cat README.md", false, false)
	failAt := tr.bash("go test ./...", true, false)
	passAt := tr.bash("make check", false, false)
	tr.bash("go test ./... | tail", false, false)
	tr.bash("npm test", false, true)
	write(t, path, tr.b.String())

	s := NewScanner()
	s.Scan(path)
	l, ok := s.Ledger(path)
	if !ok || !l.Complete {
		t.Fatalf("ledger ok=%v complete=%v", ok, l.Complete)
	}
	want := []Run{{failAt, Failed}, {passAt, Passed}, {tr.at.Add(-2 * time.Second), Unknown}, {tr.at, Unknown}}
	if fmt.Sprint(l.Runs[Test]) != fmt.Sprint(want) {
		t.Errorf("test runs = %v, want %v", l.Runs[Test], want)
	}
	if len(l.Runs[Lint]) != 1 || len(l.Runs[Build]) != 1 {
		t.Errorf("make check must count as lint + build too: %v", l.Runs)
	}
}

func TestScannerIncrementalAndPartialLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t.jsonl")
	tr := &transcript{at: time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)}
	tr.bash("go test ./...", false, false)
	full := tr.b.String()
	// The result line is still being written: only its first half is on disk.
	cut := strings.LastIndex(full[:len(full)-1], "\n") + 1 + 20
	write(t, path, full[:cut])
	s := NewScanner()
	s.Scan(path)
	if l, _ := s.Ledger(path); l.Complete || len(l.Runs[Test]) != 0 {
		t.Fatalf("a partial line must leave the ledger incomplete and unjudged: %+v", l)
	}
	write(t, path, full)
	s.Scan(path)
	if l, _ := s.Ledger(path); !l.Complete || len(l.Runs[Test]) != 1 || l.Runs[Test][0].Outcome != Passed {
		t.Fatalf("after the line completes: %+v", l)
	}
	// Truncation (a replaced file) starts over rather than reading garbage.
	write(t, path, "")
	s.Scan(path)
	if l, _ := s.Ledger(path); !l.Complete || len(l.Runs[Test]) != 0 {
		t.Fatalf("a shrunk file must reset: %+v", l)
	}
	// A vanished file is forgotten.
	os.Remove(path)
	s.Scan(path)
	if _, ok := s.Ledger(path); ok {
		t.Fatal("a missing file must be forgotten")
	}
}

func TestScannerRetain(t *testing.T) {
	dir := t.TempDir()
	a, b := filepath.Join(dir, "a"), filepath.Join(dir, "b")
	write(t, a, "")
	write(t, b, "")
	s := NewScanner()
	s.Scan(a)
	s.Scan(b)
	s.Retain(map[string]bool{a: true})
	if s.Len() != 1 {
		t.Fatalf("Len = %d, want 1", s.Len())
	}
}

func TestAudit(t *testing.T) {
	t0 := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	claim := func(name string, at time.Time) board.Board {
		return board.Board{Checks: []board.Check{{Name: name, State: board.CheckPass, At: at}}}
	}
	ledger := func(runs ...Run) *Ledger { return &Ledger{Runs: map[Category][]Run{Test: runs}, Complete: true} }

	cases := []struct {
		name     string
		b        board.Board
		f        Facts
		verdict  Verdict
		warnings int
	}{
		{"no test run at all", claim("tests", t0), Facts{Ledger: ledger()}, Unverified, 1},
		{"only a run AFTER the claim", claim("tests", t0), Facts{Ledger: ledger(Run{t0.Add(time.Minute), Passed})}, Unverified, 1},
		{"passed before the claim", claim("tests", t0), Facts{Ledger: ledger(Run{t0.Add(-time.Minute), Passed})}, Verified, 0},
		{"result just after the claim (slack)", claim("tests", t0), Facts{Ledger: ledger(Run{t0.Add(2 * time.Second), Passed})}, Verified, 0},
		{"last run failed", claim("tests", t0), Facts{Ledger: ledger(Run{t0.Add(-2 * time.Minute), Passed}, Run{t0.Add(-time.Minute), Failed})}, Contradicted, 1},
		{"masked status", claim("tests", t0), Facts{Ledger: ledger(Run{t0.Add(-time.Minute), Unknown})}, Ran, 0},
		{"pre-At record: any run counts", claim("tests", time.Time{}), Facts{Ledger: ledger(Run{t0, Passed})}, Verified, 0},
		{"incomplete scan judges nothing", claim("tests", t0), Facts{Ledger: &Ledger{Complete: false}}, "", 0},
		{"no transcript judges nothing", claim("tests", t0), Facts{}, "", 0},
		{"unknown category is not audited", claim("docs", t0), Facts{Ledger: ledger()}, "", 0},
		{"CI failing contradicts a test claim", claim("tests", t0), Facts{Ledger: ledger(Run{t0.Add(-time.Minute), Passed}), CIFailed: true}, Verified, 1},
		{"CI failing contradicts done", board.Board{Phase: board.PhaseDone}, Facts{CIFailed: true}, "", 1},
		{"a fail claim is not audited", board.Board{Checks: []board.Check{{Name: "tests", State: board.CheckFail, At: t0}}}, Facts{Ledger: ledger()}, "", 0},
	}
	for _, c := range cases {
		r := Audit(c.b, c.f)
		var got Verdict
		if len(c.b.Checks) > 0 {
			got = r.Checks[c.b.Checks[0].Name].Verdict
		}
		if got != c.verdict || len(r.Warnings) != c.warnings {
			t.Errorf("%s: verdict %q warnings %v, want %q and %d warning(s)", c.name, got, r.Warnings, c.verdict, c.warnings)
		}
	}
}

// TestAcceptanceClaimWithoutRun is SUSHI-622's acceptance case end to end: an
// agent that reports passing tests without having run any gets a warning.
func TestAcceptanceClaimWithoutRun(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t.jsonl")
	tr := &transcript{at: time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)}
	tr.bash("git diff --stat", false, false)
	tr.bash(`echo "go test ./... passed"`, false, false)
	write(t, path, tr.b.String())
	s := NewScanner()
	s.Scan(path)
	l, _ := s.Ledger(path)
	b, err := board.Apply(board.Board{}, []string{"check", "tests", "pass", "142/142"}, tr.at.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	r := Audit(b, Facts{Ledger: &l})
	if len(r.Warnings) != 1 || !strings.Contains(r.Warnings[0], "no test command ran") {
		t.Fatalf("warnings = %v", r.Warnings)
	}
}
