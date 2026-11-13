package reviewmd

import (
	"strings"
	"testing"
)

// inlineGraded is the shape internal/reviewagent's FORMAT block asks for.
const inlineGraded = "**[blocker]** `internal/tmux/client.go:357` — `Mouse=false` never turns mouse off\n" +
	"- **Grade:** impact=high confidence=verified effort=small\n" +
	"- **Gist:** A server that inherited `mouse on` keeps consuming mouse input.\n" +
	"- **Fix:** Apply `mouse off` when the effective configuration is false.\n" +
	"- **Detail:** `ConfigureServer` only appends the on-branch, so a prior attach's option survives.\n" +
	"\n" +
	"**[minor]** `internal/tmux/client.go:900` — stale comment\n" +
	"- **Gist:** The comment still names the removed flag.\n" +
	"- **Fix:** Drop the sentence.\n"

// anchorAll accepts every location verbatim — the "everything is in the diff" case.
func anchorAll(_ string, line int) (int, bool) { return line, true }

// anchorOnly accepts exactly the listed path:line pairs.
func anchorOnly(pairs map[string]int) Anchor {
	return func(path string, line int) (int, bool) {
		if want, ok := pairs[path]; ok && want == line {
			return line, true
		}
		return 0, false
	}
}

func TestRenderInlineAnchorsEveryLocatedFinding(t *testing.T) {
	got := RenderInline(Options{Title: "Claude review", Repo: "acme/widgets", Ref: "lola/x"}, inlineGraded, anchorAll)

	if len(got.Comments) != 2 {
		t.Fatalf("want 2 inline comments, got %d: %+v", len(got.Comments), got.Comments)
	}
	c := got.Comments[0]
	if c.Path != "internal/tmux/client.go" || c.Line != 357 {
		t.Errorf("comment 0 anchored at %s:%d, want internal/tmux/client.go:357", c.Path, c.Line)
	}
	// The thread body carries the severity, the title and the inlineGraded hierarchy —
	// but not the location, which GitHub draws above the comment itself.
	for _, want := range []string{
		"🛑 <b>blocker</b>",
		"never turns mouse off",
		"> A server that inherited",
		"> **Fix:** Apply",
		"<kbd>impact: high</kbd>",
		"<summary>Detail</summary>",
	} {
		if !strings.Contains(c.Body, want) {
			t.Errorf("inline body missing %q:\n%s", want, c.Body)
		}
	}
	if strings.Contains(c.Body, "client.go:357") {
		t.Errorf("the anchored location must not be repeated in the body:\n%s", c.Body)
	}
}

// The summary must describe the WHOLE review even when every finding became a
// thread — the callout is the only colour on the PR and it must not under-report.
func TestRenderInlineSummaryTalliesEveryFinding(t *testing.T) {
	got := RenderInline(Options{Title: "Claude review"}, inlineGraded, anchorAll)

	if !strings.HasPrefix(got.Body, "> [!CAUTION]\n> **Claude review** — 🛑 1 blocker · 🔹 1 minor") {
		t.Fatalf("summary must keep the full tally:\n%s", got.Body)
	}
	if !strings.Contains(got.Body, "💬 2 findings posted as inline review comments") {
		t.Errorf("summary must account for the threads:\n%s", got.Body)
	}
	// The findings that became threads are NOT repeated as blocks below.
	if strings.Contains(got.Body, "<summary>") {
		t.Errorf("anchored findings must not also render as details blocks:\n%s", got.Body)
	}
	// A COMMENT review requires a non-empty body.
	if strings.TrimSpace(got.Body) == "" {
		t.Error("the summary body must never be empty")
	}
}

func TestRenderInlineKeepsUnanchorableFindingsInTheBody(t *testing.T) {
	// Only the first location is in the diff; the second must survive in the body.
	got := RenderInline(Options{Title: "Claude review"},
		inlineGraded, anchorOnly(map[string]int{"internal/tmux/client.go": 357}))

	if len(got.Comments) != 1 {
		t.Fatalf("want 1 inline comment, got %d", len(got.Comments))
	}
	if !strings.Contains(got.Body, "stale comment") || !strings.Contains(got.Body, "The comment still names") {
		t.Errorf("the unanchorable finding lost its substance:\n%s", got.Body)
	}
	if !strings.Contains(got.Body, "could not be anchored to a line in this diff") {
		t.Errorf("the summary must say why a finding stayed here:\n%s", got.Body)
	}
	// It keeps its plain-path location, linked as usual.
	if !strings.Contains(got.Body, "<code>internal/tmux/client.go:900</code>") {
		t.Errorf("the body finding should keep its location:\n%s", got.Body)
	}
}

