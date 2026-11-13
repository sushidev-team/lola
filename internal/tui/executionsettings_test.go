package tui

import (
	"slices"
	"testing"

	"github.com/sushidev-team/lola/internal/config"
)

func TestFormGoalModePrerequisitesAndPersistence(t *testing.T) {
	f, path := newFormOn(t, []config.Project{{Name: "web", Path: "/tmp/web"}}, "web")
	f.cfg.Sandbox = config.SandboxConfig{LolaBin: "/tmp/lola-linux", Image: "toolchain:stable"}
	if err := f.cfg.Save(path); err != nil {
		t.Fatal(err)
	}
	f.toggleBool(fGoalMode)
	if f.poll.GoalMode || f.loadErr == "" {
		t.Fatal("enabled without prerequisites")
	}
	f.poll.Sandbox = "docker"
	f.toggleBool(fGoalMode)
	if f.poll.GoalMode {
		t.Fatal("enabled without Linear placement")
	}
	f.poll.TeamID, f.poll.ProjectID = "team", "project"
	f.toggleBool(fGoalMode)
	if !f.poll.GoalMode || f.loadErr != "" {
		t.Fatal("did not enable with prerequisites")
	}
	*f.textBuf(fSandboxImage) = "toolchain:stable"
	if _, ev := f.save(); ev != formSaved {
		t.Fatalf("save failed: %v", f.errs)
	}
	got, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	p := got.ProjectByName("web")
	if !p.GoalMode || p.Sandbox != "docker" || p.SandboxImage != "toolchain:stable" {
		t.Fatalf("settings = %+v", p)
	}
	// A now-invalid setup must still allow opting out.
	f.poll.Sandbox, f.poll.TeamID = "none", ""
	f.toggleBool(fGoalMode)
	if f.poll.GoalMode {
		t.Fatal("cannot disable goal mode")
	}
}

func TestFormExecutionSettingsOwnedByProjectOrStack(t *testing.T) {
	projects := []config.Project{{Name: "nori", Kind: config.ProjectKindStack}, {Name: "web", Path: "/tmp/web", Stack: "nori"}}
	for _, name := range []string{"nori", "web"} {
		f, _ := newFormOn(t, projects, name)
		for _, field := range []fieldID{fSandbox, fSandboxImage, fGoalMode} {
			if got := slices.Contains(f.fields(), field); got != (name == "nori") {
				t.Fatalf("%s field %v exposed=%v", name, field, got)
			}
		}
	}
}
