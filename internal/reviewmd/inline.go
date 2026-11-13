package reviewmd

// inline.go is the INLINE half of the github transport: instead of one comment
// carrying every finding, a review is posted as a summary body plus one ANCHORED
// review thread per finding, on the line the finding is about — the shape a
// reader can triage in the "Files changed" tab, reply to, and RESOLVE. Only a
// review COMMENT anchored to a diff line gets GitHub's resolve affordance at
// all, which is the whole reason this path exists.
//
// It shares the parser with Render (a finding is the same finding either way)
// and adds two things:
//
//   - the SPLIT. GitHub rejects the whole review with 422 if any comment names a
//     line outside the PR's diff, so which findings can be threads is decided
//     BEFORE anything is posted, by the caller's Anchor func (the daemon builds
//     it from the diff via internal/diffanchor). A finding that cannot be
//     anchored is not dropped and not weakened — it stays in the summary body,
//     rendered exactly as it is today.
//
//   - the per-thread BODY. The location is not repeated (GitHub draws the line
//     itself); everything else is the same hierarchy Render uses inside a
//     <details>: severity, title, then the gist/fix/grade blockquote with Detail
//     folded under it.
//
// Honesty rules, both load-bearing:
//   - the summary's tally counts EVERY finding, threads included, so the callout
//     never under-reports a review whose blockers all became threads;
//   - a thread whose anchor MOVED (the reviewer named a line the diff does not
//     carry and the caller snapped to a nearby one) says so in its body. A
//     comment sitting on a line it is not about, with nothing saying so, is
//     worse than one in the summary body.

import (
	"fmt"
	"html"
	"strconv"
	"strings"
)

// MaxInlineBytes bounds ONE inline comment body. GitHub's own limit is far
// higher; this is a per-thread readability and payload bound, since a review
// carries every comment in a single request.
const MaxInlineBytes = 4 * 1024

// MaxInlineComments caps how many threads one review posts. The review
// instruction already asks for at most 10 findings; this is the backstop for a
// provider that ignores it, and the overflow is not lost — it renders in the
// summary body like any unanchorable finding.
const MaxInlineComments = 20

// Anchor resolves a finding's REPORTED path:line to the line a comment may
// actually be anchored at. It returns ok=false when the location cannot carry a
// comment at all (not in the diff, unreadable location, file not in the PR), in
// which case the finding stays in the summary body.
//
// A nil Anchor anchors NOTHING (fail closed): posting a guessed line 422s the
// entire review, so "no information" must mean "no threads".
type Anchor func(path string, line int) (int, bool)

// InlineComment is one anchored review thread: where it goes and the Markdown
// body of its first comment. Line is always a RIGHT-side line of the PR's diff.
type InlineComment struct {
	Path string
	Line int
	Body string
}

// InlineReview is a whole review as the github transport posts it: the summary
// Body (always non-empty — GitHub requires a body on a COMMENT review) plus the
// anchored threads. Comments is empty when nothing could be anchored, which the
// caller reads as "post this as a plain comment instead".
type InlineReview struct {
	Body     string
	Comments []InlineComment
}

// RenderInline splits a provider's findings into anchored threads plus a summary
// body. It is the inline sibling of Render and shares its fail-open contract:
// findings it cannot parse yield zero comments and the verbatim body, so a
// formatter can never eat a review.
func RenderInline(o Options, findings string, anchor Anchor) InlineReview {
	body := strings.TrimSpace(findings)
	if body == "" {
		return InlineReview{}
	}
	all, preamble := parse(body)
	if len(all) == 0 {
		// Unparseable: no locations to anchor, post it verbatim as one comment.
		return InlineReview{Body: clip(heading(o.Title, nil)+"\n\n"+body, MaxBytes)}
	}

	var (
		comments []InlineComment
		rest     []finding
	)
	for _, f := range all {
		c, ok := inlineComment(f, anchor, len(comments) < MaxInlineComments)
		if !ok {
			rest = append(rest, f)
			continue
		}
		comments = append(comments, c)
	}
	if len(comments) == 0 {
		// Nothing anchorable: identical to the plain path, so produce exactly it.
		return InlineReview{Body: clip(build(o, all, all, preamble, ""), MaxBytes)}
	}
	return InlineReview{
		Body:     clip(build(o, all, rest, preamble, inlineNote(len(comments), len(rest))), MaxBytes),
		Comments: comments,
	}
}

// inlineComment renders one finding as an anchored thread, or reports ok=false
// when it must stay in the summary body: no location, an unreadable location, an
// anchor the caller rejected, or the MaxInlineComments cap already reached
// (room=false).
func inlineComment(f finding, anchor Anchor, room bool) (InlineComment, bool) {
	if !room || anchor == nil || f.loc == "" {
		return InlineComment{}, false
	}
	m := pathLineRe.FindStringSubmatch(f.loc)
	if m == nil {
		return InlineComment{}, false // not a plain path:line — never guess
	}
	reported, err := strconv.Atoi(m[2])
	if err != nil {
		return InlineComment{}, false
	}
	line, ok := anchor(m[1], reported)
	if !ok || line <= 0 {
		return InlineComment{}, false
	}
	return InlineComment{
		Path: m[1],
		Line: line,
		Body: inlineBody(f, f.loc, line != reported),
	}, true
}

// inlineNote is the sentence under the summary header that accounts for the
// split, so a reader of the summary alone is never left thinking the review
// found only what is listed there.
func inlineNote(threads, rest int) string {
	s := fmt.Sprintf("💬 %s posted as inline review %s on the changed lines.",
		plural(threads, "finding"), noun(threads, "comment"))
	if rest > 0 {
		s += fmt.Sprintf(" The remaining %s could not be anchored to a line in this diff and %s below.",
			plural(rest, "finding"), verbIs(rest))
	}
	return s
}

// plural renders "1 finding" / "3 findings".
func plural(n int, word string) string {
	return strconv.Itoa(n) + " " + noun(n, word)
}

// noun pluralizes word without a count in front of it.
func noun(n int, word string) string {
	if n == 1 {
		return word
	}
	return word + "s"
}

func verbIs(n int) string {
	if n == 1 {
		return "is"
	}
	return "are"
}

// inlineBody renders the body of one anchored thread. It is the same hierarchy
// Render puts inside a <details> — severity, title, then the gist/fix/grade
// blockquote with Detail folded underneath — minus the location, which GitHub
// draws itself above the comment.
//
// moved marks an anchor the caller snapped to a nearby line; the reported
// location is then stated in the body, because the comment is sitting somewhere
// slightly different from what the reviewer named.
func inlineBody(f finding, reportedLoc string, moved bool) string {
	var b strings.Builder
	b.WriteString(f.sev.emoji() + " <b>" + html.EscapeString(f.label) + "</b>")
	if t := inlineHTML(f.title); t != "" {
		b.WriteString(" — " + t)
	}
	if moved && reportedLoc != "" {
		b.WriteString("\n\n<sub>Reported at <code>" + html.EscapeString(reportedLoc) +
			"</code>; anchored to the nearest line in this diff.</sub>")
	}
	if body := detailBody(f); body != "" {
		b.WriteString("\n\n")
		b.WriteString(body)
	}
	return clip(b.String(), MaxInlineBytes)
}
