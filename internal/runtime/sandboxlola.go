package runtime

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/sushidev-team/lola/internal/config"
)

// The LINUX lola a sandboxed agent's hooks run. The container cannot execute
// the macOS binary the hook wiring names, so a linux build is bind-mounted
// read-only at the host lola's path. [sandbox].lola_bin names one explicitly;
// otherwise SandboxLola takes the one lola's default image carries
// (/usr/local/bin/lola), once per image ID, and caches it under
// $LOLA_HOME/cache/sandbox. A mount source must be something the agent cannot
// write, and ~/.lola is never mounted, so the cache qualifies.
//
// Taking it from the DEFAULT image, whatever image the project runs, is what
// lets any custom image work: it needs the agent and git, never lola.

// sandboxImageLola is where the default image keeps its lola (sandbox/Dockerfile).
const sandboxImageLola = "/usr/local/bin/lola"

// Bounds on each docker exec. A pull moves a few hundred MB once.
const (
	sandboxInspectTimeout = 15 * time.Second
	sandboxPullTimeout    = 10 * time.Minute
	sandboxCopyTimeout    = 2 * time.Minute
)

// sandboxCacheKeep is how long a cached lola of an image no longer current is
// kept: a running container may still have it mounted, and sessions rarely
// live this long.
const sandboxCacheKeep = 30 * 24 * time.Hour

// imageIDRe is `docker image inspect --format {{.Id}}`'s answer.
var imageIDRe = regexp.MustCompile(`^(?:sha256:)?([a-f0-9]{12,64})$`)

// SandboxLola extracts and caches the default image's linux lola. One per
// daemon; safe for concurrent spawns. Run is the docker exec seam: it runs the
// docker CLI with args under ctx and returns stdout, or an error carrying a
// short stderr tail. Nil runs the real CLI. Tests never run docker.
type SandboxLola struct {
	Home string
	Run  func(ctx context.Context, docker string, args ...string) ([]byte, error)

	mu   sync.Mutex
	last string
}

// Last is the most recently resolved path ("" before the first Resolve).
func (r *SandboxLola) Last() string {
	if r == nil {
		return ""
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.last
}

// Resolve returns the cached linux lola for image, extracting it first when
// this image ID has none yet and pulling the image when docker lacks it. The
// error says which step failed and how to fix it; it never falls back.
func (r *SandboxLola) Resolve(ctx context.Context, docker, image string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	id, err := r.imageID(ctx, docker, image)
	if err != nil {
		return "", err
	}
	dir := filepath.Join(r.Home, "cache", "sandbox")
	dst := filepath.Join(dir, "lola-"+id)
	if isExecutableFile(dst) {
		r.last = dst
		return dst, nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("sandbox: cannot create the lola cache: %w", err)
	}
	var nonce [6]byte
	_, _ = rand.Read(nonce[:])
	tmp := dst + ".tmp-" + hex.EncodeToString(nonce[:])
	defer os.Remove(tmp)

	cctx, cancel := context.WithTimeout(ctx, sandboxInspectTimeout)
	out, err := r.run(cctx, docker, "create", image)
	cancel()
	if err != nil {
		return "", fmt.Errorf("sandbox: docker could not create a container from %s to copy lola out of it: %w", image, err)
	}
	cid := strings.TrimSpace(string(out))
	defer func() {
		rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), sandboxInspectTimeout)
		defer cancel()
		_, _ = r.run(rctx, docker, "rm", "-f", cid)
	}()
	cctx, cancel = context.WithTimeout(ctx, sandboxCopyTimeout)
	_, err = r.run(cctx, docker, "cp", cid+":"+sandboxImageLola, tmp)
	cancel()
	if err != nil {
		return "", fmt.Errorf("sandbox: copying lola out of %s failed (is it lola's sandbox image?): %w", image, err)
	}
	if !isRegularFile(tmp) {
		return "", fmt.Errorf("sandbox: %s carries no lola at %s", image, sandboxImageLola)
	}
	if err := os.Chmod(tmp, 0o755); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, dst); err != nil {
		return "", err
	}
	r.prune(dir, dst)
	r.last = dst
	return dst, nil
}

