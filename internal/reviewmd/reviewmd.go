// Package reviewmd renders a review provider's plain findings into the GitHub
// Markdown actually posted on a PR (the `github` transport of the flexible-review
// system). It is PRESENTATION ONLY — a pure, dependency-free string→string leaf:
//
//   - It never touches the text handed to the WORKER agent, to Linear, or to a
//     notification. Those sinks keep the provider's raw findings byte for byte;
//     only internal/daemon's postGithubSink calls Render.
//   - It never adds, drops, or reorders a finding's substance. A finding's fields
//     move into a collapsed <details> block so a ten-finding review reads as ten
//     scannable lines instead of a wall of text, but nothing is summarized away.
//   - It FAILS OPEN. Anything it cannot parse (a coderabbit-cli plain-text pass,
//     a provider that ignored the format block, a truncated body) is returned
//     verbatim under the same heading — a formatter must never eat a review.
//
// What it can use is bounded by GitHub's comment sanitizer: CSS, <style>, style
// attributes and colour markup are all stripped, so a comment cannot be
// "designed". The five things that DO survive are the whole vocabulary here —
// an ALERT callout (`> [!CAUTION]`, the only real colour available), emoji,
// bold, code spans, and links — plus <details>/<summary>, which GitHub renders
// as a plain disclosure triangle with no box of its own.
//
// The input shape it understands is the one internal/reviewclaude's instruction
// asks for, tolerantly matched:
//
//	**[blocker]** `path/to/file.ext:LINE` — short title
//	- **What:** …
//	- **When:** …
//	- **Impact:** …
//	- **Fix:** …
//
// The output is bounded (MaxBytes) so the rendering overhead can never push a
// body past the 16KB head-clip in scm.PostPRComment and cut a finding mid-tag:
// details bodies are emitted most-severe-first while the budget lasts, and the
// remainder degrade to their one-line summaries.
package reviewmd

import (
	"fmt"
	"html"
	"regexp"
	"strings"
	"unicode/utf8"
)

// MaxBytes bounds a rendered body. It sits just under scm.postCommentMaxBytes
// (16KB) so the head-clip there never fires on a body this package produced —
// a clip there could land inside a <details> tag and break the whole comment.
const MaxBytes = 15 * 1024

// truncNote marks a body this package had to shorten.
const truncNote = "\n\n<sub>Findings truncated.</sub>"

// Byte-budget bookkeeping constants (see build). summaryOverhead is the two
// newlines a bare summary line costs, detailsOverhead the <details>/<summary>
// scaffolding around a detail body, and budgetSlack the reserve that keeps the
// heading, preamble and omission note from pushing the body over MaxBytes.
const (
	summaryOverhead = len("\n\n")
	detailsOverhead = len("\n\n<details>\n<summary></summary>\n\n\n\n</details>")
	budgetSlack     = 1024
)

// severity is a parsed finding's severity, ordered most severe first. Unknown
// labels keep their text and sort last; the renderer never drops a finding for
// wearing a severity we don't know.
type severity int

const (
	sevBlocker severity = iota
	sevMajor
	sevMinor
	sevOther
)

// sevEmoji is the leading glyph per severity — the whole point of the collapsed
// line is that a human can triage it at a glance.
func (s severity) emoji() string {
	switch s {
	case sevBlocker:
		return "🛑"
	case sevMajor:
		return "⚠️"
	case sevMinor:
		return "🔹"
	}
	return "▪️"
}

// classify maps a written severity label onto the ordered enum. The aliases
// cover providers that use their own vocabulary (critical/warning/nit) without
// the caller having to normalize first.
func classify(label string) severity {
	switch strings.ToLower(strings.TrimSpace(label)) {
	case "blocker", "critical", "high", "error":
		return sevBlocker
	case "major", "warning", "medium":
		return sevMajor
	case "minor", "nit", "low", "info":
		return sevMinor
	}
	return sevOther
}

// finding is one parsed entry: the header line split into severity / location /
// title, the body lines exactly as written, and — when the provider used the
// graded shape — those lines sorted into fields.
type finding struct {
	sev    severity
	label  string   // the severity word as the provider wrote it
	loc    string   // "path/to/file.ext:LINE", without backticks; may be empty
	title  string   // short title, may be empty
	body   []string // body lines verbatim (already trimmed of trailing space)
	fields fieldSet // parsed graded shape; zero value means "not that shape"
}

