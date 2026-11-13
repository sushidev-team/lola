package daemon

// Tests for the inline `github` transport (reviewinline.go): the anchored,
// resolvable-thread shape, its fall-back-to-a-plain-comment contract, and the
// worker instruction that makes the agent close the threads it fixed.

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/linear"
	"github.com/sushidev-team/lola/internal/scm"
	"github.com/sushidev-team/lola/internal/session"
)

// inlineDiff touches two files: internal/tmux/client.go around line 357 and
// desktop/termsvc.go at 12. Anything else a finding names is unanchorable.
const inlineDiff = `diff --git a/internal/tmux/client.go b/internal/tmux/client.go
--- a/internal/tmux/client.go
+++ b/internal/tmux/client.go
@@ -352,7 +352,9 @@ func (c *Client) ConfigureServer(ctx context.Context) error {
 	cmds := [][]string{
 		{"set-option", "-g", "status", "off"},
 	}
-	if c.MouseLegacy {
+	if c.Mouse {
+		cmds = append(cmds, []string{"set-option", "-g", "mouse", "on"})
+	}
 	return c.apply(ctx, cmds)
 }
diff --git a/desktop/termsvc.go b/desktop/termsvc.go
--- a/desktop/termsvc.go
+++ b/desktop/termsvc.go
@@ -10,2 +10,3 @@ func (t *TermService) Snapshot() string {
 	return t.capture()
+	// unreachable
 }
`

// inlineFindings names one anchorable location (client.go:357, inside the hunk)
// and one that is nowhere near the diff (worker.go:9000).
const inlineFindings = "**[blocker]** `internal/tmux/client.go:357` — `Mouse=false` never turns mouse off\n" +
	"- **Grade:** impact=high confidence=verified effort=small\n" +
	"- **Gist:** A server that inherited `mouse on` keeps consuming mouse input.\n" +
	"- **Fix:** Apply `mouse off` when the effective configuration is false.\n" +
	"\n" +
	"**[minor]** `internal/worker/pool.go:9000` — stale comment\n" +
	"- **Gist:** The comment still names a removed flag.\n" +
	"- **Fix:** Drop the sentence.\n"

// inlineCall records one PostPRReview invocation.
type inlineCall struct {
	repo     string
	pr       int
	commitID string
	body     string
	comments []scm.InlineComment
}

// fakeInlineReview installs both inline seams: the head-sha/diff read and the
// review post.
type fakeInlineReview struct {
	mu        sync.Mutex
	calls     []inlineCall
	sha       string
	diff      string
	targetErr error
	postErr   error
	targets   int
}

func (f *fakeInlineReview) install(d *Daemon) {
	if f.sha == "" {
		f.sha = "0123456789abcdef0123456789abcdef01234567"
	}
	d.mu.Lock()
	d.prReviewTarget = func(_ context.Context, _ string, _ int) (string, string, error) {
		f.mu.Lock()
		f.targets++
		err := f.targetErr
		f.mu.Unlock()
		if err != nil {
			return "", "", err
		}
		return f.sha, f.diff, nil
	}
	d.postPRReview = func(_ context.Context, repo string, pr int, commitID, body string, comments []scm.InlineComment) error {
		f.mu.Lock()
		f.calls = append(f.calls, inlineCall{repo, pr, commitID, body, comments})
		err := f.postErr
		f.mu.Unlock()
		return err
	}
	d.mu.Unlock()
}

func (f *fakeInlineReview) callsCopy() []inlineCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]inlineCall(nil), f.calls...)
}

func (f *fakeInlineReview) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

// inlineDesc is a claude-session provider posting to github in the inline shape.
func inlineDesc() reviewProvider {
	p := claudeDesc()
	p.Transports = config.TransportSet{config.TransportLola, config.TransportGitHub}
	p.Inline = true
	return p
}

// inlineDaemon wires a daemon with both github seams faked and one open-PR
// session in the store.
func inlineDaemon(t *testing.T, fi *fakeInlineReview, fp *fakePostPR) (*Daemon, reviewProvider, session.Session) {
	t.Helper()
	d := newTestDaemon(t, nativeTestConfig(nativePoll("p1")), &linear.Fake{}, &fakeNative{})
	(&fakeReactSeams{}).install(d)
	p := inlineDesc()
	setProviders(d, p)
	if fi.diff == "" && fi.targetErr == nil {
		fi.diff = inlineDiff
	}
	fi.install(d)
	fp.install(d)
	s := reactSess("FE-1", "review_pending", openPR(7, "MERGEABLE", "", "pass"))
	d.sessions.Upsert(s)
	return d, p, s
}

