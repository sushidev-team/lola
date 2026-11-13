package update

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

var installMu sync.Mutex

var ErrInstallerBusy = errors.New("Installer is busy. Eject any Lola installer in Finder or Disk Utility, then retry.")

type installer struct {
	run   func(string, ...string) ([]byte, error)
	start func(string) error
}

// InstallUpdate mounts the downloaded DMG, stages the new .app with ditto (which
// preserves code signing and xattrs), writes a detached shell script that waits
// for THIS process to exit before swapping the bundle, and launches it. The
// caller must quit the app right after this returns nil so the script can
// replace the old bundle and relaunch.
//
// The swap runs from an external script, not in-process, because a program
// cannot reliably overwrite its own running bundle — the script waits on our PID
// first.
func InstallUpdate(dmgPath string) (err error) {
	if !installMu.TryLock() {
		return errors.New("An installation is already in progress.")
	}
	defer func() {
		// After handing off, keep the lock until the caller quits the app.
		if err != nil {
			installMu.Unlock()
		}
	}()
	// 1. Resolve the running .app bundle by walking up from the executable.
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to find executable path: %w", err)
	}
	// exe looks like /Applications/lola.app/Contents/MacOS/lola-desktop.
	appPath := exe
	for !strings.HasSuffix(appPath, ".app") {
		parent := filepath.Dir(appPath)
		if parent == appPath {
			return fmt.Errorf("could not determine .app bundle from executable path: %s", exe)
		}
		appPath = parent
	}

	return (installer{
		run: func(name string, args ...string) ([]byte, error) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			defer cancel()
			cmd := exec.CommandContext(ctx, name, args...)
			cmd.Env = append(os.Environ(), "LC_ALL=C")
			return cmd.CombinedOutput()
		},
		start: func(path string) error {
			cmd := exec.Command("/bin/bash", path)
			if err := cmd.Start(); err != nil {
				return err
			}
			go func() { _ = cmd.Wait() }()
			return nil
		},
	}).install(dmgPath, appPath, os.TempDir())
}

func (i installer) install(dmgPath, appPath, tempRoot string) error {
	// Every attempt owns its paths. Never detach another attempt's image or
	// recursively delete a mount point: failed detach may leave it mounted.
	workspace, err := os.MkdirTemp(tempRoot, "lola-update-")
	if err != nil {
		return fmt.Errorf("failed to prepare installer: %w", err)
	}
	mountPoint := filepath.Join(workspace, "mount")
	stagingDir := filepath.Join(workspace, "staging")
	scriptPath := filepath.Join(workspace, "install.sh")
	mountAttempted, handedOff := false, false
	defer func() {
		if handedOff {
			return
		}
		if mountAttempted {
			_, _ = i.run("hdiutil", "detach", mountPoint, "-quiet")
		}
		_ = os.Remove(mountPoint) // Empty directories only; never traverse a mount.
		_ = os.RemoveAll(stagingDir)
		_ = os.Remove(scriptPath)
		_ = os.Remove(workspace)
	}()

	// 3. Mount the DMG.
	if err := os.MkdirAll(mountPoint, 0o755); err != nil {
		return fmt.Errorf("failed to create mount point: %w", err)
	}
	mountAttempted = true // Even a failed attach can leave a partial mount.
	if out, err := i.run("hdiutil", "attach", dmgPath,
		"-nobrowse", "-readonly", "-mountpoint", mountPoint); err != nil {
		message := strings.ToLower(string(out))
		if strings.Contains(message, "resource busy") || strings.Contains(message, "resource is busy") || strings.Contains(message, "resource ist belegt") {
			return ErrInstallerBusy
		}
		return fmt.Errorf("failed to mount DMG: %w\n%s", err, string(out))
	}

	// 4. Find the .app inside the mounted volume.
	entries, err := os.ReadDir(mountPoint)
	if err != nil {
		return fmt.Errorf("failed to read mounted DMG: %w", err)
	}
	var newAppName string
	for _, entry := range entries {
		if entry.IsDir() && strings.HasSuffix(entry.Name(), ".app") {
			newAppName = entry.Name()
			break
		}
	}
	if newAppName == "" {
		return fmt.Errorf("no .app bundle found in DMG")
	}
	newAppPath := filepath.Join(mountPoint, newAppName)

	// 5. Stage a copy with ditto (keeps signature + extended attributes intact).
	if err := os.MkdirAll(stagingDir, 0o755); err != nil {
		return fmt.Errorf("failed to create staging directory: %w", err)
	}
	// Staged and installed under the DMG's OWN bundle name, not the running
	// bundle's. macOS labels the Dock / Finder / Cmd-Tab entry from the bundle
	// DIRECTORY name (it honours CFBundleDisplayName only when that matches), so
	// staging under filepath.Base(appPath) meant a release that renamed the app
	// could never reach an existing install: the new contents landed back inside
	// the old directory name and the user kept seeing the old label forever, with
	// no migration path. Installing to destPath and removing the old directory
	// afterwards is what lets a rename actually propagate.
	stagedApp := filepath.Join(stagingDir, newAppName)
	destPath := filepath.Join(filepath.Dir(appPath), newAppName)
	if out, err := i.run("ditto", newAppPath, stagedApp); err != nil {
		return fmt.Errorf("failed to copy app to staging: %w\n%s", err, string(out))
	}
	// The detached script only needs the staged copy. Release the image now,
	// so a failed swap cannot strand a mounted volume after the app quits.
	if _, err := i.run("hdiutil", "detach", mountPoint, "-quiet"); err != nil {
		return errors.New("Could not eject the installer. Eject it in Disk Utility, then try again.")
	}
	mountAttempted = false
	_ = os.Remove(mountPoint)

	pid := os.Getpid()
	script := updaterScript(pid, destPath, stagedApp, appPath, workspace)

	if err := os.WriteFile(scriptPath, []byte(script), 0o700); err != nil {
		return fmt.Errorf("failed to write updater script: %w", err)
	}

	// 7. Launch the script fully detached so it outlives this process.
	if err := i.start(scriptPath); err != nil {
		return fmt.Errorf("failed to start updater script: %w", err)
	}
	handedOff = true
	return nil
}