// gradePair is one whitelisted grade axis, e.g. {"impact", "high"}.
type gradePair struct{ key, val string }

// fieldSet is the graded body split by label. rest holds body lines that carry
// some OTHER label (a legacy What/When/Impact, a provider's own field) so they
// are never lost — they ride along inside the Detail disclosure.
type fieldSet struct {
	grade  []gradePair
	gist   string
	fix    string
	detail []string
	rest   []string
}

// gradeVocab is the closed vocabulary of grade axes and their allowed values.
// It is a WHITELIST for the same reason the status interpreter has one: the
// findings are model output, and a chip on a PR reads as a fact. An unknown axis
// or value is dropped, never rendered.
var gradeVocab = map[string]map[string]bool{
	"impact":     {"high": true, "medium": true, "low": true},
	"confidence": {"verified": true, "likely": true},
	"effort":     {"small": true, "medium": true, "large": true},
}

// gradeOrder fixes the chip order regardless of the order the provider wrote
// them, so every finding's chip row reads the same way.
var gradeOrder = []string{"impact", "confidence", "effort"}

var (
	// fieldRe matches a labelled body line: "- **Gist:** …", "**Fix:** …".
	fieldRe = regexp.MustCompile(`^\s*(?:[-*+]\s+)?\*\*([A-Za-z ]{2,20}):?\*\*:?\s*(.*)$`)
	// gradePairRe matches one key=value pair inside a Grade line.
	gradePairRe = regexp.MustCompile(`([A-Za-z]{2,12})\s*=\s*([A-Za-z]{2,12})`)
)

// parseFields sorts a finding's body lines into the graded shape. Continuation
// lines (a wrapped sentence, a nested bullet) attach to the field above them, so
// a provider that wraps its Detail across lines keeps all of it. A body with no
// Gist and no Grade leaves the zero fieldSet, which detailBody reads as "not the
// graded shape, pass through verbatim".
func parseFields(body []string) fieldSet {
	var fs fieldSet
	cur := "" // which field the next continuation line belongs to
	for _, line := range body {
		m := fieldRe.FindStringSubmatch(line)
		if m == nil {
			switch cur {
			case "gist":
				fs.gist = joinCont(fs.gist, line)
			case "fix":
				fs.fix = joinCont(fs.fix, line)
			case "detail":
				fs.detail = append(fs.detail, line)
			default:
				if strings.TrimSpace(line) != "" || len(fs.rest) > 0 {
					fs.rest = append(fs.rest, line)
				}
			}
			continue
		}
		switch strings.ToLower(strings.TrimSpace(m[1])) {
		case "grade":
			fs.grade, cur = parseGrade(m[2]), "grade"
		case "gist", "summary":
			fs.gist, cur = strings.TrimSpace(m[2]), "gist"
		case "fix":
			fs.fix, cur = strings.TrimSpace(m[2]), "fix"
		case "detail", "details":
			cur = "detail"
			if t := strings.TrimSpace(m[2]); t != "" {
				fs.detail = append(fs.detail, t)
			}
		default:
			fs.rest, cur = append(fs.rest, line), "rest"
		}
	}
	return fs
}

// joinCont appends a wrapped continuation line to a single-sentence field.
func joinCont(cur, line string) string {
	l := strings.TrimSpace(line)
	if l == "" {
		return cur
	}
	if cur == "" {
		return l
	}
	return cur + " " + l
}

// parseGrade extracts the whitelisted key=value pairs from a Grade line, in the
// fixed gradeOrder. Anything outside gradeVocab is dropped.
func parseGrade(line string) []gradePair {
	found := map[string]string{}
	for _, m := range gradePairRe.FindAllStringSubmatch(line, -1) {
		k, v := strings.ToLower(m[1]), strings.ToLower(m[2])
		if vals, ok := gradeVocab[k]; ok && vals[v] {
			found[k] = v
		}
	}
	var out []gradePair
	for _, k := range gradeOrder {
		if v, ok := found[k]; ok {
			out = append(out, gradePair{k, v})
		}
	}
	return out
}

