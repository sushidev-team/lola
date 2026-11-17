package runtime

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/toolchain"
)

// A sandboxed project's toolchains (PHP, cargo, go…) are not in lola's default
// image. Before a launch, the runtime resolves what the project needs
// (internal/toolchain: its sandbox_toolchains, else what the checkout's files
// ask for) and builds a derived image FROM the default one, tagged by content,
// so it is built once and reused until a toolchain, version or the base
// changes. A project naming its own image gets exactly that image.

// sandboxBuildTimeout bounds one derived-image build (inside the spawn's own
// deadline).
const sandboxBuildTimeout = 9 * time.Minute

// projectLabel tags derived images with their project, so older ones of the
// same project can be pruned.
const projectLabel = "dev.sushi.lola.project"

// projectImages remembers the image prepared for each project, read by the
// launch line (which has no context to build with). Package-level because a
// Native is copied by value; keyed by the config it was prepared under, so a
// reload or another Native never reads a stale answer.
var preparedImages projectImages

type projectImages struct {
	mu   sync.Mutex
	tags map[string]string
	// building serializes builds per project: two spawns of one project
	// share the first build.
	building map[string]*sync.Mutex
}

func (c *projectImages) get(project string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.tags[project]
}

func (c *projectImages) set(project, tag string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.tags == nil {
		c.tags = map[string]string{}
	}
	c.tags[project] = tag
}

func (c *projectImages) lock(project string) *sync.Mutex {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.building == nil {
		c.building = map[string]*sync.Mutex{}
	}
	m := c.building[project]
	if m == nil {
		m = &sync.Mutex{}
		c.building[project] = m
	}
	return m
}

// SandboxSpec is what the derived image of p adds (empty when p names its own
// image or needs nothing beyond the default one). A stack merges its members'.
func (n *Native) SandboxSpec(p *config.Project) toolchain.Spec {
	if p == nil || !n.Cfg.UsesDefaultSandboxImage(p) {
		return toolchain.Spec{}
	}
	dirs := []string{p.Path}
	if p.IsStack() {
		dirs = nil
		for _, m := range n.Cfg.StackMembers(p.Name) {
			dirs = append(dirs, m.Path)
		}
	}
	var out toolchain.Spec
	for _, d := range dirs {
		if d == "" {
			continue
		}
		s := toolchain.Resolve(p.SandboxToolchains, p.SandboxPackages, d)
		for _, t := range s.Toolchains {
			if !out.Has(t.Kind) {
				out.Toolchains = append(out.Toolchains, t)
			}
		}
		out.PHPExtensions = append(out.PHPExtensions, s.PHPExtensions...)
		out.NodeTools = append(out.NodeTools, s.NodeTools...)
	}
	out.Packages = slices.Clone(p.SandboxPackages)
	slices.Sort(out.PHPExtensions)
	out.PHPExtensions = slices.Compact(out.PHPExtensions)
	out.NodeTools = slices.Compact(out.NodeTools)
	return out
}

// projectImage returns the image p's container runs, building the derived
// image first when it does not exist yet.
func (n *Native) projectImage(ctx context.Context, p *config.Project) (string, error) {
	base := n.Cfg.SandboxImageFor(p)
	spec := n.SandboxSpec(p)
	df := toolchain.Dockerfile(base, spec)
	if df == "" {
		preparedImages.set(n.imageKey(p), base)
		return base, nil
	}
	m := preparedImages.lock(n.imageKey(p))
	m.Lock()
	defer m.Unlock()

	docker := n.Cfg.Sandbox.DockerBin()
	baseID, err := n.imageID(ctx, docker, base)
	if err != nil {
		return "", err
	}
	tag := toolchain.Tag(df, baseID)
	if _, err := n.docker(ctx, docker, "", "image", "inspect", "--format", "{{.Id}}", tag); err == nil {
		preparedImages.set(n.imageKey(p), tag)
		return tag, nil
	}
	if n.Logf != nil {
		n.Logf("sandbox: building the image for %s (%s); the first start takes a few minutes", p.Name, spec.Summary())
	}
	bctx, cancel := context.WithTimeout(ctx, sandboxBuildTimeout)
	_, err = n.docker(bctx, docker, df, "build", "-q", "-t", tag, "--label", projectLabel+"="+p.Name, "-")
	cancel()
	if err != nil {
		return "", fmt.Errorf("sandbox: building the image for %s (%s) failed: %w", p.Name, spec.Summary(), err)
	}
	preparedImages.set(n.imageKey(p), tag)
	n.pruneProjectImages(ctx, docker, p.Name, tag)
	return tag, nil
}

