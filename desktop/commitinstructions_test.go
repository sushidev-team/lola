package main

import (
	"context"
	"strings"
	"testing"

	"github.com/sushidev-team/lola/internal/config"
)

func TestBothCommitHelpersUseUserInstructions(t *testing.T) {
	for _, automatic := range []bool{false, true} {
		for _, override := range []string{"", "Use ticket prefixes for this repository."} {
			svc, _ := aiService(t)
			configSvc := &ConfigService{}
			settings, err := configSvc.GetSettings()
			if err != nil {
				t.Fatal(err)
			}
			workspace := "Use Conventional Commits and a short body."
			settings.CommitInstructions = &workspace
			if err := configSvc.SaveSettings(settings); err != nil {
				t.Fatal(err)
			}
			project, err := configSvc.GetProject("demo")
			if err != nil {
				t.Fatal(err)
			}
			project.CommitInstructions = &override
			if err := configSvc.SaveProject(project); err != nil {
				t.Fatal(err)
			}
			instructions := make(chan string, 1)
			svc.draftCommit = func(_ context.Context, instruction, input string) (string, error) {
				instructions <- instruction + "\n" + input
				if automatic {
					return `{"groups":[{"subject":"feat: add feature","paths":["new.txt"]}]}`, nil
				}
				return `{"subject":"feat: add feature"}`, nil
			}
			if automatic {
				p, err := svc.PrepareAICommit("demo", "")
				if err != nil {
					t.Fatal(err)
				}
				job, err := svc.StartAICommit("demo", "", p.Token, false)
				if err != nil {
					t.Fatal(err)
				}
				if status := aiWait(t, svc, job.ID); status.Phase != "complete" {
					t.Fatal(status)
				}
			} else {
				if _, err := svc.Stage("demo", "", []string{"new.txt"}); err != nil {
					t.Fatal(err)
				}
				if _, err := svc.DraftCommitMessage("demo", "", ""); err != nil {
					t.Fatal(err)
				}
			}
			got := <-instructions
			want := workspace
			if override != "" {
				want = override
			}
			if !strings.Contains(got, want) || !strings.Contains(got, "Recent non-merge commit examples") {
				t.Fatal("missing user preferences or history:", got)
			}
			if override != "" && strings.Contains(got, workspace) {
				t.Fatal("workspace preference leaked into project override")
			}
		}
	}
}

func TestCommitInstructionsSettingsRoundTrip(t *testing.T) {
	path := writeTestConfig(t, minimalConfig+"commit_instructions = \"Use Conventional Commits.\"\n[[project]]\nname = \"demo\"\npath = \"/repo\"\n")
	svc := &ConfigService{}
	settings, err := svc.GetSettings()
	if err != nil {
		t.Fatal(err)
	}
	if settings.CommitInstructions == nil || *settings.CommitInstructions != "Use Conventional Commits." {
		t.Fatal(settings.CommitInstructions)
	}
	project, err := svc.GetProject("demo")
	if err != nil {
		t.Fatal(err)
	}
	if project.CommitInstructions == nil || *project.CommitInstructions != "" {
		t.Fatal("default was copied into project")
	}
	read := func(want string) {
		t.Helper()
		cfg, err := config.Load(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := cfg.CommitInstructionsFor("demo"); got != want {
			t.Fatalf("got %q, want %q", got, want)
		}
	}
	read("Use Conventional Commits.")
	override := "Use ticket prefixes.\nKeep bodies short."
	project.CommitInstructions = &override
	if err := svc.SaveProject(project); err != nil {
		t.Fatal(err)
	}
	read(override)
	workspace := "Use fix: or feat:."
	settings.CommitInstructions = &workspace
	if err := svc.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	read(override)
	// Older clients omit the new field; neither kind of save can erase it.
	settings.CommitInstructions, project.CommitInstructions = nil, nil
	if err := svc.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	if err := svc.SaveProject(project); err != nil {
		t.Fatal(err)
	}
	read(override)
	empty := ""
	project.CommitInstructions = &empty
	if err := svc.SaveProject(project); err != nil {
		t.Fatal(err)
	}
	read(workspace)
	tooLong := strings.Repeat("x", 4001)
	settings.CommitInstructions = &tooLong
	if err := svc.SaveSettings(settings); err == nil {
		t.Fatal("accepted unbounded preferences")
	}
	read(workspace)
}
