package sshelement

import (
	"fmt"
	"regexp"
	"strings"
)

// widgetAdviceRules are the things a widget page cannot do, because of its sandbox and policy.
// This is advice for the author and never a security control: a page that gets past it is
// stopped by the sandbox all the same. A false warning costs a line, so the patterns reach
// for what agents commonly write, not for every spelling.
var widgetAdviceRules = []struct {
	re   *regexp.Regexp
	text string
}{
	{regexp.MustCompile(`(?is)<script\b[^>]*\bsrc\s*=`), "<script src> cannot load. Put the code inline"},
	{regexp.MustCompile(`(?is)<(img|iframe|audio|video|source|embed|object)\b[^>]*\b(src|data)\s*=\s*["']?(https?:)?//`), "an external image or media file cannot load. Use a data: URL or inline SVG"},
	{regexp.MustCompile(`(?is)<link\b[^>]*\bhref\s*=`), "<link href> cannot load. Put the CSS in a <style>"},
	{regexp.MustCompile(`(?is)@import\b|url\(\s*["']?(https?:)?//`), "CSS cannot load anything external. Inline it"},
	{regexp.MustCompile(`\bfetch\s*\(|\bXMLHttpRequest\b|\bWebSocket\b|\bEventSource\b|\bsendBeacon\b`), "there is no network: fetch, XHR and WebSocket all fail. Pass data with --data"},
	{regexp.MustCompile(`\bimport\s*\(|\bimport\s+[\w{*][^;\n]*\bfrom\s+["']`), "import cannot load anything. Keep all the code in the one page"},
	{regexp.MustCompile(`\b(localStorage|sessionStorage|indexedDB)\b|document\.cookie`), "there is no storage: it throws here. Keep state in variables or in --data"},
	{regexp.MustCompile(`(?is)<form\b`), "a form cannot submit. Use a <button> with an onclick"},
	{regexp.MustCompile(`(^|[^.\w"'])(alert|confirm|prompt)\s*\(`), "alert, confirm and prompt do nothing here. Use lola.ask to reach the human"},
	{regexp.MustCompile(`\bwindow\.open\s*\(|(?is)\btarget\s*=\s*["']?_blank`), "popups are blocked"},
	{regexp.MustCompile(`\beval\s*\(|\bnew\s+Function\b`), "eval and new Function are blocked"},
	{regexp.MustCompile(`(?is)<a\b[^>]*\bhref\s*=\s*["']?[^#"'\s>]|\blocation\.(href|assign|replace)\b|\blocation\s*=[^=]|http-equiv\s*=\s*["']?refresh`), "a page that navigates is closed by lola. Do not follow links or set location"},
}

var (
	adviceComment = regexp.MustCompile(`(?s)<!--.*?-->`)
	adviceScript  = regexp.MustCompile(`(?is)<script\b[^>]*>(.*?)</script\s*>`)
	adviceIDAttr  = regexp.MustCompile(`(?is)\bid\s*=\s*["']?([\w-]+)`)
	adviceIDUse   = regexp.MustCompile(`getElementById\(\s*["']([\w-]+)["']|querySelector(?:All)?\(\s*["']#([\w-]+)["']|\$\(\s*["']#?([\w-]+)["']`)
	adviceWaits   = regexp.MustCompile(`DOMContentLoaded|addEventListener\(\s*["']load["']|\bonload\b`)
	adviceMakesID = regexp.MustCompile(`\.id\s*=|\bid\s*:|setAttribute\(\s*["']id["']`)
)

// maxWidgetAdvice keeps the warnings readable when a page is wrong in many ways.
const maxWidgetAdvice = 8

// WidgetAdvice lists what cannot work in a widget page, for the author to fix before a human
// ever sees it.
func WidgetAdvice(html string) []string {
	html = adviceComment.ReplaceAllString(html, "")
	var out []string
	for _, r := range widgetAdviceRules {
		if r.re.MatchString(html) {
			out = append(out, r.text)
		}
	}
	out = append(out, adviceOrder(html)...)
	if len(out) > maxWidgetAdvice {
		out = out[:maxWidgetAdvice]
	}
	return out
}

// adviceOrder finds a script reading an element that does not exist yet, the classic page
// that draws its markup and whose script does nothing. An id that appears only inside a
// script is built by that script, so it is left alone.
func adviceOrder(html string) []string {
	scripts := adviceScript.FindAllStringSubmatchIndex(html, -1)
	inScript := func(at int) bool {
		for _, s := range scripts {
			if at >= s[0] && at < s[1] {
				return true
			}
		}
		return false
	}
	firstAt := map[string]int{}
	mentioned := map[string]bool{}
	for _, m := range adviceIDAttr.FindAllStringSubmatchIndex(html, -1) {
		id := html[m[2]:m[3]]
		if inScript(m[0]) {
			mentioned[id] = true
			continue
		}
		if _, ok := firstAt[id]; !ok {
			firstAt[id] = m[0]
		}
	}
	var out []string
	said := map[string]bool{}
	for _, s := range scripts {
		body := html[s[2]:s[3]]
		if adviceWaits.MatchString(body) {
			continue
		}
		note := func(id, text string) {
			if !said[id] {
				said[id] = true
				out = append(out, text)
			}
		}
		for _, m := range adviceIDUse.FindAllStringSubmatch(body, -1) {
			id := strings.Join(m[1:], "")
			at, placed := firstAt[id]
			switch {
			case placed && at > s[1]:
				note(id, fmt.Sprintf("the script runs before the element %q exists. Move the script below it", id))
			case !placed && !mentioned[id] && !adviceMakesID.MatchString(body):
				note(id, fmt.Sprintf("no element has the id %q", id))
			}
		}
		// A bare id is a global, and only once the element exists.
		for id, at := range firstAt {
			if at > s[1] && regexp.MustCompile(`(^|[^\w.$"'-])`+regexp.QuoteMeta(id)+`\s*\.\s*\w`).MatchString(body) {
				note(id, fmt.Sprintf("the script runs before the element %q exists. Move the script below it", id))
			}
		}
	}
	return out
}
