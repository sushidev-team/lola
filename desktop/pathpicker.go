package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// PickFolder opens the native folder chooser. Cancellation returns an empty path.
func (s *ConfigService) PickFolder(start string) (string, error) {
	return pickLocalPath(start, true)
}

// PickFile opens the native file chooser. It returns a path, never file contents.
// Cancellation returns an empty path.
func (s *ConfigService) PickFile(start string) (string, error) {
	return pickLocalPath(start, false)
}

func pickLocalPath(start string, folder bool) (string, error) {
	app := application.Get()
	if app == nil {
		return "", errors.New("no application window to attach a dialog to")
	}
	title, button := "Choose a file", "Use this file"
	if folder {
		title, button = "Choose a folder", "Use this folder"
	}
	d := app.Dialog.OpenFile().
		CanChooseDirectories(folder).
		CanChooseFiles(!folder).
		CanCreateDirectories(folder).
		ShowHiddenFiles(!folder).
		ResolvesAliases(true).
		SetTitle(title).
		SetButtonText(button)
	if directory := pickerStartDirectory(start); directory != "" {
		d.SetDirectory(directory)
	}
	if win := app.Window.Current(); win != nil {
		d.AttachToWindow(win)
	}
	return d.PromptForSingleSelection()
}

// Start beside a file, or at the nearest existing parent of a partially typed
// path. Relative executable names should not resolve against the app's cwd.
func pickerStartDirectory(start string) string {
	start = strings.TrimSpace(start)
	if start == "~" || strings.HasPrefix(start, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		start = filepath.Join(home, strings.TrimPrefix(strings.TrimPrefix(start, "~"), "/"))
	}
	if !filepath.IsAbs(start) {
		return ""
	}
	for {
		if info, err := os.Stat(start); err == nil && info.IsDir() {
			return start
		}
		parent := filepath.Dir(start)
		if parent == start {
			return ""
		}
		start = parent
	}
}
