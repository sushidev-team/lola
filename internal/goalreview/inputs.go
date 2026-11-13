package goalreview

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"

	"github.com/sushidev-team/lola/internal/reviewprofile"
)

const maxArtifact = 16 << 20

// copyEvidence accepts only captured goal-check artifacts, not arbitrary paths
// named by an agent. Host paths are replaced in the model's input manifest.
func (r Runner) copyEvidence(goal, dir string, in reviewprofile.Inputs) (reviewprofile.Inputs, error) {
	if len(in.Artifacts) > 100 {
		return in, errors.New("too many review artifacts")
	}
	out := in
	out.Artifacts = slices.Clone(in.Artifacts)
	destination := filepath.Join(dir, "evidence")
	if err := os.Mkdir(destination, 0700); err != nil {
		return in, err
	}
	var total int64
	for i, a := range in.Artifacts {
		base := filepath.Join(r.Workspaces.Root, goal, "validation")
		rel, err := filepath.Rel(base, a.Path)
		if err != nil || !filepath.IsLocal(rel) {
			return in, errors.New("review artifact is outside goal validation evidence")
		}
		parts := strings.Split(rel, string(filepath.Separator))
		if len(parts) != 3 || parts[1] != "artifacts" || !identifier.MatchString(parts[2]) {
			return in, errors.New("review artifact is not a captured validation file")
		}
		checkDir, err := r.Workspaces.ValidationDirectory(goal, parts[0])
		if err != nil {
			return in, err
		}
		rootPath := filepath.Join(checkDir, "artifacts")
		info, err := os.Lstat(rootPath)
		if err != nil {
			return in, err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return in, errors.New("captured evidence directory is not owned")
		}
		root, err := os.OpenRoot(rootPath)
		if err != nil {
			return in, err
		}
		body, err := readEvidence(root, parts[2])
		root.Close()
		if err != nil {
			return in, err
		}
		sum := sha256.Sum256(body)
		if hex.EncodeToString(sum[:]) != a.SHA256 {
			return in, errors.New("review artifact hash changed")
		}
		total += int64(len(body))
		if total > 64<<20 {
			return in, errors.New("review evidence exceeds 64 MiB")
		}
		name := fmt.Sprintf("%03d", i)
		if err := os.WriteFile(filepath.Join(destination, name), body, 0600); err != nil {
			return in, err
		}
		out.Artifacts[i].Path = "/evidence/" + name
	}
	return out, nil
}

func readEvidence(root *os.Root, name string) ([]byte, error) {
	info, err := root.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() < 1 || info.Size() > maxArtifact {
		return nil, errors.New("review evidence must be a bounded regular file")
	}
	f, err := root.OpenFile(name, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err = f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return nil, errors.New("review evidence file changed")
	}
	b, err := io.ReadAll(io.LimitReader(f, maxArtifact+1))
	if err != nil {
		return nil, err
	}
	if len(b) == 0 || len(b) > maxArtifact {
		return nil, errors.New("review evidence size changed")
	}
	return b, nil
}

func mount(source, target string) (string, error) {
	if !filepath.IsAbs(source) || strings.ContainsAny(source, ",\n\r\x00") {
		return "", errors.New("invalid review mount path")
	}
	return "type=bind,source=" + source + ",target=" + target + ",readonly", nil
}
