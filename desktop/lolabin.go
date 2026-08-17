package main

// Where the `lola` CLI comes from, and how the app puts one on the user's PATH.
//
// The desktop app is a CLIENT of the daemon: it starts one by exec'ing
// `lola run`, so it needs the CLI binary and cannot re-exec itself the way the
// TUI does. The DMG used to carry only Lola.app, so a fresh install failed the
// moment the first-run wizard tried to start the daemon — "lola binary not
// found on PATH" — with nothing in the UI saying where to get one.
//
// The bundle therefore SHIPS the CLI at Contents/Resources/bin/lola, and this
// file owns the resolution order:
//
//	$LOLA_BIN  →  PATH  →  the bundled copy
//
// PATH stays AHEAD of the bundle deliberately. A developer's own build
// ($GOPATH/bin/lola, which the restart button re-execs) is the documented dev
// loop; silently preferring the shipped copy would make `go install` look like
// a no-op. The bundled copy is the FLOOR — what makes a plain DMG install work
// — never the ceiling.
//
// Because PATH can win, the two binaries can disagree in version, which is its
// own documented trap (a daemon predating a command answers `unknown cmd`).
// CLIInfo reports both versions so the UI can say so instead of leaving the
// user to debug a feature that is merely older than the app.

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// lolaSource names where a resolved CLI came from. Surfaced in the UI because
// "which lola is this app about to run" is otherwise invisible.
type lolaSource string

const (
	srcEnv     lolaSource = "LOLA_BIN" // explicit override
	srcPath    lolaSource = "PATH"     // the user's own install
	srcBundled lolaSource = "bundled"  // shipped inside Lola.app
)

// bundledRelPath is the CLI's location inside the .app, relative to the
// executable's own directory (Contents/MacOS). Kept in lockstep with
// build/darwin/Taskfile.yml's create:app:bundle.
var bundledRelPath = filepath.Join("..", "Resources", "bin", "lola")

// versionTimeout bounds `lola --version`. It is a local binary printing one
// line; anything slower is a hung process we must not wait on, since CLIInfo
// runs on the UI's path.
const versionTimeout = 3 * time.Second

// errNoLola is returned when no CLI can be resolved at all. Its text is
// user-facing (the app shows it verbatim), so it names the fix, not the cause.
var errNoLola = errors.New(
	"the lola CLI could not be found. Reinstall Lola from the DMG (it ships the CLI), " +
		"install it separately, or set LOLA_BIN to its path")

// lolaBin is a resolved CLI: which binary, and why that one.
type lolaBin struct {
	Path   string
	Source lolaSource
}

// executablePath is the seam over os.Executable so tests can place a fake
// bundle anywhere. It resolves symlinks, because a .app reached through one
// would otherwise put Resources/ in the wrong place.
var executablePath = func() (string, error) {
	p, err := os.Executable()
	if err != nil {
		return "", err
	}
	if resolved, rerr := filepath.EvalSymlinks(p); rerr == nil {
		return resolved, nil
	}
	return p, nil
}

// bundledLolaPath returns the CLI shipped inside this .app, or "" when there is
// none (a `go run` build, or a bundle from before the CLI was packaged). It
// insists on a regular, executable file: a directory or a stale non-executable
// leftover must not be handed to exec.
func bundledLolaPath() string {
	exe, err := executablePath()
	if err != nil {
		return ""
	}
	cand := filepath.Clean(filepath.Join(filepath.Dir(exe), bundledRelPath))
	fi, err := os.Stat(cand)
	if err != nil || !fi.Mode().IsRegular() || fi.Mode().Perm()&0o111 == 0 {
		return ""
	}
	return cand
}

// resolveLola applies the documented order. $LOLA_BIN is taken on trust (an
// explicit override is a decision, and reporting "your override is wrong" beats
// silently falling through to a different binary than the one named).
func resolveLola() (lolaBin, error) {
	if b := strings.TrimSpace(os.Getenv("LOLA_BIN")); b != "" {
		return lolaBin{Path: b, Source: srcEnv}, nil
	}
	if p, err := exec.LookPath("lola"); err == nil {
		return lolaBin{Path: p, Source: srcPath}, nil
	}
	if p := bundledLolaPath(); p != "" {
		return lolaBin{Path: p, Source: srcBundled}, nil
	}
	return lolaBin{}, errNoLola
}

