package update

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// Opt-in: mounts a throwaway DMG, but never launches the swap script or touches
// an installed application. Exercises real DiskImages and ditto behavior.
func TestInstallerRealDMG(t *testing.T) {
	if runtime.GOOS != "darwin" || os.Getenv("LOLA_UPDATE_IT") != "1" {
		t.Skip("set LOLA_UPDATE_IT=1 on macOS")
	}
	root := t.TempDir()
	source := filepath.Join(root, "source")
	contents := filepath.Join(source, "Lola.app", "Contents")
	if err := os.MkdirAll(contents, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(contents, "fixture"), []byte("updater test"), 0o600); err != nil {
		t.Fatal(err)
	}
	run := func(name string, args ...string) ([]byte, error) {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		return exec.CommandContext(ctx, name, args...).CombinedOutput()
	}
	dmg := filepath.Join(root, "test.dmg")
	if out, err := run("hdiutil", "create", "-srcfolder", source, "-volname", "Lola updater test", "-format", "UDZO", dmg); err != nil {
		t.Fatalf("create: %v: %s", err, out)
	}
	started := false
	i := installer{run: run, start: func(script string) error {
		workspace := filepath.Dir(script)
		if _, err := os.Stat(filepath.Join(workspace, "mount")); !os.IsNotExist(err) {
			t.Fatal("mount was not released before handoff", err)
		}
		data, err := os.ReadFile(filepath.Join(workspace, "staging", "Lola.app", "Contents", "fixture"))
		if err != nil || string(data) != "updater test" {
			t.Fatal("ditto did not stage the app", err)
		}
		started = true
		return nil
	}}
	if err := i.install(dmg, filepath.Join(root, "Applications", "Lola.app"), root); err != nil {
		t.Fatal(err)
	}
	if !started {
		t.Fatal("installer did not reach script handoff")
	}
}

func TestInstallAttemptCleanup(t *testing.T) {
	for _, failure := range []string{"busy", "localized-busy", "missing-app", "copy", "detach", "start", ""} {
		t.Run(failure, func(t *testing.T) {
			root := t.TempDir()
			var mount, script string
			var detached, started bool
			i := installer{
				run: func(name string, args ...string) ([]byte, error) {
					switch {
					case name == "hdiutil" && args[0] == "attach":
						mount = args[len(args)-1]
						if failure == "busy" || failure == "localized-busy" {
							message := "hdiutil: attach failed - Resource busy"
							if failure == "localized-busy" {
								message = "hdiutil: WARNING: deprecated\nhdiutil: attach failed - Resource ist belegt"
							}
							return []byte(message), errors.New("exit status 1")
						}
						if failure != "missing-app" {
							return nil, os.Mkdir(filepath.Join(mount, "Lola.app"), 0o755)
						}
					case name == "ditto":
						if err := os.MkdirAll(args[1], 0o755); err != nil {
							t.Fatal(err)
						}
						if failure == "copy" {
							return []byte("disk full"), errors.New("copy failed")
						}
					case name == "hdiutil" && args[0] == "detach":
						if args[1] != mount {
							t.Fatalf("detaching another attempt: %v", args)
						}
						if failure == "detach" {
							return nil, errors.New("busy")
						}
						detached = true
						// Simulate the mounted contents disappearing on detach.
						return nil, os.RemoveAll(filepath.Join(mount, "Lola.app"))
					default:
						t.Fatalf("unexpected command %s %v", name, args)
					}
					return nil, nil
				},
				start: func(path string) error {
					if !detached {
						t.Fatal("started swap while DMG was still mounted")
					}
					started, script = true, path
					if failure == "start" {
						return errors.New("start failed")
					}
					return nil
				},
			}
			err := i.install("/downloads/Lola.dmg", "/Applications/Lola.app", root)
			if (err != nil) != (failure != "") {
				t.Fatalf("failure=%q, err=%v", failure, err)
			}
			if strings.Contains(failure, "busy") && !errors.Is(err, ErrInstallerBusy) {
				t.Fatalf("want actionable busy error, got %v", err)
			}
			if started != (failure == "" || failure == "start") {
				t.Fatalf("unexpected script start: %v", started)
			}
			entries, err := os.ReadDir(root)
			if err != nil {
				t.Fatal(err)
			}
			switch failure {
			case "detach":
				if _, err := os.Stat(filepath.Join(mount, "Lola.app")); err != nil {
					t.Fatal("cleanup traversed a mount after failed detach", err)
				}
				if _, err := os.Stat(filepath.Join(filepath.Dir(mount), "staging")); !os.IsNotExist(err) {
					t.Fatal("failed staging was retained", err)
				}
			case "":
				data, err := os.ReadFile(script)
				if err != nil {
					t.Fatal(err)
				}
				if strings.Contains(string(data), "hdiutil") {
					t.Fatal("swap must not own a mounted image")
				}
				info, err := os.Stat(filepath.Dir(script))
				if err != nil || info.Mode().Perm() != 0o700 {
					t.Fatal("workspace must be private", err)
				}
			default:
				if len(entries) != 0 {
					t.Fatalf("failed attempt left files: %v", entries)
				}
			}
		})
	}
}

func TestInstallRetriesUseDifferentDirectories(t *testing.T) {
	root := t.TempDir()
	var mounts []string
	i := installer{run: func(name string, args ...string) ([]byte, error) {
		if args[0] == "attach" {
			mounts = append(mounts, args[len(args)-1])
			return []byte("Resource busy"), errors.New("busy")
		}
		return nil, nil
	}, start: func(string) error { t.Fatal("must not start"); return nil }}
	for range 2 {
		if err := i.install("/downloads/Lola.dmg", "/Applications/Lola.app", root); !errors.Is(err, ErrInstallerBusy) {
			t.Fatal(err)
		}
	}
	if mounts[0] == mounts[1] {
		t.Fatal("retry reused a mount directory")
	}
}

func TestUpdaterScriptHandlesLiteralPathsAndCleansOwnWorkspace(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "attempt with 'quotes' and $(false) `false`")
	staged := filepath.Join(workspace, "staging", "Lola.app")
	dest := filepath.Join(root, "Applications", "Lola.app")
	for _, dir := range []string{staged, dest, filepath.Join(root, "bin")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(staged, "new"), []byte("new app"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Avoid opening any actual app in this filesystem-only test.
	if err := os.WriteFile(filepath.Join(root, "bin", "open"), []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(workspace, "install.sh")
	if err := os.WriteFile(script, []byte(updaterScript(999999999, dest, staged, dest, workspace)), 0o700); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("/bin/bash", script)
	cmd.Env = append(os.Environ(), "PATH="+filepath.Join(root, "bin")+":/usr/bin:/bin")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if _, err := os.Stat(filepath.Join(dest, "new")); err != nil {
		t.Fatal("staged app was not installed", err)
	}
	if _, err := os.Stat(workspace); !os.IsNotExist(err) {
		t.Fatal("workspace was not cleaned", err)
	}
}
