package projectgit

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

const conflictTextLimit = 4 << 20

type ConflictDocument struct {
	Text     string  `json:"text"`
	Token    string  `json:"token"`
	Current  *string `json:"current,omitempty"`
	Incoming *string `json:"incoming,omitempty"`
}

// Work on a pinned parent directory. A tracked directory may have been
// replaced by a symlink since Git recorded the index stages.
func conflictParent(dir, path string) (*os.Root, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	parts := strings.Split(path, string(filepath.Separator))
	for _, part := range parts[:len(parts)-1] {
		info, err := root.Lstat(part)
		if err != nil || !info.IsDir() || strings.EqualFold(part, ".git") {
			root.Close()
			return nil, errors.New("conflict directory changed; refresh the checkout")
		}
		next, err := root.OpenRoot(part)
		root.Close()
		if err != nil {
			return nil, err
		}
		actual, err := next.Stat(".")
		if err != nil || !os.SameFile(info, actual) {
			next.Close()
			return nil, errors.New("conflict directory changed while opening it")
		}
		root = next
	}
	return root, nil
}

func (c Client) ConflictDocument(ctx context.Context, dir, path string) (ConflictDocument, error) {
	return c.conflictDocument(ctx, dir, path, true)
}

func (c Client) conflictDocument(ctx context.Context, dir, path string, includeSides bool) (ConflictDocument, error) {
	var doc ConflictDocument
	if !validPath(path) {
		return doc, errors.New("select a conflicted file")
	}
	stages, err := c.git(ctx, dir, "", "ls-files", "--unmerged", "-z", "--", path)
	if err != nil {
		return doc, err
	}
	if len(stages) == 0 {
		return doc, errors.New("this file has already been resolved; refresh Changes")
	}
	for record := range strings.SplitSeq(strings.TrimSuffix(string(stages), "\x00"), "\x00") {
		meta, name, ok := strings.Cut(record, "\t")
		if !ok || name != path || !(strings.HasPrefix(meta, "100644 ") || strings.HasPrefix(meta, "100755 ")) {
			return doc, errors.New("resolve this file type in your external merge tool, then stage the result")
		}
		fields := strings.Fields(meta)
		if len(fields) != 3 {
			return doc, errors.New("cannot read conflict stages")
		}
		if includeSides && (fields[2] == "2" || fields[2] == "3") {
			blob, err := c.git(ctx, dir, "", "cat-file", "blob", fields[1])
			if err == nil && len(blob) <= conflictTextLimit && utf8.Valid(blob) && bytes.IndexByte(blob, 0) < 0 {
				side := string(blob)
				if fields[2] == "2" {
					doc.Current = &side
				} else {
					doc.Incoming = &side
				}
			}
		}
	}
	root, err := conflictParent(dir, path)
	if err != nil {
		return doc, err
	}
	defer root.Close()
	info, err := root.Lstat(filepath.Base(path))
	if errors.Is(err, os.ErrNotExist) {
		return doc, errors.New("the working file was deleted; stage the deletion or restore the version you want")
	}
	if err != nil {
		return doc, err
	}
	if !info.Mode().IsRegular() || info.Size() > conflictTextLimit {
		return doc, errors.New("use an external editor for this file, then stage the result")
	}
	f, err := root.Open(filepath.Base(path))
	if err != nil {
		return doc, err
	}
	defer f.Close()
	actual, err := f.Stat()
	if err != nil || !os.SameFile(info, actual) {
		return doc, errors.New("working file changed while opening it")
	}
	b, err := io.ReadAll(io.LimitReader(f, conflictTextLimit+1))
	if err != nil {
		return doc, err
	}
	if len(b) > conflictTextLimit || !utf8.Valid(b) || bytes.IndexByte(b, 0) >= 0 {
		return doc, errors.New("use an external merge tool for binary or oversized files, then stage the result")
	}
	head, err := c.text(ctx, dir, "rev-parse", "HEAD")
	if err != nil {
		return doc, err
	}
	digest := sha256.New()
	fmt.Fprintf(digest, "%s\x00%s\x00%s\x00%d\x00", dir, head, path, info.Mode())
	digest.Write(stages)
	digest.Write(b)
	doc.Text = string(b)
	doc.Token = fmt.Sprintf("%x", digest.Sum(nil))
	return doc, nil
}

// Stage only after the reviewed worktree content and unmerged index still match.
func (c Client) SaveConflict(ctx context.Context, dir, path, token, result string) error {
	if len(result) > conflictTextLimit || !utf8.ValidString(result) || strings.ContainsRune(result, 0) {
		return errors.New("the resolution must be a UTF-8 text file under 4 MiB")
	}
	doc, err := c.conflictDocument(ctx, dir, path, false)
	if err != nil {
		return err
	}
	if doc.Token != token {
		return errors.New("the conflict changed on disk; reload it before saving")
	}
	if doc.Text != result {
		root, err := conflictParent(dir, path)
		if err != nil {
			return err
		}
		defer root.Close()
		info, err := root.Lstat(filepath.Base(path))
		if err != nil {
			return err
		}
		temp := ".lola-resolution-" + rand.Text()
		f, err := root.OpenFile(temp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return err
		}
		defer root.Remove(temp)
		_, err = io.WriteString(f, result)
		if err == nil {
			err = f.Chmod(info.Mode().Perm())
		}
		closeErr := f.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
		latest, err := c.conflictDocument(ctx, dir, path, false)
		if err != nil {
			return err
		}
		if latest.Token != token {
			return errors.New("the conflict changed on disk; reload it before saving")
		}
		if err = root.Rename(temp, filepath.Base(path)); err != nil {
			return err
		}
	}
	if err := c.changeIndex(ctx, dir, []string{path}, true, true); err != nil {
		return fmt.Errorf("resolution saved to the working file; staging failed: %w", err)
	}
	return nil
}
