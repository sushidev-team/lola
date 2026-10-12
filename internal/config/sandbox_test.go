package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const sandboxBody = `
[defaults]
global_cap = 3

[sandbox]
image = "ghcr.io/acme/agent:1"
lola_bin = "/opt/lola-linux"
pass_env = ["ANTHROPIC_API_KEY", "GH_TOKEN"]

[[project]]
name = "web"
path = "/src/web"
repo = "acme/web"
default_branch = "main"
sandbox = "docker"
sandbox_ports = ["3000", "8080:80"]

[[project.linked_repo]]
name = "api"
path = "/src/api"
repo = "acme/api"
default_branch = "develop"
`

func TestSandboxAndLinkedReposRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(sandboxBody), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	p := c.ProjectByName("web")
	if !p.Sandboxed() || c.SandboxImageFor(p) != "ghcr.io/acme/agent:1" || !c.AnySandboxed() {
		t.Fatalf("sandbox not resolved: %+v / %+v", p, c.Sandbox)
	}
	want := []LinkedRepo{{Name: "api", Path: "/src/api", Repo: "acme/api", DefaultBranch: "develop"}}
	if !reflect.DeepEqual(p.LinkedRepos, want) {
		t.Fatalf("linked = %+v", p.LinkedRepos)
	}
	if got := p.LinkedRepos[0].BaseBranch(*p); got != "develop" {
		t.Fatalf("base = %q", got)
	}
	if err := c.Save(path); err != nil {
		t.Fatal(err)
	}
	back, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(back.Sandbox, c.Sandbox) || !reflect.DeepEqual(back.ProjectByName("web"), p) {
		t.Fatalf("round trip lost values:\n%+v\n%+v", back.Sandbox, back.ProjectByName("web"))
	}
}

func TestEmptySandboxTableIsNotWritten(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	c, _ := Load(path)
	c.Defaults.GlobalCap = 1
	if err := c.Save(path); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	if strings.Contains(string(raw), "[sandbox]") || strings.Contains(string(raw), "linked_repo") {
		t.Fatalf("zero sandbox config written:\n%s", raw)
	}
	if c.Sandbox.DockerBin() != "docker" || c.Sandbox.EffectiveHookPort() != DefaultSandboxHookPort {
		t.Fatalf("defaults: %q %d", c.Sandbox.DockerBin(), c.Sandbox.EffectiveHookPort())
	}
}

func TestSandboxValidation(t *testing.T) {
	base := func() *Config {
		return &Config{
			Defaults: Defaults{GlobalCap: 1},
			Sandbox:  SandboxConfig{Image: "img", LolaBin: "/opt/lola"},
			Projects: []Project{{Name: "web", Path: "/src/web", Sandbox: SandboxDocker}},
		}
	}
	cases := []struct {
		name string
		mut  func(c *Config)
		want string
	}{
		{"unknown kind", func(c *Config) { c.Projects[0].Sandbox = "podman" }, "sandbox must be one of"},
		{"no image", func(c *Config) { c.Sandbox.Image = "" }, "needs an image"},
		{"no linux lola", func(c *Config) { c.Sandbox.LolaBin = "" }, "needs [sandbox].lola_bin"},
		{"relative lola", func(c *Config) { c.Sandbox.LolaBin = "lola" }, "absolute path"},
		{"bad port", func(c *Config) { c.Projects[0].SandboxPorts = []string{"0.0.0.0:80:80"} }, "sandbox_ports entry"},
		{"port range", func(c *Config) { c.Projects[0].SandboxPorts = []string{"70000"} }, "sandbox_ports entry"},
		{"bad pass_env", func(c *Config) { c.Sandbox.PassEnv = []string{"A;rm"} }, "pass_env"},
		{"hook port", func(c *Config) { c.Sandbox.HookPort = -1 }, "hook_port"},
		{"image without sandbox", func(c *Config) { c.Projects[0].Sandbox = ""; c.Projects[0].SandboxImage = "x" }, "need sandbox"},
		{"linked no name", func(c *Config) { c.Projects[0].LinkedRepos = []LinkedRepo{{Path: "/src/api"}} }, "name is required"},
		{"linked bad name", func(c *Config) { c.Projects[0].LinkedRepos = []LinkedRepo{{Name: "../x", Path: "/src/api"}} }, "must be lowercase"},
		{"linked dup", func(c *Config) {
			c.Projects[0].LinkedRepos = []LinkedRepo{{Name: "api", Path: "/a"}, {Name: "api", Path: "/b"}}
		}, "duplicate name"},
		{"linked relative", func(c *Config) { c.Projects[0].LinkedRepos = []LinkedRepo{{Name: "api", Path: "api"}} }, "must be absolute"},
		{"linked self", func(c *Config) { c.Projects[0].LinkedRepos = []LinkedRepo{{Name: "api", Path: "/src/web/"}} }, "project's own checkout"},
		{"linked repo slug", func(c *Config) {
			c.Projects[0].LinkedRepos = []LinkedRepo{{Name: "api", Path: "/a", Repo: "https://github.com/a/b"}}
		}, "owner/name"},
	}
	if err := base().Validate(); err != nil {
		t.Fatalf("base config invalid: %v", err)
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := base()
			tc.mut(c)
			err := c.Validate()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestSandboxPortPair(t *testing.T) {
	for spec, want := range map[string][2]int{"3000": {3000, 3000}, "8080:80": {8080, 80}, " 5173 ": {5173, 5173}} {
		h, c, ok := SandboxPortPair(spec)
		if !ok || h != want[0] || c != want[1] {
			t.Errorf("%q = %d:%d %v", spec, h, c, ok)
		}
	}
	for _, bad := range []string{"", "x", "1:2:3", "0", "65536", "127.0.0.1:80"} {
		if _, _, ok := SandboxPortPair(bad); ok {
			t.Errorf("%q accepted", bad)
		}
	}
}
