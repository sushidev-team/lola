package runtime

import (
	"os"
	"path/filepath"
	"strings"
)

// ResolveLolaBin picks the lola binary a session is wired to — the hooks in
// .lola/settings.json, codex's notify argv, the opencode plugin and the
// `lola report` lines of the briefing all bake this path in at spawn.
//
// exe is what the daemon was started from (os.Executable), and that is NOT
// always a binary that outlives the daemon. A daemon started from a build
// inside a session's worktree (`make build && ./lola run` in the lola repo's
// own sessions) keeps running from the unlinked inode after that worktree is
// torn down, while os.Executable still names the deleted path — so every
// session spawned afterwards got hooks and a briefing pointing at nothing, and
// failed silently: a hook that cannot exec is not an error Claude Code shows.
//
// So exe is used only while it still exists AND does not live under
// worktreesRoot (a worktree is temporary by design, even when its binary is
// still there right now). Otherwise the PATH's lola wins, under the same two
// conditions. A worktree binary that still exists beats nothing, and the bare
// name — a PATH lookup in the pane — is the last resort.
//
// lookPath and exists are seams; nil means exec.LookPath / an executable
// regular file on disk.
func ResolveLolaBin(exe, worktreesRoot string, lookPath func(string) (string, error), exists func(string) bool) string {
	if exists == nil {
		exists = isExecutableFile
	}
	stable := func(p string) bool {
		return p != "" && filepath.IsAbs(p) && exists(p) && !underDir(p, worktreesRoot)
	}
	if stable(exe) {
		return exe
	}
	if lookPath != nil {
		if p, err := lookPath("lola"); err == nil {
			if abs, aerr := filepath.Abs(p); aerr == nil && stable(abs) {
				return abs
			}
		}
	}
	if exe != "" && filepath.IsAbs(exe) && exists(exe) {
		return exe
	}
	return "lola"
}

// underDir reports whether p lies inside dir (never dir itself). An empty dir
// contains nothing.
func underDir(p, dir string) bool {
	if dir == "" {
		return false
	}
	rel, err := filepath.Rel(filepath.Clean(dir), filepath.Clean(p))
	return err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func isExecutableFile(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.Mode().IsRegular() && fi.Mode()&0o111 != 0
}

// lolaBin is the binary this spawn wires in: re-resolved per launch when the
// daemon supplied a resolver (the file can disappear long after startup), the
// fixed LolaBin otherwise.
func (n *Native) lolaBin() string {
	if n.ResolveLolaBin == nil {
		return n.LolaBin
	}
	bin := n.ResolveLolaBin()
	if bin != n.LolaBin && n.Logf != nil {
		n.Logf("lola binary %q is gone or inside a worktree; wiring sessions to %q instead", n.LolaBin, bin)
	}
	return bin
}
