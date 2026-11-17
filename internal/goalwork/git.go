// Package goalwork owns private Git stores for autonomous work. No worker shares
// refs, configuration, hooks, or writable files with the user's checkout or the
// authoritative goal repository. Worker output crosses the boundary as a bundle
// file, never by running host git against worker-controlled metadata.
package goalwork

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

var (
	ErrConflict = errors.New("goal candidate conflicts with current goal revision")
	ErrStale    = errors.New("goal revision changed; verification must be repeated")
	segment     = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
	objectID    = regexp.MustCompile(`^(?:[a-f0-9]{40}|[a-f0-9]{64})$`)
)

type Manager struct {
	mu     sync.Mutex // serializes manifest-level integration and its journal
	Root   string     // <LOLA_HOME>/goals, private to the daemon
	GitBin string
	// Run is the exec seam. Arguments include all safe git configuration flags.
	Run            func(context.Context, string, []string) (string, error)
	Timeout        time.Duration
	MaxBundleBytes int64
}

func GoalBranch(id string) (string, error) {
	if !segment.MatchString(id) {
		return "", fmt.Errorf("invalid goal identity")
	}
	return "goal/" + id, nil
}

func TaskBranch(task, attempt string) (string, error) {
	if !segment.MatchString(task) || !segment.MatchString(attempt) {
		return "", fmt.Errorf("invalid task or attempt identity")
	}
	return "task/" + task + "-" + attempt, nil
}

func (m *Manager) path(parts ...string) (string, error) {
	if !filepath.IsAbs(m.Root) || filepath.Clean(m.Root) == string(filepath.Separator) {
		return "", fmt.Errorf("goal workspace root must be an absolute private directory")
	}
	for _, part := range parts {
		if !segment.MatchString(part) {
			return "", fmt.Errorf("invalid goal path segment %q", part)
		}
	}
	root := filepath.Clean(m.Root)
	// The configured root's ancestors are trusted (macOS /tmp and /var are
	// symlink aliases). The root and every managed descendant must stay real.
	if fi, err := os.Lstat(root); err == nil && fi.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("goal root is a symlink: %s", root)
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	out := root
	for _, part := range parts {
		out = filepath.Join(out, part)
		if fi, err := os.Lstat(out); err == nil && fi.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("goal path contains a symlink: %s", out)
		} else if err != nil && !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
	}
	return out, nil
}

func (m *Manager) git(ctx context.Context, dir string, args ...string) (string, error) {
	// Every repository touched by this runner is either a trusted source read,
	// a daemon-owned store, or a fresh clone. Never use it on a worker's store.
	argv := []string{"-c", "core.hooksPath=/dev/null", "-c", "core.fsmonitor=false",
		"-c", "fetch.fsckObjects=true", "-c", "transfer.fsckObjects=true",
		"-c", "gc.auto=0", "-c", "maintenance.auto=false",
		"-c", "commit.gpgsign=false", "-c", "tag.gpgsign=false", "-c", "credential.helper=",
		"-c", "protocol.file.allow=always", "-c", "protocol.ext.allow=never",
		"-c", "uploadpack.packObjectsHook=", "-c", "user.name=Lola",
		"-c", "user.email=lola@localhost", "-c", "init.defaultBranch=main"}
	argv = append(argv, args...)
	timeout := m.Timeout
	if timeout <= 0 {
		timeout = 90 * time.Second
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if m.Run != nil {
		return m.Run(cctx, dir, argv)
	}
	bin := m.GitBin
	if bin == "" {
		bin = "git"
	}
	cmd := exec.CommandContext(cctx, bin, argv...)
	cmd.Dir = dir
	for _, e := range os.Environ() {
		key, _, _ := strings.Cut(e, "=")
		if strings.HasPrefix(key, "GIT_") || key == "GH_TOKEN" || key == "GITHUB_TOKEN" || key == "SSH_AUTH_SOCK" {
			continue
		}
		cmd.Env = append(cmd.Env, e)
	}
	cmd.Env = append(cmd.Env, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_TERMINAL_PROMPT=0", "GIT_NO_REPLACE_OBJECTS=1")
	cmd.WaitDelay = 2 * time.Second
	out, stderr := &limitedBuffer{}, &limitedBuffer{}
	cmd.Stdout, cmd.Stderr = out, stderr
	err := cmd.Run()
	if cctx.Err() != nil {
		return "", cctx.Err()
	}
	if out.overflow || stderr.overflow {
		return "", errors.New("goal git output exceeded capture limit")
	}
	if err != nil {
		return "", fmt.Errorf("goal git %s: %w: %s", args[0], err, strings.TrimSpace(stderr.String()))
	}
	return out.String(), nil
}

type limitedBuffer struct {
	bytes.Buffer
	overflow bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	n := len(p)
	left := (1 << 20) - b.Len()
	if len(p) > left {
		b.overflow = true
		p = p[:max(0, left)]
	}
	_, _ = b.Buffer.Write(p)
	return n, nil
}

func validSHA(sha string) error {
	if !objectID.MatchString(sha) {
		return fmt.Errorf("exact Git commit object ID required")
	}
	return nil
}

func (m *Manager) resolve(ctx context.Context, dir, ref string) (string, error) {
	out, err := m.git(ctx, dir, "rev-parse", "--verify", ref+"^{commit}")
	if err != nil {
		return "", err
	}
	sha := strings.TrimSpace(out)
	return sha, validSHA(sha)
}

func (m *Manager) noRemote(ctx context.Context, dir string) error {
	_, err := m.git(ctx, dir, "config", "--local", "--remove-section", "remote.origin")
	return err
}
