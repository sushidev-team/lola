package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var pngHead = []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR")

func imageWorktree(t *testing.T) string {
	t.Helper()
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("LOLA_HOME", home)
	wt := filepath.Join(home, "worktrees", "p", "s")
	if err := os.MkdirAll(wt, 0o700); err != nil {
		t.Fatal(err)
	}
	return wt
}

func TestAttachImageFileCopiesIntoTheSession(t *testing.T) {
	wt := imageWorktree(t)
	src := filepath.Join(t.TempDir(), "CleanShot 2026.PNG")
	if err := os.WriteFile(src, pngHead, 0o600); err != nil {
		t.Fatal(err)
	}
	thumb := imageThumb
	t.Cleanup(func() { imageThumb = thumb })
	imageThumb = func(string) string { return "data:x" }
	got, err := (&TalkService{}).AttachImageFile(wt, src)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(got.Path) != filepath.Join(wt, ".lola", "images") || strings.Contains(got.Path, " ") || got.Thumb != "data:x" {
		t.Errorf("got %+v", got)
	}
}

func TestAttachImageFileRefuses(t *testing.T) {
	wt := imageWorktree(t)
	dir := t.TempDir()
	text := filepath.Join(dir, "secrets.png")
	_ = os.WriteFile(text, []byte("not an image"), 0o600)
	wrong := filepath.Join(dir, "a.txt")
	_ = os.WriteFile(wrong, pngHead, 0o600)
	for _, src := range []string{text, wrong, filepath.Join(dir, "missing.png")} {
		if _, err := (&TalkService{}).AttachImageFile(wt, src); err == nil {
			t.Errorf("%s was taken", src)
		}
	}
}

func TestImagesDirStaysUnderHomeAndFollowsNoLink(t *testing.T) {
	wt := imageWorktree(t)
	if _, err := imagesDir(t.TempDir()); err == nil {
		t.Error("a folder outside lola's home was taken")
	}
	if _, err := imagesDir(wt + "/../../../../etc"); err == nil {
		t.Error("a path climbing out was taken")
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(wt, ".lola")); err != nil {
		t.Fatal(err)
	}
	if _, err := imagesDir(wt); err == nil {
		t.Error("a linked .lola was followed")
	}
}

func TestAttachClipboardImage(t *testing.T) {
	wt := imageWorktree(t)
	png, file, thumb := clipboardPNG, clipboardFile, imageThumb
	t.Cleanup(func() { clipboardPNG, clipboardFile, imageThumb = png, file, thumb })
	imageThumb = func(string) string { return "" }

	clipboardPNG = func(_ context.Context, dest string) error { return os.WriteFile(dest, pngHead, 0o600) }
	if got, err := (&TalkService{}).AttachClipboardImage(wt); err != nil || filepath.Ext(got.Path) != ".png" {
		t.Errorf("image data: %q %v", got, err)
	}

	// osascript opens the file before it learns the clipboard holds no image.
	clipboardPNG = func(_ context.Context, dest string) error {
		_ = os.WriteFile(dest, nil, 0o600)
		return errors.New("no image")
	}
	clipboardFile = func(context.Context) (string, error) { return "", errors.New("no file") }
	if _, err := (&TalkService{}).AttachClipboardImage(wt); err == nil {
		t.Error("an empty clipboard answered a path")
	}
	left, _ := os.ReadDir(filepath.Join(wt, ".lola", "images"))
	if len(left) != 1 {
		t.Errorf("a failed read left a file: %d entries", len(left))
	}

	src := filepath.Join(t.TempDir(), "copied.png")
	_ = os.WriteFile(src, pngHead, 0o600)
	clipboardFile = func(context.Context) (string, error) { return src, nil }
	if _, err := (&TalkService{}).AttachClipboardImage(wt); err != nil {
		t.Errorf("a copied file: %v", err)
	}
}
