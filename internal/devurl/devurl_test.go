package devurl

import (
	"strings"
	"testing"
)

func best(t *testing.T, pane string) string {
	t.Helper()
	got := URLs(pane)
	if len(got) == 0 {
		return ""
	}
	return got[0]
}

// The case this package exists for: `composer dev` runs four servers through
// concurrently into ONE pane, and the app is on 8001 because 8000 was taken.
// The app URL has to win over Vite's, which is printed later and looks just as
// much like an address.
func TestScanPrefersTheAppServerOverTheAssetServer(t *testing.T) {
	pane := strings.Join([]string{
		"[queue]  Processing jobs from the [{lola-nori-app-nor-352}] queue.",
		"[server]    INFO  Server running on [http://127.0.0.1:8001].",
		"[vite]   VITE v5.4.10  ready in 412 ms",
		"[vite]   ➜  Local:   http://localhost:5175/",
		"[vite]   ➜  Network: use --host to expose",
		"[logs]   INFO  Tailing application logs.",
	}, "\n")

	if got := best(t, pane); got != "http://127.0.0.1:8001" {
		t.Errorf("best = %q, want the app server", got)
	}
	// Vite's is still offered — it is a real local URL, just not the first one
	// a human means — and the Network hint carries no address to offer.
	got := URLs(pane)
	if len(got) != 2 || got[1] != "http://localhost:5175" {
		t.Errorf("URLs = %v, want the app server then Vite", got)
	}
}

// A LAN address is not a local testing URL, and a docs link is not an address
// at all. Both are dropped rather than ranked down: the result is handed to an
// opener, and pane text is untrusted.
func TestScanKeepsOnlyLoopbackAddresses(t *testing.T) {
	pane := strings.Join([]string{
		"   ▲ Next.js 15.0.3",
		"   - Local:        http://localhost:3000",
		"   - Network:      http://192.168.1.20:3000",
		"   see https://vitejs.dev/guide/ for more",
		"   failed to read file:///etc/hosts",
	}, "\n")

	got := URLs(pane)
	if len(got) != 1 || got[0] != "http://localhost:3000" {
		t.Errorf("URLs = %v, want only the loopback one", got)
	}
}

// 0.0.0.0 means "every interface", which is not something to click. The
// loopback it implies is.
func TestScanRewritesTheWildcardHost(t *testing.T) {
	if got := best(t, "Starting development server at http://0.0.0.0:8000/"); got != "http://127.0.0.1:8000" {
		t.Errorf("best = %q, want the loopback rewrite", got)
	}
}

// Plenty of tools print a port and never a URL. A serving cue in front of the
// number is what separates an address from any other number in a log.
func TestScanFallsBackToAPortWithAServingCue(t *testing.T) {
	if got := best(t, "info: Server listening on port 4000"); got != "http://localhost:4000" {
		t.Errorf("best = %q, want the synthesized URL", got)
	}
	// No cue, no address: a bare number is a build stat, an exit code, a size.
	for _, line := range []string{"compiled 3000 modules", "webpack 5.90.0 built in 3000 ms"} {
		if got := best(t, line); got != "" {
			t.Errorf("best(%q) = %q, want nothing", line, got)
		}
	}
}

// The pane is captured WITH escape sequences, and colour runs sit between the
// cue and the URL — so the cue would be missed and the URL would carry escape
// bytes into the opener.
func TestScanReadsThroughANSIColour(t *testing.T) {
	pane := "\x1b[32m  INFO \x1b[0m Server running on [\x1b[36mhttp://127.0.0.1:8000\x1b[0m]."
	if got := best(t, pane); got != "http://127.0.0.1:8000" {
		t.Errorf("best = %q, want the URL without escapes", got)
	}
}

// A restart reprints the address. The newest print describes what is running.
func TestScanPrefersTheLatestSightingOnATie(t *testing.T) {
	pane := strings.Join([]string{
		"  Local:   http://localhost:3000",
		"  restarting…",
		"  Local:   http://localhost:3001",
	}, "\n")
	if got := best(t, pane); got != "http://localhost:3001" {
		t.Errorf("best = %q, want the most recent address", got)
	}
}

// Trailing punctuation is part of the sentence, not of the URL, and a path is
// part of the URL (a tool may point at a sub-page) unless it is a bare slash.
func TestScanTrimsTheSentenceButKeepsThePath(t *testing.T) {
	if got := best(t, "Mailpit is at http://localhost:8025/mail, open it."); got != "http://localhost:8025/mail" {
		t.Errorf("best = %q", got)
	}
	if got := best(t, "➜  Local:   http://localhost:5173/"); got != "http://localhost:5173" {
		t.Errorf("best = %q, want the bare slash dropped", got)
	}
}

// A Valet/Herd site is a local testing URL even though it carries no port.
func TestScanAcceptsLocalDevelopmentDomains(t *testing.T) {
	if got := best(t, "Serving at https://nori-app.test"); got != "https://nori-app.test" {
		t.Errorf("best = %q", got)
	}
}

// HMR and the node inspector are addresses no human wants to open, and they are
// printed right beside the one they do.
func TestScanRanksHMRAndInspectorLast(t *testing.T) {
	pane := strings.Join([]string{
		"Debugger listening on ws://127.0.0.1:9229",
		"  hmr update http://localhost:24678",
		"  ➜  Local:   http://localhost:3000",
	}, "\n")
	if got := best(t, pane); got != "http://localhost:3000" {
		t.Errorf("best = %q, want the app", got)
	}
}

// Nothing to find is the common case for a pane that runs a watcher.
func TestScanOnAPaneWithNoAddress(t *testing.T) {
	pane := "> tailwindcss -i app.css -o public/app.css --watch\nRebuilding...\nDone in 142ms."
	if got := URLs(pane); len(got) != 0 {
		t.Errorf("URLs = %v, want nothing", got)
	}
}

// The cap is what keeps a chip row from becoming a log viewer.
func TestScanCapsWhatItReturns(t *testing.T) {
	var lines []string
	for _, p := range []string{"3000", "3001", "3002", "3003", "3004"} {
		lines = append(lines, "Listening on http://localhost:"+p)
	}
	if got := URLs(strings.Join(lines, "\n")); len(got) != MaxCandidates {
		t.Errorf("got %d URLs, want %d", len(got), MaxCandidates)
	}
}
