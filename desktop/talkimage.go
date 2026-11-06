package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/sushidev-team/lola/internal/config"
)

// An image a person gives an agent goes in as a file the agent can read, because an agent's composer
// takes text from lola. It lands in the session's .lola/images: inside the worktree, so reading it
// asks no permission and a sandbox mounts it, and under .lola/, which git excludes, checkpoints skip
// and teardown takes with the worktree.
//
// This runs on a person's paste, in the app and not the daemon, because an image can be larger than
// a socket line. An agent can write to .lola/, so nothing here follows a link or overwrites a file.

const (
	maxImageBytes  = 20 << 20
	maxThumbBytes  = 256 << 10
	clipboardWait  = 5 * time.Second
	clipboardPNGAS = `on run argv
	set f to open for access POSIX file (item 1 of argv) with write permission
	try
		write (the clipboard as «class PNGf») to f
		close access f
	on error e
		close access f
		error e
	end try
end run`
	clipboardFileAS = `POSIX path of (the clipboard as «class furl»)`
)

// ImageAttachment is an image a person attached: the path the agent is given, and a thumbnail as a
// data URL for the composer's tile, "" when one could not be made.
type ImageAttachment struct {
	Path  string `json:"path"`
	Thumb string `json:"thumb"`
}

var imageExts = map[string]bool{".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".webp": true}

// The clipboard, as seams. Reading it is what these do, and no key or pointer is involved.
var (
	clipboardPNG = func(ctx context.Context, dest string) error {
		return exec.CommandContext(ctx, "osascript", "-e", clipboardPNGAS, dest).Run()
	}
	clipboardFile = func(ctx context.Context) (string, error) {
		out, err := exec.CommandContext(ctx, "osascript", "-e", clipboardFileAS).Output()
		return strings.TrimSpace(string(out)), err
	}
)

// imageThumb makes the tile's picture with the system's own tool. A tile without one still works.
var imageThumb = func(src string) string {
	dir, err := os.MkdirTemp("", "lola-thumb-")
	if err != nil {
		return ""
	}
	defer os.RemoveAll(dir)
	out := filepath.Join(dir, "t.jpg")
	ctx, cancel := context.WithTimeout(context.Background(), clipboardWait)
	defer cancel()
	if exec.CommandContext(ctx, "sips", "-Z", "144", "-s", "format", "jpeg", src, "--out", out).Run() != nil {
		return ""
	}
	data, err := os.ReadFile(out)
	if err != nil || len(data) == 0 || len(data) > maxThumbBytes {
		return ""
	}
	return "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(data)
}

func attached(path string) ImageAttachment {
	return ImageAttachment{Path: path, Thumb: imageThumb(path)}
}

// ReadAttachmentImage reads an original attachment for the message gallery. The read stays
// inside this session's image folder, even if a worker has replaced a file with a symlink.
func (s *TalkService) ReadAttachmentImage(worktree, path string) (string, error) {
	home, err := config.Home()
	if err != nil {
		return "", err
	}
	wt := filepath.Clean(worktree)
	base, err := filepath.Abs(home)
	if err != nil {
		return "", err
	}
	dir := filepath.Join(wt, ".lola", "images")
	if !filepath.IsAbs(worktree) || !strings.HasPrefix(wt, base+string(filepath.Separator)) || filepath.Dir(filepath.Clean(path)) != dir {
		return "", errors.New("this is not an attachment in this session")
	}
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil || resolved != dir {
		return "", errors.New("this session's image folder is unavailable")
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return "", err
	}
	defer root.Close()
	f, err := root.Open(filepath.Base(path))
	if err != nil {
		return "", errors.New("this image is no longer available")
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxImageBytes {
		return "", errors.New("could not read this image")
	}
	data, err := io.ReadAll(io.LimitReader(f, maxImageBytes+1))
	if err != nil || len(data) > maxImageBytes {
		return "", errors.New("could not read this image")
	}
	mime := http.DetectContentType(data)
	if mime != "image/png" && mime != "image/jpeg" && mime != "image/gif" && mime != "image/webp" {
		return "", errors.New("this attachment is not an image")
	}
	return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data), nil
}

// imagesDir finds or makes a session's image folder. worktree comes from the client, so it must lie
// under lola's home, and no step of the way may be a link.
func imagesDir(worktree string) (string, error) {
	if worktree == "" || !filepath.IsAbs(worktree) {
		return "", errors.New("this session has no folder to put an image in")
	}
	base, err := config.Home()
	if err != nil {
		return "", err
	}
	home, err := filepath.Abs(base)
	if err != nil {
		return "", err
	}
	clean := filepath.Clean(worktree)
	if !strings.HasPrefix(clean, home+string(filepath.Separator)) {
		return "", errors.New("this session has no folder to put an image in")
	}
	dir := clean
	if info, err := os.Lstat(dir); err != nil || !info.IsDir() {
		return "", errors.New("this session's folder is gone")
	}
	for _, part := range []string{".lola", "images"} {
		dir = filepath.Join(dir, part)
		if err := os.Mkdir(dir, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
			return "", err
		}
		info, err := os.Lstat(dir)
		if err != nil {
			return "", err
		}
		if !info.Mode().IsDir() {
			return "", errors.New("refusing to write an image through a link in this session's folder")
		}
	}
	return dir, nil
}

func newImageName(ext string) (string, error) {
	var r [8]byte
	if _, err := rand.Read(r[:]); err != nil {
		return "", err
	}
	return "paste-" + hex.EncodeToString(r[:]) + ext, nil
}

// copyImage copies a file a person pointed at, once it has shown it is an image of a size to carry.
func copyImage(src, dir string) (string, error) {
	ext := strings.ToLower(filepath.Ext(src))
	if !imageExts[ext] {
		return "", errors.New("that is not a png, jpeg, gif or webp file")
	}
	in, err := os.Open(src)
	if err != nil {
		return "", errors.New("could not read that image")
	}
	defer in.Close()
	info, err := in.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return "", errors.New("could not read that image")
	}
	if info.Size() > maxImageBytes {
		return "", errors.New("that image is over 20 MB")
	}
	head := make([]byte, 512)
	n, _ := io.ReadFull(in, head)
	if !strings.HasPrefix(http.DetectContentType(head[:n]), "image/") {
		return "", errors.New("that file is not an image")
	}
	if _, err := in.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	name, err := newImageName(ext)
	if err != nil {
		return "", err
	}
	dest := filepath.Join(dir, name)
	out, err := os.OpenFile(dest, os.O_CREATE|os.O_EXCL|os.O_WRONLY|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return "", err
	}
	_, err = io.Copy(out, io.LimitReader(in, maxImageBytes))
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(dest)
		return "", err
	}
	return dest, nil
}

