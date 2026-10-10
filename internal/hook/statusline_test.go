package hook

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/sushidev-team/lola/internal/quota"
)

func writeJSON(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestUserStatusLinePrecedence(t *testing.T) {
	dir, cfg := t.TempDir(), t.TempDir()
	writeJSON(t, filepath.Join(cfg, "settings.json"), `{"statusLine":{"type":"command","command":"user-cmd"}}`)
	if got := userStatusLine(dir, cfg); got != "user-cmd" {
		t.Fatalf("user = %q", got)
	}
	writeJSON(t, filepath.Join(dir, ".claude", "settings.json"), `{"statusLine":{"type":"command","command":"project-cmd"}}`)
	if got := userStatusLine(dir, cfg); got != "project-cmd" {
		t.Fatalf("project = %q", got)
	}
	writeJSON(t, filepath.Join(dir, ".claude", "settings.local.json"), `{"statusLine":{"type":"command","command":"/x/lola hook statusline"}}`)
	if got := userStatusLine(dir, cfg); got != "" {
		t.Fatalf("lola's own command must never be run (recursion), got %q", got)
	}
	if got := userStatusLine("", ""); got != "" {
		t.Fatalf("nothing configured = %q", got)
	}
}

func TestStatusLineRecordsLimitsAndPassesThrough(t *testing.T) {
	home := t.TempDir()
	t.Setenv("LOLA_HOME", home)
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	seen := filepath.Join(t.TempDir(), "seen.json")
	// The user's own command reads the same stdin and prints a line.
	writeJSON(t, filepath.Join(cfg, "settings.json"),
		`{"statusLine":{"type":"command","command":"tee `+seen+` >/dev/null; echo my-status"}}`)
	reset := time.Now().Add(time.Hour).Unix()
	in := `{"workspace":{"current_dir":"` + t.TempDir() + `"},"rate_limits":{"five_hour":{"used_percentage":42,"resets_at":` +
		itoa64(reset) + `}}}`

	var out bytes.Buffer
	StatusLine(strings.NewReader(in), &out)
	if out.String() != "my-status\n" {
		t.Fatalf("pass-through output = %q", out.String())
	}
	if got, _ := os.ReadFile(seen); string(got) != in {
		t.Fatalf("the user's command must get the same stdin, got %q", got)
	}
	s, err := quota.ReadClaude(quota.ClaudePath(home))
	if err != nil || len(s.Windows) != 1 || s.Windows[0].UsedPercent != 42 {
		t.Fatalf("recorded = %+v err %v", s, err)
	}
}

func TestStatusLinePassThroughIsBounded(t *testing.T) {
	old := statusLineTimeout
	defer func() { statusLineTimeout = old }()
	statusLineTimeout = 200 * time.Millisecond
	var out bytes.Buffer
	start := time.Now()
	runStatusLine("sleep 5 & sleep 5", "", nil, &out)
	if d := time.Since(start); d > 3*time.Second {
		t.Fatalf("a hung status line must be killed, took %v", d)
	}
}

func itoa64(n int64) string { return strconv.FormatInt(n, 10) }
