package scm

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// ghResp is one canned response for the routing fake gh.
type ghResp struct {
	out  string
	err  string
	code int
}

// fakeGhRouter installs a gh stand-in that dispatches on the subcommand pair:
// it looks up a route file keyed "resp_<$1>_<$2>" (e.g. "pr_checks",
// "run_view", "pr_view"), falling back to "resp_<$1>" (e.g. "api"). Each route
// emits its canned stdout/stderr and exit code; unknown routes exit 0 empty.
// Every invocation's argv is appended to <dir>/args.log. No real gh runs.
func fakeGhRouter(t *testing.T, routes map[string]ghResp) (bin, argsLog string) {
	t.Helper()
	dir := t.TempDir()
	bin = filepath.Join(dir, "gh")
	argsLog = filepath.Join(dir, "args.log")
	for key, r := range routes {
		w := func(ext, body string) {
			if err := os.WriteFile(filepath.Join(dir, "resp_"+key+ext), []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		w(".out", r.out)
		w(".err", r.err)
		w(".code", strconv.Itoa(r.code))
	}
	script := "#!/bin/sh\n" +
		"echo \"$@\" >> \"" + argsLog + "\"\n" +
		"dir=\"" + dir + "\"\n" +
		"key=\"$1_$2\"\n" +
		"if [ ! -f \"$dir/resp_$key.out\" ]; then key=\"$1\"; fi\n" +
		"if [ -f \"$dir/resp_$key.out\" ]; then\n" +
		"  cat \"$dir/resp_$key.out\"\n" +
		"  [ -f \"$dir/resp_$key.err\" ] && cat \"$dir/resp_$key.err\" >&2\n" +
		"  exit \"$(cat \"$dir/resp_$key.code\")\"\n" +
		"fi\n" +
		"exit 0\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin, argsLog
}

const failingChecksFixture = `[
  {"name":"test","state":"FAILURE","bucket":"fail","link":"https://github.com/acme/nori/actions/runs/12345/job/67890","workflow":"CI"},
  {"name":"lint","state":"SUCCESS","bucket":"pass","link":"https://github.com/acme/nori/actions/runs/12345/job/67891","workflow":"CI"},
  {"name":"build","state":"CANCELLED","bucket":"cancel","link":"https://github.com/acme/nori/actions/runs/99999/job/1","workflow":"CI"}
]`

const failedLogFixture = "##[group]Run tests\nFAIL github.com/acme/nori/foo\n--- FAIL: TestBar (0.00s)\n    foo_test.go:12: expected 1 got 2\nexit status 1\n##[error]Process completed with exit code 1."

func TestFailingChecksParsesAndFetchesLogs(t *testing.T) {
	// gh pr checks exits 1 when a check is failing — must NOT be treated as an
	// error, since valid JSON is on stdout.
	bin, argsLog := fakeGhRouter(t, map[string]ghResp{
		"pr_checks": {out: failingChecksFixture, code: 1},
		"run_view":  {out: failedLogFixture, code: 0},
	})
	c := &Client{GhBin: bin}

	got, err := c.FailingChecks(context.Background(), "acme/nori", 42)
	if err != nil {
		t.Fatalf("FailingChecks: %v", err)
	}
	// The two failing checks (FAILURE + CANCELLED) named; the SUCCESS omitted.
	for _, want := range []string{
		"2 failing check(s) on PR #42:",
		"CI / test: FAILURE",
		"CI / build: CANCELLED",
		"foo_test.go:12: expected 1 got 2",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("FailingChecks output missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "lint") {
		t.Errorf("passing check leaked into failing summary:\n%s", got)
	}
	if len(got) > reactionMaxBytes {
		t.Errorf("output %d bytes exceeds bound %d", len(got), reactionMaxBytes)
	}
	// The pr checks call carries the exact json field set and both distinct
	// runs (12345, 99999) had their logs pulled.
	args := readFile(t, argsLog)
	if !strings.Contains(args, "pr checks 42 --repo acme/nori --json name,state,bucket,link,workflow") {
		t.Errorf("unexpected pr checks argv:\n%s", args)
	}
	if !strings.Contains(args, "run view 12345 --repo acme/nori --log-failed") ||
		!strings.Contains(args, "run view 99999 --repo acme/nori --log-failed") {
		t.Errorf("expected --log-failed pulls for both runs:\n%s", args)
	}
}

func TestFailingChecksNoFailing(t *testing.T) {
	bin, _ := fakeGhRouter(t, map[string]ghResp{
		"pr_checks": {out: `[{"name":"test","state":"SUCCESS","bucket":"pass"}]`, code: 0},
	})
	c := &Client{GhBin: bin}

	got, err := c.FailingChecks(context.Background(), "acme/nori", 42)
	if err != nil {
		t.Fatalf("FailingChecks: %v", err)
	}
	if got != "" {
		t.Errorf("no failing checks must return empty string, got %q", got)
	}
}

func TestFailingChecksEmptyArray(t *testing.T) {
	bin, _ := fakeGhRouter(t, map[string]ghResp{"pr_checks": {out: `[]`, code: 0}})
	c := &Client{GhBin: bin}

	got, err := c.FailingChecks(context.Background(), "acme/nori", 42)
	if err != nil || got != "" {
		t.Fatalf("FailingChecks([]) = (%q, %v), want empty/nil", got, err)
	}
}

func TestFailingChecksGhErrorIsDistinct(t *testing.T) {
	// Real gh failure: no parseable stdout + non-zero exit + stderr. This must
	// be an error, never conflated with "no failing checks".
	bin, _ := fakeGhRouter(t, map[string]ghResp{
		"pr_checks": {out: "", err: "gh: authentication required", code: 4},
	})
	c := &Client{GhBin: bin}

	got, err := c.FailingChecks(context.Background(), "acme/nori", 42)
	if err == nil {
		t.Fatalf("FailingChecks = %q, want error on gh failure", got)
	}
	if !strings.Contains(err.Error(), "authentication required") {
		t.Errorf("error %q should surface gh stderr", err)
	}
}

func TestFailingChecksNoRunLinkSkipsLogs(t *testing.T) {
	// An external status context (no Actions run link) is still named, but no
	// `gh run view` is attempted.
	bin, argsLog := fakeGhRouter(t, map[string]ghResp{
		"pr_checks": {out: `[{"name":"deploy/preview","state":"FAILURE","bucket":"fail","link":"https://vercel.com/x","workflow":""}]`, code: 1},
	})
	c := &Client{GhBin: bin}

	got, err := c.FailingChecks(context.Background(), "acme/nori", 7)
	if err != nil {
		t.Fatalf("FailingChecks: %v", err)
	}
	if !strings.Contains(got, "deploy/preview: FAILURE") {
		t.Errorf("external status context not named:\n%s", got)
	}
	if strings.Contains(got, "failed step logs") {
		t.Errorf("no logs expected without an Actions run link:\n%s", got)
	}
	if strings.Contains(readFile(t, argsLog), "run view") {
		t.Errorf("gh run view must not be called without a run link")
	}
}

func TestFailingChecksSizeBound(t *testing.T) {
	bigLog := strings.Repeat("panic: goroutine stack overflow trace line\n", 500) + "final: real failure here\n"
	bin, _ := fakeGhRouter(t, map[string]ghResp{
		"pr_checks": {out: failingChecksFixture, code: 1},
		"run_view":  {out: bigLog, code: 0},
	})
	c := &Client{GhBin: bin}

	got, err := c.FailingChecks(context.Background(), "acme/nori", 42)
	if err != nil {
		t.Fatalf("FailingChecks: %v", err)
	}
	if len(got) > reactionMaxBytes {
		t.Fatalf("output %d bytes exceeds bound %d", len(got), reactionMaxBytes)
	}
	if !strings.Contains(got, truncMarker) {
		t.Errorf("oversized logs should be truncated with a marker:\n%s", got)
	}
	// Named checks (the head) survive truncation; the log tail is kept.
	if !strings.Contains(got, "CI / test: FAILURE") {
		t.Errorf("named checks must survive truncation:\n%s", got)
	}
	if !strings.Contains(got, "final: real failure here") {
		t.Errorf("log TAIL should be kept over the head:\n%s", got)
	}
}

const reviewsFixture = `{"reviews":[
  {"author":{"login":"alice"},"body":"Please add error handling and a regression test.","state":"CHANGES_REQUESTED","submittedAt":"2026-07-14T10:00:00Z"},
  {"author":{"login":"bob"},"body":"nit: rename this variable","state":"COMMENTED","submittedAt":"2026-07-14T10:05:00Z"},
  {"author":{"login":"carol"},"body":"LGTM ship it","state":"APPROVED","submittedAt":"2026-07-14T10:06:00Z"},
  {"author":{"login":"dave"},"body":"","state":"CHANGES_REQUESTED","submittedAt":"2026-07-14T10:07:00Z"}
]}`

const inlineFixture = `[
  {"user":{"login":"alice"},"body":"this can be nil, guard it","path":"internal/scm/client.go","line":120,"original_line":118},
  {"user":{"login":"bob"},"body":"extract this into a helper","path":"main.go","line":0,"original_line":42},
  {"user":{"login":"eve"},"body":"  ","path":"x.go","line":1,"original_line":1}
]`

func TestReviewCommentsParses(t *testing.T) {
	bin, argsLog := fakeGhRouter(t, map[string]ghResp{
		"pr_view": {out: reviewsFixture, code: 0},
		"api":     {out: inlineFixture, code: 0},
	})
	c := &Client{GhBin: bin}

	got, err := c.ReviewComments(context.Background(), "acme/nori", 42)
	if err != nil {
		t.Fatalf("ReviewComments: %v", err)
	}
	for _, want := range []string{
		"alice (CHANGES_REQUESTED): Please add error handling and a regression test.",
		"bob (COMMENTED): nit: rename this variable",
		"alice on internal/scm/client.go:120: this can be nil, guard it",
		"bob on main.go:42: extract this into a helper", // line 0 → original_line fallback
	} {
		if !strings.Contains(got, want) {
			t.Errorf("ReviewComments missing %q:\n%s", want, got)
		}
	}
	// APPROVED body, empty-body CHANGES_REQUESTED, and whitespace-only inline
	// comment are all dropped.
	if strings.Contains(got, "LGTM") {
		t.Errorf("APPROVED review body leaked:\n%s", got)
	}
	if strings.Contains(got, "dave") || strings.Contains(got, "eve") {
		t.Errorf("empty/whitespace-only feedback leaked:\n%s", got)
	}
	if len(got) > reactionMaxBytes {
		t.Errorf("output %d bytes exceeds bound %d", len(got), reactionMaxBytes)
	}
	args := readFile(t, argsLog)
	if !strings.Contains(args, "pr view 42 --repo acme/nori --json reviews") {
		t.Errorf("unexpected pr view argv:\n%s", args)
	}
	if !strings.Contains(args, "api repos/acme/nori/pulls/42/comments") {
		t.Errorf("unexpected inline api argv:\n%s", args)
	}
}

func TestReviewCommentsNoQualifyingFeedback(t *testing.T) {
	// Only an APPROVED review and no inline comments → nothing to say.
	bin, _ := fakeGhRouter(t, map[string]ghResp{
		"pr_view": {out: `{"reviews":[{"author":{"login":"c"},"body":"LGTM","state":"APPROVED"}]}`, code: 0},
		"api":     {out: `[]`, code: 0},
	})
	c := &Client{GhBin: bin}

	got, err := c.ReviewComments(context.Background(), "acme/nori", 42)
	if err != nil {
		t.Fatalf("ReviewComments: %v", err)
	}
	if got != "" {
		t.Errorf("no qualifying feedback must return empty string, got %q", got)
	}
}

func TestReviewCommentsGhErrorIsDistinct(t *testing.T) {
	bin, _ := fakeGhRouter(t, map[string]ghResp{
		"pr_view": {out: "", err: "gh: PR not found", code: 1},
	})
	c := &Client{GhBin: bin}

	got, err := c.ReviewComments(context.Background(), "acme/nori", 42)
	if err == nil {
		t.Fatalf("ReviewComments = %q, want error on gh failure", got)
	}
	if !strings.Contains(err.Error(), "PR not found") {
		t.Errorf("error %q should surface gh stderr", err)
	}
}

func TestReviewCommentsInlineBestEffort(t *testing.T) {
	// Reviews succeed; the inline `gh api` call fails. The review bodies must
	// still come back — inline comments are a bonus, never fatal.
	bin, _ := fakeGhRouter(t, map[string]ghResp{
		"pr_view": {out: reviewsFixture, code: 0},
		"api":     {out: "", err: "gh: api boom", code: 1},
	})
	c := &Client{GhBin: bin}

	got, err := c.ReviewComments(context.Background(), "acme/nori", 42)
	if err != nil {
		t.Fatalf("ReviewComments must not fail on inline api error: %v", err)
	}
	if !strings.Contains(got, "alice (CHANGES_REQUESTED)") {
		t.Errorf("review bodies must survive an inline api failure:\n%s", got)
	}
	if strings.Contains(got, "Inline comments:") {
		t.Errorf("no inline section expected when the api call failed:\n%s", got)
	}
}

func TestBoundHelpers(t *testing.T) {
	// Under the cap: returned verbatim.
	if got := boundHead("short", 4096); got != "short" {
		t.Errorf("boundHead under cap changed content: %q", got)
	}
	if got := tailBytes("short", 4096); got != "short" {
		t.Errorf("tailBytes under cap changed content: %q", got)
	}
	// Over the cap: bounded to max, marker present, right end kept.
	head := boundHead(strings.Repeat("a\n", 5000), 100)
	if len(head) > 100 || !strings.HasSuffix(head, truncMarker) {
		t.Errorf("boundHead over cap = %d bytes, want ≤100 ending in marker", len(head))
	}
	tail := tailBytes("HEAD\n"+strings.Repeat("b\n", 5000)+"TAIL", 100)
	if len(tail) > 100 || !strings.HasPrefix(tail, truncMarker) {
		t.Errorf("tailBytes over cap = %d bytes, want ≤100 starting with marker", len(tail))
	}
	if !strings.Contains(tail, "TAIL") || strings.Contains(tail, "HEAD") {
		t.Errorf("tailBytes must keep the tail, drop the head: %q", tail)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