var (
	// headerRe matches a finding header: an optional list/heading marker, then a
	// bracketed severity with optional bold/italic markers around it, then the
	// rest of the line. `**[blocker]**`, `[blocker]`, `- **[major]**` all match.
	headerRe = regexp.MustCompile(`^\s{0,3}(?:[-*+]\s+|#{1,6}\s+)?[*_]{0,2}\[([A-Za-z ]{2,20})\][*_]{0,2}\s*(.*)$`)
	// locRe pulls the leading backticked location out of a header remainder.
	locRe = regexp.MustCompile("^`([^`]+)`\\s*(.*)$")
	// dashRe strips the separator between location and title (em dash, en dash,
	// or a spaced hyphen), whichever the provider used.
	dashRe = regexp.MustCompile(`^(?:[—–]|-\s)\s*`)
	// codeSpanRe finds backticked spans so a summary line can carry <code>
	// instead of relying on Markdown being interpreted inside <summary>.
	codeSpanRe = regexp.MustCompile("`([^`]*)`")
)

// Options carries the per-post context the renderer may use. Everything but
// Title is optional and every field FAILS OPEN: an empty Repo or Ref simply
// means the locations render as plain code spans instead of links.
type Options struct {
	// Title is the provider's human label (e.g. "Claude review").
	Title string
	// Repo is the "owner/name" the PR lives in.
	Repo string
	// Ref is the git ref the findings were taken against (the session's branch).
	// Together with Repo it turns "path/to/file.go:12" into a blob link at that
	// line — the single most useful thing a reader can click.
	Ref string
}

// Render turns a provider's findings into the Markdown posted on the PR. An
// empty findings body renders empty (the caller never posts a clean review).
// Unparseable input comes back verbatim under a plain heading.
func Render(o Options, findings string) string {
	body := strings.TrimSpace(findings)
	if body == "" {
		return ""
	}
	fs, preamble := parse(body)
	if len(fs) == 0 {
		return clip(heading(o.Title, nil)+"\n\n"+body, MaxBytes)
	}
	return clip(build(o, fs, fs, preamble, ""), MaxBytes)
}

// parse splits the body into findings plus any text preceding the first header
// (a provider preamble we keep rather than discard).
func parse(body string) (fs []finding, preamble string) {
	var pre []string
	var cur *finding
	for raw := range strings.SplitSeq(body, "\n") {
		line := strings.TrimRight(raw, " \t")
		if m := headerRe.FindStringSubmatch(line); m != nil && looksLikeSeverity(m[1]) {
			if cur != nil {
				fs = append(fs, *cur)
			}
			loc, title := splitHeaderRest(m[2])
			cur = &finding{sev: classify(m[1]), label: strings.ToLower(strings.TrimSpace(m[1])), loc: loc, title: title}
			continue
		}
		if cur == nil {
			pre = append(pre, line)
			continue
		}
		cur.body = append(cur.body, line)
	}
	if cur != nil {
		fs = append(fs, *cur)
	}
	for i := range fs {
		fs[i].fields = parseFields(fs[i].body)
	}
	return fs, strings.TrimSpace(strings.Join(pre, "\n"))
}

// looksLikeSeverity keeps the header match from swallowing an ordinary
// bracketed line ("[see also] …"): only a known severity vocabulary opens a
// finding. Unknown-but-severity-shaped words are rejected here and simply stay
// body text, which is the fail-open direction.
func looksLikeSeverity(s string) bool {
	return classify(s) != sevOther
}

// splitHeaderRest peels the backticked location and the separator off a header
// remainder, returning the location (without backticks) and the title. A header
// that carries no location returns ("", rest).
func splitHeaderRest(rest string) (loc, title string) {
	rest = strings.TrimSpace(rest)
	if m := locRe.FindStringSubmatch(rest); m != nil {
		return strings.TrimSpace(m[1]), strings.TrimSpace(dashRe.ReplaceAllString(strings.TrimSpace(m[2]), ""))
	}
	return "", strings.TrimSpace(dashRe.ReplaceAllString(rest, ""))
}