// A moved anchor is never silent: the reported location is stated in the body.
func TestRenderInlineDeclaresASnappedAnchor(t *testing.T) {
	snap := func(_ string, line int) (int, bool) { return line - 2, true }
	got := RenderInline(Options{Title: "Claude review"}, inlineGraded, snap)

	if len(got.Comments) == 0 {
		t.Fatal("expected inline comments")
	}
	c := got.Comments[0]
	if c.Line != 355 {
		t.Errorf("anchor line = %d, want 355 (snapped)", c.Line)
	}
	if !strings.Contains(c.Body, "Reported at <code>internal/tmux/client.go:357</code>") {
		t.Errorf("a snapped anchor must name the reported location:\n%s", c.Body)
	}
}

// A nil Anchor must anchor NOTHING: guessing a line 422s the whole review.
func TestRenderInlineNilAnchorPostsNoThreads(t *testing.T) {
	got := RenderInline(Options{Title: "Claude review"}, inlineGraded, nil)
	if len(got.Comments) != 0 {
		t.Fatalf("nil Anchor must produce no comments, got %d", len(got.Comments))
	}
	// It degrades to exactly the plain rendering, notes and all.
	if got.Body != Render(Options{Title: "Claude review"}, inlineGraded) {
		t.Errorf("with nothing anchorable the body must equal Render's:\n%s", got.Body)
	}
	if strings.Contains(got.Body, "💬") {
		t.Error("no threads means no inline note")
	}
}

func TestRenderInlineFailsOpenOnUnparseableFindings(t *testing.T) {
	raw := "CodeRabbit says: looks fine, but check the retry loop in worker.go.\n"
	got := RenderInline(Options{Title: "CodeRabbit"}, raw, anchorAll)
	if len(got.Comments) != 0 {
		t.Errorf("unparseable findings must not be anchored: %+v", got.Comments)
	}
	if !strings.Contains(got.Body, strings.TrimSpace(raw)) || !strings.Contains(got.Body, "### CodeRabbit") {
		t.Errorf("unparseable findings must post verbatim:\n%s", got.Body)
	}
}

func TestRenderInlineEmptyStaysEmpty(t *testing.T) {
	for _, in := range []string{"", "   \n\t"} {
		got := RenderInline(Options{Title: "R"}, in, anchorAll)
		if got.Body != "" || len(got.Comments) != 0 {
			t.Errorf("empty findings must render empty, got %+v", got)
		}
	}
}

// A location that is not a plain path:line is never guessed at (the same rule
// that keeps blobURL from linking one).
func TestRenderInlineOnlyAnchorsPlainPathLine(t *testing.T) {
	in := "**[major]** `app/Models/Invoice.php` — no line at all\n- **Fix:** x\n" +
		"**[major]** `https://evil.example/x:12` — not a path\n- **Fix:** y\n" +
		"**[major]** `a.go:notanumber` — not a line\n- **Fix:** z\n"
	got := RenderInline(Options{Title: "R"}, in, anchorAll)
	if len(got.Comments) != 0 {
		t.Errorf("only a plain path:line may be anchored, got %+v", got.Comments)
	}
}

func TestRenderInlineCapsThreadCount(t *testing.T) {
	var b strings.Builder
	total := MaxInlineComments + 5
	for i := 1; i <= total; i++ {
		b.WriteString("**[minor]** `a.go:" + itoa(i) + "` — finding " + itoa(i) + "\n- **Fix:** x\n\n")
	}
	got := RenderInline(Options{Title: "R"}, b.String(), anchorAll)
	if len(got.Comments) != MaxInlineComments {
		t.Fatalf("want %d comments (capped), got %d", MaxInlineComments, len(got.Comments))
	}
	// The overflow is not lost — it renders in the body like any unanchored one.
	if !strings.Contains(got.Body, "finding "+itoa(total)) {
		t.Errorf("capped overflow findings must survive in the body:\n%s", got.Body)
	}
}

func TestRenderInlineBoundsEachThreadBody(t *testing.T) {
	huge := strings.Repeat("word ", 4000)
	in := "**[blocker]** `a.go:1` — big\n- **Gist:** " + huge + "\n- **Fix:** x\n"
	got := RenderInline(Options{Title: "R"}, in, anchorAll)
	if len(got.Comments) != 1 {
		t.Fatalf("want 1 comment, got %d", len(got.Comments))
	}
	if n := len(got.Comments[0].Body); n > MaxInlineBytes {
		t.Errorf("inline body = %d bytes, want <= %d", n, MaxInlineBytes)
	}
	if n := len(got.Body); n > MaxBytes {
		t.Errorf("summary body = %d bytes, want <= %d", n, MaxBytes)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var out []byte
	for n > 0 {
		out = append([]byte{byte('0' + n%10)}, out...)
		n /= 10
	}
	return string(out)
}
