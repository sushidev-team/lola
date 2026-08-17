package scm

// Tests for the inline review WRITE path (reviewinline.go). They reuse
// fakeGhStdin (reviewpost_test.go) so both halves of the contract are provable:
// the request travels as JSON on STDIN, and nothing untrusted ever reaches argv.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func decodeReviewRequest(t *testing.T, stdinLog string) reviewRequest {
	t.Helper()
	var got reviewRequest
	if err := json.Unmarshal([]byte(readFile(t, stdinLog)), &got); err != nil {
		t.Fatalf("stdin was not valid JSON (%v):\n%s", err, readFile(t, stdinLog))
	}
	return got
}

func TestPostPRReviewArgvAndJSONOnStdin(t *testing.T) {
	bin, argsLog, stdinLog := fakeGhStdin(t, 0, "")
	c := &Client{GhBin: bin}

	err := c.PostPRReview(context.Background(), "acme/nori", 42, "deadbeefcafe", "summary body",
		[]InlineComment{
			{Path: "internal/tmux/client.go", Line: 357, Body: "🛑 blocker — mouse never turned off"},
			{Path: "desktop/termsvc.go", Line: 12, Body: "🔹 minor — stale comment"},
		})
	if err != nil {
		t.Fatalf("PostPRReview: %v", err)
	}

	args := loggedArgs(t, argsLog)
	want := "api --method POST repos/acme/nori/pulls/42/reviews --input -"
	if args != want {
		t.Errorf("argv = %q, want %q", args, want)
	}
	got := decodeReviewRequest(t, stdinLog)
	if got.Event != "COMMENT" {
		t.Errorf("event = %q, want COMMENT (never APPROVE/REQUEST_CHANGES on lola's own PR)", got.Event)
	}
	if got.CommitID != "deadbeefcafe" || got.Body != "summary body" {
		t.Errorf("commit/body = %q/%q", got.CommitID, got.Body)
	}
	if len(got.Comments) != 2 {
		t.Fatalf("want 2 comments, got %d", len(got.Comments))
	}
	if got.Comments[0].Path != "internal/tmux/client.go" || got.Comments[0].Line != 357 {
		t.Errorf("comment 0 = %+v", got.Comments[0])
	}
	for i, cm := range got.Comments {
		if cm.Side != "RIGHT" {
			t.Errorf("comment %d side = %q, want RIGHT", i, cm.Side)
		}
	}
	// Nothing untrusted in argv.
	if strings.Contains(args, "blocker") || strings.Contains(args, "summary body") {
		t.Errorf("review content leaked into argv: %q", args)
	}
}

// The findings are untrusted text: quotes, newlines, backticks and a shell
// metacharacter soup must survive as DATA (encoding/json escapes them) and must
// never be interpreted anywhere.
func TestPostPRReviewEncodesUntrustedText(t *testing.T) {
	bin, argsLog, stdinLog := fakeGhStdin(t, 0, "")
	c := &Client{GhBin: bin}

	nasty := "quote \" backslash \\ newline \n tab \t `cmd` $(id) '; rm -rf /' unicodeend"
	if err := c.PostPRReview(context.Background(), "acme/nori", 7, "abc1234", nasty,
		[]InlineComment{{Path: "a.go", Line: 1, Body: nasty}}); err != nil {
		t.Fatalf("PostPRReview: %v", err)
	}

	got := decodeReviewRequest(t, stdinLog)
	if got.Body != nasty {
		t.Errorf("body round-trip changed the text:\n got %q\nwant %q", got.Body, nasty)
	}
	if len(got.Comments) != 1 || got.Comments[0].Body != nasty {
		t.Errorf("comment body round-trip changed the text: %+v", got.Comments)
	}
	if strings.Contains(readFile(t, argsLog), "rm -rf") {
		t.Error("untrusted text reached argv")
	}
}

// A malformed anchor would 422 the ENTIRE review, so it is dropped — the rest of
// the threads still post, and the caller's summary body still carries every
// finding it could not anchor.
func TestPostPRReviewDropsMalformedAnchors(t *testing.T) {
	bin, _, stdinLog := fakeGhStdin(t, 0, "")
	c := &Client{GhBin: bin}

	err := c.PostPRReview(context.Background(), "acme/nori", 42, "abc1234", "body",
		[]InlineComment{
			{Path: "", Line: 5, Body: "no path"},
			{Path: "a.go", Line: 0, Body: "no line"},
			{Path: "a.go", Line: -3, Body: "negative line"},
			{Path: "a.go", Line: 9, Body: "   "},
			{Path: "a.go", Line: 9, Body: "good"},
		})
	if err != nil {
		t.Fatalf("PostPRReview: %v", err)
	}
	got := decodeReviewRequest(t, stdinLog)
	if len(got.Comments) != 1 || got.Comments[0].Body != "good" {
		t.Errorf("only the well-formed anchor should post, got %+v", got.Comments)
	}
}

func TestPostPRReviewEmptySkipsExec(t *testing.T) {
	bin, argsLog, _ := fakeGhStdin(t, 0, "")
	c := &Client{GhBin: bin}

	if err := c.PostPRReview(context.Background(), "acme/nori", 42, "abc1234", "  \n", nil); err != nil {
		t.Fatalf("empty review must be a no-op, got %v", err)
	}
	if fileExists(argsLog) {
		t.Errorf("empty review must NOT exec gh; args.log = %q", readFile(t, argsLog))
	}
}

