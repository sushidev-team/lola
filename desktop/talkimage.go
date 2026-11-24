package main

import (
	"bytes"
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
// takes text from lola. It lands in .lola/images beneath the session's attachment root. Borrowed
// checkouts have a private root, and a container sees the same absolute path as the host.
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

// ReadAttachmentImage reads an original attachment for the message gallery. The read stays
// inside this session's image folder, even if a worker has replaced a file with a symlink.
func (s *TalkService) ReadAttachmentImage(worktree, path string) (string, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || filepath.Dir(path) != filepath.Join(worktree, ".lola", "images") {
		return "", errors.New("this is not an attachment in this session")
	}
	root, err := imagesDir(worktree, false)
	if err != nil {
		return "", errors.New("this session's image folder is unavailable")
	}
	defer root.Close()
	f, err := root.OpenFile(filepath.Base(path), os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
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

// imagesDir opens the session's .lola/images from Lola's home. Each directory
// handle pins its parent while we check and open its child.
func imagesDir(worktree string, create bool) (*os.Root, error) {
	base, err := config.Home()
	if err != nil {
		return nil, err
	}
	home, err := filepath.Abs(base)
	if err != nil {
		return nil, err
	}
	rel, err := filepath.Rel(home, worktree)
	if err != nil || !filepath.IsLocal(rel) || rel == "." {
		return nil, errors.New("this session has no folder to put an image in")
	}
	root, err := os.OpenRoot(home)
	if err != nil {
		return nil, err
	}
	parts := strings.Split(rel, string(filepath.Separator))
	for i, part := range append(parts, ".lola", "images") {
		child, err := imageSubdir(root, part, create && i >= len(parts))
		root.Close()
		if err != nil {
			return nil, err
		}
		root = child
	}
	return root, nil
}

func imageSubdir(parent *os.Root, name string, create bool) (*os.Root, error) {
	if create {
		if err := parent.Mkdir(name, 0700); err != nil && !errors.Is(err, os.ErrExist) {
			return nil, err
		}
	}
	info, err := parent.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, errors.New("refusing to follow a link in this session's image folder")
	}
	child, err := parent.OpenRoot(name)
	if err != nil {
		return nil, err
	}
	opened, err := child.Stat(".")
	if err != nil || !os.SameFile(info, opened) {
		child.Close()
		return nil, errors.New("this session's image folder changed")
	}
	return child, nil
}

func newImageName(ext string) (string, error) {
	var r [8]byte
	if _, err := rand.Read(r[:]); err != nil {
		return "", err
	}
	return "paste-" + hex.EncodeToString(r[:]) + ext, nil
}

// copyImage copies a file a person pointed at, once it has shown it is an image of a size to carry.
func copyImage(src string, dir *os.Root) (string, error) {
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
	return writeImage(in, dir, ext)
}

func writeImage(in io.Reader, dir *os.Root, ext string) (string, error) {
	name, err := newImageName(ext)
	if err != nil {
		return "", err
	}
	out, err := dir.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return "", err
	}
	_, err = io.Copy(out, io.LimitReader(in, maxImageBytes))
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = dir.Remove(name)
		return "", err
	}
	return filepath.Join(dir.Name(), name), nil
}

// SaveAnnotationImage stores the cropped WebKit snapshot with the session's other attachments.
func (s *PreviewService) SaveAnnotationImage(worktree, encoded string) (string, error) {
	const maxSnapshot = 8 << 20
	if len(encoded) > base64.StdEncoding.EncodedLen(maxSnapshot) {
		return "", errors.New("preview image is too large")
	}
	jpg, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(jpg) > maxSnapshot || http.DetectContentType(jpg) != "image/jpeg" {
		return "", errors.New("invalid preview image")
	}
	dir, err := imagesDir(worktree, true)
	if err != nil {
		return "", err
	}
	defer dir.Close()
	return writeImage(bytes.NewReader(jpg), dir, ".jpg")
}

// AttachImageFile copies an image file into a session's folder and answers the copy's path.
func (s *TalkService) AttachImageFile(worktree, src string) (ImageAttachment, error) {
	dir, err := imagesDir(worktree, true)
	if err != nil {
		return ImageAttachment{}, err
	}
	defer dir.Close()
	dest, err := copyImage(src, dir)
	if err != nil {
		return ImageAttachment{}, err
	}
	return ImageAttachment{Path: dest, Thumb: imageThumb(src)}, nil
}

// AttachClipboardImage saves the image on the clipboard into a session's folder and answers its
// path: image data first, then a copied file. Nothing else on the clipboard is read or kept.
func (s *TalkService) AttachClipboardImage(worktree string) (ImageAttachment, error) {
	// osascript opens its destination by path. Keep that write in private scratch
	// and publish the finished file through the same rooted copy as a dropped file.
	tmp, err := os.MkdirTemp("", "lola-clipboard-")
	if err != nil {
		return ImageAttachment{}, err
	}
	defer os.RemoveAll(tmp)
	ctx, cancel := context.WithTimeout(context.Background(), clipboardWait)
	defer cancel()
	png := filepath.Join(tmp, "clipboard.png")
	if err := clipboardPNG(ctx, png); err == nil {
		if info, err := os.Stat(png); err == nil && info.Size() > 0 {
			return s.AttachImageFile(worktree, png)
		}
	}
	if src, err := clipboardFile(ctx); err == nil && src != "" {
		return s.AttachImageFile(worktree, src)
	}
	return ImageAttachment{}, errors.New("there is no image on the clipboard")
}
