package agent

import (
	"os"
	"path/filepath"
)

// HostAuthFile is where the agent of kind k keeps its login on this Mac:
// codex's auth.json ($CODEX_HOME, else ~/.codex) and opencode's
// ($XDG_DATA_HOME/opencode, else ~/.local/share/opencode). A sandboxed agent
// cannot read the host's home, so lola copies this file into the session's
// private config at launch. "" for claude (its login is a keychain token) and
// when the home directory cannot be resolved.
func HostAuthFile(k Kind) string {
	home, _ := os.UserHomeDir()
	switch k {
	case Codex:
		dir := os.Getenv("CODEX_HOME")
		if dir == "" {
			if home == "" {
				return ""
			}
			dir = filepath.Join(home, ".codex")
		}
		return filepath.Join(dir, "auth.json")
	case OpenCode:
		root := os.Getenv("XDG_DATA_HOME")
		if root == "" {
			if home == "" {
				return ""
			}
			root = filepath.Join(home, ".local", "share")
		}
		return filepath.Join(root, "opencode", "auth.json")
	}
	return ""
}

// HostLoggedIn reports whether HostAuthFile(k) exists as a regular file.
func HostLoggedIn(k Kind) bool {
	p := HostAuthFile(k)
	if p == "" {
		return false
	}
	fi, err := os.Stat(p)
	return err == nil && fi.Mode().IsRegular() && fi.Size() > 0
}