// An empty body WITH comments still posts: GitHub requires a body on a COMMENT
// review, and the caller (reviewmd) always produces one — but the guard must key
// on "nothing at all", not on the body alone.
func TestPostPRReviewPostsWhenOnlyCommentsPresent(t *testing.T) {
	bin, argsLog, _ := fakeGhStdin(t, 0, "")
	c := &Client{GhBin: bin}

	if err := c.PostPRReview(context.Background(), "acme/nori", 42, "abc1234", "",
		[]InlineComment{{Path: "a.go", Line: 1, Body: "x"}}); err != nil {
		t.Fatalf("PostPRReview: %v", err)
	}
	if !fileExists(argsLog) {
		t.Error("a review with comments must exec gh even with an empty body")
	}
}

func TestPostPRReviewSurfacesGhError(t *testing.T) {
	bin, _, _ := fakeGhStdin(t, 1,
		`gh: Unprocessable Entity (HTTP 422) - pull_request_review_thread.line must be part of the diff`)
	c := &Client{GhBin: bin}

	err := c.PostPRReview(context.Background(), "acme/nori", 42, "abc1234", "body",
		[]InlineComment{{Path: "a.go", Line: 9999, Body: "x"}})
	if err == nil {
		t.Fatal("a gh failure must surface as an error")
	}
	// The caller classifies permanent-vs-transient off this text (422 ⇒ fall back
	// to a plain comment), so both the command and gh's stderr must be present.
	if !strings.Contains(err.Error(), "pulls/42/reviews") || !strings.Contains(err.Error(), "422") {
		t.Errorf("error should carry the command + gh stderr: %v", err)
	}
}

func TestPostPRReviewRejectsOversizePayload(t *testing.T) {
	bin, argsLog, _ := fakeGhStdin(t, 0, "")
	c := &Client{GhBin: bin}

	huge := strings.Repeat("x", postReviewMaxBytes+1)
	err := c.PostPRReview(context.Background(), "acme/nori", 42, "abc1234", huge, nil)
	if err == nil {
		t.Fatal("an oversize payload must error rather than post a clipped (invalid) JSON body")
	}
	if fileExists(argsLog) {
		t.Error("an oversize payload must not exec gh")
	}
}

// fakeGhTarget answers the two PRReviewTarget calls: `pr view --json headRefOid`
// prints sha, `pr diff` prints diff. Its argv log lets a test prove both ran.
func fakeGhTarget(t *testing.T, sha, diff string, diffCode int) (bin, argsLog string) {
	t.Helper()
	dir := t.TempDir()
	bin = filepath.Join(dir, "gh")
	argsLog = filepath.Join(dir, "args.log")
	diffFile := filepath.Join(dir, "diff")
	if err := os.WriteFile(diffFile, []byte(diff), 0o644); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\n" +
		"echo \"$@\" >> \"" + argsLog + "\"\n" +
		"case \"$2\" in\n" +
		"  view) printf '%s\\n' '" + sha + "' ;;\n" +
		"  diff) cat \"" + diffFile + "\"; exit " + strconv.Itoa(diffCode) + " ;;\n" +
		"esac\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin, argsLog
}

func TestPRReviewTargetFetchesShaAndDiff(t *testing.T) {
	diff := "diff --git a/a.go b/a.go\n--- a/a.go\n+++ b/a.go\n@@ -1,1 +1,1 @@\n+var x = 1\n"
	bin, argsLog := fakeGhTarget(t, "0123456789abcdef0123456789abcdef01234567", diff, 0)
	c := &Client{GhBin: bin}

	sha, got, err := c.PRReviewTarget(context.Background(), "acme/nori", 42)
	if err != nil {
		t.Fatalf("PRReviewTarget: %v", err)
	}
	if sha != "0123456789abcdef0123456789abcdef01234567" {
		t.Errorf("sha = %q", sha)
	}
	if got != diff {
		t.Errorf("diff = %q, want %q", got, diff)
	}
	args := readFile(t, argsLog)
	if !strings.Contains(args, "pr view 42 --repo acme/nori --json headRefOid --jq .headRefOid") ||
		!strings.Contains(args, "pr diff 42 --repo acme/nori") {
		t.Errorf("both reads should have run, got:\n%s", args)
	}
}

func TestPRReviewTargetRejectsUnreadableSha(t *testing.T) {
	bin, _ := fakeGhTarget(t, "not-a-sha", "diff", 0)
	c := &Client{GhBin: bin}

	if _, _, err := c.PRReviewTarget(context.Background(), "acme/nori", 42); err == nil {
		t.Fatal("an unreadable head sha must be an error, never posted against")
	}
}

func TestPRReviewTargetSurfacesDiffFailure(t *testing.T) {
	bin, _ := fakeGhTarget(t, "0123456789abcdef0123456789abcdef01234567", "", 1)
	c := &Client{GhBin: bin}

	if _, _, err := c.PRReviewTarget(context.Background(), "acme/nori", 42); err == nil {
		t.Fatal("a diff failure must surface: without a diff no anchor may be guessed")
	}
}

func TestPRReviewTargetBoundsTheDiff(t *testing.T) {
	bin, _ := fakeGhTarget(t, "0123456789abcdef0123456789abcdef01234567",
		strings.Repeat("+line\n", prDiffFullMaxBytes/3), 0)
	c := &Client{GhBin: bin}

	_, diff, err := c.PRReviewTarget(context.Background(), "acme/nori", 42)
	if err != nil {
		t.Fatalf("PRReviewTarget: %v", err)
	}
	if len(diff) > prDiffFullMaxBytes {
		t.Errorf("diff = %d bytes, want <= %d", len(diff), prDiffFullMaxBytes)
	}
}
