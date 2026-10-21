package main

import (
	"os"
	"reflect"
	"testing"

	"github.com/sushidev-team/lola/internal/config"
)

func TestProjectExecutionSettingsRoundTrip(t *testing.T) {
	for _, kind := range []string{"", config.ProjectKindStack} {
		t.Run("kind="+kind, func(t *testing.T) {
			writeTestConfig(t, minimalConfig+"\n[sandbox]\nlola_bin = \"/tmp/lola-linux\"\nimage = \"toolchain:stable\"\n")
			s := &ConfigService{}
			dto, err := s.GetProject("")
			if err != nil {
				t.Fatal(err)
			}
			dto.Name, dto.Kind, dto.Path = "nori", kind, "/tmp/nori"
			if kind != "" {
				dto.Path = ""
			}
			dto.TeamID, dto.ProjectID = "team", "project"
			dto.Execution = &ProjectExecutionDTO{GoalMode: true, Sandbox: " docker ", Image: " toolchain:stable "}
			if err := s.SaveProject(dto); err != nil {
				t.Fatal(err)
			}
			got, err := s.GetProject("nori")
			if err != nil {
				t.Fatal(err)
			}
			want := &ProjectExecutionDTO{GoalMode: true, Sandbox: "docker", Image: "toolchain:stable"}
			if !reflect.DeepEqual(got.Execution, want) {
				t.Fatalf("execution = %+v", got.Execution)
			}
			got.Execution = nil // Older clients cannot wipe settings they never received.
			if err := s.SaveProject(got); err != nil {
				t.Fatal(err)
			}
			again, _ := s.GetProject("nori")
			if !reflect.DeepEqual(again.Execution, want) {
				t.Fatalf("older client changed execution: %+v", again.Execution)
			}
		})
	}
}

func TestProjectExecutionSettingsRejectInvalidWithoutWriting(t *testing.T) {
	for _, missing := range []string{"sandbox", "team", "project"} {
		t.Run(missing, func(t *testing.T) {
			path := writeTestConfig(t, minimalConfig+"\n[sandbox]\nlola_bin = \"/tmp/lola-linux\"\nimage = \"toolchain:stable\"\n")
			before, _ := os.ReadFile(path)
			s := &ConfigService{}
			dto, _ := s.GetProject("")
			dto.Name, dto.Path, dto.TeamID, dto.ProjectID = "nori", "/tmp/nori", "team", "project"
			dto.Execution = &ProjectExecutionDTO{GoalMode: true, Sandbox: "docker"}
			switch missing {
			case "sandbox":
				dto.Execution.Sandbox = "none"
			case "team":
				dto.TeamID = ""
			case "project":
				dto.ProjectID = ""
			}
			if err := s.SaveProject(dto); err == nil {
				t.Fatal("accepted invalid goal mode")
			}
			after, _ := os.ReadFile(path)
			if string(before) != string(after) {
				t.Fatal("failed save changed configuration")
			}
		})
	}
}

func TestMemberCannotOverrideStackExecutionSettings(t *testing.T) {
	writeTestConfig(t, twoProjectConfig)
	s := &ConfigService{}
	if _, err := s.CreateStack("nori"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetProjectStack("okane", "nori"); err != nil {
		t.Fatal(err)
	}
	dto, err := s.GetProject("okane")
	if err != nil {
		t.Fatal(err)
	}
	before := *dto.Execution
	dto.Execution = &ProjectExecutionDTO{GoalMode: true, Sandbox: "docker", Image: "unexpected"}
	if err := s.SaveProject(dto); err != nil {
		t.Fatal(err)
	}
	after, _ := s.GetProject("okane")
	if *after.Execution != before {
		t.Fatalf("member changed execution: %+v", after.Execution)
	}
}
