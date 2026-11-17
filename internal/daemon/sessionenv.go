package daemon

import (
	"os"
	"path/filepath"
	"strings"
)

// sessionStateVars are the variables a session's env file points at that session's private
// agent state (runtime's sandboxEnv). The daemon reads the same names to find the USER's
// agent homes: usage.ProjectsRoot, quota, claudetrust, the codex and opencode readers.
var sessionStateVars = []string{"CLAUDE_CONFIG_DIR", "CODEX_HOME", "XDG_DATA_HOME"}

// dropSessionEnv unsets every agent-home variable that points into lola's own session
// state, and names what it dropped.
//
// A shell or dev tab of a sandboxed session exports that session's env file. A daemon
// started from one (a dev command that runs the app, a `lola run` typed there) inherited
// CLAUDE_CONFIG_DIR and then treated ONE session's private config dir as the user's: every
// later session's transcripts were mounted under it, and the next daemon, started clean,
// found no transcript for any of them. A value the user set themselves is outside lola's
// home and stays.
func dropSessionEnv(home string) []string {
	if home == "" {
		return nil
	}
	var dropped []string
	for _, name := range sessionStateVars {
		v := os.Getenv(name)
		if v == "" || !sessionOwnedPath(home, v) {
			continue
		}
		if os.Unsetenv(name) == nil {
			dropped = append(dropped, name)
		}
	}
	return dropped
}

// sessionOwnedPath says whether path lies in a folder lola keeps per session: the sandbox
// state root, or a worktree (where older sessions kept that state).
func sessionOwnedPath(home, path string) bool {
	path = filepath.Clean(path)
	for _, dir := range []string{"sandbox", "worktrees", "local-sessions"} {
		root := filepath.Join(home, dir) + string(filepath.Separator)
		if strings.HasPrefix(path, root) {
			return true
		}
	}
	return false
}
