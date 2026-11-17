package config

import (
	"path/filepath"
	"testing"
)

func TestGoalModeIsExplicitAndRoundTrips(t *testing.T) {
	c := &Config{Sandbox: SandboxConfig{Image: "agent", LolaBin: "/private/lola-linux"}, Projects: []Project{{Name: "api", Path: "/private/api", Sandbox: SandboxDocker, GoalMode: true, TeamID: "team", ProjectID: "linear-project"}, {Name: "other", Path: "/private/other"}}}
	if errs := c.validateGoalMode(); len(errs) > 0 {
		t.Fatal(errs)
	}
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := c.Save(path); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.Projects[0].GoalMode || loaded.Projects[1].GoalMode {
		t.Fatal("goal mode lost or inherited")
	}
	for _, mutate := range []func(*Project){func(p *Project) { p.Sandbox = "" }, func(p *Project) { p.ProjectID = "" }, func(p *Project) { p.Stack = "parent" }} {
		p := c.Projects[0]
		mutate(&p)
		candidate := &Config{Projects: []Project{p}}
		if len(candidate.validateGoalMode()) == 0 {
			t.Fatal("unsafe goal mode accepted")
		}
	}
}
