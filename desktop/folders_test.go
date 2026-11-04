package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestOpenFolder(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "Session with spaces & symbols")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	var opened string
	if err := openFolder(dir, func(path string) error { opened = path; return nil }); err != nil {
		t.Fatal(err)
	}
	if opened != dir {
		t.Fatalf("opened %q, want %q", opened, dir)
	}
	failure := errors.New("Finder unavailable")
	if err := openFolder(dir, func(string) error { return failure }); !errors.Is(err, failure) {
		t.Fatalf("lost opener error: %v", err)
	}
}

func TestOpenFolderRejectsInvalidPaths(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"", "relative", file, file + "-missing"} {
		t.Run(path, func(t *testing.T) {
			err := openFolder(path, func(string) error { t.Fatal("opened invalid path"); return nil })
			if err == nil {
				t.Fatal("accepted invalid path")
			}
		})
	}
}
