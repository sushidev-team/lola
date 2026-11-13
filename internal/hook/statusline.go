package hook

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/quota"
)

// The status line is the ONLY place Claude Code exposes the user's
// subscription limits (internal/quota), so SettingsJSON points each lola
// session's status line at `lola hook statusline`. That replaces the user's
// own status line inside lola panes — `--settings` outranks every other
// settings source — so StatusLine runs the user's configured command with the
// same stdin and prints its output, and the pane looks exactly as it would
// without lola. Rules, in order of importance:
//
//   - The pass-through ALWAYS runs, whatever happened to the recording: lola's
//     bookkeeping must never cost the user their status line.
//   - It is bounded (statusLineTimeout, whole process group killed), because
//     Claude Code waits on it for every redraw.
//   - A command that is itself `lola hook statusline` is never run (it would
//     recurse until the timeout).

// statusLineTimeout bounds the user's command (a var for tests).
var statusLineTimeout = 5 * time.Second

const (
	// maxStatusInput caps the stdin read; the payload is a few KB.
	maxStatusInput = 1 << 20
	statusLineSelf = "hook statusline"
)

// StatusLine serves `lola hook statusline`: record the limits, then pass
// through to the user's own status-line command.
func StatusLine(stdin io.Reader, stdout io.Writer) {
	raw, _ := io.ReadAll(io.LimitReader(stdin, maxStatusInput))
	var in quota.StatusLineInput
	_ = json.Unmarshal(raw, &in)
	if s, ok := quota.ParseClaude(in, time.Now()); ok {
		if home, err := config.Home(); err == nil {
			_ = quota.RecordClaude(quota.ClaudePath(home), s) // best-effort
		}
	}
	if cmd := userStatusLine(in.Dir(), claudeConfigDir()); cmd != "" {
		runStatusLine(cmd, in.Dir(), raw, stdout)
	}
}

func claudeConfigDir() string {
	if d := os.Getenv("CLAUDE_CONFIG_DIR"); d != "" {
		return d
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".claude")
}

// userStatusLine is the status-line command the user would have had without
// lola, by Claude Code's own precedence: the project's local settings, then
// the project's, then the user's. "" when none is configured (or it is lola's
// own, which would recurse).
func userStatusLine(dir, configDir string) string {
	var candidates []string
	if dir != "" {
		candidates = append(candidates,
			filepath.Join(dir, ".claude", "settings.local.json"),
			filepath.Join(dir, ".claude", "settings.json"))
	}
	if configDir != "" {
		candidates = append(candidates, filepath.Join(configDir, "settings.json"))
	}
	for _, path := range candidates {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var s struct {
			StatusLine *struct {
				Type    string `json:"type"`
				Command string `json:"command"`
			} `json:"statusLine"`
		}
		if json.Unmarshal(data, &s) != nil || s.StatusLine == nil {
			continue
		}
		cmd := strings.TrimSpace(s.StatusLine.Command)
		if s.StatusLine.Type != "command" || cmd == "" || strings.Contains(cmd, statusLineSelf) {
			return "" // the highest-precedence source decides, even when unusable
		}
		return cmd
	}
	return ""
}

// runStatusLine runs cmd through the shell, as Claude Code would, in dir with
// raw on stdin, copying its stdout. Output is buffered and written only once
// the command is done, so a timed-out command prints nothing rather than half
// a line.
func runStatusLine(command, dir string, raw []byte, stdout io.Writer) {
	ctx, cancel := context.WithTimeout(context.Background(), statusLineTimeout)
	defer cancel()
	c := exec.CommandContext(ctx, "/bin/sh", "-c", command)
	if dir != "" {
		if st, err := os.Stat(dir); err == nil && st.IsDir() {
			c.Dir = dir
		}
	}
	c.Stdin = bytes.NewReader(raw)
	var out bytes.Buffer
	c.Stdout = &out
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	c.Cancel = func() error { return syscall.Kill(-c.Process.Pid, syscall.SIGKILL) }
	c.WaitDelay = time.Second
	if c.Run() == nil || out.Len() > 0 {
		_, _ = stdout.Write(out.Bytes())
	}
}
