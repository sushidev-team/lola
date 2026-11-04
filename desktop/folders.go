package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// OpenFolder opens an existing directory in Finder on the desktop machine.
// This is local UI work, so it does not depend on the daemon's version.
func (s *DaemonService) OpenFolder(path string) error {
	return openFolder(path, func(dir string) error {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return exec.CommandContext(ctx, "/usr/bin/open", "-a", "Finder", dir).Run()
	})
}

func openFolder(path string, open func(string) error) error {
	if !filepath.IsAbs(path) {
		return errors.New("folder path must be absolute")
	}
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("open folder: %w", err)
	}
	if !info.IsDir() {
		return errors.New("folder path is not a directory")
	}
	if err := open(path); err != nil {
		return fmt.Errorf("open folder in Finder: %w", err)
	}
	return nil
}