func TestReviewGithubInlinePostsAnchoredThreads(t *testing.T) {
	fi, fp := &fakeInlineReview{}, &fakePostPR{}
	d, p, s := inlineDaemon(t, fi, fp)

	d.postGithubSink(context.Background(), s, p, inlineFindings)

	calls := fi.callsCopy()
	if len(calls) != 1 {
		t.Fatalf("want one inline review, got %d", len(calls))
	}
	c := calls[0]
	if c.repo != "acme/widgets" || c.pr != 7 {
		t.Errorf("posted to %s#%d, want acme/widgets#7", c.repo, c.pr)
	}
	if c.commitID != fi.sha {
		t.Errorf("commit_id = %q, want the PR's head sha %q", c.commitID, fi.sha)
	}
	if len(c.comments) != 1 {
		t.Fatalf("want 1 anchored thread (only client.go:357 is in the diff), got %d: %+v", len(c.comments), c.comments)
	}
	if c.comments[0].Path != "internal/tmux/client.go" || c.comments[0].Line != 357 {
		t.Errorf("anchored at %s:%d, want internal/tmux/client.go:357", c.comments[0].Path, c.comments[0].Line)
	}
	if !strings.Contains(c.comments[0].Body, "never turns mouse off") {
		t.Errorf("thread body lost the finding: %q", c.comments[0].Body)
	}
	// The flat comment path must NOT also run — one review per PR per kind.
	if fp.count() != 0 {
		t.Errorf("a successful inline review must not also post a plain comment, got %d", fp.count())
	}
	// Both guards are stamped: the settle guard (never re-post) and the inline
	// fact the worker instruction keys on.
	got, _ := d.sessions.Get(s.ID)
	if got.PostedGitHubPRs["claude-session"] != 7 {
		t.Errorf("settle guard = %d, want 7", got.PostedGitHubPRs["claude-session"])
	}
	if got.InlineReviewPRs["claude-session"] != 7 {
		t.Errorf("inline guard = %d, want 7", got.InlineReviewPRs["claude-session"])
	}
	// Settled: a second pass posts nothing at all.
	d.postGithubSink(context.Background(), got, p, inlineFindings)
	if fi.count() != 1 || fp.count() != 0 {
		t.Errorf("a settled PR must not be posted again (inline=%d plain=%d)", fi.count(), fp.count())
	}
}

// The finding that could not be anchored is neither dropped nor guessed at: it
// travels in the review's summary body, and the body accounts for the split.
func TestReviewGithubInlineKeepsUnanchoredFindingInTheBody(t *testing.T) {
	fi, fp := &fakeInlineReview{}, &fakePostPR{}
	d, p, s := inlineDaemon(t, fi, fp)

	d.postGithubSink(context.Background(), s, p, inlineFindings)

	body := fi.callsCopy()[0].body
	if !strings.Contains(body, "internal/worker/pool.go:9000") {
		t.Errorf("the unanchorable finding must survive in the summary body:\n%s", body)
	}
	if !strings.Contains(body, "could not be anchored") {
		t.Errorf("the summary must say a finding stayed in the body:\n%s", body)
	}
	// The tally still describes the WHOLE review.
	if !strings.Contains(body, "🛑 1 blocker") || !strings.Contains(body, "🔹 1 minor") {
		t.Errorf("summary tally must cover every finding:\n%s", body)
	}
}

func TestReviewGithubInlineFallsBackWhenDiffUnavailable(t *testing.T) {
	fi := &fakeInlineReview{targetErr: errors.New("gh pr diff 7 --repo acme/widgets: HTTP 502: Bad Gateway")}
	fp := &fakePostPR{}
	d, p, s := inlineDaemon(t, fi, fp)

	d.postGithubSink(context.Background(), s, p, inlineFindings)

	if fi.count() != 0 {
		t.Errorf("no anchors can be guessed without a diff, got %d inline posts", fi.count())
	}
	if fp.count() != 1 {
		t.Fatalf("the findings must still reach the PR as a plain comment, got %d", fp.count())
	}
	got, _ := d.sessions.Get(s.ID)
	if got.PostedGitHubPRs["claude-session"] != 7 {
		t.Errorf("the plain-comment fallback must settle the guard, got %d", got.PostedGitHubPRs["claude-session"])
	}
	if got.InlineReviewPRs["claude-session"] != 0 {
		t.Error("a fallback post must NOT claim inline threads exist")
	}
}

