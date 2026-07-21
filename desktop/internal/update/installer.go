package update

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const (
	mountPoint = "/tmp/lola-update-mount"
	stagingDir = "/tmp/lola-update-staging"
	scriptPath = "/tmp/lola-update.sh"
)

// InstallUpdate mounts the downloaded DMG, stages the new .app with ditto (which
// preserves code signing and xattrs), writes a detached shell script that waits
// for THIS process to exit before swapping the bundle, and launches it. The
// caller must quit the app right after this returns nil so the script can
// replace the old bundle and relaunch.
//
// The swap runs from an external script, not in-process, because a program
// cannot reliably overwrite its own running bundle — the script waits on our PID
// first.
func InstallUpdate(dmgPath string) error {
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

	// 2. Clean any previous mount/staging leftovers.
	_ = exec.Command("hdiutil", "detach", mountPoint, "-quiet", "-force").Run()
	_ = os.RemoveAll(mountPoint)
	_ = os.RemoveAll(stagingDir)

	// 3. Mount the DMG.
	if err := os.MkdirAll(mountPoint, 0o755); err != nil {
		return fmt.Errorf("failed to create mount point: %w", err)
	}
	if out, err := exec.Command("hdiutil", "attach", dmgPath,
		"-nobrowse", "-noverify", "-mountpoint", mountPoint).CombinedOutput(); err != nil {
		return fmt.Errorf("failed to mount DMG: %w\n%s", err, string(out))
	}

	// 4. Find the .app inside the mounted volume.
	entries, err := os.ReadDir(mountPoint)
	if err != nil {
		return fmt.Errorf("failed to read mounted DMG: %w", err)
	}
	var newAppName string
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".app") {
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
	stagedApp := filepath.Join(stagingDir, filepath.Base(appPath))
	if out, err := exec.Command("ditto", newAppPath, stagedApp).CombinedOutput(); err != nil {
		return fmt.Errorf("failed to copy app to staging: %w\n%s", err, string(out))
	}

	// 6. Write the updater script. It waits for our PID, replaces the bundle,
	//    unmounts, cleans up, and relaunches. %q quoting guards paths with spaces
	//    (e.g. /Applications/lola.app is fine, but a renamed bundle may contain
	//    them).
	pid := os.Getpid()
	script := fmt.Sprintf(`#!/bin/bash
# Wait for the app to exit.
while kill -0 %d 2>/dev/null; do
    sleep 0.2
done

# Replace the old bundle with the staged one.
rm -rf %q
mv %q %q

# Unmount and clean up.
hdiutil detach %q -quiet -force 2>/dev/null
rm -rf %q
rm -f %q

# Relaunch.
open %q
`, pid, appPath, stagedApp, appPath, mountPoint, stagingDir, scriptPath, appPath)

	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		return fmt.Errorf("failed to write updater script: %w", err)
	}

	// 7. Launch the script fully detached so it outlives this process.
	cmd := exec.Command("/bin/bash", scriptPath)
	cmd.Stdout, cmd.Stderr, cmd.Stdin = nil, nil, nil
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start updater script: %w", err)
	}
	return nil
}
