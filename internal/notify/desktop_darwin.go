//go:build darwin

package notify

import (
	"context"
	"io"
	"os/exec"
	"strings"
)

// desktopChannel posts a macOS Notification Center banner via osascript. The
// note strings are passed as AppleScript string literals inside a single -e
// argument (no shell is involved), so they are escaped for that context.
type desktopChannel struct{}

func newDesktopChannel() channel { return &desktopChannel{} }

func (d *desktopChannel) name() string { return ChannelDesktop }

func (d *desktopChannel) send(ctx context.Context, n Note) error {
	return osascriptRun(ctx, osascriptArgv(n))
}

// osascriptArgv builds the full argv for the notification. The URL, when
// present, is shown as the banner subtitle. All interpolated strings are
// AppleScript-escaped so a title/body/URL can never break out of its literal.
func osascriptArgv(n Note) []string {
	script := `display notification "` + escapeAppleScript(n.Body) +
		`" with title "` + escapeAppleScript(n.Title) + `"`
	if n.URL != "" {
		script += ` subtitle "` + escapeAppleScript(n.URL) + `"`
	}
	return []string{"/usr/bin/osascript", "-e", script}
}

// escapeAppleScript renders s safe to embed inside an AppleScript double-quoted
// string literal: backslash and quote are escaped, and control characters that
// would otherwise be a syntax error (a literal newline cannot span an
// AppleScript string) become their escape sequences.
func escapeAppleScript(s string) string {
	return strings.NewReplacer(
		"\\", "\\\\",
		"\"", "\\\"",
		"\n", "\\n",
		"\r", "\\r",
		"\t", "\\t",
	).Replace(s)
}

// osascriptRun is the exec seam. Tests override it to capture the argv without
// invoking osascript(1). osascript's own output is discarded.
var osascriptRun = func(ctx context.Context, argv []string) error {
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	return cmd.Run()
}