// A review about files this PR does not touch anchors nothing; the plain comment
// says the same thing with one less API call.
func TestReviewGithubInlineFallsBackWhenNothingAnchors(t *testing.T) {
	fi, fp := &fakeInlineReview{}, &fakePostPR{}
	d, p, s := inlineDaemon(t, fi, fp)

	d.postGithubSink(context.Background(), s, p,
		"**[major]** `internal/other/file.go:12` — not in this diff\n- **Fix:** x\n")

	if fi.count() != 0 {
		t.Errorf("nothing anchorable must not post a review, got %d", fi.count())
	}
	if fp.count() != 1 {
		t.Errorf("want the plain-comment fallback, got %d posts", fp.count())
	}
}

// 422/403 on the review (a push landed between the diff read and the post, or no
// write access): fall back rather than lose the findings.
func TestReviewGithubInlinePermanentRejectionFallsBack(t *testing.T) {
	fi := &fakeInlineReview{postErr: errors.New(
		"gh api repos/acme/widgets/pulls/7/reviews: HTTP 422: line must be part of the diff")}
	fp := &fakePostPR{}
	d, p, s := inlineDaemon(t, fi, fp)

	d.postGithubSink(context.Background(), s, p, inlineFindings)

	if fi.count() != 1 {
		t.Fatalf("the inline post must have been attempted once, got %d", fi.count())
	}
	if fp.count() != 1 {
		t.Fatalf("a rejected inline review must fall back to a plain comment, got %d", fp.count())
	}
	got, _ := d.sessions.Get(s.ID)
	if got.PostedGitHubPRs["claude-session"] != 7 {
		t.Errorf("the fallback post must settle the guard, got %d", got.PostedGitHubPRs["claude-session"])
	}
	if got.InlineReviewPRs["claude-session"] != 0 {
		t.Error("a rejected inline review must not claim threads exist")
	}
}

// A transient failure must NOT silently downgrade the shape: the guard stays
// unstamped and the next cycle retries the inline post.
func TestReviewGithubInlineTransientFailureRetriesInline(t *testing.T) {
	fi := &fakeInlineReview{postErr: errors.New(
		"gh api repos/acme/widgets/pulls/7/reviews: HTTP 502: Bad Gateway")}
	fp := &fakePostPR{}
	d, p, s := inlineDaemon(t, fi, fp)

	d.postGithubSink(context.Background(), s, p, inlineFindings)
	got, _ := d.sessions.Get(s.ID)
	if got.PostedGitHubPRs["claude-session"] != 0 {
		t.Errorf("a transient failure must not settle the guard, got %d", got.PostedGitHubPRs["claude-session"])
	}
	if fp.count() != 0 {
		t.Errorf("a transient failure must not downgrade to a plain comment, got %d", fp.count())
	}
	d.postGithubSink(context.Background(), got, p, inlineFindings)
	if fi.count() != 2 {
		t.Errorf("the next cycle must retry the INLINE post, got %d attempts", fi.count())
	}
}

// An `@coderabbitai` string inside a finding must never start a fresh CodeRabbit
// review — in a thread body just as much as in the summary.
func TestReviewGithubInlineNeutralizesBotTriggers(t *testing.T) {
	fi, fp := &fakeInlineReview{}, &fakePostPR{}
	d, p, s := inlineDaemon(t, fi, fp)

	d.postGithubSink(context.Background(), s, p,
		"**[blocker]** `internal/tmux/client.go:357` — ask @coderabbitai to re-review\n"+
			"- **Gist:** @coderabbitai should look at this again.\n- **Fix:** x\n")

	c := fi.callsCopy()[0]
	for _, body := range append([]string{c.body}, c.comments[0].Body) {
		if strings.Contains(body, "@coderabbit") && !strings.Contains(body, "@\u200bcoderabbit") {
			t.Errorf("a live @coderabbit mention survived:\n%s", body)
		}
	}
}

