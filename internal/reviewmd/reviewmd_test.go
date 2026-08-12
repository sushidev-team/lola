package reviewmd

import (
	"strings"
	"testing"
)

const sample = "**[blocker]** `app/Models/Invoice.php:530` — lone `due_date` discarded when `issue_date` null\n" +
	"- **What:** `paymentTermsDays()` only derives the window when both dates exist.\n" +
	"- **When:** a draft created with a due date but no issue date.\n" +
	"- **Impact:** an immutable invoice goes out due in 14 days.\n" +
	"- **Fix:** keep the stored `due_date` when `issue_date` is missing.\n" +
	"\n" +
	"**[major]** `app/Http/Controllers/InvoiceController.php:1846` — backdated draft's issue date rewritten\n" +
	"- **What:** `'issue_date' => $issueDate` is now written unconditionally.\n" +
	"- **Fix:** treat an explicitly stored `issue_date` as authoritative.\n"

func TestRenderHeaderIsAnAlertWithTheTally(t *testing.T) {
	got := Render(Options{Title: "Claude review"}, sample)
	want := "> [!CAUTION]\n> **Claude review** — 🛑 1 blocker · ⚠️ 1 major"
	if !strings.HasPrefix(got, want) {
		t.Fatalf("header must be a CAUTION alert carrying the tally.\ngot:\n%s", got[:min(len(got), 200)])
	}
}

// The alert level is DERIVED from the worst severity — it is the only colour
// GitHub renders in a comment, so it must never lie about how bad the review is.
func TestRenderAlertLevelTracksWorstSeverity(t *testing.T) {
	cases := []struct{ in, want string }{
		{"**[blocker]** `a.go:1` — x\n- **Fix:** y\n", "> [!CAUTION]"},
		{"**[major]** `a.go:1` — x\n- **Fix:** y\n", "> [!WARNING]"},
		{"**[minor]** `a.go:1` — x\n- **Fix:** y\n", "> [!NOTE]"},
		{"**[minor]** `a.go:1` — x\n- **Fix:** y\n\n**[blocker]** `b.go:2` — z\n- **Fix:** w\n", "> [!CAUTION]"},
	}
	for _, c := range cases {
		if got := Render(Options{Title: "Claude review"}, c.in); !strings.HasPrefix(got, c.want) {
			t.Errorf("want %s for %q, got %q", c.want, firstLine(c.in), firstLine(got))
		}
	}
}

func TestRenderLinksLocationsWhenRepoAndRefKnown(t *testing.T) {
	got := Render(Options{Title: "Claude review", Repo: "acme/widgets", Ref: "lola/fe-1"}, sample)
	want := `<a href="https://github.com/acme/widgets/blob/lola/fe-1/app/Models/Invoice.php#L530"><code>app/Models/Invoice.php:530</code></a>`
	if !strings.Contains(got, want) {
		t.Fatalf("location not linked:\n%s", got)
	}
}

// A wrong link points a reader at someone else's file, so every unknown drops
// the link and keeps the plain code span.
func TestRenderLinkFailsOpen(t *testing.T) {
	cases := map[string]Options{
		"no repo":       {Title: "R", Ref: "main"},
		"no ref":        {Title: "R", Repo: "acme/widgets"},
		"bad repo":      {Title: "R", Repo: "https://evil.example/x", Ref: "main"},
		"bad ref":       {Title: "R", Repo: "acme/widgets", Ref: "main branch"},
		"unknown owner": {Title: "R", Repo: "acme", Ref: "main"},
	}
	for name, o := range cases {
		got := Render(o, sample)
		if strings.Contains(got, "<a href=") {
			t.Errorf("%s: must not emit a link:\n%s", name, got)
		}
		if !strings.Contains(got, "<code>app/Models/Invoice.php:530</code>") {
			t.Errorf("%s: plain location lost", name)
		}
	}
}

// A location that is not a plain path:line (a URL, a range, prose) is never
// interpolated into an href.
func TestRenderLinkOnlyPlainPathLine(t *testing.T) {
	for _, loc := range []string{"http://evil.example/x:1", "a.go:1-4", `a.go":1`, "app/x.go"} {
		in := "**[major]** `" + loc + "` — x\n- **Fix:** y\n"
		got := Render(Options{Title: "R", Repo: "acme/widgets", Ref: "main"}, in)
		if strings.Contains(got, "<a href=") {
			t.Errorf("location %q must not be linked:\n%s", loc, got)
		}
	}
}

