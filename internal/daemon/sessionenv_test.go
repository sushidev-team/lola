package daemon

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/sushidev-team/lola/internal/usage"
)

// A daemon started from a sandboxed session's shell must not adopt that session's private
// claude config dir as the user's: every transcript it then mounted or read was in the
// wrong place, and the Conversation of each of those sessions came up empty.
func TestDropSessionEnvIgnoresASessionsAgentHome(t *testing.T) {
	home := t.TempDir()
	user := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, "sandbox", "lola-x-1", "claude"))
	t.Setenv("CODEX_HOME", filepath.Join(home, "worktrees", "x", "lola-x-1", ".lola", "codex"))
	t.Setenv("XDG_DATA_HOME", user)

	got := dropSessionEnv(home)

	if want := []string{"CLAUDE_CONFIG_DIR", "CODEX_HOME"}; !slices.Equal(got, want) {
		t.Fatalf("dropped %v, want %v", got, want)
	}
	if v, ok := os.LookupEnv("CLAUDE_CONFIG_DIR"); ok {
		t.Errorf("CLAUDE_CONFIG_DIR still set: %q", v)
	}
	if v := os.Getenv("XDG_DATA_HOME"); v != user {
		t.Errorf("the user's own XDG_DATA_HOME was touched: %q", v)
	}
	if root := usage.ProjectsRoot(); sessionOwnedPath(home, root) {
		t.Errorf("transcripts still resolve inside lola's home: %s", root)
	}
}

func TestDropSessionEnvKeepsAUsersOwnConfigDir(t *testing.T) {
	home := t.TempDir()
	user := filepath.Join(t.TempDir(), "claude")
	t.Setenv("CLAUDE_CONFIG_DIR", user)
	// A sibling whose name only starts like lola's folder is not lola's.
	t.Setenv("CODEX_HOME", filepath.Join(home, "sandboxes", "codex"))

	if got := dropSessionEnv(home); len(got) != 0 {
		t.Fatalf("dropped %v, want nothing", got)
	}
	if v := os.Getenv("CLAUDE_CONFIG_DIR"); v != user {
		t.Errorf("CLAUDE_CONFIG_DIR = %q, want %q", v, user)
	}
}