// updaterScript builds the detached swap script. Extracted from InstallUpdate so
// the decision it encodes is unit-testable: InstallUpdate itself mounts a DMG
// and rewrites /Applications, which no test can exercise.
//
// The old bundle is removed ONLY on a genuine rename, and the rename test lives
// HERE in Go rather than as a `[ "$a" != "$b" ]` in the shell. EqualFold, not
// !=, because macOS volumes are case-insensitive by default: an install at
// /Applications/lola.app updating to a DMG shipping Lola.app is the SAME
// directory. A byte comparison calls that a rename, so the script would install
// to Lola.app and then `rm -rf` lola.app — deleting the bundle it had just
// installed and leaving the user with nothing to launch. (The doc comment on
// InstallUpdate still cites /Applications/lola.app, i.e. that colliding name is
// expected in the wild.)
//
// The removal is further guarded on `mv` succeeding and on the destination
// actually existing. Without both, a failed `mv` (disk full, staging reaped out
// of /tmp, quarantine) still ran the delete and took the working install with
// it: before this path existed there was one delete, and it was unreachable
// after a failure; now there are two, and the second was not.
func updaterScript(pid int, destPath, stagedApp, appPath, workspace string) string {
	removeOld := ""
	if !strings.EqualFold(destPath, appPath) {
		removeOld = fmt.Sprintf(`
# The bundle was renamed: drop the old directory now that the new one is in
# place, so Launch Services stops offering the stale copy.
if [ -d %[1]s ]; then
    rm -rf %[2]s
fi
`, shellQuote(destPath), shellQuote(appPath))
	}
	return fmt.Sprintf(`#!/bin/bash
# Wait for the app to exit.
while kill -0 %d 2>/dev/null; do
    sleep 0.2
done

# Replace the old bundle with the staged one.
rm -rf %[2]s
mv %[3]s %[2]s || exit 1
%[4]s
# Clean up this attempt's private staging directory and script.
rm -rf %[5]s

# Relaunch.
open %[2]s
`, pid, shellQuote(destPath), shellQuote(stagedApp), removeOld, shellQuote(workspace))
}

func shellQuote(path string) string { return "'" + strings.ReplaceAll(path, "'", "'\\''") + "'" }