func TestRenderCollapsesEachFinding(t *testing.T) {
	got := Render(Options{Title: "Claude review"}, sample)
	if n := strings.Count(got, "<details>"); n != 2 {
		t.Fatalf("want one <details> per finding, got %d\n%s", n, got)
	}
	if strings.Count(got, "</details>") != 2 {
		t.Fatalf("unbalanced details tags:\n%s", got)
	}
	// The always-visible line must carry severity + location + title.
	if !strings.Contains(got, "🛑 <b>blocker</b> · <code>app/Models/Invoice.php:530</code> — lone <code>due_date</code> discarded when <code>issue_date</code> null</summary>") {
		t.Fatalf("summary line not rendered as expected:\n%s", got)
	}
	// The body stays Markdown, blank-line separated so GitHub renders it.
	if !strings.Contains(got, "</summary>\n\n- **What:** `paymentTermsDays()`") {
		t.Fatalf("detail body must follow a blank line:\n%s", got)
	}
}

func TestRenderKeepsEveryFindingsSubstance(t *testing.T) {
	got := Render(Options{Title: "Claude review"}, sample)
	for _, frag := range []string{
		"a draft created with a due date but no issue date",
		"an immutable invoice goes out due in 14 days",
		"keep the stored `due_date` when `issue_date` is missing",
		"treat an explicitly stored `issue_date` as authoritative",
		"app/Http/Controllers/InvoiceController.php:1846",
	} {
		if !strings.Contains(got, frag) {
			t.Errorf("rendering dropped %q\n%s", frag, got)
		}
	}
}

const graded = "**[blocker]** `app/x.go:12` — nil client on the error path\n" +
	"- **Grade:** impact=high confidence=verified effort=small\n" +
	"- **Gist:** `load()` returns a nil client on a timeout, so the next call panics mid-request.\n" +
	"- **Fix:** Return the error instead of a nil client.\n" +
	"- **Detail:** The timeout branch at `client.go:88` falls through to `return nil, nil`. Same shape in `pool.go:41`.\n"

// The graded shape is the whole point of the field split: an opened finding is
// a chip row plus two sentences, with the rest folded behind a second
// disclosure — never the wall the collapse just solved.
func TestRenderGradedBodyIsChipsGistFix(t *testing.T) {
	got := Render(Options{Title: "Claude review"}, graded)
	// Gist first, fix second, chips last — all inside ONE blockquote (the left
	// rail is what binds the body to its row), Detail folded outside it.
	want := "</summary>\n\n" +
		"> `load()` returns a nil client on a timeout, so the next call panics mid-request.\n" +
		">\n" +
		"> **Fix:** Return the error instead of a nil client.\n" +
		">\n" +
		"> <kbd>impact: high</kbd> <kbd>confidence: verified</kbd> <kbd>effort: small</kbd>\n\n" +
		"<details>\n<summary>Detail</summary>\n\n"
	if !strings.Contains(got, want) {
		t.Fatalf("graded body not rendered as quoted gist + fix + chips, detail folded:\n%s", got)
	}
	if !strings.Contains(got, "Same shape in `pool.go:41`.") {
		t.Errorf("detail text lost:\n%s", got)
	}
	if strings.Count(got, "<details>") != 2 || strings.Count(got, "</details>") != 2 {
		t.Errorf("want the finding plus its nested Detail, got %d/%d tags", strings.Count(got, "<details>"), strings.Count(got, "</details>"))
	}
}

// A chip reads as fact on a PR, so the vocabulary is closed: an unknown axis or
// an unknown value is dropped rather than rendered.
func TestRenderGradeChipsAreWhitelisted(t *testing.T) {
	in := "**[major]** `a.go:1` — thing\n" +
		"- **Grade:** impact=catastrophic vibes=bad effort=small confidence=verified\n" +
		"- **Gist:** one sentence.\n"
	got := Render(Options{Title: "R"}, in)
	for _, bad := range []string{"catastrophic", "vibes"} {
		if strings.Contains(got, bad) {
			t.Errorf("unknown grade %q must be dropped:\n%s", bad, got)
		}
	}
	// Order is fixed (impact · confidence · effort) whatever order was written.
	if !strings.Contains(got, "<kbd>confidence: verified</kbd> <kbd>effort: small</kbd>") {
		t.Errorf("chips not in canonical order:\n%s", got)
	}
}

// Fields outside the graded set (a legacy When:, a provider's own label) are
// never dropped — they ride along inside the Detail disclosure.
func TestRenderKeepsUnknownFieldsInDetail(t *testing.T) {
	in := "**[major]** `a.go:1` — thing\n" +
		"- **Grade:** impact=low\n" +
		"- **Gist:** one sentence.\n" +
		"- **When:** only on Tuesdays.\n"
	got := Render(Options{Title: "R"}, in)
	i, j := strings.Index(got, "<summary>Detail</summary>"), strings.Index(got, "only on Tuesdays")
	if i < 0 || j < i {
		t.Fatalf("unknown field must survive inside Detail:\n%s", got)
	}
}

// A body with no Grade and no Gist is NOT the graded shape: it passes through
// verbatim, so an older provider still posts everything it said.
func TestRenderPassesLegacyBodyThrough(t *testing.T) {
	got := Render(Options{Title: "Claude review"}, sample)
	if !strings.Contains(got, "</summary>\n\n- **What:** `paymentTermsDays()`") {
		t.Fatalf("legacy What/When/Impact/Fix body must pass through verbatim:\n%s", got)
	}
	if strings.Contains(got, "<summary>Detail</summary>") {
		t.Fatalf("must not invent a Detail fold for a legacy body:\n%s", got)
	}
}