// lolaBinary is the lifecycle path's entry point: the absolute binary to exec
// as `lola run`.
func lolaBinary() (string, error) {
	b, err := resolveLola()
	if err != nil {
		return "", err
	}
	return b.Path, nil
}

// binVersion runs `<bin> --version` and returns its first line, or "" when the
// binary cannot answer. A failure is never an error here: an unreadable version
// is a display gap, not a reason to refuse to start a daemon.
func binVersion(path string) string {
	if path == "" {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), versionTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, "--version").Output()
	if err != nil {
		return ""
	}
	line, _, _ := strings.Cut(strings.TrimSpace(string(out)), "\n")
	return strings.TrimSpace(line)
}

// --- installing the CLI on PATH ---------------------------------------------

// cliInstallDirs are the candidate directories for InstallCLI, in preference
// order. /usr/local/bin first because it is on the default PATH of every macOS
// shell, so a symlink there works without the user editing a profile; the
// Homebrew prefix next; ~/.local/bin last as the no-sudo fallback (and the only
// one we are willing to CREATE).
var cliInstallDirs = []string{
	"/usr/local/bin",
	"/opt/homebrew/bin",
	"~/.local/bin",
}

// expandHome resolves a leading ~/ against the current user's home.
func expandHome(p string) string {
	if !strings.HasPrefix(p, "~/") {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return p
	}
	return filepath.Join(home, strings.TrimPrefix(p, "~/"))
}

// dirWritable reports whether we can create entries in dir, by actually
// creating one. Stat-and-guess gets this wrong for group/ACL cases, and the
// whole point is to fail BEFORE offering the user a button that cannot work.
func dirWritable(dir string) bool {
	f, err := os.CreateTemp(dir, ".lola-write-probe-")
	if err != nil {
		return false
	}
	name := f.Name()
	f.Close()
	_ = os.Remove(name)
	return true
}

// onPATH reports whether dir is an entry of the current process PATH. Used only
// to TELL the user whether the install will be visible to their shell — the
// install itself does not depend on it.
func onPATH(dir string) bool {
	for _, p := range filepath.SplitList(os.Getenv("PATH")) {
		if p == dir {
			return true
		}
	}
	return false
}

// installCLI symlinks the bundled CLI into the first writable candidate
// directory and returns the created path.
//
// A SYMLINK, not a copy, so the in-app updater's bundle swap carries the CLI
// with it — a copied binary would silently go stale the first time the app
// updated itself, which is exactly the version skew this whole file exists to
// make visible.
//
// It is deliberately conservative about an existing entry: a symlink into a
// .app is one of ours (or an older bundle's) and is replaced, but anything else
// — a real binary the user installed, a symlink somewhere else — is left alone
// and reported. Overwriting a hand-installed CLI would be a silent downgrade of
// something we do not own.
func installCLI() (string, error) {
	src := bundledLolaPath()
	if src == "" {
		return "", errors.New("this build of Lola does not bundle the lola CLI; install it from the release archive instead")
	}
	var tried []string
	for _, raw := range cliInstallDirs {
		dir := expandHome(raw)
		if _, err := os.Stat(dir); err != nil {
			// Only the last candidate (~/.local/bin) is ours to create; a missing
			// /usr/local/bin is a machine that does not use it.
			if raw != "~/.local/bin" {
				tried = append(tried, dir+" (missing)")
				continue
			}
			if mkErr := os.MkdirAll(dir, 0o755); mkErr != nil {
				tried = append(tried, dir+" (cannot create)")
				continue
			}
		}
		if !dirWritable(dir) {
			tried = append(tried, dir+" (not writable)")
			continue
		}
		dst := filepath.Join(dir, "lola")
		if err := replaceCLILink(dst, src); err != nil {
			tried = append(tried, dir+" ("+err.Error()+")")
			continue
		}
		return dst, nil
	}
	return "", errors.New("could not install the CLI: " + strings.Join(tried, "; "))
}

// replaceCLILink creates dst → src, replacing only a link we are willing to own.
func replaceCLILink(dst, src string) error {
	if fi, err := os.Lstat(dst); err == nil {
		if fi.Mode()&os.ModeSymlink == 0 {
			return errors.New("a real lola binary is already installed there")
		}
		target, rerr := os.Readlink(dst)
		if rerr != nil || !strings.Contains(target, ".app/Contents/") {
			return errors.New("an unrelated lola symlink is already installed there")
		}
		if rmErr := os.Remove(dst); rmErr != nil {
			return rmErr
		}
	}
	return os.Symlink(src, dst)
}