// imageKey identifies p under n's current config in preparedImages.
func (n *Native) imageKey(p *config.Project) string {
	return fmt.Sprintf("%p/%s", n.Cfg, p.Name)
}

// sandboxImageOf is the image the launch line runs for p: the one
// prepareSandbox prepared, else the configured one.
func (n *Native) sandboxImageOf(p *config.Project) string {
	if p != nil {
		if tag := preparedImages.get(n.imageKey(p)); tag != "" {
			return tag
		}
	}
	return n.Cfg.SandboxImageFor(p)
}

// imageID is the local ID of image, pulling it first when it is missing.
func (n *Native) imageID(ctx context.Context, docker, image string) (string, error) {
	if n.SandboxLola != nil {
		return n.SandboxLola.imageID(ctx, docker, image)
	}
	out, err := n.docker(ctx, docker, "", "image", "inspect", "--format", "{{.Id}}", image)
	if err != nil {
		return "", fmt.Errorf("sandbox: %s is not available: %w", image, err)
	}
	return strings.TrimSpace(string(out)), nil
}

// pruneProjectImages removes the project's older derived images. Best
// effort: one still used by a running container stays.
func (n *Native) pruneProjectImages(ctx context.Context, docker, project, keep string) {
	out, err := n.docker(ctx, docker, "", "image", "ls", "--format", "{{.Repository}}:{{.Tag}}", "--filter", "label="+projectLabel+"="+project)
	if err != nil {
		return
	}
	for _, ref := range strings.Fields(string(out)) {
		if ref != keep && strings.HasPrefix(ref, "lola-sandbox-project:") {
			_, _ = n.docker(ctx, docker, "", "image", "rm", ref)
		}
	}
}

// docker runs the docker CLI with stdin (when not ""), through the Docker seam
// in tests.
func (n *Native) docker(ctx context.Context, docker, stdin string, args ...string) ([]byte, error) {
	if n.Docker != nil {
		return n.Docker(ctx, docker, stdin, args...)
	}
	cctx, cancel := context.WithTimeout(ctx, sandboxBuildTimeout)
	defer cancel()
	cmd := exec.CommandContext(cctx, docker, args...)
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	cmd.WaitDelay = time.Second
	out, err := cmd.Output()
	if err != nil {
		if msg := tail(strings.TrimSpace(stderr.String()), 600); msg != "" {
			return nil, fmt.Errorf("%w: %s", err, msg)
		}
		return nil, err
	}
	return out, nil
}

var volumeUnsafe = regexp.MustCompile(`[^a-z0-9_.-]+`)

// cacheVolume is the docker volume holding a project's package caches
// (~/.cache in the container: npm, composer, cargo, go, uv…), shared by its
// sessions so installs are not repeated. Only lola's own images know the
// layout, so a custom image gets no volume.
func (n *Native) cacheVolume(p *config.Project) string {
	if p == nil || !n.Cfg.UsesDefaultSandboxImage(p) {
		return ""
	}
	name := strings.Trim(volumeUnsafe.ReplaceAllString(strings.ToLower(p.Name), "-"), "-.")
	if name == "" {
		return ""
	}
	return "lola-cache-" + name
}
