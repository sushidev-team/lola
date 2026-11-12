package runtime

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sushidev-team/lola/internal/agent"
	"github.com/sushidev-team/lola/internal/config"
)

// A sandboxed PHP project on lola's default image gets a derived image built
// once (content-tagged), its launch runs that image, and its package caches
// live in a per-project volume.
func TestSandboxedProjectGetsItsToolchainImage(t *testing.T) {
	f := sandboxFixture(t)
	f.n.Cfg.Sandbox.Image = "" // lola's default image
	if err := os.WriteFile(filepath.Join(f.repo, "composer.json"), []byte(`{"require":{"php":"^8.3","ext-redis":"*"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	var builds []string
	built := map[string]bool{}
	f.n.Docker = func(_ context.Context, _ string, stdin string, args ...string) ([]byte, error) {
		switch {
		case args[0] == "image" && args[1] == "inspect":
			ref := args[len(args)-1]
			if strings.HasPrefix(ref, "lola-sandbox-project:") && !built[ref] {
				return nil, errors.New("no such image")
			}
			return []byte("sha256:0123456789abcdef\n"), nil
		case args[0] == "build":
			builds = append(builds, stdin)
			built[args[3]] = true
			return nil, nil
		}
		return nil, nil
	}
	p := f.n.Cfg.ProjectByName("nori")
	if err := f.n.prepareSandbox(context.Background(), "nori", true); err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if len(builds) != 1 || !strings.Contains(builds[0], "php8.5-cli") || !strings.Contains(builds[0], "php8.5-redis") || !strings.HasPrefix(builds[0], "FROM "+config.DefaultSandboxImage()+"\n") {
		t.Fatalf("builds = %q", builds)
	}
	line := f.n.sandboxLine(p, "lola-nori-eng-42", agent.Claude, "claude")
	if !strings.Contains(line, "lola-sandbox-project:") || !strings.Contains(line, "lola-cache-nori:/home/lola/.cache") {
		t.Fatalf("launch line lacks the project image or cache:\n%s", line)
	}
	// Prepared again: the image exists, nothing is rebuilt.
	if err := f.n.prepareSandbox(context.Background(), "nori", true); err != nil || len(builds) != 1 {
		t.Fatalf("rebuilt: %v %d", err, len(builds))
	}
}

func TestCustomImageGetsNoToolchainsOrCache(t *testing.T) {
	f := sandboxFixture(t) // [sandbox].image = acme/agent:1
	if err := os.WriteFile(filepath.Join(f.repo, "composer.json"), []byte(`{"require":{"php":"^8.3"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	f.n.Docker = func(context.Context, string, string, ...string) ([]byte, error) {
		t.Fatal("docker called for a custom image")
		return nil, nil
	}
	if err := f.n.prepareSandbox(context.Background(), "nori", true); err != nil {
		t.Fatal(err)
	}
	line := f.n.sandboxLine(f.n.Cfg.ProjectByName("nori"), "lola-nori-eng-42", agent.Claude, "claude")
	if strings.Contains(line, "lola-cache-") || !strings.Contains(line, "acme/agent:1") {
		t.Fatalf("custom image line:\n%s", line)
	}
}