// build assembles the final Markdown: the alert-callout header, the preamble
// (if any), then one collapsed <details> per finding. Details bodies are
// emitted most-severe-first while the byte budget lasts; once it is spent the
// remaining findings keep their summary line only, so a long review degrades by
// hiding detail rather than by losing findings.
//
// `all` and `fs` are separate because the INLINE transport (see inline.go) posts
// most findings as anchored review threads and leaves only the rest in this
// body: the tally in the header must still describe the WHOLE review (`all`),
// while the blocks below it cover just the findings that stayed here (`fs`).
// note, when set, is a plain sentence placed under the header — it is where the
// inline transport says how many findings went to threads instead.
func build(o Options, all, fs []finding, preamble, note string) string {
	var b strings.Builder
	b.WriteString(alertHeader(o.Title, all))
	if note != "" {
		b.WriteString("\n\n")
		b.WriteString(note)
	}
	if preamble != "" {
		b.WriteString("\n\n")
		b.WriteString(preamble)
	}
	// Budget bookkeeping: the summary lines are non-negotiable (they ARE the
	// findings), so their remaining cost is reserved up front and only the
	// leftover room is spent on detail bodies.
	sums := make([]string, len(fs))
	bodies := make([]string, len(fs))
	sumsLeft := 0
	for i, f := range fs {
		sums[i] = summaryLine(f, o)
		bodies[i] = detailBody(f)
		sumsLeft += len(sums[i]) + summaryOverhead
	}

	dropped := 0
	for i := range fs {
		sumsLeft -= len(sums[i]) + summaryOverhead
		if bodies[i] == "" {
			// Nothing to hide: a bare line beats an empty collapsible.
			b.WriteString("\n\n")
			b.WriteString(sums[i])
			continue
		}
		if b.Len()+len(sums[i])+len(bodies[i])+detailsOverhead+sumsLeft >= MaxBytes-budgetSlack {
			dropped++
			b.WriteString("\n\n")
			b.WriteString(sums[i])
			continue
		}
		b.WriteString("\n\n<details>\n<summary>")
		b.WriteString(sums[i])
		b.WriteString("</summary>\n\n")
		b.WriteString(bodies[i])
		b.WriteString("\n\n</details>")
	}
	if dropped > 0 {
		fmt.Fprintf(&b, "\n\n<sub>Detail omitted for %d finding(s) — the review body exceeded the comment budget.</sub>", dropped)
	}
	return b.String()
}

// alertHeader is the comment's first block: a GitHub ALERT callout carrying the
// title and the severity tally. The alert is the one piece of real COLOUR
// GitHub allows in a comment — it sanitizes away CSS, style attributes and
// colour markup, but renders `> [!CAUTION]` / `> [!WARNING]` / `> [!NOTE]` as a
// tinted, icon-led box — so the worst severity in the review is visible before
// a single line is read. The level is derived, never decorative: a blocker
// makes it CAUTION (red), a major WARNING (amber), anything else NOTE (blue).
func alertHeader(title string, fs []finding) string {
	level := "NOTE"
	switch worst(fs) {
	case sevBlocker:
		level = "CAUTION"
	case sevMajor:
		level = "WARNING"
	}
	line := "**" + strings.TrimSpace(headingTitle(title)) + "**"
	if t := tally(fs); t != "" {
		line += " — " + t
	}
	return "> [!" + level + "]\n> " + line
}

// worst returns the most severe severity present (sevOther when the set is
// empty or carries only unknown labels).
func worst(fs []finding) severity {
	w := sevOther
	for _, f := range fs {
		if f.sev < w {
			w = f.sev
		}
	}
	return w
}

// headingTitle falls back to a generic label when the caller passes none.
func headingTitle(title string) string {
	if t := strings.TrimSpace(title); t != "" {
		return t
	}
	return "Review"
}

// heading is the plain Markdown heading used on the fail-open path (findings
// this package could not parse are posted verbatim under it).
func heading(title string, fs []finding) string {
	t := headingTitle(title)
	counts := tally(fs)
	if counts == "" {
		return "### " + t
	}
	return "### " + t + " — " + counts
}

