//go:build darwin

package notify

import (
	"context"
	"strings"
	"testing"
)

func TestOsascriptArgv(t *testing.T) {
	got := osascriptArgv(Note{Title: "Approved", Body: "PR is green", URL: "http://pr/7"})
	want := []string{
		"/usr/bin/osascript", "-e",
		`display notification "PR is green" with title "Approved" subtitle "http://pr/7"`,
	}
	if len(got) != len(want) {
		t.Fatalf("argv = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("argv[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestOsascriptArgvNoURLOmitsSubtitle(t *testing.T) {
	got := osascriptArgv(Note{Title: "T", Body: "B"})
	want := `display notification "B" with title "T"`
	if got[2] != want {
		t.Fatalf("script = %q, want %q", got[2], want)
	}
}

// TestEscapeAppleScript verifies quotes, backslashes and newlines cannot break
// out of the AppleScript string literal.
func TestEscapeAppleScript(t *testing.T) {
	in := "say \"hi\"\nback\\slash\ttab"
	got := escapeAppleScript(in)
	want := `say \"hi\"\nback\\slash\ttab`
	if got != want {
		t.Fatalf("escapeAppleScript = %q, want %q", got, want)
	}
	// The escaped form must contain no raw newline or bare unescaped quote.
	if strings.ContainsAny(got, "\n\r\t") {
		t.Fatalf("escaped output still has a raw control char: %q", got)
	}
}

// TestDesktopSendUsesSeam confirms send routes through the exec seam with the
// built argv and never actually launches osascript in tests.
func TestDesktopSendUsesSeam(t *testing.T) {
	orig := osascriptRun
	t.Cleanup(func() { osascriptRun = orig })

	var gotArgv []string
	osascriptRun = func(_ context.Context, argv []string) error {
		gotArgv = argv
		return nil
	}

	d := newDesktopChannel()
	if d == nil {
		t.Fatal("newDesktopChannel returned nil on darwin")
	}
	if err := d.send(context.Background(), Note{Title: "T", Body: "B"}); err != nil {
		t.Fatalf("send: %v", err)
	}
	want := `display notification "B" with title "T"`
	if len(gotArgv) != 3 || gotArgv[0] != "/usr/bin/osascript" || gotArgv[1] != "-e" || gotArgv[2] != want {
		t.Fatalf("seam got argv %v, want [/usr/bin/osascript -e %q]", gotArgv, want)
	}
}