func TestRenderFailsOpenOnUnparseableBody(t *testing.T) {
	raw := "Reviewed 3 files.\n\nsrc/main.go: possible nil deref at line 12\nsrc/util.go: unchecked error\n"
	got := Render(Options{Title: "CodeRabbit review"}, raw)
	if !strings.Contains(got, "### CodeRabbit review") {
		t.Fatalf("want the heading, got:\n%s", got)
	}
	if !strings.Contains(got, strings.TrimSpace(raw)) {
		t.Fatalf("unparseable findings must be posted verbatim, got:\n%s", got)
	}
	if strings.Contains(got, "<details>") {
		t.Fatalf("must not invent structure for unparsed text:\n%s", got)
	}
}

func TestRenderEmptyStaysEmpty(t *testing.T) {
	if got := Render(Options{Title: "Claude review"}, "   \n\t\n"); got != "" {
		t.Fatalf("clean review must render empty, got %q", got)
	}
}

func TestRenderTolerantHeaderShapes(t *testing.T) {
	cases := map[string]string{
		"bare brackets":  "[minor] `a.go:1` — small thing\n- **Fix:** do it\n",
		"list marker":    "- **[major]** `a.go:1` — thing\n  - **Fix:** do it\n",
		"hyphen dash":    "**[blocker]** `a.go:1` - thing\n- **Fix:** do it\n",
		"no location":    "**[major]** the build script never runs\n- **Fix:** run it\n",
		"alias severity": "**[critical]** `a.go:1` — thing\n- **Fix:** do it\n",
	}
	for name, in := range cases {
		got := Render(Options{Title: "Claude review"}, in)
		if !strings.Contains(got, "<details>") {
			t.Errorf("%s: header not recognised:\n%s", name, got)
		}
	}
}

func TestRenderIgnoresNonSeverityBrackets(t *testing.T) {
	in := "**[blocker]** `a.go:1` — thing\n- **What:** see [see also] below\n[see also] not a finding\n"
	got := Render(Options{Title: "Claude review"}, in)
	if n := strings.Count(got, "<details>"); n != 1 {
		t.Fatalf("a bracketed non-severity line must not open a finding, got %d:\n%s", n, got)
	}
}

func TestRenderEscapesHTMLInSummary(t *testing.T) {
	in := "**[major]** `a.go:1` — <img src=x onerror=alert(1)> in `<script>`\n- **Fix:** none\n"
	got := Render(Options{Title: "Claude review"}, in)
	if strings.Contains(got, "<img") {
		t.Fatalf("summary must escape raw HTML:\n%s", got)
	}
	if !strings.Contains(got, "&lt;img") {
		t.Fatalf("want escaped angle brackets:\n%s", got)
	}
}

func TestRenderSummaryOnlyWhenNoBody(t *testing.T) {
	got := Render(Options{Title: "Claude review"}, "**[minor]** `a.go:1` — nothing else to say\n")
	if strings.Contains(got, "<details>") {
		t.Fatalf("a body-less finding must not become an empty collapsible:\n%s", got)
	}
	if !strings.Contains(got, "nothing else to say") {
		t.Fatalf("summary lost:\n%s", got)
	}
}

func TestRenderStaysWithinBudget(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 40; i++ {
		b.WriteString("**[blocker]** `app/very/long/path/to/file.php:530` — a fairly wordy finding title here\n")
		b.WriteString("- **What:** " + strings.Repeat("x", 600) + "\n")
		b.WriteString("- **Fix:** " + strings.Repeat("y", 600) + "\n\n")
	}
	got := Render(Options{Title: "Claude review"}, b.String())
	if len(got) > MaxBytes {
		t.Fatalf("rendered body %d bytes exceeds MaxBytes %d", len(got), MaxBytes)
	}
	if strings.Count(got, "<details>") != strings.Count(got, "</details>") {
		t.Fatalf("budget degradation left unbalanced tags")
	}
	// Degradation hides DETAIL, never a finding: every header still has a line.
	if n := strings.Count(got, "<b>blocker</b>"); n != 40 {
		t.Fatalf("want all 40 findings present, got %d", n)
	}
	if !strings.Contains(got, "Detail omitted for") {
		t.Fatalf("want the omission note when detail is dropped:\n%s", got[:400])
	}
}

func TestRenderPreservesPreamble(t *testing.T) {
	got := Render(Options{Title: "Claude review"}, "Reviewed 12 files against main.\n\n**[minor]** `a.go:1` — thing\n- **Fix:** do it\n")
	if !strings.Contains(got, "Reviewed 12 files against main.") {
		t.Fatalf("preamble dropped:\n%s", got)
	}
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