// tally renders "🛑 1 blocker · ⚠️ 2 major", most severe first, omitting empty
// buckets. Findings with an unknown severity fall into a plain count.
func tally(fs []finding) string {
	if len(fs) == 0 {
		return ""
	}
	n := map[severity]int{}
	for _, f := range fs {
		n[f.sev]++
	}
	var parts []string
	for _, s := range []severity{sevBlocker, sevMajor, sevMinor} {
		if n[s] > 0 {
			parts = append(parts, fmt.Sprintf("%s %d %s", s.emoji(), n[s], sevWord(s)))
		}
	}
	if n[sevOther] > 0 {
		parts = append(parts, fmt.Sprintf("%s %d other", sevOther.emoji(), n[sevOther]))
	}
	return strings.Join(parts, " · ")
}

func sevWord(s severity) string {
	switch s {
	case sevBlocker:
		return "blocker"
	case sevMajor:
		return "major"
	case sevMinor:
		return "minor"
	}
	return "other"
}

// summaryLine is the always-visible line of a finding: severity, location and
// title. GitHub strips every styling hook from a comment (no CSS, no style
// attribute, no colour), so the line leans on the four things it does keep —
// an emoji, bold, a code span, and a link — to stay scannable in a flat list.
// It is written as HTML (escaped text with <code> spans rebuilt from the
// Markdown backticks) rather than Markdown, because inline Markdown inside
// <summary> is not reliably rendered and a location that failed to render as
// code would be the single most useful thing on the line.
func summaryLine(f finding, o Options) string {
	parts := []string{f.sev.emoji() + " <b>" + html.EscapeString(f.label) + "</b>"}
	if f.loc != "" {
		loc := "<code>" + html.EscapeString(f.loc) + "</code>"
		if href := blobURL(o, f.loc); href != "" {
			loc = `<a href="` + href + `">` + loc + `</a>`
		}
		parts = append(parts, loc)
	}
	line := strings.Join(parts, " · ")
	if t := inlineHTML(f.title); t != "" {
		line += " — " + t
	}
	return line
}

// pathLineRe splits a "path/to/file.ext:LINE" location. The path is deliberately
// narrow — letters, digits and the handful of punctuation marks a repo path
// actually uses — so nothing that could carry a URL, a quote or a scheme is ever
// interpolated into an href.
var pathLineRe = regexp.MustCompile(`^([A-Za-z0-9._/-]+):(\d{1,7})$`)

// blobURL turns a location into a GitHub blob link at that line, or "" when it
// cannot be built safely. It FAILS OPEN in every direction: no repo, no ref, a
// location that is not a plain path:line, or a repo that is not "owner/name"
// all yield "" and the caller renders a plain code span. A wrong link is worse
// than no link — it would point a reader at someone else's file.
func blobURL(o Options, loc string) string {
	if o.Repo == "" || o.Ref == "" {
		return ""
	}
	if !repoRe.MatchString(o.Repo) || !refRe.MatchString(o.Ref) {
		return ""
	}
	m := pathLineRe.FindStringSubmatch(loc)
	if m == nil {
		return ""
	}
	return "https://github.com/" + o.Repo + "/blob/" + o.Ref + "/" + m[1] + "#L" + m[2]
}

var (
	// repoRe accepts exactly "owner/name".
	repoRe = regexp.MustCompile(`^[A-Za-z0-9._-]+/[A-Za-z0-9._-]+$`)
	// refRe accepts a branch/sha shape safe to place in a URL path unescaped.
	refRe = regexp.MustCompile(`^[A-Za-z0-9._/-]+$`)
)

// inlineHTML escapes s and rebuilds its Markdown code spans as <code>, so the
// provider's backticked identifiers survive into an HTML context.
func inlineHTML(s string) string {
	esc := html.EscapeString(strings.TrimSpace(s))
	return codeSpanRe.ReplaceAllString(esc, "<code>$1</code>")
}