// imageID asks docker for image's ID, pulling the image once when it is not
// present. Returns the short (12 hex) form used as the cache key.
func (r *SandboxLola) imageID(ctx context.Context, docker, image string) (string, error) {
	inspect := func() (string, error) {
		cctx, cancel := context.WithTimeout(ctx, sandboxInspectTimeout)
		defer cancel()
		out, err := r.run(cctx, docker, "image", "inspect", "--format", "{{.Id}}", image)
		if err != nil {
			return "", err
		}
		m := imageIDRe.FindStringSubmatch(strings.TrimSpace(string(out)))
		if m == nil {
			return "", fmt.Errorf("unexpected image id %q", strings.TrimSpace(string(out)))
		}
		return m[1][:12], nil
	}
	if id, err := inspect(); err == nil {
		return id, nil
	}
	cctx, cancel := context.WithTimeout(ctx, sandboxPullTimeout)
	_, err := r.run(cctx, docker, "pull", image)
	cancel()
	if err != nil {
		var ee *exec.Error
		if errors.As(err, &ee) {
			return "", fmt.Errorf("sandbox: the docker CLI %q is not available (install Docker, OrbStack or Colima, or set [sandbox].docker): %w", docker, err)
		}
		return "", fmt.Errorf("sandbox: pulling %s failed (is docker running and the registry reachable?): %w", image, err)
	}
	id, err := inspect()
	if err != nil {
		return "", fmt.Errorf("sandbox: %s is still missing after a pull: %w", image, err)
	}
	return id, nil
}

// prune removes cached binaries of earlier images once they are old enough
// that no live container should still mount them. Best-effort.
func (r *SandboxLola) prune(dir, keep string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		p := filepath.Join(dir, e.Name())
		if p == keep || !strings.HasPrefix(e.Name(), "lola-") {
			continue
		}
		if fi, err := e.Info(); err == nil && time.Since(fi.ModTime()) > sandboxCacheKeep {
			_ = os.Remove(p)
		}
	}
}

func (r *SandboxLola) run(ctx context.Context, docker string, args ...string) ([]byte, error) {
	if r.Run != nil {
		return r.Run(ctx, docker, args...)
	}
	return runDocker(ctx, docker, args...)
}

// runDocker execs the docker CLI: stdout back, a short stderr tail in the error.
func runDocker(ctx context.Context, docker string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, docker, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	cmd.WaitDelay = time.Second
	out, err := cmd.Output()
	if err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("docker %s timed out: %w", args[0], ctx.Err())
		}
		if msg := tail(strings.TrimSpace(stderr.String()), 300); msg != "" {
			return nil, fmt.Errorf("%w: %s", err, msg)
		}
		return nil, err
	}
	return out, nil
}

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return "…" + s[len(s)-n:]
}

func isRegularFile(p string) bool {
	fi, err := os.Lstat(p)
	return err == nil && fi.Mode().IsRegular()
}

// sandboxLolaBin resolves the linux lola for this daemon's sandboxed launches:
// [sandbox].lola_bin when set, else the default image's (SandboxLola). Every
// sandboxed launch path calls it before the pane starts, so a missing docker
// or image fails the launch with a clear error instead of a hookless agent.
func (n *Native) sandboxLolaBin(ctx context.Context) (string, error) {
	if lb := n.Cfg.Sandbox.LolaBin; lb != "" {
		// A missing bind-mount source would make docker create an empty
		// directory in its place, and every hook would fail.
		if fi, err := os.Stat(lb); err != nil || !fi.Mode().IsRegular() {
			return "", fmt.Errorf("sandbox: [sandbox].lola_bin %q is not a file", lb)
		}
		return lb, nil
	}
	if n.SandboxLola == nil {
		return "", errors.New("sandbox: no linux lola for the agent's hooks: set [sandbox].lola_bin")
	}
	return n.SandboxLola.Resolve(ctx, n.Cfg.Sandbox.DockerBin(), config.DefaultSandboxImage())
}

// sandboxLolaPath is the linux lola a launch line mounts: the explicit one, or
// the one the last sandboxLolaBin resolved. "" when neither exists, and the
// launch line then refuses to start (sandboxHooklessGuard).
func (n *Native) sandboxLolaPath() string {
	if lb := n.Cfg.Sandbox.LolaBin; lb != "" {
		return lb
	}
	return n.SandboxLola.Last()
}

// sandboxHooklessGuard is the line a sandboxed pane runs instead of the agent
// when no linux lola is known: an agent whose hooks cannot reach the daemon is
// invisible to lola, so it must not start at all.
const sandboxHooklessGuard = "echo 'lola: no linux lola is available for the sandbox hooks (is docker running? see lola doctor)' >&2; exit 1"

// prepareSandbox runs before a sandboxed launch: it makes sure the linux lola
// for the hooks exists. A host launch passes through.
func (n *Native) prepareSandbox(ctx context.Context, project string, sandboxed bool) error {
	if !sandboxed {
		return nil
	}
	if _, err := n.sandboxLolaBin(ctx); err != nil {
		return err
	}
	// The project's own image (its toolchains on top of the default one) is
	// built here, before the launch line names it.
	if p := n.sandboxProject(project); p != nil {
		if _, err := n.projectImage(ctx, p); err != nil {
			return err
		}
	}
	return nil
}
