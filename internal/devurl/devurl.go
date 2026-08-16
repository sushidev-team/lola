// Package devurl finds the LOCAL testing URL a dev command printed into its
// pane — `http://127.0.0.1:8000`, `http://localhost:5173/` — so a surface can
// offer it as one click instead of leaving a human to read it out of a
// scrolling log and retype it.
//
// It cannot be a lookup table. lola does not know what `[[project]].dev_commands`
// runs (`composer dev`, `npm run dev`, `mix phx.server`, a Makefile), what port
// it chose, or how many servers hide behind one command — `composer dev` alone
// prints an app server, a queue worker, a log tailer and a Vite dev server into
// ONE pane, each with its own line format. So the pane text is scored rather
// than matched: every local URL is a CANDIDATE, and the lines around it decide
// which one a human means by "the app".
//
// Two things it deliberately does NOT do:
//   - Guess a scheme or a host. Only http(s) and only a loopback-ish host are
//     ever returned, because the result is handed to an opener — a URL scraped
//     from a log is untrusted text, and `file://` or a LAN address must never
//     come back out of here.
//   - Rank by port number alone. A port is the weakest possible signal (Vite
//     and Laravel both move when their default is taken, which is the whole
//     reason this exists); the CUE — "Server running on", a `[server]` label,
//     "Local:" — is what carries the meaning.
//
// Stdlib only, no I/O: the daemon captures the pane, this reads it.
package devurl