// The whole point of threads: the worker must be told they exist and be asked to
// resolve them.
func TestInlineHandoffTellsTheAgentToResolveThreads(t *testing.T) {
	fi, fp := &fakeInlineReview{}, &fakePostPR{}
	d, p, s := inlineDaemon(t, fi, fp)
	seams := &fakeReactSeams{}
	seams.install(d)

	// A worker parked at its prompt, so the hand-off is delivered immediately.
	s.AtPrompt, s.AtPromptVerified = true, true
	d.sessions.Upsert(s)
	s, _ = d.sessions.Get(s.ID)

	d.routeFindings(context.Background(), s, p, inlineFindings)

	sends := seams.sendCalls()
	if len(sends) != 1 {
		t.Fatalf("want one worker hand-off, got %d", len(sends))
	}
	text := sends[0].text
	for _, want := range []string{
		"inline review threads on PR #7 (acme/widgets)",
		"commit and push",
		"resolve that thread",
		"resolveReviewThread",
		"reviewThreads(first:100)",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("hand-off missing %q:\n%s", want, text)
		}
	}
	// The findings themselves are still there, raw.
	if !strings.Contains(text, "**[blocker]**") {
		t.Errorf("the hand-off must still carry the raw findings:\n%s", text)
	}
}

// No threads lola posted, no ASSERTION that any exist: a run that fell back to a
// plain comment may still hand the worker the conditional close-the-loop recipe
// (the PR may carry someone else's threads — CodeRabbit's, a human's — and the
// listing command is the source of truth), but it must never state that these
// findings are sitting there as threads.
func TestInlineHandoffSilentWithoutThreads(t *testing.T) {
	fi := &fakeInlineReview{targetErr: errors.New("gh: HTTP 502")}
	fp := &fakePostPR{}
	d, p, s := inlineDaemon(t, fi, fp)
	seams := &fakeReactSeams{}
	seams.install(d)

	s.AtPrompt, s.AtPromptVerified = true, true
	d.sessions.Upsert(s)
	s, _ = d.sessions.Get(s.ID)

	d.routeFindings(context.Background(), s, p, inlineFindings)

	sends := seams.sendCalls()
	if len(sends) != 1 {
		t.Fatalf("want one worker hand-off, got %d", len(sends))
	}
	if strings.Contains(sends[0].text, "These findings are also open as inline review threads") {
		t.Errorf("a fallback comment must not promise threads:\n%s", sends[0].text)
	}
	if !strings.Contains(sends[0].text, "may also be open as review threads on PR #7") {
		t.Errorf("the fallback still asks the worker to close what it fixes:\n%s", sends[0].text)
	}
}

// inlineThreadNote is derived, never remembered: a stamp for a DIFFERENT PR (the
// session moved on) or an unreadable repo yields no instruction.
func TestInlineThreadNoteIsPRExact(t *testing.T) {
	p := inlineDesc()
	s := reactSess("FE-1", "review_pending", openPR(7, "MERGEABLE", "", "pass"))
	s.InlineReviewPRs = map[string]int{"claude-session": 7}

	if note := inlineThreadNote(s, p); note == "" {
		t.Fatal("a matching PR must produce the instruction")
	}

	old := s
	old.PR = openPR(9, "MERGEABLE", "", "pass") // a newer PR, threads were on #7
	if note := inlineThreadNote(old, p); note != "" {
		t.Errorf("threads on another PR must not be described:\n%s", note)
	}

	noRepo := s
	noRepo.Repo = "not a repo slug"
	if note := inlineThreadNote(noRepo, p); note != "" {
		t.Errorf("an unreadable repo must produce no instruction:\n%s", note)
	}

	flat := p
	flat.Inline = false
	if note := inlineThreadNote(s, flat); note != "" {
		t.Errorf("a non-inline provider must produce no instruction:\n%s", note)
	}
}

// prThreadNote covers the hand-offs lola did NOT post threads for. It must stay
// CONDITIONAL in its wording (nothing here proves a thread is open), still carry
// the gh recipe, and stay silent whenever the PR or repo cannot be named exactly.
func TestPRThreadNoteIsConditional(t *testing.T) {
	s := reactSess("FE-1", "review_pending", openPR(7, "MERGEABLE", "", "pass"))

	note := prThreadNote(s)
	if !strings.Contains(note, "may also be open as review threads on PR #7 (acme/widgets)") {
		t.Errorf("the note must not assert threads exist:\n%s", note)
	}
	for _, want := range []string{"commit and push", "resolveReviewThread", "reviewThreads(first:100)"} {
		if !strings.Contains(note, want) {
			t.Errorf("note missing %q:\n%s", want, note)
		}
	}

	noPR := s
	noPR.PR = nil
	if note := prThreadNote(noPR); note != "" {
		t.Errorf("no PR, no instruction:\n%s", note)
	}

	noRepo := s
	noRepo.Repo = "not a repo slug"
	if note := prThreadNote(noRepo); note != "" {
		t.Errorf("an unreadable repo must produce no instruction:\n%s", note)
	}
}
