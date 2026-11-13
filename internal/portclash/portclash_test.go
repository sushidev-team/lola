package portclash

import (
	"strings"
	"testing"
)

// The wordings that matter are the ones real dev_commands print. Each of these
// is a line lola has to recognize, because the whole feature is "the tab died
// instantly and nobody read why".
func TestPortRecognizesTheWordingsServersUse(t *testing.T) {
	cases := []struct {
		name string
		line string
		want int
	}{
		{"go listen", `ERROR  listen tcp 127.0.0.1:9245: bind: address already in use`, 9245},
		{"node eaddrinuse", `Error: listen EADDRINUSE: address already in use 127.0.0.1:3000`, 3000},
		{"node ipv6", `Error: listen EADDRINUSE: address already in use :::3000`, 3000},
		{"vite strict port", `Error: Port 9245 is already in use`, 9245},
		{"php artisan", `Failed to listen on 127.0.0.1:8000 (reason: Address already in use)`, 8000},
		{"docker", `Bind for 0.0.0.0:8080 failed: port is already allocated`, 8080},
		{"puma", `Address already in use - bind(2) for "127.0.0.1" port 3000`, 3000},
		{"ipv6 bracket", `listen tcp [::1]:5173: bind: address already in use`, 5173},
		{"wildcard", `cannot bind *:4000: address already in use`, 4000},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := Port("starting…\n" + tc.line + "\n")
			if !ok || got != tc.want {
				t.Fatalf("Port(%q) = %d, %v; want %d, true", tc.line, got, ok, tc.want)
			}
		})
	}
}

// FAIL CLOSED: the caller's next act is offering to kill a process, so anything
// this package is not sure about must report nothing at all.
func TestPortReportsNothingWithoutBothHalvesOfTheCue(t *testing.T) {
	for _, pane := range []string{
		"",
		"vite v5.0.0 ready in 312 ms\n  ➜  Local: http://localhost:5173/\n",
		// The phrase with no address anywhere: Python's bare errno message.
		"OSError: [Errno 48] Address already in use\n",
		// An address with no failure: the ordinary startup line.
		"Server running on http://127.0.0.1:8000\n",
		// Prose that merely contains the words — an agent's pane, not a server's.
		"I checked whether the port is in use and it is free\n",
		// A port number outside the legal range is a number matched by accident.
		"listen tcp 127.0.0.1:99999: bind: address already in use\n",
	} {
		if port, ok := Port(pane); ok {
			t.Errorf("Port(%q) reported :%d; want nothing", pane, port)
		}
	}
}

// A tab that failed, was restarted and failed again must report the LATEST
// failure — the scrollback still holds the first one.
func TestPortPrefersTheNewestFailure(t *testing.T) {
	pane := strings.Join([]string{
		`listen tcp 127.0.0.1:8000: bind: address already in use`,
		`^C`,
		`listen tcp 127.0.0.1:8001: bind: address already in use`,
	}, "\n")
	if got, ok := Port(pane); !ok || got != 8001 {
		t.Fatalf("Port = %d, %v; want 8001, true", got, ok)
	}
}

// A timestamp is the one thing that can look like a bare ":port", which is why
// the host-anchored form is tried first and the bare one last.
func TestPortIgnoresATimestampBesideARealAddress(t *testing.T) {
	pane := `[14:37:37] Error: listen tcp 127.0.0.1:9245: bind: address already in use`
	if got, ok := Port(pane); !ok || got != 9245 {
		t.Fatalf("Port = %d, %v; want 9245, true", got, ok)
	}
}

// Only the tail is scanned: a pane holds hours of scrollback and the message
// that killed the command is the last thing in it.
func TestPortScansOnlyTheTail(t *testing.T) {
	old := `listen tcp 127.0.0.1:8000: bind: address already in use`
	pane := old + strings.Repeat("\nbuilding…", MaxScanLines+50)
	if port, ok := Port(pane); ok {
		t.Fatalf("Port reported :%d from beyond the scan window", port)
	}
}