// AttachImageFile copies an image file into a session's folder and answers the copy's path.
func (s *TalkService) AttachImageFile(worktree, src string) (ImageAttachment, error) {
	dir, err := imagesDir(worktree)
	if err != nil {
		return ImageAttachment{}, err
	}
	dest, err := copyImage(src, dir)
	if err != nil {
		return ImageAttachment{}, err
	}
	return attached(dest), nil
}

// AttachClipboardImage saves the image on the clipboard into a session's folder and answers its
// path: image data first, then a copied file. Nothing else on the clipboard is read or kept.
func (s *TalkService) AttachClipboardImage(worktree string) (ImageAttachment, error) {
	dir, err := imagesDir(worktree)
	if err != nil {
		return ImageAttachment{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), clipboardWait)
	defer cancel()
	name, err := newImageName(".png")
	if err != nil {
		return ImageAttachment{}, err
	}
	dest := filepath.Join(dir, name)
	if err := clipboardPNG(ctx, dest); err == nil {
		if info, serr := os.Lstat(dest); serr == nil && info.Mode().IsRegular() && info.Size() > 0 {
			return attached(dest), nil
		}
	}
	_ = os.Remove(dest)
	if src, err := clipboardFile(ctx); err == nil && src != "" {
		copied, err := copyImage(src, dir)
		if err != nil {
			return ImageAttachment{}, err
		}
		return attached(copied), nil
	}
	return ImageAttachment{}, errors.New("there is no image on the clipboard")
}
