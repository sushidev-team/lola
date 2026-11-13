package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeBundle builds a Lola.app skeleton under t.TempDir and points
// executablePath at its Contents/MacOS/Lola. withCLI decides whether the bundled
// CLI is present, so both a current bundle and one predating it are testable.
func fakeBundle(t *testing.T, withCLI bool) (appDir string) {
	t.Helper()
	appDir = filepath.Join(t.TempDir(), "Lola.app")
	macos := filepath.Join(appDir, "Contents", "MacOS")
	if err := os.MkdirAll(macos, 0o755); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(macos, "Lola")
	if err := os.WriteFile(exe, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if withCLI {
		bin := filepath.Join(appDir, "Contents", "Resources", "bin")
		if err := os.MkdirAll(bin, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(bin, "lola"), []byte("#!/bin/sh\necho lola\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	orig := executablePath
	executablePath = func() (string, error) { return exe, nil }
	t.Cleanup(func() { executablePath = orig })
	return appDir
}

// isolatePATH empties PATH and LOLA_BIN so a resolution test cannot be decided
// by the developer's own machine — the one thing that would make this whole
// suite pass everywhere except on a clean install.
func isolatePATH(t *testing.T) {
	t.Helper()
	t.Setenv("PATH", filepath.Join(t.TempDir(), "empty"))
	t.Setenv("LOLA_BIN", "")
}

func TestBundledLolaPathFindsTheShippedCLI(t *testing.T) {
	app := fakeBundle(t, true)
	got := bundledLolaPath()
	want := filepath.Join(app, "Contents", "Resources", "bin", "lola")
	if got != want {
		t.Fatalf("bundledLolaPath() = %q, want %q", got, want)
	}
}

// A bundle from before the CLI was packaged must report nothing rather than a
// path that does not exist — the caller hands the result straight to exec.
func TestBundledLolaPathEmptyWithoutOne(t *testing.T) {
	fakeBundle(t, false)
	if got := bundledLolaPath(); got != "" {
		t.Fatalf("bundledLolaPath() = %q, want empty", got)
	}
}

// A leftover that is not executable must not be offered to exec either.
func TestBundledLolaPathIgnoresNonExecutable(t *testing.T) {
	app := fakeBundle(t, true)
	p := filepath.Join(app, "Contents", "Resources", "bin", "lola")
	if err := os.Chmod(p, 0o644); err != nil {
		t.Fatal(err)
	}
	if got := bundledLolaPath(); got != "" {
		t.Fatalf("bundledLolaPath() = %q, want empty for a non-executable file", got)
	}
}

// The whole point of the change: a DMG-only install (nothing on PATH) still
// resolves a CLI, so the first-run wizard can start a daemon.
func TestResolveLolaFallsBackToTheBundle(t *testing.T) {
	isolatePATH(t)
	app := fakeBundle(t, true)
	b, err := resolveLola()
	if err != nil {
		t.Fatalf("resolveLola() error = %v", err)
	}
	if b.Source != srcBundled {
		t.Errorf("source = %q, want %q", b.Source, srcBundled)
	}
	if want := filepath.Join(app, "Contents", "Resources", "bin", "lola"); b.Path != want {
		t.Errorf("path = %q, want %q", b.Path, want)
	}
}

// PATH stays AHEAD of the bundle: a developer's own build is the documented dev
// loop, and preferring the shipped copy would make `go install` a no-op.
func TestResolveLolaPrefersPATHOverTheBundle(t *testing.T) {
	fakeBundle(t, true)
	dir := t.TempDir()
	own := filepath.Join(dir, "lola")
	if err := os.WriteFile(own, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LOLA_BIN", "")
	t.Setenv("PATH", dir)

	b, err := resolveLola()
	if err != nil {
		t.Fatalf("resolveLola() error = %v", err)
	}
	if b.Source != srcPath || b.Path != own {
		t.Fatalf("resolveLola() = %+v, want the PATH binary %q", b, own)
	}
}

// An explicit override beats everything, and is taken on trust: naming a binary
// is a decision, and silently falling through to a different one would be worse
// than reporting that the named one is wrong.
func TestResolveLolaHonoursLolaBin(t *testing.T) {
	fakeBundle(t, true)
	t.Setenv("LOLA_BIN", "/somewhere/else/lola")
	b, err := resolveLola()
	if err != nil {
		t.Fatalf("resolveLola() error = %v", err)
	}
	if b.Source != srcEnv || b.Path != "/somewhere/else/lola" {
		t.Fatalf("resolveLola() = %+v, want the LOLA_BIN override", b)
	}
}

// With nothing anywhere, the error must name the FIX. The old text described the
// lookup that failed ("not found on PATH") and left a fresh install with nothing
// to act on — the bug this file exists to close.
func TestResolveLolaErrorNamesTheFix(t *testing.T) {
	isolatePATH(t)
	fakeBundle(t, false)
	_, err := resolveLola()
	if err == nil {
		t.Fatal("expected an error with no CLI anywhere")
	}
	for _, want := range []string{"DMG", "LOLA_BIN"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q must mention %q", err, want)
		}
	}
}

func TestInstallCLISymlinksIntoAWritableDir(t *testing.T) {
	app := fakeBundle(t, true)
	dir := t.TempDir()
	orig := cliInstallDirs
	cliInstallDirs = []string{dir}
	t.Cleanup(func() { cliInstallDirs = orig })

	got, err := installCLI()
	if err != nil {
		t.Fatalf("installCLI() error = %v", err)
	}
	if want := filepath.Join(dir, "lola"); got != want {
		t.Fatalf("installCLI() = %q, want %q", got, want)
	}
	// A SYMLINK, not a copy: the in-app updater swaps the whole bundle, and a
	// copied binary would silently go stale on the first self-update.
	target, err := os.Readlink(got)
	if err != nil {
		t.Fatalf("installed entry must be a symlink: %v", err)
	}
	if want := filepath.Join(app, "Contents", "Resources", "bin", "lola"); target != want {
		t.Errorf("symlink target = %q, want %q", target, want)
	}
}

// A read-only candidate is skipped rather than failing the whole install — the
// common macOS case is a root-owned /usr/local/bin with ~/.local/bin behind it.
func TestInstallCLISkipsUnwritableDirs(t *testing.T) {
	fakeBundle(t, true)
	locked, open := t.TempDir(), t.TempDir()
	if err := os.Chmod(locked, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o700) })
	orig := cliInstallDirs
	cliInstallDirs = []string{locked, open}
	t.Cleanup(func() { cliInstallDirs = orig })

	got, err := installCLI()
	if err != nil {
		t.Fatalf("installCLI() error = %v", err)
	}
	if want := filepath.Join(open, "lola"); got != want {
		t.Fatalf("installCLI() = %q, want the writable dir %q", got, want)
	}
}

// A CLI the user installed by hand is NOT ours to replace: overwriting it would
// be a silent downgrade of something this app does not own.
func TestInstallCLIRefusesToClobberARealBinary(t *testing.T) {
	fakeBundle(t, true)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "lola"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	orig := cliInstallDirs
	cliInstallDirs = []string{dir}
	t.Cleanup(func() { cliInstallDirs = orig })

	if _, err := installCLI(); err == nil {
		t.Fatal("installCLI() must refuse to replace a real binary")
	}
}

// Re-running the install (a second app version, a repeated click) must succeed,
// replacing the link we own rather than reporting a conflict with ourselves.
func TestInstallCLIReplacesItsOwnLink(t *testing.T) {
	fakeBundle(t, true)
	dir := t.TempDir()
	orig := cliInstallDirs
	cliInstallDirs = []string{dir}
	t.Cleanup(func() { cliInstallDirs = orig })

	if _, err := installCLI(); err != nil {
		t.Fatalf("first installCLI() error = %v", err)
	}
	if _, err := installCLI(); err != nil {
		t.Fatalf("second installCLI() error = %v", err)
	}
}

// Nothing to install from is a clear message, not a dangling symlink.
func TestInstallCLIWithoutABundledCopy(t *testing.T) {
	fakeBundle(t, false)
	if _, err := installCLI(); err == nil {
		t.Fatal("installCLI() must fail when the app bundles no CLI")
	}
}

// The bundled CLI's location is a CONTRACT between two files that cannot see
// each other: build/darwin/Taskfile.yml writes it, and bundledRelPath reads it.
// Changing either alone breaks the fallback SILENTLY — the app simply goes back
// to "no CLI found" on a machine with nothing on PATH, which is the exact bug
// this whole path exists to fix. So assert the Taskfile really copies to the
// place the Go side looks.
func TestBundledPathMatchesThePackagingTask(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("build", "darwin", "Taskfile.yml"))
	if err != nil {
		t.Fatalf("read packaging Taskfile: %v", err)
	}
	// bundledRelPath is relative to Contents/MacOS; the Taskfile writes an
	// app-relative path, so compare the tail both agree on.
	rel := filepath.ToSlash(filepath.Clean(filepath.Join("Contents", "MacOS", bundledRelPath)))
	if !strings.Contains(string(raw), rel) {
		t.Fatalf("build/darwin/Taskfile.yml must copy the CLI to %q (from bundledRelPath %q)", rel, bundledRelPath)
	}
}