import (
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// MaxCandidates bounds what a caller ships to a UI. One pane can print a dozen
// URLs (a restart reprints them all); a row of chips is not a log viewer.
const MaxCandidates = 3

// Candidate is one local URL found in the pane, with the evidence that ranked
// it. Score is comparable only within one scan.
type Candidate struct {
	URL   string
	Host  string
	Port  int
	Score int
	Line  string // the (trimmed, de-ANSI'd) line it was found on — evidence
}

var (
	// ANSI: the pane is captured with escapes (`capture-pane -e`), and a colour
	// run can sit between "on" and the URL, so cues would be missed unless the
	// text is cleaned first. Covers CSI/most escapes plus OSC (which tmux emits
	// for hyperlinks and titles).
	ansiCSIRe = regexp.MustCompile(`\x1b\[[0-9;:?]*[ -/]*[@-~]`)
	ansiOSCRe = regexp.MustCompile(`\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)`)
	ansiMisc  = regexp.MustCompile(`\x1b[@-Z\\-_]`)

	// A URL with an explicit scheme. The host alternation keeps IPv6's own
	// brackets from colliding with the brackets Laravel wraps its URL in
	// (`Server running on [http://127.0.0.1:8000]`).
	urlRe = regexp.MustCompile(`(?i)\bhttps?://(?:\[[0-9a-f:]+\]|[a-z0-9](?:[a-z0-9._-]*[a-z0-9])?)(?::\d{1,5})?(?:/[^\s"'<>\x60]*)?`)

	// The fallback for a command that names a port without printing a URL
	// ("Listening on port 3000", "server started on port 8080"). Deliberately
	// requires a serving cue in front of the number: a bare "3000" in a log line
	// is not an address.
	portOnlyRe = regexp.MustCompile(`(?i)\b(?:listening|running|serving|started|ready|available)\b[^0-9\n]{0,24}?\bport\b[^0-9\n]{0,6}(\d{2,5})`)

	// The strong cue: this line is announcing a server.
	serverCueRe = regexp.MustCompile(`(?i)\b(server (?:is )?running|running (?:at|on)|listening (?:on|at)|listening|server started|started server|now listening|ready (?:on|at|in)|serving (?:at|on)|available (?:at|on)|app running)\b`)

	// The medium cue: the "Local:" line Vite, Next, Astro, Nuxt and friends all
	// print. It marks the address a human should open — but those tools are also
	// usually the ASSET server, so it ranks below an explicit server line.
	localCueRe = regexp.MustCompile(`(?i)(^|\s)local(host)?\s*:\s*`)

	// concurrently/overmind/foreman prefix every line with the process that
	// wrote it, which is the best evidence in a multi-server pane about WHICH
	// server a URL belongs to.
	labelRe = regexp.MustCompile(`^\s*[\[(]([a-z0-9_-]{1,16})[\])]`)

	appLabelRe   = regexp.MustCompile(`(?i)^(server|serve|app|web|api|http|backend|php|artisan|rails|django|next|nuxt|remix)$`)
	assetLabelRe = regexp.MustCompile(`(?i)^(vite|assets|asset|css|tailwind|webpack|esbuild|rollup|watch|queue|worker|horizon|logs|log|pail|mail|sockudo|redis|db|docker)$`)

	// Lines that mention an address which is NOT the one to open.
	notLocalCueRe = regexp.MustCompile(`(?i)\b(network|external|hmr|websocket|inspector|debugger|devtools|proxy target|press h)\b`)
)

// loopback names the hosts a local testing URL may carry. Anything else — a LAN
// IP from a "Network:" line, a real domain from a log — is dropped rather than
// ranked down: handing an opener the wrong host is worse than handing it
// nothing.
func loopback(host string) (string, bool) {
	h := strings.ToLower(host)
	switch h {
	case "localhost", "127.0.0.1", "[::1]", "::1", "host.docker.internal":
		return h, true
	case "0.0.0.0", "[::]":
		// "Bound on every interface" is not an address to open — but it does
		// mean the loopback works, and that is what a human clicks.
		return "127.0.0.1", true
	}
	// A local development TLD (Valet/Herd `.test`, `.localhost`).
	if strings.HasSuffix(h, ".test") || strings.HasSuffix(h, ".localhost") || strings.HasSuffix(h, ".local") {
		return h, true
	}
	return "", false
}

// Scan reads a captured pane and returns the local URLs it advertises, best
// first and deduplicated, capped at MaxCandidates.
func Scan(pane string) []Candidate {
	best := map[string]Candidate{}
	order := map[string]int{}

	for i, raw := range strings.Split(pane, "\n") {
		line := strings.TrimSpace(stripANSI(raw))
		if line == "" {
			continue
		}
		bonus := lineScore(line)
		for _, c := range candidatesIn(line) {
			c.Score += bonus
			c.Line = line
			prev, seen := best[c.URL]
			// A later sighting of the same URL wins ties: a dev server that
			// restarted reprints its address, and the newest print is the one
			// describing what is running now.
			if !seen || c.Score >= prev.Score {
				best[c.URL] = c
				order[c.URL] = i
			}
		}
	}

	out := make([]Candidate, 0, len(best))
	for _, c := range best {
		out = append(out, c)
	}
	sort.SliceStable(out, func(a, b int) bool {
		if out[a].Score != out[b].Score {
			return out[a].Score > out[b].Score
		}
		return order[out[a].URL] > order[out[b].URL]
	})
	if len(out) > MaxCandidates {
		out = out[:MaxCandidates]
	}
	return out
}

// URLs is Scan reduced to the addresses, for a caller that only ships strings.
func URLs(pane string) []string {
	cands := Scan(pane)
	out := make([]string, 0, len(cands))
	for _, c := range cands {
		out = append(out, c.URL)
	}
	return out
}

// candidatesIn extracts every acceptable URL from one line, plus the
// port-without-a-URL fallback.
func candidatesIn(line string) []Candidate {
	var out []Candidate
	for _, m := range urlRe.FindAllString(line, -1) {
		if c, ok := parseURL(m); ok {
			out = append(out, c)
		}
	}
	if len(out) > 0 {
		return out
	}
	if m := portOnlyRe.FindStringSubmatch(line); m != nil {
		port, err := strconv.Atoi(m[1])
		if err == nil && port > 0 && port <= 65535 {
			out = append(out, Candidate{
				URL:  "http://localhost:" + m[1],
				Host: "localhost",
				Port: port,
				// Half a server cue: the line SAYS it is serving, but the
				// address is this package's construction, not the tool's word.
				Score: portScore(port) - 10,
			})
		}
	}
	return out
}

// parseURL validates one match and scores it on its own merits (host + port).
func parseURL(raw string) (Candidate, bool) {
	// Log lines wrap URLs in brackets and end sentences with them.
	url := strings.TrimRight(raw, ".,;:!?)]}'\"")
	rest, ok := strings.CutPrefix(strings.ToLower(url), "http://")
	scheme := "http://"
	if !ok {
		rest, ok = strings.CutPrefix(strings.ToLower(url), "https://")
		scheme = "https://"
		if !ok {
			return Candidate{}, false
		}
	}
	// Split host[:port] from the path, minding IPv6's brackets.
	hostPort := rest
	path := ""
	if i := strings.IndexByte(rest, '/'); i >= 0 {
		hostPort, path = rest[:i], rest[i:]
	}
	host, port := hostPort, 0
	if i := strings.LastIndexByte(hostPort, ':'); i >= 0 && !strings.HasSuffix(hostPort, "]") {
		if n, err := strconv.Atoi(hostPort[i+1:]); err == nil && n > 0 && n <= 65535 {
			host, port = hostPort[:i], n
		}
	}
	norm, ok := loopback(host)
	if !ok {
		return Candidate{}, false
	}
	// The path is kept (a tool may point at /admin) but a bare "/" is noise.
	if path == "/" {
		path = ""
	}
	rebuilt := scheme + norm
	if port > 0 {
		rebuilt += ":" + strconv.Itoa(port)
	}
	return Candidate{URL: rebuilt + path, Host: norm, Port: port, Score: portScore(port)}, true
}

// portScore only ever subtracts. There is no such thing as an app port — the
// whole reason this package exists is that a taken 8000 becomes 8001 and a
// taken 5173 becomes 5175 — so a "looks like an app port" bonus would be a
// guess dressed as evidence, and it would outrank the recency tie-break that
// actually means something. A port that belongs to a well-known NON-app service
// is different: that is knowledge, not a guess.
func portScore(port int) int {
	switch port {
	case 24678, 9229, 35729: // HMR, node inspector, livereload — never the app
		return -30
	}
	if port >= 5173 && port <= 5199 { // Vite's range, including its fallbacks
		return -12
	}
	return 0
}

// lineScore is what the words around a URL say about it.
func lineScore(line string) int {
	score := 0
	if serverCueRe.MatchString(line) {
		score += 40
	} else if localCueRe.MatchString(line) {
		score += 20
	}
	if m := labelRe.FindStringSubmatch(line); m != nil {
		switch {
		case appLabelRe.MatchString(m[1]):
			score += 15
		case assetLabelRe.MatchString(m[1]):
			score -= 25
		}
	}
	if notLocalCueRe.MatchString(line) {
		score -= 45
	}
	return score
}

// stripANSI removes the escape sequences `capture-pane -e` leaves in the text.
func stripANSI(s string) string {
	if !strings.ContainsRune(s, 0x1b) {
		return s
	}
	s = ansiOSCRe.ReplaceAllString(s, "")
	s = ansiCSIRe.ReplaceAllString(s, "")
	return ansiMisc.ReplaceAllString(s, "")
}