// detailBody is the hidden half of a finding. When the body carries the graded
// shape (Grade / Gist / Fix / Detail) it is REBUILT for a skimming reader: a
// chip row of `key: value` code spans, then the two sentences that carry the
// triage decision, with everything else folded behind a second disclosure. That
// is the whole point of the field split — an opened finding must stay readable
// in one glance, not become the wall the collapse just solved.
//
// Anything else — a legacy What/When/Impact/Fix body, a coderabbit-cli pass,
// free prose — is passed through verbatim, so an unrecognized shape still posts
// everything it said.
func detailBody(f finding) string {
	if s := gradedBody(f); s != "" {
		return s
	}
	return strings.Join(trimBlanks(f.body), "\n")
}

// gradedBody renders the graded shape, or "" when the body does not carry it
// (no Gist and no Grade ⇒ not this shape; pass through instead).
//
// The ORDER and the wrapper are the hierarchy, and both are deliberate. Four
// flush blocks at equal weight read as debris hanging off the row above them,
// so the substance — the gist, then the fix, then the grade chips — goes inside
// a BLOCKQUOTE: GitHub draws it with a left rail and muted text, which is the
// only containment a sanitized comment can express, and it visibly binds the
// body to its summary row instead of letting it collide with the next finding.
// Detail stays OUTSIDE the quote, a plain triangle under the block, because it
// is a footer rather than part of the finding's statement (and because a nested
// HTML block inside a blockquote is the one construct GitHub parses least
// predictably).
func gradedBody(f finding) string {
	g := f.fields
	if g.gist == "" && len(g.grade) == 0 {
		return ""
	}
	var parts []string
	if g.gist != "" {
		parts = append(parts, g.gist) // what breaks — read first
	}
	if g.fix != "" {
		parts = append(parts, "**Fix:** "+g.fix) // what to do — read second
	}
	if chips := gradeChips(g.grade); chips != "" {
		parts = append(parts, chips) // metadata — glanced at, never read
	}
	body := blockquote(strings.Join(parts, "\n\n"))
	if extra := strings.Join(trimBlanks(append(append([]string{}, g.detail...), g.rest...)), "\n"); extra != "" {
		body += "\n\n<details>\n<summary>Detail</summary>\n\n" + extra + "\n\n</details>"
	}
	return body
}

// gradeChips renders the whitelisted grade pairs as <kbd> elements. <kbd> is in
// GitHub's comment allowlist and is drawn as a small bordered, shadowed key —
// the only true CHIP a sanitized comment can produce (a code span would put the
// grades at the same weight as the identifiers quoted around them). Pairs
// outside the whitelist are dropped rather than shown: a made-up axis reads as
// fact on a PR.
func gradeChips(pairs []gradePair) string {
	var out []string
	for _, p := range pairs {
		out = append(out, "<kbd>"+p.key+": "+p.val+"</kbd>")
	}
	return strings.Join(out, " ")
}

// blockquote prefixes every line so the block survives as ONE blockquote —
// including the blank lines between paragraphs, which must carry a bare ">" or
// GitHub ends the quote there and the hierarchy falls apart.
func blockquote(s string) string {
	if s == "" {
		return ""
	}
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		if strings.TrimSpace(l) == "" {
			lines[i] = ">"
			continue
		}
		lines[i] = "> " + l
	}
	return strings.Join(lines, "\n")
}

// trimBlanks drops leading and trailing blank lines, keeping interior ones.
func trimBlanks(lines []string) []string {
	i, j := 0, len(lines)
	for i < j && strings.TrimSpace(lines[i]) == "" {
		i++
	}
	for j > i && strings.TrimSpace(lines[j-1]) == "" {
		j--
	}
	return lines[i:j]
}

// clip head-clips s to max bytes on a rune boundary, appending truncNote when it
// cuts. It is the last-resort bound; build's per-finding budget normally keeps
// the body well under it.
func clip(s string, max int) string {
	if len(s) <= max {
		return s
	}
	keep := max - len(truncNote)
	if keep < 0 {
		keep = 0
	}
	return trimPartialRune(s[:keep]) + truncNote
}

// trimPartialRune drops a trailing partial UTF-8 sequence left by a byte cut.
func trimPartialRune(s string) string {
	for len(s) > 0 {
		if r, size := utf8.DecodeLastRuneInString(s); r == utf8.RuneError && size <= 1 {
			s = s[:len(s)-1]
			continue
		}
		break
	}
	return s
}
