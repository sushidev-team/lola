package goalcheck

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

const maxArtifactBytes = 16 << 20

func captureArtifacts(workspace, dir string, specs []ArtifactSpec) ([]Artifact, error) {
	root, err := os.OpenRoot(workspace)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	var result []Artifact
	if len(specs) == 0 {
		return result, nil
	}
	destination := filepath.Join(dir, "artifacts")
	if err := os.Mkdir(destination, 0700); err != nil {
		return nil, err
	}
	for _, spec := range specs {
		f, err := openRegular(root, spec.Path)
		if err != nil {
			return nil, fmt.Errorf("capture %s: %w", spec.ID, err)
		}
		path := filepath.Join(destination, spec.ID)
		a, err := copyArtifact(f, path)
		_ = f.Close()
		if err != nil {
			return nil, fmt.Errorf("capture %s: %w", spec.ID, err)
		}
		a.ID = spec.ID
		a.Kind = spec.Kind
		result = append(result, a)
	}
	return result, nil
}

func openRegular(root *os.Root, path string) (*os.File, error) {
	if !filepath.IsLocal(path) || filepath.Clean(path) != path {
		return nil, errors.New("artifact path escapes workspace")
	}
	var prefix string
	for _, part := range strings.Split(path, string(filepath.Separator)) {
		prefix = filepath.Join(prefix, part)
		fi, err := root.Lstat(prefix)
		if err != nil {
			return nil, err
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			return nil, errors.New("artifact symlinks are not accepted")
		}
		if prefix != path && !fi.IsDir() {
			return nil, errors.New("artifact ancestor is not a directory")
		}
	}
	f, err := root.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	fi, err := f.Stat()
	if err != nil || !fi.Mode().IsRegular() || fi.Size() == 0 || fi.Size() > maxArtifactBytes {
		_ = f.Close()
		return nil, errors.New("artifact must be a nonempty regular file, at most 16 MiB")
	}
	return f, nil
}

func copyArtifact(f *os.File, destination string) (Artifact, error) {
	out, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return Artifact{}, err
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(out, h), io.LimitReader(f, maxArtifactBytes+1))
	if err == nil {
		err = out.Sync()
	}
	closeErr := out.Close()
	if err != nil {
		return Artifact{}, err
	}
	if closeErr != nil {
		return Artifact{}, closeErr
	}
	if n == 0 || n > maxArtifactBytes {
		return Artifact{}, errors.New("artifact size changed outside allowed limits")
	}
	return Artifact{Path: destination, SHA256: hex.EncodeToString(h.Sum(nil)), Bytes: n}, nil
}

func validateArtifacts(dir string, artifacts []Artifact) error {
	if len(artifacts) == 0 {
		return nil
	}
	root, err := os.OpenRoot(filepath.Join(dir, "artifacts"))
	if err != nil {
		return err
	}
	defer root.Close()
	for _, a := range artifacts {
		if !identifier.MatchString(a.ID) || a.Path != filepath.Join(dir, "artifacts", a.ID) {
			return errors.New("artifact receipt path mismatch")
		}
		f, err := openRegular(root, a.ID)
		if err != nil {
			return err
		}
		h := sha256.New()
		n, err := io.Copy(h, io.LimitReader(f, maxArtifactBytes+1))
		_ = f.Close()
		if err != nil {
			return err
		}
		if n != a.Bytes || hex.EncodeToString(h.Sum(nil)) != a.SHA256 {
			return errors.New("captured artifact changed after verification")
		}
	}
	return nil
}
